package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"studyquest/backend/internal/handler"
	"studyquest/backend/internal/middleware"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newSubtitleJobRepo builds a fresh repo against the test env's DB. Used for
// service/repo-level assertions that bypass HTTP.
func (e *testEnv) newSubtitleJobRepo() repository.SubtitleJobRepository {
	return repository.NewSubtitleJobRepository(e.db)
}
func (e *testEnv) newSubtitleJobService() service.SubtitleJobService {
	return service.NewSubtitleJobService(
		e.newSubtitleJobRepo(),
		repository.NewEpisodeRepository(e.db),
		e.episodeServiceForTest(),
		repository.NewCourseRepository(e.db),
		repository.NewChapterRepository(e.db),
		repository.NewSubjectRepository(e.db),
	)
}

// episodeServiceForTest returns the same EpisodeService the running server
// uses. We can't read it off the handler, so rebuild from the same repos.
func (e *testEnv) episodeServiceForTest() service.EpisodeService {
	return service.NewEpisodeService(
		repository.NewEpisodeRepository(e.db),
		service.NewStorageProviderResolver(repository.NewStorageSourceRepository(e.db)),
	)
}

// TestSubtitleJobEnqueueGate covers the service-layer business rules: an
// entertainment episode is refused, an episode with an existing subtitle is
// skipped, re-enqueuing an active job de-duplicates, and a clean learning
// episode enqueues. These don't touch the storage provider.
func TestSubtitleJobEnqueueGate(t *testing.T) {
	env := newTestEnv(t)

	// learning course + entertainment course, each with one episode.
	learningCourse := env.createCourse(t, "数学基础", "math", nil)
	entCourse := env.createCourse(t, "动画片", "chinese", nil)
	// flip the entertainment course's content_type directly (createCourse always
	// makes learning).
	env.setContentType(t, entCourse, string(model.ContentEntertainment))

	learnEp := env.createEpisode(t, learningCourse, "第1集")
	entEp := env.createEpisode(t, entCourse, "第1集")

	svc := env.newSubtitleJobService()

	// 1. Entertainment episode → 2026-07-20 放开后,娱乐课也走正常字幕流程
	// (不再 skip),让 AI 链能跑起来。之前这里断言 skip,现在断言入队成功。
	entJob, skipped, reason, err := svc.Enqueue(entEp, 0, "")
	if err != nil || skipped || reason != "" {
		t.Fatalf("entertainment episode should enqueue (not skip) after 2026-07-20 refactor, got job=%v skipped=%v reason=%q err=%v", entJob, skipped, reason, err)
	}

	// 2. Clean learning episode → enqueued.
	job, skipped, reason, err := svc.Enqueue(learnEp, 0, "")
	if err != nil || skipped || reason != "" {
		t.Fatalf("clean enqueue: expected ok, got job=%v skipped=%v reason=%q err=%v", job, skipped, reason, err)
	}
	if job.Status != model.SubtitleJobQueued {
		t.Fatalf("new job status = %q, want queued", job.Status)
	}

	// 3. Re-enqueue same episode → de-duplicated.
	_, skipped2, reason2, err := svc.Enqueue(learnEp, 0, "")
	if err != nil || !skipped2 || reason2 != service.SkipReasonAlreadyQueue {
		t.Fatalf("re-enqueue should skip as already_queued, got skipped=%v reason=%q err=%v", skipped2, reason2, err)
	}

	// 4. Give it a subtitle, then try a fresh (terminal-history) episode →
	//    has_subtitle skip.
	env.giveSubtitle(t, learnEp, "1\n00:00:01,000 --> 00:00:02,000\nhi\n")
	// Mark the queued job terminal so it's no longer active. MarkDone requires
	// the job to be processing (state guard), so claim it first.
	// 2026-07-20:entEp 现在也入了队,ClaimNext 可能拿到 entJob 或 learnEp 的 job
	// (按 created_at 排序)。需要循环 claim 直到拿到 learnEp 的 job,然后 mark done。
	jobRepo := env.newSubtitleJobRepo()
	var claimedID uint
	for i := 0; i < 5; i++ {
		c, cerr := jobRepo.ClaimNext("test-worker")
		if cerr != nil {
			t.Fatalf("claim before mark-done err: %v", cerr)
		}
		if c == nil {
			t.Fatalf("claim before mark-done: ran out of queued jobs, learnEp job id=%d never claimed", job.ID)
		}
		if c.ID == job.ID {
			claimedID = c.ID
			break
		}
		// 拿到的是 entJob,mark done 跳过它,下一轮再 claim。
		if err := jobRepo.MarkDone(c.ID); err != nil {
			t.Fatalf("mark done ent job: %v", err)
		}
	}
	if claimedID != job.ID {
		t.Fatalf("failed to claim learnEp job id=%d (last claimed=%d)", job.ID, claimedID)
	}
	if err := jobRepo.MarkDone(job.ID); err != nil {
		t.Fatal(err)
	}
	_, skipped3, reason3, err := svc.Enqueue(learnEp, 0, "")
	if err != nil || !skipped3 || reason3 != service.SkipReasonHasSubtitle {
		t.Fatalf("episode with subtitle should skip as has_subtitle, got skipped=%v reason=%q err=%v", skipped3, reason3, err)
	}
}

// TestSubtitleJobEnqueueBatchReturnsSkippedReasons verifies the admin bulk
// action surfaces per-id skip reasons so the SPA can show "3 added, 2 skipped".
func TestSubtitleJobEnqueueBatchReturnsSkippedReasons(t *testing.T) {
	env := newTestEnv(t)

	c1 := env.createCourse(t, "课程", "math", nil)
	good1 := env.createEpisode(t, c1, "g1")
	good2 := env.createEpisode(t, c1, "g2")
	entCourse := env.createCourse(t, "娱乐", "chinese", nil)
	env.setContentType(t, entCourse, string(model.ContentEntertainment))
	entEp := env.createEpisode(t, entCourse, "e1")
	alreadySubbed := env.createEpisode(t, c1, "subbed")
	env.giveSubtitle(t, alreadySubbed, "1\n00:00:01,000 --> 00:00:02,000\nx\n")

	svc := env.newSubtitleJobService()
	enqueued, skipped, reasons, err := svc.EnqueueBatch([]uint{good1, good2, entEp, alreadySubbed, 99999}, 0)
	if err != nil {
		t.Fatalf("batch errored: %v", err)
	}
	// 2026-07-20:娱乐课(entEp)现在也入队(放开 AI 链),所以 3 enqueued
	// (good1/good2/entEp) + 2 skipped(alreadySubbed + 99999)。
	if len(enqueued) != 3 || len(skipped) != 2 {
		t.Fatalf("enqueued=%v skipped=%v, want 3 enqueued / 2 skipped", enqueued, skipped)
	}
	// 娱乐课不应该在 skipped 集合里(改回正常入队)。
	for _, id := range skipped {
		if id == entEp {
			t.Errorf("entertainment episode should NOT be skipped after 2026-07-20 refactor, but was in skipped list", )
		}
	}
	if reasons[alreadySubbed] != service.SkipReasonHasSubtitle {
		t.Errorf("subbed episode reason = %q, want %q", reasons[alreadySubbed], service.SkipReasonHasSubtitle)
	}
	if reasons[99999] != service.SkipReasonNotFound {
		t.Errorf("missing episode reason = %q, want %q", reasons[99999], service.SkipReasonNotFound)
	}
}

// TestSubtitleJobCompletePersistsSubtitle verifies that completing a job writes
// the SRT into the subtitles table (the player-consumed table) and marks the
// job done. This is the payoff of the whole pipeline.
func TestSubtitleJobCompletePersistsSubtitle(t *testing.T) {
	env := newTestEnv(t)

	c := env.createCourse(t, "课程", "math", nil)
	ep := env.createEpisode(t, c, "第1集")

	svc := env.newSubtitleJobService()
	job, _, _, err := svc.Enqueue(ep, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	// A worker must claim the job before completing it (Complete is state-
	// guarded: only processing jobs may be completed). We claim at the repo
	// layer to avoid the storage-provider round trip in this test.
	claimed, cerr := env.newSubtitleJobRepo().ClaimNext("test-worker")
	if cerr != nil || claimed == nil || claimed.ID != job.ID {
		t.Fatalf("claim: claimed=%v err=%v", claimed, cerr)
	}
	// The worker's self-id must be stamped on the job (observability).
	if claimed.ClaimedBy != "test-worker" {
		t.Fatalf("claimed_by = %q, want test-worker", claimed.ClaimedBy)
	}

	srt := "1\n00:00:01,000 --> 00:00:02,000\n你好世界\n"
	if err := svc.Complete(job.ID, srt, "", ""); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// The subtitle must be persisted and readable via the episode repo.
	has, err := repository.NewEpisodeRepository(env.db).HasSubtitle(ep)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("Complete did not persist a subtitle row")
	}
	// The job must be done.
	j, _ := env.newSubtitleJobRepo().FindByID(job.ID)
	if j.Status != model.SubtitleJobDone {
		t.Fatalf("job status = %q, want done", j.Status)
	}
}

// TestSubtitleJobReapStale flips a processing job whose claimed_at is old back
// to queued — the safety net for a worker that died mid-transcription.
func TestSubtitleJobReapStale(t *testing.T) {
	env := newTestEnv(t)

	c := env.createCourse(t, "课程", "math", nil)
	ep := env.createEpisode(t, c, "第1集")
	repo := env.newSubtitleJobRepo()
	job := &model.SubtitleJob{EpisodeID: ep, Status: model.SubtitleJobProcessing, Language: "zh-CN"}
	if err := repo.Create(job); err != nil {
		t.Fatal(err)
	}
	// Push claimed_at far into the past by writing it directly (ClaimNext stamps
	// now, so we can't age it via the API).
	old := time.Now().Add(-2 * time.Hour)
	if err := env.db.Model(&model.SubtitleJob{}).Where("id = ?", job.ID).
		Updates(map[string]any{"claimed_at": old}).Error; err != nil {
		t.Fatal(err)
	}

	n, err := repo.ReapStale(30 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1", n)
	}
	got, _ := repo.FindByID(job.ID)
	if got.Status != model.SubtitleJobQueued {
		t.Fatalf("after reap status = %q, want queued", got.Status)
	}
}

// TestSubtitleJobClaimAtomicity proves two concurrent ClaimNext calls cannot
// both win the SAME job. The repo's ClaimNext is a single atomic UPDATE…
// RETURNING, so SQLite serializes the two writes; at most one racer can get a
// non-nil job. We tolerate SQLITE_BUSY errors on the losers (the in-memory
// test DB under GORM's connection pool occasionally surfaces a lock instead of
// a clean "no row" — both are acceptable losers). The property under test is
// "never MORE than one winner", which is what the atomic statement guarantees.
func TestSubtitleJobClaimAtomicity(t *testing.T) {
	env := newTestEnv(t)

	c := env.createCourse(t, "课程", "math", nil)
	ep := env.createEpisode(t, c, "第1集")
	repo := env.newSubtitleJobRepo()
	if err := repo.Create(&model.SubtitleJob{EpisodeID: ep, Status: model.SubtitleJobQueued, Priority: 5, Language: "zh-CN"}); err != nil {
		t.Fatal(err)
	}

	const racers = 8
	var (
		wg      sync.WaitGroup
		winners uint32
		mu      sync.Mutex
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := repo.ClaimNext("test-worker")
			if err != nil {
				// A lock error on a loser is fine — see test comment.
				return
			}
			if got != nil {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if winners > 1 {
		t.Fatalf("%d racers won the single job, want at most 1 (ClaimNext not atomic)", winners)
	}
	if winners == 0 {
		// Every racer hit a lock error: the in-memory DB was uncooperative
		// this run. Verify the claim still works at all via a sequential call,
		// so the test isn't silently a no-op.
		got, err := repo.ClaimNext("test-worker")
		if err != nil {
			t.Fatalf("sequential ClaimNext after all-lock run errored: %v", err)
		}
		if got == nil || got.ID == 0 {
			t.Fatal("sequential ClaimNext returned no job even though one is queued")
		}
	}
}

// TestSubtitleJobClaimDistinctJobsSequentially verifies the property the
// pre-RETURNING ClaimNext bug violated: each claim must return the row IT
// flipped, not "the most recent processing row". We can't reliably drive true
// concurrency against the in-memory test DB (SQLite + GORM pool surfaces lock
// errors instead of clean interleaving), so we claim N distinct jobs one at a
// time and assert each result is distinct and each job is claimed exactly once.
// This still catches a regression to the ambiguous re-SELECT: if ClaimNext
// returned "any processing row" instead of the one it just touched, we'd see a
// duplicate (the same already-claimed job returned again) once more than one
// job is processing.
func TestSubtitleJobClaimDistinctJobsSequentially(t *testing.T) {
	env := newTestEnv(t)

	c := env.createCourse(t, "课程", "math", nil)
	const n = 6
	repo := env.newSubtitleJobRepo()
	jobIDs := make(map[uint]bool, n)
	for i := 0; i < n; i++ {
		ep := env.createEpisode(t, c, "ep"+strconv.Itoa(i))
		j := &model.SubtitleJob{EpisodeID: ep, Status: model.SubtitleJobQueued, Priority: 1, Language: "zh-CN"}
		if err := repo.Create(j); err != nil {
			t.Fatal(err)
		}
		jobIDs[j.ID] = true
	}

	seen := make(map[uint]bool, n)
	for i := 0; i < n; i++ {
		got, err := repo.ClaimNext("test-worker")
		if err != nil {
			t.Fatalf("claim %d errored: %v", i, err)
		}
		if got == nil || got.ID == 0 {
			t.Fatalf("claim %d returned no job, but jobs remain queued", i)
		}
		if seen[got.ID] {
			t.Fatalf("claim %d returned job %d which was already claimed — ClaimNext is returning the wrong row", i, got.ID)
		}
		if !jobIDs[got.ID] {
			t.Fatalf("claim %d returned unknown job %d", i, got.ID)
		}
		seen[got.ID] = true
	}
	// All claimed; a further claim should be empty.
	got, err := repo.ClaimNext("test-worker")
	if err != nil {
		t.Fatalf("drain claim errored: %v", err)
	}
	if got != nil {
		t.Fatalf("after claiming all %d jobs, ClaimNext still returned %+v", n, got)
	}
}

// TestSubtitleJobCompleteRejectsStaleCompletion locks in the reaper/retry race
// guard: if a job has been reaped (or retried) back to queued and re-claimed by
// a second worker, a late Complete from the FIRST worker must be rejected and
// must NOT write a duplicate subtitle. Without this guard the first worker's
// stale SRT would clobber or duplicate the second's.
func TestSubtitleJobCompleteRejectsStaleCompletion(t *testing.T) {
	env := newTestEnv(t)

	c := env.createCourse(t, "课程", "math", nil)
	ep := env.createEpisode(t, c, "第1集")
	svc := env.newSubtitleJobService()
	repo := env.newSubtitleJobRepo()

	job, _, _, err := svc.Enqueue(ep, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	// Worker A claims.
	a, _ := repo.ClaimNext("test-worker")
	if a == nil || a.ID != job.ID {
		t.Fatal("worker A did not claim the job")
	}

	// Simulate the reaper (or admin retry) moving the job back to queued, then
	// worker B claiming + completing it.
	if n, _ := repo.ReapStale(0); n != 1 { // staleAfter=0 → reaps immediately
		t.Fatalf("expected reaper to move the job back, reaped %d", n)
	}
	b, _ := repo.ClaimNext("test-worker")
	if b == nil || b.ID != job.ID {
		t.Fatal("worker B did not re-claim the reaped job")
	}
	if err := svc.Complete(job.ID, "1\n00:00:01,000 --> 00:00:02,000\nworker B\n", "", ""); err != nil {
		t.Fatalf("worker B complete should succeed: %v", err)
	}

	// Now worker A's late SRT arrives — it MUST be rejected (the job is no
	// longer processing for A; B already completed it).
	err = svc.Complete(job.ID, "1\n00:00:01,000 --> 00:00:02,000\nworker A LATE\n", "", "")
	if !errors.Is(err, service.ErrSubtitleJobStaleComplete) {
		t.Fatalf("late complete should return ErrSubtitleJobStaleComplete, got %v", err)
	}

	// And there must be exactly ONE subtitle (worker B's), not two.
	subs, err := repository.NewEpisodeRepository(env.db).ListSubtitles(ep)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected exactly 1 subtitle (worker B's), got %d", len(subs))
	}
	if !strings.Contains(subs[0].VttContent, "worker B") {
		t.Fatalf("persisted subtitle is not worker B's: %q", subs[0].VttContent)
	}
}

// TestSubtitleJobClaimPriority: higher priority is claimed first, then oldest.
func TestSubtitleJobClaimPriority(t *testing.T) {
	env := newTestEnv(t)

	c := env.createCourse(t, "课程", "math", nil)
	repo := env.newSubtitleJobRepo()
	low := env.createEpisode(t, c, "low")
	high := env.createEpisode(t, c, "high")
	if err := repo.Create(&model.SubtitleJob{EpisodeID: low, Status: model.SubtitleJobQueued, Priority: 0, Language: "zh-CN"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // ensure created_at ordering
	if err := repo.Create(&model.SubtitleJob{EpisodeID: high, Status: model.SubtitleJobQueued, Priority: 9, Language: "zh-CN"}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ClaimNext("test-worker")
	if err != nil {
		t.Fatal(err)
	}
	if got.EpisodeID != high {
		t.Fatalf("first claim = episode %d, want the high-priority %d", got.EpisodeID, high)
	}
}

// TestSubtitleJobAdminHTTPEndpoints covers the admin-facing HTTP surface:
// enqueue returns enqueued/skipped/reasons, list returns the joined rows,
// stats returns counters, and skip/retry transition a failed job.
func TestSubtitleJobAdminHTTPEndpoints(t *testing.T) {
	env := newTestEnv(t)

	c := env.createCourse(t, "课程", "math", nil)
	ep := env.createEpisode(t, c, "第1集")
	entCourse := env.createCourse(t, "娱乐", "chinese", nil)
	env.setContentType(t, entCourse, string(model.ContentEntertainment))
	entEp := env.createEpisode(t, entCourse, "e1")

	// Enqueue via the admin HTTP endpoint.
	resp := env.do(t, http.MethodPost, "/admin/api/subtitle-jobs", map[string]any{
		"episode_ids": []uint{ep, entEp},
		"priority":    3,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("enqueue: %d %s", resp.Code, resp.Body.String())
	}
	var enq struct {
		Enqueued []uint          `json:"enqueued"`
		Skipped  []uint          `json:"skipped"`
		Reasons  map[uint]string `json:"reasons"`
	}
	json.Unmarshal(resp.Body.Bytes(), &enq)
	// 2026-07-20:娱乐课(entEp)也入队 → 2 enqueued (ep + entEp), 0 skipped。
	if len(enq.Enqueued) != 2 {
		t.Fatalf("enqueued = %v, want 2 (learning ep + entertainment ep)", enq.Enqueued)
	}
	for _, id := range enq.Enqueued {
		if id != ep && id != entEp {
			t.Fatalf("unexpected enqueued id %d (want %d or %d)", id, ep, entEp)
		}
	}
	if len(enq.Skipped) != 0 {
		t.Fatalf("skipped = %v reasons = %v, want empty (entertainment no longer skipped)", enq.Skipped, enq.Reasons)
	}

	// List shows the queued job with the joined episode title.
	resp = env.do(t, http.MethodGet, "/admin/api/subtitle-jobs", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list: %d %s", resp.Code, resp.Body.String())
	}
	var list []struct {
		ID           uint   `json:"id"`
		EpisodeID    uint   `json:"episode_id"`
		EpisodeTitle string `json:"episode_title"`
		Status       string `json:"status"`
		Priority     int    `json:"priority"`
	}
	json.Unmarshal(resp.Body.Bytes(), &list)
	found := false
	for _, j := range list {
		if j.EpisodeID == ep && j.Status == model.SubtitleJobQueued && j.Priority == 3 && j.EpisodeTitle == "第1集" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list did not contain the queued job with joined title: %s", resp.Body.String())
	}

	// Stats reflects queued count. 2026-07-20:娱乐课也入队,所以 Queued=2
	// (learnEp + entEp)。
	resp = env.do(t, http.MethodGet, "/admin/api/subtitle-jobs/stats", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", resp.Code, resp.Body.String())
	}
	var stats service.SubtitleJobStats
	json.Unmarshal(resp.Body.Bytes(), &stats)
	if stats.Queued != 2 {
		t.Fatalf("stats.Queued = %d, want 2 (learnEp + entEp, body %s)", stats.Queued, resp.Body.String())
	}

	// Find the job id, fail it via repo, then retry via HTTP → back to queued.
	job, _ := env.newSubtitleJobRepo().FindActiveByEpisode(ep)
	if job == nil {
		t.Fatal("active job not found")
	}
	env.newSubtitleJobRepo().MarkFailed(job.ID, "boom")

	// retry failed → queued
	resp = env.do(t, http.MethodPost, "/admin/api/subtitle-jobs/"+strconv.FormatUint(uint64(job.ID), 10)+"/retry", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", resp.Code, resp.Body.String())
	}
	j, _ := env.newSubtitleJobRepo().FindByID(job.ID)
	if j.Status != model.SubtitleJobQueued {
		t.Fatalf("after retry status = %q, want queued", j.Status)
	}

	// skip → skipped (terminal)
	resp = env.do(t, http.MethodPost, "/admin/api/subtitle-jobs/"+strconv.FormatUint(uint64(job.ID), 10)+"/skip", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("skip: %d %s", resp.Code, resp.Body.String())
	}
	j, _ = env.newSubtitleJobRepo().FindByID(job.ID)
	if j.Status != model.SubtitleJobSkipped {
		t.Fatalf("after skip status = %q, want skipped", j.Status)
	}
}

// TestSubtitleJobWorkerClaimRequiresIngestKey: the worker claim endpoint is
// gated by X-Ingest-Key (no key → 401 when the key is configured). We exercise
// this against a minimal engine that mounts the real subtitle-job handler
// behind the real IngestKeyMiddleware, rather than spinning up the full env —
// the point under test is that the subtitle worker rides the same gate as the
// ingest toolchain, not the full server.
func TestSubtitleJobWorkerClaimRequiresIngestKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Build the real handler against a throwaway in-memory DB.
	dbName := fmt.Sprintf("file:test_ingestkey_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	episodeRepo := repository.NewEpisodeRepository(db)
	subtitleRepo := repository.NewSubtitleJobRepository(db)
	resolver := service.NewStorageProviderResolver(repository.NewStorageSourceRepository(db))
	epSvc := service.NewEpisodeService(episodeRepo, resolver)
	svc := service.NewSubtitleJobService(subtitleRepo, episodeRepo, epSvc,
		repository.NewCourseRepository(db), repository.NewChapterRepository(db), repository.NewSubjectRepository(db))
	h := handler.NewSubtitleJobHandler(svc)

	const key = "secret-key-123"
	r := gin.New()
	g := r.Group("/api/v1").Use(middleware.IngestKeyMiddleware(key))
	g.POST("/subtitle-jobs/claim", h.Claim)

	// No key → rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subtitle-jobs/claim", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("claim without key: got %d, want 401", w.Code)
	}

	// With key → passes the gate (returns job:null since queue is empty).
	req = httptest.NewRequest(http.MethodPost, "/api/v1/subtitle-jobs/claim", nil)
	req.Header.Set("X-Ingest-Key", key)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("claim with key: got %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var body struct {
		Job any `json:"job"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Job != nil {
		t.Fatalf("empty queue should return job:null, got %v", body.Job)
	}
}

// ---- helpers used only by this file ----

func (e *testEnv) setContentType(t *testing.T, courseID uint, ct string) {
	t.Helper()
	if err := e.db.Model(&model.Course{}).Where("id = ?", courseID).Update("content_type", ct).Error; err != nil {
		t.Fatalf("set content_type: %v", err)
	}
}

func (e *testEnv) giveSubtitle(t *testing.T, episodeID uint, srt string) {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/admin/api/episodes/"+strconv.FormatUint(uint64(episodeID), 10)+"/subtitles", map[string]any{
		"language":    "zh-CN",
		"label":       "中文",
		"srt_content": srt,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("give subtitle: %d %s", resp.Code, resp.Body.String())
	}
}

// TestSubtitleJobClaimResponseContext verifies ClaimNext enriches the claim
// payload with the cache-matching keys (filename + file_size) and the Whisper
// prompt context (subject / course_title / chapter_title / whisper_hint).
//
// It uses a webdav storage source on purpose: WebDAVProvider.GetDownloadURL is
// pure URL construction (no network), so the storage round-trip inside
// ClaimNext succeeds offline and the post-download enrichment runs.
func TestSubtitleJobClaimResponseContext(t *testing.T) {
	dbName := fmt.Sprintf("file:test_claim_ctx_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Seed subjects so SubjectID resolves to a key.
	if err := service.NewSubjectService(db, repository.NewSubjectRepository(db), repository.NewBadgeRepository(db), service.NewBadgeService(db, repository.NewBadgeRepository(db), repository.NewProgressRepository(db))).SeedDefaultSubjects(); err != nil {
		t.Fatalf("seed subjects: %v", err)
	}

	// A webdav source: GetDownloadURL builds a URL offline, so ClaimNext's
	// storage resolution succeeds without a live server.
	src := model.StorageSource{Name: "test-webdav", Type: "webdav", URL: "http://test-dav/", IsDefault: true}
	if err := db.Create(&src).Error; err != nil {
		t.Fatalf("create source: %v", err)
	}

	// Course with an AI hint, under the "math" subject.
	var mathSubject model.Subject
	db.Where("key = ?", "math").First(&mathSubject)
	course := model.Course{Title: "高等数学", SubjectID: mathSubject.ID, ContentType: model.ContentLearning}
	course.SetAIConfig(model.AIConfig{WhisperHint: "重点听极限的 ε-δ 定义，老师口音较重"})
	if err := db.Create(&course).Error; err != nil {
		t.Fatalf("create course: %v", err)
	}
	// A chapter so the chapter_title path is exercised.
	chapter := model.Chapter{CourseID: course.ID, Title: "第一章 极限与连续", SortOrder: 1}
	if err := db.Create(&chapter).Error; err != nil {
		t.Fatalf("create chapter: %v", err)
	}

	// Episode with an original path (so basename prefers it), a file size, the
	// chapter link, and the webdav source so GetStreamURL resolves.
	size := int64(524288000)
	ep := model.Episode{
		CourseID:             course.ID,
		ChapterID:            &chapter.ID,
		Title:                "连续性的定义",
		VideoRelativePath:    "/课程/连续性的定义.mp4",
		OriginalRelativePath: "/课程/orig/连续性的定义.mp4",
		FileSize:             &size,
		SourceID:             &src.ID,
		SortOrder:            1,
	}
	if err := db.Create(&ep).Error; err != nil {
		t.Fatalf("create episode: %v", err)
	}

	episodeRepo := repository.NewEpisodeRepository(db)
	subtitleRepo := repository.NewSubtitleJobRepository(db)
	resolver := service.NewStorageProviderResolver(repository.NewStorageSourceRepository(db))
	epSvc := service.NewEpisodeService(episodeRepo, resolver)
	svc := service.NewSubtitleJobService(subtitleRepo, episodeRepo, epSvc,
		repository.NewCourseRepository(db), repository.NewChapterRepository(db), repository.NewSubjectRepository(db))

	if _, _, _, err := svc.Enqueue(ep.ID, 0, ""); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	got, err := svc.ClaimNext("desktop-4060", "test-ua")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if got == nil {
		t.Fatal("ClaimNext returned no job")
	}

	// Cache-matching keys.
	if got.Filename != "连续性的定义.mp4" {
		t.Errorf("Filename = %q, want 连续性的定义.mp4 (basename of OriginalRelativePath)", got.Filename)
	}
	if got.FileSize == nil || *got.FileSize != size {
		t.Errorf("FileSize = %v, want %d", got.FileSize, size)
	}
	// Whisper prompt context.
	if got.Subject != "math" {
		t.Errorf("Subject = %q, want math", got.Subject)
	}
	if got.CourseTitle != "高等数学" {
		t.Errorf("CourseTitle = %q, want 高等数学", got.CourseTitle)
	}
	if got.ChapterTitle != "第一章 极限与连续" {
		t.Errorf("ChapterTitle = %q, want 第一章 极限与连续", got.ChapterTitle)
	}
	// Whisper prompt context: ClaimResult.WhisperHint carries the value from
	// Course.EffectiveWhisperHint(subject) (sourced from AIConfigJSON, falling back
	// to the deprecated AIHint column for un-migrated rows). The worker reads
	// only whisper_hint now (legacy ai_hint protocol field was removed when
	// the worker was upgraded in lockstep).
	wantHint := course.EffectiveWhisperHint(mathSubject)
	if got.WhisperHint != wantHint {
		t.Errorf("WhisperHint = %q, want %q", got.WhisperHint, wantHint)
	}
	if got.EpisodeTitle != "连续性的定义" {
		t.Errorf("EpisodeTitle = %q", got.EpisodeTitle)
	}
}

// TestSubtitleJobHeartbeatProgress verifies a heartbeat can record the worker's
// transcription ratio and that a terminal transition (Complete) clears it, so a
// requeued/done job never shows a stale percentage.
func TestSubtitleJobHeartbeatProgress(t *testing.T) {
	env := newTestEnv(t)

	c := env.createCourse(t, "课程", "math", nil)
	ep := env.createEpisode(t, c, "第1集")
	svc := env.newSubtitleJobService()
	repo := env.newSubtitleJobRepo()

	if _, _, _, err := svc.Enqueue(ep, 0, ""); err != nil {
		t.Fatal(err)
	}
	// Claim at the repo layer to reach 'processing' without a storage round-trip.
	if _, err := repo.ClaimNext("test-worker"); err != nil {
		t.Fatal(err)
	}
	job, _ := repo.FindActiveByEpisode(ep)
	if job == nil {
		t.Fatal("active job not found")
	}

	// A heartbeat carrying a ratio must persist it.
	ratio := 0.42
	if err := svc.Heartbeat(job.ID, &ratio); err != nil {
		t.Fatalf("heartbeat with progress: %v", err)
	}
	j, _ := repo.FindByID(job.ID)
	if j.Progress == nil || *j.Progress != ratio {
		t.Fatalf("progress = %v, want %v", j.Progress, ratio)
	}

	// A heartbeat with nil ratio refreshes claimed_at but must not wipe progress.
	if err := svc.Heartbeat(job.ID, nil); err != nil {
		t.Fatalf("heartbeat without progress: %v", err)
	}
	j, _ = repo.FindByID(job.ID)
	if j.Progress == nil || *j.Progress != ratio {
		t.Fatalf("progress after nil-ratio heartbeat = %v, want unchanged %v", j.Progress, ratio)
	}

	// Completing clears progress (terminal state must not show a stale %).
	if err := svc.Complete(job.ID, "1\n00:00:01,000 --> 00:00:02,000\nx\n", "", ""); err != nil {
		t.Fatalf("complete: %v", err)
	}
	j, _ = repo.FindByID(job.ID)
	if j.Progress != nil {
		t.Fatalf("progress after complete = %v, want nil", j.Progress)
	}
}

// TestSubtitleVTTEndpoint is a regression test for the Gin route
// /subtitles/:id.vtt. Gin does NOT split the param name on ".", so the
// registered param is "id.vtt" (not "id"), and c.Param("id") returns "". The
// handler must read c.Param("id.vtt") and strip ".vtt" — otherwise every VTT
// request 400s with "invalid subtitle ID format" and the player shows no
// subtitles even though the SRT is in the DB.
func TestSubtitleVTTEndpoint(t *testing.T) {
	env := newTestEnv(t)
	c := env.createCourse(t, "课程", "math", nil)
	ep := env.createEpisode(t, c, "第1集")

	srt := "1\n00:00:01,000 --> 00:00:02,000\n你好世界\n"
	env.giveSubtitle(t, ep, srt)

	// Find the subtitle id via the admin list endpoint.
	resp := env.do(t, http.MethodGet, "/admin/api/episodes/"+strconv.FormatUint(uint64(ep), 10)+"/subtitles", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list subtitles: %d %s", resp.Code, resp.Body.String())
	}
	var subs []struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(resp.Body.Bytes(), &subs)
	if len(subs) != 1 {
		t.Fatalf("expected 1 subtitle, got %d", len(subs))
	}
	subID := subs[0].ID

	// GET the VTT — this is the endpoint libmpv hits. Must return 200 + VTT text.
	vttURL := "/api/v1/subtitles/" + strconv.FormatUint(uint64(subID), 10) + ".vtt"
	resp = env.do(t, http.MethodGet, vttURL, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("VTT endpoint returned %d (body: %s) — the :id.vtt param parsing is broken", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.HasPrefix(body, "WEBVTT") {
		t.Errorf("VTT body should start with WEBVTT, got: %q", body[:min(40, len(body))])
	}
	// The SRT timestamp comma must be converted to a VTT dot.
	if strings.Contains(body, "00:00:01,000") {
		t.Errorf("VTT still contains SRT-style comma timestamp: %q", body)
	}
	if !strings.Contains(body, "00:00:01.000") {
		t.Errorf("VTT missing dot-style timestamp: %q", body)
	}
	if !strings.Contains(body, "你好世界") {
		t.Errorf("VTT missing subtitle text: %q", body)
	}
}
