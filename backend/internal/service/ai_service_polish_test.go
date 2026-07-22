package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// polishTestEnv wires a real aiService (file-backed DB, real repos, glossary +
// subject repos). The resolver defaults to nil — most tests want the "AI off"
// path. Tests that need the source-skip branch (which sits AFTER the nil-
// resolver check in runPolishJob) pass withResolver=true to get a non-nil but
// empty resolver (NewProviderResolver(nil,"") — its ResolveChatByPurpose
// returns ErrNoProvider, but the source-skip branch runs before that call).
//
// The full LLM-driven partial/success paths (writeback + chain + detail) are
// covered by the *_E2E tests below, which use polishE2EEnv + a fake LLM
// injected via aiService.polishLLMOverride. This polishTestEnv is for the
// non-LLM service-layer behavior (skip routing, isPolishableSource consistency,
// chain semantics, no-resolver block).
func polishTestEnv(t *testing.T, withResolver bool) (*aiService, repository.EpisodeRepository, repository.CourseRepository, repository.GlossaryRepository, repository.SubjectRepository) {
	t.Helper()
	db := testutil.NewFileDB(t)
	contentRepo := repository.NewAIContentRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	glossaryRepo := repository.NewGlossaryRepository(db)
	subjectRepo := repository.NewSubjectRepository(db)
	var resolver *ai.ProviderResolver
	if withResolver {
		// Non-nil but empty resolver: no AIProvider rows configured, so any
		// ResolveChatByPurpose call returns ErrNoProvider. The source-skip and
		// no-resolver-fail branches in runPolishJob run before that call, so
		// they're reachable with this fixture.
		resolver = ai.NewProviderResolver(nil, "")
	}
	svc := NewAIService(
		db, contentRepo, episodeRepo, courseRepo,
		resolver,
		nil, nil,
		glossaryRepo,
		subjectRepo,
	).(*aiService)
	t.Cleanup(svc.Stop) // release the worker goroutine
	return svc, episodeRepo, courseRepo, glossaryRepo, subjectRepo
}

// TestIsPolishableSource pins the single source of truth for which subtitle
// sources the polish pipeline admits. This helper is shared between EnqueuePolish
// and runPolishJob — a regression that lets the two disagree re-introduces the
// "queued → skipped" loop bug (see ai_jobs.error "source=llm_optimized not
// eligible for polish").
func TestIsPolishableSource(t *testing.T) {
	cases := []struct {
		source string
		want   bool
	}{
		{"whisper", true},        // primary case: raw transcript to fix
		{"llm_optimized", true},  // re-polish with richer TermDict
		{"embedded", false},      // human-corrected, don't touch
		{"manual", false},        // admin-uploaded, don't touch
		{"", false},              // default-empty, treat as non-polishable
		{"unknown", false},       // future/stray value, conservative reject
	}
	for _, c := range cases {
		got := isPolishableSource(c.source)
		if got != c.want {
			t.Errorf("isPolishableSource(%q) = %v, want %v", c.source, got, c.want)
		}
	}
}

// TestEnqueuePolish_AdmitsLlmOptimized is the regression test for the
// "queued → skipped" loop: EnqueuePolish must admit source=llm_optimized so
// re-polish (after admin accepts glossary terms) is possible. Combined with
// TestRunPolishJob_LlmOptimizedNotSkipped (which we can't easily write without
// a fake resolver — see comment in polishTestEnv), this locks down one half of
// the enqueue/execute consistency contract.
func TestEnqueuePolish_AdmitsLlmOptimized(t *testing.T) {
	svc, episodeRepo, courseRepo, _, _ := polishTestEnv(t, false)
	courseID, epID := seedPolishEpisode(t, episodeRepo, courseRepo, "llm_optimized")

	enqueued, skipped, err := svc.EnqueuePolish([]uint{epID})
	if err != nil {
		t.Fatalf("EnqueuePolish: %v", err)
	}
	if len(enqueued) != 1 || enqueued[0] != epID {
		t.Fatalf("expected ep %d enqueued for re-polish, got enqueued=%v skipped=%v",
			epID, enqueued, skipped)
	}
	// Also assert the job row landed in the DB so downstream runPolishJob will
	// pick it up. (runPolishJob's isPolishableSource check will also accept it,
	// so this job won't get skipped — see TestIsPolishableSource above.)
	var n int64
	svc.db.Model(&model.AIJob{}).
		Where("job_type = ? AND episode_id = ?", "polish", epID).Count(&n)
	if n != 1 {
		t.Errorf("expected 1 polish job row, got %d", n)
	}
	_ = courseID
}

// TestEnqueuePolish_RejectsEmbedded asserts the other half of the contract:
// embedded/manual tracks are NOT admissible. This guards against a future
// relaxation that accidentally admits human-corrected tracks.
func TestEnqueuePolish_RejectsEmbedded(t *testing.T) {
	svc, episodeRepo, courseRepo, _, _ := polishTestEnv(t, false)
	_, epID := seedPolishEpisode(t, episodeRepo, courseRepo, "embedded")

	enqueued, skipped, err := svc.EnqueuePolish([]uint{epID})
	if err != nil {
		t.Fatalf("EnqueuePolish: %v", err)
	}
	if len(enqueued) != 0 {
		t.Errorf("expected embedded-source episode to be rejected, got enqueued=%v", enqueued)
	}
	if _, ok := skipped[epID]; !ok {
		t.Errorf("expected ep %d in skipped map, got %v", epID, skipped)
	}
}

// TestRunPolishJob_NonPolishableSourceSkipsAndChains verifies the source-skip
// branch in runPolishJob: when an embedded/manual subtitle slips through (e.g.
// OnSubtitleCompleted misrouted), the job is marked skipped (NOT failed) AND a
// segment job is chained so downstream AI still proceeds off the raw track.
// This is the only runPolishJob branch reachable without a resolver.
func TestRunPolishJob_NonPolishableSourceSkipsAndChains(t *testing.T) {
	// withResolver=true so runPolishJob's nil-resolver check passes and we
	// reach the source-skip branch. The empty resolver's ResolveChatByPurpose
	// would return ErrNoProvider, but source-skip runs before that call.
	svc, episodeRepo, courseRepo, _, _ := polishTestEnv(t, true)
	courseID, epID := seedPolishEpisode(t, episodeRepo, courseRepo, "embedded")

	// Create a polish job manually (the runPolishJob input).
	epIDCopy, courseIDCopy := epID, courseID
	job := &model.AIJob{
		JobType:   "polish",
		EpisodeID: &epIDCopy,
		CourseID:  &courseIDCopy,
		Status:    "processing",
		Priority:  priorityPolish,
	}
	if err := svc.contentRepo.CreateJob(job); err != nil {
		t.Fatalf("seed polish job: %v", err)
	}

	svc.runPolishJob(job)

	// Job should be skipped (not failed — this isn't an error, just a no-op).
	got, err := svc.contentRepo.GetJob(job.ID)
	if err != nil || got == nil {
		t.Fatalf("reload job: %v err=%v", got, err)
	}
	if got.Status != "skipped" {
		t.Errorf("status = %q, want skipped", got.Status)
	}

	// And a segment job should have been chained so downstream AI proceeds.
	var segN int64
	svc.db.Model(&model.AIJob{}).
		Where("job_type = ? AND episode_id = ?", "segment", epID).Count(&segN)
	if segN != 1 {
		t.Errorf("expected 1 chained segment job, got %d", segN)
	}
}

// TestRunPolishJob_NoResolverFails verifies that when the resolver is nil
// (AI not configured), a polish job is marked failed (NOT skipped) so the admin
// notices. This is the "block, don't skip" semantic for polish — the whole
// point of polish is to fix the raw transcript before AI consumes it, so a
// missing provider should surface as a fixable error, not silently degrade.
func TestRunPolishJob_NoResolverFails(t *testing.T) {
	// withResolver=false: nil resolver → runPolishJob's first check fails the
	// job. This is the "block, don't skip" semantic for polish.
	svc, episodeRepo, courseRepo, _, _ := polishTestEnv(t, false)
	courseID, epID := seedPolishEpisode(t, episodeRepo, courseRepo, "whisper")

	epIDCopy, courseIDCopy := epID, courseID
	job := &model.AIJob{
		JobType:   "polish",
		EpisodeID: &epIDCopy,
		CourseID:  &courseIDCopy,
		Status:    "processing",
		Priority:  priorityPolish,
	}
	if err := svc.contentRepo.CreateJob(job); err != nil {
		t.Fatalf("seed polish job: %v", err)
	}

	svc.runPolishJob(job)

	got, _ := svc.contentRepo.GetJob(job.ID)
	if got.Status != "failed" {
		t.Errorf("status = %q, want failed (no resolver → block)", got.Status)
	}
	// And NO segment job chained (failed polish halts the chain; admin retries
	// or skips manually).
	var segN int64
	svc.db.Model(&model.AIJob{}).
		Where("job_type = ? AND episode_id = ?", "segment", epID).Count(&segN)
	if segN != 0 {
		t.Errorf("expected 0 segment jobs after failed polish, got %d", segN)
	}
}

// seedPolishEpisode creates a course + episode + a primary subtitle with the
// given source, returns (courseID, episodeID). The subtitle's RawVttContent is
// seeded too (runPolishJob reads it as the polish input).
func seedPolishEpisode(t *testing.T, episodeRepo repository.EpisodeRepository, courseRepo repository.CourseRepository, source string) (courseID, epID uint) {
	t.Helper()
	course := &model.Course{Title: "Polish Test Course"}
	if err := courseRepo.Create(course); err != nil {
		t.Fatalf("create course: %v", err)
	}
	ep := &model.Episode{Title: "Polish Test Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := episodeRepo.Create(ep); err != nil {
		t.Fatalf("create episode: %v", err)
	}
	// Seed a primary subtitle with the requested source. Use SaveSubtitle to
	// exercise the real upsert path (mirrors how whisper Complete + embedded
	// extractor + manual upload populate the row).
	sub := &model.Subtitle{
		EpisodeID:     ep.ID,
		Language:      "zh-CN",
		Label:         "中文",
		VttContent:    "WEBVTT\n\n1\n00:00:00.000 --> 00:00:01.000\n考算\n",
		RawVttContent: "WEBVTT\n\n1\n00:00:00.000 --> 00:00:01.000\n考算\n",
		Source:        source,
		IsPrimary:     true,
	}
	if err := episodeRepo.SaveSubtitle(sub); err != nil {
		t.Fatalf("seed subtitle (source=%s): %v", source, err)
	}
	// Reload to pick up the assigned ID + verify source persisted.
	got, err := episodeRepo.GetSubtitle(ep.ID)
	if err != nil || got == nil {
		t.Fatalf("reload subtitle: %v err=%v", got, err)
	}
	if got.Source != source {
		t.Fatalf("seed subtitle source = %q, want %q (SaveSubtitle didn't persist?)", got.Source, source)
	}
	return course.ID, ep.ID
}

// --- end-to-end polish tests (via polishLLMOverride seam) -------------------
//
// These exercise the FULL runPolishJob path — LLM call, writeback, glossary
// upsert, detail string, and chain-to-segment — with a fake LLM injected via
// aiService.polishLLMOverride. Without the override this path is untestable at
// the service layer (the real resolver only builds openai_compat HTTP clients).
//
// The fakeLLM here is the service-layer analog of polish_test.go's fakeLLM:
// it returns scripted JSON responses (or errors) per Chat call, so we can drive
// runPolishJob through both the all-success and partial-failure trajectories.

// fakePolishLLM is a scriptable ai.LLMProvider for service-level polish tests.
// Each Chat call pops the next canned response. Responses are raw JSON strings
// the polish pipeline expects (the same {changes,glossary} envelope
// parsePolishJSON parses). When a response's `err` is non-nil, Chat returns
// that error instead — used to simulate network/relay failures for the
// partial-failure test.
type fakePolishLLM struct {
	mu        sync.Mutex
	responses []fakePolishResp
	calls     int
}

type fakePolishResp struct {
	content string
	err     error
}

func (m *fakePolishLLM) Chat(_ context.Context, _ ai.ChatRequest) (*ai.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.calls
	m.calls++
	if idx >= len(m.responses) {
		return nil, errors.New("fakePolishLLM: no more scripted responses")
	}
	r := m.responses[idx]
	if r.err != nil {
		return nil, r.err
	}
	return &ai.ChatResponse{Content: r.content, FinishReason: "stop"}, nil
}
func (m *fakePolishLLM) Ping(_ context.Context) error { return nil }
func (m *fakePolishLLM) ProviderType() string         { return "fake-polish" }

// polishE2EEnv wires an aiService WITH a fake LLM injected via polishLLMOverride,
// plus a real (empty) resolver so the nil-resolver guard passes. The fake LLM
// is returned so each test scripts its own responses.
func polishE2EEnv(t *testing.T, llm *fakePolishLLM) (*aiService, repository.EpisodeRepository, repository.CourseRepository) {
	t.Helper()
	db := testutil.NewFileDB(t)
	contentRepo := repository.NewAIContentRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	// Non-nil empty resolver — runPolishJob's nil check passes, but
	// polishLLMOverride short-circuits before any resolver call.
	resolver := ai.NewProviderResolver(nil, "")
	svc := NewAIService(
		db, contentRepo, episodeRepo, courseRepo,
		resolver, nil, nil,
		repository.NewGlossaryRepository(db),
		repository.NewSubjectRepository(db),
	).(*aiService)
	svc.polishLLMOverride = llm
	t.Cleanup(svc.Stop)
	return svc, episodeRepo, courseRepo
}

// TestRunPolishJob_AllSuccess_E2E drives the happy path end-to-end: fake LLM
// returns a valid homophone fix → runPolishJob writes back the polished subtitle
// (source=llm_optimized, optimized=true), chains a segment job, and marks the
// job done with a detail string containing high_edit_distance.
//
// This is the test that was MISSING before — it covers the writeback + chain
// + detail logic that the polish-package tests (which stop at Polish()'s return
// value) can't reach.
func TestRunPolishJob_AllSuccess_E2E(t *testing.T) {
	llm := &fakePolishLLM{responses: []fakePolishResp{{
		// One chunk, one valid fix: 考算 → 口算.
		content: `{"changes":[{"id":1,"text":"口算"}],"glossary":[]}`,
	}}}
	svc, episodeRepo, courseRepo := polishE2EEnv(t, llm)
	courseID, epID := seedPolishEpisode(t, episodeRepo, courseRepo, "whisper")

	epIDCopy, courseIDCopy := epID, courseID
	job := &model.AIJob{
		JobType: "polish", EpisodeID: &epIDCopy, CourseID: &courseIDCopy,
		Status: "processing", Priority: priorityPolish,
	}
	if err := svc.contentRepo.CreateJob(job); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	svc.runPolishJob(job)

	// Job done with detail mentioning high_edit_distance.
	got, _ := svc.contentRepo.GetJob(job.ID)
	if got.Status != "done" {
		t.Fatalf("status = %q, want done (full success)", got.Status)
	}
	if !strings.Contains(got.Error, "high_edit_distance=0") {
		t.Errorf("detail should contain high_edit_distance=0, got: %s", got.Error)
	}

	// Subtitle written back with source=llm_optimized + optimized=true.
	sub, _ := episodeRepo.GetSubtitle(epID)
	if sub == nil {
		t.Fatal("subtitle missing after polish")
	}
	if sub.Source != "llm_optimized" || !sub.Optimized {
		t.Errorf("subtitle source=%q optimized=%v, want llm_optimized/true", sub.Source, sub.Optimized)
	}
	if !strings.Contains(sub.VttContent, "口算") {
		t.Errorf("polished VTT should contain 口算, got: %s", sub.VttContent)
	}
	// RawVttContent preserved (the immutable snapshot — polish must not touch it).
	if !strings.Contains(sub.RawVttContent, "考算") {
		t.Errorf("RawVttContent should still contain 考算 (immutable snapshot), got: %s", sub.RawVttContent)
	}

	// Segment job chained.
	var segN int64
	svc.db.Model(&model.AIJob{}).
		Where("job_type = ? AND episode_id = ?", "segment", epID).Count(&segN)
	if segN != 1 {
		t.Errorf("expected 1 chained segment job, got %d", segN)
	}
}

// TestRunPolishJob_Partial_E2E drives the partial-failure path: fake LLM returns
// garbage (unparseable JSON) for all retry attempts → runPolishJob marks the job
// FAILED, does NOT write back the subtitle (stays whisper/raw), does NOT chain
// segment, and the detail names the failed chunk.
//
// This locks down the 2026-07-21 behavior change (partial used to be done-with-
// partial; now it's a hard fail that halts the chain).
func TestRunPolishJob_Partial_E2E(t *testing.T) {
	// Script maxRetries (3) garbage responses so the chunk exhausts retries.
	var resps []fakePolishResp
	for i := 0; i < maxRetriesForServiceTest; i++ {
		resps = append(resps, fakePolishResp{content: "not json at all"})
	}
	llm := &fakePolishLLM{responses: resps}
	svc, episodeRepo, courseRepo := polishE2EEnv(t, llm)
	courseID, epID := seedPolishEpisode(t, episodeRepo, courseRepo, "whisper")

	epIDCopy, courseIDCopy := epID, courseID
	job := &model.AIJob{
		JobType: "polish", EpisodeID: &epIDCopy, CourseID: &courseIDCopy,
		Status: "processing", Priority: priorityPolish,
	}
	if err := svc.contentRepo.CreateJob(job); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	svc.runPolishJob(job)

	// Job FAILED (not done-with-partial).
	got, _ := svc.contentRepo.GetJob(job.ID)
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed (partial halts)", got.Status)
	}
	// Detail mentions the partial + the chunk failure.
	if !strings.Contains(got.Error, "partial") {
		t.Errorf("detail should mention partial, got: %s", got.Error)
	}

	// Subtitle NOT written back — stays whisper, optimized=false.
	sub, _ := episodeRepo.GetSubtitle(epID)
	if sub == nil {
		t.Fatal("subtitle missing")
	}
	if sub.Source != "whisper" || sub.Optimized {
		t.Errorf("subtitle source=%q optimized=%v, want whisper/false (partial must not write back)", sub.Source, sub.Optimized)
	}
	if !strings.Contains(sub.VttContent, "考算") {
		t.Errorf("VTT should still be the raw 考算 (untouched), got: %s", sub.VttContent)
	}

	// NO segment job chained.
	var segN int64
	svc.db.Model(&model.AIJob{}).
		Where("job_type = ? AND episode_id = ?", "segment", epID).Count(&segN)
	if segN != 0 {
		t.Errorf("expected 0 segment jobs after partial-fail polish, got %d", segN)
	}
}

// TestRunPolishJob_HighEditDistanceFlagged_E2E verifies the informational
// high_edit_distance stat flows through to the job detail when the LLM returns
// a suspicious rewrite (applied, but flagged). This is the audit trail the admin
// uses to decide which cues to spot-check in the diff UI.
func TestRunPolishJob_HighEditDistanceFlagged_E2E(t *testing.T) {
	llm := &fakePolishLLM{responses: []fakePolishResp{{
		// A clear rewrite: 考算 (2 chars) → completely different long text.
		// Applied under the relaxed rules, but flagged high_edit_distance.
		content: `{"changes":[{"id":1,"text":"完全不一样的长句子这里"}],"glossary":[]}`,
	}}}
	svc, episodeRepo, courseRepo := polishE2EEnv(t, llm)
	courseID, epID := seedPolishEpisode(t, episodeRepo, courseRepo, "whisper")

	epIDCopy, courseIDCopy := epID, courseID
	job := &model.AIJob{
		JobType: "polish", EpisodeID: &epIDCopy, CourseID: &courseIDCopy,
		Status: "processing", Priority: priorityPolish,
	}
	if err := svc.contentRepo.CreateJob(job); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	svc.runPolishJob(job)

	got, _ := svc.contentRepo.GetJob(job.ID)
	if got.Status != "done" {
		t.Fatalf("status = %q, want done (rewrite is applied, not failed)", got.Status)
	}
	// high_edit_distance=1 in the detail prefix + the [注意] callout.
	if !strings.Contains(got.Error, "high_edit_distance=1") {
		t.Errorf("detail should show high_edit_distance=1, got: %s", got.Error)
	}
	if !strings.Contains(got.Error, "注意") {
		t.Errorf("detail should contain the [注意] callout, got: %s", got.Error)
	}
}

// maxRetriesForServiceTest mirrors polish.maxRetries (3). We can't import the
// polish package's unexported const, so we hardcode the same value here with a
// comment pointing at the source. If polish.maxRetries ever changes, update this
// too — the partial test depends on scripting exactly that many failures.
const maxRetriesForServiceTest = 3 // == polish.maxRetries
