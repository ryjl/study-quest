package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"
	"studyquest/backend/internal/testutil"
)

func init() { gin.SetMode(gin.TestMode) }

// stubUserRepo returns a fixed user for a single id, so UserAuthMiddleware
// tests don't need to plumb the full user-repo seeding.
type stubUserRepo struct {
	repository.UserRepository
	users map[uint]*model.User
}

func (s *stubUserRepo) FindByID(id uint) (*model.User, error) {
	if u, ok := s.users[id]; ok {
		return u, nil
	}
	return nil, nil
}

// buildAuthChain wires a tiny gin engine with UserAuthMiddleware guarding a
// single handler that echoes the resolved userID, mirroring how real handlers
// consume c.Get("userID"). Returns the engine and the session service so the
// test can issue/revoke tokens.
func buildAuthChain(t *testing.T) (*gin.Engine, service.SessionService) {
	t.Helper()
	db := testutil.NewDB(t)
	repo := repository.NewSessionRepository(db)
	svc := service.NewSessionService(repo, time.Hour)
	urepo := &stubUserRepo{users: map[uint]*model.User{
		1: {ID: 1, Nickname: "alice", Role: "student"},
	}}

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Next() }) // no logger noise
	authed := r.Group("/api/v1")
	authed.Use(UserAuthMiddleware(svc, urepo))
	authed.GET("/whoami", func(c *gin.Context) {
		uid, _ := c.Get("userID")
		c.JSON(http.StatusOK, gin.H{"user_id": uid})
	})
	return r, svc
}

func do(t *testing.T, r *gin.Engine, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestUserAuthMiddleware_ValidToken(t *testing.T) {
	r, svc := buildAuthChain(t)
	tok, _ := svc.Issue(1, "iPad", "ua")

	w := do(t, r, "Bearer "+tok)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func TestUserAuthMiddleware_MissingHeader(t *testing.T) {
	r, _ := buildAuthChain(t)
	w := do(t, r, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestUserAuthMiddleware_NonBearerHeader(t *testing.T) {
	r, _ := buildAuthChain(t)
	w := do(t, r, "Basic xyz")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestUserAuthMiddleware_UnknownToken(t *testing.T) {
	r, _ := buildAuthChain(t)
	w := do(t, r, "Bearer deadbeef")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestUserAuthMiddleware_RevokedToken(t *testing.T) {
	r, svc := buildAuthChain(t)
	tok, _ := svc.Issue(1, "iPad", "ua")
	_ = svc.Revoke(tok)

	w := do(t, r, "Bearer "+tok)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 after revoke", w.Code)
	}
}

func TestUserAuthMiddleware_UserDeleted(t *testing.T) {
	// Build a chain where the stub only knows user 1, but issue a token for
	// user 999 (which the stub doesn't know). The middleware must reject.
	db := testutil.NewDB(t)
	repo := repository.NewSessionRepository(db)
	svc := service.NewSessionService(repo, time.Hour)
	urepo := &stubUserRepo{users: map[uint]*model.User{1: {ID: 1, Nickname: "alice", Role: "student"}}}

	r := gin.New()
	g := r.Group("/api/v1")
	g.Use(UserAuthMiddleware(svc, urepo))
	g.GET("/whoami", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	// Issue a token for user 999 directly via the repo so we bypass the user
	// existence check in the stub; the token itself is valid but the user is
	// unknown to FindByID.
	tok, _ := svc.Issue(999, "ghost", "ua")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for deleted user", w.Code)
	}
}

// ---- LoginRateLimitMiddleware ----

func TestLoginRateLimitMiddleware_AllowsUpToMax(t *testing.T) {
	// max=3, window=1m. Three requests should pass.
	limiter := newLoginRateLimiter(time.Minute, 3, time.Now)
	for i := 0; i < 3; i++ {
		ok, _ := limiter.allow("1.2.3.4")
		if !ok {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
}

func TestLoginRateLimitMiddleware_BlocksAfterMax(t *testing.T) {
	limiter := newLoginRateLimiter(time.Minute, 3, time.Now)
	for i := 0; i < 3; i++ {
		limiter.allow("1.2.3.4")
	}
	ok, count := limiter.allow("1.2.3.4")
	if ok {
		t.Fatal("4th attempt should be blocked")
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

func TestLoginRateLimitMiddleware_WindowExpiryRestoresAccess(t *testing.T) {
	now := time.Now()
	limiter := newLoginRateLimiter(time.Minute, 2, func() time.Time { return now })

	limiter.allow("ip")
	limiter.allow("ip")
	if ok, _ := limiter.allow("ip"); ok {
		t.Fatal("3rd attempt within window should be blocked")
	}

	// Advance past the window; old hits should be pruned and access restored.
	now = now.Add(time.Minute + time.Second)
	ok, _ := limiter.allow("ip")
	if !ok {
		t.Fatal("after window expiry, access should be restored")
	}
}

func TestLoginRateLimitMiddleware_PerIPIsolation(t *testing.T) {
	limiter := newLoginRateLimiter(time.Minute, 2, time.Now)
	limiter.allow("A")
	limiter.allow("A")
	if ok, _ := limiter.allow("A"); ok {
		t.Fatal("A should be blocked")
	}
	// A different IP is independent.
	if ok, _ := limiter.allow("B"); !ok {
		t.Fatal("B should be allowed (independent of A)")
	}
}

func TestLoginRateLimitMiddleware_ConcurrentSafe(t *testing.T) {
	const max = 50
	limiter := newLoginRateLimiter(time.Minute, max, time.Now)
	const goroutines = 200
	var wg sync.WaitGroup
	allowed, blocked := 0, 0
	var mu sync.Mutex
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _ := limiter.allow("shared-ip")
			mu.Lock()
			if ok {
				allowed++
			} else {
				blocked++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if allowed != max {
		t.Fatalf("allowed = %d, want exactly %d", allowed, max)
	}
	if blocked != goroutines-max {
		t.Fatalf("blocked = %d, want %d", blocked, goroutines-max)
	}
	if allowed+blocked != goroutines {
		t.Fatalf("counting lost requests: allowed+blocked = %d, want %d", allowed+blocked, goroutines)
	}
}

// ---- IngestKeyMiddleware ----

func TestIngestKeyMiddleware_EmptyKeyAllowsAll(t *testing.T) {
	r := gin.New()
	r.Use(IngestKeyMiddleware("")) // no key configured
	r.POST("/ingest", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("empty key should allow; got %d", w.Code)
	}
}

func TestIngestKeyMiddleware_MatchingKeyAllows(t *testing.T) {
	r := gin.New()
	r.Use(IngestKeyMiddleware("secret"))
	r.POST("/ingest", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	req.Header.Set("X-Ingest-Key", "secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("matching key should allow; got %d", w.Code)
	}
}

func TestIngestKeyMiddleware_MissingKeyRejected(t *testing.T) {
	r := gin.New()
	r.Use(IngestKeyMiddleware("secret"))
	r.POST("/ingest", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing key should be rejected; got %d", w.Code)
	}
}

func TestIngestKeyMiddleware_WrongKeyRejected(t *testing.T) {
	r := gin.New()
	r.Use(IngestKeyMiddleware("secret"))
	r.POST("/ingest", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodPost, "/ingest", nil)
	req.Header.Set("X-Ingest-Key", "wrong")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key should be rejected; got %d", w.Code)
	}
}

// ---- CORS: allow-headers must include the tokens handlers rely on ----

func TestCORSMiddleware_AllowHeadersIncludesExpectedTokens(t *testing.T) {
	r := gin.New()
	r.Use(CORSMiddleware())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	allowed := w.Header().Get("Access-Control-Allow-Headers")
	if !containsToken(allowed, "Authorization") {
		t.Fatalf("CORS Allow-Headers must include Authorization; got %q", allowed)
	}
	if !containsToken(allowed, "X-Ingest-Key") {
		t.Fatalf("CORS Allow-Headers must include X-Ingest-Key; got %q", allowed)
	}
}

func containsToken(csv, token string) bool {
	for _, t := range strings.Split(csv, ",") {
		if strings.TrimSpace(t) == token {
			return true
		}
	}
	return false
}
