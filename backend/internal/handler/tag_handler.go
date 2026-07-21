package handler

import (
	"net/http"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// TagHandler handles admin CRUD and the client-facing list for Tags.
type TagHandler interface {
	AdminListTags(c *gin.Context)
	AdminCreateTag(c *gin.Context)
	AdminUpdateTag(c *gin.Context)
	AdminDeleteTag(c *gin.Context)
	ClientListTags(c *gin.Context)
}

type tagHandler struct {
	svc service.TagService
}

// NewTagHandler creates an instance of TagHandler.
func NewTagHandler(svc service.TagService) TagHandler {
	return &tagHandler{svc: svc}
}

// AdminListTags GET /admin/api/tags — includes a per-tag course_count (how
// many courses use each tag) so the admin table can show the delete blast
// radius without an N+1 lookup.
func (h *tagHandler) AdminListTags(c *gin.Context) {
	list, err := h.svc.List()
	if err != nil {
		respondError(c, err)
		return
	}
	counts, err := h.svc.BatchCourseCounts()
	if err != nil {
		// Non-fatal: counts just read as 0 everywhere.
		counts = nil
	}
	out := make([]tagDTO, 0, len(list))
	for _, t := range list {
		out = append(out, toTagDTOWithCount(t, counts))
	}
	c.JSON(http.StatusOK, out)
}

// ClientListTags GET /api/v1/tags — same payload, for the Flutter filter chips.
func (h *tagHandler) ClientListTags(c *gin.Context) {
	list, err := h.svc.List()
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]tagDTO, 0, len(list))
	for _, t := range list {
		out = append(out, toTagDTO(t))
	}
	c.JSON(http.StatusOK, out)
}

// AdminCreateTag POST /admin/api/tags
func (h *tagHandler) AdminCreateTag(c *gin.Context) {
	var req struct {
		Key       string `json:"key" binding:"required"`
		Label     string `json:"label" binding:"required"`
		Color     string `json:"color"`
		SortOrder int    `json:"sort_order"`
	}
	if !bindJSON(c, &req) { return }
	tag, err := h.svc.Create(req.Key, req.Label, req.Color, req.SortOrder)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toTagDTO(*tag))
}

// AdminUpdateTag PUT /admin/api/tags/:id
// Courses store tag IDs, so renaming the label or key requires no cascade.
func (h *tagHandler) AdminUpdateTag(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tag ID"})
		return
	}
	var req struct {
		Key       string `json:"key" binding:"required"`
		Label     string `json:"label" binding:"required"`
		Color     string `json:"color"`
		SortOrder int    `json:"sort_order"`
	}
	if !bindJSON(c, &req) { return }
	tag, err := h.svc.FindByID(uint(id))
	if err != nil {
		respondError(c, err)
		return
	}
	if tag == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "tag not found"})
		return
	}
	tag.Key = req.Key
	tag.Label = req.Label
	tag.Color = req.Color
	tag.SortOrder = req.SortOrder
	if err := h.svc.Update(tag); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTagDTO(*tag))
}

// AdminDeleteTag DELETE /admin/api/tags/:id
// System-seeded tags (IsSystem=true) are refused with 403; everything else is
// deletable and the course_tags join rows are cleared by ON DELETE CASCADE
// (i.e. the tag is detached from every course that used it).
func (h *tagHandler) AdminDeleteTag(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tag ID"})
		return
	}
	if err := h.svc.Delete(uint(id)); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
