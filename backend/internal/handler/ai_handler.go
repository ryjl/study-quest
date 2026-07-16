package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

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
	SubmitQuizAnswer(c *gin.Context)
	RegenerateQuiz(c *gin.Context)
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
// summary JSON (headline / key_points / concepts / takeaway) plus a flag the
// client uses to decide whether to show the AI card. We re-parse the stored
// JSON rather than returning it raw so the client gets a stable, documented
// shape even if the model's output drifts slightly.
type summaryResponse struct {
	Headline  string   `json:"headline"`
	KeyPoints []string `json:"key_points"`
	Concepts  []string `json:"concepts"`
	Takeaway  string   `json:"takeaway"`
}

// GetEpisodeSummary returns the AI summary for an episode.
// GET /api/v1/episodes/:id/ai-summary
//
// 404 when no summary exists (not yet generated, or AI off for the course) —
// the client treats 404 as "no summary available" and hides the card. This is
// the graceful-degradation contract: AI is an add-on, so absence is normal.
func (h *aiHandler) GetEpisodeSummary(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID"})
		return
	}
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
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
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
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

// submitQuizRequest is the body for POST /ai-quiz/submit. For a choice question
// the client sends answer_index; for a fill question, answer_text. The service
// grades by question type, ignoring the irrelevant field.
type submitQuizRequest struct {
	QuestionID  uint   `json:"question_id"`
	AnswerIndex *int   `json:"answer_index,omitempty"`
	AnswerText  string `json:"answer_text,omitempty"`
}

// SubmitQuizAnswer grades one answer, persists it, updates memory, returns the
// verdict (correct? correct answer? explanation? jump-to-video time?).
// POST /api/v1/episodes/:id/ai-quiz/submit
func (h *aiHandler) SubmitQuizAnswer(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req submitQuizRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.QuestionID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
	var answerText *string
	if req.AnswerText != "" {
		t := req.AnswerText
		answerText = &t
	}
	result, err := h.aiService.SubmitQuizAnswer(userID, req.QuestionID, req.AnswerIndex, answerText)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "题目不存在或无权作答"})
		return
	}
	c.JSON(http.StatusOK, result)
}

// RegenerateQuiz drops the user's current quiz and re-runs the agent against
// their latest memory (换题). Returns the new status (generating → poll).
// POST /api/v1/episodes/:id/ai-quiz/regenerate
func (h *aiHandler) RegenerateQuiz(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
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
		// No unlock service wired (tests) — fail open for staff only. We only
		// reach here for non-staff, so deny.
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

// encodeJSON is a tiny helper kept for symmetry with other handlers; not all
// call sites need it but quiz response shaping may grow. Unused for now —
// referenced via _ to avoid an unused-warning if encoding/json grows import
// needs elsewhere.
var _ = json.Marshal
