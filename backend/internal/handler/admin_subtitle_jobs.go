package handler

import (
	"net/http"
	"strings"
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
	// Progress is the worker-reported transcription ratio (0.0..1.0), or null
	// when none has been reported. The queue view shows it for processing jobs.
	Progress        *float64   `json:"progress,omitempty"`
	DurationSeconds *int       `json:"duration_seconds,omitempty"`
	// SubtitleID is the id of the subtitle row this job produced (matched by
	// episode_id + language), or null when none exists. The queue UI uses this
	// to offer a "view generated subtitle" action on done rows.
	SubtitleID      *uint      `json:"subtitle_id,omitempty"`
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
		Progress:        r.Progress,
		DurationSeconds: r.DurationSeconds,
		CourseID:        r.EpisodeCourseID,
		SubtitleID:      r.SubtitleID,
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
	if !bindJSON(c, &req) { return }

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
	limit := parseLimit(c, 200, 500)
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

// ResetSubtitleJob is the admin "un-stick a processing job" action — the manual
// counterpart of the automatic reaper. Use case: the worker is alive but a
// relay/network call hung without crashing, so the job isn't stale enough for
// the reaper yet, but the admin has judged it stuck. Only valid on processing
// jobs; the service returns an error for other states which we surface as 409
// (so the UI can say "nothing to reset" rather than silently succeeding).
// POST /admin/api/subtitle-jobs/:id/reset
func (h *adminHandler) ResetSubtitleJob(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}
	if err := h.subtitleJobService.Reset(id); err != nil {
		// "only processing jobs can be reset" → 409; anything else → 500.
		// String match is brittle but mirrors the existing retry path's style;
		// a typed error would be cleaner but is out of scope for this addition.
		if strings.Contains(err.Error(), "only processing jobs") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
