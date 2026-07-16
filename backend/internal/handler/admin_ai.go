package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"
)

// aiProviderDTO is the JSON shape for an AIProvider row. The api_key field is
// handled specially on READ (never echoed back — see toAIProviderDTO) and on
// WRITE (empty on update = "don't change", see mergeAIProvider). This matches
// the Settings.tsx admin-password convention ("leave blank = don't modify").
type aiProviderDTO struct {
	ID           uint   `json:"id"`
	Capability   string `json:"capability"`    // chat | embedding | rerank
	Name         string `json:"name"`          // display name
	ProviderType string `json:"provider_type"` // openai_compat | onnx_local
	BaseURL      string `json:"base_url"`      // chat relay base; empty for onnx_local
	APIKey       string `json:"api_key"`       // write-only: never echoed back on read
	ModelName    string `json:"model_name"`    // model id (chat) or model dir (onnx)
	ExtraJSON    string `json:"extra_json,omitempty"`
	IsEnabled    bool   `json:"is_enabled"`
}

// toAIProviderDTO converts a model row to its DTO, STRIPPING the api_key. The
// key is a secret; the admin UI shows a masked "leave blank to keep" field
// rather than the real value, so the list/detail endpoints must never return
// it. (Same posture the admin-password path takes; plaintext-at-rest encryption
// is tracked as a separate cross-cutting task.)
func toAIProviderDTO(p model.AIProvider) aiProviderDTO {
	return aiProviderDTO{
		ID:           p.ID,
		Capability:   p.Capability,
		Name:         p.Name,
		ProviderType: p.ProviderType,
		BaseURL:      p.BaseURL,
		APIKey:       "", // never echo back
		ModelName:    p.ModelName,
		ExtraJSON:    p.ExtraJSON,
		IsEnabled:    p.IsEnabled,
	}
}

// ListAIProviders returns all configured AI providers.
// GET /admin/api/ai/providers
func (h *adminHandler) ListAIProviders(c *gin.Context) {
	if h.aiProviderRepo == nil {
		c.JSON(http.StatusOK, []aiProviderDTO{})
		return
	}
	providers, err := h.aiProviderRepo.List()
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]aiProviderDTO, 0, len(providers))
	for _, p := range providers {
		out = append(out, toAIProviderDTO(p))
	}
	c.JSON(http.StatusOK, out)
}

// CreateAIProvider creates a new AI provider config.
// POST /admin/api/ai/providers
func (h *adminHandler) CreateAIProvider(c *gin.Context) {
	if h.aiProviderRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI subsystem not configured"})
		return
	}
	var req aiProviderDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
	if msg := validateAIProvider(req, true); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	p := model.AIProvider{
		Capability:   req.Capability,
		Name:         req.Name,
		ProviderType: req.ProviderType,
		BaseURL:      req.BaseURL,
		APIKey:       req.APIKey,
		ModelName:    req.ModelName,
		ExtraJSON:    req.ExtraJSON,
		IsEnabled:    req.IsEnabled,
	}
	if err := h.aiProviderRepo.Create(&p); err != nil {
		respondError(c, err)
		return
	}
	h.invalidateAI(req.Capability)
	c.JSON(http.StatusOK, toAIProviderDTO(p))
}

// UpdateAIProvider updates an existing provider. A blank api_key in the request
// means "keep the existing secret" — this lets the admin edit other fields
// without re-entering the key every time (which they can't see).
// PUT /admin/api/ai/providers/:id
func (h *adminHandler) UpdateAIProvider(c *gin.Context) {
	if h.aiProviderRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI subsystem not configured"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	existing, err := h.aiProviderRepo.FindByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider 不存在"})
		return
	}
	var req aiProviderDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
	if msg := validateAIProvider(req, false); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}

	// Merge: overwrite mutable fields; preserve the existing key when the
	// request left api_key blank (the "don't modify" convention).
	existing.Capability = req.Capability
	existing.Name = req.Name
	existing.ProviderType = req.ProviderType
	existing.BaseURL = req.BaseURL
	existing.ModelName = req.ModelName
	existing.ExtraJSON = req.ExtraJSON
	existing.IsEnabled = req.IsEnabled
	if req.APIKey != "" {
		existing.APIKey = req.APIKey
	}

	if err := h.aiProviderRepo.Update(existing); err != nil {
		respondError(c, err)
		return
	}
	// Invalidate both old + new capability, since capability itself can change.
	h.invalidateAI(existing.Capability)
	h.invalidateAI(req.Capability)
	c.JSON(http.StatusOK, toAIProviderDTO(*existing))
}

// DeleteAIProvider removes a provider config.
// DELETE /admin/api/ai/providers/:id
func (h *adminHandler) DeleteAIProvider(c *gin.Context) {
	if h.aiProviderRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI subsystem not configured"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	// Look up capability before delete so we can invalidate the right cache slot.
	existing, _ := h.aiProviderRepo.FindByID(id)
	if err := h.aiProviderRepo.Delete(id); err != nil {
		respondError(c, err)
		return
	}
	if existing != nil {
		h.invalidateAI(existing.Capability)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// TestAIProvider tests connectivity for one provider by actually building it
// and calling Ping. For chat this sends a tiny completion to the relay; for
// embedding it loads the ONNX model and embeds one word. This is the most
// expensive admin action but the only way to truly verify the full path works.
// POST /admin/api/ai/providers/:id/test
func (h *adminHandler) TestAIProvider(c *gin.Context) {
	if h.aiProviderRepo == nil || h.aiResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI subsystem not configured"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	p, err := h.aiProviderRepo.FindByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider 不存在"})
		return
	}

	// Ping with a generous-but-bounded timeout: model load (ONNX) or a slow
	// relay can take a few seconds, but we don't want a stuck endpoint to hang
	// the admin UI forever.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// Rebuild fresh for the test (don't trust the cache — the admin may have
	// just edited the row and the cache may still hold the old instance).
	h.aiResolver.Invalidate(p.Capability)

	start := time.Now()
	var pingErr error
	switch p.Capability {
	case model.AICapabilityChat:
		var llm ai.LLMProvider
		llm, pingErr = h.aiResolver.ResolveChat()
		if pingErr == nil {
			pingErr = llm.Ping(ctx)
		}
	case model.AICapabilityEmbedding:
		var emb ai.Embedder
		emb, pingErr = h.aiResolver.ResolveEmbedder()
		if pingErr == nil {
			pingErr = emb.Ping(ctx)
		}
	case model.AICapabilityRerank:
		// Not wired in MVP; surface clearly rather than pretending.
		c.JSON(http.StatusOK, gin.H{"ok": false, "message": "rerank capability not implemented yet"})
		return
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "未知 capability: " + p.Capability})
		return
	}
	latency := time.Since(start).Milliseconds()

	if pingErr != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "message": pingErr.Error(), "latency_ms": latency})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "连接成功", "latency_ms": latency})
}

// GetAIStatus reports which capabilities have an enabled provider configured.
// Read-only (no building/pinging), so it's cheap and safe to call on every
// Settings page load. Used by the admin UI to show "chat: ready / embedding: 未配置".
// GET /admin/api/ai/status
func (h *adminHandler) GetAIStatus(c *gin.Context) {
	if h.aiResolver == nil {
		c.JSON(http.StatusOK, gin.H{"chat": false, "embedding": false, "rerank": false, "configured": false})
		return
	}
	chat, embed, err := h.aiResolver.IsReady()
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"chat":       chat,
		"embedding":  embed,
		"rerank":     false, // not implemented in MVP
		"configured": chat || embed,
	})
}

// --- helpers ---

// validateAIProvider checks required fields per capability. requireAPIKey is
// true only on CREATE: an update may legitimately omit the key (keep existing).
func validateAIProvider(req aiProviderDTO, requireAPIKey bool) string {
	if req.Name == "" {
		return "name 为必填"
	}
	if req.Capability != model.AICapabilityChat && req.Capability != model.AICapabilityEmbedding && req.Capability != model.AICapabilityRerank {
		return "capability 必须是 chat / embedding / rerank"
	}
	if req.ProviderType == "" {
		return "provider_type 为必填"
	}
	// Per-type required fields.
	switch req.ProviderType {
	case "openai_compat":
		if req.BaseURL == "" {
			return "openai_compat 需要填写 base_url"
		}
		if requireAPIKey && req.APIKey == "" {
			return "新建时 api_key 为必填"
		}
		if req.ModelName == "" {
			return "需要填写 model_name"
		}
	case "onnx_local":
		if req.ModelName == "" {
			return "onnx_local 需要填写 model_name(模型目录名)"
		}
	default:
		return "不支持的 provider_type: " + req.ProviderType + "(仅支持 openai_compat / onnx_local)"
	}
	return ""
}

// invalidateAI drops the cached provider for one capability, if a resolver is
// wired. Called after every provider mutation so the next resolve is fresh.
func (h *adminHandler) invalidateAI(capability string) {
	if h.aiResolver != nil {
		h.aiResolver.Invalidate(capability)
	}
}

// ---------------------------------------------------------------------------
// AI generation jobs + observability (Phase B)
// ---------------------------------------------------------------------------

// aiEnqueueRequest is the body for POST /admin/api/ai/jobs. The admin picks
// episodes from the course tree and requests a job type (segment/summary).
type aiEnqueueRequest struct {
	JobType    string `json:"job_type"`    // segment | summary
	EpisodeIDs []uint `json:"episode_ids"` // episodes to process
}

// aiEnqueueResponse reports per-episode outcomes so the admin UI can show
// "X enqueued, Y skipped (reason)" after a bulk trigger.
type aiEnqueueResponse struct {
	Enqueued []uint            `json:"enqueued"`
	Skipped  map[uint]string   `json:"skipped"`
}

// EnqueueAIJobs creates segment or summary jobs for a batch of episodes.
// POST /admin/api/ai/jobs
func (h *adminHandler) EnqueueAIJobs(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	var req aiEnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
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
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_type 必须是 segment 或 summary"})
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
		ID: j.ID, JobType: j.JobType, EpisodeID: j.EpisodeID, CourseID: j.CourseID,
		Status: j.Status, Attempt: j.Attempt, Error: j.Error, Progress: j.Progress,
		CreatedAt: j.CreatedAt.Format(time.RFC3339),
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
	runs, _ := h.aiService.ListRunsForJob(id)
	c.JSON(http.StatusOK, gin.H{"job": toAIJobDTO(*view), "runs": runs})
}

// ListAIRuns returns recent decision runs (across all jobs), newest first.
// GET /admin/api/ai/runs?limit=50
// This powers the "agent decision trace" panel — the observability centerpiece.
func (h *adminHandler) ListAIRuns(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, []model.AIRun{})
		return
	}
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	runs, err := h.aiService.ListRecentRuns(limit)
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

// ---------------------------------------------------------------------------
// Phase C — quiz observability (admin): per-user quizzes + detail + summaries
// ---------------------------------------------------------------------------

// GetAISummary serves a generated summary's content to the admin. The AI
// Workflow job view links a summary job to its episode; this endpoint lets the
// admin read the actual headline/key_points/concepts without switching to the
// client. GET /admin/api/ai/summaries/:episodeID
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
type courseSummaryAdminDTO struct {
	Status      string `json:"status"`             // ready | generating | ""(无总结未生成)
	SummaryText string `json:"summary_text,omitempty"`
	ModelUsed   string `json:"model_used,omitempty"`
	GeneratedAt string `json:"generated_at,omitempty"`
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
	if summary != nil {
		dto.Status = "ready"
		dto.SummaryText = summary.SummaryText
		dto.ModelUsed = summary.ModelUsed
		dto.GeneratedAt = summary.GeneratedAt.Format(time.RFC3339)
	} else if h.aiService.HasPendingCourseSummaryJob(courseID) {
		// 无总结但正在生成——前端据此显示 spinner 并继续轮询。
		dto.Status = "generating"
	}
	c.JSON(http.StatusOK, dto)
}

// ---------------------------------------------------------------------------
// Phase E — admin 用户学习报告(agent 驱动,跨课程画像)
// ---------------------------------------------------------------------------

// userStudyReportDTO 是 user_study_report 的 admin JSON 视图。status 让前端区分:
//   - ready:有报告(report 字段非空)
//   - generating:无报告 + 有在途 job(前端轮询)
//   - 空 status + 无 report:无报告也未生成(前端显示"生成报告"按钮)
type userStudyReportDTO struct {
	Status      string `json:"status"`             // ready | generating | ""(无报告未生成)
	Report      string `json:"report,omitempty"`   // 报告文本(ready 时有)
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
