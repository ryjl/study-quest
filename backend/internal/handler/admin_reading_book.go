package handler

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// Code split from admin_reading.go for navigability.
// Reading book CRUD.

func (h *adminHandler) ListReadingBooks(c *gin.Context) {
	books, err := h.readingBookRepo.List("", 0, nil, false)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]readingBookDTO, 0, len(books))
	for _, b := range books {
		out = append(out, h.toReadingBookDTO(b))
	}
	c.JSON(http.StatusOK, out)
}

func (h *adminHandler) CreateReadingBook(c *gin.Context) {
	var req struct {
		SeriesID         uint   `json:"series_id"`
		SortOrder        int    `json:"sort_order"`
		Title            string `json:"title" binding:"required"`
		FileRelativePath string `json:"file_relative_path" binding:"required"`
		CoverURL         string `json:"cover_url"`
		Grade            string `json:"grade"`
		Subject          string `json:"subject" binding:"required"`
		TagIDs           []uint `json:"tag_ids"`
	}
	if !bindJSON(c, &req) { return }
	subjectID, err := h.resolveSubjectID(req.Subject)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	book, err := h.readingBookService.CreateBook(req.SeriesID, req.SortOrder, req.Title, req.FileRelativePath, req.CoverURL, parseGrades(req.Grade), subjectID, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.toReadingBookDTO(*book))
}

func (h *adminHandler) UpdateReadingBook(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book ID"})
		return
	}
	var req struct {
		SeriesID         uint   `json:"series_id"`
		SortOrder        int    `json:"sort_order"`
		Title            string `json:"title" binding:"required"`
		FileRelativePath string `json:"file_relative_path" binding:"required"`
		CoverURL         string `json:"cover_url"`
		Grade            string `json:"grade"`
		Subject          string `json:"subject" binding:"required"`
		TagIDs           []uint `json:"tag_ids"`
	}
	if !bindJSON(c, &req) { return }
	subjectID, err := h.resolveSubjectID(req.Subject)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	book, err := h.readingBookService.UpdateBook(id, req.SeriesID, req.SortOrder, req.Title, req.FileRelativePath, req.CoverURL, parseGrades(req.Grade), subjectID, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	if book == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}
	c.JSON(http.StatusOK, h.toReadingBookDTO(*book))
}

func (h *adminHandler) DeleteReadingBook(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book ID"})
		return
	}
	if err := h.readingBookService.DeleteBook(id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ── Articles ──
