package repository

import (
	"studyquest/backend/internal/model"
	"testing"

	"gorm.io/gorm"
)

// seedCourseForUnlock inserts a course + a user + an access row, returning
// their ids. Reused across unlock repo tests so ResolveEffective has the
// GrantedAt anchor it needs.
func seedCourseForUnlock(t *testing.T, db *gorm.DB) (courseID, userID uint) {
	t.Helper()
	subj := model.Subject{Key: "math", Label: "数学"}
	if err := db.Create(&subj).Error; err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	c := model.Course{Title: "C", SubjectID: subj.ID}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("seed course: %v", err)
	}
	if err := db.Create(&model.CourseGrade{CourseID: c.ID, Grade: model.Grade("3")}).Error; err != nil {
		t.Fatalf("seed course grade: %v", err)
	}
	u := model.User{Nickname: "u", PinHash: "x"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	a := model.UserCourseAccess{UserID: u.ID, CourseID: c.ID}
	if err := db.Create(&a).Error; err != nil {
		t.Fatalf("seed access: %v", err)
	}
	return c.ID, u.ID
}

func TestUnlockTemplateCRUD(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUnlockRepository(db)
	courseID, _ := seedCourseForUnlock(t, db)

	// Initially absent → GetTemplate returns nil.
	got, err := repo.GetTemplate(courseID)
	if err != nil || got != nil {
		t.Fatalf("GetTemplate before set: got=%v err=%v", got, err)
	}

	// Upsert.
	if err := repo.UpsertTemplate(&model.CourseUnlockTemplate{
		CourseID: courseID, Strategy: model.StrategyWeekly,
		WeeklyTimesJSON: `[{"weekday":0,"hour":19,"minute":0}]`,
	}); err != nil {
		t.Fatalf("UpsertTemplate: %v", err)
	}
	got, err = repo.GetTemplate(courseID)
	if err != nil || got == nil || got.Strategy != model.StrategyWeekly {
		t.Fatalf("GetTemplate after set: got=%v err=%v", got, err)
	}

	// Re-upsert replaces.
	if err := repo.UpsertTemplate(&model.CourseUnlockTemplate{
		CourseID: courseID, Strategy: model.StrategyManual,
	}); err != nil {
		t.Fatalf("UpsertTemplate(2): %v", err)
	}
	got, _ = repo.GetTemplate(courseID)
	if got.Strategy != model.StrategyManual {
		t.Errorf("after re-upsert strategy=%s want manual", got.Strategy)
	}

	// Delete.
	if err := repo.DeleteTemplate(courseID); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
	got, _ = repo.GetTemplate(courseID)
	if got != nil {
		t.Errorf("GetTemplate after delete: got=%v want nil", got)
	}
}

func TestUnlockOverrideManualIncrement(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUnlockRepository(db)
	courseID, userID := seedCourseForUnlock(t, db)

	// No override yet.
	o, err := repo.GetOverride(userID, courseID)
	if err != nil || o != nil {
		t.Fatalf("GetOverride before: got=%v err=%v", o, err)
	}

	// Increment creates the row with count=1 (atomic INSERT ... ON CONFLICT,
	// no read-modify-write — the invariant that prevents lost counts).
	if err := repo.IncrementManualUnlock(userID, courseID); err != nil {
		t.Fatalf("IncrementManualUnlock(1): %v", err)
	}
	o, _ = repo.GetOverride(userID, courseID)
	if o == nil || o.ManualUnlockCount != 1 {
		t.Fatalf("after 1st increment: %+v", o)
	}

	// Two more increments → 3.
	repo.IncrementManualUnlock(userID, courseID)
	repo.IncrementManualUnlock(userID, courseID)
	o, _ = repo.GetOverride(userID, courseID)
	if o.ManualUnlockCount != 3 {
		t.Errorf("ManualUnlockCount=%d want 3", o.ManualUnlockCount)
	}
}

// TestUnlockOverrideManualDecrement covers the undo path: decrement reduces the
// count, never goes negative, and is a no-op when no override row exists.
func TestUnlockOverrideManualDecrement(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUnlockRepository(db)
	courseID, userID := seedCourseForUnlock(t, db)

	// No row yet → decrement is a silent no-op (no error, no row created).
	if err := repo.DecrementManualUnlock(userID, courseID); err != nil {
		t.Fatalf("DecrementManualUnlock with no row: %v", err)
	}
	o, _ := repo.GetOverride(userID, courseID)
	if o != nil {
		t.Errorf("decrement created a row unexpectedly: %+v", o)
	}

	// Increment to 3, then decrement twice → 1.
	repo.IncrementManualUnlock(userID, courseID)
	repo.IncrementManualUnlock(userID, courseID)
	repo.IncrementManualUnlock(userID, courseID)
	repo.DecrementManualUnlock(userID, courseID)
	repo.DecrementManualUnlock(userID, courseID)
	o, _ = repo.GetOverride(userID, courseID)
	if o.ManualUnlockCount != 1 {
		t.Errorf("after 3 up + 2 down: count=%d want 1", o.ManualUnlockCount)
	}

	// Over-decrement floors at 0 (MAX(...,0) in SQL), never negative.
	repo.DecrementManualUnlock(userID, courseID)
	repo.DecrementManualUnlock(userID, courseID)
	repo.DecrementManualUnlock(userID, courseID)
	o, _ = repo.GetOverride(userID, courseID)
	if o.ManualUnlockCount != 0 {
		t.Errorf("over-decrement: count=%d want 0 (floored)", o.ManualUnlockCount)
	}
}

func TestUnlockAllowlistReplace(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUnlockRepository(db)
	courseID, userID := seedCourseForUnlock(t, db)

	// Set replaces wholesale (set semantics, not append).
	if err := repo.SetAllowedEpisodes(userID, courseID, []uint{3, 7, 12}); err != nil {
		t.Fatalf("SetAllowedEpisodes: %v", err)
	}
	o, _ := repo.GetOverride(userID, courseID)
	if o == nil {
		t.Fatal("override row not created")
	}
	eff, err := repo.ResolveEffective(userID, courseID)
	if err != nil {
		t.Fatalf("ResolveEffective: %v", err)
	}
	if len(eff.AllowedEpisodeIDs) != 3 {
		t.Fatalf("allowlist len=%d want 3", len(eff.AllowedEpisodeIDs))
	}

	// Replace with fewer.
	if err := repo.SetAllowedEpisodes(userID, courseID, []uint{5}); err != nil {
		t.Fatalf("SetAllowedEpisodes(2): %v", err)
	}
	eff, _ = repo.ResolveEffective(userID, courseID)
	if len(eff.AllowedEpisodeIDs) != 1 || eff.AllowedEpisodeIDs[0] != 5 {
		t.Errorf("after replace: %+v", eff.AllowedEpisodeIDs)
	}
}

func TestResolveEffectiveInheritance(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUnlockRepository(db)
	courseID, userID := seedCourseForUnlock(t, db)

	// Neither template nor override → AllOpen.
	eff, err := repo.ResolveEffective(userID, courseID)
	if err != nil {
		t.Fatalf("ResolveEffective: %v", err)
	}
	if eff.Strategy != model.StrategyAllOpen {
		t.Errorf("default strategy=%s want all_open", eff.Strategy)
	}
	if eff.GrantedAt.IsZero() {
		t.Error("GrantedAt should come from access row, got zero")
	}

	// Template = weekly → inherited when no override.
	if err := repo.UpsertTemplate(&model.CourseUnlockTemplate{
		CourseID: courseID, Strategy: model.StrategyWeekly,
		WeeklyTimesJSON: `[{"weekday":0,"hour":19,"minute":0}]`,
	}); err != nil {
		t.Fatalf("UpsertTemplate: %v", err)
	}
	eff, _ = repo.ResolveEffective(userID, courseID)
	if eff.Strategy != model.StrategyWeekly {
		t.Errorf("inherited strategy=%s want weekly", eff.Strategy)
	}
	if len(eff.WeeklyTimes) != 1 || eff.WeeklyTimes[0].Hour != 19 {
		t.Errorf("inherited weekly times=%+v", eff.WeeklyTimes)
	}

	// Override = manual → wins over template.
	if err := repo.UpsertOverride(&model.UserUnlockOverride{
		UserID: userID, CourseID: courseID, Strategy: model.StrategyManual,
	}); err != nil {
		t.Fatalf("UpsertOverride: %v", err)
	}
	eff, _ = repo.ResolveEffective(userID, courseID)
	if eff.Strategy != model.StrategyManual {
		t.Errorf("override strategy=%s want manual", eff.Strategy)
	}
}
