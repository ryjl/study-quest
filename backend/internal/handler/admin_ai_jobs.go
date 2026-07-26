package handler

import (
	"net/http"
	"time"
	"github.com/gin-gonic/gin"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"
)

// Code split from admin_ai.go for navigability.

type aiEnqueueRequest struct {
	JobType    string `json:"job_type"`    // segment | summary | polish | homework
	EpisodeIDs []uint `json:"episode_ids"` // episodes to process
}

// aiEnqueueResponse reports per-episode outcomes so the admin UI can show
// "X enqueued, Y skipped (reason)" after a bulk trigger.
type aiEnqueueResponse struct {
	Enqueued []uint          `json:"enqueued"`
	Skipped  map[uint]string `json:"skipped"`
}

// EnqueueAIJobs creates segment or summary jobs for a batch of episodes.
// POST /admin/api/ai/jobs
func (h *adminHandler) EnqueueAIJobs(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	var req aiEnqueueRequest
	if !bindJSON(c, &req) { return }
	if len(req.EpisodeIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "episode_ids 不能为空"})
		return
	}
	var enqueued []uint
	var skipped map[uint]string
	var err error
	switch req.JobType {
	case "segment":
		enqueued, skipped, err = h.aiService.EnqueueSegment(req.EpisodeIDs)
	case "summary":
		enqueued, skipped, err = h.aiService.EnqueueSummary(req.EpisodeIDs)
	case "polish":
		enqueued, skipped, err = h.aiService.EnqueuePolish(req.EpisodeIDs)
	case "homework":
		// v2:作业勾选式批量入队。与 segment/summary/polish 三兄弟并列,前端零改动地
		// 复用 POST /admin/api/ai/jobs 端点(只多一个 case)。旧的 course-level 端点
		// POST /admin/api/ai/courses/:id/homework/generate 标废弃但不清(二期再删)。
		enqueued, skipped, err = h.aiService.EnqueueHomework(req.EpisodeIDs)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_type 必须是 segment / summary / polish / homework"})
		return
	}
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, aiEnqueueResponse{Enqueued: enqueued, Skipped: skipped})
}

// aiJobDTO is the admin-facing job view. Includes episode/course ids AND the
// resolved display names (episode_title/course_title/user_nickname) so the UI
// renders titles instead of bare ids. Name resolution happens in the service
// layer (see service.AIJobView); the handler just projects it to JSON.
type aiJobDTO struct {
	ID           uint     `json:"id"`
	JobType      string   `json:"job_type"`
	EpisodeID    uint     `json:"episode_id"`
	CourseID     uint     `json:"course_id"`
	Status       string   `json:"status"`
	Attempt      int      `json:"attempt"`
	Error        string   `json:"error,omitempty"`
	Progress     *float64 `json:"progress,omitempty"`
	CreatedAt    string   `json:"created_at"`
	CompletedAt  string   `json:"completed_at,omitempty"`
	EpisodeTitle string   `json:"episode_title,omitempty"`
	CourseTitle  string   `json:"course_title,omitempty"`
	UserNickname string   `json:"user_nickname,omitempty"`
}

func toAIJobDTO(v service.AIJobView) aiJobDTO {
	j := v.Job
	d := aiJobDTO{
		ID: j.ID, JobType: j.JobType,
		// EpisodeID/CourseID 在 model.AIJob 是 *uint(subject 级 advice job 为 nil)。
		// DTO 保持 uint 契约稳定(老前端读 0 不读 null),ptrVal 把 nil 转 0。
		// subject 级 job 显示 episode_id=0 / episode_title="" 正常(无对应实体)。
		EpisodeID: model.PtrVal(j.EpisodeID), CourseID: model.PtrVal(j.CourseID),
		Status: j.Status, Attempt: j.Attempt, Error: j.Error, Progress: j.Progress,
		CreatedAt:    j.CreatedAt.Format(time.RFC3339),
		EpisodeTitle: v.EpisodeTitle, CourseTitle: v.CourseTitle, UserNickname: v.UserNickname,
	}
	if j.CompletedAt != nil {
		d.CompletedAt = j.CompletedAt.Format(time.RFC3339)
	}
	return d
}

// ListAIJobs lists AI jobs, optionally filtered by job_type and/or status.
// GET /admin/api/ai/jobs?job_type=summary&status=failed
func (h *adminHandler) ListAIJobs(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, []aiJobDTO{})
		return
	}
	jobType := c.Query("job_type")
	status := c.Query("status")
	views, err := h.aiService.ListJobs(jobType, status, 100)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]aiJobDTO, 0, len(views))
	for _, v := range views {
		out = append(out, toAIJobDTO(v))
	}
	// Include stats so the UI can show counts without a second request.
	stats, _ := h.aiService.JobStats()
	c.JSON(http.StatusOK, gin.H{"jobs": out, "stats": stats})
}

// GetAIJob returns one job (for a detail view).
// GET /admin/api/ai/jobs/:id
func (h *adminHandler) GetAIJob(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	view, err := h.aiService.GetJob(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if view == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job 不存在"})
		return
	}
	// Include the decision runs for this job so the detail view can replay them.
	// Enriched 携带 episode/course/user 标题,让详情页的 runs 列表也能看到"在哪节课"。
	runs, _ := h.aiService.ListRunsForJobEnriched(id)
	c.JSON(http.StatusOK, gin.H{"job": toAIJobDTO(*view), "runs": runs})
}

// ListAIRuns returns recent decision runs (across all jobs), newest first.
// GET /admin/api/ai/runs?limit=50
// This powers the "agent decision trace" panel — the observability centerpiece.
// 返回 AIRunView(带 episode_title/course_title/user_nickname),让决策痕迹表
// 和 Dashboard 最近活动能展示课程/课时,不只是 capability + #job_id。
func (h *adminHandler) ListAIRuns(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, []service.AIRunView{})
		return
	}
	limit := parseLimit(c, 50, 500)
	runs, err := h.aiService.ListRecentRunsEnriched(limit)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, runs)
}

// GetAIRun returns one decision run's full detail (prompt/response/usage).
// GET /admin/api/ai/runs/:id
func (h *adminHandler) GetAIRun(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	run, err := h.aiService.GetRun(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if run == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run 不存在"})
		return
	}
	c.JSON(http.StatusOK, run)
}

// ResetAIJob manually resets one 'processing' AI job back to 'queued' on admin
// demand — the manual counterpart of the automatic reaper. Use case: the worker
// is alive but a relay call hung without crashing, so the job isn't stale
// enough for the 30min reaper yet, but the admin has judged it stuck. Clears
// claimed_at + error so the next worker poll re-claims it cleanly.
// POST /admin/api/ai/jobs/:id/reset
func (h *adminHandler) ResetAIJob(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := h.aiService.ResetJob(id); err != nil {
		// ErrJobNotProcessing is non-fatal: the job already finished or was
		// reaped. Surface 409 so the UI can say "nothing to reset" rather than
		// silently pretending success (which would hide a double-reset).
		if err == repository.ErrJobNotProcessing {
			c.JSON(http.StatusConflict, gin.H{"error": "任务不在处理中(可能已完成或已被重置)"})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RetryAIJob revives one 'failed' AI job back to 'queued' so the worker re-runs
// it. Use case: a job failed (e.g. embedding/chat provider was misconfigured),
// the admin fixed the underlying problem, now they want to re-run instead of
// leaving it failed forever. This is the ONLY way to revive a failed job —
// failJob marks jobs failed without auto-retry (AI calls cost money, so we don't
// loop on a bad config). Clears error + claimed_at; the next worker poll
// (3s) re-claims it.
// POST /admin/api/ai/jobs/:id/retry
func (h *adminHandler) RetryAIJob(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := h.aiService.RetryJob(id); err != nil {
		// ErrJobNotFailed is non-fatal: the job isn't failed (it succeeded, is
		// queued/processing, or was already retried). Surface 409 so the UI can
		// say "nothing to retry" rather than silently pretending success.
		if err == repository.ErrJobNotFailed {
			c.JSON(http.StatusConflict, gin.H{"error": "任务不是失败状态(可能已成功或已被重试)"})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// SkipPolishAIJob is the polish-specific escape hatch. A failed polish job
// HALTS the downstream chain (segment never auto-enqueues). When the admin
// decides polish isn't worth fixing (raw subtitle is good enough, or the
// provider issue can't be resolved), this endpoint marks the job done and
// chains segment so AI proceeds off the raw text. Only valid on a FAILED
// POLISH job — other states/types return 409 so the UI can hide the button.
// POST /admin/api/ai/jobs/:id/skip-polish
func (h *adminHandler) SkipPolishAIJob(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := h.aiService.SkipPolish(id); err != nil {
		switch err {
		case repository.ErrJobNotFound:
			c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		case repository.ErrJobNotPolish:
			c.JSON(http.StatusConflict, gin.H{"error": "该任务不是 polish 任务"})
		case repository.ErrJobNotFailed:
			c.JSON(http.StatusConflict, gin.H{"error": "任务不是失败状态,无需跳过"})
		default:
			respondError(c, err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------------------------------------------------------------------
// Phase C — quiz observability (admin): per-user quizzes + detail + summaries
// ---------------------------------------------------------------------------

// GetAISummary serves a generated summary's content to the admin. The AI
// Workflow job view links a summary job to its episode; this endpoint lets the
// admin read the actual headline/key_points/concepts without switching to the
