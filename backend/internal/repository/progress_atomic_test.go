package repository

import (
	"path/filepath"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// These tests cover UpsertAndAccumulateWatch — the atomic INSERT ... ON
// CONFLICT DO UPDATE that replaced the racy GetProgress → mutate → Save
// sequence. The old path lost watch_seconds whenever two reports interleaved
// (the player's 5s timer vs the quiz ping), which is why the admin "learning
// time" column could sit at 0 after minutes of watching.

// setupProgressTestDB builds a file-backed temp SQLite DB. We deliberately do
// NOT use :memory: here (unlike setupTestDB): each :memory: connection gets its
// OWN private database, so when GORM opens a 2nd connection to serve a burst of
// concurrent writers, that connection sees no tables ("no such table"). A temp
// file with the default pool keeps all writers on one shared schema. Cleanup is
// automatic via t.TempDir().
func setupProgressTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "progress_test.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestUpsertAndAccumulateWatchConcurrent is the regression test for the lost-
// delta bug. We fire many concurrent upserts of 10s each against one
// (user, episode); the atomic ON CONFLICT path must land the full sum, with
// no lost update. (SQLite serializes writer statements under a single DB
// mutex — that per-statement atomicity is exactly what makes the increment
// race-free. The point is to prove the NEW path can't drop a delta, not to
// stress SQLite's scheduler.)
func TestUpsertAndAccumulateWatchConcurrent(t *testing.T) {
	db := setupProgressTestDB(t)
	repo := NewProgressRepository(db)

	const uid, eid uint = 1, 2
	const goroutines = 50
	const deltaEach = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(pos int) {
			defer wg.Done()
			// position is bookkeeping; the delta is what we assert on.
			_, err := repo.UpsertAndAccumulateWatch(uid, eid, pos*deltaEach, deltaEach)
			if err != nil {
				t.Errorf("UpsertAndAccumulateWatch: %v", err)
			}
		}(i)
	}
	wg.Wait()

	prog, err := repo.GetProgress(uid, eid)
	if err != nil || prog == nil {
		t.Fatalf("GetProgress after concurrent upserts: %v %v", prog, err)
	}
	want := goroutines * deltaEach
	if prog.WatchSeconds != want {
		t.Fatalf("concurrent accumulation lost deltas: watch_seconds=%d, want %d", prog.WatchSeconds, want)
	}
}

// TestUpsertAndAccumulateWatchClamp verifies the clamps: an absurd positive
// delta is capped at 600s (so a buggy/abusive client can't inflate time), and
// a negative delta is treated as 0 (never decrements the running total).
func TestUpsertAndAccumulateWatchClamp(t *testing.T) {
	db := setupProgressTestDB(t)
	repo := NewProgressRepository(db)

	// Absurd positive delta → clamped to 600.
	prog, err := repo.UpsertAndAccumulateWatch(1, 3, 5, 100000)
	if err != nil {
		t.Fatalf("upsert absurd delta: %v", err)
	}
	if prog.WatchSeconds != 600 {
		t.Fatalf("expected absurd delta clamped to 600, got %d", prog.WatchSeconds)
	}

	// Negative delta → treated as 0 (must not change the existing 600).
	prog, err = repo.UpsertAndAccumulateWatch(1, 3, 5, -50)
	if err != nil {
		t.Fatalf("upsert negative delta: %v", err)
	}
	if prog.WatchSeconds != 600 {
		t.Fatalf("negative delta must not change watch_seconds; got %d, want 600", prog.WatchSeconds)
	}
}

// TestUpsertAndAccumulateWatchInsertsAndAccumulates covers the basic path:
// first call inserts, subsequent calls add to the running total, and
// last_position_seconds tracks the most recent report. Completion is left
// untouched (the service layer owns that gate).
func TestUpsertAndAccumulateWatchInsertsAndAccumulates(t *testing.T) {
	db := setupProgressTestDB(t)
	repo := NewProgressRepository(db)

	prog, err := repo.UpsertAndAccumulateWatch(7, 8, 100, 30)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if prog.WatchSeconds != 30 || prog.LastPositionSeconds != 100 {
		t.Fatalf("first upsert: watch=%d pos=%d, want 30/100", prog.WatchSeconds, prog.LastPositionSeconds)
	}
	if prog.IsCompleted != 0 {
		t.Fatal("first upsert must not mark complete (completion gating is the service's job)")
	}

	prog, err = repo.UpsertAndAccumulateWatch(7, 8, 200, 45)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if prog.WatchSeconds != 75 {
		t.Fatalf("second upsert: watch=%d, want 75 (30+45)", prog.WatchSeconds)
	}
	if prog.LastPositionSeconds != 200 {
		t.Fatalf("second upsert: pos=%d, want 200", prog.LastPositionSeconds)
	}
}
