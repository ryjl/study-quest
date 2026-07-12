package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/model"
	"testing"
	"time"
)

// seedCourseEpisodes builds a course + n episodes (sort_order 1..n) + a
// student granted access 30 days ago. Returns ids for assertion.
func seedCourseEpisodes(t *testing.T, env *testEnv, n int) (courseID, userID uint, epIDs []uint) {
	t.Helper()
	courseID = env.createCourse(t, "解锁测试课", "math", nil)
	// Write episodes with explicit sort_order so the unlock rank is stable.
	for i := 1; i <= n; i++ {
		epID := env.createEpisode(t, courseID, "第"+strconv.Itoa(i)+"节")
		if err := env.db.Model(&model.Episode{}).Where("id = ?", epID).
			Update("sort_order", i).Error; err != nil {
			t.Fatalf("set episode sort_order: %v", err)
		}
		epIDs = append(epIDs, epID)
	}
	userID = env.createUser(t, "解锁生", "student")
	env.grantAccess(t, userID, courseID)
	// Pin the access row's granted_at to a deterministic instant for the
	// time-based strategy tests.
	setGrantedAt(t, env, userID, courseID, appclock.Now().AddDate(0, 0, -30))
	return courseID, userID, epIDs
}

func setGrantedAt(t *testing.T, env *testEnv, userID, courseID uint, ts time.Time) {
	t.Helper()
	if err := env.db.Model(&model.UserCourseAccess{}).
		Where("user_id = ? AND course_id = ?", userID, courseID).
		Update("granted_at", ts).Error; err != nil {
		t.Fatalf("set granted_at: %v", err)
	}
}

// studentEpisodes GETs /api/v1/courses/:id/episodes as the student and returns
// the id→locked map.
func studentEpisodes(t *testing.T, env *testEnv, userID, courseID uint) map[uint]bool {
	t.Helper()
	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/courses/"+itoa(courseID)+"/episodes", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("student episodes: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode episodes: %v", err)
	}
	out := map[uint]bool{}
	for _, e := range list {
		id := uint(e["ID"].(float64))
		locked, _ := e["locked"].(bool)
		out[id] = locked
	}
	return out
}

// TestUnlockBackwardCompat: a course with NO template yields all episodes
// visible (the backward-compat default). Guards against breaking existing data.
func TestUnlockBackwardCompat(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, _ := seedCourseEpisodes(t, env, 3)

	locks := studentEpisodes(t, env, userID, courseID)
	if len(locks) != 3 {
		t.Fatalf("expected 3 episodes, got %d", len(locks))
	}
	for _, locked := range locks {
		if locked {
			t.Errorf("backward-compat: episode unexpectedly locked")
		}
	}
}

// TestUnlockManualTemplateAndBump: manual template → only ep1 visible; a
// manual +1 exposes ep2 as well.
func TestUnlockManualTemplateAndBump(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, _ := seedCourseEpisodes(t, env, 4)

	// Set manual template.
	resp := env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{
		"strategy": "manual",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("save template: %d %s", resp.Code, resp.Body.String())
	}

	locks := studentEpisodes(t, env, userID, courseID)
	visibleCount := 0
	for _, locked := range locks {
		if !locked {
			visibleCount++
		}
	}
	if visibleCount != 1 {
		t.Errorf("manual initial: visible=%d want 1", visibleCount)
	}

	// Manual +1.
	resp = env.do(t, http.MethodPost, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/manual-unlock", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("manual unlock: %d %s", resp.Code, resp.Body.String())
	}
	locks = studentEpisodes(t, env, userID, courseID)
	visibleCount = 0
	for _, locked := range locks {
		if !locked {
			visibleCount++
		}
	}
	if visibleCount != 2 {
		t.Errorf("after manual +1: visible=%d want 2", visibleCount)
	}
}

// TestUnlockSelectedCherryPick: selected strategy + allowlist [ep3] → only
// episode 3 visible, everything else locked (jump-pick verified).
func TestUnlockSelectedCherryPick(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, epIDs := seedCourseEpisodes(t, env, 5)

	// Override this user to selected + allowlist [ep3].
	resp := env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override", map[string]any{
		"strategy":             "selected",
		"allowed_episode_ids":  []uint{epIDs[2]},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("save override: %d %s", resp.Code, resp.Body.String())
	}

	locks := studentEpisodes(t, env, userID, courseID)
	for id, locked := range locks {
		if id == epIDs[2] && locked {
			t.Errorf("ep3 should be visible, got locked")
		}
		if id != epIDs[2] && !locked {
			t.Errorf("ep %d should be locked, got visible", id)
		}
	}
}

// TestUnlockPlayInfoGate: a locked episode's play-info must 403; a visible
// one returns 200 (best-effort — it may 500 if storage isn't configured, but
// NOT 403). This is the anti-URL-guessing defense.
func TestUnlockPlayInfoGate(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, epIDs := seedCourseEpisodes(t, env, 3)

	// manual template → ep2/ep3 locked.
	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{"strategy": "manual"})

	// ep1 visible → play-info must NOT be 403.
	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/episodes/"+itoa(epIDs[0])+"/play-info", nil)
	if resp.Code == http.StatusForbidden {
		t.Errorf("ep1 play-info gated (403) but should be visible")
	}

	// ep2 locked → play-info MUST be 403.
	resp = env.doAsUser(t, userID, http.MethodGet, "/api/v1/episodes/"+itoa(epIDs[1])+"/play-info", nil)
	if resp.Code != http.StatusForbidden {
		t.Errorf("ep2 play-info: expected 403, got %d", resp.Code)
	}
}

// TestUnlockAdminBypassesGate: admin role sees all episodes regardless of
// template (they manage content, not consume under drip).
func TestUnlockAdminBypassesGate(t *testing.T) {
	env := newTestEnv(t)
	courseID, _, epIDs := seedCourseEpisodes(t, env, 3)
	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{"strategy": "manual"})

	// Admin reads episodes via the admin endpoint (not the student one).
	resp := env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(courseID)+"/episodes", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin episodes: %d", resp.Code)
	}
	var list []map[string]any
	json.Unmarshal(resp.Body.Bytes(), &list)
	if len(list) != 3 {
		t.Errorf("admin should see all 3 episodes, got %d", len(list))
	}
	_ = epIDs
}

// TestUnlockOverrideWinsOverTemplate: course template = manual, but one user
// overrides to all_open → that user sees everything, others stay gated.
func TestUnlockOverrideWinsOverTemplate(t *testing.T) {
	env := newTestEnv(t)
	courseID, userA, epIDs := seedCourseEpisodes(t, env, 4)
	userB := env.createUser(t, "生B", "student")
	env.grantAccess(t, userB, courseID)

	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{"strategy": "manual"})
	// Override B → all_open.
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userB)+"/courses/"+itoa(courseID)+"/unlock-override", map[string]any{
		"strategy":            "all_open",
		"allowed_episode_ids": []uint{},
	})

	locksA := studentEpisodes(t, env, userA, courseID)
	locksB := studentEpisodes(t, env, userB, courseID)
	visA, visB := 0, 0
	for _, l := range locksA {
		if !l {
			visA++
		}
	}
	for _, l := range locksB {
		if !l {
			visB++
		}
	}
	if visA != 1 {
		t.Errorf("userA (template) visible=%d want 1", visA)
	}
	if visB != 4 {
		t.Errorf("userB (override all_open) visible=%d want 4", visB)
	}
	_ = epIDs
}

// TestUnlockAutoPruneDeletedAllowlist: allowlist referencing a deleted episode
// must silently drop it (no 500, no dangling visible id).
func TestUnlockAutoPruneDeletedAllowlist(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, epIDs := seedCourseEpisodes(t, env, 4)
	// selected + [ep1, ep3].
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override", map[string]any{
		"strategy":            "selected",
		"allowed_episode_ids": []uint{epIDs[0], epIDs[2]},
	})
	// Delete ep1 directly.
	if err := env.db.Delete(&model.Episode{}, epIDs[0]).Error; err != nil {
		t.Fatalf("delete ep1: %v", err)
	}
	locks := studentEpisodes(t, env, userID, courseID)
	// ep3 still visible, ep1 gone from the map entirely.
	if len(locks) != 3 {
		t.Fatalf("after delete expected 3 episodes, got %d", len(locks))
	}
	if locks[epIDs[2]] {
		t.Errorf("ep3 should be visible (locked=false), got locked")
	}
	if !locks[epIDs[1]] {
		t.Errorf("ep2 should be locked, got visible")
	}
}

// TestUnlockStatusEndpoint: the unlock-status endpoint reports the right
// strategy + a non-empty next_unlock_at for a weekly strategy whose water
// level hasn't saturated yet. grantedAt is set to "yesterday" so only the
// initial 1 episode is unlocked and a future Sunday 19:00 boundary exists.
func TestUnlockStatusEndpoint(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, _ := seedCourseEpisodes(t, env, 5)

	// Pin the clock to a Saturday so the Sunday 19:00 weekly boundary is in
	// the future. grantedAt is set to yesterday (Friday), so water level = 1
	// (only the initial episode is unlocked, the Sunday boundary hasn't elapsed).
	pinClock(t, time.Date(2026, 7, 11, 10, 0, 0, 0, time.FixedZone("CST", 8*3600)))

	// weekly Sun 19:00; granted yesterday (water level still 1, not saturated).
	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{
		"strategy":     "weekly",
		"weekly_times": []map[string]any{{"weekday": 0, "hour": 19, "minute": 0}},
	})
	setGrantedAt(t, env, userID, courseID, appclock.Now().AddDate(0, 0, -1))

	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/courses/"+itoa(courseID)+"/unlock-status", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("unlock-status: %d %s", resp.Code, resp.Body.String())
	}
	var st map[string]any
	json.Unmarshal(resp.Body.Bytes(), &st)
	if st["strategy"] != "weekly" {
		t.Errorf("strategy=%v want weekly", st["strategy"])
	}
	if st["next_unlock_at"] == "" || st["next_unlock_at"] == nil {
		t.Errorf("expected a next_unlock_at, got %v", st["next_unlock_at"])
	}
	// Water level = 1 (granted yesterday, no weekly point elapsed yet).
	if n, _ := st["unlocked_n"].(float64); int(n) != 1 {
		t.Errorf("unlocked_n=%v want 1", st["unlocked_n"])
	}
}

// pinClock fixes appclock to a frozen instant (CST) for the test's duration,
// restoring it on return. The resolver reads appclock.Now() internally, so
// pinning it makes the water level fully deterministic in HTTP tests.
func pinClock(t *testing.T, now time.Time) {
	t.Helper()
	loc := time.FixedZone("CST", 8*3600)
	prevZone := appclock.Zone()
	appclock.SetZone(loc)
	appclock.SetNow(func() time.Time { return now.In(loc) })
	// Restore with time.Now directly — capturing appclock.Now as the "previous"
	// value would store the wrapper itself, turning nowFunc into appclock.Now,
	// which calls nowFunc() → infinite recursion. (Mirrors badge_time_test.)
	t.Cleanup(func() {
		appclock.SetZone(prevZone)
		appclock.SetNow(time.Now)
	})
}

// countVisible reads the student episodes endpoint and returns how many are
// unlocked (locked=false).
func countVisible(t *testing.T, env *testEnv, userID, courseID uint) int {
	t.Helper()
	locks := studentEpisodes(t, env, userID, courseID)
	vis := 0
	for _, locked := range locks {
		if !locked {
			vis++
		}
	}
	return vis
}

// TestUnlockIntervalAutoAdvance: interval=7days strategy. Granted at T0; at
// T0 the student sees 1 episode; after advancing the clock 8 days, they see 2.
func TestUnlockIntervalAutoAdvance(t *testing.T) {
	env := newTestEnv(t)
	// Pin "now" to a fixed Sunday; seedCourseEpisodes uses appclock.Now()-30d
	// for granted_at, which we then override explicitly below.
	t0 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC) // arbitrary fixed now
	pinClock(t, t0)

	courseID, userID, _ := seedCourseEpisodes(t, env, 5)
	// Grant exactly at t0 so the interval math is clean.
	setGrantedAt(t, env, userID, courseID, appclock.Now())

	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{
		"strategy":         "interval",
		"interval_seconds": 7 * 86400, // every 7 days
	})

	// At t0: only 1 episode (the base level).
	if vis := countVisible(t, env, userID, courseID); vis != 1 {
		t.Fatalf("at grant: visible=%d want 1", vis)
	}

	// Advance clock 8 days → past one 7-day boundary → 2 visible.
	pinClock(t, t0.AddDate(0, 0, 8))
	if vis := countVisible(t, env, userID, courseID); vis != 2 {
		t.Fatalf("after +8d: visible=%d want 2", vis)
	}

	// Advance another 7 days → 3 visible.
	pinClock(t, t0.AddDate(0, 0, 15))
	if vis := countVisible(t, env, userID, courseID); vis != 3 {
		t.Fatalf("after +15d: visible=%d want 3", vis)
	}
}

// TestUnlockWeeklyMultiPointAutoAdvance: weekly with Mon 19:00 + Thu 19:00.
// Granted Sunday; Monday-19 hasn't happened yet → 1; past Monday-19 → 2;
// past Thursday-19 → 3.
func TestUnlockWeeklyMultiPointAutoAdvance(t *testing.T) {
	env := newTestEnv(t)
	// 2026-03-01 is a Sunday. Pin now to Sunday 08:00 CST.
	sunday := time.Date(2026, 3, 1, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	pinClock(t, sunday)

	courseID, userID, _ := seedCourseEpisodes(t, env, 6)
	setGrantedAt(t, env, userID, courseID, appclock.Now())

	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{
		"strategy": "weekly",
		"weekly_times": []map[string]any{
			{"weekday": 1, "hour": 19, "minute": 0}, // Monday 19:00
			{"weekday": 4, "hour": 19, "minute": 0}, // Thursday 19:00
		},
	})

	// Sunday 08:00: no point elapsed → 1 visible.
	if vis := countVisible(t, env, userID, courseID); vis != 1 {
		t.Fatalf("Sun 08:00: visible=%d want 1", vis)
	}

	// Monday 18:00 (before the 19:00 point): still 1.
	pinClock(t, time.Date(2026, 3, 2, 18, 0, 0, 0, time.FixedZone("CST", 8*3600)))
	if vis := countVisible(t, env, userID, courseID); vis != 1 {
		t.Fatalf("Mon 18:00 (before point): visible=%d want 1", vis)
	}

	// Monday 19:30 (past the point): 2.
	pinClock(t, time.Date(2026, 3, 2, 19, 30, 0, 0, time.FixedZone("CST", 8*3600)))
	if vis := countVisible(t, env, userID, courseID); vis != 2 {
		t.Fatalf("Mon 19:30 (after point): visible=%d want 2", vis)
	}

	// Thursday 19:30 (past second point): 3.
	pinClock(t, time.Date(2026, 3, 5, 19, 30, 0, 0, time.FixedZone("CST", 8*3600)))
	if vis := countVisible(t, env, userID, courseID); vis != 3 {
		t.Fatalf("Thu 19:30 (after 2nd point): visible=%d want 3", vis)
	}
}

// TestUnlockSummaryInCourseList: the course-list DTO carries the drip-unlock
// summary so cards can show cadence without a per-course fetch. Verifies:
//   - student sees UnlockStrategy/UnlockedCount on a manual-template course
//   - student sees NO summary (empty) on an all-open course (badge hidden)
//   - the resolved water level matches the manual bumps
func TestUnlockSummaryInCourseList(t *testing.T) {
	env := newTestEnv(t)
	// Course A: manual template (drip). Course B: no template (all-open).
	courseA, userA, _ := seedCourseEpisodes(t, env, 4)
	courseB := env.createCourse(t, "开放课", "math", nil)
	// Re-grant B to userA (createCourse doesn't auto-grant).
	env.grantAccess(t, userA, courseB)
	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseA)+"/unlock-template", map[string]any{"strategy": "manual"})

	list := env.doAsUser(t, userA, http.MethodGet, "/api/v1/courses", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("course list: %d %s", list.Code, list.Body.String())
	}
	var dtos []map[string]any
	json.Unmarshal(list.Body.Bytes(), &dtos)
	byID := map[uint]map[string]any{}
	for _, d := range dtos {
		byID[uint(d["ID"].(float64))] = d
	}

	a := byID[courseA]
	if a["UnlockStrategy"] != "manual" {
		t.Errorf("courseA UnlockStrategy=%v want manual", a["UnlockStrategy"])
	}
	if n, _ := a["UnlockedCount"].(float64); int(n) != 1 {
		t.Errorf("courseA UnlockedCount=%v want 1", a["UnlockedCount"])
	}
	if lab, _ := a["UnlockStrategyLabel"].(string); lab == "" {
		t.Errorf("courseA UnlockStrategyLabel empty, want a label")
	}

	b := byID[courseB]
	// all_open must NOT carry a strategy (so the card hides the badge).
	if b["UnlockStrategy"] != "" && b["UnlockStrategy"] != "all_open" {
		t.Errorf("courseB UnlockStrategy=%v want empty/all_open (no badge)", b["UnlockStrategy"])
	}
}

// TestUnlockSummaryHiddenForAdmin: admin/parent roles get the course list
// WITHOUT the unlock summary — the client DTO projection (with Unlock* fields)
// is only applied on /api/v1/courses for student roles. The admin
// /admin/api/courses endpoint returns its own snake_case DTO and never carries
// unlock fields. This guards against accidentally leaking the per-student
// resolution onto the admin view.
func TestUnlockSummaryHiddenForAdmin(t *testing.T) {
	env := newTestEnv(t)
	courseID, _, _ := seedCourseEpisodes(t, env, 3)
	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{"strategy": "manual"})

	list := env.do(t, http.MethodGet, "/admin/api/courses", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("admin course list: %d", list.Code)
	}
	var dtos []map[string]any
	json.Unmarshal(list.Body.Bytes(), &dtos)
	for _, d := range dtos {
		// admin DTO uses lowercase "id".
		rawID, ok := d["id"]
		if !ok {
			continue
		}
		if uint(rawID.(float64)) != courseID {
			continue
		}
		// The unlock projection keys must be ABSENT from the admin DTO entirely.
		for _, key := range []string{"UnlockStrategy", "UnlockStrategyLabel", "UnlockedCount", "EpisodeTotal", "NextUnlockAt", "unlock_strategy"} {
			if _, present := d[key]; present {
				t.Errorf("admin DTO leaked client-only key %q", key)
			}
		}
		return
	}
	t.Errorf("course %d not found in admin course list (ids: %v)", courseID, idsOf(dtos))
}

func idsOf(dtos []map[string]any) []uint {
	out := []uint{}
	for _, d := range dtos {
		if raw, ok := d["id"].(float64); ok {
			out = append(out, uint(raw))
		}
	}
	return out
}

// TestUnlockManualUndoEndToEnd: +1 exposes ep2, −1 re-hides it. Verifies the
// undo endpoint actually changes what the student sees, not just the counter.
func TestUnlockManualUndoEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, epIDs := seedCourseEpisodes(t, env, 4)

	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{"strategy": "manual"})
	// initial: ep1 visible
	if vis := countVisible(t, env, userID, courseID); vis != 1 {
		t.Fatalf("initial visible=%d want 1", vis)
	}

	// +1 → ep1, ep2 visible
	env.do(t, http.MethodPost, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/manual-unlock", nil)
	if vis := countVisible(t, env, userID, courseID); vis != 2 {
		t.Fatalf("after +1 visible=%d want 2", vis)
	}

	// −1 → back to ep1 only
	resp := env.do(t, http.MethodPost, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/manual-unlock-undo", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("manual-unlock-undo: %d %s", resp.Code, resp.Body.String())
	}
	if vis := countVisible(t, env, userID, courseID); vis != 1 {
		t.Fatalf("after -1 visible=%d want 1", vis)
	}
	_ = epIDs
}

// TestUnlockManualUndoIdempotentAtFloor: undoing below zero is a no-op that
// still returns 200 (the UI −1 button can't 404).
func TestUnlockManualUndoIdempotentAtFloor(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, _ := seedCourseEpisodes(t, env, 3)
	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{"strategy": "manual"})

	// Undo three times at count=0 → all 200, visible stays 1 (base level only).
	for i := 0; i < 3; i++ {
		resp := env.do(t, http.MethodPost, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/manual-unlock-undo", nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("undo #%d: expected 200, got %d", i, resp.Code)
		}
	}
	if vis := countVisible(t, env, userID, courseID); vis != 1 {
		t.Errorf("after over-undo visible=%d want 1 (base level, not 0)", vis)
	}
}

// TestUnlockPlayInfoGateFailClosed: the gate must reject a non-admin request
// that lacks a userID (malformed/missing middleware context) with 401, NOT fall
// through to handing out a stream URL. Guards the fail-open regression.
func TestUnlockPlayInfoGateFailClosed(t *testing.T) {
	env := newTestEnv(t)
	courseID, _, epIDs := seedCourseEpisodes(t, env, 2)
	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{"strategy": "manual"})

	// A request with NO X-User-ID header at all (so middleware won't inject one).
	// It's not admin either. The gate must deny, not hand out the stream URL.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/episodes/"+itoa(epIDs[1])+"/play-info", nil)
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)
	// Either 401 (gate fail-closed) or the middleware's own 401 — but NOT 200/302
	// (which would mean a stream URL leaked). Accept anything that isn't a success.
	if w.Code < 400 {
		t.Errorf("unauthenticated non-admin play-info: code=%d, expected >=400 (fail-closed), body=%s", w.Code, w.Body.String())
	}
}

// TestUnlockOverrideWeeklyFallsBackToTemplate: an override that sets
// strategy=weekly but leaves weekly_times empty must inherit the template's
// weekly points (documented fallback), so an admin can change the global
// cadence in one place. Guards the SaveOverride → ResolveEffective handoff.
func TestUnlockOverrideWeeklyFallsBackToTemplate(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, _ := seedCourseEpisodes(t, env, 5)

	// Template: weekly Sun 19:00.
	env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{
		"strategy":     "weekly",
		"weekly_times": []map[string]any{{"weekday": 0, "hour": 19, "minute": 0}},
	})
	// Override: weekly but NO weekly_times of its own.
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override", map[string]any{
		"strategy":            "weekly",
		"weekly_times":        []map[string]any{},
		"allowed_episode_ids": []uint{},
	})

	// The override's weekly points should have fallen back to the template's.
	// granted yesterday → 1 visible (no weekly point elapsed); the fallback means
	// the cadence is real, so next_unlock_at is non-empty.
	setGrantedAt(t, env, userID, courseID, appclock.Now().AddDate(0, 0, -1))
	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/courses/"+itoa(courseID)+"/unlock-status", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("unlock-status: %d %s", resp.Code, resp.Body.String())
	}
	var st map[string]any
	json.Unmarshal(resp.Body.Bytes(), &st)
	if st["strategy"] != "weekly" {
		t.Errorf("strategy=%v want weekly", st["strategy"])
	}
	if st["next_unlock_at"] == "" || st["next_unlock_at"] == nil {
		t.Errorf("weekly fallback broken: next_unlock_at empty, expected template's Sun-19:00 point. full=%v", st)
	}
}
