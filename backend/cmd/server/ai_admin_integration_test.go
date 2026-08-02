package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"studyquest/backend/internal/model"
)

// AI admin integration tests cover the HTTP surface of two clusters that hang
// off h.aiService on the admin handler:
//
//   AI jobs:  GET /admin/api/ai/jobs, GET /admin/api/ai/jobs/:id,
//             POST /admin/api/ai/jobs/:id/reset
//   glossary: GET /admin/api/courses/:id/glossary-candidates,
//             POST /admin/api/glossary-candidates/:id/accept,
//             POST /admin/api/glossary-candidates/:id/reject,
//             POST /admin/api/glossary-candidates/accept-batch
//
// These run against newTestEnvWithAI (real aiService wired). EnqueueAIJobs is
// NOT exercised here — it kicks off real worker processing and overlaps with
// the service-layer lifecycle e2e in ai_service_lifecycle_test.go; this file
// covers the read/admin-action paths only.

// aiJobDTO is the subset of the job DTO these tests assert on.
type aiJobDTO struct {
	ID      uint   `json:"id"`
	JobType string `json:"job_type"`
	Status  string `json:"status"`
}

// seedAIJob inserts a bare AIJob row directly (no enqueue path) so these tests
// can stage any status without depending on the worker. Returns the job's ID.
func (e *testEnv) seedAIJob(t *testing.T, jobType, status string) uint {
	t.Helper()
	j := &model.AIJob{JobType: jobType, Status: status}
	if err := e.db.Create(j).Error; err != nil {
		t.Fatalf("seed AIJob (%s/%s): %v", jobType, status, err)
	}
	return j.ID
}

// ─── AI jobs ───

// TestAIJobs_ListEmptyReturnsJobsAndStats GET /ai/jobs on a fresh env returns
// an empty jobs array plus a stats object (the UI renders counts from stats
// without a second request, so the shape — not just the 200 — is the contract).
func TestAIJobs_ListEmptyReturnsJobsAndStats(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)

	resp := env.do(t, http.MethodGet, "/admin/api/ai/jobs", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list jobs: %d %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Jobs  []aiJobDTO      `json:"jobs"`
		Stats map[string]any `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode jobs list: %v (body: %s)", err, resp.Body.String())
	}
	if body.Jobs == nil {
		t.Error("jobs is nil; want an empty array (UI expects the array shape)")
	}
	if body.Stats == nil {
		t.Error("stats is nil; want a stats object even when empty")
	}
}

// TestAIJobs_GetNotFound GET /ai/jobs/:id for a nonexistent id returns 404
// (GetJob returns a nil view, which the handler maps to 404, not 500).
func TestAIJobs_GetNotFound(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)

	resp := env.do(t, http.MethodGet, "/admin/api/ai/jobs/999999", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("get nonexistent job: want 404, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestAIJobs_ResetProcessingReturnsOK POST /ai/jobs/:id/reset on a 'processing'
// job resets it to queued and returns 200 — the manual counterpart of the
// reaper, for a job stuck while the worker is still alive.
func TestAIJobs_ResetProcessingReturnsOK(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	jobID := env.seedAIJob(t, "summary", "processing")

	resp := env.do(t, http.MethodPost, "/admin/api/ai/jobs/"+itoa(jobID)+"/reset", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("reset processing job: want 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	// Verify the status actually flipped to queued (ResetJob's UPDATE only
	// fires on status='processing', so a stale read would mask a no-op).
	var job model.AIJob
	if err := env.db.First(&job, jobID).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if job.Status != "queued" {
		t.Errorf("job status after reset = %q; want queued", job.Status)
	}
}

// TestAIJobs_ResetDoneReturns409 resetting a non-processing (e.g. done) job is
// rejected with 409 (ErrJobNotProcessing) so a double-reset surfaces as "nothing
// to reset" instead of silently pretending success.
func TestAIJobs_ResetDoneReturns409(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	jobID := env.seedAIJob(t, "summary", "done")

	resp := env.do(t, http.MethodPost, "/admin/api/ai/jobs/"+itoa(jobID)+"/reset", nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("reset done job: want 409, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// ─── Glossary candidates ───

// seedGlossaryCandidate inserts a pending candidate tied to a real course, the
// shape the polish job persists. Returns the candidate's ID.
func (e *testEnv) seedGlossaryCandidate(t *testing.T, courseID uint, original, corrected string) uint {
	t.Helper()
	c := &model.GlossaryCandidate{
		CourseID:   courseID,
		Original:   original,
		Corrected:  corrected,
		Confidence: 0.9,
		Status:     "pending",
	}
	if err := e.db.Create(c).Error; err != nil {
		t.Fatalf("seed glossary candidate: %v", err)
	}
	return c.ID
}

// TestGlossary_ListReturnsCandidates GET /courses/:id/glossary-candidates
// surfaces candidates the polish job mined for that course, ordered by
// confidence. An empty course returns an empty array (UI shape contract).
func TestGlossary_ListReturnsCandidates(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "Glossary Course", "chinese", nil)
	env.seedGlossaryCandidate(t, courseID, "马走日", "马走日(象棋术语)")

	resp := env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(courseID)+"/glossary-candidates", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list candidates: %d %s", resp.Code, resp.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode candidates: %v (body: %s)", err, resp.Body.String())
	}
	if len(rows) != 1 {
		t.Fatalf("candidates count = %d; want 1", len(rows))
	}
	if rows[0]["original"] != "马走日" {
		t.Errorf("candidate original = %v; want 马走日", rows[0]["original"])
	}
}

// TestGlossary_AcceptThenReacceptIs409 accept a pending candidate → 200; a
// second accept on the same candidate → 409 (ErrGlossaryNotPending: already
// reviewed). Guards the status gate that stops double-acceptance.
func TestGlossary_AcceptThenReacceptIs409(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "Accept Course", "chinese", nil)
	candID := env.seedGlossaryCandidate(t, courseID, "orig", "corr")

	resp := env.do(t, http.MethodPost, "/admin/api/glossary-candidates/"+itoa(candID)+"/accept", map[string]any{})
	if resp.Code != http.StatusOK {
		t.Fatalf("first accept: want 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	resp2 := env.do(t, http.MethodPost, "/admin/api/glossary-candidates/"+itoa(candID)+"/accept", map[string]any{})
	if resp2.Code != http.StatusConflict {
		t.Fatalf("re-accept: want 409 (already reviewed), got %d (body: %s)", resp2.Code, resp2.Body.String())
	}
}

// TestGlossary_RejectThenReacceptIs409 reject a pending candidate → 200; then
// accepting it → 409 (it's no longer pending). Guards the same gate from the
// reject side.
func TestGlossary_RejectThenReacceptIs409(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "Reject Course", "chinese", nil)
	candID := env.seedGlossaryCandidate(t, courseID, "orig2", "corr2")

	resp := env.do(t, http.MethodPost, "/admin/api/glossary-candidates/"+itoa(candID)+"/reject", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("reject: want 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	resp2 := env.do(t, http.MethodPost, "/admin/api/glossary-candidates/"+itoa(candID)+"/accept", map[string]any{})
	if resp2.Code != http.StatusConflict {
		t.Fatalf("accept after reject: want 409 (not pending), got %d (body: %s)", resp2.Code, resp2.Body.String())
	}
}

// TestGlossary_AcceptBatch accept-batch processes multiple pending candidates
// and reports which succeeded. A subsequent list filtered to pending omits the
// accepted ones. Guards the batch entry point the admin UI uses for bulk review.
func TestGlossary_AcceptBatch(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "Batch Course", "chinese", nil)
	id1 := env.seedGlossaryCandidate(t, courseID, "b1", "b1c")
	id2 := env.seedGlossaryCandidate(t, courseID, "b2", "b2c")

	resp := env.do(t, http.MethodPost, "/admin/api/glossary-candidates/accept-batch", map[string]any{
		"ids": []uint{id1, id2},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("accept-batch: want 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}
	var body struct {
		Accepted []uint `json:"accepted"`
		OK       bool   `json:"ok"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode accept-batch: %v (body: %s)", err, resp.Body.String())
	}
	if len(body.Accepted) != 2 {
		t.Errorf("accepted count = %d; want 2 (ids: %v)", len(body.Accepted), body.Accepted)
	}
	if !body.OK {
		t.Error("ok = false; want true (no errors expected)")
	}

	// Filtered list: pending status should now exclude both.
	listResp := env.do(t, http.MethodGet, "/admin/api/courses/"+itoa(courseID)+"/glossary-candidates?status=pending", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("pending list after batch: %d %s", listResp.Code, listResp.Body.String())
	}
	var pending []map[string]any
	json.Unmarshal(listResp.Body.Bytes(), &pending)
	if len(pending) != 0 {
		t.Errorf("pending candidates after batch = %d; want 0", len(pending))
	}
}
