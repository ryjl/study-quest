package repository

import (
	"sync"
	"testing"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"
)

// ai_job_status_test.go — regression tests for UpdateJobStatus's progress
// handling. Two bugs fixed in the same change:
//
//  1. Concurrent processing-progress writes could regress (3-way polish chunk
//     goroutines each compute a done value, but their DB commits race; without
//     a monotonic guard the smaller-late value wins). Fixed with
//     `WHERE progress IS NULL OR progress < ?` on the processing path.
//
//  2. Terminal writes (failed/skipped) left a stale high progress from the
//     processing phase, so a failed job rendered progress≈1.0 — contradicted
//     by status=failed. Fixed by nulling progress on failed/skipped (and
//     pinning 1.0 on done).

// seedAIJob creates one queued AIJob and returns it. For status-path tests we
// don't need episode/course/user — AIJob's only NOT NULL columns are job_type
// and status.
func seedAIJob(t *testing.T, db *gorm.DB) *model.AIJob {
	t.Helper()
	job := &model.AIJob{JobType: "polish", Status: "queued"}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job
}

func reloadJob(t *testing.T, db *gorm.DB, id uint) *model.AIJob {
	t.Helper()
	var j model.AIJob
	if err := db.First(&j, id).Error; err != nil {
		t.Fatalf("reload job %d: %v", id, err)
	}
	return &j
}

// TestUpdateJobStatus_ProcessingProgressIsMonotonic verifies the monotonic
// guard: a smaller progress written AFTER a larger one must not overwrite it.
// We can't reliably force commit ordering with real goroutines (the race is
// timing-dependent), so this drives the guard directly: claim to processing,
// write progress=0.8, then write progress=0.4 — the guard must reject 0.4 and
// leave progress at 0.8.
func TestUpdateJobStatus_ProcessingProgressIsMonotonic(t *testing.T) {
	db := testutil.NewFileDB(t) // file-backed: safe across the GORM conn pool
	repo := NewAIContentRepository(db)
	job := seedAIJob(t, db)

	// Claim to processing first (the guard only applies on the processing path).
	if err := repo.UpdateJobStatus(job.ID, "processing", "", nil); err != nil {
		t.Fatalf("set processing: %v", err)
	}
	high := 0.8
	if err := repo.UpdateJobStatus(job.ID, "processing", "", &high); err != nil {
		t.Fatalf("write progress 0.8: %v", err)
	}
	low := 0.4
	if err := repo.UpdateJobStatus(job.ID, "processing", "", &low); err != nil {
		t.Fatalf("write progress 0.4: %v", err)
	}
	got := reloadJob(t, db, job.ID)
	if got.Progress == nil {
		t.Fatal("progress is NULL after writes; want 0.8")
	}
	if *got.Progress != high {
		t.Errorf("progress = %v, want %v (monotonic guard must reject the smaller-late value)", *got.Progress, high)
	}
}

// TestUpdateJobStatus_ConcurrentProgressNoRegression drives the guard under
// real concurrency: many goroutines write random-ish progress values, the
// final DB value must equal the MAX of them (the guard keeps only the largest).
// This is the actual polish-chunk-callback scenario (3-way, scaled up).
func TestUpdateJobStatus_ConcurrentProgressNoRegression(t *testing.T) {
	db := testutil.NewFileDB(t)
	repo := NewAIContentRepository(db)
	job := seedAIJob(t, db)
	if err := repo.UpdateJobStatus(job.ID, "processing", "", nil); err != nil {
		t.Fatalf("processing: %v", err)
	}

	// 20 goroutines each write a distinct progress in [0.01, 0.20]. The MAX
	// (0.20) must win regardless of commit order.
	const n = 20
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := float64(i) / 100.0 // 0.01 .. 0.20
			_ = repo.UpdateJobStatus(job.ID, "processing", "", &p)
		}(i)
	}
	wg.Wait()

	got := reloadJob(t, db, job.ID)
	wantMax := 0.20
	if got.Progress == nil {
		t.Fatal("progress NULL after concurrent writes; want 0.20")
	}
	if *got.Progress < wantMax-1e-9 {
		t.Errorf("progress = %v, want %v (monotonic guard dropped the max under concurrency)", *got.Progress, wantMax)
	}
}

// TestUpdateJobStatus_FailedNullsProgress verifies a failed job doesn't keep a
// stale high progress from the processing phase. Before the fix, failJob wrote
// status=failed with progress=nil (no update), leaving the ≈1.0 from the last
// processing write — a failed job showing a full progress bar.
func TestUpdateJobStatus_FailedNullsProgress(t *testing.T) {
	db := testutil.NewFileDB(t)
	repo := NewAIContentRepository(db)
	job := seedAIJob(t, db)
	repo.UpdateJobStatus(job.ID, "processing", "", nil)
	high := 0.9
	repo.UpdateJobStatus(job.ID, "processing", "", &high)

	// Now fail it (the partial-polish path: high progress written, then fail).
	if err := repo.UpdateJobStatus(job.ID, "failed", "partial: 1/5 chunks failed", nil); err != nil {
		t.Fatalf("fail: %v", err)
	}
	got := reloadJob(t, db, job.ID)
	if got.Status != "failed" {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.Progress != nil {
		t.Errorf("progress = %v, want nil (failed job must not keep stale processing progress)", *got.Progress)
	}
}

// TestUpdateJobStatus_DonePinsProgressOne verifies done pins progress to 1.0
// (the successful end state) rather than leaving whatever the last processing
// write happened to land on.
func TestUpdateJobStatus_DonePinsProgressOne(t *testing.T) {
	db := testutil.NewFileDB(t)
	repo := NewAIContentRepository(db)
	job := seedAIJob(t, db)
	repo.UpdateJobStatus(job.ID, "processing", "", nil)
	mid := 0.6
	repo.UpdateJobStatus(job.ID, "processing", "", &mid)

	if err := repo.UpdateJobStatus(job.ID, "done", "polished", nil); err != nil {
		t.Fatalf("done: %v", err)
	}
	got := reloadJob(t, db, job.ID)
	if got.Status != "done" {
		t.Errorf("status = %q, want done", got.Status)
	}
	if got.Progress == nil || *got.Progress != 1.0 {
		t.Errorf("progress = %v, want 1.0 (done must pin progress)", got.Progress)
	}
}
