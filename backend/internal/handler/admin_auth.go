package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"studyquest/backend/internal/storage"
)


func (h *adminHandler) LoginAPI(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	savedHash, err := h.settingsRepo.Get("admin_password_hash")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error checking password"})
		return
	}

	// Default fallback password for initial setup. Only generate when there's
	// truly no hash yet, AND require the write to succeed before using it — if
	// we generated a fresh random hash every login (salt differs each call) but
	// Set silently failed, the cookie (the generated hash) would never match
	// what's in the DB and the session would be rejected on the next request.
	// Re-reading after Set guarantees cookie == stored value.
	if savedHash == "" {
		defaultHash, _ := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		if err := h.settingsRepo.Set("admin_password_hash", string(defaultHash), "Admin panel password hash"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "初始化默认密码失败"})
			return
		}
		// Re-read so we set the cookie to exactly what's persisted (avoids any
		// divergence between the in-memory hash and the DB row).
		savedHash, _ = h.settingsRepo.Get("admin_password_hash")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(savedHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "密码错误"})
		return
	}

	c.SetCookie("admin_session", savedHash, 86400, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// LogoutAPI clears the admin session cookie (JSON version).
func (h *adminHandler) LogoutAPI(c *gin.Context) {
	c.SetCookie("admin_session", "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Me reports the current admin session state. The SPA calls this on boot to
// decide between routing to the login page or the dashboard.
func (h *adminHandler) Me(c *gin.Context) {
	cookie, err := c.Cookie("admin_session")
	if err != nil || cookie == "" {
		c.JSON(http.StatusOK, gin.H{"authenticated": false})
		return
	}
	savedHash, _ := h.settingsRepo.Get("admin_password_hash")
	if savedHash == "" || cookie != savedHash {
		c.JSON(http.StatusOK, gin.H{"authenticated": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"authenticated": true})
}

// DashboardStats aggregates the headline numbers + charts shown on the
// admin landing page. Each stat is a single SQL query so the page stays snappy.
// A failure in any one stat degrades to a zero value rather than 500-ing the
// whole dashboard, but each error is logged so silent breakage is visible.
func (h *adminHandler) GetSettings(c *gin.Context) {
	all, err := h.settingsRepo.GetAll()
	if err != nil {
		respondError(c, err)
		return
	}
	get := func(k string) string {
		if v, ok := all[k]; ok {
			return v
		}
		return ""
	}
	c.JSON(http.StatusOK, gin.H{
		"storage_type":     get("storage_type"),
		"storage_url":      get("storage_url"),
		"storage_username": get("storage_username"),
		"storage_password": get("storage_password"),
		"storage_token":    get("storage_token"),
	})
}

// UserLedger returns a paginated slice of a user's point transactions.
func (h *adminHandler) UpdateSettings(c *gin.Context) {
	var req struct {
		StorageType     string `json:"storage_type"`
		StorageURL      string `json:"storage_url"`
		StorageUsername string `json:"storage_username"`
		StoragePassword string `json:"storage_password"`
		StorageToken    string `json:"storage_token"`
		AdminPassword   string `json:"admin_password"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	_ = h.settingsRepo.Set("storage_type", req.StorageType, "Storage provider type (alist/webdav)")
	_ = h.settingsRepo.Set("storage_url", req.StorageURL, "AList/WebDAV service base endpoint URL")
	_ = h.settingsRepo.Set("storage_username", req.StorageUsername, "Basic authentication username")
	_ = h.settingsRepo.Set("storage_password", req.StoragePassword, "Basic authentication password")
	_ = h.settingsRepo.Set("storage_token", req.StorageToken, "AList API Authorization Token")
	if req.AdminPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.AdminPassword), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt password"})
			return
		}
		_ = h.settingsRepo.Set("admin_password_hash", string(hash), "Admin panel password hash")
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Settings updated successfully"})
}

func (h *adminHandler) PingStorage(c *gin.Context) {
	// Support testing unsaved settings directly from the form
	var req struct {
		StorageType     string `json:"storage_type"`
		StorageURL      string `json:"storage_url"`
		StorageUsername string `json:"storage_username"`
		StoragePassword string `json:"storage_password"`
		StorageToken    string `json:"storage_token"`
	}

	// Try to bind JSON body. If it binds successfully and has a URL, we test it on the fly
	if c.Request.Method == "POST" {
		if err := c.ShouldBindJSON(&req); err == nil && req.StorageURL != "" {
			var provider storage.StorageProvider
			if req.StorageType == "alist" {
				provider = storage.NewAListProvider(req.StorageURL, req.StorageUsername, req.StoragePassword, req.StorageToken)
			} else if req.StorageType == "webdav" {
				provider = storage.NewWebDAVProvider(req.StorageURL, req.StorageUsername, req.StoragePassword)
			}

			if provider != nil {
				if err := provider.Ping(); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "status": "failed"})
					return
				}
				c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Successfully connected to storage source (测试通过)"})
				return
			}
		}
	}

	// Default fallback to saved settings
	if err := h.importService.PingStorage(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "status": "failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Successfully connected to storage source"})
}
