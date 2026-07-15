package handler

import (
	"net/http"
	"strconv"
	"time"

	"studyquest/backend/internal/repository"

	"github.com/gin-gonic/gin"
)

// subtitleJobDTO is one row of the admin queue view. It carries the job's own
// fields plus the joined episode title (so the list renders without an N+1).
type subtitleJobDTO struct {
	ID              uint       `json:"id"`
	EpisodeID       uint       `json:"episode_id"`
	EpisodeTitle    string     `json:"episode_title"`
	CourseID        *uint      `json:"course_id,omitempty"`
	Status          string     `json:"status"`
	Priority        int        `json:"priority"`
	Attempt         int        `json:"attempt"`
	Language        string     `json:"language"`
	ClaimedBy       string     `json:"claimed_by,omitempty"`
	ClaimedAt       *string    `json:"claimed_at,omitempty"`
	CompletedAt     *string    `json:"completed_at,omitempty"`
	Error           string     `json:"error,omitempty"`
	DurationSeconds *int       `json:"duration_seconds,omitempty"`
	CreatedAt       string     `json:"created_at"`
	UpdatedAt       string     `json:"updated_at"`
}

func toSubtitleJobDTO(r repository.SubtitleJobWithEpisode) subtitleJobDTO {
	dto := subtitleJobDTO{
		ID:              r.ID,
		EpisodeID:       r.EpisodeID,
		Status:          r.Status,
		Priority:        r.Priority,
		Attempt:         r.Attempt,
		Language:        r.Language,
		ClaimedBy:       r.ClaimedBy,
		Error:           r.Error,
		DurationSeconds: r.DurationSeconds,
		CourseID:        r.EpisodeCourseID,
		CreatedAt:       formatTime(r.CreatedAt),
		UpdatedAt:       formatTime(r.UpdatedAt),
	}
	if r.EpisodeTitle != nil {
		dto.EpisodeTitle = *r.EpisodeTitle
	}
	if r.ClaimedAt != nil {
		s := r.ClaimedAt.Format(time.RFC3339)
		dto.ClaimedAt = &s
	}
	if r.CompletedAt != nil {
		s := r.CompletedAt.Format(time.RFC3339)
		dto.CompletedAt = &s
	}
	return dto
}

// EnqueueSubtitleJobs is the admin bulk-action: opt the selected episodes into
// the subtitle queue. Returns which were enqueued and which were skipped (with
// a machine reason code the SPA turns into toast text), so the operator sees
// "added 3, skipped 2 (already have subtitles)" rather than a silent partial.
func (h *adminHandler) EnqueueSubtitleJobs(c *gin.Context) {
	var req struct {
		EpisodeIDs []uint `json:"episode_ids" binding:"required"`
		Priority   int    `json:"priority"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload: " + err.Error()})
		return
	}

	enqueued, skipped, reasons, err := h.subtitleJobService.EnqueueBatch(req.EpisodeIDs, req.Priority)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "enqueue failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":   "ok",
		"enqueued": enqueued,
		"skipped":  skipped,
		"reasons":  reasons,
	})
}

// ListSubtitleJobs returns the queue for the admin view. Optional ?status=
// filter; ?limit= caps the row count (default 200).
func (h *adminHandler) ListSubtitleJobs(c *gin.Context) {
	status := c.Query("status")
	limit := 200
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	rows, err := h.subtitleJobService.ListQueue(status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list failed: " + err.Error()})
		return
	}
	out := make([]subtitleJobDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSubtitleJobDTO(r))
	}
	c.JSON(http.StatusOK, out)
}

// SkipSubtitleJob is the admin "give up on this one" action (terminal).
func (h *adminHandler) SkipSubtitleJob(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}
	if err := h.subtitleJobService.Skip(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "skip failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "skipped"})
}

// RetrySubtitleJob moves a failed/skipped job back to queued. The attempt count
// is preserved so the operator can see it has been tried before.
func (h *adminHandler) RetrySubtitleJob(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}
	if err := h.subtitleJobService.Retry(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "queued"})
}

// SubtitleJobStats is the progress snapshot polled by the admin UI (mirrors the
// probe-progress polling pattern: poll while running, back off when idle).
func (h *adminHandler) SubtitleJobStats(c *gin.Context) {
	stats, err := h.subtitleJobService.GetStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stats failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}
