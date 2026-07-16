package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// AIService is the facade for the AI subsystem's business logic: enqueueing and
// running jobs (segment / summary), and reading results (summaries, jobs, runs
// for the admin observability page). It owns the in-process job worker.
//
// It is the ONLY place that ties together the provider resolver (ai.ProviderResolver),
// the segmenter, the embedder, the summarizer, and the content repo — keeping
// that wiring out of the handlers (which stay thin) and out of the agent package
// (which stays focused on decision logic). Handlers depend on this interface.
//
// Nil-safe semantics: when AI isn't wired (no resolver / no providers), the
// enqueue methods still record jobs (so the admin sees what WOULD have run) but
// processing reports "AI not configured" and marks the job skipped rather than
// crashing. This is the "add-on layer" guarantee in action.
type AIService interface {
	// ── job enqueue (admin-triggered) ──
	EnqueueSegment(episodeIDs []uint) (enqueued []uint, skipped map[uint]string, err error)
	EnqueueSummary(episodeIDs []uint) (enqueued []uint, skipped map[uint]string, err error)

	// ── subtitle completion hook ──
	// OnSubtitleCompleted is called by the subtitle job service when a transcript
	// lands. It enqueues a segment job so the new transcript is chunked+embedded
	// automatically, but ONLY if the episode's course has AI enabled. This is the
	// seam that connects Step 2 (subtitles) to Step 3 (AI) without either knowing
	// the other's internals.
	OnSubtitleCompleted(episodeID uint)

	// ── quiz: client-driven lazy generation + answering (Phase C) ──
	// GetOrEnqueueQuiz is the lazy-generation entry point. The client GETs the
	// quiz; if none exists yet, one is enqueued for this user and the status is
	// "generating" (client polls). Returns "ready" with the existing quiz, or
	// "unavailable" when AI/quiz is off or the episode isn't ready (no chunks).
	GetOrEnqueueQuiz(userID, episodeID uint) (status string, quiz *model.Quiz, err error)
	// GetQuizForClient returns the quiz + questions in a client-safe shape: the
	// question list NEVER exposes the correct answer/answer_text (that's revealed
	// only after submit, via SubmitQuizAnswer). Includes per-question answered
	// state so the client can show progress on a redo.
	GetQuizForClient(userID, episodeID uint) (*QuizView, error)
	// SubmitQuizAnswer grades one answer, persists it, updates memory, and
	// returns the verdict + explanation + jump-to-video timestamp. answerIndex is
	// used for choice, answerText for fill (exactly one is set per question type).
	SubmitQuizAnswer(userID, questionID uint, answerIndex *int, answerText *string) (*AnswerResult, error)
	// RegenerateQuiz drops the user's current quiz and re-runs the agent against
	// their latest memory (换题). Returns the new status ("generating").
	RegenerateQuiz(userID, episodeID uint) (status string, err error)

	// ── quiz: admin observability (Phase C) ──
	ListQuizzesForUser(userID uint) ([]QuizDetailQuiz, error)
	GetQuizDetail(quizID uint) (*QuizDetail, error)

	// ── results (read) ──
	GetSummary(episodeID uint) (*model.AISummary, error)
	ListJobs(jobType, status string, limit int) ([]model.AIJob, error)
	GetJob(id uint) (*model.AIJob, error)
	ListRunsForJob(jobID uint) ([]model.AIRun, error)
	ListRecentRuns(limit int) ([]model.AIRun, error)
	GetRun(id uint) (*model.AIRun, error)
	JobStats() (map[string]int, error)
}

type aiService struct {
	db            *gorm.DB
	contentRepo   repository.AIContentRepository
	episodeRepo   repository.EpisodeRepository
	courseRepo    repository.CourseRepository
	subtitleRepo  repository.EpisodeRepository // same repo, GetSubtitle lives here
	resolver      *ai.ProviderResolver
	unlockService UnlockService // gates client quiz access (IsEpisodeVisible); nil in tests
}

// NewAIService constructs an AIService. resolver may be nil in degenerate
// builds (AI disabled); the service degrades gracefully. unlockService gates
// client-facing quiz access (IsEpisodeVisible); the existing unlock service
// satisfies this interface.
func NewAIService(
	db *gorm.DB,
	contentRepo repository.AIContentRepository,
	episodeRepo repository.EpisodeRepository,
	courseRepo repository.CourseRepository,
	resolver *ai.ProviderResolver,
	unlockService UnlockService,
) AIService {
	s := &aiService{
		db:            db,
		contentRepo:   contentRepo,
		episodeRepo:   episodeRepo,
		courseRepo:    courseRepo,
		resolver:      resolver,
		unlockService: unlockService,
	}
	go s.runWorker() // single in-process worker goroutine; see runWorker
	return s
}

// --- job enqueue ---

func (s *aiService) EnqueueSegment(episodeIDs []uint) ([]uint, map[uint]string, error) {
	return s.enqueue(episodeIDs, "segment")
}

func (s *aiService) EnqueueSummary(episodeIDs []uint) ([]uint, map[uint]string, error) {
	return s.enqueue(episodeIDs, "summary")
}

// enqueue creates one job per episode. It resolves each episode's course_id and
// skips episodes that can't be resolved (deleted / missing). Returns the list of
// enqueued ids and a per-episode skip-reason map (not an error — partial success
// is normal when bulk-enqueuing from the course tree).
func (s *aiService) enqueue(episodeIDs []uint, jobType string) ([]uint, map[uint]string, error) {
	enqueued := make([]uint, 0, len(episodeIDs))
	skipped := make(map[uint]string)
	for _, epID := range episodeIDs {
		ep, err := s.episodeRepo.FindByID(epID)
		if err != nil {
			skipped[epID] = "查询课时失败: " + err.Error()
			continue
		}
		if ep == nil {
			skipped[epID] = "课时不存在"
			continue
		}
		job := &model.AIJob{
			JobType:   jobType,
			EpisodeID: epID,
			CourseID:  ep.CourseID,
			Status:    "queued",
		}
		if err := s.contentRepo.CreateJob(job); err != nil {
			skipped[epID] = "入队失败: " + err.Error()
			continue
		}
		enqueued = append(enqueued, epID)
	}
	return enqueued, skipped, nil
}

// --- subtitle completion hook ---

func (s *aiService) OnSubtitleCompleted(episodeID uint) {
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil || ep == nil {
		return // can't do anything without the episode
	}
	// Only auto-segment if the course has AI enabled. This is the gate that keeps
	// AI a pure add-on: a course with AI off never triggers AI work, even when
	// subtitles arrive.
	course, err := s.courseRepo.FindByID(ep.CourseID)
	if err != nil || course == nil {
		return
	}
	if !course.AISummaryEnabled && !course.AIQuizEnabled {
		return // AI off for this course — leave it as a plain subtitled video.
	}
	job := &model.AIJob{
		JobType:   "segment",
		EpisodeID: episodeID,
		CourseID:  ep.CourseID,
		Status:    "queued",
	}
	if err := s.contentRepo.CreateJob(job); err != nil {
		log.Printf("AI: failed to enqueue segment job for episode %d: %v", episodeID, err)
	}
}

// --- in-process worker ---

// runWorker is the single goroutine that drains AI jobs. It polls every few
// seconds for queued segment/summary jobs and processes them sequentially.
// Sequential is fine for MVP (jobs are LLM-bound, ~5-30s each; parallelism would
// just rate-limit at the provider). The poll interval is a tradeoff: short =
// jobs start quickly, long = less DB churn when idle.
//
// It runs for the process lifetime; there's no shutdown handshake beyond the
// process exiting (an in-flight LLM call may be interrupted, which is fine —
// the job stays "processing" and a future reaper would reset it; for now, a
// killed job is just lost, acceptable for a generation task that the admin can
// re-trigger).
func (s *aiService) runWorker() {
	jobTypes := []string{"segment", "summary", "quiz"}
	for {
		s.processOneJob(jobTypes)
		// Poll every 3s. A real impl might use a channel signaled on enqueue,
		// but a poll is simpler and the 3s latency is invisible to the admin
		// (jobs take 5-30s to run anyway).
		sleep(3)
	}
}

// processOneJob claims and runs at most one queued job. Split from runWorker for
// testability (a test can drive single jobs without the poll loop).
func (s *aiService) processOneJob(jobTypes []string) {
	job, err := s.contentRepo.ClaimNextQueuedJob(jobTypes)
	if err != nil {
		log.Printf("AI worker: claim error: %v", err)
		return
	}
	if job == nil {
		return // nothing queued
	}
	switch job.JobType {
	case "segment":
		s.runSegmentJob(job)
	case "summary":
		s.runSummaryJob(job)
	case "quiz":
		s.runQuizJob(job)
	default:
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "unknown job_type: "+job.JobType, nil)
	}
}

// runSegmentJob segments an episode's subtitle into chunks and embeds them.
// Steps: load subtitle → parse SRT → segment → embed each chunk → persist.
// On any failure the job is marked failed with the error message (visible in the
// admin UI) so the operator can diagnose without server logs.
func (s *aiService) runSegmentJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "AI not configured (no resolver)", nil)
		return
	}
	// 1. Load the episode's subtitle.
	sub, err := s.episodeRepo.GetSubtitle(job.EpisodeID)
	if err != nil {
		s.failJob(job, "load subtitle: "+err.Error())
		return
	}
	if sub == nil {
		s.failJob(job, "no subtitle for this episode")
		return
	}
	// 2. Parse + segment.
	cues, err := ai.ParseSRT(sub.SrtContent)
	if err != nil {
		s.failJob(job, "parse SRT: "+err.Error())
		return
	}
	drafts := ai.SegmentChunks(cues, ai.DefaultSegmentConfig())
	if len(drafts) == 0 {
		s.failJob(job, "segmentation produced no chunks")
		return
	}
	// 3. Embed each chunk's text. Batched for efficiency (one embedder call per
	// chunk would be wasteful whether local or API).
	embedder, err := s.resolver.ResolveEmbedder()
	if err != nil {
		s.failJob(job, "resolve embedder: "+err.Error())
		return
	}
	texts := make([]string, len(drafts))
	for i, d := range drafts {
		texts[i] = d.Text
	}
	vectors, err := embedder.Embed(ctx, texts)
	if err != nil {
		s.failJob(job, "embed chunks: "+err.Error())
		return
	}
	// 4. Persist. Convert drafts + vectors into ContentChunk rows.
	chunks := make([]model.ContentChunk, len(drafts))
	for i, d := range drafts {
		embJSON, _ := json.Marshal(vectors[i])
		chunks[i] = model.ContentChunk{
			ChunkIndex: d.ChunkIndex,
			StartTime:  &d.StartTime,
			EndTime:    &d.EndTime,
			Text:       d.Text,
			Embedding:  string(embJSON),
			SourceRef:  fmt.Sprintf("%d", sub.ID),
		}
	}
	if err := s.contentRepo.ReplaceChunksForEpisode(job.EpisodeID, job.CourseID, "subtitle", chunks); err != nil {
		s.failJob(job, "persist chunks: "+err.Error())
		return
	}
	s.contentRepo.UpdateJobStatus(job.ID, "done", "", nil)
}

// runSummaryJob reads an episode's chunks and asks the summarizer to summarize.
// Requires chunks to exist (a segment job must have run first). If none exist,
// the job is marked failed with a clear message rather than silently producing
// an empty summary.
func (s *aiService) runSummaryJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "AI not configured (no resolver)", nil)
		return
	}
	// Chunks must exist. If they don't, the operator likely needs to run
	// segmentation first; surface that clearly.
	chunks, err := s.contentRepo.ListChunks(job.EpisodeID, "subtitle")
	if err != nil {
		s.failJob(job, "load chunks: "+err.Error())
		return
	}
	if len(chunks) == 0 {
		s.failJob(job, "no content chunks — run segmentation first")
		return
	}
	// Build course context for the prompt.
	course, _ := s.courseRepo.FindByID(job.CourseID)
	hint := ""
	subject := ""
	if course != nil {
		hint = course.AIHint
		if course.Subject.Label != "" {
			subject = course.Subject.Label
		}
	}
	llm, err := s.resolver.ResolveChat()
	if err != nil {
		s.failJob(job, "resolve chat provider: "+err.Error())
		return
	}
	modelName := s.resolver.ChatModelName()
	summarizer := agent.NewSummarizer(llm, s.contentRepo, modelName)
	_, err = summarizer.Summarize(ctx, agent.SummarizerRequest{
		EpisodeID:  job.EpisodeID,
		CourseID:   job.CourseID,
		CourseHint: hint,
		Subject:    subject,
		Chunks:     chunks,
	}, job.ID)
	if err != nil {
		s.failJob(job, err.Error())
		return
	}
	s.contentRepo.UpdateJobStatus(job.ID, "done", "", nil)
}

// failJob marks a job failed with an error message and logs it. Centralized so
// every failure path records consistently (the admin UI shows the error string).
func (s *aiService) failJob(job *model.AIJob, msg string) {
	log.Printf("AI job %d (%s) episode %d failed: %s", job.ID, job.JobType, job.EpisodeID, msg)
	s.contentRepo.UpdateJobStatus(job.ID, "failed", msg, nil)
}

// --- reads ---

func (s *aiService) GetSummary(episodeID uint) (*model.AISummary, error) {
	return s.contentRepo.GetSummary(episodeID)
}

func (s *aiService) ListJobs(jobType, status string, limit int) ([]model.AIJob, error) {
	return s.contentRepo.ListJobs(jobType, status, limit)
}

func (s *aiService) GetJob(id uint) (*model.AIJob, error) {
	return s.contentRepo.GetJob(id)
}

func (s *aiService) ListRunsForJob(jobID uint) ([]model.AIRun, error) {
	return s.contentRepo.ListRunsForJob(jobID)
}

func (s *aiService) ListRecentRuns(limit int) ([]model.AIRun, error) {
	return s.contentRepo.ListRecentRuns(limit)
}

func (s *aiService) GetRun(id uint) (*model.AIRun, error) {
	return s.contentRepo.GetRun(id)
}

func (s *aiService) JobStats() (map[string]int, error) {
	return s.contentRepo.JobStats()
}

// sleep is a thin wrapper around time.Sleep used by the worker poll loop. Kept
// as a helper so it's swappable in tests (a test can replace it with a no-op or
// a channel signal).
var sleep = func(seconds int) { time.Sleep(time.Duration(seconds) * time.Second) }
