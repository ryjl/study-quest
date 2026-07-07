package repository

import (
	"studyquest/backend/internal/model"
	"testing"
)

func TestSubjectCRUD(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSubjectRepository(db)

	// Create
	s := &model.Subject{Key: "history", Label: "历史", Emoji: "📜", Color: "#fbbf24", SortOrder: 6}
	if err := repo.Create(s); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if s.ID == 0 {
		t.Fatal("expected ID to be set after Create")
	}

	// FindByKey
	got, err := repo.FindByKey("history")
	if err != nil || got == nil {
		t.Fatalf("FindByKey: %v %v", got, err)
	}
	if got.Label != "历史" {
		t.Errorf("Label = %s, want 历史", got.Label)
	}

	// FindByID
	got2, err := repo.FindByID(s.ID)
	if err != nil || got2 == nil {
		t.Fatalf("FindByID: %v %v", got2, err)
	}

	// Update
	got.Label = "历史学"
	got.Color = "#aabbcc"
	if err := repo.Update(got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	again, _ := repo.FindByKey("history")
	if again.Label != "历史学" || again.Color != "#aabbcc" {
		t.Errorf("after Update: Label=%s Color=%s", again.Label, again.Color)
	}

	// List ordering: seeded subjects + history, sorted by sort_order then id.
	list, err := repo.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("List returned empty")
	}
}

func TestSubjectDeleteBlockedByCourseFK(t *testing.T) {
	db := setupTestDB(t)
	db.Exec("PRAGMA foreign_keys=ON") // RESTRICT is only enforced with FKs on
	subjects := seedTestSubjects(t, db)
	courseRepo := NewCourseRepository(db)
	subjectRepo := NewSubjectRepository(db)

	// Create a course pointing at the "math" subject.
	c := &model.Course{Title: "Math Course", Grade: "3", SubjectID: subjects["math"].ID}
	if err := courseRepo.Create(c); err != nil {
		t.Fatalf("create course: %v", err)
	}

	// Deleting the subject must fail because of the FK RESTRICT constraint.
	err := subjectRepo.Delete(subjects["math"].ID)
	if err == nil {
		t.Fatal("expected FK constraint error when deleting subject referenced by a course, got nil")
	}

	// After deleting the course, the subject can be deleted.
	if err := courseRepo.Delete(c.ID); err != nil {
		t.Fatalf("delete course: %v", err)
	}
	if err := subjectRepo.Delete(subjects["math"].ID); err != nil {
		t.Fatalf("delete subject after course removed: %v", err)
	}
}

func TestSubjectUpdateBadgesRuleTarget(t *testing.T) {
	db := setupTestDB(t)
	seedTestSubjects(t, db)
	subjectRepo := NewSubjectRepository(db)
	badgeRepo := NewBadgeRepository(db)

	// Create a subject_count badge targeting "math".
	badge := &model.Badge{
		Code: "math_pro", Title: "数学高手", IconName: "badge_math",
		RuleType: "subject_count", RuleTarget: "math", Threshold: 3,
	}
	if err := badgeRepo.Create(badge); err != nil {
		t.Fatalf("create badge: %v", err)
	}

	// Rename the subject key from math → mathematics via the repo helper.
	if err := subjectRepo.UpdateBadgesRuleTarget("math", "mathematics"); err != nil {
		t.Fatalf("UpdateBadgesRuleTarget: %v", err)
	}

	got, _ := badgeRepo.FindByCode("math_pro")
	if got.RuleTarget != "mathematics" {
		t.Errorf("badge rule_target = %s, want mathematics", got.RuleTarget)
	}
}
