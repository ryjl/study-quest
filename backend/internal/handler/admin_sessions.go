package handler

import (
	"net/http"
	"strings"
	"time"

	"studyquest/backend/internal/model"

	"github.com/gin-gonic/gin"
)

// sessionResponse is the admin-facing view of a login session. The full token
// is returned (needed to target a row for revoke/note edits); a short prefix is
// included for UI display convenience. This payload is only ever sent to the
// authenticated admin panel.
type sessionResponse struct {
	Token       string    `json:"token"`
	TokenPrefix string    `json:"token_prefix"` // first 8 chars, for UI display
	UserID      uint      `json:"user_id"`
	DeviceName  string    `json:"device_name"`
	UserAgent   string    `json:"user_agent"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func toSessionResponse(s model.Session) sessionResponse {
	prefix := s.Token
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return sessionResponse{
		Token:       s.Token,
		TokenPrefix: prefix,
		UserID:      s.UserID,
		DeviceName:  s.DeviceName,
		UserAgent:   s.UserAgent,
		Note:        s.Note,
		CreatedAt:   s.CreatedAt,
		LastSeenAt:  s.LastSeenAt,
		ExpiresAt:   s.ExpiresAt,
	}
}

// ListUserSessions GET /admin/api/users/:id/sessions
// Returns the user's active (non-expired) device sessions, newest first.
func (h *adminHandler) ListUserSessions(c *gin.Context) {
	uid, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	sessions, err := h.sessionService.ListDevices(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sessions"})
		return
	}
	out := make([]sessionResponse, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, toSessionResponse(s))
	}
	c.JSON(http.StatusOK, out)
}

// RevokeUserSession DELETE /admin/api/users/:id/sessions/:token
// Revokes a single device session. Idempotent: deleting an already-absent
// token still returns 200 (admin UI may double-click or the row may have
// expired between list and click).
func (h *adminHandler) RevokeUserSession(c *gin.Context) {
	token := strings.TrimSpace(c.Param("token"))
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing session token"})
		return
	}
	if err := h.sessionService.Revoke(token); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke session"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// RevokeAllUserSessions DELETE /admin/api/users/:id/sessions
// Revokes every active session for a user — i.e. signs them out of all devices.
func (h *adminHandler) RevokeAllUserSessions(c *gin.Context) {
	uid, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	if err := h.sessionService.RevokeAllByUser(uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke sessions"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// UpdateSessionNote PATCH /admin/api/sessions/:token/note
// Sets the admin-editable note on a device session (e.g. "客厅那台 iPad").
// Body: {"note": "..."}; empty note clears it.
func (h *adminHandler) UpdateSessionNote(c *gin.Context) {
	token := strings.TrimSpace(c.Param("token"))
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing session token"})
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := h.sessionService.SetDeviceNote(token, req.Note); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update note"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
