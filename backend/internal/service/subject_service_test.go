package service

import (
	"studyquest/backend/internal/testutil"
	"errors"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSubjectTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// SQLite disables FK enforcement by default; turn it on so the
	// courses.subject_id ON DELETE RESTRICT constraint is actually exercised.
	db.Exec("PRAGMA foreign_keys=ON")
	return db
}

// newSubjectSvc builds a fully wired subjectService (with badgeService) on a
// fresh in-memory DB, so subject auto-badge generation is exercised.
func newSubjectSvc(t *testing.T) (*gorm.DB, SubjectService) {
	t.Helper()
	db := newSubjectTestDB(t)
	subjectRepo := repository.NewSubjectRepository(db)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	bs := NewBadgeService(db, badgeRepo, progressRepo)
	return db, NewSubjectService(db, subjectRepo, badgeRepo, bs)
}

func TestSeedDefaultSubjects(t *testing.T) {
	_, svc := newSubjectSvc(t)

	if err := svc.SeedDefaultSubjects(); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	list, _ := svc.List()
	if len(list) != 11 {
		t.Fatalf("expected 11 default subjects (10 academic + entertainment), got %d", len(list))
	}

	// Idempotent: seeding again must not duplicate.
	if err := svc.SeedDefaultSubjects(); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	list2, _ := svc.List()
	if len(list2) != 11 {
		t.Fatalf("seed not idempotent: got %d after second seed", len(list2))
	}

	// math + english keys must exist (badge rule targets depend on them).
	keys := map[string]bool{}
	for _, s := range list2 {
		keys[s.Key] = true
	}
	if !keys["math"] || !keys["english"] {
		t.Errorf("default seed missing math/english keys: %v", keys)
	}
}

// TestSeedDefaultSubjectsBackfillsExistingInstall locks in the incremental
// backfill: an install that already has SOME default subjects (e.g. an old
// install from before the junior-high subjects were added) must pick up the
// newly-added defaults on the next boot, without duplicating the ones it has
// and without touching user-created subjects.
func TestSeedDefaultSubjectsBackfillsExistingInstall(t *testing.T) {
	db, svc := newSubjectSvc(t)
	subjectRepo := repository.NewSubjectRepository(db)

	// Simulate an OLD install: it has the original 5 subjects but not the
	// 5 junior-high subjects added later. Plus one user-created subject.
	oldDefaults := []model.Subject{
		{Key: "chinese", Label: "语文", SortOrder: 1, IsSystem: true},
		{Key: "math", Label: "数学", SortOrder: 2, IsSystem: true},
		{Key: "english", Label: "英语", SortOrder: 3, IsSystem: true},
		{Key: "physics", Label: "物理", SortOrder: 4, IsSystem: true},
		{Key: "extra", Label: "课外百科", SortOrder: 5, IsSystem: true},
	}
	for i := range oldDefaults {
		if err := subjectRepo.Create(&oldDefaults[i]); err != nil {
			t.Fatalf("seed old subject %s: %v", oldDefaults[i].Key, err)
		}
	}
	// A user-created subject that must NOT be touched by the backfill.
	mine := model.Subject{Key: "my_subj", Label: "我的", SortOrder: 99, IsSystem: false}
	if err := subjectRepo.Create(&mine); err != nil {
		t.Fatalf("seed user subject: %v", err)
	}

	// "Reboot" — run the seeder. It must ADD the 5 missing defaults (history,
	// geography, biology, chemistry, politics) without duplicating the 5 present
	// ones or clobbering the user subject.
	if err := svc.SeedDefaultSubjects(); err != nil {
		t.Fatalf("backfill seed: %v", err)
	}

	list, _ := svc.List()
	// 5 old + 6 new defaults (5 academic + entertainment) + 1 user = 12.
	if len(list) != 12 {
		t.Fatalf("after backfill: expected 12 subjects, got %d", len(list))
	}

	// The newly-added defaults must now exist.
	have := map[string]bool{}
	for _, s := range list {
		have[s.Key] = true
	}
	for _, k := range []string{"history", "geography", "biology", "chemistry", "politics"} {
		if !have[k] {
			t.Errorf("backfill missing new default subject %q", k)
		}
	}
}

func TestSubjectServiceDeleteInUse(t *testing.T) {
	db, svc := newSubjectSvc(t)
	subjects := testutil.SeedSubjects(t, db)
	courseRepo := repository.NewCourseRepository(db)

	// A course referencing the subject should block deletion.
	course := &model.Course{Title: "Math", SubjectID: subjects["math"].ID}
	if err := courseRepo.Create(course); err != nil {
		t.Fatalf("create course: %v", err)
	}
	db.Create(&model.CourseGrade{CourseID: course.ID, Grade: model.Grade("3")})

	err := svc.Delete(subjects["math"].ID)
	if !errors.Is(err, ErrSubjectInUse) {
		t.Fatalf("expected ErrSubjectInUse, got %v", err)
	}

	// Removing the course should allow deletion.
	if err := courseRepo.Delete(course.ID); err != nil {
		t.Fatalf("delete course: %v", err)
	}
	if err := svc.Delete(subjects["math"].ID); err != nil {
		t.Fatalf("delete subject after course removed: %v", err)
	}
}

func TestSubjectServiceRenameKeyCascadesBadge(t *testing.T) {
	db, svc := newSubjectSvc(t)
	subjects := testutil.SeedSubjects(t, db)
	badgeRepo := repository.NewBadgeRepository(db)

	// Badge whose rule_target matches the subject's current key.
	badge := &model.Badge{
		Code: "math_pro", Title: "数学高手", IconName: "badge_math",
		RuleType: "subject_count", RuleTarget: "math", Threshold: 3,
	}
	if err := badgeRepo.Create(badge); err != nil {
		t.Fatalf("create badge: %v", err)
	}

	// Rename the subject key.
	subj := subjects["math"]
	subj.Key = "mathematics"
	if err := svc.Update(&subj, "math"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Badge rule_target should have been rewritten.
	got, _ := badgeRepo.FindByCode("math_pro")
	if got.RuleTarget != "mathematics" {
		t.Errorf("badge rule_target = %s, want mathematics", got.RuleTarget)
	}
}
