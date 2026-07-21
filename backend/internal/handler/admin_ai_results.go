package handler

import (
	"net/http"
	"time"
	"github.com/gin-gonic/gin"
	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
)

// Code split from admin_ai.go for navigability.

func (h *adminHandler) GetAISummary(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI 子系统未配置"})
		return
	}
	episodeID, err := parseUintParam(c, "episodeID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 episodeID"})
		return
	}
	summary, err := h.aiService.GetSummary(episodeID)
	if err != nil {
		respondError(c, err)
		return
	}
	if summary == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "该课时暂无总结"})
		return
	}
	// Return the raw row; the admin SPA parses summary_json for rich display.
	c.JSON(http.StatusOK, gin.H{
		"episode_id":   summary.EpisodeID,
		"course_id":    summary.CourseID,
		"summary_json": summary.SummaryJSON,
		"model_used":   summary.ModelUsed,
		"created_at":   summary.CreatedAt.Format("2006-01-02 15:04:05"),
	})
}

// ListUserQuizzes lists all of a user's quizzes (the per-user AI view entry).
// GET /admin/api/ai/users/:userID/quizzes
func (h *adminHandler) ListUserQuizzes(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	quizzes, err := h.aiService.ListQuizzesForUser(userID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, quizzes)
}

// GetQuizDetail returns the full per-quiz observability bundle: questions WITH
// answers, the student's answer history, their mastery, the agent's feedback,
// and the ai_runs that produced it (trace_json lives on the runs — the SPA
// renders the "思考时间线" from it). GET /admin/api/ai/quizzes/:quizID
func (h *adminHandler) GetQuizDetail(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI 子系统未配置"})
		return
	}
	quizID, err := parseUintParam(c, "quizID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 quizID"})
		return
	}
	detail, err := h.aiService.GetQuizDetail(quizID)
	if err != nil {
		respondError(c, err)
		return
	}
	if detail == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "题库不存在"})
		return
	}
	c.JSON(http.StatusOK, detail)
}

// ---------------------------------------------------------------------------
// Phase D — admin 课程级总结(course-unique 纯内容总结,agent 驱动)
// ---------------------------------------------------------------------------

// courseSummaryAdminDTO 是 ai_course_summary 的 admin JSON 视图。status 让前端区分:
//   - ready:有总结(summary_text 字段非空)
//   - generating:无总结 + 有在途 job(前端轮询)
//   - 空 status + 无 summary:无总结也未生成(前端显示"生成总结"按钮)
//
// EpisodeCountAtGen / CurrentEpisodeCount 用于陈旧检测:前者是生成时快照的"已总结
// 课时数"(存 DB),后者是读时现算(每次 GET 查 ai_summaries.count)。差值 > 0 = 陈旧。
type courseSummaryAdminDTO struct {
	Status             string `json:"status"` // ready | generating | ""(无总结未生成)
	SummaryText        string `json:"summary_text,omitempty"`
	ModelUsed          string `json:"model_used,omitempty"`
	GeneratedAt        string `json:"generated_at,omitempty"`
	EpisodeCountAtGen  int    `json:"episode_count_at_gen,omitempty"`
	CurrentEpisodeCount int   `json:"current_episode_count,omitempty"`
}

// TriggerCourseSummary 触发为某课程生成课程级总结(异步入队 course_summary job)。
// 返回 status="generating"(或 unavailable,当 AI off 或课程不存在)。前端随后轮询 GET
// 端点直到 ready。
// POST /admin/api/ai/courses/:id/course-summary
//
// 设计为"强制重生成"语义:即使已有总结,POST 也会重跑(覆盖)。这让 admin 能刷新过期
// 总结(比如课程新增了 episode 之后)。去重靠 service 的在途 job 检查(避免连点堆 job)。
func (h *adminHandler) TriggerCourseSummary(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	status, err := h.aiService.EnqueueCourseSummary(courseID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// GetCourseSummary 取某课程的最新课程总结(供 admin GET 端点)。
// GET /admin/api/ai/courses/:id/course-summary
//
// 响应 status 三态:
//   - ready:有总结(返回 summary_text + 元数据)
//   - generating:无总结 + 有在途 job(前端继续轮询 / 显示 spinner)
//   - "":无总结 + 无在途 job(前端显示"生成总结"按钮)
func (h *adminHandler) GetCourseSummary(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	summary, err := h.aiService.GetCourseSummary(courseID)
	if err != nil {
		respondError(c, err)
		return
	}
	dto := courseSummaryAdminDTO{}
	// CurrentEpisodeCount 无论有没有 summary 都算——前端 ready 时用它跟 at_gen
	// 比对(陈旧提示),无 summary 时无害(前端不显示这个字段)。
	if currentCount, cerr := h.aiService.CountEpisodesWithSummary(courseID); cerr == nil {
		dto.CurrentEpisodeCount = int(currentCount)
	}
	if summary != nil {
		dto.Status = "ready"
		dto.SummaryText = summary.SummaryText
		dto.ModelUsed = summary.ModelUsed
		dto.GeneratedAt = summary.GeneratedAt.Format(time.RFC3339)
		dto.EpisodeCountAtGen = summary.EpisodeCountAtGen
	} else if h.aiService.HasPendingCourseSummaryJob(courseID) {
		// 无总结但正在生成——前端据此显示 spinner 并继续轮询。
		dto.Status = "generating"
	}
	c.JSON(http.StatusOK, dto)
}

// ListEpisodeSummaryStatus 返回某课程下"已有 AI summary"的 episode id 列表。
// GET /admin/api/ai/courses/:id/summaries-status
//
// 给 AI 控制台「内容管理」tab gate 每集"删除"按钮用:没有 summary 的课时不应显示
// 删除按钮(删了也是无意义的幂等 no-op,反而误导 admin)。返回一个 id 数组,前端转 Set。
func (h *adminHandler) ListEpisodeSummaryStatus(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, gin.H{"episode_ids_with_summary": []uint{}})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	ids, err := h.aiService.ListEpisodeSummaryStatus(courseID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"episode_ids_with_summary": ids})
}

// ---------------------------------------------------------------------------
// Phase E — admin 用户学习报告(agent 驱动,跨课程画像)
// ---------------------------------------------------------------------------

// userStudyReportDTO 是 user_study_report 的 admin JSON 视图。status 让前端区分:
//   - ready:有报告(report 字段非空)
//   - generating:无报告 + 有在途 job(前端轮询)
//   - 空 status + 无 report:无报告也未生成(前端显示"生成报告"按钮)
type userStudyReportDTO struct {
	Status      string `json:"status"`           // ready | generating | ""(无报告未生成)
	Report      string `json:"report,omitempty"` // 报告文本(ready 时有)
	ModelUsed   string `json:"model_used,omitempty"`
	GeneratedAt string `json:"generated_at,omitempty"`
}

// TriggerUserStudyReport 触发为某用户生成学习报告(异步入队 user_report job)。
// 返回 status="generating"(或 unavailable,当 AI off)。前端随后轮询 GET 端点直到 ready。
// POST /admin/api/ai/users/:id/study-report
//
// 设计为"强制重生成"语义:即使已有报告,POST 也会重跑(覆盖)。这让 admin 能刷新过期
// 报告。去重靠 service 的在途 job 检查(避免连点堆 job)。
func (h *adminHandler) TriggerUserStudyReport(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的用户 id"})
		return
	}
	status, err := h.aiService.EnqueueUserReport(userID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// GetUserStudyReport 取某用户的最新学习报告(供 admin GET 端点)。
// GET /admin/api/ai/users/:id/study-report
//
// 响应 status 三态:
//   - ready:有报告(返回 report 文本 + 元数据)
//   - generating:无报告 + 有在途 job(前端继续轮询 / 显示 spinner)
//   - "":无报告 + 无在途 job(前端显示"生成报告"按钮)
func (h *adminHandler) GetUserStudyReport(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的用户 id"})
		return
	}
	report, err := h.aiService.GetUserStudyReport(userID)
	if err != nil {
		respondError(c, err)
		return
	}
	dto := userStudyReportDTO{}
	if report != nil {
		dto.Status = "ready"
		dto.Report = report.ReportText
		dto.ModelUsed = report.ModelUsed
		dto.GeneratedAt = report.GeneratedAt.Format(time.RFC3339)
	} else if h.aiService.HasPendingUserReportJob(userID) {
		// 无报告但正在生成——前端据此显示 spinner 并继续轮询。
		dto.Status = "generating"
	}
	c.JSON(http.StatusOK, dto)
}

// ---------------------------------------------------------------------------
// Prompt 预览(admin 调优 hint 时实时看效果)
// ---------------------------------------------------------------------------

// previewPromptRequest 是 POST /admin/api/ai/courses/:id/preview-prompt 的 body。
// agent 三选一,对应 summary / quiz / advice 三个 agent 的 prompt 构造。
type previewPromptRequest struct {
	Agent string `json:"agent"` // summary | quiz | advice
}

// resolvedHintsDTO 展示"这个课程最终解析出的 5 个 hint 来源"。让 admin 直观看到
// 当前用的是学科默认还是课程覆盖的(Effective*Hint 会从课程级回退到学科级)。
type resolvedHintsDTO struct {
	WhisperHint string `json:"whisper_hint"`
	SummaryHint string `json:"summary_hint"`
	QuizHint    string `json:"quiz_hint"`
	AdviceHint  string `json:"advice_hint"`
	TermDict    string `json:"term_dict"`
}

// previewPromptResponse 是预览端点的完整响应。system_prompt + user_prompt 就是
// 这个课程+agent 最终会发给 LLM 的开场消息(预览即真相);resolved_hints 帮 admin
// 判断"我改的 hint 真的生效了吗"。
type previewPromptResponse struct {
	SystemPrompt  string           `json:"system_prompt"`
	UserPrompt    string           `json:"user_prompt"`
	ResolvedHints resolvedHintsDTO `json:"resolved_hints"`
}

// PreviewCoursePrompt 拼出某课程 + 某 agent 最终会发给 LLM 的完整 prompt,**不调 LLM**。
// 纯文本拼接,响应快(<10ms)。用于 admin 调优 hint 后立刻看效果,不用等真生成。
//
// POST /admin/api/ai/courses/:id/preview-prompt  body: {"agent": "summary"|"quiz"|"advice"}
//
// 工作流:
//  1. 加载 course + 预加载 subject(courseRepo.FindByID 不预加载 Subject,这里单独取一次)。
//  2. 用 Course.EffectiveXxxHint(subject) 解析出最终生效的 5 个 hint(课程级覆盖学科级)。
//  3. 根据 agent 类型构造对应 Request(填入 hint/TermDict,episode/user/mastery 等运行时字段
//     填占位值——预览不针对具体学生,重点看 hint/TermDict 的解析结果),调 agent 包的导出
//     preview 函数拿到 (system, user)。
//  4. 返回 system_prompt + user_prompt + resolved_hints。
func (h *adminHandler) PreviewCoursePrompt(c *gin.Context) {
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	var req previewPromptRequest
	if !bindJSON(c, &req) { return }
	// 校验 agent 字段(白名单),防 injection / 笔误。
	switch req.Agent {
	case "summary", "quiz", "advice":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "agent 必须是 summary / quiz / advice"})
		return
	}

	course, err := h.courseRepo.FindByID(courseID)
	if err != nil {
		respondError(c, err)
		return
	}
	if course == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "课程不存在"})
		return
	}
	// courseRepo.FindByID 不预加载 Subject,单独取一次。subject 可能被删(course 仍残留
	// 旧 SubjectID),取不到时退化为零值 Subject(hint 全空,prompt 仍可拼出来,只是没 hint 注入)。
	var subject model.Subject
	if course.SubjectID != 0 {
		if subj, _ := h.subjectRepo.FindByID(course.SubjectID); subj != nil {
			subject = *subj
		}
	}

	// 解析最终生效的 5 个 hint(课程级覆盖学科级)。展示给 admin 看"现在生效的是哪个值"。
	resolved := resolvedHintsDTO{
		WhisperHint: course.EffectiveWhisperHint(subject),
		SummaryHint: course.EffectiveSummaryHint(subject),
		QuizHint:    course.EffectiveQuizHint(subject),
		AdviceHint:  course.EffectiveAdviceHint(subject),
		TermDict:    course.EffectiveTermDict(subject),
	}

	// 取课程下任意一集填进 Request 的 EpisodeID/EpisodeTitle 等字段。
	// 预览不针对具体学生/课时,但 prompt 模板需要这些占位字段(如 quiz prompt 显示"课时: xxx")。
	// 没有课时就填占位值,prompt 仍能拼出来(只是显示"#0"或空标题)。
	var episodeID uint
	var episodeTitle string
	if h.episodeRepo != nil {
		if eps, eErr := h.episodeRepo.ListByCourse(courseID); eErr == nil && len(eps) > 0 {
			episodeID = eps[0].ID
			episodeTitle = eps[0].Title
		}
	}
	subjectLabel := subject.Label // 学科中文名(如"数学"),prompt 里用

	var systemPrompt, userPrompt string
	switch req.Agent {
	case "summary":
		// summary 不需要 episode/user 信息,只需 Subject + SummaryHint + TermDict + Chunks。
		// Chunks 留空(prompt 显示空字幕段);admin 看的是 hint/TermDict 的注入位置和效果。
		systemPrompt, userPrompt = agent.BuildSummaryPromptForPreview(agent.SummarizerRequest{
			CourseID:    courseID,
			Subject:     subjectLabel,
			SummaryHint: resolved.SummaryHint,
			TermDict:    resolved.TermDict,
		})
	case "quiz":
		// quiz 的 user prompt 需要 EpisodeTitle/Subject 作为上下文;mastery 留空
		// (预览不针对具体学生,prompt 会显示"新学生,暂无答题记录")。
		systemPrompt, userPrompt = agent.BuildQuizPromptForPreview(agent.QuizzerRequest{
			EpisodeID:    episodeID,
			CourseID:     courseID,
			EpisodeTitle: episodeTitle,
			Subject:      subjectLabel,
		})
	case "advice":
		// advice 选 course scope(和课程级 preview 最匹配)。填 Subject + AdviceHint + TermDict;
		// mastery/ExtraContext 留空(prompt 显示"当前无答题记录")。
		systemPrompt, userPrompt = agent.BuildAdvicePromptForPreview(agent.AdviceRequest{
			Scope:      agent.ScopeCourse,
			ScopeID:    courseID,
			ScopeTitle: course.Title,
			Subject:    subjectLabel,
			CourseID:   courseID,
			AdviceHint: resolved.AdviceHint,
			TermDict:   resolved.TermDict,
		})
	}

	c.JSON(http.StatusOK, previewPromptResponse{
		SystemPrompt:  systemPrompt,
		UserPrompt:    userPrompt,
		ResolvedHints: resolved,
	})
}

// ---------------------------------------------------------------------------
// 重新生成 + 删除(2026-07-19 加):AI 控制台中枢的后端端点。
// ---------------------------------------------------------------------------

// regenerateUserQuizRequest 是 POST /admin/api/ai/users/:userID/quizzes/regenerate 的 body。
