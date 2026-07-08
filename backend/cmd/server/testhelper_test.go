package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"studyquest/backend/internal/handler"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/router"
	"studyquest/backend/internal/service"
)

// testEnv bundles a fully-wired gin engine + the underlying DB for direct
// fixture setup. It mirrors main.go's DI but with an in-memory SQLite DB, the
// probe worker constructed-but-not-Started (no ffprobe/netdisk side effects),
// and a pre-authenticated admin session cookie ready to use.
type testEnv struct {
	engine      *gin.Engine
	db          *gorm.DB
	adminCookie *http.Cookie
}

// newTestEnv builds a fresh server with seeded subjects/tags/badges + a logged-
// in admin session cookie ready to use. Each call yields an independent
// in-memory DB, so tests do not leak state into each other.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	// FKs are OFF by default in SQLite; turn them on so the RESTRICT/CASCADE
	// constraints exercised by the subject/tag tests actually fire.
	db.Exec("PRAGMA foreign_keys=ON")
	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}

	// repos (order matches main.go)
	settingsRepo := repository.NewSettingsRepository(db)
	userRepo := repository.NewUserRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	chapterRepo := repository.NewChapterRepository(db)
	badgeRepo := repository.NewBadgeRepository(db)
	subjectRepo := repository.NewSubjectRepository(db)
	tagRepo := repository.NewTagRepository(db)

	// services
	userService := service.NewUserService(userRepo)
	courseService := service.NewCourseService(courseRepo, userRepo)
	episodeService := service.NewEpisodeService(episodeRepo, settingsRepo)
	badgeService := service.NewBadgeService(badgeRepo, progressRepo)
	progressService := service.NewProgressService(progressRepo, episodeRepo, badgeService)
	subjectService := service.NewSubjectService(subjectRepo, badgeRepo)
	tagService := service.NewTagService(tagRepo)
	// Constructed but never Started: Enqueue is a pure in-memory op (pushes
	// onto a buffered channel + bumps stats), so it stays safe without a
	// consumer. Only Start() → probeOne → episodeService.Probe would spawn
	// ffprobe / hit the netdisk, which we don't want in tests.
	probeWorker := service.NewProbeWorker(episodeService, episodeRepo)
	importService := service.NewImportService(episodeRepo, courseRepo, settingsRepo, chapterRepo, subjectRepo, probeWorker.Enqueue)
	chapterService := service.NewChapterService(chapterRepo)

	// seed (subjects → tags → badges, same order as main.go; subjects must
	// come first so the subject_count badge rules resolve against a populated
	// table).
	if err := subjectService.SeedDefaultSubjects(); err != nil {
		t.Fatalf("seed subjects: %v", err)
	}
	if err := tagService.SeedDefaultTags(); err != nil {
		t.Fatalf("seed tags: %v", err)
	}
	if err := badgeService.SeedDefaultBadges(); err != nil {
		t.Fatalf("seed badges: %v", err)
	}

	// handlers
	healthH := handler.NewHealthHandler()
	userH := handler.NewUserHandler(userService)
	courseH := handler.NewCourseHandler(courseService, episodeService, chapterService, subjectRepo)
	episodeH := handler.NewEpisodeHandler(episodeService, progressService, settingsRepo)
	progressH := handler.NewProgressHandler(progressService)
	ingestH := handler.NewIngestHandler(episodeRepo, episodeService, probeWorker.Enqueue)
	adminH := handler.NewAdminHandler(
		settingsRepo, userRepo, courseRepo, episodeRepo, chapterRepo, progressRepo,
		subjectRepo, badgeRepo, userService, courseService, importService,
		episodeService, chapterService, badgeService, probeWorker,
	)
	badgeH := handler.NewBadgeHandler(badgeService)
	subjectH := handler.NewSubjectHandler(subjectService)
	tagH := handler.NewTagHandler(tagService)

	r := gin.New()
	router.RegisterRoutes(r, healthH, userH, courseH, episodeH, progressH, ingestH, adminH, badgeH, subjectH, tagH, userRepo, settingsRepo)

	// Pre-seed the admin password hash so login only pays for one bcrypt
	// compare instead of the lazy-init generate+compare (~120ms → ~60ms).
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	if err := settingsRepo.Set("admin_password_hash", string(hash), "Admin panel password hash"); err != nil {
		t.Fatalf("seed admin hash: %v", err)
	}

	env := &testEnv{engine: r, db: db}
	env.adminCookie = env.loginAdmin(t)
	return env
}

// loginAdmin posts the real /admin/api/login flow (so the auth middleware +
// cookie path get exercised) and returns the admin_session cookie.
func (e *testEnv) loginAdmin(t *testing.T) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": "admin"})
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin login: expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Name == "admin_session" {
			return c
		}
	}
	t.Fatalf("admin login: no admin_session cookie in response (cookies: %v)", cookies)
	return nil
}

// do fires an admin-authenticated request (carries the admin cookie) and
// returns the recorder. body may be nil; otherwise it's JSON-encoded.
func (e *testEnv) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if e.adminCookie != nil {
		req.AddCookie(e.adminCookie)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

// doAsUser fires a client-authenticated request via the X-User-ID header (the
// real UserAuthMiddleware path) — used for /api/v1/* client endpoints.
func (e *testEnv) doAsUser(t *testing.T, userID uint, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-User-ID", strconv.FormatUint(uint64(userID), 10))
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

// ---- business helpers (shared by the A–E tests) ----

// createSubject POSTs a custom (non-system) subject and returns its id. Used
// by tests that need a deletable subject — seeded subjects are IsSystem=true.
func (e *testEnv) createSubject(t *testing.T, key, label, emoji, color string) uint {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/admin/api/subjects", map[string]any{
		"key": key, "label": label, "emoji": emoji, "color": color,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create subject %q: expected 200, got %d (body: %s)", key, resp.Code, resp.Body.String())
	}
	var created struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created subject: %v (body: %s)", err, resp.Body.String())
	}
	return created.ID
}

// createTag POSTs a custom (non-system) tag and returns its id. Used by tests
// that need a deletable tag — seeded tags are IsSystem=true.
func (e *testEnv) createTag(t *testing.T, key, label, color string) uint {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/admin/api/tags", map[string]any{
		"key": key, "label": label, "color": color,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create tag %q: expected 200, got %d (body: %s)", key, resp.Code, resp.Body.String())
	}
	var created struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created tag: %v (body: %s)", err, resp.Body.String())
	}
	return created.ID
}

// createCourse POSTs to /admin/api/courses and returns the created course's ID
// by re-listing (the create endpoint returns the full DTO, but re-listing is
// the contract we actually want to lock in).
func (e *testEnv) createCourse(t *testing.T, title, subjectKey string, tagIDs []uint) uint {
	t.Helper()
	body := map[string]any{
		"title":   title,
		"grade":   "universal",
		"subject": subjectKey,
	}
	if tagIDs != nil {
		body["tag_ids"] = tagIDs
	}
	resp := e.do(t, http.MethodPost, "/admin/api/courses", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("create course %q: expected 200, got %d (body: %s)", title, resp.Code, resp.Body.String())
	}
	var created struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created course: %v (body: %s)", err, resp.Body.String())
	}
	return created.ID
}

// createUser POSTs to /admin/api/users and returns the new user's ID.
func (e *testEnv) createUser(t *testing.T, nickname, role string) uint {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/admin/api/users", map[string]any{
		"nickname": nickname,
		"pin":      "1234",
		"role":     role,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create user %q: expected 200, got %d (body: %s)", nickname, resp.Code, resp.Body.String())
	}
	var created struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created user: %v (body: %s)", err, resp.Body.String())
	}
	return created.ID
}

// grantAccess grants userID access to courseID via POST /admin/api/access.
func (e *testEnv) grantAccess(t *testing.T, userID, courseID uint) {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/admin/api/access", map[string]any{
		"user_id":   userID,
		"course_id": courseID,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("grant access user=%d course=%d: expected 200, got %d (body: %s)",
			userID, courseID, resp.Code, resp.Body.String())
	}
}

// createEpisode POSTs an episode under a course and returns its ID.
func (e *testEnv) createEpisode(t *testing.T, courseID uint, title string) uint {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/admin/api/courses/"+strconv.FormatUint(uint64(courseID), 10)+"/episodes", map[string]any{
		"title":                title,
		"video_relative_path":  "/fake/" + title + ".mp4",
		"sort_order":           1,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create episode %q: expected 200, got %d (body: %s)", title, resp.Code, resp.Body.String())
	}
	var created struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created episode: %v (body: %s)", err, resp.Body.String())
	}
	return created.ID
}

// setEpisodeDuration writes duration_seconds directly to the DB, bypassing the
// probe worker. Required because ReportProgress only marks an episode
// completed when position_seconds >= duration*0.9 AND duration > 0, and a
// freshly created episode has duration_seconds = nil (never completable).
func (e *testEnv) setEpisodeDuration(t *testing.T, episodeID uint, seconds int) {
	t.Helper()
	if err := e.db.Model(&model.Episode{}).Where("id = ?", episodeID).
		Update("duration_seconds", seconds).Error; err != nil {
		t.Fatalf("set episode %d duration: %v", episodeID, err)
	}
}

// setEpisodeCover writes cover_url directly to the DB, bypassing the probe
// worker. Used to test the course DTO's cover_fallback_url (which borrows the
// first episode's cover when the course itself has none).
func (e *testEnv) setEpisodeCover(t *testing.T, episodeID uint, coverURL string) {
	t.Helper()
	if err := e.db.Model(&model.Episode{}).Where("id = ?", episodeID).
		Update("cover_url", coverURL).Error; err != nil {
		t.Fatalf("set episode %d cover: %v", episodeID, err)
	}
}

// reportProgress POSTs a progress report as userID (X-User-ID auth path).
func (e *testEnv) reportProgress(t *testing.T, userID, episodeID uint, positionSec, deltaSec int) {
	t.Helper()
	resp := e.reportProgressRaw(t, userID, episodeID, positionSec, deltaSec)
	if resp.Code != http.StatusOK {
		t.Fatalf("report progress user=%d ep=%d: expected 200, got %d (body: %s)",
			userID, episodeID, resp.Code, resp.Body.String())
	}
}

// reportProgressRaw is the non-fatal variant of reportProgress; it returns the
// recorder so callers can inspect the response (used while debugging).
func (e *testEnv) reportProgressRaw(t *testing.T, userID, episodeID uint, positionSec, deltaSec int) *httptest.ResponseRecorder {
	t.Helper()
	return e.doAsUser(t, userID, http.MethodPost, "/api/v1/progress/report", map[string]any{
		"episode_id":          episodeID,
		"position_seconds":    positionSec,
		"delta_watch_seconds": deltaSec,
	})
}

// findSubjectID lists subjects and returns the id whose key matches.
func (e *testEnv) findSubjectID(t *testing.T, key string) uint {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/admin/api/subjects", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list subjects: expected 200, got %d", resp.Code)
	}
	var list []struct {
		ID  uint   `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode subjects: %v", err)
	}
	for _, s := range list {
		if s.Key == key {
			return s.ID
		}
	}
	t.Fatalf("subject key %q not found in %v", key, list)
	return 0
}

// findTagID lists tags and returns the id whose key matches.
func (e *testEnv) findTagID(t *testing.T, key string) uint {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/admin/api/tags", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list tags: expected 200, got %d", resp.Code)
	}
	var list []struct {
		ID  uint   `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode tags: %v", err)
	}
	for _, tg := range list {
		if tg.Key == key {
			return tg.ID
		}
	}
	t.Fatalf("tag key %q not found in %v", key, list)
	return 0
}
