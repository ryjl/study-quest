package handler

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"studyquest/backend/internal/model"
)

// storageSourceDTO is the JSON shape for a StorageSource row. Password and
// Token are write-only secrets: they are NEVER echoed back on read (see
// toStorageSourceDTO, which returns them empty) and on update an empty value
// means "keep the existing secret" (see UpdateStorageSource). This mirrors the
// AIProvider api_key handling in admin_ai.go and the admin-password convention
// ("leave blank = don't modify"). At-rest encryption is a separate PR.
type storageSourceDTO struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // "alist" | "webdav"
	URL       string `json:"url"`
	Username  string `json:"username"`
	Password  string `json:"password"` // write-only: never echoed back on read
	Token     string `json:"token"`     // write-only: never echoed back on read
	IsDefault bool   `json:"is_default"`
}

// toStorageSourceDTO converts a model row to its DTO, STRIPPING Password and
// Token. These are secrets; the admin UI shows masked "leave blank to keep"
// fields rather than the real values, so the list/detail endpoints must never
// return them. (Same posture as toAIProviderDTO; plaintext-at-rest encryption
// is tracked as a separate cross-cutting task.)
func toStorageSourceDTO(s model.StorageSource) storageSourceDTO {
	return storageSourceDTO{
		ID: s.ID, Name: s.Name, Type: s.Type, URL: s.URL,
		Username:  s.Username,
		Password:  "", // never echo back
		Token:     "", // never echo back
		IsDefault: s.IsDefault,
	}
}

// ListStorageSources returns all configured storage sources (default first).
// GET /admin/api/storage-sources
func (h *adminHandler) ListStorageSources(c *gin.Context) {
	if h.storageSourceRepo == nil {
		c.JSON(http.StatusOK, []storageSourceDTO{})
		return
	}
	sources, err := h.storageSourceRepo.List()
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]storageSourceDTO, 0, len(sources))
	for _, s := range sources {
		out = append(out, toStorageSourceDTO(s))
	}
	c.JSON(http.StatusOK, out)
}

// CreateStorageSource creates a new storage source. If is_default is true, any
// previously-default row is unflagged first so at most one default exists.
// POST /admin/api/storage-sources
func (h *adminHandler) CreateStorageSource(c *gin.Context) {
	if h.storageSourceRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage source feature not configured"})
		return
	}
	var req storageSourceDTO
	if !bindJSON(c, &req) { return }
	if req.Name == "" || req.Type == "" || req.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name, type, url 为必填"})
		return
	}
	if req.Type != "alist" && req.Type != "webdav" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type 必须是 alist 或 webdav"})
		return
	}
	src := model.StorageSource{
		Name: req.Name, Type: req.Type, URL: req.URL,
		Username: req.Username, Password: req.Password, Token: req.Token,
		IsDefault: req.IsDefault,
	}
	if req.IsDefault {
		if err := h.storageSourceRepo.ClearDefault(); err != nil {
			respondError(c, err)
			return
		}
	}
	if err := h.storageSourceRepo.Create(&src); err != nil {
		respondError(c, err)
		return
	}
	if h.storageResolver != nil {
		h.storageResolver.Invalidate(src.ID)
	}
	c.JSON(http.StatusOK, toStorageSourceDTO(src))
}

// UpdateStorageSource updates an existing storage source by id.
// PUT /admin/api/storage-sources/:id
func (h *adminHandler) UpdateStorageSource(c *gin.Context) {
	if h.storageSourceRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage source feature not configured"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid source ID"})
		return
	}
	var req storageSourceDTO
	if !bindJSON(c, &req) { return }
	src, err := h.storageSourceRepo.FindByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if src == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "storage source not found"})
		return
	}
	src.Name = req.Name
	src.Type = req.Type
	src.URL = req.URL
	src.Username = req.Username
	// "blank = keep" convention (same as AIProvider.api_key): the admin UI can't
	// see the secret (GET strips it), so it sends an empty string for Password
	// and Token when the operator didn't retype them. An empty incoming value
	// means "don't modify" — only overwrite when a non-empty value arrives.
	if req.Password != "" {
		src.Password = req.Password
	}
	if req.Token != "" {
		src.Token = req.Token
	}
	if req.IsDefault && !src.IsDefault {
		if err := h.storageSourceRepo.ClearDefault(); err != nil {
			respondError(c, err)
			return
		}
	}
	src.IsDefault = req.IsDefault
	if err := h.storageSourceRepo.Update(src); err != nil {
		respondError(c, err)
		return
	}
	if h.storageResolver != nil {
		h.storageResolver.Invalidate(src.ID)
	}
	c.JSON(http.StatusOK, toStorageSourceDTO(*src))
}

// DeleteStorageSource deletes a storage source by id. It REFUSES (409) if any
// episode or reading book still references the source — deleting a source in
// use would orphan those rows (source_id has no FK, so nothing cascades) and
// silently break their playback + disaster-recovery lookup. The 409 body names
// the counts so the admin knows what to migrate/delete first.
// DELETE /admin/api/storage-sources/:id
func (h *adminHandler) DeleteStorageSource(c *gin.Context) {
	if h.storageSourceRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage source feature not configured"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid source ID"})
		return
	}
	episodes, books, err := h.storageSourceRepo.CountReferences(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if episodes > 0 || books > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":    fmt.Sprintf("该存储源被 %d 个课时、%d 本书引用，请先迁移或删除这些内容", episodes, books),
			"episodes": episodes,
			"books":    books,
		})
		return
	}
	if err := h.storageSourceRepo.Delete(id); err != nil {
		respondError(c, err)
		return
	}
	if h.storageResolver != nil {
		h.storageResolver.Invalidate(id)
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// PingStorageSource tests connectivity to a storage source by id. Builds a
// provider via the resolver and calls Ping(). Used by the SPA's per-source
// "测试连接" button.
// POST /admin/api/storage-sources/:id/ping
func (h *adminHandler) PingStorageSource(c *gin.Context) {
	if h.storageSourceRepo == nil || h.storageResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage source feature not configured"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid source ID"})
		return
	}
	src, err := h.storageSourceRepo.FindByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if src == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "storage source not found"})
		return
	}
	provider, perr := h.storageResolver.Resolve(&src.ID)
	if perr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "failed", "error": perr.Error()})
		return
	}
	if err := provider.Ping(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "failed", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "连接成功"})
}

// GetStorageWhitelist returns the user's storage-source whitelist (source id
// array). Empty = unrestricted.
// GET /admin/api/users/:id/storage-whitelist
func (h *adminHandler) GetStorageWhitelist(c *gin.Context) {
	if h.storageSourceRepo == nil {
		c.JSON(http.StatusOK, []uint{})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	wl, err := h.storageSourceRepo.WhitelistForUser(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if wl == nil {
		wl = []uint{}
	}
	c.JSON(http.StatusOK, wl)
}

// SetStorageWhitelist replaces the user's storage-source whitelist wholesale.
// An empty array clears it (restoring the unrestricted state).
// PUT /admin/api/users/:id/storage-whitelist
func (h *adminHandler) SetStorageWhitelist(c *gin.Context) {
	if h.storageSourceRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage source feature not configured"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	var req struct {
		SourceIDs []uint `json:"source_ids"`
	}
	if !bindJSON(c, &req) { return }
	if err := h.storageSourceRepo.SetWhitelist(id, req.SourceIDs); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
