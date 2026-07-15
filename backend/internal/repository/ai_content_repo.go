package repository

import (
	"errors"

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

	// ── ai_runs ──
	CreateRun(run *model.AIRun) error
	GetRun(id uint) (*model.AIRun, error)
	ListRunsForJob(jobID uint) ([]model.AIRun, error)
	ListRecentRuns(limit int) ([]model.AIRun, error)
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
