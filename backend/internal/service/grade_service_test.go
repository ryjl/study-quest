package service

import (
	"testing"

	"gorm.io/gorm"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// TestGradeService_ListAll_IncludesPresetsAndCustoms verifies ListAll returns presets (even when unused) + any custom
// tags from the DB, with correct counts across the course_grades table.
func TestGradeService_ListAll_IncludesPresetsAndCustoms(t *testing.T) {
	db := testutil.NewFileDB(t)
	svc := NewGradeService(repository.NewGradeRepository(db))

	// Seed: course 1 with primary+college, course 2 with a custom "考研" tag.
	seedCourseGrades(t, db, 1, []string{"primary", "college"})
	seedCourseGrades(t, db, 2, []string{"考研"})

	all, err := svc.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	m := map[string]repository.GradeUsage{}
	for _, g := range all {
		m[g.Grade] = g
	}
	// Presets present (even unused ones like "other", "universal", "junior", "senior").
	for _, p := range model.PresetGrades {
		if _, ok := m[string(p)]; !ok {
			t.Errorf("preset %q missing from ListAll", p)
		}
	}
	// Custom tag present with count 1.
	if g, ok := m["考研"]; !ok {
		t.Errorf("custom tag 考研 missing from ListAll")
	} else if g.Count != 1 {
		t.Errorf("考研 count = %d, want 1", g.Count)
	}
	// Preset counts.
	if g, ok := m["college"]; !ok || g.Count != 1 {
		t.Errorf("college: ok=%v count=%d, want count 1", ok, g.Count)
	}
	if g, ok := m["primary"]; !ok || g.Count != 1 {
		t.Errorf("primary: ok=%v count=%d, want count 1", ok, g.Count)
	}
}

// TestGradeService_Rename_CustomTagMovesRows verifies rename cascades across tables and refuses presets.
func TestGradeService_Rename_CustomTagMovesRows(t *testing.T) {
	db := testutil.NewFileDB(t)
	svc := NewGradeService(repository.NewGradeRepository(db))

	seedCourseGrades(t, db, 1, []string{"考研"})
	seedCourseGrades(t, db, 2, []string{"考研"})

	if err := svc.Rename("考研", "研究生"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	all, _ := svc.ListAll()
	m := map[string]int64{}
	for _, g := range all {
		m[g.Grade] = g.Count
	}
	if m["研究生"] != 2 {
		t.Errorf("after rename, 研究生 count = %d, want 2", m["研究生"])
	}
	if m["考研"] != 0 {
		t.Errorf("after rename, 考研 should be gone, got count %d", m["考研"])
	}
}

// TestGradeService_Rename_RefusesPreset verifies preset protection.
func TestGradeService_Rename_RefusesPreset(t *testing.T) {
	db := testutil.NewFileDB(t)
	svc := NewGradeService(repository.NewGradeRepository(db))
	err := svc.Rename("primary", "something")
	if err != ErrGradeIsPreset {
		t.Errorf("rename preset: err = %v, want ErrGradeIsPreset", err)
	}
}

// TestGradeService_Merge_PresetIntoCustom verifies merging a preset's rows into a custom tag works
// (the historical-adult→college migration path).
func TestGradeService_Merge_PresetIntoCustom(t *testing.T) {
	db := testutil.NewFileDB(t)
	svc := NewGradeService(repository.NewGradeRepository(db))

	// Seed "adilt" rows (simulating historical DB data from the old preset).
	seedCourseGrades(t, db, 1, []string{"adult"})
	seedCourseGrades(t, db, 2, []string{"adult", "college"})

	// Merge adult→college. "adult" isn't a preset anymore (deleted 2026-07-21),
	// but it IS in the DB, so this should succeed.
	if err := svc.Merge("adult", "college"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	all, _ := svc.ListAll()
	m := map[string]int64{}
	for _, g := range all {
		m[g.Grade] = g.Count
	}
	if m["college"] != 2 {
		t.Errorf("after merge, college count = %d, want 2", m["college"])
	}
	if m["adult"] != 0 {
		t.Errorf("after merge, adilt should be gone, got count %d", m["adult"])
	}
}

// TestGradeService_Delete_RefusesInUse verifies delete is blocked when the tag has rows.
func TestGradeService_Delete_RefusesInUse(t *testing.T) {
	db := testutil.NewFileDB(t)
	svc := NewGradeService(repository.NewGradeRepository(db))
	seedCourseGrades(t, db, 1, []string{"考研"})

	err := svc.Delete("考研")
	if err != ErrGradeInUse {
		t.Errorf("delete in-use tag: err = %v, want ErrGradeInUse", err)
	}
}

// TestGradeService_Delete_RefusesPreset verifies delete is blocked for presets even when Count=0.
func TestGradeService_Delete_RefusesPreset(t *testing.T) {
	db := testutil.NewFileDB(t)
	svc := NewGradeService(repository.NewGradeRepository(db))
	// "other" is a preset with 0 uses in the empty db.
	err := svc.Delete("other")
	if err != ErrGradeIsPreset {
		t.Errorf("delete preset: err = %v, want ErrGradeIsPreset", err)
	}
}

// seedCourseGrades attaches a grade set to a course row. Creates the course if it doesn't exist
// (the grade tables have an FK to courses, so we need a real course row first).
func seedCourseGrades(t *testing.T, db *gorm.DB, courseID uint, grades []string) {
	t.Helper()
	// Ensure the course row exists (idempotent — Save with the struct so AutoMigrate has created courses table).
	course := &model.Course{ID: courseID, Title: "grade-test-course"}
	db.Save(course)
	for _, g := range grades {
		if err := db.Exec("INSERT OR IGNORE INTO course_grades (course_id, grade) VALUES (?, ?)", courseID, g).Error; err != nil {
			t.Fatalf("seed grade %s for course %d: %v", g, courseID, err)
		}
	}
}
