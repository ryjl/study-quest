package handler

import (
	"errors"
	"net/http"
	"studyquest/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// Code split from admin_content.go for navigability.
// Episode CRUD + bulk ops + list.

func (h *adminHandler) CreateEpisode(c *gin.Context) {
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	var req struct {
		ChapterID         uint   `json:"chapter_id"`
		Title             string `json:"title" binding:"required"`
		VideoRelativePath string `json:"video_relative_path" binding:"required"`
		AttachmentJSON    string `json:"attachment_json"`
		SortOrder         int    `json:"sort_order"`
	}

	if !bindJSON(c, &req) { return }

	if req.AttachmentJSON == "" {
		req.AttachmentJSON = "[]"
	}

	var chapterIDPtr *uint
	if req.ChapterID > 0 {
		chapterIDPtr = &req.ChapterID
	}

	ep, err := h.episodeService.CreateEpisode(
		courseID,
		chapterIDPtr,
		req.Title,
		req.VideoRelativePath,
		req.AttachmentJSON,
		req.SortOrder,
		"", nil, nil,
	)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, toEpisodeDTO(*ep))
}

func (h *adminHandler) UpdateEpisode(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid episode ID"})
		return
	}

	var req struct {
		ChapterID         uint   `json:"chapter_id"`
		Title             string `json:"title" binding:"required"`
		VideoRelativePath string `json:"video_relative_path" binding:"required"`
		SortOrder         int    `json:"sort_order"`
	}

	if !bindJSON(c, &req) { return }

	var chapterIDPtr *uint
	if req.ChapterID > 0 {
		chapterIDPtr = &req.ChapterID
	}

	// Use the PATCH-style admin update so media metadata is never clobbered.
	ep, err := h.episodeService.UpdateEpisodeAdmin(id, chapterIDPtr, req.Title, req.VideoRelativePath, req.SortOrder)
	if err != nil {
		respondError(c, err)
		return
	}
	if ep == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Episode not found"})
		return
	}

	c.JSON(http.StatusOK, toEpisodeDTO(*ep))
}

func (h *adminHandler) DeleteEpisode(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid episode ID"})
		return
	}

	if err := h.episodeService.DeleteEpisode(id); err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) ReorderEpisodes(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}

	if !bindJSON(c, &req) { return }

	if err := h.episodeService.ReorderEpisodes(req.IDs); err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "reordered"})
}

func (h *adminHandler) BulkDeleteEpisodes(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}

	if !bindJSON(c, &req) { return }

	for _, id := range req.IDs {
		if err := h.episodeService.DeleteEpisode(id); err != nil {
			respondError(c, err)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) BulkMoveEpisodes(c *gin.Context) {
	var req struct {
		IDs       []uint `json:"ids" binding:"required"`
		ChapterID uint   `json:"chapter_id"`
	}

	if !bindJSON(c, &req) { return }

	if err := h.episodeService.BulkMoveEpisodes(req.IDs, req.ChapterID); err != nil {
		// Cross-course / not-found are bad-request class; surface the
		// explanatory message so the admin knows which episode/chapter clashed.
		if errors.Is(err, service.ErrEpisodeMoveCrossCourse) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "moved"})
}

// Chapter Controllers
func (h *adminHandler) ListEpisodesByCourse(c *gin.Context) {
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	episodes, err := h.episodeService.GetEpisodesByCourse(courseID)
	if err != nil {
		respondError(c, err)
		return
	}

	out := make([]episodeDTO, 0, len(episodes))
	for _, ep := range episodes {
		out = append(out, toEpisodeDTO(ep))
	}
	// Stamp subtitle_count in one batch query (avoids an N+1 across episodes).
	ids := make([]uint, 0, len(episodes))
	for _, ep := range episodes {
		ids = append(ids, ep.ID)
	}
	if counts, cerr := h.episodeRepo.CountSubtitlesByEpisodes(ids); cerr == nil {
		withSubtitleCounts(out, counts)
	}
	c.JSON(http.StatusOK, out)
}

// Subtitle Controllers
