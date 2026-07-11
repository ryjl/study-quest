package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// UnlockHandler handles admin CRUD for course unlock templates and per-(user,
// course) overrides, plus the student-facing unlock-status endpoint is on
// CourseHandler (it needs the user auth context).
type UnlockHandler interface {
	// Template (course-level default strategy)
	GetTemplate(c *gin.Context)
	SaveTemplate(c *gin.Context)
	DeleteTemplate(c *gin.Context)

	// Override (per user, course)
	ListUserOverrides(c *gin.Context)
	GetOverride(c *gin.Context)
	SaveOverride(c *gin.Context)
	DeleteOverride(c *gin.Context)

	// Manual unlock bump (manual/interval/weekly strategies)
	ManualUnlock(c *gin.Context)
	// ManualUnlockUndo reverses one accidental +1 (admin tapped too many
	// times). Floors the manual count at 0; never affects the automatic level.
	ManualUnlockUndo(c *gin.Context)

	// Allowlist replace (selected strategy, or additive elsewhere)
	SetAllowedEpisodes(c *gin.Context)

	// Preview the resolved visibility for a (user, course) — powers the admin
	// "this student currently sees X/Y episodes" readout.
	UnlockPreview(c *gin.Context)
}

type unlockHandler struct {
	svc service.UnlockService
}

// NewUnlockHandler creates an instance of UnlockHandler.
func NewUnlockHandler(svc service.UnlockService) UnlockHandler {
	return &unlockHandler{svc: svc}
}

// weeklyTimeDTO mirrors model.WeeklyTime over the wire (snake-ish json tags
// kept short for the admin SPA form payload).
type weeklyTimeDTO struct {
	Weekday int `json:"weekday"`
	Hour    int `json:"hour"`
	Minute  int `json:"minute"`
}

func parseWeeklyTimes(raw any) ([]model.WeeklyTime, error) {
	if raw == nil {
		return nil, nil
	}
	// Accept either []interface{} (loose JSON) or []weeklyTimeDTO.
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var dtos []weeklyTimeDTO
	if err := json.Unmarshal(buf, &dtos); err != nil {
		return nil, err
	}
	out := make([]model.WeeklyTime, len(dtos))
	for i, d := range dtos {
		out[i] = model.WeeklyTime{Weekday: d.Weekday, Hour: d.Hour, Minute: d.Minute}
	}
	return out, nil
}

// templateRequest is the shared body for SaveTemplate / SaveOverride.
type templateRequest struct {
	Strategy       string `json:"strategy"`
	IntervalSeconds int   `json:"interval_seconds"`
	WeeklyTimes    any    `json:"weekly_times"`
	AllowedIDs     []uint `json:"allowed_episode_ids"`
}

// ---- Template ----

func (h *unlockHandler) GetTemplate(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID"})
		return
	}
	t, err := h.svc.GetTemplate(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if t == nil {
		c.JSON(http.StatusOK, gin.H{"course_id": id, "strategy": model.StrategyAllOpen, "exists": false})
		return
	}
	c.JSON(http.StatusOK, unlockTemplateDTO(t, true))
}

func (h *unlockHandler) SaveTemplate(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID"})
		return
	}
	var req templateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	wt, perr := parseWeeklyTimes(req.WeeklyTimes)
	if perr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid weekly_times: " + perr.Error()})
		return
	}
	t, err := h.svc.SaveTemplate(id, req.Strategy, req.IntervalSeconds, wt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, unlockTemplateDTO(t, true))
}

func (h *unlockHandler) DeleteTemplate(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID"})
		return
	}
	if err := h.svc.DeleteTemplate(id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ---- Override ----

func (h *unlockHandler) ListUserOverrides(c *gin.Context) {
	uid, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user ID"})
		return
	}
	list, err := h.svc.ListOverridesByUser(uid)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, o := range list {
		out = append(out, unlockOverrideDTO(&o))
	}
	c.JSON(http.StatusOK, out)
}

func (h *unlockHandler) GetOverride(c *gin.Context) {
	uid, cid, ok := parseUserCourseParams(c)
	if !ok {
		return
	}
	o, err := h.svc.GetOverride(uid, cid)
	if err != nil {
		respondError(c, err)
		return
	}
	if o == nil {
		c.JSON(http.StatusOK, gin.H{"user_id": uid, "course_id": cid, "exists": false})
		return
	}
	c.JSON(http.StatusOK, unlockOverrideDTO(o))
}

func (h *unlockHandler) SaveOverride(c *gin.Context) {
	uid, cid, ok := parseUserCourseParams(c)
	if !ok {
		return
	}
	var req templateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	wt, perr := parseWeeklyTimes(req.WeeklyTimes)
	if perr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid weekly_times: " + perr.Error()})
		return
	}
	allowed := req.AllowedIDs
	if allowed == nil {
		allowed = []uint{}
	}
	o, err := h.svc.SaveOverride(uid, cid, req.Strategy, req.IntervalSeconds, wt, allowed)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, unlockOverrideDTO(o))
}

func (h *unlockHandler) DeleteOverride(c *gin.Context) {
	uid, cid, ok := parseUserCourseParams(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteOverride(uid, cid); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *unlockHandler) ManualUnlock(c *gin.Context) {
	uid, cid, ok := parseUserCourseParams(c)
	if !ok {
		return
	}
	if err := h.svc.IncrementManualUnlock(uid, cid); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ManualUnlockUndo reverses one accidental +1. Idempotent at the floor: calling
// it when the manual count is already 0 is a no-op (still returns 200), so the
// admin UI's "−1" button can be tapped without worrying about a 404/error when
// there's nothing left to undo.
func (h *unlockHandler) ManualUnlockUndo(c *gin.Context) {
	uid, cid, ok := parseUserCourseParams(c)
	if !ok {
		return
	}
	if err := h.svc.DecrementManualUnlock(uid, cid); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *unlockHandler) SetAllowedEpisodes(c *gin.Context) {
	uid, cid, ok := parseUserCourseParams(c)
	if !ok {
		return
	}
	var req struct {
		AllowedEpisodeIDs []uint `json:"allowed_episode_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	ids := req.AllowedEpisodeIDs
	if ids == nil {
		ids = []uint{}
	}
	if err := h.svc.SetAllowedEpisodes(uid, cid, ids); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *unlockHandler) UnlockPreview(c *gin.Context) {
	uid, cid, ok := parseUserCourseParams(c)
	if !ok {
		return
	}
	vis, err := h.svc.ResolveVisibleEpisodes(uid, cid)
	if err != nil {
		respondError(c, err)
		return
	}
	nextStr := ""
	if vis.NextUnlockAt != nil {
		nextStr = vis.NextUnlockAt.In(appclock.Zone()).Format("2006-01-02 15:04")
	}
	c.JSON(http.StatusOK, gin.H{
		"visible_ids":     vis.VisibleIDs,
		"visible_count":   len(vis.VisibleIDs),
		"total":           vis.Total,
		"unlocked_n":      vis.UnlockedN,
		"strategy":        vis.Strategy,
		"strategy_label":  vis.StrategyLabel,
		"next_unlock_at":  nextStr,
	})
}

// ---- DTO helpers ----

func unlockTemplateDTO(t *model.CourseUnlockTemplate, exists bool) gin.H {
	wt := []weeklyTimeDTO{}
	if t.WeeklyTimesJSON != "" {
		_ = json.Unmarshal([]byte(t.WeeklyTimesJSON), &wt)
	}
	return gin.H{
		"course_id":        t.CourseID,
		"strategy":         t.Strategy,
		"interval_seconds": t.IntervalSeconds,
		"weekly_times":     wt,
		"exists":           exists,
	}
}

func unlockOverrideDTO(o *model.UserUnlockOverride) gin.H {
	allowed := []uint{}
	if o.AllowedEpisodeIDsJSON != "" {
		_ = json.Unmarshal([]byte(o.AllowedEpisodeIDsJSON), &allowed)
	}
	wt := []weeklyTimeDTO{}
	if o.WeeklyTimesJSON != "" {
		_ = json.Unmarshal([]byte(o.WeeklyTimesJSON), &wt)
	}
	return gin.H{
		"user_id":            o.UserID,
		"course_id":          o.CourseID,
		"strategy":           o.Strategy,
		"interval_seconds":   o.IntervalSeconds,
		"weekly_times":       wt,
		"manual_unlock_count": o.ManualUnlockCount,
		"allowed_episode_ids": allowed,
		"exists":             true,
	}
}

// ---- param parsing helpers ----

// parseUserCourseParams reads :id (user) and :cid (course) from the route and
// writes the error response on failure. Returns ok=false if the caller should
// abort. parseUintParam is defined in admin_handler.go and reused here.
func parseUserCourseParams(c *gin.Context) (uid, cid uint, ok bool) {
	u, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user ID"})
		return 0, 0, false
	}
	cc, err := strconv.ParseUint(c.Param("cid"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID"})
		return 0, 0, false
	}
	return uint(u), uint(cc), true
}
