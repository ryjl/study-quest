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

	// ── knowledge_memories (Phase C feedback loop) ──
	// GetMasteries returns a user's per-chunk mastery for an episode (the agent
	// reads this to find weak points). Empty for a new student.
	GetMasteries(userID, episodeID uint) ([]model.KnowledgeMemory, error)
	// UpsertMemoryOnAnswer atomically applies the feedback update after an
	// answer: mastery ± (correct +0.1 / wrong -0.2, clamped 0-1), the right
	// counter ticks, last_reviewed = now. INSERT ... ON CONFLICT so concurrent
	// answers don't lose deltas (mirrors the progress atomic-accumulate rule).
	UpsertMemoryOnAnswer(userID, chunkID, episodeID, courseID uint, correct bool) error
}

type aiContentRepo struct {
	db *gorm.DB
}

// NewAIContentRepository creates an AIContentRepository bound to db.
func NewAIContentRepository(db *gorm.DB) AIContentRepository {
	return &aiContentRepo{db: db}
}

// --- content_chunks ---

func (r *aiContentRepo) ReplaceChunksForEpisode(episodeID, courseID uint, sourceType string, chunks []model.ContentChunk) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("episode_id = ? AND source_type = ?", episodeID, sourceType).
			Delete(&model.ContentChunk{}).Error; err != nil {
			return err
		}
		if len(chunks) == 0 {
			return nil
		}
		// Stamp the FK fields the caller shouldn't have to repeat per row.
		for i := range chunks {
			chunks[i].EpisodeID = episodeID
			chunks[i].CourseID = courseID
			chunks[i].SourceType = sourceType
		}
		return tx.Create(&chunks).Error
	})
}

func (r *aiContentRepo) ListChunks(episodeID uint, sourceType string) ([]model.ContentChunk, error) {
	var chunks []model.ContentChunk
	q := r.db.Where("episode_id = ?", episodeID)
	if sourceType != "" {
		q = q.Where("source_type = ?", sourceType)
	}
	if err := q.Order("chunk_index ASC").Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

func (r *aiContentRepo) HasChunks(episodeID uint, sourceType string) (bool, error) {
	var count int64
	if err := r.db.Model(&model.ContentChunk{}).
		Where("episode_id = ? AND source_type = ?", episodeID, sourceType).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *aiContentRepo) CountChunks(episodeID uint) (int64, error) {
	var count int64
	if err := r.db.Model(&model.ContentChunk{}).
		Where("episode_id = ?", episodeID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// --- ai_summaries ---

func (r *aiContentRepo) GetSummary(episodeID uint) (*model.AISummary, error) {
	var s model.AISummary
	if err := r.db.Where("episode_id = ?", episodeID).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *aiContentRepo) UpsertSummary(s *model.AISummary) error {
	// uniqueIndex on episode_id → upsert: replace if exists (re-generation).
	return r.db.Save(s).Error
}

// --- ai_jobs ---

func (r *aiContentRepo) CreateJob(job *model.AIJob) error {
	return r.db.Create(job).Error
}

func (r *aiContentRepo) GetJob(id uint) (*model.AIJob, error) {
	var job model.AIJob
	if err := r.db.First(&job, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *aiContentRepo) UpdateJobStatus(id uint, status string, errMsg string, progress *float64) error {
	updates := map[string]interface{}{
		"status":   status,
		"error":    errMsg,
		"progress": progress,
	}
	if status == "done" || status == "failed" || status == "skipped" {
		updates["completed_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	}
	return r.db.Model(&model.AIJob{}).Where("id = ?", id).Updates(updates).Error
}

// ClaimNextQueuedJob atomically claims the oldest queued job of one of the given
// types. Returns (nil, nil) when none are queued. Single in-process worker means
// contention is low, but the WHERE status='queued' guard still prevents double
// processing if we later add worker parallelism.
func (r *aiContentRepo) ClaimNextQueuedJob(jobTypes []string) (*model.AIJob, error) {
	if len(jobTypes) == 0 {
		return nil, nil
	}
	var job model.AIJob
	err := r.db.Raw(`UPDATE ai_jobs
		SET status = ?, claimed_at = CURRENT_TIMESTAMP, attempt = attempt + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = (
			SELECT id FROM ai_jobs
			WHERE status = ? AND job_type IN ?
			ORDER BY priority DESC, created_at ASC
			LIMIT 1
		)
		RETURNING *`,
		"processing", "queued", jobTypes).Scan(&job).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if job.ID == 0 {
		return nil, nil
	}
	return &job, nil
}

func (r *aiContentRepo) ListJobs(jobType string, status string, limit int) ([]model.AIJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := r.db.Model(&model.AIJob{})
	if jobType != "" {
		q = q.Where("job_type = ?", jobType)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var jobs []model.AIJob
	if err := q.Order("created_at DESC").Limit(limit).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *aiContentRepo) JobStats() (map[string]int, error) {
	type row struct {
		Status string
		Count  int
	}
	var rows []row
	if err := r.db.Model(&model.AIJob{}).
		Select("status, count(*) as count").
		Group("status").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Status] = r.Count
	}
	return out, nil
}

// ReapStaleJobs 把长时间停在 'processing' 的作业(claimed_at 早于 cutoff)重置
// 回 'queued',并清空 claimed_at/error。AI worker 是进程内单 goroutine,正常情
// 况不会滞留,但进程被 hard-kill(SIGKILL/断电)时会留下这种僵尸行——没有 reaper
// 的话它们就永远卡在 processing,既占统计又永不重跑。参照 subtitle reaper。
func (r *aiContentRepo) ReapStaleJobs(staleTimeout time.Duration) (int64, error) {
	cutoff := time.Now().Add(-staleTimeout)
	res := r.db.Exec(`UPDATE ai_jobs
		SET status = 'queued', claimed_at = NULL, error = '', updated_at = CURRENT_TIMESTAMP
		WHERE status = 'processing' AND claimed_at IS NOT NULL AND claimed_at < ?`,
		cutoff)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// --- ai_runs ---

func (r *aiContentRepo) CreateRun(run *model.AIRun) error {
	return r.db.Create(run).Error
}

func (r *aiContentRepo) GetRun(id uint) (*model.AIRun, error) {
	var run model.AIRun
	if err := r.db.First(&run, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (r *aiContentRepo) ListRunsForJob(jobID uint) ([]model.AIRun, error) {
	var runs []model.AIRun
	if err := r.db.Where("job_id = ?", jobID).Order("created_at ASC").Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

func (r *aiContentRepo) ListRecentRuns(limit int) ([]model.AIRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var runs []model.AIRun
	if err := r.db.Order("created_at DESC").Limit(limit).Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

// --- quizzes / questions / answers (Phase C) ---

func (r *aiContentRepo) GetQuiz(userID, episodeID uint) (*model.Quiz, error) {
	var q model.Quiz
	// Only the active quiz is "current" — archived rows are history. The default
	// 'active' on the column means rows created before Phase 3 (no explicit
	// status) are also treated as active, preserving the old single-quiz view.
	if err := r.db.Where("user_id = ? AND episode_id = ? AND status = 'active'", userID, episodeID).
		First(&q).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &q, nil
}

func (r *aiContentRepo) GetQuizByID(quizID uint) (*model.Quiz, error) {
	var q model.Quiz
	if err := r.db.First(&q, quizID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &q, nil
}

func (r *aiContentRepo) GetQuestions(quizID uint) ([]model.Question, error) {
	var qs []model.Question
	if err := r.db.Where("quiz_id = ?", quizID).Order("id ASC").Find(&qs).Error; err != nil {
		return nil, err
	}
	return qs, nil
}

// CreateQuiz replaces the (user, episode) quiz in one transaction: ARCHIVE the
// prior active quiz (keep its row + questions for history), then insert the new
// active quiz + questions. Answers + memory are NOT touched — a quiz refresh is
// not an amnesia event for the student.
//
// Why archive (not delete): Phase 3 keeps the student's past generations visible
// in the read-only history panel. The old quiz's questions stay attached to it
// (quiz_id FK) so history can show what was asked. Answers already snapshot
// QuizID, so they keep pointing at the archived quiz regardless.
func (r *aiContentRepo) CreateQuiz(quiz *model.Quiz, questions []model.Question) (uint, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Find the existing ACTIVE quiz for this (user, episode) and archive it.
		// Scoping to status='active' is important: archived rows from earlier
		// regens must be left untouched, otherwise we'd overwrite their
		// ArchivedAt and lose ordering in the history panel.
		var old model.Quiz
		findErr := tx.Where("user_id = ? AND episode_id = ? AND status = 'active'", quiz.UserID, quiz.EpisodeID).
			First(&old).Error
		if findErr == nil {
			// Flip the old quiz to archived in place — keep the row + its
			// questions (the history view lists them). ArchivedAt drives the
			// newest-first ordering of the history panel.
			now := time.Now()
			if err := tx.Model(&old).Updates(map[string]interface{}{
				"status":      "archived",
				"archived_at": now,
			}).Error; err != nil {
				return err
			}
		} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		// Insert the new active quiz row. The partial unique index
		// idx_quiz_user_ep_active is the DB-level guard that the archive step
		// ran first; within this single transaction the app-layer ordering above
		// already guarantees it.
		quiz.ID = 0        // ensure insert, not accidental update
		quiz.Status = ""   // let the column default 'active' apply (avoid hardcoding)
		if err := tx.Create(quiz).Error; err != nil {
			return err
		}
		// Stamp the FK on each question and bulk-insert.
		for i := range questions {
			questions[i].QuizID = quiz.ID
		}
		if len(questions) > 0 {
			if err := tx.Create(&questions).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return quiz.ID, nil
}

// ListArchivedQuizzes returns a (user, episode)'s superseded quiz generations,
// newest-archive-first. Each archived row carries its original questions (read
// via GetQuestions) so the history panel can render a fully read-only past
// attempt including correct answers.
func (r *aiContentRepo) ListArchivedQuizzes(userID, episodeID uint) ([]model.Quiz, error) {
	var quizzes []model.Quiz
	if err := r.db.Where("user_id = ? AND episode_id = ? AND status = 'archived'", userID, episodeID).
		Order("archived_at DESC").Find(&quizzes).Error; err != nil {
		return nil, err
	}
	return quizzes, nil
}

func (r *aiContentRepo) ListQuizzesForUser(userID uint) ([]model.Quiz, error) {
	var quizzes []model.Quiz
	if err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&quizzes).Error; err != nil {
		return nil, err
	}
	return quizzes, nil
}

func (r *aiContentRepo) ListAnswersForQuiz(quizID, userID uint) ([]model.Answer, error) {
	// Answers carry a snapshot QuizID (set at submit time), so we scope by user +
	// quiz WITHOUT joining questions. This is deliberate: regenerate (换题) deletes
	// old questions, which would orphan a question-join. The QuizID on historical
	// answers refers to the quiz row that existed at answer time; since a (user,
	// episode) has at most one quiz row at a time but it's replaced on regen, we
	// also include answers whose QuizID differs from the current one but share the
	// episode — giving the full attempt history across regenerations.
	// Resolve the episode from the current quiz first.
	var quiz model.Quiz
	if err := r.db.Select("episode_id").First(&quiz, quizID).Error; err != nil {
		return nil, err
	}
	var answers []model.Answer
	// All answers by this user on this episode (across all quiz generations).
	// We match on episode via the quiz_id → quiz → episode relationship for
	// answers that still point at a live quiz, OR fall back to the snapshot. The
	// simplest correct query: answers.user_id match + the answer's question or
	// snapshot quiz belongs to this episode. Since QuizID is snapshotted and
	// points to potentially-deleted quiz rows, join through a subquery of quiz
	// ids for this episode.
	err := r.db.
		Where("user_id = ? AND quiz_id IN (SELECT id FROM quizzes WHERE episode_id = ?)", userID, quiz.EpisodeID).
		Order("answered_at DESC").
		Find(&answers).Error
	if err != nil {
		return nil, err
	}
	return answers, nil
}

func (r *aiContentRepo) CreateAnswer(a *model.Answer) error {
	return r.db.Create(a).Error
}

// --- knowledge_memories (Phase C feedback loop) ---

func (r *aiContentRepo) GetMasteries(userID, episodeID uint) ([]model.KnowledgeMemory, error) {
	var rows []model.KnowledgeMemory
	if err := r.db.Where("user_id = ? AND episode_id = ?", userID, episodeID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// UpsertMemoryOnAnswer atomically applies the +0.1/-0.2 mastery update using
// INSERT ... ON CONFLICT DO UPDATE. This is the feedback-loop write path and
// MUST be atomic: a concurrent answer (e.g. rapid-fire submits) doing
// read-modify-write would lose deltas — same bug class as the progress
// watch-seconds accumulation, prevented the same way (single SQL statement).
//
// The mastery math is duplicated from agent.ApplyMastery intentionally — the DB
// is the source of truth here (the Go helper is for tests that don't touch the
// DB). Both apply identical clamp + step rules; keep them in sync.
func (r *aiContentRepo) UpsertMemoryOnAnswer(userID, chunkID, episodeID, courseID uint, correct bool) error {
	// Both the INSERT (first answer for this chunk) and the ON CONFLICT UPDATE
	// (subsequent answers) must apply the same mastery delta + counter bump. The
	// INSERT path uses the initial mastery (0) + the delta; the UPDATE path reads
	// the existing mastery. deltas: correct +0.1 (clamp 1.0), wrong -0.2 (clamp 0).
	var deltaUpdate, fieldUpdate, initMastery string
	var initCorrect, initWrong int
	if correct {
		deltaUpdate = "MIN(mastery + 0.1, 1.0)"
		fieldUpdate = "correct_count = correct_count + 1"
		initMastery = "0.1"
		initCorrect = 1
	} else {
		deltaUpdate = "MAX(mastery - 0.2, 0.0)"
		fieldUpdate = "wrong_count = wrong_count + 1"
		initMastery = "0.0" // 0 - 0.2 clamped to 0
		initWrong = 1
	}
	// uniqueIndex(user_id, chunk_id) is the conflict target. episode_id/course_id
	// are part of the INSERT (for new rows) but not the conflict key.
	sql := `INSERT INTO knowledge_memories (user_id, episode_id, course_id, chunk_id, mastery, correct_count, wrong_count, last_reviewed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, chunk_id) DO UPDATE SET
			mastery = ` + deltaUpdate + `,
			` + fieldUpdate + `,
			last_reviewed = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP`
	return r.db.Exec(sql, userID, episodeID, courseID, chunkID, initMastery, initCorrect, initWrong).Error
}
