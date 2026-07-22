package repository

import (
	"testing"
	"time"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"
)

// These tests guard the "storage timestamps are always UTC" invariant
// (CLAUDE.md rule #3). The concrete business-logic regression (a local-time
// cutoff vs a UTC claimed_at making the reaper kill fresh jobs) is already
// covered by reaper_timezone_test.go; these tests guard the storage layer
// itself — that the UTC convention round-trips and that same-row timestamps
// written by different mechanisms agree.

// withShanghaiLocal temporarily sets time.Local to Asia/Shanghai and restores
// it on cleanup. A local-zone write only looks wrong when the process zone
// differs from UTC; on a UTC host it's correct by accident, so tests force a
// non-UTC zone to stay machine-independent.
func withShanghaiLocal(t *testing.T) {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
}

// TestAutoManagedTimestampsRoundTripUTC asserts the full write→read round trip
// for a GORM auto-managed CreatedAt lands within the UTC "now" window and reads
// back tagged UTC. Guards the NowFunc + _loc=UTC configuration in
// testutil.GormConfig / cmd/server main.go: if someone drops _loc=UTC from the
// DSN, CreatedAt reads back tagged with the process zone instead of UTC and the
// Location assertion fails.
func TestAutoManagedTimestampsRoundTripUTC(t *testing.T) {
	withShanghaiLocal(t)
	db := testutil.NewFileDB(t)

	before := time.Now().UTC()
	job := &model.SubtitleJob{EpisodeID: 1, Status: model.SubtitleJobQueued}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
	after := time.Now().UTC()

	var got model.SubtitleJob
	if err := db.First(&got, job.ID).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	// CreatedAt must sit inside the UTC window [before, after].
	if got.CreatedAt.Before(before) || got.CreatedAt.After(after) {
		t.Errorf("auto-managed CreatedAt = %v (loc=%v), must be within UTC window [%v, %v]",
			got.CreatedAt, got.CreatedAt.Location(), before, after)
	}
	// And it must read back tagged UTC — this is what _loc=UTC guarantees. A
	// DSN that drops _loc=UTC leaves the value tagged with the process zone.
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("auto-managed CreatedAt location = %v, want UTC (DSN missing _loc=UTC?)",
			got.CreatedAt.Location())
	}
}

// TestSameRowTimestampsAgree asserts that on one subtitle_jobs row the
// CURRENT_TIMESTAMP-written claimed_at (bare UTC text via raw SQL) and the
// Go-written completed_at (time.Now().UTC() via MarkDone) describe the same
// instant to the second. This is the shape of the historical bug: the two
// values came from different timezone sources and disagreed by the host's UTC
// offset, which silently broke duration math. The fix unified them on UTC;
// this test locks that agreement in place.
//
// Runs against the production-configured test DB (testutil.NewFileDB applies
// _loc=UTC + UTC NowFunc), so it confirms the configured system is consistent
// end-to-end. The companion business-level regression — that the reaper's
// Go-side cutoff agrees with a UTC claimed_at — lives in reaper_timezone_test.go.
func TestSameRowTimestampsAgree(t *testing.T) {
	withShanghaiLocal(t)
	db := testutil.NewFileDB(t)
	repo := NewSubtitleJobRepository(db)

	job := &model.SubtitleJob{EpisodeID: 2, Status: model.SubtitleJobQueued}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
	// ClaimNext stamps claimed_at via CURRENT_TIMESTAMP (bare UTC text).
	if _, err := repo.ClaimNext("worker-A"); err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	// MarkDone stamps completed_at via the repo's Go time.Now().UTC().
	if err := repo.MarkDone(job.ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	var got model.SubtitleJob
	if err := db.First(&got, job.ID).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if got.ClaimedAt == nil || got.CompletedAt == nil {
		t.Fatalf("want claimed_at + completed_at populated, got %+v", got)
	}

	// claimed_at and completed_at are written milliseconds apart on the same
	// row — they MUST agree to the second. A timezone-source mismatch (the bug)
	// makes them diverge by the host's UTC offset (+8h here).
	if diff := got.CompletedAt.Sub(*got.ClaimedAt); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("completed_at (%v) and claimed_at (%v) diverge by %v — same-row UTC "+
			"timestamps must agree to the second; a multi-second gap indicates a "+
			"local-vs-UTC source mix (the historical reaper-bug shape)",
			got.CompletedAt, got.ClaimedAt, diff)
	}
	if got.CompletedAt.Before(*got.ClaimedAt) {
		t.Errorf("completed_at (%v) precedes claimed_at (%v) — impossible under UTC-everywhere",
			got.CompletedAt, got.ClaimedAt)
	}
}
