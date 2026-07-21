package handler

import (
	"errors"
	"net/http"

	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// SubtitleJobHandler is the worker-facing protocol surface. It mounts under the
// same IngestKeyMiddleware group as the ingest endpoints (X-Ingest-Key header)
// — the subtitle worker is another member of the Python toolchain and shares
// that pre-shared key. None of these routes are admin-authenticated: the worker
// is a machine, not a browser session.
//
// Protocol (one round-trip per job):
//   POST /subtitle-jobs/claim        → {job, download_url, download_header, ...} | {job:null}
//   POST /subtitle-jobs/:id/complete ← {srt_content, language?, label?}
//   POST /subtitle-jobs/:id/heartbeat
//   POST /subtitle-jobs/:id/fail     ← {error}
//
// The download URL is minted fresh on each claim (alist signs expire), so the
// worker must use it promptly and never cache it across jobs.
type SubtitleJobHandler interface {
	Claim(c *gin.Context)
	Complete(c *gin.Context)
	Heartbeat(c *gin.Context)
	Fail(c *gin.Context)
}

type subtitleJobHandler struct {
	svc service.SubtitleJobService
}

// NewSubtitleJobHandler creates the worker-protocol handler.
func NewSubtitleJobHandler(svc service.SubtitleJobService) SubtitleJobHandler {
	return &subtitleJobHandler{svc: svc}
}

func (h *subtitleJobHandler) Claim(c *gin.Context) {
	// Worker self-identifies via X-Worker-ID (its hostname or a configured id).
	// Falls back to "anonymous" — the field is for observability only, not auth.
	workerID := c.GetHeader("X-Worker-ID")
	if workerID == "" {
		workerID = "anonymous"
	}
	res, err := h.svc.ClaimNext(workerID, c.Request.UserAgent())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "claim failed: " + err.Error()})
		return
	}
	if res == nil {
		// No queued work. The worker should back off (sleep) and poll again.
		c.JSON(http.StatusOK, gin.H{"job": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"job": gin.H{
			"id":         res.Job.ID,
			"episode_id": res.Job.EpisodeID,
			"language":   res.Job.Language,
			"attempt":    res.Job.Attempt,
			"claimed_by": res.Job.ClaimedBy,
		},
		"download_url":    res.DownloadURL,
		"download_header": res.DownloadHeader,
		"episode": gin.H{
			"id":               res.Job.EpisodeID,
			"title":            res.EpisodeTitle,
			"duration_seconds": res.DurationSec,
			// Cache-matching keys + Whisper prompt context (see ClaimResult docs).
			"filename":      res.Filename,
			"file_size":     res.FileSize,
			"subject":       res.Subject,
			"course_title":  res.CourseTitle,
			"chapter_title": res.ChapterTitle,
			// Whisper prompt context. Sourced from Course.EffectiveWhisperHint()
			// (reads AIConfigJSON, falls back to deprecated AIHint column).
			// The worker reads ONLY whisper_hint — the legacy ai_hint protocol
			// field was removed when the worker was upgraded in lockstep.
			"whisper_hint":  res.WhisperHint,
		},
	})
}

func (h *subtitleJobHandler) Complete(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}

	var req struct {
		SrtContent string `json:"srt_content" binding:"required"`
		Language   string `json:"language"`
		Label      string `json:"label"`
	}
	if !bindJSON(c, &req) { return }

	if err := h.svc.Complete(id, req.SrtContent, req.Language, req.Label); err != nil {
		if errors.Is(err, service.ErrSubtitleJobNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
			return
		}
		if errors.Is(err, service.ErrSubtitleJobStaleComplete) {
			// Not a worker error: another worker (or a reaper+re-claim) beat us
			// to it. Tell the worker to drop this SRT and move on — 409 so it can
			// distinguish "my result is stale" from a real server error.
			c.JSON(http.StatusConflict, gin.H{"error": "stale completion, discard SRT", "status": "stale"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "complete failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "done"})
}

func (h *subtitleJobHandler) Heartbeat(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}
	// Body is optional: a worker may POST an empty body to just refresh
	// claimed_at, or include progress_ratio (0.0..1.0) to report how far along
	// the transcription is. Absent/unset leaves the stored progress untouched.
	var req struct {
		ProgressRatio *float64 `json:"progress_ratio"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.Heartbeat(id, req.ProgressRatio); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "heartbeat failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *subtitleJobHandler) Fail(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}
	var req struct {
		Error string `json:"error"`
	}
	_ = c.ShouldBindJSON(&req) // body optional; absent error is allowed
	if err := h.svc.Fail(id, req.Error); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "fail failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "failed"})
}
