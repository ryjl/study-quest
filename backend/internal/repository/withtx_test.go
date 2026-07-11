package repository

import (
	"testing"

	"studyquest/backend/internal/model"
)

// TestWithTxIsolation locks in the WithTx contract used by the Phase 1
// transaction refactor: a WithTx-bound repo writes through the passed-in
// transaction, and a ROLLED-BACK transaction must not leave any write visible
// to the original (non-tx) repo. If WithTx accidentally shared mutable state
// with the original, or bound to the wrong session, these would fail.
//
// Covers the progress repo (the one with the most transactional surface area
// and the AddPoints internal transaction). The same 3-line WithTx pattern is
// copied across all repos, so this is representative.

// TestWithTxWriteGoesThroughTx: a write on a WithTx repo is visible only after
// the tx commits, and is visible to a fresh read on the original DB.
func TestWithTxWriteGoesThroughTx(t *testing.T) {
	db := setupProgressTestDB(t) // file-backed: tx must share the schema (see progress_atomic_test)
	repo := NewProgressRepository(db)

	// Seed a user-progress row to operate on.
	_, err := repo.UpsertAndAccumulateWatch(1, 10, 5, 10)
	if err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	tx := db.Begin()
	txRepo := repo.WithTx(tx)

	// Mark complete INSIDE the tx.
	if err := txRepo.MarkCompleted(1, 10); err != nil {
		t.Fatalf("MarkCompleted in tx: %v", err)
	}

	// BEFORE commit: the original repo should NOT see is_completed=1 yet (the
	// write is uncommitted). We read via a fresh query on db (not tx).
	pre, _ := repo.GetProgress(1, 10)
	if pre.IsCompleted != 0 {
		t.Errorf("before commit: is_completed = %d, want 0 (tx not committed)", pre.IsCompleted)
	}

	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}

	// AFTER commit: the original repo sees the committed write.
	post, _ := repo.GetProgress(1, 10)
	if post.IsCompleted != 1 {
		t.Errorf("after commit: is_completed = %d, want 1", post.IsCompleted)
	}
}

// TestWithTxRollbackLeavesNothing: a write on a WithTx repo that is rolled
// back must leave NO visible state on the original DB. This is the invariant
// that makes the import/progress transactions safe — a mid-way failure rolls
// back everything, no orphans.
func TestWithTxRollbackLeavesNothing(t *testing.T) {
	db := setupProgressTestDB(t)
	repo := NewProgressRepository(db)

	tx := db.Begin()
	txRepo := repo.WithTx(tx)

	// Write a brand-new progress row + mark complete, INSIDE the tx.
	if _, err := txRepo.UpsertAndAccumulateWatch(7, 70, 1, 1); err != nil {
		t.Fatalf("upsert in tx: %v", err)
	}
	if err := txRepo.MarkCompleted(7, 70); err != nil {
		t.Fatalf("MarkCompleted in tx: %v", err)
	}

	// Roll back — simulating a later step in the service transaction failing.
	if err := tx.Rollback().Error; err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// The original repo must see NO row for (7,70) — the rollback discarded it.
	got, err := repo.GetProgress(7, 70)
	if err != nil {
		t.Fatalf("GetProgress after rollback: %v", err)
	}
	if got != nil {
		t.Errorf("after rollback: row exists = %+v, want nil (tx rolled back)", got)
	}
}

// TestWithTxDoesNotShareState confirms the original repo still points at the
// base DB after a WithTx call (the WithTx must return a NEW struct, not mutate
// the receiver in place). If it mutated the receiver, subsequent non-tx calls
// would silently route through a stale/committed tx.
func TestWithTxDoesNotShareState(t *testing.T) {
	db := setupProgressTestDB(t)
	repo := NewProgressRepository(db)

	// Use WithTx, commit, then confirm the ORIGINAL repo still works against db.
	tx := db.Begin()
	txRepo := repo.WithTx(tx)
	if _, err := txRepo.UpsertAndAccumulateWatch(9, 90, 1, 1); err != nil {
		t.Fatalf("upsert via WithTx: %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Original repo must still be usable and see the committed row.
	got, _ := repo.GetProgress(9, 90)
	if got == nil {
		t.Error("original repo sees no row after a WithTx commit; WithTx may have mutated the receiver")
	}

	// And a second, independent write on the original repo works (proves it's
	// not bound to a closed tx handle).
	if _, err := repo.UpsertAndAccumulateWatch(11, 110, 1, 1); err != nil {
		t.Errorf("second write on original repo after WithTx: %v (receiver corrupted?)", err)
	}
}

// Compile-time guard: ensure model.UserProgress is referenced so this file
// doesn't get an unused-import error if the above tests evolve.
var _ = model.UserProgress{}
