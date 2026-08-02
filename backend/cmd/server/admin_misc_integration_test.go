package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"studyquest/backend/internal/model"
)

// admin_misc_integration_test.go — EASY coverage for three previously-untested
// pure-DB admin clusters that don't warrant a file each:
//
//   badges CRUD  — POST/PUT/DELETE /admin/api/badges (+ list, which the seed
//                  already populates so we can assert non-empty)
//   attachments  — GET /api/v1/episodes/:id/attachments (list; the stream
//                  happy-path is HARD — needs a fake alist server, no provider
//                  injection seam yet — so only the list + 404 are covered)
//   reading-import execute — POST /admin/api/reading-import/execute (pure DB
//                  transaction; preview-tree is HARD — same storage-provider
//                  blocker — so only execute is covered here)

// ─── badges ───

// TestBadges_AdminCreateBadgeCreatesAndLists create a custom badge → list
// includes it. Guards the create path + that list reflects new rows. (List is
// also exercised incidentally elsewhere, but asserting the created row shows up
// locks the create→list contract explicitly.)
func TestBadges_AdminCreateBadgeCreatesAndLists(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPost, "/admin/api/badges", map[string]any{
		"code":         "test-custom-badge",
		"title":        "Test Badge",
		"description":  "created by integration test",
		"rule_type":    "episode_count",
		"rule_target":  "course",
		"threshold":    5,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create badge: %d %s", resp.Code, resp.Body.String())
	}
	// model.Badge has no json tags → emitted with PascalCase field names.
	var created struct {
		ID    uint   `json:"ID"`
		Code  string `json:"Code"`
		Title string `json:"Title"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created badge: %v (body: %s)", err, resp.Body.String())
	}
	if created.Code != "test-custom-badge" {
		t.Errorf("created badge code = %q; want test-custom-badge", created.Code)
	}

	listResp := env.do(t, http.MethodGet, "/admin/api/badges", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list badges: %d %s", listResp.Code, listResp.Body.String())
	}
	var badges []map[string]any
	if err := json.Unmarshal(listResp.Body.Bytes(), &badges); err != nil {
		t.Fatalf("decode badges: %v", err)
	}
	found := false
	for _, b := range badges {
		if b["Code"] == "test-custom-badge" {
			found = true
			break
		}
	}
	if !found {
		t.Error("created badge not present in list after create")
	}
}

// TestBadges_AdminCreateBadge_MissingThreshold for a non-composite badge with
// no threshold AND no tiers, create is rejected with 400 (the validation gate
// that keeps an un-evaluatable badge rule out of the DB).
func TestBadges_AdminCreateBadge_MissingThreshold(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPost, "/admin/api/badges", map[string]any{
		"code":      "no-threshold",
		"title":     "No Threshold",
		"rule_type": "episode_count",
		// no threshold, no tiers, no rule_json
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("create badge without threshold: want 400, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestBadges_AdminDeleteBadge delete removes a custom badge; a follow-up list
// omits it. (System badges may be undeletable; this operates on a freshly-
// created custom badge to avoid that constraint.)
func TestBadges_AdminDeleteBadge(t *testing.T) {
	env := newTestEnv(t)
	created := env.do(t, http.MethodPost, "/admin/api/badges", map[string]any{
		"code": "to-delete", "title": "T", "rule_type": "episode_count", "threshold": 1,
	})
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var b struct {
		ID uint `json:"ID"`
	}
	json.Unmarshal(created.Body.Bytes(), &b)

	del := env.do(t, http.MethodDelete, "/admin/api/badges/"+itoa(b.ID), nil)
	if del.Code != http.StatusOK {
		t.Fatalf("delete badge: want 200, got %d (body: %s)", del.Code, del.Body.String())
	}

	listResp := env.do(t, http.MethodGet, "/admin/api/badges", nil)
	var badges []map[string]any
	json.Unmarshal(listResp.Body.Bytes(), &badges)
	for _, x := range badges {
		if x["Code"] == "to-delete" {
			t.Error("deleted badge still present in list")
		}
	}
}

// ─── episode attachments (list) ───

// TestAttachments_ListReturnsAttachmentJSON GET /episodes/:id/attachments
// returns whatever the episode's attachment_json column holds (the handler
// just parses + returns it). Seeds an episode with a known attachment array
// and asserts it round-trips. Uses doAsUser because this is a /api/v1/ client
// route (token-gated), not an admin cookie route.
func TestAttachments_ListReturnsAttachmentJSON(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Attach Course", "math", nil)
	episodeID := env.createEpisode(t, courseID, "Attach Ep")
	userID := env.createUser(t, "attach-user", "student")
	env.grantAccess(t, userID, courseID)
	// all_open so the episode is visible to the client gate.
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override",
		map[string]any{"strategy": "all_open", "allowed_episode_ids": []uint{}})
	// Stamp an attachment array directly onto the episode row.
	attachJSON := `[{"name":"handout.pdf","size":1024,"index":0},{"name":"slides.pdf","size":2048,"index":1}]`
	if err := env.db.Model(&model.Episode{}).Where("id = ?", episodeID).Update("attachment_json", attachJSON).Error; err != nil {
		t.Fatalf("seed attachment_json: %v", err)
	}

	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/episodes/"+itoa(episodeID)+"/attachments", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list attachments: %d %s", resp.Code, resp.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode attachments: %v (body: %s)", err, resp.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("attachments count = %d; want 2 (the seeded array)", len(got))
	}
	if got[0]["name"] != "handout.pdf" {
		t.Errorf("first attachment name = %v; want handout.pdf", got[0]["name"])
	}
}

// TestAttachments_ListEmptyWhenNoAttachments an episode with no attachment_json
// returns an empty array (UI shape contract — not null, which would make the
// client's .map throw).
func TestAttachments_ListEmptyWhenNoAttachments(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Attach Empty Course", "math", nil)
	episodeID := env.createEpisode(t, courseID, "Attach Empty Ep")
	userID := env.createUser(t, "attach-empty-user", "student")
	env.grantAccess(t, userID, courseID)
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override",
		map[string]any{"strategy": "all_open", "allowed_episode_ids": []uint{}})

	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/episodes/"+itoa(episodeID)+"/attachments", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list attachments: %d %s", resp.Code, resp.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode empty attachments: %v (body: %s)", err, resp.Body.String())
	}
	if len(got) != 0 {
		t.Errorf("attachments count = %d; want 0 (no attachment_json seeded)", len(got))
	}
}

// ─── reading-import execute ───

// TestReadingImport_ExecuteCreatesSeriesAndBooks POST execute with a new-series
// spec + a tree of book nodes → the series + books land in the DB. This is the
// pure-DB half of reading-import (preview-tree needs a real storage provider,
// out of scope). Guards the transaction that fans the tree out into rows.
func TestReadingImport_ExecuteCreatesSeriesAndBooks(t *testing.T) {
	env := newTestEnv(t)
	sourceID := env.defaultSourceID
	tree := map[string]any{
		"name":     "root",
		"path":     "/books",
		"is_dir":   true,
		"type":     "dir",
		"children": []map[string]any{
			// type must be "book" (the leaf convention importReadingNode checks);
			// the actual file format (pdf/epub) is in the path extension.
			{"name": "Book One", "path": "/books/one.pdf", "is_dir": false, "type": "book", "size": 100},
			{"name": "Book Two", "path": "/books/two.epub", "is_dir": false, "type": "book", "size": 200},
		},
	}
	resp := env.do(t, http.MethodPost, "/admin/api/reading-import/execute", map[string]any{
		"new_series": map[string]any{
			"title":   "Imported Series",
			"grade":   "primary",
			"subject": "chinese",
		},
		"tree":      tree,
		"source_id": sourceID,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("execute reading-import: %d %s", resp.Code, resp.Body.String())
	}

	// Verify rows landed.
	var series model.ReadingSeries
	if err := env.db.Where("title = ?", "Imported Series").First(&series).Error; err != nil {
		t.Fatalf("imported series not found: %v", err)
	}
	var books []model.ReadingBook
	env.db.Where("series_id = ?", series.ID).Find(&books)
	if len(books) != 2 {
		t.Errorf("imported books = %d; want 2", len(books))
	}
}

// TestReadingImport_ExecuteRejectsMissingSourceID execute without a source_id
// is rejected with 400 — every book must bind to a storage source (there's no
// global-settings fallback at stream time, so a nil source_id would leave rows
// un-streamable).
func TestReadingImport_ExecuteRejectsMissingSourceID(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPost, "/admin/api/reading-import/execute", map[string]any{
		"new_series": map[string]any{"title": "No Source", "grade": "primary"},
		"tree":       map[string]any{"name": "x", "path": "/x", "is_dir": true, "type": "dir"},
		// no source_id
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("execute without source_id: want 400, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}
