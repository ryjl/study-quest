package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"studyquest/backend/internal/model"
)

// Helper: create a reading series via the admin API.
func (e *testEnv) createReadingSeries(t *testing.T, title, subjectKey string) uint {
	t.Helper()
	body := map[string]any{
		"title":       title,
		"description": "",
		"grade":       "universal",
		"subject":     subjectKey,
		"cover_url":   "",
		"sort_order":  0,
		"tag_ids":     []uint{},
	}
	resp := e.do(t, http.MethodPost, "/admin/api/reading-series", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("createReadingSeries: %d %s", resp.Code, resp.Body.String())
	}
	var s struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(resp.Body.Bytes(), &s)
	return s.ID
}

// Helper: create a reading book via the admin API.
func (e *testEnv) createReadingBook(t *testing.T, title, path, subjectKey string, seriesID uint) uint {
	t.Helper()
	body := map[string]any{
		"series_id":          seriesID,
		"sort_order":         0,
		"title":              title,
		"file_relative_path": path,
		"file_hash":          "",
		"cover_url":          "",
		"grade":              "universal",
		"subject":            subjectKey,
		"tag_ids":            []uint{},
	}
	resp := e.do(t, http.MethodPost, "/admin/api/reading-books", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("createReadingBook: %d %s", resp.Code, resp.Body.String())
	}
	var b struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(resp.Body.Bytes(), &b)
	// Stamp the seeded default source so the book is streamable under the
	// default-deny storage gate (mirrors createEpisode). Storage-specific tests
	// override via direct DB writes.
	if e.defaultSourceID != 0 {
		if err := e.db.Model(&model.ReadingBook{}).Where("id = ?", b.ID).
			Update("source_id", e.defaultSourceID).Error; err != nil {
			t.Fatalf("seed reading book source: %v", err)
		}
	}
	return b.ID
}

// Helper: create a reading article via the admin API.
func (e *testEnv) createReadingArticle(t *testing.T, title, url, subjectKey string, seriesID uint) uint {
	t.Helper()
	body := map[string]any{
		"series_id":          seriesID,
		"sort_order":         0,
		"title":              title,
		"source_url":         url,
		"whitelist_domains":  "",
		"cover_url":          "",
		"grade":              "universal",
		"subject":            subjectKey,
		"tag_ids":            []uint{},
	}
	resp := e.do(t, http.MethodPost, "/admin/api/reading-articles", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("createReadingArticle: %d %s", resp.Code, resp.Body.String())
	}
	var a struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(resp.Body.Bytes(), &a)
	return a.ID
}

// Helper: grant reading access to a user.
func (e *testEnv) grantReadingAccess(t *testing.T, userID, targetID uint, targetType string) {
	t.Helper()
	body := map[string]any{
		"user_id":     userID,
		"target_type": targetType,
		"target_id":   targetID,
	}
	resp := e.do(t, http.MethodPost, "/admin/api/reading-access", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("grantReadingAccess: %d %s", resp.Code, resp.Body.String())
	}
}

// TestReadingRoomCRUD verifies the full admin CRUD cycle for series, books, and
// articles — create, list, update, delete — through the HTTP layer.
func TestReadingRoomCRUD(t *testing.T) {
	env := newTestEnv(t)

	// Create a series.
	sID := env.createReadingSeries(t, "上博系列", "chinese")
	if sID == 0 {
		t.Fatal("series ID should be non-zero")
	}

	// Create a book in the series.
	bID := env.createReadingBook(t, "恐龙百科", "/books/dino.pdf", "chinese", sID)
	if bID == 0 {
		t.Fatal("book ID should be non-zero")
	}

	// Create a standalone article.
	aID := env.createReadingArticle(t, "上博导览", "https://mp.weixin.qq.com/s/abc", "chinese", 0)
	if aID == 0 {
		t.Fatal("article ID should be non-zero")
	}

	// List series — should see 1.
	resp := env.do(t, http.MethodGet, "/admin/api/reading-series", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list series: %d", resp.Code)
	}
	var seriesList []map[string]any
	json.Unmarshal(resp.Body.Bytes(), &seriesList)
	if len(seriesList) != 1 {
		t.Fatalf("list series: got %d, want 1", len(seriesList))
	}

	// List books — should see 1.
	resp = env.do(t, http.MethodGet, "/admin/api/reading-books", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list books: %d", resp.Code)
	}
	var bookList []map[string]any
	json.Unmarshal(resp.Body.Bytes(), &bookList)
	if len(bookList) != 1 {
		t.Fatalf("list books: got %d, want 1", len(bookList))
	}

	// Update the series title.
	body := map[string]any{
		"title": "Updated Series", "description": "new", "grade": "universal",
		"subject": "chinese", "cover_url": "", "sort_order": 1, "tag_ids": []uint{},
	}
	resp = env.do(t, http.MethodPut, "/admin/api/reading-series/"+itoa(sID), body)
	if resp.Code != http.StatusOK {
		t.Fatalf("update series: %d %s", resp.Code, resp.Body.String())
	}
	var updated map[string]any
	json.Unmarshal(resp.Body.Bytes(), &updated)
	if updated["title"] != "Updated Series" {
		t.Fatalf("updated title: got %v", updated["title"])
	}

	// Delete the article.
	resp = env.do(t, http.MethodDelete, "/admin/api/reading-articles/"+itoa(aID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete article: %d", resp.Code)
	}

	// List articles — should be 0 now.
	resp = env.do(t, http.MethodGet, "/admin/api/reading-articles", nil)
	var articleList []map[string]any
	json.Unmarshal(resp.Body.Bytes(), &articleList)
	if len(articleList) != 0 {
		t.Fatalf("after delete, list articles: got %d, want 0", len(articleList))
	}
}

// TestReadingRoomAccessControl is the key integration test for the access model:
// a student with NO reading access gets 403 on stream/progress/article endpoints.
// After granting series access, the student can access books inside that series.
// This locks in the fail-closed + series-inheritance fix.
func TestReadingRoomAccessControl(t *testing.T) {
	env := newTestEnv(t)

	// Create a series with a book inside it.
	sID := env.createReadingSeries(t, "Series", "chinese")
	bID := env.createReadingBook(t, "Book", "/b.pdf", "chinese", sID)

	// Create a student.
	studentID := env.createUser(t, "student1", "student")

	// Student with no access → GET /readings should return empty shelf.
	resp := env.doAsUser(t, studentID, http.MethodGet, "/api/v1/readings", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("readings (no access): %d", resp.Code)
	}
	var view map[string]any
	json.Unmarshal(resp.Body.Bytes(), &view)
	series, _ := view["Series"].([]any)
	if len(series) != 0 {
		t.Fatalf("readings (no access): Series should be empty, got %d", len(series))
	}

	// Student with no access → stream book → 403.
	resp = env.doAsUser(t, studentID, http.MethodGet, "/api/v1/readings/books/"+itoa(bID)+"/stream", nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("stream (no access): expected 403, got %d", resp.Code)
	}

	// Student with no access → report progress → 403.
	resp = env.doAsUser(t, studentID, http.MethodPost, "/api/v1/readings/books/"+itoa(bID)+"/progress", map[string]any{"lastPage": 3})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("report progress (no access): expected 403, got %d", resp.Code)
	}

	// Grant series access to the student.
	env.grantReadingAccess(t, studentID, sID, "series")

	// Now student should see the series in /readings.
	resp = env.doAsUser(t, studentID, http.MethodGet, "/api/v1/readings", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("readings (series access): %d", resp.Code)
	}
	json.Unmarshal(resp.Body.Bytes(), &view)
	series, _ = view["Series"].([]any)
	if len(series) != 1 {
		t.Fatalf("readings (series access): Series should have 1, got %d", len(series))
	}

	// Student can now access the series detail.
	resp = env.doAsUser(t, studentID, http.MethodGet, "/api/v1/readings/series/"+itoa(sID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("series detail (series access): expected 200, got %d", resp.Code)
	}

	// Student can now report progress on the book (via series inheritance).
	resp = env.doAsUser(t, studentID, http.MethodPost, "/api/v1/readings/books/"+itoa(bID)+"/progress", map[string]any{"lastPage": 5})
	if resp.Code != http.StatusOK {
		t.Fatalf("report progress (series access): expected 200, got %d %s", resp.Code, resp.Body.String())
	}

	// Progress can be read back.
	resp = env.doAsUser(t, studentID, http.MethodGet, "/api/v1/readings/books/"+itoa(bID)+"/progress", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("get progress: expected 200, got %d", resp.Code)
	}
	var prog map[string]any
	json.Unmarshal(resp.Body.Bytes(), &prog)
	if int(prog["lastPage"].(float64)) != 5 {
		t.Fatalf("get progress: lastPage=%v, want 5", prog["lastPage"])
	}
}

// TestReadingRoomAccessControlAdminBypass verifies that admin/parent roles
// bypass the access check entirely.
func TestReadingRoomAccessControlAdminBypass(t *testing.T) {
	env := newTestEnv(t)

	sID := env.createReadingSeries(t, "Series", "chinese")
	bID := env.createReadingBook(t, "Book", "/b.pdf", "chinese", sID)

	// Admin (via doAsUser with admin role) — no grant needed.
	// createUser returns a user; we create one with "admin" role.
	adminID := env.createUser(t, "admin1", "admin")

	// Admin can get series detail without any grant.
	resp := env.doAsUser(t, adminID, http.MethodGet, "/api/v1/readings/series/"+itoa(sID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin series detail: expected 200, got %d", resp.Code)
	}

	// Admin can report progress without any grant.
	resp = env.doAsUser(t, adminID, http.MethodPost, "/api/v1/readings/books/"+itoa(bID)+"/progress", map[string]any{"lastPage": 1})
	if resp.Code != http.StatusOK {
		t.Fatalf("admin report progress: expected 200, got %d", resp.Code)
	}
}

// TestReadingRoomBulkAccess verifies the grant_all / revoke_all bulk operations.
func TestReadingRoomBulkAccess(t *testing.T) {
	env := newTestEnv(t)

	env.createReadingSeries(t, "S1", "chinese")
	env.createReadingSeries(t, "S2", "chinese")
	env.createReadingBook(t, "B1", "/b1.pdf", "chinese", 0)
	env.createReadingArticle(t, "A1", "https://x.com", "chinese", 0)

	studentID := env.createUser(t, "student1", "student")

	// Grant all.
	resp := env.do(t, http.MethodPost, "/admin/api/users/"+itoa(studentID)+"/reading-access/bulk", map[string]any{"action": "grant_all"})
	if resp.Code != http.StatusOK {
		t.Fatalf("bulk grant_all: %d %s", resp.Code, resp.Body.String())
	}

	// Student should now see everything.
	resp = env.doAsUser(t, studentID, http.MethodGet, "/api/v1/readings", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("readings after grant_all: %d", resp.Code)
	}
	var view map[string]any
	json.Unmarshal(resp.Body.Bytes(), &view)
	series, _ := view["Series"].([]any)
	books, _ := view["Books"].([]any)
	articles, _ := view["Articles"].([]any)
	if len(series) != 2 || len(books) != 1 || len(articles) != 1 {
		t.Fatalf("after grant_all: series=%d books=%d articles=%d, want 2/1/1", len(series), len(books), len(articles))
	}

	// Revoke all.
	resp = env.do(t, http.MethodPost, "/admin/api/users/"+itoa(studentID)+"/reading-access/bulk", map[string]any{"action": "revoke_all"})
	if resp.Code != http.StatusOK {
		t.Fatalf("bulk revoke_all: %d", resp.Code)
	}

	// Student should see nothing.
	resp = env.doAsUser(t, studentID, http.MethodGet, "/api/v1/readings", nil)
	json.Unmarshal(resp.Body.Bytes(), &view)
	series, _ = view["Series"].([]any)
	books, _ = view["Books"].([]any)
	if len(series) != 0 || len(books) != 0 {
		t.Fatalf("after revoke_all: series=%d books=%d, want 0/0", len(series), len(books))
	}
}
