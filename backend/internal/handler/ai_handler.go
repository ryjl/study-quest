package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"studyquest/backend/internal/service"
)

// AIHandler serves the CLIENT-facing AI endpoints (the student/parent Flutter
// app reading a summary). Distinct from the admin AI endpoints (admin_ai.go) —
// different auth (UserAuth vs AdminAuth), different surface (read-only summary
// vs full CRUD+observability). Kept as its own handler to avoid bloating
// EpisodeHandler, which is already the largest client handler.
//
// Phase B only exposes summary reads. Phase C adds quiz endpoints here; Phase D
// adds chat.
type AIHandler interface {
	GetEpisodeSummary(c *gin.Context)
}

type aiHandler struct {
	aiService service.AIService
}

// NewAIHandler constructs an AIHandler. aiService may be nil in a build without
// AI wired; reads then 404 cleanly.
func NewAIHandler(aiService service.AIService) AIHandler {
	return &aiHandler{aiService: aiService}
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
