package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)


func (h *adminHandler) LoginAPI(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	if !bindJSON(c, &req) { return }

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
	// Storage connection config moved to storage_sources (multi-source). The
	// only thing surfaced here is whether an admin password is set, so the SPA
	// can show a hint, plus the polish_concurrency knob (so the AI 性能 card can
	// display the current value).
	hash, _ := h.settingsRepo.Get("admin_password_hash")
	polishConc := h.settingsRepo.GetWithDefault("polish_concurrency", "3")
	c.JSON(http.StatusOK, gin.H{
		"has_admin_password":  hash != "",
		"polish_concurrency":  polishConc,
	})
}

// UserLedger returns a paginated slice of a user's point transactions.
func (h *adminHandler) UpdateSettings(c *gin.Context) {
	var req struct {
		AdminPassword     string `json:"admin_password"`
		PolishConcurrency *int   `json:"polish_concurrency"` // pointer: distinguish "omitted" from "0"
	}

	if !bindJSON(c, &req) { return }

	if req.AdminPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.AdminPassword), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt password"})
			return
		}
		_ = h.settingsRepo.Set("admin_password_hash", string(hash), "Admin panel password hash")
	}

	// polish_concurrency: 1~10，默认 3。用指针区分"未提交该字段"(不改)和"显式
	// 给了值"。越界返回 400——虽然 polish.go 会把 <1 clamp 成 1,但这里直接拒更
	// 诚实,让 admin 知道填错了。
	if req.PolishConcurrency != nil {
		conc := *req.PolishConcurrency
		if conc < 1 || conc > 10 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "polish_concurrency 必须在 1~10 之间"})
			return
		}
		if err := h.settingsRepo.Set("polish_concurrency", strconv.Itoa(conc),
			"Polish LLM concurrency (in-flight chunk calls per job)"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save polish_concurrency"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Settings updated successfully"})
}
