package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"studyquest/backend/internal/model"
)

// Grades integration tests exercise the admin grade CRUD endpoints over HTTP:
//
//	GET    /admin/api/grades         — list preset + custom tags with counts
//	PUT    /admin/api/grades/:key    — rename a custom tag (body {new_key})
//	POST   /admin/api/grades/merge   — merge one tag into another (body {from, to})
//	DELETE /admin/api/grades/:key    — delete a 0-use custom tag
//
// The service-layer behavior (rename cascade, preset refusal, merge rules) is
// already covered by internal/service/grade_service_test.go; these tests lock
// the HTTP layer only — admin-cookie auth, route binding, DTO serialization,
// and the respondGradeError status-code mapping (409 vs 404 vs 400).

// gradeListRow mirrors one row of GET /admin/api/grades for assertion.
type gradeListRow struct {
	Grade    string `json:"grade"`
	Label    string `json:"label"`
	Count    int64  `json:"count"`
	IsPreset bool   `json:"is_preset"`
}

// listGrades GETs /admin/api/grades and returns the parsed rows, failing the
// test on a non-200 so each test's happy-path precondition is load-bearing.
func (e *testEnv) listGrades(t *testing.T) []gradeListRow {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/admin/api/grades", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/grades: %d %s", resp.Code, resp.Body.String())
	}
	var rows []gradeListRow
	if err := json.Unmarshal(resp.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode grades list: %v (body: %s)", err, resp.Body.String())
	}
	return rows
}

// findGrade returns the row for grade g, or nil if it isn't listed.
func findGrade(rows []gradeListRow, g string) *gradeListRow {
	for i := range rows {
		if rows[i].Grade == g {
			return &rows[i]
		}
	}
	return nil
}

// seedCourseGrade stamps a course with a grade tag via raw SQL, mirroring the
// four-table join structure GradeRepository.ListAll counts across. A real
// Course row (from createCourse) satisfies the FK; INSERT OR IGNORE keeps the
// composite-PK insert idempotent across re-runs.
func (e *testEnv) seedCourseGrade(t *testing.T, courseID uint, grade string) {
	t.Helper()
	if err := e.db.Exec("INSERT OR IGNORE INTO course_grades (course_id, grade) VALUES (?, ?)", courseID, grade).Error; err != nil {
		t.Fatalf("seed course_grades (%d, %q): %v", courseID, grade, err)
	}
}

// TestGrades_ListShowsPresetsAndCustoms GET /grades returns every preset (even
// unused ones — they're always-available picker options) plus any custom tags
// the DB has accumulated, each with a cross-table Count.
func TestGrades_ListShowsPresetsAndCustoms(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Grade Course", "math", nil)
	// One custom tag + one preset-in-use so both branches of ListAll are hit.
	env.seedCourseGrade(t, courseID, "考研")
	env.seedCourseGrade(t, courseID, "primary")

	rows := env.listGrades(t)

	// Every preset must appear (unused ones too — the picker shows them).
	for _, p := range model.PresetGrades {
		if findGrade(rows, string(p)) == nil {
			t.Errorf("preset %q missing from list", p)
		}
	}
	// The custom tag is present with Count >= 1.
	custom := findGrade(rows, "考研")
	if custom == nil {
		t.Fatal("custom tag 考研 missing from list")
	}
	if custom.Count < 1 {
		t.Errorf("考研 count = %d; want >= 1", custom.Count)
	}
	if custom.IsPreset {
		t.Error("考研 flagged as preset; it's a custom tag")
	}
	// primary has a real row → Count >= 1, and the preset label is localized.
	primary := findGrade(rows, "primary")
	if primary == nil {
		t.Fatal("preset primary missing from list")
	}
	if primary.Count < 1 {
		t.Errorf("primary count = %d; want >= 1", primary.Count)
	}
	if !primary.IsPreset {
		t.Error("primary not flagged as preset")
	}
	if primary.Label == "" || primary.Label == "primary" {
		t.Errorf("primary label = %q; want a localized preset label", primary.Label)
	}
}

// TestGrades_Rename_CustomTagMoves PUT /grades/:key moves a custom tag; a
// follow-up GET confirms the old key is gone and the new key carries the count.
func TestGrades_Rename_CustomTagMoves(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Rename Course", "math", nil)
	env.seedCourseGrade(t, courseID, "oldtag")

	resp := env.do(t, http.MethodPut, "/admin/api/grades/oldtag", map[string]any{"new_key": "newtag"})
	if resp.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", resp.Code, resp.Body.String())
	}

	rows := env.listGrades(t)
	if findGrade(rows, "oldtag") != nil {
		t.Error("oldtag still listed after rename; should have moved")
	}
	moved := findGrade(rows, "newtag")
	if moved == nil {
		t.Fatal("newtag missing from list after rename")
	}
	if moved.Count < 1 {
		t.Errorf("newtag count = %d; want >= 1 (carried from oldtag)", moved.Count)
	}
}

// TestGrades_Rename_RefusesPreset Rename rejects a preset source with 409
// (ErrGradeIsPreset) — presets are system-defined and can only be Merge'd away.
func TestGrades_Rename_RefusesPreset(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPut, "/admin/api/grades/primary", map[string]any{"new_key": "whatever"})
	if resp.Code != http.StatusConflict {
		t.Fatalf("rename preset: want 409, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestGrades_Merge_CustomIntoPreset POST /grades/merge moves a custom tag's
// rows onto a preset (the canonical "考研 → graduate" consolidation) and the
// source disappears from the list.
func TestGrades_Merge_CustomIntoPreset(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Merge Course", "math", nil)
	env.seedCourseGrade(t, courseID, "tomerge")

	resp := env.do(t, http.MethodPost, "/admin/api/grades/merge", map[string]any{
		"from": "tomerge",
		"to":   "college",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", resp.Code, resp.Body.String())
	}

	rows := env.listGrades(t)
	if findGrade(rows, "tomerge") != nil {
		t.Error("tomerge still listed after merge; its rows should have moved to college")
	}
	college := findGrade(rows, "college")
	if college == nil {
		t.Fatal("college missing from list after merge")
	}
	if college.Count < 1 {
		t.Errorf("college count = %d after merge; want >= 1 (absorbed tomerge's rows)", college.Count)
	}
}

// TestGrades_Merge_TargetMustExist merging into a non-existent non-preset tag
// is refused with 400 (the service returns a fmt.Errorf, not a sentinel — it
// falls through respondGradeError's default branch). This guards against
// orphaning rows at a tag the admin can't see.
func TestGrades_Merge_TargetMustExist(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Merge Target Course", "math", nil)
	env.seedCourseGrade(t, courseID, "sourcetag")

	resp := env.do(t, http.MethodPost, "/admin/api/grades/merge", map[string]any{
		"from": "sourcetag",
		"to":   "nonexistent-target",
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("merge into nonexistent target: want 400, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestGrades_Delete_RefusesInUse DELETE refuses a tag that still has rows with
// 409 (ErrGradeInUse) — the admin must merge first. Otherwise rows would be
// orphaned (Course still references a grade that no longer lists).
func TestGrades_Delete_RefusesInUse(t *testing.T) {
	env := newTestEnv(t)
	courseID := env.createCourse(t, "Delete Course", "math", nil)
	env.seedCourseGrade(t, courseID, "usedtag")

	resp := env.do(t, http.MethodDelete, "/admin/api/grades/usedtag", nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("delete in-use tag: want 409, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestGrades_Delete_RefusesPreset DELETE refuses a preset with 409 even when
// it has 0 rows — presets are permanent picker options.
func TestGrades_Delete_RefusesPreset(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodDelete, "/admin/api/grades/primary", nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("delete preset: want 409, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}
