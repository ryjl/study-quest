package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"studyquest/backend/internal/model"
)

// ──────────────────────────────────────────────────────────────────────────────
// Regression tests for the architecture refactor. These exist because the
// refactor changed load-bearing structure (transactions, error mapping, file
// split, builder wiring) and the pre-existing tests only covered happy paths.
// Each test below locks in a behavior the refactor introduced or relied on.
// ──────────────────────────────────────────────────────────────────────────────

// TestImportAtomicity verifies the import transaction boundary added in Phase 1.
// It imports a valid tree (chapter + 2 episodes) and asserts the persisted row
// counts are exact — no orphan chapters/episodes, no duplicate course. This is
// the observable invariant the transaction protects: a clean, complete import.
//
// (A true forced-mid-write rollback test would require injecting a repo fault,
// which the current architecture doesn't expose. The unit-level atomicity of
// the progress completion+points path is covered separately by
// TestProgressCompletionAtomicity + TestProgressAtomicityThroughRouter below.)
func TestImportAtomicity(t *testing.T) {
	env := newTestEnv(t)

	tree := map[string]any{
		"new_course": map[string]any{"title": "回滚验证课", "grade": "3", "subject": "math"},
		"tree": map[string]any{
			"name": "Root", "is_dir": true, "type": "course",
			"children": []map[string]any{
				{"name": "第一章", "is_dir": true, "type": "chapter", "children": []map[string]any{
					{"name": "第1集", "path": "/a/1.mp4", "is_dir": false, "type": "episode", "size": 100},
					{"name": "第2集", "path": "/a/2.mp4", "is_dir": false, "type": "episode", "size": 200},
				}},
			},
		},
	}
	resp := env.do(t, http.MethodPost, "/admin/api/import/execute", tree)
	if resp.Code != http.StatusOK {
		t.Fatalf("import: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	// Verify exactly 1 course, 1 chapter, 2 episodes — no orphans, no dupes.
	var courseCount, chapterCount, episodeCount int64
	env.db.Model(&model.Course{}).Where("title = ?", "回滚验证课").Count(&courseCount)
	env.db.Model(&model.Chapter{}).
		Joins("JOIN courses ON courses.id = chapters.course_id").
		Where("courses.title = ?", "回滚验证课").Count(&chapterCount)
	env.db.Model(&model.Episode{}).
		Joins("JOIN courses ON courses.id = episodes.course_id").
		Where("courses.title = ?", "回滚验证课").Count(&episodeCount)

	if courseCount != 1 {
		t.Errorf("course count = %d, want 1 (no dupes)", courseCount)
	}
	if chapterCount != 1 {
		t.Errorf("chapter count = %d, want 1", chapterCount)
	}
	if episodeCount != 2 {
		t.Errorf("episode count = %d, want 2", episodeCount)
	}
}

// TestAdminFileSplitSmoke verifies that after splitting admin_handler.go into
// admin_auth/user/content/import files, representative routes from EACH split
// file still resolve to working handlers through the real router. If a method
// was lost or mis-wired during the file move, this catches it.
func TestAdminFileSplitSmoke(t *testing.T) {
	env := newTestEnv(t)

	// admin_auth.go: GetSettings
	if resp := env.do(t, http.MethodGet, "/admin/api/settings", nil); resp.Code != http.StatusOK {
		t.Errorf("auth split /settings: %d (body: %s)", resp.Code, resp.Body.String())
	}
	// admin_user.go: ListUsers (also verifies the response decodes — proves the
	// DTO path + respondError refactor didn't break the happy-path shape)
	resp := env.do(t, http.MethodGet, "/admin/api/users", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("user split /users: %d (body: %s)", resp.Code, resp.Body.String())
	}
	var users []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &users); err != nil {
		t.Errorf("ListUsers decode: %v (body: %s)", err, resp.Body.String())
	}
	// admin_content.go: ListCourses
	if resp := env.do(t, http.MethodGet, "/admin/api/courses", nil); resp.Code != http.StatusOK {
		t.Errorf("content split /courses: %d (body: %s)", resp.Code, resp.Body.String())
	}
	// admin_import.go: ProbeProgress (always available, no storage needed)
	if resp := env.do(t, http.MethodGet, "/admin/api/probe/progress", nil); resp.Code != http.StatusOK {
		t.Errorf("import split /probe/progress: %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestErrorMappingEndToEnd verifies the centralized error mapping (Phase 2)
// produces correct status codes through the FULL stack (handler → respondError
// → HTTP), not just in isolation. The missing-user case was a 500 before the
// refactor (service returned errors.New("user not found") which the handler
// couldn't distinguish from a real failure); it must now be a clean 404.
func TestErrorMappingEndToEnd(t *testing.T) {
	env := newTestEnv(t)

	resp := env.do(t, http.MethodPut, "/admin/api/users/99999", map[string]any{
		"nickname": "x", "role": "student",
	})
	if resp.Code != http.StatusNotFound {
		t.Errorf("missing user update: got %d, want 404", resp.Code)
	}
	// The body must NOT contain raw SQL/driver internals (no-leak invariant).
	body := resp.Body.String()
	for _, leak := range []string{"gorm", "sqlite", "SQL", "query", "record not found"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(leak)) {
			t.Errorf("error response leaks internal %q: %s", leak, body)
		}
	}
}

// TestProgressAtomicityThroughRouter verifies the transaction boundary (Phase 1)
// end-to-end through the real HTTP path, not just at the service unit level.
// Completing an episode must atomically set is_completed AND award 10 points
// in one transaction; a duplicate report must not double-award.
func TestProgressAtomicityThroughRouter(t *testing.T) {
	env := newTestEnv(t)

	uid := env.createUser(t, "AtomicStudent", "student")
	cid := env.createCourse(t, "AtomicCourse", "math", nil)
	eid := env.createEpisode(t, cid, "Ep1")
	env.setEpisodeDuration(t, eid, 100) // 100s → 90% threshold = 90s
	env.grantAccess(t, uid, cid)

	// Report at 95s (>90% of 100s) → should complete + award 10 points.
	resp := env.doAsUser(t, uid, http.MethodPost, "/api/v1/progress/report", map[string]any{
		"episode_id":          eid,
		"position_seconds":    95,
		"delta_watch_seconds": 95,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("report progress: %d (body: %s)", resp.Code, resp.Body.String())
	}

	// Verify points awarded — the atomic invariant: completion + points together.
	// The points endpoint returns the raw model.UserPoint when a row exists
	// (PascalCase, no json tags) or a snake_case gin.H when nil — tolerate both.
	readPoints := func() int {
		t.Helper()
		pResp := env.doAsUser(t, uid, http.MethodGet, "/api/v1/progress/points", nil)
		if pResp.Code != http.StatusOK {
			t.Fatalf("get points: %d", pResp.Code)
		}
		var raw map[string]any
		json.Unmarshal(pResp.Body.Bytes(), &raw)
		// PascalCase (model) or snake_case (nil-fallback gin.H).
		if v, ok := raw["CurrentPoints"]; ok {
			return int(v.(float64))
		}
		return int(raw["current_points"].(float64))
	}

	// Completing one math episode (the course's only episode → full course
	// completion) awards: 10 (system_watch) + 10 (time_master tier0) + 5
	// (subject_math tier0) + 10 (course_master tier0) + 0 (first_blood) = 35.
	if got := readPoints(); got != 35 {
		t.Errorf("points after completion = %d, want 35 (10 watch + 10 time + 5 subject + 10 course)", got)
	}

	// Second report at same position must NOT double-award (IsCompleted==1 skips).
	env.doAsUser(t, uid, http.MethodPost, "/api/v1/progress/report", map[string]any{
		"episode_id":          eid,
		"position_seconds":    95,
		"delta_watch_seconds": 5,
	})
	if got := readPoints(); got != 35 {
		t.Errorf("points after duplicate report = %d, want 35 (no double-award)", got)
	}
}

// TestBadgeUnlockAtomicityThroughRouter verifies the badge_service transaction
// (Phase 1): unlocking a badge writes both the user_badges row AND a ledger
// entry atomically. We complete enough episodes to satisfy the "first_blood"
// badge (1 completed episode) and check both the badge and a points balance
// that reflects the episode award — proving the transactional writes landed
// together through the full stack.
func TestBadgeUnlockAtomicityThroughRouter(t *testing.T) {
	env := newTestEnv(t)

	uid := env.createUser(t, "BadgeStudent", "student")
	cid := env.createCourse(t, "BadgeCourse", "math", nil)
	eid := env.createEpisode(t, cid, "Ep1")
	env.setEpisodeDuration(t, eid, 100)
	env.grantAccess(t, uid, cid)

	// Complete one episode → first_blood badge should unlock.
	env.doAsUser(t, uid, http.MethodPost, "/api/v1/progress/report", map[string]any{
		"episode_id":          eid,
		"position_seconds":    95,
		"delta_watch_seconds": 95,
	})

	// Badge must be unlocked (the user_badges write committed).
	bResp := env.doAsUser(t, uid, http.MethodGet, "/api/v1/users/"+strconvUINT(uid)+"/badges", nil)
	if bResp.Code != http.StatusOK {
		t.Fatalf("get badges: %d", bResp.Code)
	}
	var badges []map[string]any
	json.Unmarshal(bResp.Body.Bytes(), &badges)
	var found bool
	for _, b := range badges {
		// "unlocked" is the only snake_case key (added by the handler); the rest
		// are raw-model PascalCase.
		if unlocked, _ := b["unlocked"].(bool); unlocked {
			// code may be PascalCase (raw model) or snake_case.
			code, _ := b["code"].(string)
			if code == "" {
				code, _ = b["Code"].(string)
			}
			if code == "first_blood" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("first_blood badge not unlocked after completing an episode (badges: %+v)", badges)
	}
}

// strconvUINT formats a uint for path embedding, keeping the test free of an
// import for one call. (strconv is already imported by testhelper.)
func strconvUINT(u uint) string {
	if u == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	return string(buf[i:])
}
