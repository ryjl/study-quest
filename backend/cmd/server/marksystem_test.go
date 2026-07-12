package main

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// These tests cover markSystemDefaults — the one-shot backfill that marks the
// canonical seeded subjects/tags/badges as IsSystem=true on instances that were
// created BEFORE the IsSystem column existed (so their seed rows otherwise
// carry is_system=false and wouldn't be delete-protected).
//
// Contracts under test:
//  1. Known default keys (in the hardcoded lists) → flagged is_system=true.
//  2. User-created rows with custom keys → left at is_system=false (untouched).
//  3. Idempotent: running twice converges to the same state.

func setupMarkSystemDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestMarkSystemDefaultsBackfill simulates a pre-IsSystem install: seed rows
// exist with is_system=false (as old data would), plus user-created custom
// rows. After the backfill, defaults must be flagged and customs untouched.
func TestMarkSystemDefaultsBackfill(t *testing.T) {
	db := setupMarkSystemDB(t)

	// "Legacy" seeded rows — created WITHOUT the IsSystem flag (old install).
	legacySubjects := []model.Subject{
		{Key: "math", Label: "数学"},       // default → should be flagged
		{Key: "chinese", Label: "语文"},    // default → should be flagged
		{Key: "my_subject", Label: "我的学科"},    // user-created → must stay false
	}
	for i := range legacySubjects {
		if err := db.Create(&legacySubjects[i]).Error; err != nil {
			t.Fatalf("seed subject %s: %v", legacySubjects[i].Key, err)
		}
	}

	legacyTags := []model.Tag{
		{Key: "required", Label: "必修"},   // default → flagged
		{Key: "logic", Label: "逻辑"},      // default → flagged
		{Key: "my-tag", Label: "我的标签"}, // user-created → untouched
	}
	for i := range legacyTags {
		if err := db.Create(&legacyTags[i]).Error; err != nil {
			t.Fatalf("seed tag %s: %v", legacyTags[i].Key, err)
		}
	}

	legacyBadges := []model.Badge{
		{Code: "streak", Title: "连续学习", IconName: "x", RuleType: "consecutive_days"}, // default → flagged
		{Code: "explorer", Title: "博学多闻", IconName: "x", RuleType: "distinct_subject_count"}, // default → flagged
		{Code: "my_custom_badge", Title: "自建", IconName: "x", RuleType: "watch_duration", Threshold: 1}, // user-created → untouched
	}
	for i := range legacyBadges {
		if err := db.Create(&legacyBadges[i]).Error; err != nil {
			t.Fatalf("seed badge %s: %v", legacyBadges[i].Code, err)
		}
	}

	// Run the backfill.
	markSystemDefaults(db)

	assertSystem := func(t *testing.T, table, col, key string, want bool) {
		t.Helper()
		var got bool
		if err := db.Table(table).Where(col+" = ?", key).Pluck("is_system", &got).Error; err != nil {
			t.Fatalf("query %s.%s=%s: %v", table, col, key, err)
		}
		if got != want {
			t.Errorf("%s %s=%s: is_system=%v, want %v", table, col, key, got, want)
		}
	}

	// Defaults flagged true.
	for _, k := range []string{"math", "chinese"} {
		assertSystem(t, "subjects", "key", k, true)
	}
	for _, k := range []string{"required", "logic"} {
		assertSystem(t, "tags", "key", k, true)
	}
	for _, c := range []string{"streak", "explorer"} {
		assertSystem(t, "badges", "code", c, true)
	}

	// User-created rows untouched (still false).
	assertSystem(t, "subjects", "key", "my_subject", false)
	assertSystem(t, "tags", "key", "my-tag", false)
	assertSystem(t, "badges", "code", "my_custom_badge", false)
}

// TestMarkSystemDefaultsIdempotent verifies a second run converges to the same
// state (no row flips, no error). Catches a regression where the UPDATE would
// accidentally rewrite user rows.
func TestMarkSystemDefaultsIdempotent(t *testing.T) {
	db := setupMarkSystemDB(t)

	// One default + one custom, both legacy (is_system=false).
	db.Create(&model.Subject{Key: "math", Label: "数学"})
	db.Create(&model.Subject{Key: "my_subject", Label: "我的学科"})

	markSystemDefaults(db)
	markSystemDefaults(db) // second run

	var mathFlag, mySubjectFlag bool
	db.Table("subjects").Where("key = ?", "math").Pluck("is_system", &mathFlag)
	db.Table("subjects").Where("key = ?", "my_subject").Pluck("is_system", &mySubjectFlag)

	if !mathFlag {
		t.Error("idempotent run: math should remain is_system=true")
	}
	if mySubjectFlag {
		t.Error("idempotent run: my_subject (user-created) must remain is_system=false")
	}
}

// TestMarkSystemDefaultsEmptyDB verifies the backfill is a no-op (not an error)
// on a database with no matching rows — e.g. a fresh install where the seeder
// hasn't run yet. This guards against the UPDATE blowing up on an empty IN list.
func TestMarkSystemDefaultsEmptyDB(t *testing.T) {
	db := setupMarkSystemDB(t)
	// No rows at all. Must not panic or log a fatal.
	markSystemDefaults(db)

	var count int64
	db.Table("subjects").Where("is_system = 1").Count(&count)
	if count != 0 {
		t.Errorf("empty DB: expected 0 flagged subjects, got %d", count)
	}
}
