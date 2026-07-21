package handler

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// Code split from admin_reading.go for navigability.
// Reading article CRUD.

func (h *adminHandler) ListReadingArticles(c *gin.Context) {
	articles, err := h.readingArticleRepo.List("", 0, nil, false)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]readingArticleDTO, 0, len(articles))
	for _, a := range articles {
		out = append(out, h.toReadingArticleDTO(a))
	}
	c.JSON(http.StatusOK, out)
}

func (h *adminHandler) CreateReadingArticle(c *gin.Context) {
	var req struct {
		SeriesID         uint   `json:"series_id"`
		SortOrder        int    `json:"sort_order"`
		Title            string `json:"title" binding:"required"`
		SourceURL        string `json:"source_url" binding:"required"`
		WhitelistDomains string `json:"whitelist_domains"`
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
	article, err := h.readingArticleService.CreateArticle(req.SeriesID, req.SortOrder, req.Title, req.SourceURL, normalizeWhitelistJSON(req.WhitelistDomains), req.CoverURL, parseGrades(req.Grade), subjectID, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.toReadingArticleDTO(*article))
}

func (h *adminHandler) UpdateReadingArticle(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid article ID"})
		return
	}
	var req struct {
		SeriesID         uint   `json:"series_id"`
		SortOrder        int    `json:"sort_order"`
		Title            string `json:"title" binding:"required"`
		SourceURL        string `json:"source_url" binding:"required"`
		WhitelistDomains string `json:"whitelist_domains"`
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
	article, err := h.readingArticleService.UpdateArticle(id, req.SeriesID, req.SortOrder, req.Title, req.SourceURL, normalizeWhitelistJSON(req.WhitelistDomains), req.CoverURL, parseGrades(req.Grade), subjectID, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	if article == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Article not found"})
		return
	}
	c.JSON(http.StatusOK, h.toReadingArticleDTO(*article))
}

func (h *adminHandler) DeleteReadingArticle(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid article ID"})
		return
	}
	if err := h.readingArticleService.DeleteArticle(id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ── Access ──

// GrantReadingAccess grants a user access to a reading resource. target_type is
// "series" | "book" | "article".
