package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"studyquest/backend/internal/model"
)

// storageSourceDTO is the JSON shape for a StorageSource row. Mirrors the model
// field-for-field; IsDefault lets the SPA mark the default row in the list.
type storageSourceDTO struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // "alist" | "webdav"
	URL       string `json:"url"`
	Username  string `json:"username"`
	Password  string `json:"password"` // returned as-is (plaintext; encryption is a separate PR)
	Token     string `json:"token"`
	IsDefault bool   `json:"is_default"`
}

func toStorageSourceDTO(s model.StorageSource) storageSourceDTO {
	return storageSourceDTO{
		ID: s.ID, Name: s.Name, Type: s.Type, URL: s.URL,
		Username: s.Username, Password: s.Password, Token: s.Token,
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
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
	src.Name = req.Name
	src.Type = req.Type
	src.URL = req.URL
	src.Username = req.Username
	src.Password = req.Password
	src.Token = req.Token
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

// DeleteStorageSource deletes a storage source by id.
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	if err := h.storageSourceRepo.SetWhitelist(id, req.SourceIDs); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
