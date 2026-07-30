package handler

import (
	"errors"
	"net/http"
	"strings"

	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// UserHandler manages auth and endpoint requests from mobile PAD.
type UserHandler interface {
	GetUsers(c *gin.Context)
	Login(c *gin.Context)
	Logout(c *gin.Context)
}

type userHandler struct {
	userService    service.UserService
	sessionService service.SessionService
}

// NewUserHandler creates an instance of UserHandler. sessionService is used to
// issue/revoke the opaque login token returned to the client.
func NewUserHandler(us service.UserService, ss service.SessionService) UserHandler {
	return &userHandler{userService: us, sessionService: ss}
}

func (h *userHandler) GetUsers(c *gin.Context) {
	users, err := h.userService.GetUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
		return
	}

	// Filter out sensitive PIN hash from client response
	type userResponse struct {
		ID        uint   `json:"id"`
		Nickname  string `json:"nickname"`
		AvatarURL string `json:"avatar_url"`
		Role      string `json:"role"`
		Grade     string `json:"grade"`
	}

	res := make([]userResponse, len(users))
	for i, u := range users {
		res[i] = userResponse{
			ID:        u.ID,
			Nickname:  u.Nickname,
			AvatarURL: u.AvatarURL,
			Role:      u.Role,
			Grade:     u.Grade,
		}
	}

	c.JSON(http.StatusOK, res)
}

func (h *userHandler) Login(c *gin.Context) {
	var req struct {
		UserID     uint   `json:"user_id" binding:"required"`
		Pin        string `json:"pin" binding:"required"`
		DeviceName string `json:"device_name"` // optional; OS-level device label for the admin device list
	}

	if !bindJSON(c, &req) {
		return
	}

	valid, err := h.userService.Authenticate(req.UserID, req.Pin)
	if err != nil {
		// Account lockout is surfaced distinctly (429, matching the per-IP
		// limiter) so a legitimate user who fat-fingered into a lockout gets a
		// hint to wait rather than a generic "incorrect PIN".
		if errors.Is(err, service.ErrAccountLocked) {
			c.Header("Retry-After", "900") // 15 min hint; matches default lockout window
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "account temporarily locked due to repeated failed logins, please try again later",
			})
			return
		}
		// Any other failure to look up the user (deleted account, DB error) is
		// surfaced as a generic 401 — both to avoid distinguishing "no such
		// user" from "wrong PIN" for an attacker, and because a 500 here would
		// leak that the userID is the lookup key.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "incorrect user PIN code"})
		return
	}

	if !valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "incorrect user PIN code"})
		return
	}

	// Resolve role/grade for the response (Authenticate returns only bool).
	// Reusing the existing GetUsers list keeps this PR scoped; a dedicated
	// GetByID on the service would be a cleaner follow-up.
	var role, grade string
	if users, lerr := h.userService.GetUsers(); lerr == nil {
		for _, u := range users {
			if u.ID == req.UserID {
				role = u.Role
				grade = u.Grade
				break
			}
		}
	}

	// Issue an opaque session token. Each login = a new session row, so the
	// same user logging in on a second device does NOT invalidate the first.
	token, ierr := h.sessionService.Issue(req.UserID, req.DeviceName, c.Request.UserAgent())
	if ierr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":   token, // opaque session token (NOT the user ID)
		"role":    role,
		"grade":   grade,
		"user_id": req.UserID,
	})
}

// Logout revokes the caller's current session. The token is taken from the
// Authorization header (set by UserAuthMiddleware's parsing rules). Idempotent:
// logging out twice, or with an already-expired token, still returns 200.
func (h *userHandler) Logout(c *gin.Context) {
	token := bearerTokenFromHeader(c.GetHeader("Authorization"))
	if token != "" {
		_ = h.sessionService.Revoke(token)
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// bearerTokenFromHeader mirrors middleware.bearerToken without importing the
// middleware package (avoids a handler→middleware dependency cycle).
func bearerTokenFromHeader(authHeader string) string {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
}
