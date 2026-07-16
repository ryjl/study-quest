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
	// EnqueueSegmentForCourse enqueues segment jobs for every episode of a
	// course that already has a subtitle (the agent needs source material). Used
	// when an admin flips a course's AI switch from off→on: previously-arrived
	// subtitles never triggered AI work (OnSubtitleCompleted early-returns when AI
	// is off), so we batch-backfill here. Returns the number of jobs enqueued.
	EnqueueSegmentForCourse(courseID uint) (int, error)

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
	// ListQuizHistory returns a user's archived (superseded) quizzes for an
	// episode as fully read-only views (correct answers revealed, no submit
	// path). Powers the Phase 3 history panel. Newest-archive first.
	ListQuizHistory(userID, episodeID uint) ([]QuizHistoryView, error)

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

	// ── maintenance ──
	// ReapStaleJobs resets 'processing' AI jobs whose claimed_at is older than a
	// fixed threshold back to 'queued', so a worker that crashed mid-LLM-call
	// doesn't leave a job stuck forever. Mirrors the subtitle reaper; the AI
	// worker is in-process so this is mainly insurance against a hard kill.
	ReapStaleJobs() (int64, error)
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
	// segment 优先级 2:高于 summary(派生数据,可慢慢来),低于 quiz(学生正在等)。
	return s.enqueue(episodeIDs, "segment", prioritySegment)
}

func (s *aiService) EnqueueSummary(episodeIDs []uint) ([]uint, map[uint]string, error) {
	// summary 优先级 1:它是 segment 的下游产物,不阻塞任何用户交互,最低即可。
	return s.enqueue(episodeIDs, "summary", prioritySummary)
}

// 作业优先级(高 = ClaimNextQueuedJob 先捞)。设计意图:
//   - quiz(10):学生正盯着屏幕等出题,响应延迟最刺眼,必须最先跑。
//   - segment(2):summary/quiz 的上游,但属于后台批量,不需要抢在 quiz 前。
//   - summary(1):纯派生展示数据,无人在等,放到最低。
const (
	priorityQuiz    = 10
	prioritySegment = 2
	prioritySummary = 1
)

// enqueue creates one job per episode. It resolves each episode's course_id and
// skips episodes that can't be resolved (deleted / missing). Returns the list of
// enqueued ids and a per-episode skip-reason map (not an error — partial success
// is normal when bulk-enqueuing from the course tree).
func (s *aiService) enqueue(episodeIDs []uint, jobType string, priority int) ([]uint, map[uint]string, error) {
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
			Priority:  priority,
		}
		if err := s.contentRepo.CreateJob(job); err != nil {
			skipped[epID] = "入队失败: " + err.Error()
			continue
		}
		enqueued = append(enqueued, epID)
	}
	return enqueued, skipped, nil
}

// EnqueueSegmentForCourse 批量给一个课程下所有"已有字幕"的 episode 入队
// segment job。用于 admin 把 AI 开关从 off→on 的回填场景:开关关着时到达的
// 字幕在 OnSubtitleCompleted 里被早返回了,不会产生任何 AI 工作,所以历史
// 字幕需要在这个时机补一次。只挑有字幕的 episode,因为 segment job 没有
// 字幕会直接失败(无源材料)。返回实际入队的作业数。
func (s *aiService) EnqueueSegmentForCourse(courseID uint) (int, error) {
	episodes, err := s.episodeRepo.ListByCourse(courseID)
	if err != nil {
		return 0, err
	}
	if len(episodes) == 0 {
		return 0, nil
	}
	// 收集 episode id,批量查"是否有字幕",避免逐条 N+1。
	ids := make([]uint, 0, len(episodes))
	for _, ep := range episodes {
		ids = append(ids, ep.ID)
	}
	subCounts, err := s.episodeRepo.CountSubtitlesByEpisodes(ids)
	if err != nil {
		return 0, err
	}
	targetIDs := make([]uint, 0, len(ids))
	for _, id := range ids {
		if subCounts[id] > 0 {
			targetIDs = append(targetIDs, id)
		}
	}
	if len(targetIDs) == 0 {
		return 0, nil
	}
	enqueued, _, err := s.EnqueueSegment(targetIDs)
	if err != nil {
		return 0, err
	}
	return len(enqueued), nil
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
		Priority:  prioritySegment,
	}
	if err := s.contentRepo.CreateJob(job); err != nil {
		log.Printf("AI: failed to enqueue segment job for episode %d: %v", episodeID, err)
	}
}

// hasPendingJob reports whether a queued/processing job of jobType exists for
// the episode. The generic sibling of hasPendingQuizJob: used to suppress
// duplicate auto-enqueue (e.g. the summary chain firing while a prior summary
// job is still in flight). done/failed/skipped jobs don't count — those are
// finished states and a new trigger means we genuinely want a fresh attempt.
func (s *aiService) hasPendingJob(jobType string, episodeID uint) bool {
	var count int64
	s.db.Model(&model.AIJob{}).
		Where("job_type = ? AND episode_id = ? AND status IN ?", jobType, episodeID, []string{"queued", "processing"}).
		Count(&count)
	return count > 0
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

	// 链式触发 summary:segment 是 summary 的上游,既然刚把源材料(chunk)写好,
	// 紧接着入队 summary 就能让一节字幕落地后自动走到结构化总结,无需 admin 再
	// 手动点按钮(那个 admin UI 其实一直没接,导致 summary 永远没人触发)。
	// 三道门:课程开关、尚无 summary、尚无在途 summary 作业。最后一道防止
	// 重复入队(例如重新分段时旧 summary job 还在跑)。
	if course, cerr := s.courseRepo.FindByID(job.CourseID); cerr == nil && course != nil && course.AISummaryEnabled {
		if existing, serr := s.contentRepo.GetSummary(job.EpisodeID); serr == nil && existing == nil {
			if !s.hasPendingJob("summary", job.EpisodeID) {
				sjob := &model.AIJob{
					JobType:   "summary",
					EpisodeID: job.EpisodeID,
					CourseID:  job.CourseID,
					Status:    "queued",
					Priority:  prioritySummary,
				}
				if err := s.contentRepo.CreateJob(sjob); err != nil {
					log.Printf("AI: failed to chain-enqueue summary job for episode %d: %v", job.EpisodeID, err)
				}
			}
		}
	}
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

// ReapStaleJobs 委托给 repo,固定 30 分钟阈值。一个 LLM 调用最多 ~30s,加上
// ReAct 多轮也就几分钟;claimed_at 超过半小时还停在 processing 几乎可以肯定
// 是 worker 挂了,重置回 queued 让下一轮 poll 重新认领。
func (s *aiService) ReapStaleJobs() (int64, error) {
	return s.contentRepo.ReapStaleJobs(30 * time.Minute)
}

// sleep is a thin wrapper around time.Sleep used by the worker poll loop. Kept
// as a helper so it's swappable in tests (a test can replace it with a no-op or
// a channel signal).
var sleep = func(seconds int) { time.Sleep(time.Duration(seconds) * time.Second) }
