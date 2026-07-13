package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// loginUser posts the real login flow and returns the opaque token. Uses the
// admin-create-user PIN ("1234") that testhelper.createUser sets.
func loginUser(t *testing.T, env *testEnv, userID uint, pin string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"user_id": userID, "pin": pin})
	resp := env.doRaw(t, http.MethodPost, "/api/v1/users/login", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("login user %d: expected 200, got %d (body: %s)", userID, resp.Code, resp.Body.String())
	}
	var out struct {
		Token  string `json:"token"`
		Role   string `json:"role"`
		UserID uint   `json:"user_id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if out.Token == "" {
		t.Fatal("login response missing token")
	}
	if out.UserID != userID {
		t.Fatalf("login user_id = %d, want %d", out.UserID, userID)
	}
	return out.Token
}

// TestLoginFlow_HappyPath verifies the real login endpoint issues an opaque
// token that works against a protected route.
func TestLoginFlow_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "alice", "student")

	tok := loginUser(t, env, uid, "1234")

	// The issued token must authenticate a protected endpoint.
	resp := env.doAsUserToken(t, tok, http.MethodGet, "/api/v1/subjects", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("token should authenticate; got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestLoginFlow_WrongPin verifies a wrong PIN is rejected and issues NO token.
func TestLoginFlow_WrongPin(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "bob", "student")

	body, _ := json.Marshal(map[string]any{"user_id": uid, "pin": "9999"})
	resp := env.doRaw(t, http.MethodPost, "/api/v1/users/login", body)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("wrong PIN: expected 401, got %d", resp.Code)
	}
}

// TestLoginFlow_UnknownUser verifies login for a non-existent user is rejected.
func TestLoginFlow_UnknownUser(t *testing.T) {
	env := newTestEnv(t)
	body, _ := json.Marshal(map[string]any{"user_id": 999999, "pin": "1234"})
	resp := env.doRaw(t, http.MethodPost, "/api/v1/users/login", body)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: expected 401, got %d", resp.Code)
	}
}

// TestLogout_InvalidatesToken verifies the logout endpoint revokes the
// presented token, so subsequent requests with it fail.
func TestLogout_InvalidatesToken(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "carol", "student")
	tok := loginUser(t, env, uid, "1234")

	// Logout via the real endpoint (carries the token in Authorization).
	req := newRequest(http.MethodPost, "/api/v1/users/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := serve(env, req)
	if w.Code != http.StatusOK {
		t.Fatalf("logout: expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	// The token must now be rejected.
	resp := env.doAsUserToken(t, tok, http.MethodGet, "/api/v1/subjects", nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("token should be invalid after logout; got %d", resp.Code)
	}
}

// TestProtectedRoute_NoAuthRejected verifies a protected endpoint rejects
// requests with no Authorization header. /subjects is behind UserAuthMiddleware.
func TestProtectedRoute_NoAuthRejected(t *testing.T) {
	env := newTestEnv(t)
	resp := env.doRaw(t, http.MethodGet, "/api/v1/subjects", nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth request should be rejected; got %d", resp.Code)
	}
}

// REGRESSION GUARD: the legacy X-User-ID header must NOT authenticate against
// the real router (not just the unit-tested middleware). This catches any
// wiring that re-enables X-User-ID.
func TestProtectedRoute_LegacyXUserIDRejected(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "dave", "student")
	req := newRequest(http.MethodGet, "/api/v1/subjects", nil)
	req.Header.Set("X-User-ID", itoa(uid))
	w := serve(env, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("X-User-ID must NOT authenticate via the router; got %d", w.Code)
	}
}

// TestMultiDevice_SameUserIndependentSessions verifies logging the same user
// in twice yields two independent, both-valid tokens.
func TestMultiDevice_SameUserIndependentSessions(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "eve", "student")

	// Two logins on "different devices".
	tokA := loginUser(t, env, uid, "1234")
	tokB := loginUser(t, env, uid, "1234")
	if tokA == tokB {
		t.Fatal("two logins returned the same token; multi-device support broken")
	}

	// Both must work independently.
	for i, tok := range []string{tokA, tokB} {
		resp := env.doAsUserToken(t, tok, http.MethodGet, "/api/v1/subjects", nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("device %d token should work; got %d", i, resp.Code)
		}
	}
}

// TestAdminKick_RevokeSingleDevice verifies the admin can revoke one device
// session without affecting others.
func TestAdminKick_RevokeSingleDevice(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "frank", "student")
	tokA := loginUser(t, env, uid, "1234")
	tokB := loginUser(t, env, uid, "1234")

	// Admin revokes device A.
	path := "/admin/api/users/" + itoa(uid) + "/sessions/" + tokA
	resp := env.do(t, http.MethodDelete, path, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin revoke single: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	// A is invalid, B still valid.
	if r := env.doAsUserToken(t, tokA, http.MethodGet, "/api/v1/subjects", nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token A should fail; got %d", r.Code)
	}
	if r := env.doAsUserToken(t, tokB, http.MethodGet, "/api/v1/subjects", nil); r.Code != http.StatusOK {
		t.Fatalf("token B should still work; got %d", r.Code)
	}
}

// TestAdminKick_RevokeAllDevices verifies the bulk-revoke endpoint signs a
// user out of every device.
func TestAdminKick_RevokeAllDevices(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "grace", "student")
	tokA := loginUser(t, env, uid, "1234")
	tokB := loginUser(t, env, uid, "1234")
	tokC := loginUser(t, env, uid, "1234")

	path := "/admin/api/users/" + itoa(uid) + "/sessions"
	if resp := env.do(t, http.MethodDelete, path, nil); resp.Code != http.StatusOK {
		t.Fatalf("admin revoke all: expected 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	for i, tok := range []string{tokA, tokB, tokC} {
		if r := env.doAsUserToken(t, tok, http.MethodGet, "/api/v1/subjects", nil); r.Code != http.StatusUnauthorized {
			t.Fatalf("token %d should be revoked; got %d", i, r.Code)
		}
	}
}

// TestAdminKick_CrossUserIsolation verifies revoking user 1's sessions does
// not affect user 2.
func TestAdminKick_CrossUserIsolation(t *testing.T) {
	env := newTestEnv(t)
	uid1 := env.createUser(t, "heidi", "student")
	uid2 := env.createUser(t, "ivan", "student")
	tok1 := loginUser(t, env, uid1, "1234")
	tok2 := loginUser(t, env, uid2, "1234")

	// Revoke all of user 1.
	env.do(t, http.MethodDelete, "/admin/api/users/"+itoa(uid1)+"/sessions", nil)

	if r := env.doAsUserToken(t, tok1, http.MethodGet, "/api/v1/subjects", nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("user 1 token should be revoked; got %d", r.Code)
	}
	if r := env.doAsUserToken(t, tok2, http.MethodGet, "/api/v1/subjects", nil); r.Code != http.StatusOK {
		t.Fatalf("user 2 token must remain valid; got %d", r.Code)
	}
}

// TestAdminListSessions_ShowsDeviceName verifies device_name sent at login is
// persisted and visible in the admin device list.
func TestAdminListSessions_ShowsDeviceName(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "judy", "student")

	// Login with a device_name.
	body, _ := json.Marshal(map[string]any{"user_id": uid, "pin": "1234", "device_name": "客厅iPad"})
	resp := env.doRaw(t, http.MethodPost, "/api/v1/users/login", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("login: %d (body: %s)", resp.Code, resp.Body.String())
	}

	list := env.do(t, http.MethodGet, "/admin/api/users/"+itoa(uid)+"/sessions", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list sessions: %d (body: %s)", list.Code, list.Body.String())
	}
	var sessions []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if sessions[0]["device_name"] != "客厅iPad" {
		t.Fatalf("device_name = %v, want 客厅iPad", sessions[0]["device_name"])
	}
}

// TestAdminUpdateSessionNote verifies the PATCH note endpoint persists a
// human-friendly label and that it shows up in the next list call.
func TestAdminUpdateSessionNote(t *testing.T) {
	env := newTestEnv(t)
	uid := env.createUser(t, "karl", "student")
	tok := loginUser(t, env, uid, "1234")

	patch := env.do(t, http.MethodPatch, "/admin/api/sessions/"+tok+"/note", map[string]any{"note": "客厅那台"})
	if patch.Code != http.StatusOK {
		t.Fatalf("patch note: %d (body: %s)", patch.Code, patch.Body.String())
	}

	list := env.do(t, http.MethodGet, "/admin/api/users/"+itoa(uid)+"/sessions", nil)
	var sessions []map[string]any
	json.Unmarshal(list.Body.Bytes(), &sessions)
	if len(sessions) != 1 || sessions[0]["note"] != "客厅那台" {
		t.Fatalf("note not persisted; got %+v", sessions)
	}
}

// TestStreamRoute_RequiresAuth verifies /episodes/:id/stream is no longer
// public — it must require a valid token now.
func TestStreamRoute_RequiresAuth(t *testing.T) {
	env := newTestEnv(t)
	// No need for a real episode; the auth gate runs before the handler logic,
	// so even a bogus id should 401 (not 5xx/redirect) when unauthenticated.
	resp := env.doRaw(t, http.MethodGet, "/api/v1/episodes/1/stream", nil)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("stream without token: expected 401, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestIngestRoute_RequiresKeyWhenConfigured is a sanity check that the ingest
// middleware is mounted. The default test env has an EMPTY key (ingest open),
// so we only assert the open behavior here; the key-enforced behavior is
// covered by the middleware unit tests.
func TestIngestRoute_OpenByDefaultInTestEnv(t *testing.T) {
	env := newTestEnv(t)
	// POST with an empty body; the handler will reject on validation, but NOT
	// with 401 — that's the point. Any non-401 means the middleware let it by.
	body := strings.NewReader("{}")
	req := newRequest(http.MethodPost, "/api/v1/ingest/episodes", body)
	req.Header.Set("Content-Type", "application/json")
	w := serve(env, req)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("ingest should be open with empty key; got 401 (body: %s)", w.Body.String())
	}
	// We don't care about the exact non-401 code (likely 400 from the handler).
}
