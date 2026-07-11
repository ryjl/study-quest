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

func TestSeedDefaultSubjects(t *testing.T) {
	db := newSubjectTestDB(t)
	subjectRepo := repository.NewSubjectRepository(db)
	badgeRepo := repository.NewBadgeRepository(db)
	svc := NewSubjectService(subjectRepo, badgeRepo)

	if err := svc.SeedDefaultSubjects(); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	list, _ := svc.List()
	if len(list) != 5 {
		t.Fatalf("expected 5 default subjects, got %d", len(list))
	}

	// Idempotent: seeding again must not duplicate.
	if err := svc.SeedDefaultSubjects(); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	list2, _ := svc.List()
	if len(list2) != 5 {
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

func TestSubjectServiceDeleteInUse(t *testing.T) {
	db := newSubjectTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	subjectRepo := repository.NewSubjectRepository(db)
	badgeRepo := repository.NewBadgeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	svc := NewSubjectService(subjectRepo, badgeRepo)

	// A course referencing the subject should block deletion.
	course := &model.Course{Title: "Math", Grade: "3", SubjectID: subjects["math"].ID}
	if err := courseRepo.Create(course); err != nil {
		t.Fatalf("create course: %v", err)
	}

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
	db := newSubjectTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	subjectRepo := repository.NewSubjectRepository(db)
	badgeRepo := repository.NewBadgeRepository(db)
	svc := NewSubjectService(subjectRepo, badgeRepo)

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
