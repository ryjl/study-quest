package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"studyquest/backend/internal/model"
)

// ai_runs_logs_integration_test.go — covers the AI observability read paths:
//
//	GET /admin/api/ai/runs       (list, newest first)
//	GET /admin/api/ai/runs/:id   (detail)
//	GET /admin/api/logs          (structured-log list)
//
// All hang off h.aiService, so they need newTestEnvWithAI. The runs path reads
// contentRepo (wired); the logs path reads logRepo, which is nil in the stock
// helper so it returns [] (the graceful-empty contract — worth pinning). Runs
// with seeded AIRun rows prove the list/detail shapes and enrichment.

// TestAIRuns_ListEmptyReturnsArray GET /ai/runs with no runs returns an empty
// array (not nil/null), so the admin UI's .map doesn't throw.
func TestAIRuns_ListEmptyReturnsArray(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)

	resp := env.do(t, http.MethodGet, "/admin/api/ai/runs", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list runs: %d %s", resp.Code, resp.Body.String())
	}
	var runs []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &runs); err != nil {
		t.Fatalf("decode runs: %v (body: %s)", err, resp.Body.String())
	}
	if runs == nil {
		t.Error("runs is nil; want an empty array (UI expects the array shape)")
	}
	if len(runs) != 0 {
		t.Errorf("runs count = %d; want 0 (fresh env)", len(runs))
	}
}

// TestAIRuns_ListAndDetailReturnSeededRun seed an AIRun (+ a parent AIJob for
// enrichment) → list returns it → detail by id returns the same run with the
// raw response_text. Guards the enrichment join + the detail path.
func TestAIRuns_ListAndDetailReturnSeededRun(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "Runs Course", "math", nil)
	episodeID := env.createEpisode(t, courseID, "Runs Ep")
	// Parent job (the run's job_id enrichment reads its episode/course titles).
	job := &model.AIJob{JobType: "summary", EpisodeID: &episodeID, CourseID: &courseID, Status: "done"}
	if err := env.db.Create(job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	run := &model.AIRun{
		JobID:        job.ID,
		Capability:   "summary",
		ModelUsed:    "fake-model",
		ResponseText: `{"headline":"test"}`,
		PromptTokens: 100, CompletionTokens: 50, DurationMs: 1234,
	}
	if err := env.db.Create(run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}

	// List: the run appears with enriched titles.
	listResp := env.do(t, http.MethodGet, "/admin/api/ai/runs", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list runs: %d %s", listResp.Code, listResp.Body.String())
	}
	var list []map[string]any
	json.Unmarshal(listResp.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("runs count = %d; want 1", len(list))
	}
	if list[0]["episode_title"] != "Runs Ep" {
		t.Errorf("run episode_title = %v; want Runs Ep (enrichment)", list[0]["episode_title"])
	}

	// Detail: the same run, with the raw response_text.
	detResp := env.do(t, http.MethodGet, "/admin/api/ai/runs/"+itoa(run.ID), nil)
	if detResp.Code != http.StatusOK {
		t.Fatalf("get run: %d %s", detResp.Code, detResp.Body.String())
	}
	var detail struct {
		ID           uint   `json:"id"`
		Capability   string `json:"capability"`
		ResponseText string `json:"response_text"`
		ModelUsed    string `json:"model_used"`
	}
	json.Unmarshal(detResp.Body.Bytes(), &detail)
	if detail.ID != run.ID || detail.Capability != "summary" {
		t.Errorf("detail = %+v; want id=%d capability=summary", detail, run.ID)
	}
	if detail.ResponseText == "" {
		t.Error("detail response_text empty; want the seeded raw response")
	}
}

// TestAIRuns_GetNotFound GET /ai/runs/:id for a nonexistent id returns 404.
func TestAIRuns_GetNotFound(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)

	resp := env.do(t, http.MethodGet, "/admin/api/ai/runs/999999", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("get nonexistent run: want 404, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}

// TestLogs_ListEmptyReturnsArrayWithNilLogRepo GET /logs with logRepo=nil
// (the stock newTestEnvWithAI config) returns an empty array, not an error —
// the nil-safe graceful-degradation contract (AI附加层铁律: logging never
// blocks business logic). Pinning this guards against a future change that
// makes ListLogs panic on a nil repo.
func TestLogs_ListEmptyReturnsArrayWithNilLogRepo(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)

	resp := env.do(t, http.MethodGet, "/admin/api/logs", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list logs: %d %s", resp.Code, resp.Body.String())
	}
	var logs []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &logs); err != nil {
		t.Fatalf("decode logs: %v (body: %s)", err, resp.Body.String())
	}
	if logs == nil {
		t.Error("logs is nil; want an empty array (graceful empty when logRepo is nil)")
	}
}
