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

// ai_service_advice.go 是 Phase C advice 能力的编排层(和 ai_service_quiz.go 平行):
// worker job(runAdviceJob)、客户端 lazy 生成(GetOrEnqueueAdvice)、submit-all 后的
// 链式触发(EnqueueAdviceForEpisode)。agent 包拥有决策逻辑(ReAct loop + advice 工具
// + AdviceAgent);本文件是 GLUE:解析 job → 构造 AdviceAgent → 跑 → 存 StudyAdvice。

// advice status 常量(和 quizStatus* 平行)。handler 用 status 决定响应形状。
const (
	adviceStatusReady       = "ready"
	adviceStatusGenerating  = "generating"
	adviceStatusUnavailable = "unavailable"
)

// runAdviceJob 是 advice 生成路径。流程(仿 runQuizJob,但更简单——无 self-check、无
// 题目解析):
//  1. 校验 AI resolver + job.UserID(advice 是 per-user 的,必须有用户)。
//  2. 从 job 解析 scope/scopeID(存在 job 的 PayloadJSON 里,因为 AIJob 表没有专门的
//     scope 列;用 episode_id 字段做 episode 级的便捷访问)。
//  3. 构造 AdviceAgent(agent + advice toolbox + memory + deps)。
//  4. 跑 Generate → 存 StudyAdvice(UpsertAdvice 替换旧记录)。
//  5. 记录 ai_run(供 admin 观测)。
//
// scope/scopeID 的编码约定(存 PayloadJSON,因为 AIJob schema 是 episode-centric 的):
//   - episode 级:job.EpisodeID 就是 scopeID,scope="episode"。
//   - course 级:job.CourseID 就是 scopeID,scope="course"。
//   - subject 级:scopeID 存在 PayloadJSON.subject_id 里。
// 这样 episode/course 级可以直接用 job 已有字段,subject 级才需要 PayloadJSON。
func (s *aiService) runAdviceJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "AI not configured (no resolver)", nil)
		return
	}
	if job.UserID == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "advice job missing user_id", nil)
		return
	}
	userID := *job.UserID

	// 解析 scope/scopeID + 收集 prompt 元数据。
	req, err := s.buildAdviceRequest(job)
	if err != nil {
		s.failJob(job, "advice job: "+err.Error())
		return
	}

	// chat provider 是必须的;embedder advice 不用(无向量检索)。
	llm, err := s.resolver.ResolveChat()
	if err != nil {
		s.failJob(job, "resolve chat provider: "+err.Error())
		return
	}
	modelName := s.resolver.ChatModelName()

	// 构造 agent graph:deps adapter(带 userRepo)→ memory → advice toolbox → agent。
	deps := &agentToolDeps{
		contentRepo: s.contentRepo,
		episodeRepo: s.episodeRepo,
		courseRepo:  s.courseRepo,
		userRepo:    s.userRepo,
	}
	memory := agent.NewMemoryStore(s.contentRepo)
	toolbox := agent.NewAdviceToolbox(deps, memory, userID, req.CourseID, req.EpisodeID, req.SubjectID)
	// MaxSteps 调到 10(比 quiz 的 6 高):跨课程/科目聚合数据量大,agent 可能要多次
	// 调 get_course_mastery/get_subject_mastery + get_episode_summary 才能写全。每步
	// 重发完整对话(token 成本线性增长),10 步是"够用但不烧钱"的折中。
	// MaxTokens 给 2500:自然语言建议 200-500 字,绰绰有余;不像 quiz 要吐 JSON 数组。
	genAgent := agent.NewAgent(llm, modelName, toolbox, agent.AgentOpts{MaxSteps: 10, MaxTokens: 2500})
	adviser := agent.NewAdviceAgent(genAgent, memory, deps)

	start := time.Now()
	res, err := adviser.Generate(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		// 仍记录尝试(部分 trace 对调试生成失败有价值)。
		if res != nil {
			s.recordAdviceRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "fail", err.Error(), "")
		}
		s.failJob(job, "advice generation: "+err.Error())
		return
	}

	// 存 advice(UpsertAdvice 替换旧记录,语义=当前建议快照)。
	advice := &model.StudyAdvice{
		UserID:              userID,
		Scope:               req.Scope,
		ScopeID:             req.ScopeID,
		AdviceText:          res.AdviceText,
		MasterySnapshotJSON: agent.MarshalMasterySnapshot(res.MasterySnapshot),
		ModelUsed:           modelName,
		GeneratedAt:         time.Now(),
	}
	if err := s.contentRepo.UpsertAdvice(advice); err != nil {
		s.recordAdviceRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "fail", "persist: "+err.Error(), res.AdviceText)
		s.failJob(job, "persist advice: "+err.Error())
		return
	}

	s.recordAdviceRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "pass", "", res.AdviceText)
	s.contentRepo.UpdateJobStatus(job.ID, "done", "", nil)
}

// buildAdviceRequest 从 job 解析出 AdviceRequest。scope 编码见 runAdviceJob 的注释。
// 同时填充 ScopeTitle/Subject 等元数据(给 prompt 省工具调用)。
//
// scope 的判定:job.PayloadJSON 带 scope 字段时以它为准(支持三种 scope);否则按
// 默认"episode 级"处理(用 job.EpisodeID)。这让 episode 级 advice job 不需要 PayloadJSON。
func (s *aiService) buildAdviceRequest(job *model.AIJob) (agent.AdviceRequest, error) {
	userID := *job.UserID
	// 默认 episode 级。
	scope := agent.ScopeEpisode
	scopeID := job.EpisodeID
	subjectID := uint(0)
	if job.PayloadJSON != "" {
		var p struct {
			Scope     string `json:"scope"`
			ScopeID   uint   `json:"scope_id"`
			SubjectID uint   `json:"subject_id"`
		}
		if err := json.Unmarshal([]byte(job.PayloadJSON), &p); err == nil {
			if p.Scope != "" {
				scope = p.Scope
			}
			if p.ScopeID != 0 {
				scopeID = p.ScopeID
			}
			subjectID = p.SubjectID
		}
	}

	req := agent.AdviceRequest{
		UserID:    userID,
		Scope:     scope,
		ScopeID:   scopeID,
		EpisodeID: job.EpisodeID,
		CourseID:  job.CourseID,
		SubjectID: subjectID,
	}

	// 按 scope 补元数据(标题、科目名),让 prompt 有素材。
	switch scope {
	case agent.ScopeEpisode:
		ep, err := s.episodeRepo.FindByID(scopeID)
		if err != nil {
			return req, fmt.Errorf("load episode %d: %w", scopeID, err)
		}
		if ep == nil {
			return req, fmt.Errorf("episode %d not found", scopeID)
		}
		req.EpisodeID = ep.ID
		req.CourseID = ep.CourseID
		req.ScopeTitle = ep.Title
		// 顺便反查 course 拿 subject_id + 科目名(供 subject 工具 + prompt)。
		if course, cerr := s.courseRepo.FindByID(ep.CourseID); cerr == nil && course != nil {
			req.SubjectID = course.SubjectID
			req.Subject = course.Subject.Label
		}
	case agent.ScopeCourse:
		course, err := s.courseRepo.FindByID(scopeID)
		if err != nil {
			return req, fmt.Errorf("load course %d: %w", scopeID, err)
		}
		if course == nil {
			return req, fmt.Errorf("course %d not found", scopeID)
		}
		req.CourseID = course.ID
		req.SubjectID = course.SubjectID
		req.Subject = course.Subject.Label
		req.ScopeTitle = course.Title
	case agent.ScopeSubject:
		// 科目标题由 handler/job 入队时放进 PayloadJSON.subject_title(或留空,prompt
		// 会用"这个科目"占位)。subject_id 必须有(scopeID 即是)。
		req.SubjectID = scopeID
		if subjectID != 0 {
			req.SubjectID = subjectID
		}
		// scopeID 就是 subject_id,确保一致。
		req.SubjectID = scopeID
	default:
		return req, fmt.Errorf("unknown advice scope: %s", scope)
	}
	return req, nil
}

// recordAdviceRun 写 ai_run(供 admin 观测 advice 生成)。和 recordQuizRun 平行,但
// capability="advice",response_text 存 advice 文本预览(截断,避免 ai_run 行过大)。
func (s *aiService) recordAdviceRun(jobID uint, modelName string, trace []agent.TraceStep, usage ai.Usage, turns int, elapsed time.Duration, result, note, adviceText string) {
	preview := truncateAdvicePreview(adviceText)
	s.contentRepo.CreateRun(&model.AIRun{
		JobID:            jobID,
		Capability:       "advice",
		InputJSON:        fmt.Sprintf(`{"job_id":%d,"turns":%d,"steps":%d}`, jobID, turns, len(trace)),
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelUsed:        modelName,
		ResponseText:     preview,
		TraceJSON:        agent.TraceJSON(trace),
		SelfCheckResult:  result, // 复用字段存 advice 的 pass/fail(advice 无 self-check,这里记生成结果)
		SelfCheckNote:    note,
		DurationMs:       int(elapsed.Milliseconds()),
	})
}

// truncateAdvicePreview 把 advice 文本截到 400 字并包成 JSON 预览(ai_run.response_text)。
// 和 quiz 的 truncateForRun([]QuestionDraft, ...) 不同签名(Go 不支持重载),所以单独
// 命名。admin 前端按 capability="advice" 渲染 advice_preview 字段。
func truncateAdvicePreview(adviceText string) string {
	if len([]rune(adviceText)) > 400 {
		adviceText = string([]rune(adviceText)[:400]) + "…"
	}
	preview, _ := json.Marshal(map[string]any{
		"advice_preview": adviceText,
	})
	return string(preview)
}

// 为了让 recordAdviceRun 复用预览截断且不和 quiz 的 truncateForRun([]QuestionDraft,...)
// 冲突,recordAdviceRun 直接调 truncateAdvicePreview(纯文本入参)。

// GetOrEnqueueAdvice 是建议的 lazy 生成入口(同 GetOrEnqueueQuiz 的模式):
//   - 已有 advice → "ready" + advice
//   - 无 advice + 有在途 advice job → "generating"(避免重复入队)
//   - 无 advice + AI 配置好 → 入队低优先级 advice job,返回 "generating"
//   - AI off → "unavailable"
//
// scope/scopeID 决定 job 的 PayloadJSON 编码(见 buildAdviceRequest)。访问控制由
// handler 在调用前做(canAccessEpisode/canAccessCourse),本方法信任调用方已 gate。
func (s *aiService) GetOrEnqueueAdvice(userID uint, scope string, scopeID uint) (string, *model.StudyAdvice, error) {
	// 在途 job 优先(同 quiz):正在生成的 advice 即将替换当前(可能为空)的 advice,
	// 直接返回 generating 让客户端轮询。
	if s.hasPendingAdviceJob(userID, scope, scopeID) {
		return adviceStatusGenerating, nil, nil
	}
	advice, err := s.contentRepo.GetAdvice(userID, scope, scopeID)
	if err != nil {
		return adviceStatusUnavailable, nil, err
	}
	if advice != nil {
		return adviceStatusReady, advice, nil
	}
	// 无 advice。检查前置条件(AI 配置好)再入队。
	if s.resolver == nil {
		return adviceStatusUnavailable, nil, nil
	}
	if err := s.enqueueAdviceJob(userID, scope, scopeID); err != nil {
		return adviceStatusUnavailable, nil, err
	}
	return adviceStatusGenerating, nil, nil
}

// EnqueueAdviceForEpisode 是 submit-all 成功后的链式触发:异步入队 episode 级 advice
// job(低优先级)。幂等:已有在途 advice job 不重复入队(避免 submit 重试堆 job)。
// 失败只记日志,不阻断 submit-all 主流程(advice 是 nice-to-have,不是交卷的一部分)。
func (s *aiService) EnqueueAdviceForEpisode(userID, episodeID uint) error {
	// 在途 advice job 就不重复入队(submit 被重试 / 重复触发时)。
	if s.hasPendingAdviceJob(userID, agent.ScopeEpisode, episodeID) {
		return nil
	}
	return s.enqueueAdviceJob(userID, agent.ScopeEpisode, episodeID)
}

// enqueueAdviceJob 构造并持久化一条 advice job。scope/scopeID 编码进 PayloadJSON
// (episode/course 级也走 PayloadJSON,保持单一编码路径;buildAdviceRequest 会解码)。
// episode_id/course_id 字段也填上(让 admin job 列表能按 episode/course 过滤,且
// buildAdviceRequest 的默认 episode 级路径能用 job.EpisodeID)。
func (s *aiService) enqueueAdviceJob(userID uint, scope string, scopeID uint) error {
	// 先尝试解析 episodeID/courseID,让 AIJob 表的索引字段也准确(便于 admin 过滤 +
	// jobNameCache 解析标题)。不同 scope 的 ID 含义不同:
	var episodeID, courseID uint
	switch scope {
	case agent.ScopeEpisode:
		episodeID = scopeID
		if ep, err := s.episodeRepo.FindByID(scopeID); err == nil && ep != nil {
			courseID = ep.CourseID
		}
	case agent.ScopeCourse:
		courseID = scopeID
	case agent.ScopeSubject:
		// subject 级两者都 0(不属于具体 episode/course)。
	}
	payload, _ := json.Marshal(map[string]any{
		"scope":     scope,
		"scope_id":  scopeID,
		"subject_id": 0, // buildAdviceRequest 会从 course 反查填上
	})
	job := &model.AIJob{
		JobType:     "advice",
		EpisodeID:   episodeID,
		CourseID:    courseID,
		UserID:      &userID,
		Status:      "queued",
		Priority:    priorityAdvice,
		PayloadJSON: string(payload),
	}
	if err := s.contentRepo.CreateJob(job); err != nil {
		log.Printf("AI: failed to enqueue advice job (%s/%d) for user %d: %v", scope, scopeID, userID, err)
		return err
	}
	return nil
}

// hasPendingAdviceJob 报告该 (user, scope, scope_id) 是否有在途 advice job(queued/
// processing)。用于 lazy 生成 + 链式触发的去重,避免堆 job(同 quiz 的 hasPendingQuizJob)。
//
// 之前用 PayloadJSON 的 SQL LIKE 去重,但 LIKE '%"scope_id":1%' 会前缀误匹配
// scope_id=11/100/1234(数字边界不被 LIKE 识别),导致同 scope 不同 id 的 advice
// 被静默吞掉。现在改为:先按 (job_type, user_id, status) 查出该用户在途的 advice
// job(量级极小——每用户每 scope 至多一条),Go 层解码 PayloadJSON 精确比较 scope/
// scope_id。彻底消除前缀误判,代价是多一次内存遍历(行数 < 10,可忽略)。
func (s *aiService) hasPendingAdviceJob(userID uint, scope string, scopeID uint) bool {
	var jobs []model.AIJob
	s.db.Where("job_type = ? AND user_id = ? AND status IN ?",
		"advice", userID, []string{"queued", "processing"}).
		Find(&jobs)
	for _, j := range jobs {
		if j.PayloadJSON == "" {
			// 无 PayloadJSON 的 advice job 默认是 episode 级(buildAdviceRequest
			// 的回退路径),用 job.EpisodeID 比较。
			if scope == agent.ScopeEpisode && j.EpisodeID == scopeID {
				return true
			}
			continue
		}
		var p struct {
			Scope   string `json:"scope"`
			ScopeID uint   `json:"scope_id"`
		}
		if json.Unmarshal([]byte(j.PayloadJSON), &p) == nil && p.Scope == scope && p.ScopeID == scopeID {
			return true
		}
	}
	return false
}
