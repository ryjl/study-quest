package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"studyquest/backend/internal/model"
)

// unlock_admin_integration_test.go — covers the unlock-admin endpoints NOT
// already hit by unlock_integration_test.go (which exercises PUT template,
// manual-unlock, manual-unlock-undo, unlock-override GET/PUT). The gaps:
//
//	GET    /admin/api/courses/:id/unlock-template          (no-row + with-row)
//	DELETE /admin/api/courses/:id/unlock-template
//	PUT    /admin/api/users/:id/courses/:cid/allowed-episodes
//	GET    /admin/api/users/:id/courses/:cid/unlock-preview
//	GET    /admin/api/users/:id/unlock-overrides           (list)
//
// All pure-DB via unlockService (no AI, no network).

// TestUnlockTemplate_GetNoRowDefaultsToAllOpen GET template for a course with
// no saved row returns exists=false + the all_open default (the UI shows the
// default picker state, not a 404).
func TestUnlockTemplate_GetNoRowDefaultsToAllOpen(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "UnlockTpl Course", "math", nil)

	resp := env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("get template (no row): %d %s", resp.Code, resp.Body.String())
	}
	var body struct {
		CourseID uint   `json:"course_id"`
		Strategy string `json:"strategy"`
		Exists   bool   `json:"exists"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, resp.Body.String())
	}
	if body.Exists {
		t.Error("exists = true; want false (no template saved)")
	}
	if body.Strategy != model.StrategyAllOpen {
		t.Errorf("strategy = %q; want %q (the default)", body.Strategy, model.StrategyAllOpen)
	}
}

// TestUnlockTemplate_SaveGetDeleteRoundTrip PUT (save) → GET reflects the saved
// strategy → DELETE removes → GET reverts to the all_open default. Guards the
// full template CRUD lifecycle (the existing unlock_integration_test only PUTs).
func TestUnlockTemplate_SaveGetDeleteRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Unlock CRUD Course", "math", nil)

	// Save an interval strategy.
	save := env.do(t, http.MethodPut, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", map[string]any{
		"strategy":         model.StrategyInterval,
		"interval_seconds": 3600,
		"weekly_times":     []map[string]any{},
	})
	if save.Code != http.StatusOK {
		t.Fatalf("save template: %d %s", save.Code, save.Body.String())
	}

	// GET reflects it.
	got := env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", nil)
	var body struct {
		Strategy        string `json:"strategy"`
		IntervalSeconds int    `json:"interval_seconds"`
		Exists          bool   `json:"exists"`
	}
	json.Unmarshal(got.Body.Bytes(), &body)
	if !body.Exists || body.Strategy != model.StrategyInterval || body.IntervalSeconds != 3600 {
		t.Fatalf("after save: %+v; want exists/interval/3600", body)
	}

	// DELETE removes.
	del := env.do(t, http.MethodDelete, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", nil)
	if del.Code != http.StatusOK {
		t.Fatalf("delete template: %d %s", del.Code, del.Body.String())
	}

	// GET reverts to the default.
	after := env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(courseID)+"/unlock-template", nil)
	var afterBody struct {
		Strategy string `json:"strategy"`
		Exists   bool   `json:"exists"`
	}
	json.Unmarshal(after.Body.Bytes(), &afterBody)
	if afterBody.Exists || afterBody.Strategy != model.StrategyAllOpen {
		t.Errorf("after delete: %+v; want exists=false strategy=all_open", afterBody)
	}
}

// TestUnlock_AllowedEpisodesSetsAndPersists PUT allowed-episodes under a
// "selected" strategy → an unlock-preview for the same (user, course) reflects
// exactly those visible ids. Guards the allowed-list write + its effect on
// preview. (The allowed list only constrains visibility under StrategySelected;
// under all_open everything is visible regardless, so this must use selected.)
func TestUnlock_AllowedEpisodesSetsAndPersists(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Allowed Course", "math", nil)
	ep1 := env.createEpisode(t, courseID, "Ep1")
	env.createEpisode(t, courseID, "Ep2") // second episode; stays locked (not in allowed list)
	userID := env.createUser(t, "allowed-user", "student")
	env.grantAccess(t, userID, courseID)
	// Set the override to "selected" so the allowed-episodes list is the source
	// of truth (under all_open, the list is ignored — all episodes show).
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override",
		map[string]any{"strategy": model.StrategySelected, "allowed_episode_ids": []uint{}})

	// Allow only ep1.
	resp := env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/allowed-episodes",
		map[string]any{"allowed_episode_ids": []uint{ep1}})
	if resp.Code != http.StatusOK {
		t.Fatalf("set allowed-episodes: %d %s", resp.Code, resp.Body.String())
	}

	// preview should show only ep1 visible.
	prev := env.do(t, http.MethodGet, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-preview", nil)
	if prev.Code != http.StatusOK {
		t.Fatalf("unlock-preview: %d %s", prev.Code, prev.Body.String())
	}
	var body struct {
		VisibleIDs []uint `json:"visible_ids"`
		Total      int    `json:"total"`
	}
	json.Unmarshal(prev.Body.Bytes(), &body)
	if len(body.VisibleIDs) != 1 || body.VisibleIDs[0] != ep1 {
		t.Errorf("visible_ids = %v; want [%d] (the one allowed episode)", body.VisibleIDs, ep1)
	}
	if body.Total != 2 {
		t.Errorf("total = %d; want 2 (both episodes exist)", body.Total)
	}
}

// TestUnlockPreview_NoEpisodesReturnsEmptyTotalZero unlock-preview for a course
// with no episodes returns visible_ids=[] and total=0 (not an error).
func TestUnlockPreview_NoEpisodesReturnsEmptyTotalZero(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Empty Preview Course", "math", nil)
	userID := env.createUser(t, "empty-preview-user", "student")
	env.grantAccess(t, userID, courseID)
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override",
		map[string]any{"strategy": "all_open", "allowed_episode_ids": []uint{}})

	resp := env.do(t, http.MethodGet, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-preview", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("unlock-preview: %d %s", resp.Code, resp.Body.String())
	}
	var body struct {
		VisibleIDs []uint `json:"visible_ids"`
		Total      int    `json:"total"`
	}
	json.Unmarshal(resp.Body.Bytes(), &body)
	if body.Total != 0 || len(body.VisibleIDs) != 0 {
		t.Errorf("preview for empty course: total=%d visible=%v; want 0/[]", body.Total, body.VisibleIDs)
	}
}

// TestUnlock_ListUserOverridesReturnsArray GET /users/:id/unlock-overrides
// returns an array (possibly empty — UI shape contract). Seeds one override
// and asserts it shows up.
func TestUnlock_ListUserOverridesReturnsArray(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Overrides Course", "math", nil)
	userID := env.createUser(t, "overrides-user", "student")
	env.grantAccess(t, userID, courseID)
	// Seed an override via the PUT endpoint.
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override",
		map[string]any{"strategy": "all_open", "allowed_episode_ids": []uint{}})

	resp := env.do(t, http.MethodGet, "/admin/api/users/"+itoa(userID)+"/unlock-overrides", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list overrides: %d %s", resp.Code, resp.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode overrides: %v (body: %s)", err, resp.Body.String())
	}
	found := false
	for _, o := range list {
		if cid, ok := o["course_id"].(float64); ok && uint(cid) == courseID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("override for course %d not in list %v", courseID, list)
	}
}
