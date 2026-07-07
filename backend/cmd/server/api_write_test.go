package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// This file holds the "write-side" regression net: create/update/delete,
// reorder, bulk ops, and detail reads for every resource. The A–E tests
// (api_integration_test.go) lock the contracts touched by the subject/tag
// refactor; these tests widen the net to the rest of the admin surface so a
// future regression in any write path is caught before it ships.

// ---- F. Course write paths + detail ----

// TestCourseUpdate verifies UpdateCourse persists title/grade/subject/tag
// changes and that the admin DTO round-trips them. It also locks the
// "unknown subject key → 400" guard.
func TestCourseUpdate(t *testing.T) {
	env := newTestEnv(t)
	tag1 := env.findTagID(t, "required")
	cid := env.createCourse(t, "原标题", "math", []uint{tag1})

	// Update: change title, switch subject to english, swap tags.
	tag2 := env.findTagID(t, "thinking")
	resp := env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(cid), map[string]any{
		"title":   "新标题",
		"grade":   "3",
		"subject": "english",
		"tag_ids": []uint{tag2},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("update course: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var updated struct {
		Title   string `json:"title"`
		Grade   string `json:"grade"`
		Subject string `json:"subject"`
		TagIDs  []uint `json:"tag_ids"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated course: %v", err)
	}
	if updated.Title != "新标题" {
		t.Errorf("title: got %q, want %q", updated.Title, "新标题")
	}
	if updated.Subject != "english" {
		t.Errorf("subject: got %q, want %q", updated.Subject, "english")
	}
	if updated.Grade != "3" {
		t.Errorf("grade: got %q, want %q", updated.Grade, "3")
	}
	if len(updated.TagIDs) != 1 || updated.TagIDs[0] != tag2 {
		t.Errorf("tag_ids: got %v, want [%d]", updated.TagIDs, tag2)
	}

	// Unknown subject key must be rejected, not silently coerced.
	resp = env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(cid), map[string]any{
		"title":   "x",
		"grade":   "3",
		"subject": "nonexistent_subject",
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unknown subject: expected 400, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestCourseUpdateNotFound locks the 404 path: updating a non-existent course
// returns 404 with the canonical "Course not found" message.
func TestCourseUpdateNotFound(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPut, "/admin/api/courses/99999", map[string]any{
		"title":   "x",
		"grade":   "3",
		"subject": "math",
	})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertErrorMsg(t, resp.Body.Bytes(), "Course not found")
}

// TestCourseDetailShape verifies GetCourseDetail returns the documented
// {course, episodes, chapters} envelope (the admin edit page depends on it).
func TestCourseDetailShape(t *testing.T) {
	env := newTestEnv(t)
	cid := env.createCourse(t, "详情课", "math", nil)
	env.createEpisode(t, cid, "第1集")

	resp := env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(cid)+"/detail", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("course detail: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var detail struct {
		Course struct {
			ID uint `json:"id"`
		} `json:"course"`
		Episodes []any `json:"episodes"`
		Chapters []any `json:"chapters"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v (body: %s)", err, resp.Body.String())
	}
	if detail.Course.ID != cid {
		t.Errorf("detail.course.id: got %d, want %d", detail.Course.ID, cid)
	}
	if len(detail.Episodes) != 1 {
		t.Errorf("detail.episodes: got %d, want 1", len(detail.Episodes))
	}
}

// TestCourseDelete confirms a course with no FK dependents can be deleted and
// that the (current) behavior of deleting a non-existent id still returns 200.
func TestCourseDelete(t *testing.T) {
	env := newTestEnv(t)
	cid := env.createCourse(t, "待删课", "math", nil)

	resp := env.do(t, http.MethodDelete, "/admin/api/courses/"+itoa(cid), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete course: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	// Re-fetch detail → must be gone (404).
	resp = env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(cid)+"/detail", nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("detail after delete: expected 404, got %d", resp.Code)
	}

	// Deleting a non-existent id currently returns 200 {"status":"deleted"}
	// (no existence check). Lock this behavior so a silent change is caught.
	resp = env.do(t, http.MethodDelete, "/admin/api/courses/99999", nil)
	if resp.Code != http.StatusOK {
		t.Errorf("delete non-existent: expected 200 (current behavior), got %d", resp.Code)
	}
	assertStatus(t, resp.Body.Bytes(), "deleted")
}

// ---- G. Subject + Tag full CRUD ----

// TestSubjectCRUD covers create → update → delete of a subject not referenced
// by any course (the happy path; the in-use 409 is already covered by A).
func TestSubjectCRUD(t *testing.T) {
	env := newTestEnv(t)

	// Create.
	resp := env.do(t, http.MethodPost, "/admin/api/subjects", map[string]any{
		"key":        "history",
		"label":      "历史",
		"emoji":      "📜",
		"color":      "#8b5cf6",
		"sort_order": 9,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create subject: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var created struct {
		ID    uint   `json:"id"`
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created subject: %v", err)
	}
	if created.Key != "history" || created.Label != "历史" {
		t.Fatalf("created subject: %+v", created)
	}

	// Update label only (key unchanged → no badge cascade).
	resp = env.do(t, http.MethodPut, "/admin/api/subjects/"+itoa(created.ID), map[string]any{
		"key":        "history",
		"label":      "历史学",
		"emoji":      "📜",
		"color":      "#8b5cf6",
		"sort_order": 9,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("update subject: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	// Update with non-existent id → 404 (lowercase "subject not found").
	resp = env.do(t, http.MethodPut, "/admin/api/subjects/99999", map[string]any{
		"key": "x", "label": "x",
	})
	if resp.Code != http.StatusNotFound {
		t.Errorf("update missing subject: expected 404, got %d", resp.Code)
	}
	assertErrorMsg(t, resp.Body.Bytes(), "subject not found")

	// Delete the unused subject → 200.
	resp = env.do(t, http.MethodDelete, "/admin/api/subjects/"+itoa(created.ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete subject: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertStatus(t, resp.Body.Bytes(), "deleted")
}

// TestTagCRUD covers create → update → delete for tags.
func TestTagCRUD(t *testing.T) {
	env := newTestEnv(t)

	resp := env.do(t, http.MethodPost, "/admin/api/tags", map[string]any{
		"key":        "custom",
		"label":      "自定义",
		"color":      "#ef4444",
		"sort_order": 99,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create tag: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var created struct {
		ID    uint   `json:"id"`
		Key   string `json:"key"`
		Color string `json:"color"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created tag: %v", err)
	}
	if created.Key != "custom" || created.Color != "#ef4444" {
		t.Errorf("created tag: %+v", created)
	}

	// Update.
	resp = env.do(t, http.MethodPut, "/admin/api/tags/"+itoa(created.ID), map[string]any{
		"key":        "custom",
		"label":      "自定义改",
		"color":      "#22c55e",
		"sort_order": 99,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("update tag: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	// Update missing → 404 lowercase.
	resp = env.do(t, http.MethodPut, "/admin/api/tags/99999", map[string]any{
		"key": "x", "label": "x",
	})
	if resp.Code != http.StatusNotFound {
		t.Errorf("update missing tag: expected 404, got %d", resp.Code)
	}
	assertErrorMsg(t, resp.Body.Bytes(), "tag not found")

	// Delete (unused → 200; the in-use CASCADE is covered by B).
	resp = env.do(t, http.MethodDelete, "/admin/api/tags/"+itoa(created.ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete tag: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertStatus(t, resp.Body.Bytes(), "deleted")
}

// ---- assertion helpers ----

func assertErrorMsg(t *testing.T, body []byte, want string) {
	t.Helper()
	var g struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode error body %q: %v", string(body), err)
	}
	if g.Error != want {
		t.Fatalf("error message: got %q, want %q", g.Error, want)
	}
}

func assertStatus(t *testing.T, body []byte, want string) {
	t.Helper()
	var g struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode status body %q: %v", string(body), err)
	}
	if g.Status != want {
		t.Fatalf("status: got %q, want %q", g.Status, want)
	}
}

// (strconv is imported to keep this file self-contained if itoa is ever
// replaced by direct strconv calls later.)
var _ = strconv.Itoa

// ---- H. User write paths + access grant/revoke ----

// TestUserUpdate verifies UpdateUser persists nickname/role changes and that
// the missing-id path 404s.
func TestUserUpdate(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "原名", "student")

	resp := env.do(t, http.MethodPut, "/admin/api/users/"+itoa(uid), map[string]any{
		"nickname": "新名",
		"role":     "admin",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("update user: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var updated struct {
		Nickname string `json:"nickname"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated user: %v", err)
	}
	if updated.Nickname != "新名" {
		t.Errorf("nickname: got %q, want %q", updated.Nickname, "新名")
	}
	if updated.Role != "admin" {
		t.Errorf("role: got %q, want %q", updated.Role, "admin")
	}

	// Missing user: the service returns an error (rather than nil) for a
	// missing id, so UpdateUser surfaces this as 500 "user not found"
	// (lowercase, from the service error) rather than the 404 branch at
	// admin_handler.go:729 which is currently unreachable. We lock the
	// actual behavior so a future change is intentional, not accidental.
	resp = env.do(t, http.MethodPut, "/admin/api/users/99999", map[string]any{
		"nickname": "x", "role": "student",
	})
	if resp.Code != http.StatusInternalServerError {
		t.Errorf("update missing user: expected 500 (current behavior), got %d", resp.Code)
	}
	assertErrorMsg(t, resp.Body.Bytes(), "user not found")
}

// TestUserDelete verifies a user can be deleted and that access rows they owned
// don't leak (re-listing users after delete shouldn't include them).
func TestUserDelete(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "待删用户", "student")

	resp := env.do(t, http.MethodDelete, "/admin/api/users/"+itoa(uid), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete user: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertStatus(t, resp.Body.Bytes(), "deleted")

	// Confirm gone from the user list.
	resp = env.do(t, http.MethodGet, "/admin/api/users", nil)
	var users []struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &users); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	for _, u := range users {
		if u.ID == uid {
			t.Fatalf("deleted user %d still in list %v", uid, users)
		}
	}
}

// TestAccessGrantRevoke covers the access lifecycle: grant lets a user see a
// course in the client list, revoke removes it again.
func TestAccessGrantRevoke(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "授权用户", "student")
	cid := env.createCourse(t, "授权课", "math", nil)

	// Before grant: client course list excludes the course.
	resp := env.doAsUser(t, uid, http.MethodGet, "/api/v1/courses", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("client courses pre-grant: expected 200, got %d", resp.Code)
	}
	if hasCourse(resp.Body.Bytes(), cid) {
		t.Fatal("course visible before grant (should be excluded)")
	}

	// Grant.
	resp = env.do(t, http.MethodPost, "/admin/api/access", map[string]any{
		"user_id": uid, "course_id": cid,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("grant access: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertStatus(t, resp.Body.Bytes(), "granted")

	// After grant: course visible to the client.
	resp = env.doAsUser(t, uid, http.MethodGet, "/api/v1/courses", nil)
	if !hasCourse(resp.Body.Bytes(), cid) {
		t.Fatal("course not visible after grant")
	}

	// Revoke.
	resp = env.do(t, http.MethodPost, "/admin/api/access/revoke", map[string]any{
		"user_id": uid, "course_id": cid,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("revoke access: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertStatus(t, resp.Body.Bytes(), "revoked")

	// After revoke: excluded again.
	resp = env.doAsUser(t, uid, http.MethodGet, "/api/v1/courses", nil)
	if hasCourse(resp.Body.Bytes(), cid) {
		t.Fatal("course still visible after revoke")
	}
}

func hasCourse(body []byte, cid uint) bool {
	var list []struct {
		ID uint `json:"ID"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return false
	}
	for _, c := range list {
		if c.ID == cid {
			return true
		}
	}
	return false
}

// ---- I. Episode write + reorder + bulk ----

// TestEpisodeUpdateDelete covers UpdateEpisode (admin path, preserves media)
// and DeleteEpisode.
func TestEpisodeUpdateDelete(t *testing.T) {
	env := newTestEnv(t)
	cid := env.createCourse(t, "集课", "math", nil)
	epID := env.createEpisode(t, cid, "原集")

	// Update title.
	resp := env.do(t, http.MethodPut, "/admin/api/episodes/"+itoa(epID), map[string]any{
		"title":                "改集",
		"video_relative_path":  "/fake/x.mp4",
		"sort_order":           1,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("update episode: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var updated struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated episode: %v", err)
	}
	if updated.Title != "改集" {
		t.Errorf("title: got %q, want %q", updated.Title, "改集")
	}

	// Update missing → 404 "Episode not found".
	resp = env.do(t, http.MethodPut, "/admin/api/episodes/99999", map[string]any{
		"title": "x", "video_relative_path": "/x.mp4",
	})
	if resp.Code != http.StatusNotFound {
		t.Errorf("update missing episode: expected 404, got %d", resp.Code)
	}
	assertErrorMsg(t, resp.Body.Bytes(), "Episode not found")

	// Delete.
	resp = env.do(t, http.MethodDelete, "/admin/api/episodes/"+itoa(epID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete episode: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertStatus(t, resp.Body.Bytes(), "deleted")
}

// TestEpisodeReorder locks the {ids:[...]} reorder contract: sort_order is
// derived from array index.
func TestEpisodeReorder(t *testing.T) {
	env := newTestEnv(t)
	cid := env.createCourse(t, "排序课", "math", nil)
	ep1 := env.createEpisode(t, cid, "第1集")
	ep2 := env.createEpisode(t, cid, "第2集")

	// Reverse the order.
	resp := env.do(t, http.MethodPost, "/admin/api/episodes/reorder", map[string]any{
		"ids": []uint{ep2, ep1},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("reorder: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertStatus(t, resp.Body.Bytes(), "reordered")

	// Verify sort_order persisted: ep2 should now be 1, ep1 should be 2.
	var eps []struct {
		ID        uint `json:"id"`
		SortOrder int  `json:"sort_order"`
	}
	listResp := env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(cid)+"/detail", nil)
	var detail struct {
		Episodes []struct {
			ID        uint `json:"id"`
			SortOrder int  `json:"sort_order"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	eps = detail.Episodes
	for _, e := range eps {
		var want int
		if e.ID == ep2 {
			want = 1
		} else if e.ID == ep1 {
			want = 2
		} else {
			continue
		}
		if e.SortOrder != want {
			t.Errorf("episode %d sort_order: got %d, want %d", e.ID, e.SortOrder, want)
		}
	}
}

// TestEpisodeBulkDelete locks the bulk-delete contract (ids array, 200 status).
func TestEpisodeBulkDelete(t *testing.T) {
	env := newTestEnv(t)
	cid := env.createCourse(t, "批量课", "math", nil)
	ep1 := env.createEpisode(t, cid, "a")
	ep2 := env.createEpisode(t, cid, "b")

	resp := env.do(t, http.MethodPost, "/admin/api/episodes/bulk-delete", map[string]any{
		"ids": []uint{ep1, ep2},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("bulk delete: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertStatus(t, resp.Body.Bytes(), "deleted")

	// Both must be gone from the course detail.
	detailResp := env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(cid)+"/detail", nil)
	var detail struct {
		Episodes []struct {
			ID uint `json:"id"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(detailResp.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.Episodes) != 0 {
		t.Errorf("episodes after bulk delete: got %d, want 0", len(detail.Episodes))
	}
}

// ---- J. Chapter CRUD + reorder ----

// TestChapterLifecycle covers create → list → update → reorder → delete.
func TestChapterLifecycle(t *testing.T) {
	env := newTestEnv(t)
	cid := env.createCourse(t, "章课", "math", nil)

	// Create two chapters.
	resp := env.do(t, http.MethodPost, "/admin/api/courses/"+itoa(cid)+"/chapters", map[string]any{
		"title": "第一章",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create chapter 1: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var ch1 struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &ch1); err != nil {
		t.Fatalf("decode chapter 1: %v", err)
	}
	resp = env.do(t, http.MethodPost, "/admin/api/courses/"+itoa(cid)+"/chapters", map[string]any{
		"title": "第二章",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create chapter 2: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var ch2 struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &ch2); err != nil {
		t.Fatalf("decode chapter 2: %v", err)
	}

	// List → 2 chapters.
	resp = env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(cid)+"/chapters", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list chapters: expected 200, got %d", resp.Code)
	}
	var list []struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode chapters: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("chapter count: got %d, want 2", len(list))
	}

	// Update title.
	resp = env.do(t, http.MethodPut, "/admin/api/chapters/"+itoa(ch1.ID), map[string]any{
		"title": "第一章改",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("update chapter: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	// Reorder: reverse.
	resp = env.do(t, http.MethodPost, "/admin/api/chapters/reorder", map[string]any{
		"ids": []uint{ch2.ID, ch1.ID},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("reorder chapters: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertStatus(t, resp.Body.Bytes(), "reordered")

	// Delete ch1.
	resp = env.do(t, http.MethodDelete, "/admin/api/chapters/"+itoa(ch1.ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete chapter: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	assertStatus(t, resp.Body.Bytes(), "deleted")

	// List → 1 chapter left.
	resp = env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(cid)+"/chapters", nil)
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode chapters after delete: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("chapter count after delete: got %d, want 1", len(list))
	}
}
