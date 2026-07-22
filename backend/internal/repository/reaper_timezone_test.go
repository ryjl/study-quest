package repository

import (
	"testing"
	"time"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"
)

// TestReapStaleJobs_DoesNotReapFreshlyClaimed is the regression test for the timezone bug
// that caused polish jobs to be reaped within minutes of being claimed.
//
// The bug: ClaimNextQueuedJob writes claimed_at via SQLite CURRENT_TIMESTAMP (UTC), but
// ReapStaleJobs computed the cutoff via time.Now() (local). In a +08:00 deployment the
// cutoff was 8h ahead of the UTC claimed_at, so even a 1-second-old claim looked "30+
// minutes stale" and got reaped. Polish jobs (2-7 min) never finished — they'd get
// claimed, reaped at the next 5-min reaper tick, re-claimed, re-reaped, forever.
//
// This test seeds a job claimed 1 minute ago (UTC, matching CURRENT_TIMESTAMP semantics)
// and asserts ReapStaleJobs with a 30-min threshold does NOT reap it. Under the buggy
// code (time.Now() local), a +08:00 machine would reap it (1 min UTC looks like 8h01m
// old to a local cutoff). The test machine's own timezone doesn't matter here because
// we seed claimed_at in UTC and the fix computes the cutoff in UTC — both sides agree
// regardless of machine TZ. (Pre-fix, if the machine were UTC, the test would pass
// accidentally; on +08 it would fail. Post-fix it passes everywhere.)
func TestReapStaleJobs_DoesNotReapFreshlyClaimed(t *testing.T) {
	db := testutil.NewFileDB(t)
	repo := NewAIContentRepository(db)

	// Seed a processing job claimed 1 minute ago. Use UTC to match what
	// ClaimNextQueuedJob's CURRENT_TIMESTAMP would have written.
	claimedAgo := time.Now().UTC().Add(-1 * time.Minute)
	epID := uint(1)
	job := &model.AIJob{
		JobType: "polish", EpisodeID: &epID,
		Status: "processing", ClaimedAt: &claimedAgo,
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	// 30-min threshold: a 1-min-old claim must NOT be reaped.
	n, err := repo.ReapStaleJobs(30 * time.Minute)
	if err != nil {
		t.Fatalf("ReapStaleJobs: %v", err)
	}
	if n != 0 {
		t.Errorf("ReapStaleJobs reaped %d job(s) — a 1-min-old claim must survive a 30-min threshold (timezone bug?)", n)
	}
}

// TestReapStaleJobs_ReapsGenuinelyStale is the positive control: a job claimed 31
// minutes ago (UTC) MUST be reaped under a 30-min threshold. This ensures the fix
// didn't break the reaper's actual job (the prior bug made it over-aggressive, but
// we still need it to catch real zombies).
func TestReapStaleJobs_ReapsGenuinelyStale(t *testing.T) {
	db := testutil.NewFileDB(t)
	repo := NewAIContentRepository(db)

	claimedAgo := time.Now().UTC().Add(-31 * time.Minute)
	epID := uint(1)
	job := &model.AIJob{
		JobType: "polish", EpisodeID: &epID,
		Status: "processing", ClaimedAt: &claimedAgo,
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	n, err := repo.ReapStaleJobs(30 * time.Minute)
	if err != nil {
		t.Fatalf("ReapStaleJobs: %v", err)
	}
	if n != 1 {
		t.Errorf("ReapStaleJobs reaped %d job(s), want 1 (31-min-old claim is genuinely stale)", n)
	}
}

// TestSubtitleReapStale_DoesNotReapFreshlyClaimed is the same regression for the
// subtitle reaper, which had the identical bug. Same shape: 1-min-old claim, 30-min
// threshold, must survive.
func TestSubtitleReapStale_DoesNotReapFreshlyClaimed(t *testing.T) {
	db := testutil.NewFileDB(t)
	repo := NewSubtitleJobRepository(db)

	claimedAgo := time.Now().UTC().Add(-1 * time.Minute)
	job := &model.SubtitleJob{
		EpisodeID: 1, Status: model.SubtitleJobProcessing, ClaimedAt: &claimedAgo,
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	n, err := repo.ReapStale(30 * time.Minute)
	if err != nil {
		t.Fatalf("ReapStale: %v", err)
	}
	if n != 0 {
		t.Errorf("ReapStale reaped %d job(s) — a 1-min-old claim must survive (subtitle reaper timezone bug?)", n)
	}
}

// TestSubtitleReapStale_ReapsGenuinelyStale: positive control for the subtitle reaper.
func TestSubtitleReapStale_ReapsGenuinelyStale(t *testing.T) {
	db := testutil.NewFileDB(t)
	repo := NewSubtitleJobRepository(db)

	claimedAgo := time.Now().UTC().Add(-31 * time.Minute)
	job := &model.SubtitleJob{
		EpisodeID: 1, Status: model.SubtitleJobProcessing, ClaimedAt: &claimedAgo,
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	n, err := repo.ReapStale(30 * time.Minute)
	if err != nil {
		t.Fatalf("ReapStale: %v", err)
	}
	if n != 1 {
		t.Errorf("ReapStale reaped %d job(s), want 1 (31-min-old claim is genuinely stale)", n)
	}
}