package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
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
	// adviceStatusCooling:该 (user,scope,scope_id) 的 advice 生成连续失败达到熔断
	// 阈值。语义同 quizStatusCooling(避免反复入队烧 token)。前端据此提示并给重试入口。
	adviceStatusCooling = "cooling"
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
//
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
	llm, err := s.resolver.ResolveChatByPurpose("advice")
	if err != nil {
		s.failJob(job, "resolve chat provider: "+err.Error())
		return
	}
	modelName := s.resolver.ChatModelNameByPurpose("advice")

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
	genAgent := agent.NewAgent(llm, modelName, toolbox, agent.AgentOpts{MaxSteps: ai.MaxStepsAdvice, MaxTokens: ai.MaxTokensAdvice})
	adviser := agent.NewAdviceAgent(genAgent, memory, deps)

	start := time.Now()
	res, err := adviser.Generate(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		// 仍记录尝试(部分 trace 对调试生成失败有价值)。
		if res != nil {
			s.recordAdviceRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "fail", err.Error(), "", res.SystemPrompt, res.UserPrompt)
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
		GeneratedAt:         time.Now().UTC(),
	}
	if err := s.contentRepo.UpsertAdvice(advice); err != nil {
		s.recordAdviceRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "fail", "persist: "+err.Error(), res.AdviceText, res.SystemPrompt, res.UserPrompt)
		s.failJob(job, "persist advice: "+err.Error())
		return
	}

	s.recordAdviceRun(job.ID, modelName, res.Trace, res.Usage, res.Turns, elapsed, "pass", "", res.AdviceText, res.SystemPrompt, res.UserPrompt)
	s.contentRepo.UpdateJobStatus(job.ID, "done", "", nil)
}

// buildAdviceRequest 从 job 解析出 AdviceRequest。scope 编码见 runAdviceJob 的注释。
// 同时填充 ScopeTitle/Subject 等元数据(给 prompt 省工具调用)。
//
// scope 的判定:job.PayloadJSON 带 scope 字段时以它为准(支持三种 scope);否则按
// 默认"episode 级"处理(用 job.EpisodeID)。这让 episode 级 advice job 不需要 PayloadJSON。
func (s *aiService) buildAdviceRequest(job *model.AIJob) (agent.AdviceRequest, error) {
	userID := *job.UserID
	// 默认 episode 级。EpisodeID 现在是 *uint,subject 级 advice job 是 nil ——
	// ptrVal 把 nil 转成 0 作为默认 scopeID,buildAdviceRequest 后面会从 PayloadJSON
	// 覆盖 scope/scopeID(advice job 一定带 PayloadJSON),所以这里的默认值实际只对
	// 假设的"无 payload episode advice job"生效(enqueueAdviceJob 现在不创建这种)。
	scope := agent.ScopeEpisode
	scopeID := model.PtrVal(job.EpisodeID)
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
		EpisodeID: model.PtrVal(job.EpisodeID),
		CourseID:  model.PtrVal(job.CourseID),
		SubjectID: subjectID,
	}

	// 按 scope 补元数据(标题、科目名),让 prompt 有素材。同时在能拿到 course 的
	// scope(episode/course)注入 AdviceHint + TermDict;subject scope 没有 course,留空。
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
		// 顺便反查 course 拿 subject_id + 科目名(供 subject 工具 + prompt)+ advice hint + 术语字典。
		// courseRepo.FindByID 不 Preload Subject(避免 UpdateCourse 的 Save 误改关联),
		// 这里单独 s.db.First 查 subject。
		if course, cerr := s.courseRepo.FindByID(ep.CourseID); cerr == nil && course != nil {
			req.SubjectID = course.SubjectID
			var subj model.Subject
			if course.SubjectID != 0 {
				s.db.First(&subj, course.SubjectID)
			}
			req.Subject = subj.Label
			req.AdviceHint = course.EffectiveAdviceHint(subj)
			req.TermDict = course.EffectiveTermDict(subj)
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
		var subj model.Subject
		if course.SubjectID != 0 {
			s.db.First(&subj, course.SubjectID)
		}
		req.Subject = subj.Label
		req.ScopeTitle = course.Title
		req.AdviceHint = course.EffectiveAdviceHint(subj)
		req.TermDict = course.EffectiveTermDict(subj)
	case agent.ScopeSubject:
		// 科目标题由 handler/job 入队时放进 PayloadJSON.subject_title(或留空,prompt
		// 会用"这个科目"占位)。subject_id 必须有(scopeID 即是);payload 里的 subject_id
		// 若有也用(冗余兜底,enqueueAdviceJob 目前写死 0,所以实际走 scopeID)。
		req.SubjectID = scopeID
		if subjectID != 0 {
			req.SubjectID = subjectID
		}
		// subject scope 没有 course,直接读学科级 AIConfig 的 AdviceHint/TermDict。
		// EffectiveXxxHint 是 Course 方法(做课程>学科回退),subject scope 没课程层级,
		// 直接取 subject.AIConfig() 的对应字段。
		var subj model.Subject
		if s.db.First(&subj, req.SubjectID).Error == nil && subj.ID != 0 {
			req.Subject = subj.Label
			cfg := subj.AIConfig()
			req.AdviceHint = cfg.AdviceHint
			req.TermDict = strings.TrimSpace(cfg.TermDict)
		}
	default:
		return req, fmt.Errorf("unknown advice scope: %s", scope)
	}
	return req, nil
}

// recordAdviceRun 写 ai_run(供 admin 观测 advice 生成)。和 recordQuizRun 平行,但
// capability="advice",response_text 存 advice 文本预览(截断,避免 ai_run 行过大)。
// systemPrompt/userPrompt 是本次发给 LLM 的开场 prompt,写进 ai_runs.system_prompt_text /
// user_prompt_text 供 admin "查看回放"。
func (s *aiService) recordAdviceRun(jobID uint, modelName string, trace []agent.TraceStep, usage ai.Usage, turns int, elapsed time.Duration, result, note, adviceText, systemPrompt, userPrompt string) {
	preview := truncateAdvicePreview(adviceText)
	if err := s.contentRepo.CreateRun(&model.AIRun{
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
		// 记下这次发给 LLM 的完整 system+user prompt,供 admin "查看回放"还原本次 prompt。
		SystemPromptText: systemPrompt,
		UserPromptText:   userPrompt,
	}); err != nil {
		log.Printf("AI: recordAdviceRun failed for job %d: %v", jobID, err)
	}
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
	// 门控:advice 分析的是 mastery 弱点,没有答题记录(新学生或该 scope 尚未做题)
	// 时没有分析价值——直接返回 unavailable,不入队、不轮询、前端隐藏卡片。
	// 这避免首次进 AI 学习页就白烧一次 LLM 调用生成"建议先做题"的无意义文本。
	// 交卷(submit-all)后 EnqueueAdviceForEpisode 会链式触发重算,那时一定有记录了。
	if !s.hasAnyMasteryForScope(userID, scope, scopeID) {
		return adviceStatusUnavailable, nil, nil
	}
	// 熔断检查:该 (user,scope,scope_id) 的 advice 连续失败 ≥ 阈值,拒绝自动入队
	// (语义同 quiz 的冷却)。per-user 计数:A 学生 advice 失败不影响 B 学生。
	if s.consecutiveAdviceFailures(userID, scope, scopeID) >= maxConsecutiveFailures {
		return adviceStatusCooling, nil, nil
	}
	if err := s.enqueueAdviceJob(userID, scope, scopeID); err != nil {
		return adviceStatusUnavailable, nil, err
	}
	return adviceStatusGenerating, nil, nil
}

// hasAnyMasteryForScope 判断该学生在该 scope 下有没有任何答题掌握度记录。
// 用 KnowledgeMemory 表(交卷时写入)而非 Answer 表——advice 关心的是 mastery 是否
// 可分析,而非原始答题流水;且 KnowledgeMemory 已冗余 course_id,各 scope 都能直查
// 不用 JOIN。任一 scope 查询出错时保守返回 true(宁可多生成一次,不要错误隐藏)。
func (s *aiService) hasAnyMasteryForScope(userID uint, scope string, scopeID uint) bool {
	switch scope {
	case agent.ScopeEpisode:
		rows, err := s.contentRepo.GetMasteries(userID, scopeID)
		return err == nil && len(rows) > 0
	case agent.ScopeCourse:
		rows, err := s.contentRepo.GetCourseMasteries(userID, scopeID)
		return err == nil && len(rows) > 0
	case agent.ScopeSubject:
		rows, err := s.contentRepo.GetSubjectMasteries(userID, scopeID)
		return err == nil && len(rows) > 0
	}
	return true // 未知 scope 不阻断(保守)
}

// EnqueueAdviceForEpisode 是 submit-all 成功后的链式触发:异步入队 episode 级 advice
// job(低优先级)。幂等:已有在途 advice job 不重复入队(避免 submit 重试堆 job)。
// 失败只记日志,不阻断 submit-all 主流程(advice 是 nice-to-have,不是交卷的一部分)。
func (s *aiService) EnqueueAdviceForEpisode(userID, episodeID uint) error {
	// 在途 advice job 就不重复入队(submit 被重试 / 重复触发时)。
	if s.hasPendingAdviceJob(userID, agent.ScopeEpisode, episodeID) {
		return nil
	}
	// 熔断检查:交卷后链式触发的 advice 也要尊重冷却——否则交卷一次就绕过熔断入队,
	// 学生反复交卷(或 submit 重试)又会堆失败 job 烧 token。和 GetOrEnqueueAdvice 用
	// 同一个 consecutiveAdviceFailures 判定,行为一致。
	if s.consecutiveAdviceFailures(userID, agent.ScopeEpisode, episodeID) >= maxConsecutiveFailures {
		return nil
	}
	return s.enqueueAdviceJob(userID, agent.ScopeEpisode, episodeID)
}

// RegenerateAdvice 是 admin 触发的强制重生成 advice 入口(三档 scope 都支持)。
// 和 GetOrEnqueueAdvice 的关键差异:
//   - 跳过 mastery gate:GetOrEnqueueAdvice 在学生没做题时返回 unavailable,避免空跑;
//     admin 强制重跑应能跑(即便 advice 内容多半是"建议先做题")。
//   - 在途去重照抄 hasPendingAdviceJob,避免连点堆 job。
//
// 返回 status="generating"(已入队)或 "unavailable"(AI off)。和 lazy 生成语义对齐,
// admin SPA 据此决定显示"重新生成中"还是直接关闭弹窗。
//
// 这是 admin 给 course/subject 级 advice"刷新"的唯一入口(lazy 生成只在 episode 级走,
// course/subject 级以前一旦生成就永远不变)。admin 用这个端点能覆盖刷新任何 advice。
func (s *aiService) RegenerateAdvice(userID uint, scope string, scopeID uint) (string, error) {
	// scope 白名单兜底(input validation,在任何 early-return 之前校验,避免 nil resolver
	// 路径静默吞掉坏 scope)。handler 也校验,service 层独立守一道(defense-in-depth)。
	switch scope {
	case agent.ScopeEpisode, agent.ScopeCourse, agent.ScopeSubject:
	default:
		return adviceStatusUnavailable, fmt.Errorf("invalid scope: %s", scope)
	}
	if s.resolver == nil {
		return adviceStatusUnavailable, nil
	}
	// 在途去重:已有 advice job 就不重复入队(和 EnqueueAdviceForEpisode 一致)。
	if s.hasPendingAdviceJob(userID, scope, scopeID) {
		return adviceStatusGenerating, nil
	}
	if err := s.enqueueAdviceJob(userID, scope, scopeID); err != nil {
		return adviceStatusUnavailable, err
	}
	return adviceStatusGenerating, nil
}

// enqueueAdviceJob 构造并持久化一条 advice job。scope/scopeID 编码进 PayloadJSON
// (episode/course 级也走 PayloadJSON,保持单一编码路径;buildAdviceRequest 会解码)。
// episode_id/course_id 字段也填上(让 admin job 列表能按 episode/course 过滤,且
// buildAdviceRequest 的默认 episode 级路径能用 job.EpisodeID)。
func (s *aiService) enqueueAdviceJob(userID uint, scope string, scopeID uint) error {
	// 先尝试解析 episodeID/courseID,让 AIJob 表的索引字段也准确(便于 admin 过滤 +
	// jobNameCache 解析标题)。不同 scope 的 ID 含义不同。EpisodeID/CourseID 是 *uint,
	// 能解析出来的取地址,subject 级两者都 nil(不属于任何 episode/course)。
	var episodeID, courseID *uint
	switch scope {
	case agent.ScopeEpisode:
		// copy scopeID to local before taking address (avoid pointing at the param)
		epID := scopeID
		episodeID = &epID
		if ep, err := s.episodeRepo.FindByID(scopeID); err == nil && ep != nil {
			c := ep.CourseID
			courseID = &c
		}
	case agent.ScopeCourse:
		c := scopeID
		courseID = &c
	case agent.ScopeSubject:
		// subject 级两者都 nil(以前塞 0,现在诚实表达"无对应实体")。
	}
	payload, _ := json.Marshal(map[string]any{
		"scope":      scope,
		"scope_id":   scopeID,
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
		// scope/scope_id 匹配规则(无 PayloadJSON 默认 episode 级用 EpisodeID,
		// 有则解码 PayloadJSON 比较)抽到了 adviceJobMatchesScope,供这里和
		// consecutiveAdviceFailures 复用——保持两处判定完全一致。
		if adviceJobMatchesScope(j, scope, scopeID) {
			return true
		}
	}
	return false
}
