package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"studyquest/backend/internal/model"
)

// setupLearningEpisode creates a course + episode + grants access + returns ids.
func setupLearningEpisode(t *testing.T, env *testEnv, userID uint) (courseID, episodeID uint) {
	t.Helper()
	courseID = env.createCourse(t, "学习课-"+itoa(userID), "math", nil)
	env.grantAccess(t, userID, courseID)
	episodeID = env.createEpisode(t, courseID, "第1集")
	return
}

// reportAsUser is a thin wrapper around doAsUser that posts a progress report.
func reportAsUser(t *testing.T, env *testEnv, userID, episodeID uint, pos, delta int) {
	t.Helper()
	resp := env.doAsUser(t, userID, http.MethodPost, "/api/v1/progress/report", map[string]any{
		"episode_id":          episodeID,
		"position_seconds":    pos,
		"delta_watch_seconds": delta,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("report progress user=%d ep=%d: 200 expected, got %d (body: %s)",
			userID, episodeID, resp.Code, resp.Body.String())
	}
}

// TestWatchEvent_RecordedOnReport verifies a progress report writes exactly one
// watch event with the reported delta.
func TestWatchEvent_RecordedOnReport(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "学习者", "student")
	_, epID := setupLearningEpisode(t, env, uid)

	reportAsUser(t, env, uid, epID, 30, 120)

	day := todayStr()
	events := fetchWatchEvents(t, env, uid, day)
	if len(events) != 1 {
		t.Fatalf("want 1 event after one report, got %d", len(events))
	}
	if events[0].DurationSeconds != 120 {
		t.Fatalf("event duration = %d, want 120", events[0].DurationSeconds)
	}
	if events[0].ContentType != "learning" {
		t.Fatalf("content_type = %q, want learning", events[0].ContentType)
	}
}

// TestWatchEvent_WindowMerge verifies two close-in-time reports merge into one
// row whose duration is the sum of both deltas.
func TestWatchEvent_WindowMerge(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "连续看", "student")
	_, epID := setupLearningEpisode(t, env, uid)

	reportAsUser(t, env, uid, epID, 5, 30)
	reportAsUser(t, env, uid, epID, 10, 25) // <60s apart → merge

	day := todayStr()
	events := fetchWatchEvents(t, env, uid, day)
	if len(events) != 1 {
		t.Fatalf("two close reports should merge to 1 row, got %d", len(events))
	}
	if events[0].DurationSeconds != 55 {
		t.Fatalf("merged duration = %d, want 55 (30+25)", events[0].DurationSeconds)
	}
}

// TestWatchEvent_DualWriteConsistency verifies the key invariant: the sum of
// event durations equals the aggregate watch_seconds (for learning).
func TestWatchEvent_DualWriteConsistency(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "双写", "student")
	_, epID := setupLearningEpisode(t, env, uid)

	// Several reports with varied deltas.
	reportAsUser(t, env, uid, epID, 5, 30)
	reportAsUser(t, env, uid, epID, 35, 20)
	reportAsUser(t, env, uid, epID, 55, 15)

	// Aggregate from user_progresses (direct DB read).
	var prog model.UserProgress
	if err := env.db.Where("user_id = ? AND episode_id = ?", uid, epID).First(&prog).Error; err != nil {
		t.Fatalf("read progress: %v", err)
	}
	// Sum from watch_events.
	day := todayStr()
	events := fetchWatchEvents(t, env, uid, day)
	var sumEvt int
	for _, e := range events {
		sumEvt += e.DurationSeconds
	}
	if prog.WatchSeconds != sumEvt {
		t.Fatalf("aggregate watch_seconds (%d) != sum of event durations (%d); dual-write drifted",
			prog.WatchSeconds, sumEvt)
	}
}

// TestWatchEvent_DualWriteConsistency_ClampedDelta is the regression guard for
// the clamp-centralization fix. A client may send a huge delta (e.g. a catch-up
// upload after reconnect: 1200s in one report). Both the aggregate and the
// event log must record the SAME clamped value (600s), or the dual-write
// invariant breaks. The original code clamped only inside the learning repo,
// so the event log recorded the raw 1200 while the aggregate recorded 600.
func TestWatchEvent_DualWriteConsistency_ClampedDelta(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "补传", "student")
	_, epID := setupLearningEpisode(t, env, uid)

	// One report with a delta far above the 600s clamp.
	reportAsUser(t, env, uid, epID, 1200, 1200)

	var prog model.UserProgress
	if err := env.db.Where("user_id = ? AND episode_id = ?", uid, epID).First(&prog).Error; err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if prog.WatchSeconds != 600 {
		t.Fatalf("aggregate should clamp to 600, got %d", prog.WatchSeconds)
	}
	events := fetchWatchEvents(t, env, uid, todayStr())
	if len(events) != 1 || events[0].DurationSeconds != 600 {
		var got int
		if len(events) == 1 {
			got = events[0].DurationSeconds
		}
		t.Fatalf("event must record the clamped 600 (not the raw delta) so SUM(events)==watch_seconds; got %d events, first=%d",
			len(events), got)
	}
}

// TestWatchEvent_EntertainmentRecorded verifies entertainment playback writes an
// event with content_type=entertainment.
func TestWatchEvent_EntertainmentRecorded(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "娱乐用户", "student")
	// Create an entertainment course. createCourse uses default content_type;
	// flip it via direct DB.
	cid := env.createCourse(t, "动画片", "math", nil)
	env.grantAccess(t, uid, cid)
	if err := env.db.Model(&model.Course{}).Where("id = ?", cid).
		Update("content_type", "entertainment").Error; err != nil {
		t.Fatalf("set entertainment: %v", err)
	}
	epID := env.createEpisode(t, cid, "第1集")

	reportAsUser(t, env, uid, epID, 10, 60)

	day := todayStr()
	events := fetchWatchEvents(t, env, uid, day)
	if len(events) != 1 {
		t.Fatalf("want 1 entertainment event, got %d", len(events))
	}
	if events[0].ContentType != "entertainment" {
		t.Fatalf("content_type = %q, want entertainment", events[0].ContentType)
	}
}

// TestWatchEvent_DifferentEpisodesNoMerge verifies reports for different
// episodes produce separate event rows even when back-to-back.
func TestWatchEvent_DifferentEpisodesNoMerge(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "切换", "student")
	cid := env.createCourse(t, "多集课", "math", nil)
	env.grantAccess(t, uid, cid)
	ep1 := env.createEpisode(t, cid, "第1集")
	ep2 := env.createEpisode(t, cid, "第2集")

	reportAsUser(t, env, uid, ep1, 5, 30)
	reportAsUser(t, env, uid, ep2, 5, 20) // different episode, same instant

	day := todayStr()
	events := fetchWatchEvents(t, env, uid, day)
	if len(events) != 2 {
		t.Fatalf("different episodes should produce 2 rows, got %d", len(events))
	}
}

// TestWatchEvent_DeleteUserClearsEvents verifies deleting a user removes their
// watch events (the user-delete transaction clears them).
func TestWatchEvent_DeleteUserClearsEvents(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "待删", "student")
	_, epID := setupLearningEpisode(t, env, uid)
	reportAsUser(t, env, uid, epID, 5, 30)

	// Confirm the event exists.
	day := todayStr()
	if events := fetchWatchEvents(t, env, uid, day); len(events) != 1 {
		t.Fatalf("precondition: want 1 event, got %d", len(events))
	}

	// Delete the user via admin.
	resp := env.do(t, http.MethodDelete, "/admin/api/users/"+itoa(uid), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete user: 200 expected, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	// Watch history endpoint should return empty.
	resp = env.do(t, http.MethodGet, "/admin/api/users/"+itoa(uid)+"/watch-events?day="+day, nil)
	// User no longer exists; the endpoint still returns its (now empty) events.
	if resp.Code != http.StatusOK {
		t.Fatalf("watch-events after delete: got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var events []watchEventJSON
	json.Unmarshal(resp.Body.Bytes(), &events)
	if len(events) != 0 {
		t.Fatalf("deleted user should have 0 events, got %d", len(events))
	}
}

// TestWatchEvent_WatchHistoryEndpoint verifies the heatmap endpoint returns
// per-day totals covering the whole requested range (zero-filled).
func TestWatchEvent_WatchHistoryEndpoint(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "热力", "student")
	_, epID := setupLearningEpisode(t, env, uid)
	reportAsUser(t, env, uid, epID, 5, 90)

	// Request this month (the default range).
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	// Use the admin endpoint.
	path := "/admin/api/users/" + itoa(uid) + "/watch-history?from=" + from.Format("2006-01-02")
	resp := env.do(t, http.MethodGet, path, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("watch-history: got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var days []watchHistoryDayJSON
	if err := json.Unmarshal(resp.Body.Bytes(), &days); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(days) == 0 {
		t.Fatal("watch-history returned no days")
	}
	// Today should have 90s.
	var todayTotal int64
	for _, d := range days {
		if d.Date == todayStr() {
			todayTotal = d.Seconds
		}
	}
	if todayTotal != 90 {
		t.Fatalf("today total = %d, want 90", todayTotal)
	}
}

// TestWatchEvent_AdminEndpointsRejectNonAdmin verifies the watch-history
// endpoints are gated by the admin cookie (not reachable with a user token).
func TestWatchEvent_AdminEndpointsRejectNonAdmin(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "普通", "student")
	day := todayStr()

	// Hit with the user's bearer token instead of the admin cookie.
	token, _ := env.sessionService.Issue(uid, "test", "test/ua")
	for _, p := range []string{
		"/admin/api/users/" + itoa(uid) + "/watch-history",
		"/admin/api/users/" + itoa(uid) + "/watch-events?day=" + day,
	} {
		req := newRequest(http.MethodGet, p, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := serve(env, req)
		// Admin endpoints are guarded by AdminAuthMiddleware which checks the
		// admin_session cookie, not the bearer. A request with no admin cookie
		// must be rejected (401).
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s without admin cookie: got %d, want 401", p, w.Code)
		}
	}
}

// ---- helpers ----

type watchEventJSON struct {
	ID              uint   `json:"id"`
	EpisodeTitle    string `json:"episode_title"`
	CourseTitle     string `json:"course_title"`
	ContentType     string `json:"content_type"`
	DurationSeconds int    `json:"duration_seconds"`
}

type watchHistoryDayJSON struct {
	Date    string `json:"date"`
	Seconds int64  `json:"seconds"`
}

func fetchWatchEvents(t *testing.T, env *testEnv, userID uint, day string) []watchEventJSON {
	t.Helper()
	resp := env.do(t, http.MethodGet, "/admin/api/users/"+itoa(userID)+"/watch-events?day="+day, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("fetch watch events user=%d day=%s: got %d (body: %s)",
			userID, day, resp.Code, resp.Body.String())
	}
	var events []watchEventJSON
	if err := json.Unmarshal(resp.Body.Bytes(), &events); err != nil {
		t.Fatalf("decode watch events: %v", err)
	}
	return events
}

// todayStr returns the current business-zone day as YYYY-MM-DD (matches how the
// backend's ListByUserAndDay buckets, so seeded events land in "today").
func todayStr() string {
	// Use the server's time package conventions: appclock.Now() in Asia/Shanghai.
	// We import it indirectly via model to avoid an extra import; simpler to use
	// time.Now in the +8 zone for the test's same-process notion of "today".
	t := time.Now()
	// Force CST so the day string matches the backend's bucketing regardless of
	// the test host's zone (CI may be UTC).
	cst := time.FixedZone("CST", 8*3600)
	return t.In(cst).Format("2006-01-02")
}
