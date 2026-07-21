package handler

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// Code split from admin_content.go for navigability.
// Chapter CRUD + reorder.

func (h *adminHandler) CreateChapter(c *gin.Context) {
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	var req struct {
		Title          string `json:"title" binding:"required"`
		Description    string `json:"description"`
		CoverURL       string `json:"cover_url"`
		AttachmentJSON string `json:"attachment_json"`
		SortOrder      int    `json:"sort_order"`
	}

	if !bindJSON(c, &req) { return }

	ch, err := h.chapterService.CreateChapter(courseID, req.Title, req.Description, req.CoverURL, req.AttachmentJSON, req.SortOrder)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, toChapterDTO(*ch))
}

func (h *adminHandler) UpdateChapter(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter ID"})
		return
	}

	var req struct {
		Title          string `json:"title" binding:"required"`
		Description    string `json:"description"`
		CoverURL       string `json:"cover_url"`
		AttachmentJSON string `json:"attachment_json"`
		SortOrder      int    `json:"sort_order"`
	}

	if !bindJSON(c, &req) { return }

	ch, err := h.chapterService.UpdateChapter(id, req.Title, req.Description, req.CoverURL, req.AttachmentJSON, req.SortOrder)
	if err != nil {
		respondError(c, err)
		return
	}
	if ch == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chapter not found"})
		return
	}

	c.JSON(http.StatusOK, toChapterDTO(*ch))
}

// ReorderChapters rewrites sort_order for the given chapter IDs (in order).
func (h *adminHandler) ReorderChapters(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}
	if !bindJSON(c, &req) { return }
	for i, id := range req.IDs {
		ch, err := h.chapterService.GetChapterByID(id)
		if err != nil {
			respondError(c, err)
			return
		}
		if ch == nil {
			continue
		}
		ch.SortOrder = i + 1
		if _, err := h.chapterService.UpdateChapter(id, ch.Title, ch.Description, ch.CoverURL, ch.AttachmentJSON, ch.SortOrder); err != nil {
			respondError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "reordered"})
}

func (h *adminHandler) DeleteChapter(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter ID"})
		return
	}

	if err := h.chapterService.DeleteChapter(id); err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) ListChaptersByCourse(c *gin.Context) {
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	chapters, err := h.chapterService.GetChaptersByCourse(courseID)
	if err != nil {
		respondError(c, err)
		return
	}

	out := make([]chapterDTO, 0, len(chapters))
	for _, ch := range chapters {
		out = append(out, toChapterDTO(ch))
	}
	c.JSON(http.StatusOK, out)
}
