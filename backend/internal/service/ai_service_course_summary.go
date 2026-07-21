package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
)

// ai_service_course_summary.go 是 Phase D 课程级总结能力的编排层(和 ai_service_advice.go /
// ai_service_user_report.go 平行):worker job(runCourseSummaryJob)、admin 触发入口
// (EnqueueCourseSummary)、客户端读取(GetCourseSummary)。agent 包拥有决策逻辑(ReAct loop
// + course summary 工具集 + CourseSummaryAgent);本文件是 GLUE:解析 job → 构造
// CourseSummaryAgent → 跑 → 存 AICourseSummary。
//
// 和 advice 的关键差异:课程总结是 course-unique 的纯内容总结(不含个人 mastery),所以
//   - job 不需要 user_id(job.UserID 留 nil,admin 触发时也不绑定具体 user);
//   - 工具集不含 mastery 类工具(NewCourseSummaryToolbox 只注册 get_course_episodes +
//     get_episode_summary);
//   - pre-seed 读 episode 列表 + headline(不是 mastery 摘要)。

// course summary status 常量(和 adviceStatus* / userReportStatus* 平行)。admin handler 用
// status 决定响应形状。
const (
	courseSummaryStatusReady       = "ready"
	courseSummaryStatusGenerating  = "generating"
	courseSummaryStatusUnavailable = "unavailable"
)

// runCourseSummaryJob 是 course_summary 生成路径。流程(仿 runAdviceJob,但 pre-seed 和存储
// 不同):
//  1. 校验 AI resolver + job.CourseID(课程总结按 course,必须有课程)。
//  2. 反查 course 拿标题/科目(供 prompt 引用)。
//  3. 构造 CourseSummaryAgent(agent + course summary toolbox + deps)。
//  4. 跑 Generate → 存 AICourseSummary(UpsertCourseSummary 替换旧记录)。
//  5. 记录 ai_run(供 admin 观测)。
//
// 和 runAdviceJob 的差异:job.UserID 不要求(course-unique);存储按 course_id 不是
// (user, scope, scope_id);pre-seed 是 episode 列表 + headline 不是 mastery 摘要。
func (s *aiService) runCourseSummaryJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "AI not configured (no resolver)", nil)
		return
	}
	if job.CourseID == nil || *job.CourseID == 0 {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "course_summary job missing course_id", nil)
		return
	}
	courseID := *job.CourseID

	// 反查课程拿标题/科目(供 prompt 引用)。课程不存在直接 fail。
	course, err := s.courseRepo.FindByID(courseID)
	if err != nil {
		s.failJob(job, "course_summary job: load course: "+err.Error())
		return
	}
	if course == nil {
		s.failJob(job, fmt.Sprintf("course_summary job: course %d not found", courseID))
		return
	}

	req := agent.CourseSummaryRequest{
		CourseID:    course.ID,
		CourseTitle: course.Title,
		Subject:     course.Subject.Label,
	}
	// job.UserID 仅用于 admin 可观测(谁触发的);课程总结本身不含个人维度。
	if job.UserID != nil {
		req.UserID = *job.UserID
	}

	// chat provider 是必须的;course summary 不用 embedder(无向量检索)。
	llm, err := s.resolver.ResolveChatByPurpose("course_summary")
	if err != nil {
		s.failJob(job, "resolve chat provider: "+err.Error())
		return
	}
	modelName := s.resolver.ChatModelNameByPurpose("course_summary")

	// 构造 agent graph:deps adapter → course summary toolbox → agent。
	// deps 和 advice 共用 agentToolDeps(它实现了所有 ToolDeps 方法);course summary
	// 路径不需要 userRepo(不调 list_user_courses),传 nil 即可。
	deps := &agentToolDeps{
		contentRepo: s.contentRepo,
		episodeRepo: s.episodeRepo,
		courseRepo:  s.courseRepo,
	}
	toolbox := agent.NewCourseSummaryToolbox(deps, course.ID)
	// MaxSteps 8(比 advice 的 10 低,比 quiz 的 6 高):课程总结的典型轨迹是 pre-seed 已
	// 喂了所有 episode + headline,agent 只需调 1-3 次 get_episode_summary 深入查看关键
	// episode,然后写总结。8 步是"够用但不烧钱"的折中。MaxTokens 给 3000:课程总结比
	// advice 长一些(要串起整门课的脉络,300-700 字),3000 token 绰绰有余。
	genAgent := agent.NewAgent(llm, modelName, toolbox, agent.AgentOpts{MaxSteps: 8, MaxTokens: 3000})
	summarizer := agent.NewCourseSummaryAgent(genAgent, deps)

	start := time.Now()
	res, err := summarizer.Generate(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		// 仍记录尝试(部分 trace 对调试生成失败有价值)。
		if res != nil {
			s.recordCourseSummaryRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "fail", err.Error(), "", res.SystemPrompt, res.UserPrompt)
		}
		s.failJob(job, "course summary generation: "+err.Error())
		return
	}

	// 存课程总结(UpsertCourseSummary 替换旧记录,语义=当前课程导览)。
	// 生成时快照当前"已总结课时数"——给陈旧检测用(下次读时跟当前数对比)。
	episodeCountAtGen, _ := s.contentRepo.CountEpisodesWithSummaryByCourse(course.ID)
	summary := &model.AICourseSummary{
		CourseID:          course.ID,
		SummaryText:       res.SummaryText,
		ModelUsed:         modelName,
		GeneratedAt:       time.Now(),
		EpisodeCountAtGen: int(episodeCountAtGen),
	}
	if err := s.contentRepo.UpsertCourseSummary(summary); err != nil {
		s.recordCourseSummaryRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "fail", "persist: "+err.Error(), res.SummaryText, res.SystemPrompt, res.UserPrompt)
		s.failJob(job, "persist course summary: "+err.Error())
		return
	}

	s.recordCourseSummaryRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "pass", "", res.SummaryText, res.SystemPrompt, res.UserPrompt)
	s.contentRepo.UpdateJobStatus(job.ID, "done", "", nil)
}

// recordCourseSummaryRun 写 ai_run(供 admin 观测 course summary 生成)。和 recordAdviceRun
// 平行,但 capability="course_summary",response_text 存总结文本预览(截断,避免 ai_run
// 行过大)。systemPrompt/userPrompt 是本次发给 LLM 的开场 prompt,写进 ai_runs.system_prompt_text /
// user_prompt_text 供 admin "查看回放"。
func (s *aiService) recordCourseSummaryRun(jobID uint, modelName string, trace []agent.TraceStep, usage ai.Usage, turns int, elapsed time.Duration, result, note, summaryText, systemPrompt, userPrompt string) {
	preview := truncateCourseSummaryPreview(summaryText)
	s.contentRepo.CreateRun(&model.AIRun{
		JobID:            jobID,
		Capability:       "course_summary",
		InputJSON:        fmt.Sprintf(`{"job_id":%d,"turns":%d,"steps":%d}`, jobID, turns, len(trace)),
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelUsed:        modelName,
		ResponseText:     preview,
		TraceJSON:        agent.TraceJSON(trace),
		SelfCheckResult:  result, // 复用字段存 course summary 的 pass/fail(无 self-check,这里记生成结果)
		SelfCheckNote:    note,
		DurationMs:       int(elapsed.Milliseconds()),
		// 记下这次发给 LLM 的完整 system+user prompt,供 admin "查看回放"还原本次 prompt。
		SystemPromptText: systemPrompt,
		UserPromptText:   userPrompt,
	})
}

// truncateCourseSummaryPreview 把课程总结文本截到 500 字(比 advice 的 400 字略长,因为课程
// 总结本就更长)并包成 JSON 预览(ai_run.response_text)。
func truncateCourseSummaryPreview(summaryText string) string {
	if len([]rune(summaryText)) > 500 {
		summaryText = string([]rune(summaryText)[:500]) + "…"
	}
	preview, _ := json.Marshal(map[string]any{
		"course_summary_preview": summaryText,
	})
	return string(preview)
}

// EnqueueCourseSummary 是 admin 触发的"为某课程生成课程级总结"入口。流程:
//   - AI off / 课程不存在 → "unavailable";
//   - 已有在途 course_summary job → "generating"(避免重复入队);
//   - 否则入队低优先级 course_summary job,返回 "generating"。
//
// 和 GetOrEnqueueAdvice 的差异:course summary 不 lazy 生成(客户端只读已生成的,不触发——
// 因为总结是 course-unique 共享的,不应让任一学生触发生成);admin 显式触发。
func (s *aiService) EnqueueCourseSummary(courseID uint) (string, error) {
	if s.resolver == nil {
		return courseSummaryStatusUnavailable, nil
	}
	// 课程存在性校验(顺便反查不会用上,但给一个清晰的 early error)。
	course, err := s.courseRepo.FindByID(courseID)
	if err != nil {
		return courseSummaryStatusUnavailable, err
	}
	if course == nil {
		return courseSummaryStatusUnavailable, fmt.Errorf("course %d not found", courseID)
	}
	// 在途 job 就不重复入队(admin 反复点生成按钮时)。
	if s.HasPendingCourseSummaryJob(courseID) {
		return courseSummaryStatusGenerating, nil
	}
	if err := s.enqueueCourseSummaryJob(courseID); err != nil {
		return courseSummaryStatusUnavailable, err
	}
	return courseSummaryStatusGenerating, nil
}

// enqueueCourseSummaryJob 构造并持久化一条 course_summary job。course-unique,不带 user_id
// (course summary 不针对个人)。EpisodeID 留 nil(course 级 job,不绑定具体 episode)。
func (s *aiService) enqueueCourseSummaryJob(courseID uint) error {
	courseIDCopy := courseID
	job := &model.AIJob{
		JobType:  "course_summary",
		CourseID: &courseIDCopy,
		Status:   "queued",
		Priority: priorityCourseSummary,
	}
	if err := s.contentRepo.CreateJob(job); err != nil {
		log.Printf("AI: failed to enqueue course_summary job for course %d: %v", courseID, err)
		return err
	}
	return nil
}

// HasPendingCourseSummaryJob 报告该课程是否有在途 course_summary job(queued/processing)。
// 用于 admin handler 区分"正在生成"vs"无总结未生成",避免重复入队。
func (s *aiService) HasPendingCourseSummaryJob(courseID uint) bool {
	var count int64
	s.db.Model(&model.AIJob{}).
		Where("job_type = ? AND course_id = ? AND status IN ?",
			"course_summary", courseID, []string{"queued", "processing"}).
		Count(&count)
	return count > 0
}

// GetCourseSummary 委托 contentRepo:取某课程的最新课程总结(unique on course_id)。无总结
// 返回 nil,客户端 handler 据此返回 404。课程总结是 course-unique 的纯内容总结。
func (s *aiService) GetCourseSummary(courseID uint) (*model.AICourseSummary, error) {
	return s.contentRepo.GetCourseSummary(courseID)
}
