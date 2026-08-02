package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// ai_service_lifecycle_test.go — job lifecycle e2e at the service layer.
//
// homework/polish already have full e2e (ai_service_homework_test.go,
// ai_service_polish_test.go); this file closes the gap for the dispatcher
// loop shared by ALL job types: enqueue → processOneJob claims → the right
// runXxxJob runs → status lands on done/failed/skipped and the artifact
// persists.
//
// Why service layer (not HTTP): processOneJob is the documented test seam
// ("Split from runWorker for testability", see processOneJob in ai_service.go),
// and going through the real worker goroutine would re-introduce the timing
// flakiness P0 just eliminated.
//
// Two test shapes:
//  1. TestJobLifecycle_Summary_DonePersists + _LLMFailure — full happy/failure
//     path with a fake LLM via the summaryLLMOverride seam (mirrors
//     polishLLMOverride/homeworkLLMOverride). Proves the artifact writeback.
//  2. TestJobLifecycle_DispatchRoutesByType — enqueue one job of every
//     dispatcher-admitted type with no resolver; processOneJob must route each
//     to its handler so every job leaves "queued". Guards the dispatcher's
//     job-type switch (a dropped case would leave a job stuck "processing")
//     without needing a fake per type.

// fakeSummaryLLM is a scripted LLM matching ai.LLMProvider. It returns one
// canned summary-JSON response per Chat call (the summarizer parses it into a
// SummaryResult and upserts). Mirrors fakeHomeworkLLM / fakePolishLLM.
type fakeSummaryLLM struct {
	mu       sync.Mutex
	content  string // canned response
	err      error  // when set, Chat returns this (drives the failure-path test)
	called   int
	provider string
}

func (m *fakeSummaryLLM) Chat(_ context.Context, _ ai.ChatRequest) (*ai.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.called++
	if m.err != nil {
		return nil, m.err
	}
	return &ai.ChatResponse{Content: m.content, FinishReason: "stop"}, nil
}
func (m *fakeSummaryLLM) Ping(_ context.Context) error { return nil }
func (m *fakeSummaryLLM) ProviderType() string         { return m.provider }

// validSummaryJSON is a minimal parseable SummaryResult: a headline + one
// key_point. parseSummaryJSON + normalize accept this and UpsertSummary lands it.
func validSummaryJSON() string {
	return `{
	  "headline": "分数加减法",
	  "key_points": ["同分母直接相加", "异分母先通分"],
	  "concepts": ["通分", "公分母"],
	  "sections": [],
	  "common_mistakes": ["忘记通分"],
	  "methods": [],
	  "pre_adventure": [],
	  "takeaway": "通分是关键"
	}`
}

// lifecycleEnv builds a worker-free aiService over a file DB with a non-nil
// resolver (empty provider repo) so the resolver!=nil guard passes; the
// summaryLLMOverride seam short-circuits before any real resolve. Seeds
// subject/course/episode + one subtitle chunk (the summarizer refuses empty
// chunks, Summarize:131).
func lifecycleEnv(t *testing.T) (svc *aiService, episodeID, courseID uint) {
	t.Helper()
	db := testutil.NewFileDB(t)
	subjects := testutil.SeedSubjects(t, db)
	course := &model.Course{Title: "Lifecycle Course", SubjectID: subjects["math"].ID}
	if err := db.Create(course).Error; err != nil {
		t.Fatalf("create course: %v", err)
	}
	episode := &model.Episode{Title: "Lifecycle Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := db.Create(episode).Error; err != nil {
		t.Fatalf("create episode: %v", err)
	}
	contentRepo := repository.NewAIContentRepository(db)
	contentRepo.ReplaceChunksForEpisode(episode.ID, course.ID, "subtitle", []model.ContentChunk{
		{EpisodeID: episode.ID, CourseID: course.ID, SourceType: "subtitle", ChunkIndex: 0, Text: "本课讲分数加减法"},
	})
	resolver := ai.NewProviderResolver(repository.NewAIProviderRepository(db), "")
	svc = NewAIService(db, contentRepo, repository.NewEpisodeRepository(db), repository.NewCourseRepository(db),
		resolver, nil, repository.NewUserRepository(db), nil, repository.NewSubjectRepository(db),
		nil, nil, nil, nil, nil, nil).(*aiService)
	return svc, episode.ID, course.ID
}

// seedQueuedJob inserts a job in "queued" status, the state processOneJob's
// ClaimNextQueuedJob selects.
func seedQueuedJob(t *testing.T, svc *aiService, jobType string, episodeID, courseID uint, userID *uint) {
	t.Helper()
	job := &model.AIJob{
		JobType:   jobType,
		EpisodeID: &episodeID,
		CourseID:  &courseID,
		Status:    "queued",
	}
	if userID != nil {
		job.UserID = userID
	}
	if err := svc.contentRepo.CreateJob(job); err != nil {
		t.Fatalf("seed queued %s job: %v", jobType, err)
	}
}

// firstJobID returns the id of the first job by created_at asc — the same
// ordering ClaimNextQueuedJob uses (priority desc, created_at asc), so with a
// single job this is the one processOneJob will claim.
func firstJobID(t *testing.T, svc *aiService) uint {
	t.Helper()
	var job model.AIJob
	if err := svc.db.Order("created_at asc").First(&job).Error; err != nil {
		t.Fatalf("load first job: %v", err)
	}
	return job.ID
}

// jobStatus reads a job's status straight from the DB.
func jobStatus(t *testing.T, svc *aiService, jobID uint) string {
	t.Helper()
	var job model.AIJob
	if err := svc.db.First(&job, jobID).Error; err != nil {
		t.Fatalf("load job %d: %v", jobID, err)
	}
	return job.Status
}

// TestJobLifecycle_Summary_DonePersists is the full happy-path lifecycle:
// enqueue a summary job → processOneJob claims and runs it via the fake LLM →
// the job lands "done" and an AISummary row is persisted for the episode. This
// is the single test that proves the artifact writeback end-to-end.
func TestJobLifecycle_Summary_DonePersists(t *testing.T) {
	svc, episodeID, courseID := lifecycleEnv(t)
	fake := &fakeSummaryLLM{content: validSummaryJSON(), provider: "fake-summary"}
	svc.summaryLLMOverride = fake

	seedQueuedJob(t, svc, "summary", episodeID, courseID, nil)
	jobID := firstJobID(t, svc)

	// Drive ONE job through the dispatcher (the same call runWorker makes per
	// tick). No goroutine, no timing — deterministic.
	svc.processOneJob([]string{"summary", "quiz", "advice", "homework", "polish"})

	if got := jobStatus(t, svc, jobID); got != "done" {
		t.Fatalf("job status after processOneJob = %q; want done", got)
	}
	if fake.called == 0 {
		t.Error("fake LLM never called; summaryLLMOverride didn't short-circuit the resolver")
	}

	// Artifact persisted: an AISummary row for this episode exists.
	var summ model.AISummary
	if err := svc.db.Where("episode_id = ?", episodeID).First(&summ).Error; err != nil {
		t.Fatalf("AISummary for episode %d not persisted: %v", episodeID, err)
	}
	if summ.SummaryJSON == "" {
		t.Error("AISummary.SummaryJSON empty; summarizer didn't upsert the parsed result")
	}
}

// TestJobLifecycle_Summary_LLMFailureMarksFailed the failure path: when the
// LLM errors on BOTH retry attempts (runSummaryJob retries once = 2 attempts),
// the job lands "failed" with the error message recorded (visible in admin UI).
func TestJobLifecycle_Summary_LLMFailureMarksFailed(t *testing.T) {
	svc, episodeID, courseID := lifecycleEnv(t)
	svc.summaryLLMOverride = &fakeSummaryLLM{err: errors.New("relay 504"), provider: "fake-summary"}

	seedQueuedJob(t, svc, "summary", episodeID, courseID, nil)
	jobID := firstJobID(t, svc)

	svc.processOneJob([]string{"summary"})

	var job model.AIJob
	if err := svc.db.First(&job, jobID).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}
	if job.Status != "failed" {
		t.Errorf("job status = %q; want failed after LLM error on both attempts", job.Status)
	}
	if job.Error == "" {
		t.Error("job.Error empty; want the failure message recorded for the admin UI")
	}
}

// TestJobLifecycle_DispatchRoutesByType guards the processOneJob job-type
// switch (ai_service.go:1023): enqueue one job of EVERY dispatcher-admitted type
// with NO resolver wired (so each runXxxJob hits its nil-resolver early-return →
// "skipped") and assert each job LEAVES "queued". A regression that dropped a
// case from the switch would leave a job "processing" (claimed but never run) —
// caught here.
//
// Covers 5 of 8 types here: summary/quiz/advice (single-call or agent paths
// behind the resolver gate) + course_summary/user_report. The other 3 are
// covered elsewhere: segment (subtitle_jobs_integration), polish + homework
// (full e2e in ai_service_polish_test / ai_service_homework_test), and summary
// has its own persist-verified e2e above. The agent-backed jobs (quiz/advice/
// course_summary/user_report) use multi-step ReAct loops, so faking their full
// happy-path artifact write would mean reimplementing each agent's tool-call
// protocol — out of scope; this dispatch test proves they're ROUTED, which is
// the lifecycle-layer regression risk.
func TestJobLifecycle_DispatchRoutesByType(t *testing.T) {
	db := testutil.NewFileDB(t)
	subjects := testutil.SeedSubjects(t, db)
	course := &model.Course{Title: "Dispatch Course", SubjectID: subjects["math"].ID}
	db.Create(course)
	episode := &model.Episode{Title: "Dispatch Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	db.Create(episode)
	contentRepo := repository.NewAIContentRepository(db)
	user := &model.User{Nickname: "dispatch-user", PinHash: "x", Role: "student"}
	db.Create(user)
	// resolver=nil: every runXxxJob early-returns "skipped" (the nil-resolver
	// guard). The job still must be claimed + routed for status to leave "queued".
	svc := NewAIService(db, contentRepo, repository.NewEpisodeRepository(db), repository.NewCourseRepository(db),
		nil, nil, repository.NewUserRepository(db), nil, nil, nil, nil, nil, nil, nil, nil).(*aiService)

	types := []string{"summary", "quiz", "advice", "course_summary", "user_report"}
	for _, jt := range types {
		seedQueuedJob(t, svc, jt, episode.ID, course.ID, &user.ID)
	}

	// processOneJob handles one job per call; drive one per type.
	for i := 0; i < len(types); i++ {
		svc.processOneJob(types)
	}

	var jobs []model.AIJob
	if err := svc.db.Find(&jobs).Error; err != nil {
		t.Fatalf("load jobs: %v", err)
	}
	if len(jobs) != len(types) {
		t.Fatalf("expected %d jobs, got %d", len(types), len(jobs))
	}
	for _, j := range jobs {
		// "skipped" (nil resolver) is the expected terminal state; the point is
		// NONE stay "queued" (unclaimed) or "processing" (claimed-but-never-run,
		// which is what a missing switch case would cause).
		if j.Status == "queued" || j.Status == "processing" {
			t.Errorf("job %d (type %s) stuck in %q — dispatcher didn't route it (switch case missing?)",
				j.ID, j.JobType, j.Status)
		}
	}
}
