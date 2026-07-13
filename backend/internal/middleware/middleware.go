package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"

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
		// NOTE: X-User-ID was intentionally removed when the app switched to
		// opaque session tokens. The legacy header must NOT be honored anywhere
		// in the auth path — see UserAuthMiddleware. X-Ingest-Key is added so
		// preflight passes for the Python toolchain's ingest requests.
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Ingest-Key")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// UserAuthMiddleware validates client sessions for Flutter PAD/TV.
//
// It only accepts an opaque session token in `Authorization: Bearer <token>`
// and validates it against the session table. The token is NOT the user ID
// (that was the old, insecure scheme); a bare numeric userID sent as Bearer
// is rejected explicitly. On success it sets the same context keys downstream
// handlers already read ("userID", "userRole", "userNickname"), so callers
// are unchanged.
func UserAuthMiddleware(sessionService service.SessionService, userRepo repository.UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization token missing"})
			c.Abort()
			return
		}

		// Defensive guard: reject the legacy "Bearer <userID>" form (bare
		// integer) explicitly. Session lookup would fail anyway, but a distinct
		// branch makes the regression boundary obvious in logs and in tests.
		if isAllDigits(token) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization scheme"})
			c.Abort()
			return
		}

		userID, err := sessionService.Validate(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "session invalid or expired"})
			c.Abort()
			return
		}

		user, err := userRepo.FindByID(userID)
		if err != nil || user == nil {
			// Token valid but user no longer exists (deleted). A dangling
			// session row is harmless and ages out by TTL.
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user account not found"})
			c.Abort()
			return
		}

		c.Set("userID", user.ID)
		c.Set("userRole", user.Role)
		c.Set("userNickname", user.Nickname)
		c.Next()
	}
}

// bearerToken extracts the token from `Authorization: Bearer <token>`.
// Returns "" if the header is absent or not in Bearer form. Deliberately does
// NOT consult X-User-ID — that header is rejected by design.
func bearerToken(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
}

// isAllDigits reports whether s is non-empty and all ASCII digits. Used to
// reject the legacy "token == userID" scheme at the boundary.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// loginRateLimiter is a per-IP sliding-window counter for login attempts.
// Intentionally in-memory: a server restart clears all counters, which is fine
// for a family deployment and avoids a DB round-trip on the hot login path.
//
// Concurrency: a single mutex guards the whole map. Login throughput is not a
// concern for this app, so the coarse lock is simpler than a sharded structure.
type loginRateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time // ip -> attempt timestamps within the window
	window time.Duration          // how long an attempt counts against the limit
	max    int                    // max attempts within the window before 429
	now    func() time.Time       // injectable clock for tests
}

func newLoginRateLimiter(window time.Duration, max int, now func() time.Time) *loginRateLimiter {
	return &loginRateLimiter{
		hits:   make(map[string][]time.Time),
		window: window,
		max:    max,
		now:    now,
	}
}

// allow checks whether ip is under the limit and, if so, records a hit.
// Each call represents one attempt; the caller doesn't yet know whether it
// will succeed. Rate-limiting *attempts* (not failures) is the safer default —
// a brute-forcer shouldn't get to "succeed on attempt 6" because the limit
// only counted failures. The window slides: old timestamps are pruned each call.
//
// Note: there is deliberately NO "reset on success" path. The limiter is a
// pre-handler gate, so it can't observe whether the login actually succeeded,
// and threading a post-handler hook back into the limiter isn't worth the
// complexity for this deployment. Net behavior: 6th attempt within 15 min is
// blocked regardless of prior successes. That's slightly stricter for
// fat-fingering users but strictly safer against brute force.
//
// Slice-alias invariant: `fresh := l.hits[ip][:0]` reuses the backing array.
// This is safe ONLY because we append each kept element before the range
// cursor could reach a position we'd overwrite — we never append more than we
// consume per iteration. Do NOT reorder the prune loop to also append new
// entries here without re-validating.
//
// Returns (true, currentCount) when the request is allowed, (false, count)
// when the ip has already reached max within the window.
func (l *loginRateLimiter) allow(ip string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)

	// Prune expired timestamps; reuse the same backing array to avoid alloc.
	fresh := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}

	if len(fresh) >= l.max {
		l.hits[ip] = fresh
		return false, len(fresh)
	}

	fresh = append(fresh, now)
	l.hits[ip] = fresh
	return true, len(fresh)
}

// LoginRateLimitMiddleware throttles the login endpoint per source IP. After
// `max` attempts within `window`, further attempts get HTTP 429 until the
// oldest attempt ages out. Counts *attempts*, not failures.
//
// Uses c.ClientIP() (not c.RemoteIP()) so behind a trusted reverse proxy the
// real client IP is used; without proxy trust, ClientIP == RemoteIP.
func LoginRateLimitMiddleware(window time.Duration, max int) gin.HandlerFunc {
	limiter := newLoginRateLimiter(window, max, time.Now)
	return func(c *gin.Context) {
		ip := c.ClientIP()
		ok, count := limiter.allow(ip)
		if !ok {
			c.Header("Retry-After", "900") // 15 min hint; matches default window
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":  "too many login attempts, please try again later",
				"count":  count,
				"window": int(window.Seconds()),
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// IngestKeyMiddleware gates the ingest endpoints with a pre-shared key. When
// key is empty the middleware is a no-op (backward compatible for LAN-only
// deployments); when set, requests must carry `X-Ingest-Key: <key>` or they
// are rejected with 401. The key is a plain string shared between the Go
// backend and the Python toolchain via environment variables — it is NOT a
// user credential and has nothing to do with login.
func IngestKeyMiddleware(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if key == "" {
			c.Next()
			return
		}
		if c.GetHeader("X-Ingest-Key") != key {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing ingest key"})
			c.Abort()
			return
		}
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
