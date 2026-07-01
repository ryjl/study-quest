package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"studyquest/backend/internal/repository"

	"github.com/gin-gonic/gin"
)

// CORSMiddleware handles cross-origin resource sharing requests securely.
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		// Prevent using wildcards (*). Allow local development and local loopbacks.
		if origin != "" && (strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") || strings.HasPrefix(origin, "http://192.168.")) {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		}
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-User-ID, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// UserAuthMiddleware validates client sessions for Flutter PAD/TV.
func UserAuthMiddleware(userRepo repository.UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Read either X-User-ID header or standard Authorization header
		userIDStr := c.GetHeader("X-User-ID")
		if userIDStr == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				userIDStr = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if userIDStr == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization token or user ID header missing"})
			c.Abort()
			return
		}

		userID, err := strconv.ParseUint(userIDStr, 10, 32)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization token format"})
			c.Abort()
			return
		}

		user, err := userRepo.FindByID(uint(userID))
		if err != nil || user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User session invalid or unauthorized"})
			c.Abort()
			return
		}

		// Store user object and ID in context
		c.Set("userID", uint(userID))
		c.Set("userRole", user.Role)
		c.Set("userNickname", user.Nickname)
		c.Next()
	}
}

// AdminAuthMiddleware guards admin web endpoints.
func AdminAuthMiddleware(settingsRepo repository.SettingsRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie("admin_session")
		if err != nil || cookie == "" {
			// If request is API, return JSON. Otherwise redirect to login HTML.
			if strings.HasPrefix(c.Request.URL.Path, "/admin/api/") {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Admin session missing"})
			} else {
				c.Redirect(http.StatusFound, "/admin/login")
			}
			c.Abort()
			return
		}

		// Validate cookie against admin session key (in settings or simple validation)
		// For MVP, we retrieve the admin password hash from DB and use it as session token for simplicity
		savedHash, err := settingsRepo.Get("admin_password_hash")
		if err != nil || savedHash == "" || cookie != savedHash {
			if strings.HasPrefix(c.Request.URL.Path, "/admin/api/") {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Admin session expired or invalid"})
			} else {
				c.Redirect(http.StatusFound, "/admin/login")
			}
			c.Abort()
			return
		}

		c.Next()
	}
}
