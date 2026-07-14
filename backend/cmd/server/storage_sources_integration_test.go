package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"studyquest/backend/internal/model"
)

// createStorageSource POSTs a storage source via the admin API and returns its
// id. Used by the storage-source integration tests.
func (e *testEnv) createStorageSource(t *testing.T, name, typ, url string, isDefault bool) uint {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/admin/api/storage-sources", map[string]any{
		"name": name, "type": typ, "url": url, "is_default": isDefault,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create storage source %q: expected 200, got %d (body: %s)", name, resp.Code, resp.Body.String())
	}
	var created struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created source: %v (body: %s)", err, resp.Body.String())
	}
	return created.ID
}

// setEpisodeSource writes source_id directly on an episode row. The admin
// create-episode endpoint doesn't set SourceID (it's an import-time field), so
// tests stamp it after creation to model an imported episode.
func (e *testEnv) setEpisodeSource(t *testing.T, episodeID, sourceID uint) {
	t.Helper()
	if err := e.db.Model(&model.Episode{}).Where("id = ?", episodeID).
		Update("source_id", sourceID).Error; err != nil {
		t.Fatalf("set episode %d source: %v", episodeID, err)
	}
}

// setStorageWhitelist PUTs the user's storage-source whitelist wholesale.
func (e *testEnv) setStorageWhitelist(t *testing.T, userID uint, sourceIDs []uint) {
	t.Helper()
	resp := e.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/storage-whitelist", map[string]any{
		"source_ids": sourceIDs,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("set whitelist user=%d: expected 200, got %d (body: %s)", userID, resp.Code, resp.Body.String())
	}
}

// TestStorageSourceCRUDEndpoint covers the admin storage-source CRUD endpoints
// end-to-end (create → list → update → delete) plus the at-most-one-default
// invariant and the ping route (which 500s without a real backend, but must
// not 404).
func TestStorageSourceCRUDEndpoint(t *testing.T) {
	env := newTestEnv(t)
	// newTestEnv seeds a "test-default" source; this test creates two more and
	// checks the CRUD lifecycle on them, tolerating the seeded row in listings.
	a := env.createStorageSource(t, "家长盘", "alist", "http://a", false)
	b := env.createStorageSource(t, "小朋友盘", "webdav", "http://b", false)

	// List returns all (seeded default + a + b); default (seeded) is first.
	resp := env.do(t, http.MethodGet, "/admin/api/storage-sources", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list: %d", resp.Code)
	}
	var list []map[string]any
	json.Unmarshal(resp.Body.Bytes(), &list)
	if len(list) < 2 {
		t.Fatalf("list len: got %d", len(list))
	}
	// The default-flagged row (the seeded test-default) must be first.
	if !list[0]["is_default"].(bool) {
		t.Errorf("default source should be listed first, got id=%v is_default=%v", list[0]["id"], list[0]["is_default"])
	}

	// Promote A to default → the seeded default loses its flag (uniqueness).
	resp = env.do(t, http.MethodPut, "/admin/api/storage-sources/"+itoa(a), map[string]any{
		"name": "家长盘", "type": "alist", "url": "http://a", "is_default": true,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("update A: %d (body: %s)", resp.Code, resp.Body.String())
	}
	resp = env.do(t, http.MethodGet, "/admin/api/storage-sources", nil)
	json.Unmarshal(resp.Body.Bytes(), &list)
	// Exactly one default now (A), and it's listed first.
	defaults := 0
	for _, s := range list {
		if s["is_default"].(bool) {
			defaults++
			if s["id"].(float64) != float64(a) {
				t.Errorf("expected A to be the sole default, got id=%v", s["id"])
			}
		}
	}
	if defaults != 1 {
		t.Errorf("expected exactly 1 default after promoting A, got %d", defaults)
	}

	// Ping hits a non-existent backend → 500 (not 404; the route resolved).
	resp = env.do(t, http.MethodPost, "/admin/api/storage-sources/"+itoa(a)+"/ping", nil)
	if resp.Code != http.StatusOK && resp.Code != http.StatusInternalServerError {
		t.Errorf("ping: expected 200 or 500, got %d", resp.Code)
	}

	// Delete B → list drops by one (B gone; seeded default + A remain).
	resp = env.do(t, http.MethodDelete, "/admin/api/storage-sources/"+itoa(b), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete B: %d", resp.Code)
	}
	resp = env.do(t, http.MethodGet, "/admin/api/storage-sources", nil)
	json.Unmarshal(resp.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("after delete, list len: got %d", len(list))
	}
}

// TestStorageWhitelistEmptyDeniesPlayInfo is THE default-deny assertion at the
// HTTP layer: a user with an empty allow-list (the default) is denied play-info
// (403) for any episode that has a SourceID, because the allow-list allows
// nothing. This replaced the old empty=unrestricted behavior once the global
// fallback was removed.
func TestStorageWhitelistEmptyDeniesPlayInfo(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, epIDs := seedCourseEpisodes(t, env, 1)

	srcA := env.createStorageSource(t, "A", "alist", "http://a", true)
	env.setEpisodeSource(t, epIDs[0], srcA)
	// No whitelist set (default-deny). play-info must 403 on the source gate.
	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/episodes/"+itoa(epIDs[0])+"/play-info", nil)
	if resp.Code != http.StatusForbidden {
		t.Errorf("empty allow-list: play-info expected 403 (default-deny), got %d", resp.Code)
	}
	_ = courseID
}

// TestStorageAccessGateDeniesWrongSource: a user whitelisted to [A] requesting
// play-info for an episode on source B → 403 (the access-time 防呆 gate).
func TestStorageAccessGateDeniesWrongSource(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, epIDs := seedCourseEpisodes(t, env, 2)

	srcA := env.createStorageSource(t, "A", "alist", "http://a", true)
	srcB := env.createStorageSource(t, "B", "webdav", "http://b", false)
	env.setEpisodeSource(t, epIDs[0], srcA) // ep1 on A
	env.setEpisodeSource(t, epIDs[1], srcB) // ep2 on B
	env.setStorageWhitelist(t, userID, []uint{srcA}) // user may only use A

	// ep1 (source A, whitelisted) → NOT 403.
	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/episodes/"+itoa(epIDs[0])+"/play-info", nil)
	if resp.Code == http.StatusForbidden {
		t.Errorf("ep1 (source A, whitelisted): play-info 403'd, should pass the source gate (got %d)", resp.Code)
	}
	// ep2 (source B, NOT whitelisted) → 403 from the source gate.
	resp = env.doAsUser(t, userID, http.MethodGet, "/api/v1/episodes/"+itoa(epIDs[1])+"/play-info", nil)
	if resp.Code != http.StatusForbidden {
		t.Errorf("ep2 (source B, not whitelisted): expected 403, got %d", resp.Code)
	}
	_ = courseID
}

// TestStorageGrantTimeGateRefuses: granting a course whose episodes live on
// source B to a user whitelisted only for A → the grant is refused (403) with
// a message naming the offending source.
func TestStorageGrantTimeGateRefuses(t *testing.T) {
	env := newTestEnv(t)
	srcA := env.createStorageSource(t, "A", "alist", "http://a", true)
	srcB := env.createStorageSource(t, "B", "webdav", "http://b", false)

	// Course with an episode on source B.
	courseID := env.createCourse(t, "受限课", "math", nil)
	epID := env.createEpisode(t, courseID, "第1节")
	env.setEpisodeSource(t, epID, srcB)

	// New user, whitelisted to [A] only. Note: don't use grantAccess (it
	// t.Fatalf's on non-200); call do directly to observe the refusal.
	userID := env.createUser(t, "受限用户", "student")
	env.setStorageWhitelist(t, userID, []uint{srcA})

	resp := env.do(t, http.MethodPost, "/admin/api/access", map[string]any{
		"user_id": userID, "course_id": courseID,
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("grant of B-content to [A]-whitelisted user: expected 403, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var body map[string]any
	json.Unmarshal(resp.Body.Bytes(), &body)
	if msg, _ := body["error"].(string); msg == "" {
		t.Errorf("expected an error message naming the source, got body: %s", resp.Body.String())
	}
}

// TestStorageStaffBypassesAccessGate: a parent-role user is never subject to
// the source whitelist — they can reach any episode's play-info regardless of
// their own whitelist.
func TestStorageStaffBypassesAccessGate(t *testing.T) {
	env := newTestEnv(t)
	srcB := env.createStorageSource(t, "B", "webdav", "http://b", false)
	courseID := env.createCourse(t, "家长课", "math", nil)
	epID := env.createEpisode(t, courseID, "第1节")
	env.setEpisodeSource(t, epID, srcB)

	parentID := env.createUser(t, "家长", "parent")
	env.grantAccess(t, parentID, courseID)
	// Give the parent a restrictive whitelist — it must be IGNORED for staff.
	env.setStorageWhitelist(t, parentID, []uint{})

	// parent's play-info: NOT 403 (staff bypass; may be 500 from unreachable storage).
	resp := env.doAsUser(t, parentID, http.MethodGet, "/api/v1/episodes/"+itoa(epID)+"/play-info", nil)
	if resp.Code == http.StatusForbidden {
		t.Errorf("parent should bypass source gate, got 403 (body: %s)", resp.Body.String())
	}
}

// TestStorageWhitelistRoundTripsViaUserDTO: setting a whitelist via PUT is
// reflected back in the user DTO (ListUsers / GetUser), so the Users drawer
// can render the checkboxes from server state.
func TestStorageWhitelistRoundTripsViaUserDTO(t *testing.T) {
	env := newTestEnv(t)
	srcA := env.createStorageSource(t, "A", "alist", "http://a", true)
	srcB := env.createStorageSource(t, "B", "webdav", "http://b", false)
	userID := env.createUser(t, "用户", "student")

	env.setStorageWhitelist(t, userID, []uint{srcA, srcB})

	resp := env.do(t, http.MethodGet, "/admin/api/users", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list users: %d", resp.Code)
	}
	var users []map[string]any
	json.Unmarshal(resp.Body.Bytes(), &users)
	for _, u := range users {
		if uint(u["id"].(float64)) != userID {
			continue
		}
		acc, _ := u["storage_source_access"].([]any)
		if len(acc) != 2 {
			t.Errorf("storage_source_access: expected 2 ids, got %v", acc)
		}
		return
	}
	t.Fatal("user not found in list")
}

// TestStorageStreamGateDeniesWrongSource: the /stream endpoint (which 302s
// directly to the netdisk URL) must enforce the source whitelist just like
// play-info — otherwise a user bypasses the whitelist by calling /stream with
// a guessed episode id. This guards the C1 security fix.
func TestStorageStreamGateDeniesWrongSource(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, epIDs := seedCourseEpisodes(t, env, 2)

	srcA := env.createStorageSource(t, "A", "alist", "http://a", true)
	srcB := env.createStorageSource(t, "B", "webdav", "http://b", false)
	env.setEpisodeSource(t, epIDs[0], srcA)
	env.setEpisodeSource(t, epIDs[1], srcB)
	env.setStorageWhitelist(t, userID, []uint{srcA})

	// ep1 (A, allowed) → NOT 403 (may be 500 from unreachable storage, but the
	// source gate itself must pass).
	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/episodes/"+itoa(epIDs[0])+"/stream", nil)
	if resp.Code == http.StatusForbidden {
		t.Errorf("/stream ep1 (A, allowed): 403'd, should pass the source gate (got %d)", resp.Code)
	}
	// ep2 (B, not allowed) → 403 from the source gate (must NOT reach the 302).
	resp = env.doAsUser(t, userID, http.MethodGet, "/api/v1/episodes/"+itoa(epIDs[1])+"/stream", nil)
	if resp.Code != http.StatusForbidden {
		t.Errorf("/stream ep2 (B, not allowed): expected 403, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	_ = courseID
}

// TestStorageStreamAttachmentGateDeniesWrongSource: attachments (PDFs/docs)
// have no drip/unlock semantics, so the source whitelist is their ONLY gate —
// verify /episodes/:id/attachments/:index/stream enforces it.
func TestStorageStreamAttachmentGateDeniesWrongSource(t *testing.T) {
	env := newTestEnv(t)
	courseID, userID, epIDs := seedCourseEpisodes(t, env, 1)

	srcB := env.createStorageSource(t, "B", "webdav", "http://b", false)
	env.setEpisodeSource(t, epIDs[0], srcB)
	// Whitelist a DIFFERENT source (A) so the episode's source (B) is excluded.
	srcA := env.createStorageSource(t, "A", "alist", "http://a", true)
	env.setStorageWhitelist(t, userID, []uint{srcA})

	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/episodes/"+itoa(epIDs[0])+"/attachments/0/stream", nil)
	if resp.Code != http.StatusForbidden {
		t.Errorf("/attachments/0/stream (B, not allowed): expected 403, got %d", resp.Code)
	}
	_ = courseID
}
