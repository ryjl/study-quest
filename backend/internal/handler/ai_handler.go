package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/service"
)

// AIHandler serves the CLIENT-facing AI endpoints (the student/parent Flutter
// app reading a summary). Distinct from the admin AI endpoints (admin_ai.go) —
// different auth (UserAuth vs AdminAuth), different surface (read-only summary
// vs full CRUD+observability). Kept as its own handler to avoid bloating
// EpisodeHandler, which is already the largest client handler.
//
// Phase B exposes summary reads. Phase C adds quiz endpoints (get/submit/
// regenerate) here; Phase D adds chat.
type AIHandler interface {
	GetEpisodeSummary(c *gin.Context)
	// Phase C quiz endpoints. All gated by the same access check as stream/
	// play-info (IsEpisodeVisible): a student can only quiz on episodes they
	// may watch.
	GetEpisodeQuiz(c *gin.Context)
	// SubmitAllQuizAnswers 是 Phase B 的统一交卷端点(一次提交 = 一次考试)。
	SubmitAllQuizAnswers(c *gin.Context)
	RegenerateQuiz(c *gin.Context)
	// GetEpisodeQuizHistory returns the user's archived (superseded) quizzes for
	// an episode as fully read-only views (correct answers revealed). Phase 3.
	GetEpisodeQuizHistory(c *gin.Context)
	// ── Phase C — agent 驱动的学习建议(advice)端点 ──
	// 三档 scope 各一个端点,都走 GetOrEnqueueAdvice 的 lazy 生成。返回
	// {status: ready/generating/unavailable, advice: {...}}。访问控制:episode 走
	// canAccessEpisode(和 quiz 同闸门);course/subject 级只需要登录——advice 数据
	// 按 user_id 键存(GetAdvice(userID, scope, scopeID)),绝不泄露跨用户数据,所以
	// 即便学生查一个没权限的 course,也只会拿到他自己在这个 course 上的(多半空的)
	// 建议,看不到别人的数据。
	GetEpisodeAdvice(c *gin.Context)
	GetCourseAdvice(c *gin.Context)
	GetSubjectAdvice(c *gin.Context)
	// ── Phase D — 课程级总结(course-unique 纯内容总结)端点 ──
	// 客户端只读已生成的课程总结(不触发生成——总结是 course-unique 共享的,admin 生成)。
	// 返回 {summary_text, model_used, generated_at}。无总结时 404(客户端隐藏课程总结卡片)。
	GetCourseSummary(c *gin.Context)
	// ── 错题本 (TODO.md P0) 端点 ──
	// 数据按 user_id 键存,只需登录(同 advice course/subject 级口径)。
	GetWrongBook(c *gin.Context)
	MarkWrongBookMastered(c *gin.Context)
	GetWrongBookRedo(c *gin.Context)
	SubmitWrongBookRedo(c *gin.Context)
	// ── 课程考试 (TODO.md P0) 端点 ──
	// StartExam/SubmitExam 走 canAccessCourse 门;status/gate 只需登录。
	StartExam(c *gin.Context)
	GetActiveExam(c *gin.Context)
	SubmitExam(c *gin.Context)
	GetExamStatus(c *gin.Context)
}

type aiHandler struct {
	aiService     service.AIService
	unlockService service.UnlockService // gates episode access for quiz endpoints
}

// NewAIHandler constructs an AIHandler. aiService may be nil in a build without
// AI wired; reads then 404 cleanly. unlockService gates quiz access (the quiz
// reads/grades content tied to an episode, so it respects the same visibility
// rule as streaming).
func NewAIHandler(aiService service.AIService, unlockService service.UnlockService) AIHandler {
	return &aiHandler{aiService: aiService, unlockService: unlockService}
}

// summaryResponse is the JSON shape served to the client. It's the parsed
// summary JSON (headline / key_points / concepts / pre_adventure / takeaway).
// We re-parse the stored JSON rather than returning it raw so the client gets a
// stable, documented shape even if the model's output drifts slightly.
//
// pre_adventure mirrors agent.PreAdventureItem but is defined locally here to
// keep the handler a pure wire-shape projection (no dependency on the agent
// package). If the two ever drift, json.Unmarshal still tolerates missing keys.
type summaryResponse struct {
	Headline      string             `json:"headline"`
	Sections      []summarySection   `json:"sections"`
	KeyPoints     []string           `json:"key_points"`
	Methods       []string           `json:"methods"`
	CommonMistakes []string          `json:"common_mistakes"`
	Concepts      []string           `json:"concepts"`
	PreAdventure  []preAdventureItem `json:"pre_adventure"`
	Takeaway      string             `json:"takeaway"`
}

// summarySection mirrors agent.SummarySection locally (知识点小节)。
type summarySection struct {
	Title  string   `json:"title"`
	Points []string `json:"points"`
}

// normalizeNilSlices 保证切片非 nil,避免 Marshal 成 null(前端 .map 会炸)。
// 老 summary(Phase F 前生成)没有 sections/methods/common_mistakes,Unmarshal 留 nil。
func (r *summaryResponse) normalizeNilSlices() {
	if r.Sections == nil {
		r.Sections = []summarySection{}
	}
	if r.KeyPoints == nil {
		r.KeyPoints = []string{}
	}
	if r.Methods == nil {
		r.Methods = []string{}
	}
	if r.CommonMistakes == nil {
		r.CommonMistakes = []string{}
	}
	if r.Concepts == nil {
		r.Concepts = []string{}
	}
	if r.PreAdventure == nil {
		r.PreAdventure = []preAdventureItem{}
	}
}

// preAdventureItem is one pre-class exploration question (open-ended, sparks
// curiosity) plus a non-spoilery hint. Produced by the summarizer in the same
// LLM call as the summary — zero extra cost.
type preAdventureItem struct {
	Prompt string `json:"prompt"`
	Hint   string `json:"hint"`
}

// GetEpisodeSummary returns the AI summary for an episode.
// GET /api/v1/episodes/:id/ai-summary
//
// 404 when no summary exists (not yet generated, or AI off for the course) —
// the client treats 404 as "no summary available" and hides the card. This is
// the graceful-degradation contract: AI is an add-on, so absence is normal.
func (h *aiHandler) GetEpisodeSummary(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID"})
		return
	}
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	// 访问控制:和 GetEpisodeQuiz/RegenerateQuiz 走同一道 canAccessEpisode 闸门。
	// 之前这里漏了检查,任何登录用户都能凭 episode id 直接读别课程/被锁 episode
	// 的 summary——和 quiz 端点不一致的权限漏洞。
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	if !h.canAccessEpisode(c, userID, uint(id)) {
		return // canAccessEpisode 已经写好错误响应
	}
	summary, err := h.aiService.GetSummary(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load summary"})
		return
	}
	if summary == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no summary for this episode"})
		return
	}
	// Parse the stored JSON into the documented response shape.
	var resp summaryResponse
	if err := json.Unmarshal([]byte(summary.SummaryJSON), &resp); err != nil {
		// Stored JSON is malformed (shouldn't happen — we control the writer).
		// Return a minimal valid response rather than crashing the client.
		resp = summaryResponse{Headline: "(总结解析失败)"}
	}
	resp.normalizeNilSlices()
	c.JSON(http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Phase C — quiz endpoints
// ---------------------------------------------------------------------------

// quizResponse is the unified payload for GET /ai-quiz. The status field tells
// the client what to render:
//   - "ready": quiz is populated; render the questions
//   - "generating": a job was just enqueued; poll (show "正在为你生成练习…")
//   - "unavailable": AI/quiz off or no source material; hide the quiz card
type quizResponse struct {
	Status        string                  `json:"status"`
	Quiz          *service.QuizView       `json:"quiz,omitempty"`
	AgentFeedback string                  `json:"agent_feedback,omitempty"`
}

// GetEpisodeQuiz returns the user's quiz for an episode, lazily enqueuing
// generation on first access.
// GET /api/v1/episodes/:id/ai-quiz
//
// Status codes:
//   - 200 {status:"ready", quiz:{...}}   — quiz exists, serve it
//   - 202 {status:"generating"}          — enqueued, client should poll
//   - 404 {status:"unavailable"}         — AI off / no chunks / not visible
func (h *aiHandler) GetEpisodeQuiz(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID"})
		return
	}
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, quizResponse{Status: "unavailable"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	if !h.canAccessEpisode(c, userID, uint(id)) {
		return // canAccessEpisode already wrote the error response
	}

	status, _, err := h.aiService.GetOrEnqueueQuiz(userID, uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load quiz"})
		return
	}
	switch status {
	case "ready":
		// Fetch the full client view (questions without answers).
		view, err := h.aiService.GetQuizForClient(userID, uint(id))
		if err != nil || view == nil {
			c.JSON(http.StatusOK, quizResponse{Status: "ready"})
			return
		}
		c.JSON(http.StatusOK, quizResponse{Status: "ready", Quiz: view, AgentFeedback: view.AgentFeedback})
	case "generating":
		c.JSON(http.StatusAccepted, quizResponse{Status: "generating"})
	default:
		c.JSON(http.StatusNotFound, quizResponse{Status: "unavailable"})
	}
}

// submitAllQuizRequest 是统一交卷的 body:answers 是一整张卷子的作答数组。
// 选择题每条带 answer_index,填空题带 answer_text,另一字段省略。
type submitAllQuizRequest struct {
	Answers []service.QuizAnswerInput `json:"answers" binding:"required"`
}

// SubmitAllQuizAnswers 一次性判分整张卷子,逐题返回结果,并锁定该 quiz(一次提交=
// 一次考试)。POST /api/v1/episodes/:id/ai-quiz/submit-all
//
// 状态码:200(交卷成功,返回每题结果数组)/ 409(已交卷,不能重复提交)/ 400(格式)。
func (h *aiHandler) SubmitAllQuizAnswers(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID"})
		return
	}
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	if !h.canAccessEpisode(c, userID, uint(id)) {
		return // canAccessEpisode 已写好错误响应
	}
	var req submitAllQuizRequest
	if !bindJSON(c, &req) { return }
	results, err := h.aiService.SubmitAllQuizAnswers(userID, uint(id), req.Answers)
	if err != nil {
		// 已交卷 → 409,告诉前端别重复提交。
		if errors.Is(err, service.ErrQuizAlreadySubmitted) {
			c.JSON(http.StatusConflict, gin.H{"error": "这套题已交卷,不能重复提交"})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "题目不存在或无权作答"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// RegenerateQuiz drops the user's current quiz and re-runs the agent against
// their latest memory (换题). Returns the new status (generating → poll).
// POST /api/v1/episodes/:id/ai-quiz/regenerate
func (h *aiHandler) RegenerateQuiz(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID"})
		return
	}
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	if !h.canAccessEpisode(c, userID, uint(id)) {
		return
	}
	status, err := h.aiService.RegenerateQuiz(userID, uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "重新生成失败"})
		return
	}
	c.JSON(http.StatusAccepted, quizResponse{Status: status})
}

// GetEpisodeQuizHistory returns the user's archived quizzes for an episode as
// fully read-only views (correct answers + per-question wrong state revealed).
// GET /api/v1/episodes/:id/ai-quiz/history
//
// Always 200 with a (possibly empty) list — absence of history is the normal
// case before the first regenerate, not an error. Same canAccessEpisode gate as
// the other quiz endpoints.
func (h *aiHandler) GetEpisodeQuizHistory(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID"})
		return
	}
	if h.aiService == nil {
		c.JSON(http.StatusOK, gin.H{"history": []interface{}{}})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	if !h.canAccessEpisode(c, userID, uint(id)) {
		return
	}
	history, err := h.aiService.ListQuizHistory(userID, uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load quiz history"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"history": history})
}

// requireUserID reads the authenticated user's ID from the gin context (set by
// UserAuthMiddleware). Staff roles (admin/parent) are also accepted — they can
// preview quizzes. Writes a 401 and returns false when no trustworthy identity
// is present (fail-closed, mirroring GetPlayInfo's gate).
func requireUserID(c *gin.Context) (uint, bool) {
	uidVal, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return 0, false
	}
	uid, ok := uidVal.(uint)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return 0, false
	}
	return uid, true
}

// canAccessEpisode enforces the same visibility gate as streaming: staff bypass,
// everyone else must have the episode visible via the unlock service. Writes the
// error response and returns false when access is denied.
func (h *aiHandler) canAccessEpisode(c *gin.Context, userID, episodeID uint) bool {
	// Staff bypass — they manage content, not consume under a drip schedule.
	if roleVal, hasRole := c.Get("userRole"); hasRole {
		if role, ok := roleVal.(string); ok && model.IsStaffRole(role) {
			return true
		}
	}
	if h.unlockService == nil {
		// No unlock service wired (tests) — non-staff denied (fail-closed).
		c.JSON(http.StatusForbidden, gin.H{"error": "episode access cannot be verified"})
		return false
	}
	visible, err := h.unlockService.IsEpisodeVisible(userID, episodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check episode access"})
		return false
	}
	if !visible {
		c.JSON(http.StatusForbidden, gin.H{"error": "episode is locked"})
		return false
	}
	return true
}

// canAccessCourse 检查学生是否有权访问某课程(staff bypass;非 staff 必须至少有
// 一个可见 episode,即被授权了这门课)。用于 course 级 advice 端点——虽然 advice
// 数据按 user_id 隔离(拿不到别人的),但不应让任意登录用户对未授权课程触发 LLM
// job 或探测课程是否存在。
func (h *aiHandler) canAccessCourse(c *gin.Context, userID, courseID uint) bool {
	if roleVal, hasRole := c.Get("userRole"); hasRole {
		if role, ok := roleVal.(string); ok && model.IsStaffRole(role) {
			return true
		}
	}
	if h.unlockService == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "course access cannot be verified"})
		return false
	}
	vis, err := h.unlockService.ResolveVisibleEpisodes(userID, courseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check course access"})
		return false
	}
	if len(vis.VisibleIDs) == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "no access to this course"})
		return false
	}
	return true
}

// encodeJSON is a tiny helper kept for symmetry with other handlers; not all
// call sites need it but quiz response shaping may grow. Unused for now —
// referenced via _ to avoid an unused-warning if encoding/json grows import
// needs elsewhere.
var _ = json.Marshal

// ---------------------------------------------------------------------------
// Phase C — advice endpoints (agent 驱动的学习建议)
// ---------------------------------------------------------------------------

// adviceResponse 是三个 advice 端点统一的响应形状。status 字段告诉客户端:
//   - ready:advice 已生成,advice 字段带完整内容;
//   - generating:已入队生成中,客户端稍后轮询;
//   - unavailable:AI 未配置或该 scope 不支持,客户端隐藏/降级。
//
// advice 字段在非 ready 时省略(omitempty)。复用 model.StudyAdvice 的 JSON(tag 已定义)。
type adviceResponse struct {
	Status string              `json:"status"`
	Scope  string              `json:"scope"`
	ID     uint                `json:"id"`
	Advice *model.StudyAdvice  `json:"advice,omitempty"`
}

// GetEpisodeAdvice 返回某节课的复习建议(episode 级)。
// GET /api/v1/episodes/:id/ai-advice
//
// 和 quiz 端点同一道 canAccessEpisode 闸门:能看这节课视频才能看它的建议。
// 第一次访问触发 lazy 生成(入队 advice job),返回 generating 让客户端轮询。
func (h *aiHandler) GetEpisodeAdvice(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID"})
		return
	}
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	if !h.canAccessEpisode(c, userID, uint(id)) {
		return // canAccessEpisode 已写好错误响应
	}
	status, advice, err := h.aiService.GetOrEnqueueAdvice(userID, "episode", uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load advice"})
		return
	}
	c.JSON(http.StatusOK, adviceResponse{Status: status, Scope: "episode", ID: uint(id), Advice: advice})
}

// GetCourseAdvice 返回某门课程的整体弱点分析(course 级)。
// GET /api/v1/courses/:id/ai-advice
//
// 访问控制:走 canAccessCourse(staff bypass;非 staff 必须被授权该课程)。
// advice 数据本身按 user_id 键存(拿不到别人的),但这里仍校验课程访问权,
// 避免任意登录用户对未授权课程触发 LLM job 或探测课程存在性。
func (h *aiHandler) GetCourseAdvice(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID"})
		return
	}
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	if !h.canAccessCourse(c, userID, uint(id)) {
		return
	}
	status, advice, err := h.aiService.GetOrEnqueueAdvice(userID, "course", uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load advice"})
		return
	}
	c.JSON(http.StatusOK, adviceResponse{Status: status, Scope: "course", ID: uint(id), Advice: advice})
}

// GetSubjectAdvice 返回某科目(跨多门课)的弱点分析(subject 级)。
// GET /api/v1/subjects/:id/ai-advice
//
// 访问控制:只需登录(同 course 级理由)。
func (h *aiHandler) GetSubjectAdvice(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subject ID"})
		return
	}
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	status, advice, err := h.aiService.GetOrEnqueueAdvice(userID, "subject", uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load advice"})
		return
	}
	c.JSON(http.StatusOK, adviceResponse{Status: status, Scope: "subject", ID: uint(id), Advice: advice})
}

// ---------------------------------------------------------------------------
// Phase D — 课程级总结端点(course-unique 纯内容总结)
// ---------------------------------------------------------------------------

// courseSummaryResponse 是 GET /courses/:id/ai-summary 的响应形状。课程总结是 course-unique
// 的纯内容总结(不含个人维度),所有学生共享同一份。status 字段告诉客户端:
//   - ready:总结已生成,summary_text/model_used/generated_at 带完整内容;
//   - unavailable:AI 未配置,或该课程暂无总结(客户端隐藏课程总结卡片)。
//
// 注意:这里没有 "generating"——客户端不触发生成(course summary 是 admin 触发的);客户端
// 只读已生成的,无总结就 404。admin 触发生成走 admin 端点(POST /admin/api/ai/course-summary)。
// courseSummaryResponse 是 ai_course_summary 的客户端 JSON 视图。比 admin 简化:
//   - 没有 "generating" 态(客户端不触发,无总结直接 404 + unavailable)
//   - 有陈旧信号字段(episode_count_at_gen/current_episode_count),让学生看到
//     "内容基于 N 节生成,目前 M 节,可能未涵盖最新课时"的诚实提示
type courseSummaryResponse struct {
	Status             string `json:"status"`
	SummaryText        string `json:"summary_text,omitempty"`
	ModelUsed          string `json:"model_used,omitempty"`
	GeneratedAt        string `json:"generated_at,omitempty"`
	EpisodeCountAtGen  int    `json:"episode_count_at_gen,omitempty"`
	CurrentEpisodeCount int   `json:"current_episode_count,omitempty"`
}

// GetCourseSummary 返回某门课程的课程级总结(course-unique 纯内容总结)。
// GET /api/v1/courses/:id/ai-summary
//
// 客户端只读已生成的总结,不触发生成(课程总结是 admin 手动触发的——它是 course-unique
// 共享的,不应让任一学生触发生成)。无总结时 404(status=unavailable),客户端隐藏卡片。
// 访问控制:只需登录(总结是 course-unique 共享的,内容对所有能访问该课程的学生公开;
// 若后续要严格校验课程访问权,可在 service 层加 userRepo.HasAccess 检查)。
func (h *aiHandler) GetCourseSummary(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID"})
		return
	}
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, courseSummaryResponse{Status: "unavailable"})
		return
	}
	// 只需登录(课程总结对所有学生共享,不按 user 区分)。
	if _, ok := requireUserID(c); !ok {
		return
	}
	summary, err := h.aiService.GetCourseSummary(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load course summary"})
		return
	}
	if summary == nil {
		c.JSON(http.StatusNotFound, courseSummaryResponse{Status: "unavailable"})
		return
	}
	// 当前已总结课时数(用于陈旧检测)。best-effort,失败时省略。
	currentCount, _ := h.aiService.CountEpisodesWithSummary(uint(id))
	// GeneratedAt 用 RFC3339(与 admin 端一致),客户端 DateTime.parse 解析,
	// 显示层再格式化。统一传输格式,避免同字段两套格式带来的歧义。
	c.JSON(http.StatusOK, courseSummaryResponse{
		Status:              "ready",
		SummaryText:         summary.SummaryText,
		ModelUsed:           summary.ModelUsed,
		GeneratedAt:         summary.GeneratedAt.Format(time.RFC3339),
		EpisodeCountAtGen:   summary.EpisodeCountAtGen,
		CurrentEpisodeCount: int(currentCount),
	})
}
