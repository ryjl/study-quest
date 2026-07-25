package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// AIContentRepository covers the AI subsystem's persisted state: content chunks
// (the RAG corpus), summaries, async jobs, and decision-run traces.
//
// Unlike SubtitleJobRepository, there is NO ClaimNext/heartbeat/MarkDone protocol
// here: AI jobs run IN-PROCESS (the LLM call is an HTTP request from the server
// itself, unlike Whisper which runs on a separate machine). So a job is simply
// created (queued), picked up by an in-process worker goroutine that flips it
// to processing→done/failed, with no atomic-claim dance needed. The status
// machine is the same (queued|processing|done|failed|skipped) but the
// transitions are single-writer per job (the goroutine that owns it).
//
// All methods are nil-safe at the SERVICE layer (the repo assumes a live db);
// the handler guards the whole feature off when AI isn't wired.
type AIContentRepository interface {
	// ── content_chunks ──
	// ReplaceChunksForEpisode deletes all existing chunks for an episode+source
	// and inserts the new set in one transaction. Called after re-segmenting
	// (segmentation is idempotent: re-running replaces, doesn't accumulate).
	ReplaceChunksForEpisode(episodeID, courseID uint, sourceType string, chunks []model.ContentChunk) error
	// ListChunks returns all chunks for an episode+source, ordered by chunk_index.
	// Used by the summarizer (read the whole lesson's text) and retriever (Phase C).
	ListChunks(episodeID uint, sourceType string) ([]model.ContentChunk, error)
	// HasChunks reports whether an episode has any chunks of a source type.
	// Cheap gate: skip segmentation if chunks already exist.
	HasChunks(episodeID uint, sourceType string) (bool, error)
	// CountChunks returns how many chunks an episode has (for the admin UI).
	CountChunks(episodeID uint) (int64, error)

	// ── ai_summaries ──
	GetSummary(episodeID uint) (*model.AISummary, error)
	UpsertSummary(s *model.AISummary) error
	// DeleteSummary 删除某 episode 的 summary(物理删)。供 admin 控制台"删除"按钮用。
	// 重新生成走 Upsert(覆盖);删除是独立操作,语义是"清掉,下次想再要时重新生成"。
	DeleteSummary(episodeID uint) error
	// ListEpisodeIDsWithSummaryByCourse 返回某课程下"已有 AI summary"的 episode id
	// 集合。给 admin 内容管理 tab 用来 gate 每集"删除"按钮(无 summary 不显示)。
	// AISummary 自带 CourseID,无需 JOIN。
	ListEpisodeIDsWithSummaryByCourse(courseID uint) ([]uint, error)
	// CountEpisodesWithSummaryByCourse 返回某课程下"已有 AI summary"的 episode 数量。
	// 给课程总览的"陈旧检测"用:生成时快照 EpisodeCountAtGen,读时跟当前数对比。
	CountEpisodesWithSummaryByCourse(courseID uint) (int64, error)

	// ── ai_jobs ──
	CreateJob(job *model.AIJob) error
	GetJob(id uint) (*model.AIJob, error)
	// UpdateJobStatus flips a job's status (and related fields). Single-writer
	// per job (the owning worker goroutine), so no status-guard WHERE needed
	// here — contrast SubtitleJob.MarkDone which guards because an external
	// worker's late Complete can race a reaper.
	UpdateJobStatus(id uint, status string, errMsg string, progress *float64) error
	// ClaimNextQueuedJob returns the oldest queued job of a given type (or types),
	// flipping it to processing. Since the AI worker is in-process and
	// single-goroutine, contention is minimal, but the status WHERE still makes
	// it safe if we later parallelize.
	ClaimNextQueuedJob(jobTypes []string) (*model.AIJob, error)
	ListJobs(jobType string, status string, limit int) ([]model.AIJob, error)
	JobStats() (map[string]int, error)
	// ReapStaleJobs resets 'processing' jobs whose claimed_at is older than
	// staleTimeout back to 'queued' (clearing the claim + error), so a worker
	// that crashed mid-LLM-call doesn't strand a job in processing forever.
	// Mirrors subtitle_job_repo.ReapStale. Returns the number of rows reset.
	ReapStaleJobs(staleTimeout time.Duration) (int64, error)
	// ResetJob resets ONE processing job back to 'queued' on admin demand — the
	// manual equivalent of ReapStaleJobs for a job the admin has judged stuck
	// (e.g. the worker is alive but a relay call hung without crashing). Clears
	// claimed_at + error so the next worker poll re-claims it cleanly. Returns
	// ErrJobNotProcessing (non-fatal) if the job isn't currently processing, so
	// the handler can surface "nothing to reset" instead of a silent no-op.
	ResetJob(jobID uint) error
	// RetryJob resets ONE failed job back to 'queued' on admin demand, so it can
	// be re-run after the underlying problem (e.g. embedding was unavailable) is
	// fixed. Unlike ResetJob (which targets stuck-but-alive processing jobs),
	// RetryJob targets terminal 'failed' jobs — the only way to revive them,
	// since failJob marks them failed without auto-retry (AI calls cost money,
	// so we don't loop on a bad config). Returns ErrJobNotFailed if the job isn't
	// currently failed (e.g. it already succeeded or was retried).
	RetryJob(jobID uint) error

	// ── ai_runs ──
	CreateRun(run *model.AIRun) error
	GetRun(id uint) (*model.AIRun, error)
	ListRunsForJob(jobID uint) ([]model.AIRun, error)
	ListRecentRuns(limit int) ([]model.AIRun, error)

	// ── quizzes / questions / answers (Phase C) ──
	// GetQuiz returns the single ACTIVE quiz for a (user, episode), or nil if
	// none. Archived quizzes (superseded generations, Phase 3) are NOT returned
	// here — they're read-only history surfaced via ListArchivedQuizzes. Nil is
	// the trigger for lazy generation: the service enqueues a quiz job.
	GetQuiz(userID, episodeID uint) (*model.Quiz, error)
	// GetQuizByID loads one quiz by its primary key (admin detail view).
	GetQuizByID(quizID uint) (*model.Quiz, error)
	// DeleteQuiz 物理删一条 quiz(连同其 Question/Answer 由 FK CASCADE 清)。
	// 和 archive(active→archived)语义不同:archive 保留历史,delete 是彻底清除。
	// 供 admin 控制台"删除"按钮用。
	DeleteQuiz(quizID uint) error
	// GetQuestions returns a quiz's questions ordered by id.
	GetQuestions(quizID uint) ([]model.Question, error)
	// CreateQuiz replaces the (user, episode) quiz in one transaction: ARCHIVES
	// the prior active quiz (换题/regenerate) by flipping Status→archived and
	// stamping ArchivedAt, then inserts a fresh active quiz + its questions. The
	// old questions row stays attached to the archived quiz so the student's
	// past attempts remain readable in history. Answers and KnowledgeMemory are
	// preserved (a quiz refresh never wipes a student's answer history or
	// mastery). The single-active invariant is also enforced by a partial unique
	// index (see model.migrateQuizActiveUniqueIndex). Returns the new quiz ID.
	CreateQuiz(quiz *model.Quiz, questions []model.Question) (uint, error)
	// ListArchivedQuizzes returns a (user, episode)'s superseded quizzes
	// (Status='archived') newest-archive-first, for the read-only history panel.
	// Never includes the active quiz.
	ListArchivedQuizzes(userID, episodeID uint) ([]model.Quiz, error)
	// ListQuizzesForUser lists all of a user's quizzes (admin user view),
	// newest first.
	ListQuizzesForUser(userID uint) ([]model.Quiz, error)
	// ListAnswersForQuiz returns every answer to any question in a quiz
	// (admin detail view — shows the student's attempt history, supports redo).
	ListAnswersForQuiz(quizID, userID uint) ([]model.Answer, error)
	// CreateAnswer appends one answer record. Append-only by design: redoing a
	// quiz adds a new row, it never edits the old one (so the full attempt
	// history is preserved for observability).
	CreateAnswer(a *model.Answer) error
	// MarkQuizSubmitted 给 quiz 盖"已交卷"时间戳(SubmittedAt=now)。Phase B 统一
	// 提交:一次 submit-all = 一次考试,提交后该 quiz 锁定不可再改。幂等:重复调用
	// 只更新时间戳(用 Updates 只写 submitted_at 一列,不触碰其它字段)。
	MarkQuizSubmitted(quizID uint, at time.Time) error
	// TryMarkQuizSubmitted 是并发安全的抢占式交卷戳:仅当 submitted_at IS NULL 时
	// 盖戳,返回是否抢到。供 SubmitAllQuizAnswers 消除 TOCTOU 窗口用——在落任何
	// answer/memory 之前抢,抢不到直接拒。详见实现注释。
	TryMarkQuizSubmitted(quizID uint, at time.Time) (bool, error)
	// ArchiveQuizByID 把一条 quiz 翻成 archived(status='archived' + 设 archived_at)。
	// 精确按 id 定位,不按 (user,episode) 批量,避免误伤其它行。供 SubmitAllQuizAnswers
	// 「交卷即归档」用——交卷成功后把这套卷子移进历史面板(可点开 review),下次进入
	// 当前练习区不再卡在「上次结果只读态」。和换题(regenerate→CreateQuiz)的归档是
	// 同一套机制,archived_at 驱动历史面板 newest-first 排序。
	ArchiveQuizByID(quizID uint, at time.Time) error
	// HasAnyQuiz 报告某 (user, episode) 是否有任何 quiz 行(active 或 archived 都算)。
	// 供 GetOrEnqueueQuiz 区分「首次」(无任何 quiz→自动 enqueue 生成) vs「已做过」
	// (有历史→返回 done,不自动出新题,等学生点重新生成)。
	HasAnyQuiz(userID, episodeID uint) (bool, error)

	// ── 错题本 + 课程考试 抽题层 (见 question_pool_repo.go) ──
	// ListWrongAnswersByUserCourse 列出某用户在某课程下全部做错的题(跨 episode、跨
	// quiz generation)。错题本「按课程」视图的数据源。见 question_pool_repo.go 注释。
	ListWrongAnswersByUserCourse(userID, courseID uint) ([]WrongBookRow, error)
	// ListWrongAnswersByUser 列出某用户的全部错题(全平台),可按 subject/course/chunk
	// 过滤(0 = 不过滤)。错题本顶层视图的数据源。
	ListWrongAnswersByUser(userID, subjectID, courseID, chunkID uint) ([]WrongBookRow, error)
	// ListQuestionsByCourseForExam 取某课程下全部 questions(跨 episode、跨用户)作为
	// 考试抽题池。只取有 chunk_id 的题(合成题排除)。供 exam_selector 加权抽题。
	ListQuestionsByCourseForExam(courseID uint) ([]ExamPoolQuestion, error)
	// ListChunksByCourseForExam 取某课程下全部 subtitle chunks(跨 episode),供抽题
	// 退化用或给 quizzer agent 做课程级出题上下文。
	ListChunksByCourseForExam(courseID uint) ([]model.ContentChunk, error)

	// ── knowledge_memories (Phase C feedback loop) ──
	// GetMasteries returns a user's per-chunk mastery for an episode (the agent
	// reads this to find weak points). Empty for a new student.
	GetMasteries(userID, episodeID uint) ([]model.KnowledgeMemory, error)
	// UpsertMemoryOnAnswer atomically applies the feedback update after an
	// answer: mastery ± (correct +0.1 / wrong -0.2, clamped 0-1), the right
	// counter ticks, last_reviewed = now. INSERT ... ON CONFLICT so concurrent
	// answers don't lose deltas (mirrors the progress atomic-accumulate rule).
	UpsertMemoryOnAnswer(userID, chunkID, episodeID, courseID uint, correct bool) error

	// ── cross-course aggregation (Phase C: advice agent) ──
	// GetCourseMasteries 跨课时聚合:返回一个学生在某课程下所有课时的掌握度行,
	// 用于 advice agent 的"跨课程弱点分析"。KnowledgeMemory 已冗余 course_id
	// (见 model.migrateQuizActiveUniqueIndex 之上的迁移),所以一次 WHERE 就够。
	// 调用方(MemoryStore.CourseMasteries)负责按 mastery ASC 排序,弱点优先。
	GetCourseMasteries(userID, courseID uint) ([]model.KnowledgeMemory, error)
	// GetSubjectMasteries 科目级聚合:JOIN courses 取出该 subject 下所有课程的
	// mastery。比 GetCourseMasteries 多一层 course→subject 归属,用于"整个数学
	// 科目的弱点分析"。返回顺序同样由调用方排序。
	GetSubjectMasteries(userID, subjectID uint) ([]model.KnowledgeMemory, error)

	// ── study_advices (Phase C: advice generation result) ──
	// GetAdvice returns the stored advice for a (user, scope, scope_id) triple, or
	// nil if none has been generated yet. scope ∈ {"episode","course","subject"}.
	// Used by the client GET endpoints; nil triggers lazy generation via a job.
	GetAdvice(userID uint, scope string, scopeID uint) (*model.StudyAdvice, error)
	// UpsertAdvice replaces any existing advice for the (user, scope, scope_id)
	// triple — re-generation fully replaces (mirrors AISummary.UpsertSummary),
	// keeping only the latest snapshot. The unique index on the triple is the
	// DB-level guard; this Save relies on it.
	UpsertAdvice(a *model.StudyAdvice) error
	// ListUserAdvice 列出某用户的所有 advice(所有 scope,所有 scope_id),按
	// generated_at DESC 排序。给 admin 控制台显示"这个学生有哪些 advice"用。
	ListUserAdvice(userID uint) ([]model.StudyAdvice, error)
	// DeleteAdvice 物理删某 (user, scope, scope_id) 的 advice。重新生成走 Upsert
	// (覆盖);删除是独立操作。多态 scope_id 不影响这里 —— 删除按 (user, scope, scope_id)
	// 三元组定位,和 GetAdvice 一致。
	DeleteAdvice(userID uint, scope string, scopeID uint) error

	// ── ai_course_summaries (Phase D: course-unique 课程级总结) ──
	// GetCourseSummary 取某课程的总结(unique on course_id,所以最多一条)。无记录返回
	// (nil, nil),让客户端/handler 决定是返回 "无总结" 还是触发 admin 生成。课程总结是
	// course-unique 的纯内容总结(不含 user 维度),所有学生共享。
	GetCourseSummary(courseID uint) (*model.AICourseSummary, error)
	// UpsertCourseSummary 替换该课程的旧总结(unique index on course_id 是 DB 级守卫,
	// 重新生成完全覆盖旧总结——和 UpsertAdvice/UpsertSummary 同语义)。admin 触发重生成
	// 时调用。
	UpsertCourseSummary(s *model.AICourseSummary) error
	// DeleteCourseSummary 物理删某课程的总结(unique on course_id,最多一条)。
	DeleteCourseSummary(courseID uint) error

	// ── user_study_reports (Phase E: admin 跨课程学习报告) ──
	// GetUserStudyReport 取某用户的最新学习报告(unique on user_id,所以最多一条)。
	// 无记录返回 (nil, nil),让 admin handler 决定是返回 "无报告" 还是触发生成。
	GetUserStudyReport(userID uint) (*model.UserStudyReport, error)
	// UpsertUserStudyReport 替换该用户的旧报告(unique index on user_id 是 DB 级守卫,
	// 重新生成完全覆盖旧报告——和 UpsertAdvice 同语义)。admin 触发重生成时调用。
	UpsertUserStudyReport(r *model.UserStudyReport) error
	// DeleteUserReport 物理删某用户的学习报告(unique on user_id,最多一条)。
	DeleteUserReport(userID uint) error
}

// ErrJobNotProcessing is returned by ResetJob when the targeted job isn't in
// 'processing' state. The handler treats this as a non-fatal "nothing to do"
// (the admin reset a job that already finished or was reaped) rather than 500.
var ErrJobNotProcessing = errors.New("job is not in processing state")

// ErrJobNotFailed is returned by RetryJob when the targeted job isn't in
// 'failed' state. Non-fatal: the handler surfaces "nothing to retry" rather than
// a silent no-op (which would hide a double-retry or a job that already
// succeeded on a prior retry).
var ErrJobNotFailed = errors.New("job is not in failed state")

// ErrJobNotPolish is returned by AIService.SkipPolish when the targeted job
// isn't a polish job (the skip-polish escape hatch only makes sense for polish;
// other job types have their own retry/reset semantics). Non-fatal: handler
// surfaces 409.
var ErrJobNotPolish = errors.New("job is not a polish job")

// ErrJobNotFound is returned by AIService methods that look up a job by id and
// need to distinguish "not found" from a real DB error. GetJob itself returns
// (nil, nil) for not-found (legacy convention from the episode repo), so the
// service layer maps nil→this error for callers that want a value to compare.
var ErrJobNotFound = errors.New("ai job not found")

type aiContentRepo struct {
	db *gorm.DB
}

// NewAIContentRepository creates an AIContentRepository bound to db.
func NewAIContentRepository(db *gorm.DB) AIContentRepository {
	return &aiContentRepo{db: db}
}

// --- content_chunks ---

// Method implementations live in topical sibling files:
//   ai_chunk_repo.go    — content chunks (RAG corpus)
//   ai_summary_repo.go  — episode summaries
//   ai_job_repo.go      — async jobs + runs (the in-process state machine)
//   ai_quiz_repo.go     — quizzes, questions, answers
//   ai_memory_repo.go   — knowledge mastery / memory
//   ai_advice_repo.go   — study advice, course summaries, user reports
