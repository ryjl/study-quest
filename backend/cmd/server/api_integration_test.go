package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"studyquest/backend/internal/model"
)

// TestSmoke verifies the testEnv wiring end-to-end: the server boots, the
// admin login flow yields a usable cookie, and a public endpoint responds.
// If this fails, every other test in this file will too — so it's the canary.
func TestSmoke(t *testing.T) {
	env := newTestEnv(t)

	// Public endpoint, no auth.
	resp := env.do(t, http.MethodGet, "/api/v1/health", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("health: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var health struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if health.Status != "ok" {
		t.Fatalf("health status: got %q, want %q", health.Status, "ok")
	}

	// /admin/api/me must accept the admin cookie (proves the auth middleware
	// path works, not just login).
	resp = env.do(t, http.MethodGet, "/admin/api/me", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin /me: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// ---- A. Subject: FK RESTRICT + key cascade into badge rules ----

// TestSubjectDeleteWithCourse locks in that a subject referenced by a course
// cannot be deleted (the GORM OnDelete:RESTRICT → ErrSubjectInUse → 409 path).
func TestSubjectDeleteWithCourse(t *testing.T) {
	env := newTestEnv(t)
	// Use a custom (non-system) subject so the delete actually reaches the
	// FK-RESTRICT guard. The seeded "math" is now IsSystem=true and would be
	// refused with 403 before the RESTRICT check runs (covered separately by
	// TestSystemSubjectNotDeletable).
	customID := env.createSubject(t, "custom", "自建科目", "🧩", "#abc123")

	// Create a course bound to the custom subject. Its subject_id FK now RESTRICTs deletion.
	env.createCourse(t, "自建科目课", "custom", nil)

	resp := env.do(t, http.MethodDelete, "/admin/api/subjects/"+itoa(customID), nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("delete subject in use: expected 409, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	// Don't assert on the exact message wording — it's centralized in
	// handler/respondError and intentionally generic. Status 409 is the
	// stable contract.
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error == "" {
		t.Fatalf("error body empty: %s", resp.Body.String())
	}
}

// TestSystemSubjectNotDeletable locks in that seeded-default subjects (IsSystem)
// are refused with 403, even when no course references them — the protection
// is intentional so the canonical catalog always survives.
func TestSystemSubjectNotDeletable(t *testing.T) {
	env := newTestEnv(t)
	mathID := env.findSubjectID(t, "math")

	resp := env.do(t, http.MethodDelete, "/admin/api/subjects/"+itoa(mathID), nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("delete system subject: expected 403, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestSubjectRenameKeyCascadesBadge locks in that renaming a subject's key
// rewrites the rule_target of subject_count badges (math_expert → mathematics),
// so badges stay aligned with subject keys after a rename.
func TestSubjectRenameKeyCascadesBadge(t *testing.T) {
	env := newTestEnv(t)
	mathID := env.findSubjectID(t, "math")

	resp := env.do(t, http.MethodPut, "/admin/api/subjects/"+itoa(mathID), map[string]any{
		"key":    "mathematics",
		"label":  "数学",
		"emoji":  "📐",
		"color":  "#f59e0b",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("rename subject: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	// The auto-generated subject badge is seeded as subject_math with
	// rule_type=subject_count, rule_target=math. After the rename the badge's
	// code AND rule_target must both point at the new key. (model.Badge has no
	// JSON tags, so AdminListBadges emits PascalCase field names.)
	ruleTarget := badgeRuleTarget(t, env, "subject_mathematics")
	if ruleTarget != "mathematics" {
		t.Fatalf("subject_mathematics RuleTarget after rename: got %q, want %q", ruleTarget, "mathematics")
	}
}

// badgeRuleTarget lists badges and returns the rule_target of the one with the
// given code.
func badgeRuleTarget(t *testing.T, env *testEnv, code string) string {
	t.Helper()
	resp := env.do(t, http.MethodGet, "/admin/api/badges", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list badges: expected 200, got %d", resp.Code)
	}
	// model.Badge carries no json tags, so the response uses PascalCase.
	var list []struct {
		Code       string `json:"Code"`
		RuleType   string `json:"RuleType"`
		RuleTarget string `json:"RuleTarget"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode badges: %v", err)
	}
	for _, b := range list {
		if b.Code == code {
			return b.RuleTarget
		}
	}
	t.Fatalf("badge %q not found in %v", code, list)
	return ""
}

// ---- B. Tag: many2many CASCADE on delete ----

// TestTagDeleteDetachesFromCourse locks in that deleting a tag removes it from
// every course via the course_tags join-table OnDelete:CASCADE, and that the
// course DTO reflects the removal on the next read.
func TestTagDeleteDetachesFromCourse(t *testing.T) {
	env := newTestEnv(t)
	// Use custom (non-system) tags so the delete actually fires the CASCADE.
	// Seeded tags are IsSystem=true and refused with 403 (covered by
	// TestSystemTagNotDeletable).
	tag1 := env.createTag(t, "custom1", "自建标签1", "#111111")
	tag2 := env.createTag(t, "custom2", "自建标签2", "#222222")

	cid := env.createCourse(t, "标签课", "math", []uint{tag1, tag2})

	// Delete tag1; the join row must cascade away, leaving only tag2.
	resp := env.do(t, http.MethodDelete, "/admin/api/tags/"+itoa(tag1), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete tag: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	got := courseByID(t, env, cid)
	want := []uint{tag2}
	if !reflect.DeepEqual(got.TagIDs, want) {
		t.Fatalf("tag_ids after cascade delete: got %v, want %v", got.TagIDs, want)
	}
}

// TestSystemTagNotDeletable locks in that seeded-default tags (IsSystem) are
// refused with 403 regardless of usage.
func TestSystemTagNotDeletable(t *testing.T) {
	env := newTestEnv(t)
	requiredID := env.findTagID(t, "required")

	resp := env.do(t, http.MethodDelete, "/admin/api/tags/"+itoa(requiredID), nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("delete system tag: expected 403, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// courseByID lists admin courses and returns the one matching id (decoded with
// just the tag-related fields the assertions care about).
func courseByID(t *testing.T, env *testEnv, id uint) struct {
	TagIDs []uint `json:"tag_ids"`
} {
	t.Helper()
	resp := env.do(t, http.MethodGet, "/admin/api/courses", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list courses: expected 200, got %d", resp.Code)
	}
	var list []struct {
		ID     uint   `json:"id"`
		TagIDs []uint `json:"tag_ids"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode courses: %v", err)
	}
	for _, c := range list {
		if c.ID == id {
			return struct {
				TagIDs []uint `json:"tag_ids"`
			}{c.TagIDs}
		}
	}
	t.Fatalf("course %d not found in %v", id, list)
	return struct {
		TagIDs []uint `json:"tag_ids"`
	}{}
}

// itoa is a tiny uint→string helper to keep test bodies free of strconv noise.
func itoa(id uint) string {
	const digits = "0123456789"
	if id == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for id > 0 {
		i--
		buf[i] = digits[id%10]
		id /= 10
	}
	return string(buf[i:])
}

// (silence unused-import during incremental file growth: model is used by
// the E test's grade assertion.)
var _ = model.GradeUniversal

// ---- C. Course DTO dual contract (admin snake_case vs client PascalCase) ----

// TestCourseDTOContracts locks in that the admin and client course endpoints
// emit DIFFERENT field-name conventions from the same underlying course:
//   - admin /admin/api/courses  → snake_case (tag_ids, tags_list, subject_id)
//   - client /api/v1/courses    → PascalCase (TagIDs, TagsList, Subject)
// Both are consumed by different frontends, so both contracts must hold.
func TestCourseDTOContracts(t *testing.T) {
	env := newTestEnv(t)
	tag := env.findTagID(t, "required")
	cid := env.createCourse(t, "契约测试", "math", []uint{tag})

	// --- admin side: snake_case ---
	resp := env.do(t, http.MethodGet, "/admin/api/courses", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin list courses: expected 200, got %d", resp.Code)
	}
	var adminList []struct {
		ID        uint     `json:"id"`
		Title     string   `json:"title"`
		Subject   string   `json:"subject"`
		SubjectID uint     `json:"subject_id"`
		TagIDs    []uint   `json:"tag_ids"`
		TagsList  []string `json:"tags_list"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &adminList); err != nil {
		t.Fatalf("decode admin courses: %v (body: %s)", err, resp.Body.String())
	}
	var adminMatch bool
	for _, c := range adminList {
		if c.ID == cid {
			adminMatch = true
			if c.Subject != "math" {
				t.Errorf("admin subject: got %q, want %q", c.Subject, "math")
			}
			if c.SubjectID == 0 {
				t.Error("admin subject_id: got 0, want non-zero")
			}
			if !uintSliceEqual(c.TagIDs, []uint{tag}) {
				t.Errorf("admin tag_ids: got %v, want %v", c.TagIDs, []uint{tag})
			}
			if len(c.TagsList) != 1 {
				t.Errorf("admin tags_list: got %v, want 1 entry", c.TagsList)
			}
		}
	}
	if !adminMatch {
		t.Fatalf("created course id=%d not in admin list %v", cid, adminList)
	}

	// --- client side: PascalCase ---
	// Need a real user with access, since /api/v1/courses is UserAuth-gated.
	uid := env.createUser(t, "学生", "student")
	env.grantAccess(t, uid, cid)
	resp = env.doAsUser(t, uid, http.MethodGet, "/api/v1/courses", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("client list courses: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var clientList []struct {
		ID            uint     `json:"ID"`
		Title         string   `json:"Title"`
		Subject       string   `json:"Subject"`
		TagIDs        []uint   `json:"TagIDs"`
		TagsList      []string `json:"TagsList"`
		GradeDisplay  string   `json:"GradeDisplay"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &clientList); err != nil {
		t.Fatalf("decode client courses: %v (body: %s)", err, resp.Body.String())
	}
	var clientMatch bool
	for _, c := range clientList {
		if c.ID == cid {
			clientMatch = true
			if c.Subject != "math" {
				t.Errorf("client Subject: got %q, want %q", c.Subject, "math")
			}
			if !uintSliceEqual(c.TagIDs, []uint{tag}) {
				t.Errorf("client TagIDs: got %v, want %v", c.TagIDs, []uint{tag})
			}
			if len(c.TagsList) != 1 {
				t.Errorf("client TagsList: got %v, want 1 entry", c.TagsList)
			}
			if c.GradeDisplay == "" {
				t.Error("client GradeDisplay: got empty, want non-empty")
			}
		}
	}
	if !clientMatch {
		t.Fatalf("created course id=%d not in client list %v", cid, clientList)
	}
}

func uintSliceEqual(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- D. User batch aggregates (completed / watch_minutes / badges / active) ----

// TestUserListBatchAggregates drives a full progress+badge flow through the
// HTTP layer and asserts the batch-aggregated fields on ListUsers are correct.
//
// Episode duration is set directly in the DB (bypassing the probe worker),
// because ReportProgress only marks an episode completed when
// position_seconds >= duration*0.9 AND duration > 0; a freshly created
// episode has duration_seconds=nil and is never completable.
func TestUserListBatchAggregates(t *testing.T) {
	env := newTestEnv(t)

	uid := env.createUser(t, "学习达人", "student")
	cid := env.createCourse(t, "进度课", "math", nil)
	env.grantAccess(t, uid, cid)
	epID := env.createEpisode(t, cid, "第1集")
	env.setEpisodeDuration(t, epID, 100) // position>=90 completes

	// delta 120s → watch_minutes=2; position 95>=90 → completed; first_blood
	// (watch_duration>=1min) unlocks synchronously inside ReportProgress.
	env.reportProgress(t, uid, epID, 95, 120)

	resp := env.do(t, http.MethodGet, "/admin/api/users", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list users: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var users []struct {
		ID                uint   `json:"id"`
		Nickname          string `json:"nickname"`
		CompletedEpisodes int    `json:"completed_episodes"`
		WatchMinutes      int    `json:"watch_minutes"`
		UnlockedBadges    int    `json:"unlocked_badges"`
		LastActiveAt      string `json:"last_active_at"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &users); err != nil {
		t.Fatalf("decode users: %v (body: %s)", err, resp.Body.String())
	}
	var match bool
	for _, u := range users {
		if u.ID != uid {
			continue
		}
		match = true
		if u.CompletedEpisodes != 1 {
			t.Errorf("completed_episodes: got %d, want 1", u.CompletedEpisodes)
		}
		if u.WatchMinutes != 2 {
			t.Errorf("watch_minutes: got %d, want 2", u.WatchMinutes)
		}
		if u.UnlockedBadges != 4 {
			// 1 completed math episode (the course's only episode → full
			// course completion) unlocks 4 badges: first_blood (single-tier),
			// time_master (tier0=1min), subject_math (tier0=1 episode),
			// course_master (tier0=1 course).
			t.Errorf("unlocked_badges: got %d, want 4", u.UnlockedBadges)
		}
		if u.LastActiveAt == "" {
			t.Error("last_active_at: got empty, want a timestamp")
		}
	}
	if !match {
		t.Fatalf("user id=%d not in list %v", uid, users)
	}
}

// ---- E. Import: empty grade falls back to "universal" ----

// TestImportEmptyGradeFallsBackToUniversal locks in that ExecuteImport treats
// an empty grade as "universal" rather than rejecting it. The import endpoint
// consumes a pre-built tree JSON (NOT a filesystem scan), so no real files or
// ffprobe are needed. We verify the grade by reading the course back from the
// DB, since the HTTP response is just {"status":"success"}.
func TestImportEmptyGradeFallsBackToUniversal(t *testing.T) {
	env := newTestEnv(t)

	body := map[string]any{
		"new_course": map[string]any{
			"title":   "导入测试课",
			"grade":   "", // ← the whole point: empty triggers universal fallback
			"subject": "math",
		},
		"tree": map[string]any{
			"name":     "Root",
			"is_dir":   true,
			"type":     "course",
			"children": []map[string]any{
				{
					"name":    "第1集",
					"path":    "/fake/lesson1.mp4",
					"is_dir":  false,
					"type":    "episode",
					"size":    1024,
				},
			},
		},
	}
	resp := env.do(t, http.MethodPost, "/admin/api/import/execute", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("execute import: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	// Response body is just {"status":"success"}; verify the persisted grade.
	var course model.Course
	if err := env.db.Where("title = ?", "导入测试课").First(&course).Error; err != nil {
		t.Fatalf("query imported course: %v", err)
	}
	// Grades now live in the course_grades join table (one row per grade).
	var grades []model.CourseGrade
	env.db.Where("course_id = ?", course.ID).Find(&grades)
	found := false
	for _, g := range grades {
		if g.Grade == model.GradeUniversal {
			found = true
		}
	}
	if !found {
		t.Errorf("imported course grades: expected 'universal' in %+v, not found", grades)
	}
}

// TestCourseCoverFallback verifies the course DTO derives cover_fallback_url
// from the first episode (by sort_order) when the course itself has no cover,
// and leaves it empty when the course already has its own cover_url.
func TestCourseCoverFallback(t *testing.T) {
	env := newTestEnv(t)

	// Course with NO own cover; give its first episode a cover.
	cid := env.createCourse(t, "封面回退课", "math", nil)
	ep1 := env.createEpisode(t, cid, "第1集")
	env.setEpisodeCover(t, ep1, "/covers/ep1.jpg")

	// A second course that HAS its own cover — fallback must stay empty.
	cidOwn := env.createCourse(t, "自带封面课", "math", nil)
	env.db.Model(&model.Course{}).Where("id = ?", cidOwn).Update("cover_url", "/covers/own.jpg")

	// A third course with no cover AND no episode covers — fallback empty.
	cidBare := env.createCourse(t, "无封面无课时课", "math", nil)
	env.createEpisode(t, cidBare, "空集") // episode created without a cover

	resp := env.do(t, http.MethodGet, "/admin/api/courses", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list courses: %d %s", resp.Code, resp.Body.String())
	}
	var list []struct {
		ID              uint   `json:"id"`
		Title           string `json:"title"`
		CoverURL        string `json:"cover_url"`
		CoverFallbackURL string `json:"cover_fallback_url"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byID := make(map[uint]struct{ Cover, Fallback string }, len(list))
	for _, c := range list {
		byID[c.ID] = struct{ Cover, Fallback string }{c.CoverURL, c.CoverFallbackURL}
	}

	if got := byID[cid]; got.Cover != "" || got.Fallback != "/covers/ep1.jpg" {
		t.Errorf("course with no own cover: cover=%q fallback=%q, want cover=\"\" fallback=/covers/ep1.jpg", got.Cover, got.Fallback)
	}
	if got := byID[cidOwn]; got.Cover != "/covers/own.jpg" || got.Fallback != "" {
		t.Errorf("course with own cover: cover=%q fallback=%q, want fallback empty when cover present", got.Cover, got.Fallback)
	}
	if got := byID[cidBare]; got.Cover != "" || got.Fallback != "" {
		t.Errorf("course with no covers anywhere: cover=%q fallback=%q, want both empty", got.Cover, got.Fallback)
	}
}
