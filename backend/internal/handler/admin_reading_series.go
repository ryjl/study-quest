package handler

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// Code split from admin_reading.go for navigability.
// Reading series CRUD.

func (h *adminHandler) ListReadingSeries(c *gin.Context) {
	series, err := h.readingSeriesRepo.List("", 0, nil)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]readingSeriesDTO, 0, len(series))
	for _, s := range series {
		out = append(out, h.toReadingSeriesDTO(s))
	}
	c.JSON(http.StatusOK, out)
}

func (h *adminHandler) GetReadingSeriesDetail(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid series ID"})
		return
	}
	series, err := h.readingSeriesRepo.FindByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if series == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Series not found"})
		return
	}
	books, _ := h.readingBookRepo.ListBySeries(id)
	articles, _ := h.readingArticleRepo.ListBySeries(id)
	bookDTOs := make([]readingBookDTO, 0, len(books))
	for _, b := range books {
		bookDTOs = append(bookDTOs, h.toReadingBookDTO(b))
	}
	articleDTOs := make([]readingArticleDTO, 0, len(articles))
	for _, a := range articles {
		articleDTOs = append(articleDTOs, h.toReadingArticleDTO(a))
	}
	c.JSON(http.StatusOK, gin.H{
		"series":   h.toReadingSeriesDTO(*series),
		"books":    bookDTOs,
		"articles": articleDTOs,
	})
}

func (h *adminHandler) CreateReadingSeries(c *gin.Context) {
	var req struct {
		Title       string `json:"title" binding:"required"`
		Description string `json:"description"`
		Grade       string `json:"grade"`
		Subject     string `json:"subject" binding:"required"`
		CoverURL    string `json:"cover_url"`
		SortOrder   int    `json:"sort_order"`
		TagIDs      []uint `json:"tag_ids"`
	}
	if !bindJSON(c, &req) { return }
	subjectID, err := h.resolveSubjectID(req.Subject)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	series, err := h.readingSeriesService.CreateSeries(req.Title, req.Description, parseGrades(req.Grade), subjectID, req.CoverURL, req.SortOrder, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.toReadingSeriesDTO(*series))
}

func (h *adminHandler) UpdateReadingSeries(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid series ID"})
		return
	}
	var req struct {
		Title       string `json:"title" binding:"required"`
		Description string `json:"description"`
		Grade       string `json:"grade"`
		Subject     string `json:"subject" binding:"required"`
		CoverURL    string `json:"cover_url"`
		SortOrder   int    `json:"sort_order"`
		TagIDs      []uint `json:"tag_ids"`
	}
	if !bindJSON(c, &req) { return }
	subjectID, err := h.resolveSubjectID(req.Subject)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	series, err := h.readingSeriesService.UpdateSeries(id, req.Title, req.Description, parseGrades(req.Grade), subjectID, req.CoverURL, req.SortOrder, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	if series == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Series not found"})
		return
	}
	c.JSON(http.StatusOK, h.toReadingSeriesDTO(*series))
}

func (h *adminHandler) DeleteReadingSeries(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid series ID"})
		return
	}
	if err := h.readingSeriesService.DeleteSeries(id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ── Books ──
