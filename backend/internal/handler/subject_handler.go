package handler

import (
	"errors"
	"net/http"
	"strconv"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// SubjectHandler handles admin CRUD and the client-facing list for Subjects.
type SubjectHandler interface {
	// Admin API endpoints
	AdminListSubjects(c *gin.Context)
	AdminCreateSubject(c *gin.Context)
	AdminUpdateSubject(c *gin.Context)
	AdminDeleteSubject(c *gin.Context)

	// Client API endpoint (Flutter / student-side filter dropdown)
	ClientListSubjects(c *gin.Context)
}

type subjectHandler struct {
	svc service.SubjectService
}

// NewSubjectHandler creates an instance of SubjectHandler.
func NewSubjectHandler(svc service.SubjectService) SubjectHandler {
	return &subjectHandler{svc: svc}
}

// AdminListSubjects GET /admin/api/subjects — returns all subjects ordered by sort_order.
func (h *subjectHandler) AdminListSubjects(c *gin.Context) {
	list, err := h.svc.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]subjectDTO, 0, len(list))
	for _, s := range list {
		out = append(out, toSubjectDTO(s))
	}
	c.JSON(http.StatusOK, out)
}

// ClientListSubjects GET /api/v1/subjects — same payload, public/restricted.
func (h *subjectHandler) ClientListSubjects(c *gin.Context) {
	list, err := h.svc.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]subjectDTO, 0, len(list))
	for _, s := range list {
		out = append(out, toSubjectDTO(s))
	}
	c.JSON(http.StatusOK, out)
}

// AdminCreateSubject POST /admin/api/subjects
func (h *subjectHandler) AdminCreateSubject(c *gin.Context) {
	var req struct {
		Key       string `json:"key" binding:"required"`
		Label     string `json:"label" binding:"required"`
		Emoji     string `json:"emoji"`
		Color     string `json:"color"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	subj, err := h.svc.Create(req.Key, req.Label, req.Emoji, req.Color, req.SortOrder)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toSubjectDTO(*subj))
}

// AdminUpdateSubject PUT /admin/api/subjects/:id
// If the Key changes, badges.rule_target is cascaded in the same transaction.
func (h *subjectHandler) AdminUpdateSubject(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subject ID"})
		return
	}

	var req struct {
		Key       string `json:"key" binding:"required"`
		Label     string `json:"label" binding:"required"`
		Emoji     string `json:"emoji"`
		Color     string `json:"color"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	subj, err := h.svc.FindByID(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if subj == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "subject not found"})
		return
	}

	oldKey := subj.Key
	subj.Key = req.Key
	subj.Label = req.Label
	subj.Emoji = req.Emoji
	subj.Color = req.Color
	subj.SortOrder = req.SortOrder

	if err := h.svc.Update(subj, oldKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toSubjectDTO(*subj))
}

// AdminDeleteSubject DELETE /admin/api/subjects/:id
// Returns 409 when courses still reference the subject (FK ON DELETE RESTRICT).
func (h *subjectHandler) AdminDeleteSubject(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subject ID"})
		return
	}

	if err := h.svc.Delete(uint(id)); err != nil {
		if errors.Is(err, service.ErrSubjectInUse) {
			c.JSON(http.StatusConflict, gin.H{
				"error": "该科目下还有课程，无法删除；请先迁移或删除这些课程后再试。",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
