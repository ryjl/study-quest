package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
)

// ai_service_user_report.go 是 Phase E admin 用户学习报告的编排层(和
// ai_service_advice.go 平行)。agent 包拥有决策逻辑(UserStudyAgent + user_study 工具集);
// 本文件是 GLUE:解析 job → 预算该学生所有课程的 mastery 概要 → 构造 UserStudyAgent →
// 跑 → 存 UserStudyReport。
//
// 和 advice job 的差异:
//   - advice job 用 PayloadJSON 存 scope/scope_id;user_report job 直接用 job.UserID(报
//     告是 per-user 的,user_id 即是全部 scope)。
//   - advice 是给学生看的单点复习建议;user_report 是给 admin 看的跨课程画像报告。
//   - pre-seed:advice 读单个 scope 的 mastery;user_report 遍历用户所有课程,每课程算
//     平均 mastery + 最弱知识点。

// user_report status 常量(和 adviceStatus* 平行)。admin handler 用 status 决定响应形状。
const (
	userReportStatusReady       = "ready"
	userReportStatusGenerating  = "generating"
	userReportStatusUnavailable = "unavailable"
)

// userStudyAdviceAdapter 把 contentRepo 的 GetAdvice 桥接到 agent 包的
// userStudyAdviceRepo 接口。agent 包不该 import repo,所以这个 adapter 留在 service 层。
// 空实现 contentRepo 字段即可满足接口;nil contentRepo 时工具回退"无建议"。
type userStudyAdviceAdapter struct {
	repo interface {
		GetAdvice(userID uint, scope string, scopeID uint) (*model.StudyAdvice, error)
	}
}

func (a *userStudyAdviceAdapter) GetUserAdvice(userID uint, scope string, scopeID uint) (*model.StudyAdvice, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	return a.repo.GetAdvice(userID, scope, scopeID)
}

// runUserReportJob 是 user_report 生成路径。流程(仿 runAdviceJob):
//  1. 校验 AI resolver + job.UserID(报告是 per-user 的,必须有用户)。
//  2. 取用户昵称(报告里称呼该学生)。
//  3. 预算该学生所有课程的 mastery 概要(每课程平均 mastery + 最弱知识点)—— pre-seed。
//  4. 构造 UserStudyAgent(user_study toolbox + memory + advice adapter)。
//  5. 跑 Generate → 存 UserStudyReport(UpsertUserStudyReport 替换旧记录)。
//  6. 记录 ai_run(供 admin 观测)。
func (s *aiService) runUserReportJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "AI not configured (no resolver)", nil)
		return
	}
	if job.UserID == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "user_report job missing user_id", nil)
		return
	}
	userID := *job.UserID

	// 取昵称:报告里"小明同学"比"#42"友好。userRepo 有就拿,没有回退 "(学生#id)"。
	nickname := fmt.Sprintf("(学生#%d)", userID)
	if s.userRepo != nil {
		// aiUserCourseLister 只暴露 GetAccessList;昵称走 db 直读(和 resolveJobNames
		// 里取 user nickname 同思路,不为此再加 userRepo 依赖)。
		var nick string
		if err := s.db.Model(&model.User{}).Select("nickname").Where("id = ?", userID).Take(&nick).Error; err == nil && nick != "" {
			nickname = nick
		}
	}

	// 预算 pre-seed:遍历该学生所有课程,每课程算平均 mastery + 最弱知识点。
	courses := s.buildUserStudyCourses(ctx, userID)

	// chat provider 必须;embedder user_report 不用(无向量检索)。
	llm, err := s.resolver.ResolveChatByPurpose("user_report")
	if err != nil {
		s.failJob(job, "resolve chat provider: "+err.Error())
		return
	}
	modelName := s.resolver.ChatModelNameByPurpose("user_report")

	// 构造 agent graph:deps adapter → memory → user_study toolbox(只绑 userID)→ agent。
	deps := &agentToolDeps{
		contentRepo: s.contentRepo,
		episodeRepo: s.episodeRepo,
		courseRepo:  s.courseRepo,
		userRepo:    s.userRepo,
	}
	memory := agent.NewMemoryStore(s.contentRepo)
	adviceAdapter := &userStudyAdviceAdapter{repo: s.contentRepo}
	toolbox := agent.NewUserStudyToolbox(deps, memory, adviceAdapter, userID)
	// MaxSteps 10(和 advice 同档):跨课程数据量大,agent 可能要多次调
	// get_course_mastery(每门课一次)+ get_user_advice + get_course_summary 才能写全。
	// MaxTokens 3000:报告 400-800 字,比 advice 略长(跨课程内容更多)。
	genAgent := agent.NewAgent(llm, modelName, toolbox, agent.AgentOpts{MaxSteps: 10, MaxTokens: 3000})
	studier := agent.NewUserStudyAgent(genAgent, memory, deps)

	start := time.Now()
	res, err := studier.Generate(ctx, agent.UserStudyRequest{
		UserID:       userID,
		UserNickname: nickname,
		Courses:      courses,
	})
	elapsed := time.Since(start)

	if err != nil {
		if res != nil {
			s.recordUserReportRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "fail", err.Error(), "", res.SystemPrompt, res.UserPrompt)
		}
		s.failJob(job, "user report generation: "+err.Error())
		return
	}

	// 存报告(UpsertUserStudyReport 替换旧记录,unique on user_id)。
	report := &model.UserStudyReport{
		UserID:      userID,
		ReportText:  res.ReportText,
		ModelUsed:   modelName,
		GeneratedAt: time.Now(),
	}
	if err := s.contentRepo.UpsertUserStudyReport(report); err != nil {
		s.recordUserReportRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "fail", "persist: "+err.Error(), res.ReportText, res.SystemPrompt, res.UserPrompt)
		s.failJob(job, "persist user report: "+err.Error())
		return
	}

	s.recordUserReportRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "pass", "", res.ReportText, res.SystemPrompt, res.UserPrompt)
	s.contentRepo.UpdateJobStatus(job.ID, "done", "", nil)
}

// buildUserStudyCourses 预算该学生所有课程的 mastery 概要(pre-seed 用)。流程:
//  1. 取该学生被授权的课程 id 列表(ListUserCourses → userRepo.GetAccessList)。
//  2. 对每门课取 CourseMasteries(MemoryStore 跨课程聚合,mastery ASC)。
//  3. 算平均 mastery + 取最弱知识点(mastery 最低那条 + chunk.text 线索)。
//  4. join 课程标题(courseRepo.FindByID)让 prompt 友好。
//
// 遍历每门课调一次 CourseMasteries 是 O(课程数) 次查询——课程数通常几个到十几个,可接受。
// 单个课程查询失败不阻塞整份报告(跳过该课程,记录空概要)。
func (s *aiService) buildUserStudyCourses(ctx context.Context, userID uint) []agent.UserStudyCourse {
	if s.userRepo == nil {
		return nil
	}
	courseIDs, err := s.userRepo.GetAccessList(userID)
	if err != nil || len(courseIDs) == 0 {
		return nil
	}
	memory := agent.NewMemoryStore(s.contentRepo)
	out := make([]agent.UserStudyCourse, 0, len(courseIDs))
	for _, cid := range courseIDs {
		entry := agent.UserStudyCourse{CourseID: cid}
		// 课程标题:让 prompt 友好。失败留空(agent 用"课程#id")。
		if course, cerr := s.courseRepo.FindByID(cid); cerr == nil && course != nil {
			entry.Title = course.Title
		}
		// 该课程 mastery 聚合(跨所有课时,mastery ASC 弱点优先)。
		rows, merr := memory.CourseMasteries(ctx, userID, cid)
		if merr != nil || len(rows) == 0 {
			// 无答题记录(新课或未做题):平均留 0,WeakestPoint 留空,prompt 会标"新课"。
			out = append(out, entry)
			continue
		}
		// 平均 mastery + 最弱知识点。rows 已按 mastery ASC(memory.go 的
		// sortMasteriesWorstFirst),所以 rows[0] 就是最弱知识点。
		var sum float64
		for _, r := range rows {
			sum += r.Mastery
		}
		entry.AverageMastery = sum / float64(len(rows))
		weakest := rows[0]
		// chunk 文本线索:帮 agent 用人话说"最弱的是通分"而非"chunk#37"。
		// 复用 loadChunksForMasteries 的思路,但那是 Toolbox 方法;这里直接读 chunk。
		// 为单个知识点读一次 chunks(按 episode)。weakest.EpisodeID 是该知识点所在课时。
		chunkHint := ""
		if chunks, cerr := s.contentRepo.ListChunks(weakest.EpisodeID, "subtitle"); cerr == nil {
			for _, ch := range chunks {
				if ch.ID == weakest.ChunkID {
					// 截断到 80 字,prompt 里够 agent 理解又不太长。
					hint := ch.Text
					if r := []rune(hint); len(r) > 80 {
						hint = string(r[:80]) + "…"
					}
					chunkHint = hint
					break
				}
			}
		}
		entry.WeakestPoint = fmt.Sprintf("mastery=%.2f (对%d错%d) 线索:%s",
			weakest.Mastery, weakest.CorrectCount, weakest.WrongCount, chunkHint)
		if chunkHint == "" {
			entry.WeakestPoint = fmt.Sprintf("mastery=%.2f (对%d错%d)", weakest.Mastery, weakest.CorrectCount, weakest.WrongCount)
		}
		out = append(out, entry)
	}
	return out
}

// recordUserReportRun 写 ai_run(供 admin 观测 user_report 生成)。和 recordAdviceRun
// 平行,capability="user_report",response_text 存报告文本预览(截断)。systemPrompt/
// userPrompt 是本次发给 LLM 的开场 prompt,写进 ai_runs.system_prompt_text /
// user_prompt_text 供 admin "查看回放"。
func (s *aiService) recordUserReportRun(jobID uint, modelName string, trace []agent.TraceStep, usage ai.Usage, turns int, elapsed time.Duration, result, note, reportText, systemPrompt, userPrompt string) {
	preview := truncateAdvicePreview(reportText)
	s.contentRepo.CreateRun(&model.AIRun{
		JobID:            jobID,
		Capability:       "user_report",
		InputJSON:        fmt.Sprintf(`{"job_id":%d,"turns":%d,"steps":%d}`, jobID, turns, len(trace)),
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelUsed:        modelName,
		ResponseText:     preview,
		TraceJSON:        agent.TraceJSON(trace),
		SelfCheckResult:  result, // 复用字段存 pass/fail(报告无 self-check)
		SelfCheckNote:    note,
		DurationMs:       int(elapsed.Milliseconds()),
		// 记下这次发给 LLM 的完整 system+user prompt,供 admin "查看回放"还原本次 prompt。
		SystemPromptText: systemPrompt,
		UserPromptText:   userPrompt,
	})
}

// EnqueueUserReport 是 admin 触发的"为某用户生成学习报告"入口(和 advice 的 lazy 生成
// 不同:user_report 纯 admin 触发,没有客户端 lazy 路径)。流程:
//   - AI 配置好 → 入队低优先级 user_report job(绑定 UserID),返回 "generating"。
//   - 已有在途 user_report job → "generating"(避免重复入队)。
//   - AI off → "unavailable"。
//
// 不在这里读已有报告(admin GET 端点自己读);这个方法是"触发重生成"语义。返回的 status
// 让 admin 前端决定是立刻轮询还是提示 unavailable。
func (s *aiService) EnqueueUserReport(userID uint) (string, error) {
	if s.resolver == nil {
		return userReportStatusUnavailable, nil
	}
	// 在途 job 去重(避免 admin 连点按钮堆 job)。
	if s.hasPendingUserReportJob(userID) {
		return userReportStatusGenerating, nil
	}
	if err := s.enqueueUserReportJob(userID); err != nil {
		return userReportStatusUnavailable, err
	}
	return userReportStatusGenerating, nil
}

// GetUserStudyReport 取该用户的最新学习报告(供 admin GET 端点)。无报告返回 (nil,nil)。
// status 由调用方(handler)结合"是否有在途 job"决定:有报告→ready;无报告+有在途 job
// →generating;无报告+无在途 job→空(前端显示"生成报告"按钮)。
func (s *aiService) GetUserStudyReport(userID uint) (*model.UserStudyReport, error) {
	return s.contentRepo.GetUserStudyReport(userID)
}

// HasPendingUserReportJob 暴露在途 job 查询给 handler(决定响应 generating 还是空)。
// handler 用来区分"无报告且未在生成"(显示生成按钮)vs"正在生成"(显示 spinner)。
func (s *aiService) HasPendingUserReportJob(userID uint) bool {
	return s.hasPendingUserReportJob(userID)
}

// enqueueUserReportJob 构造并持久化一条 user_report job。scope 就是 user_id(报告 per-
// user,不需要 PayloadJSON 存额外 scope;EpisodeID/CourseID 留 nil,因为不属于具体课时/
// 课程)。低优先级(和 advice/summary 同级):admin 触发,不在屏幕前干等(页面显示
// generating + 轮询),不该饿死 quiz(高优先级)。
func (s *aiService) enqueueUserReportJob(userID uint) error {
	job := &model.AIJob{
		JobType:  "user_report",
		UserID:   &userID,
		Status:   "queued",
		Priority: priorityUserReport,
	}
	if err := s.contentRepo.CreateJob(job); err != nil {
		log.Printf("AI: failed to enqueue user_report job for user %d: %v", userID, err)
		return err
	}
	return nil
}

// hasPendingUserReportJob 报告该用户是否有在途 user_report job(queued/processing)。用于
// admin 触发的去重(避免连点堆 job)+ handler 判断 generating 状态。比 advice 简单:
// user_report job 的 scope 就是 user_id(没有 scope/scope_id 编码),直接 WHERE user_id。
func (s *aiService) hasPendingUserReportJob(userID uint) bool {
	var count int64
	s.db.Model(&model.AIJob{}).
		Where("job_type = ? AND user_id = ? AND status IN ?", "user_report", userID, []string{"queued", "processing"}).
		Count(&count)
	return count > 0
}

// (truncateAdvicePreview 复用 advice.go 的实现,本文件无需重复定义或 import json。)
