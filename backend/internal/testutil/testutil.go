// Package testutil provides shared test helpers (DB setup, seed fixtures) so
// the 11+ duplicated setupTestDB / seedTestSubjects functions across the
// repository and service packages can collapse into one place.
//
// Usage:
//
//	db := testutil.NewDB(t)
//	subjects := testutil.SeedSubjects(t, db)
//
// NewDB opens a fresh in-memory SQLite DB and runs model.AutoMigrate. Tests
// that exercise cross-connection transactions (the cmd/server integration tests
// through a real engine) use their own file:?mode=memory DSN in testhelper; this
// helper is for unit-style service/repository tests that don't route through a
// pooled transaction.
package testutil

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// NewDB returns a freshly migrated in-memory SQLite DB for a single test.
// Fails the test (via t.Fatal) on open or migrate error.
//
// In-memory DBs are private PER CONNECTION in SQLite: when GORM hands a burst
// of concurrent writers to a 2nd pooled connection, that connection sees no
// tables ("no such table"). For tests that fan out concurrent writers (the
// atomic-upsert race tests), use NewFileDB instead.
func NewDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("testutil: open in-memory sqlite: %v", err)
	}
	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("testutil: auto-migrate: %v", err)
	}
	return db
}

// NewFileDB returns a freshly migrated FILE-backed SQLite DB in a temp dir.
// The temp dir is managed by the testing framework and cleaned up after the
// test. Use this (not NewDB) for tests that need concurrent writers across the
// GORM connection pool — :memory: gives each connection a private empty DB,
// so a 2nd pooled connection sees no tables.
func NewFileDB(t *testing.T) *gorm.DB {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("testutil: open file-backed sqlite: %v", err)
	}
	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("testutil: auto-migrate: %v", err)
	}
	return db
}

// SeedSubjects inserts the canonical subject set and returns a key→Subject map
// so test fixtures can reference a real SubjectID when building Courses. The
// labels/emojis here are test conveniences, not the seeded defaults the app
// ships with (those live in service.SeedDefaultSubjects).
func SeedSubjects(t *testing.T, db *gorm.DB) map[string]model.Subject {
	t.Helper()
	defaults := []model.Subject{
		{Key: "chinese", Label: "语文", SortOrder: 1},
		{Key: "math", Label: "数学", SortOrder: 2},
		{Key: "english", Label: "英语", SortOrder: 3},
		{Key: "physics", Label: "物理/科学", SortOrder: 4},
	}
	out := make(map[string]model.Subject, len(defaults))
	for i := range defaults {
		if err := db.Create(&defaults[i]).Error; err != nil {
			t.Fatalf("testutil: seed subject %q: %v", defaults[i].Key, err)
		}
		out[defaults[i].Key] = defaults[i]
	}
	return out
}
