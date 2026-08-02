package handler

import (
	"errors"
	"net/http"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// GradeHandler handles admin CRUD for grade tags (the open-tag-system management surface).
// Grades get created implicitly when a course/reading item is saved with a new tag value;
// this handler is for the LATER operations the admin needs once tags accumulate: list with
// usage counts, rename (fix typo), merge (dedupe), delete (cleanup).
//
// Endpoints (all admin-session-protected):
//   GET    /admin/api/grades            — list preset + custom tags with counts
//   PUT    /admin/api/grades/:key       — rename a custom tag (body: {new_key})
//   POST   /admin/api/grades/merge   — merge one tag into another (body: {from, to})
//   DELETE /admin/api/grades/:key      — delete a 0-use custom tag
type GradeHandler interface {
	ListGrades(c *gin.Context)
	RenameGrade(c *gin.Context)
	MergeGrades(c *gin.Context)
	DeleteGrade(c *gin.Context)
}

type gradeHandler struct {
	svc service.GradeService
}

// NewGradeHandler creates a GradeHandler. svc may be nil in degenerate builds; methods
// return 503 in that case.
func NewGradeHandler(svc service.GradeService) GradeHandler {
	return &gradeHandler{svc: svc}
}

// gradeUsageDTO is one row of the grade management table: the tag value, its localized
// label (for presets), the reference count across all four grade tables, and whether
// it's a system preset (which locks rename/delete).
type gradeUsageDTO struct {
	Grade    string `json:"grade"`
	Label   string `json:"label"`    // localized preset label, or the raw tag for customs
	Count   int64  `json:"count"`
	IsPreset bool  `json:"is_preset"`
}

// ListGrades: GET /admin/api/grades
//
// Returns preset tags (in model.PresetGrades order) followed by custom tags
// (alphabetical). Presets with 0 references still appear — they're always-available
// options the GradePicker shows as checkboxes. The admin UI renders this as a table
// with rename/merge/delete actions gated on is_preset + count.
func (h *gradeHandler) ListGrades(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "grade 子系统未配置"})
		return
	}
	usages, err := h.svc.ListAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gradeUsageDTO, 0, len(usages))
	for _, u := range usages {
		// Localize preset tags via the model helper; custom tags render as-is.
		label := u.Grade
		if u.IsPreset {
			if l := model.PresetGradeLabel(model.Grade(u.Grade)); l != "" {
				label = l
			}
		}
		out = append(out, gradeUsageDTO{
			Grade:    u.Grade,
			Label:   label,
			Count:   u.Count,
			IsPreset: u.IsPreset,
		})
	}
	c.JSON(http.StatusOK, out)
}

// RenameGrade: PUT /admin/api/grades/:key  body: {new_key}
//
// Renames a CUSTOM tag across all four grade tables (transactional). Preset tags
// return 409 (use Merge to migrate their rows instead). The source tag must exist.
func (h *gradeHandler) RenameGrade(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "grade 子系统未配置"})
		return
	}
	from := c.Param("key")
	var req struct {
		NewKey string `json:"new_key" binding:"required"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.Rename(from, req.NewKey); err != nil {
		respondGradeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "renamed"})
}

// MergeGrades: POST /admin/api/grades/merge  body: {from, to}
//
// Moves every row tagged `from` over to `to`. Unlike Rename, `from` MAY be a
// preset — this is the migration path for deprecated presets (e.g. historical "adult"
// rows → "college"). `to` must be a preset or an existing custom tag.
func (h *gradeHandler) MergeGrades(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "grade 子系统未配置"})
		return
	}
	var req struct {
		From string `json:"from" binding:"required"`
		To   string `json:"to" binding:"required"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.Merge(req.From, req.To); err != nil {
		respondGradeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "merged"})
}

// DeleteGrade: DELETE /admin/api/grades/:key
//
// Removes every row tagged `grade`. Refuses presets (409) and tags still in use
// (409 — merge first). Only useful for cleaning up a custom tag that ended up at
// Count==0.
func (h *gradeHandler) DeleteGrade(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "grade 子系统未配置"})
		return
	}
	grade := c.Param("key")
	if err := h.svc.Delete(grade); err != nil {
		respondGradeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// respondGradeError maps the service-layer sentinel errors to HTTP statuses.
// ErrGradeIsPreset / ErrGradeInUse are 409 (admin can resolve via merge/delete
// of a custom tag instead); ErrGradeNotFound is 404 (the tag isn't there to
// act on). Input validation errors (empty new_key, merge-into-a-nonexistent-
// tag) and unexpected DB errors fall through to 400. Mirrors the
// subject_handler / ai_jobs error-mapping style.
func respondGradeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrGradeIsPreset):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrGradeNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrGradeInUse):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, repository.ErrGlossaryNotFound):
		// Defensive: shouldn't happen here, but keep the mapping consistent.
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
}
