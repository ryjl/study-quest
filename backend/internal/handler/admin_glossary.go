package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"
)

// glossaryCandidateDTO is the JSON shape for a GlossaryCandidate row. Status is
// included so the UI can show pending/accepted/rejected badges and the review
// list can filter to pending by default.
type glossaryCandidateDTO struct {
	ID             uint    `json:"id"`
	CourseID       uint    `json:"course_id"`
	Original       string  `json:"original"`
	Corrected      string  `json:"corrected"`
	Context        string  `json:"context"`
	Confidence     float64 `json:"confidence"`
	EvidenceCount  int     `json:"evidence_count"`
	EvidenceSample string  `json:"evidence_sample,omitempty"`
	Status         string  `json:"status"`
	AcceptedAt     *string `json:"accepted_at,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

func toGlossaryCandidateDTO(r model.GlossaryCandidate) glossaryCandidateDTO {
	dto := glossaryCandidateDTO{
		ID:             r.ID,
		CourseID:       r.CourseID,
		Original:       r.Original,
		Corrected:      r.Corrected,
		Context:        r.Context,
		Confidence:     r.Confidence,
		EvidenceCount:  r.EvidenceCount,
		EvidenceSample: r.EvidenceSample,
		Status:         r.Status,
		CreatedAt:      r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      r.UpdatedAt.Format(time.RFC3339),
	}
	if r.AcceptedAt != nil {
		s := r.AcceptedAt.Format(time.RFC3339)
		dto.AcceptedAt = &s
	}
	return dto
}

// ListGlossaryCandidates returns a course's term-correction candidates mined
// by the polish job. ?status=pending|accepted|rejected filters (default: all,
// so the admin can also see past decisions). Ordered by confidence desc.
// GET /admin/api/courses/:id/glossary-candidates?status=pending
func (h *adminHandler) ListGlossaryCandidates(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	status := c.Query("status") // "" = all
	rows, err := h.aiService.ListGlossaryCandidates(courseID, status)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]glossaryCandidateDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toGlossaryCandidateDTO(r))
	}
	c.JSON(http.StatusOK, out)
}

// acceptGlossaryReq is the body for POST .../accept. corrected/context are
// OPTIONAL admin overrides — when empty, the candidate's existing values are
// used. apply_to_subject_siblings spreads the rule to every other course under
// the same subject, sparing per-course review.
type acceptGlossaryReq struct {
	Corrected              string `json:"corrected,omitempty"`
	Context                string `json:"context,omitempty"`
	ApplyToSubjectSiblings bool   `json:"apply_to_subject_siblings,omitempty"`
}

// AcceptGlossaryCandidate promotes one pending candidate into the course's
// TermDict so future polish runs apply it automatically. The admin may edit
// corrected/context before accepting. Optionally applies to sibling courses.
// POST /admin/api/glossary-candidates/:id/accept
func (h *adminHandler) AcceptGlossaryCandidate(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	var req acceptGlossaryReq
	// Body is optional (admin can accept with no overrides). Don't 400 on a
	// missing body — treat it as "accept as-is".
	_ = c.ShouldBindJSON(&req)
	if err := h.aiService.AcceptGlossaryCandidate(id, req.Corrected, req.Context, req.ApplyToSubjectSiblings); err != nil {
		switch {
		case errors.Is(err, repository.ErrGlossaryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "候选不存在"})
		case errors.Is(err, service.ErrGlossaryNotPending):
			c.JSON(http.StatusConflict, gin.H{"error": "该候选已审核过(已接受或已拒绝)"})
		default:
			respondError(c, err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RejectGlossaryCandidate marks one candidate rejected. The row stays (it's the
// dedup anchor for UpsertCandidate); it just leaves the default pending list.
// POST /admin/api/glossary-candidates/:id/reject
func (h *adminHandler) RejectGlossaryCandidate(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := h.aiService.RejectGlossaryCandidate(id); err != nil {
		switch {
		case errors.Is(err, repository.ErrGlossaryNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "候选不存在"})
		case errors.Is(err, service.ErrGlossaryNotPending):
			c.JSON(http.StatusConflict, gin.H{"error": "该候选已审核过(已接受或已拒绝)"})
		default:
			respondError(c, err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// acceptGlossaryBatchReq is the body for the batch-accept endpoint. Accepts
// the listed candidate ids in one request (each with its own optional override
// would be heavy; for now we accept all as-is — admin edits one at a time
// before batching). apply_to_subject_siblings applies to every accepted row.
type acceptGlossaryBatchReq struct {
	IDs                    []uint `json:"ids"`
	ApplyToSubjectSiblings bool   `json:"apply_to_subject_siblings,omitempty"`
}

// AcceptGlossaryCandidateBatch accepts multiple candidates in one call. Used
// by the admin UI's "批量接受选中" button. Each id is accepted as-is (no
// per-row overrides in the batch path — the admin who wants to edit accepts
// one at a time). Returns a per-id error map so the UI can show which rows
// failed (e.g. one was concurrently reviewed).
// POST /admin/api/glossary-candidates/accept-batch
func (h *adminHandler) AcceptGlossaryCandidateBatch(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	var req acceptGlossaryBatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids 不能为空"})
		return
	}
	errs := make(map[uint]string, len(req.IDs))
	accepted := make([]uint, 0, len(req.IDs))
	for _, id := range req.IDs {
		if err := h.aiService.AcceptGlossaryCandidate(id, "", "", req.ApplyToSubjectSiblings); err != nil {
			errs[id] = err.Error()
			continue
		}
		accepted = append(accepted, id)
	}
	c.JSON(http.StatusOK, gin.H{
		"accepted":   accepted,
		"errors":     errs,
		"ok":         len(errs) == 0,
	})
}
