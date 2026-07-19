package handler

import (
	"net/http"
	"strconv"
	"studyquest/backend/internal/model"
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
		respondError(c, err)
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
		respondError(c, err)
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
		Key       string            `json:"key" binding:"required"`
		Label     string            `json:"label" binding:"required"`
		Color     string            `json:"color"`
		SortOrder int               `json:"sort_order"`
		// AIConfig 是学科级默认 AI 提示(5 字段)。可选:不传(nil)→ 走空配置。
		AIConfig  *aiConfigRequest `json:"ai_config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	aiCfg, _ := req.AIConfig.toModel()
	subj, err := h.svc.Create(req.Key, req.Label, req.Color, req.SortOrder, aiCfg)
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
		Key       string            `json:"key" binding:"required"`
		Label     string            `json:"label" binding:"required"`
		Color     string            `json:"color"`
		SortOrder int               `json:"sort_order"`
		// AIConfig 是学科级默认 AI 提示(5 字段)。可选:nil → 保留原值(不动
		// AIConfigJSON);非 nil → 用请求体的 5 字段整体覆盖(全空即清空)。
		AIConfig  *aiConfigRequest `json:"ai_config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	subj, err := h.svc.FindByID(uint(id))
	if err != nil {
		respondError(c, err)
		return
	}
	if subj == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "subject not found"})
		return
	}

	oldKey := subj.Key
	subj.Key = req.Key
	subj.Label = req.Label
	subj.Color = req.Color
	subj.SortOrder = req.SortOrder
	// 仅当请求显式带 ai_config 时才覆盖;老客户端不传 → 保留已有配置。
	if req.AIConfig != nil {
		subj.SetAIConfig(model.AIConfig{
			WhisperHint: req.AIConfig.WhisperHint,
			SummaryHint: req.AIConfig.SummaryHint,
			QuizHint:    req.AIConfig.QuizHint,
			AdviceHint:  req.AIConfig.AdviceHint,
			TermDict:    req.AIConfig.TermDict,
		})
	}

	if err := h.svc.Update(subj, oldKey); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSubjectDTO(*subj))
}

// AdminDeleteSubject DELETE /admin/api/subjects/:id
// Returns 403 for system-seeded subjects (IsSystem), 409 when courses still
// reference the subject (FK ON DELETE RESTRICT).
func (h *subjectHandler) AdminDeleteSubject(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subject ID"})
		return
	}

	if err := h.svc.Delete(uint(id)); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
