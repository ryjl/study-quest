package repository

import (
	"errors"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// ErrSubtitleJobNotClaimed is returned by MarkDone when the job isn't in the
// 'processing' state anymore — i.e. a reaper or retry moved it between the
// worker's claim and its Complete. The caller MUST discard the SRT it was
// about to persist, because another worker may already have completed a
// fresh transcription of the same job. Compare via errors.Is.
var ErrSubtitleJobNotClaimed = errors.New("subtitle job no longer processing (stale completion)")

// SubtitleJobRepository persists subtitle-generation queue rows. The notable
// method is ClaimNext — it is an atomic compare-and-swap (a single UPDATE with
// a SELECT subquery) so that two workers racing for the next job can never
// both win. This is the one place the subtitle queue cannot mirror the
// in-memory ProbeWorker: the worker lives on another machine, so the queue
// state must live in the DB and the claim must be one atomic statement.
type SubtitleJobRepository interface {
	// WithTx returns a copy bound to an in-progress transaction.
	WithTx(tx *gorm.DB) SubtitleJobRepository

	Create(job *model.SubtitleJob) error
	FindByID(id uint) (*model.SubtitleJob, error)
	Update(job *model.SubtitleJob) error

	// FindActiveByEpisode returns the episode's queued or processing job, or
	// (nil, nil) if there is none. Used by the service layer to de-duplicate
	// enqueue requests (an episode may have at most one non-terminal job).
	FindActiveByEpisode(episodeID uint) (*model.SubtitleJob, error)

	// ClaimNext atomically flips the highest-priority (then oldest) queued job
	// to processing, bumps attempt, stamps claimed_at, records which worker
	// claimed it, and returns it. Returns (nil, nil) when no job is queued.
	// The claim is a single statement so two concurrent workers cannot both
	// grab the same job.
	ClaimNext(workerID string) (*model.SubtitleJob, error)

	// Status transitions. Each loads by id, validates nothing (the service
	// layer owns state-machine legality), sets the relevant fields, and saves.
	MarkDone(jobID uint) error
	MarkFailed(jobID uint, errStr string) error
	MarkSkipped(jobID uint) error
	// MarkQueued flips a job back to queued (used by Retry and ReapStale). It
	// clears claimed_at so the row looks freshly enqueued.
	MarkQueued(jobID uint) error

	// TouchClaim stamps claimed_at = now on a processing job (worker heartbeat).
	// progress, when non-nil, also persists the worker's transcription ratio
	// (0.0..1.0) so the admin view can show how far along a long transcription is.
	// progress==nil leaves the stored value untouched.
	TouchClaim(jobID uint, progress *float64) error

	// ReapStale flips processing jobs whose claimed_at is older than the given
	// age back to queued, returning how many it reaped. A safety net for a
	// worker that crashed or was powered off mid-transcription.
	ReapStale(staleAfter time.Duration) (int, error)

	// Listing / stats for the admin queue view.
	ListByStatus(status string, limit int) ([]model.SubtitleJob, error)
	ListAll(limit int) ([]model.SubtitleJob, error)
	CountByStatus() (map[string]int, error)

	// EpisodeWithJob joins each queued/processing/done/failed job to its
	// episode title + duration so the admin list can render without an N+1.
	// Rows are ordered by status group then recency.
	ListWithEpisode(status string, limit int) ([]SubtitleJobWithEpisode, error)
}

// SubtitleJobWithEpisode is a job row joined with its episode's display fields.
// SubtitleID is the id of the subtitle row the job produced (matched by
// episode_id + the job's target language), or nil when no such row exists
// (job not done yet, or the subtitle was deleted). Used by the admin queue UI
// to offer a "view generated subtitle" action on done rows — without this
// the queue page had no way to jump from a finished job to its output.
type SubtitleJobWithEpisode struct {
	model.SubtitleJob
	EpisodeTitle    *string
	EpisodeCourseID *uint
	DurationSeconds *int
	SubtitleID      *uint
}

type subtitleJobRepo struct {
	db *gorm.DB
}

// NewSubtitleJobRepository creates an instance of SubtitleJobRepository.
func NewSubtitleJobRepository(db *gorm.DB) SubtitleJobRepository {
	return &subtitleJobRepo{db: db}
}

func (r *subtitleJobRepo) WithTx(tx *gorm.DB) SubtitleJobRepository {
	return &subtitleJobRepo{db: tx}
}

func (r *subtitleJobRepo) Create(job *model.SubtitleJob) error {
	return r.db.Create(job).Error
}

func (r *subtitleJobRepo) FindByID(id uint) (*model.SubtitleJob, error) {
	var job model.SubtitleJob
	if err := r.db.First(&job, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *subtitleJobRepo) Update(job *model.SubtitleJob) error {
	return r.db.Save(job).Error
}

func (r *subtitleJobRepo) FindActiveByEpisode(episodeID uint) (*model.SubtitleJob, error) {
	var job model.SubtitleJob
	err := r.db.Where("episode_id = ? AND status IN ?", episodeID, []string{
		model.SubtitleJobQueued, model.SubtitleJobProcessing,
	}).Order("id desc").First(&job).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

// ClaimNext atomically claims the highest-priority (then oldest) queued job.
//
// Implemented as ONE statement: an UPDATE whose WHERE narrows to a single id
// picked by a correlated subquery, with RETURNING to hand back the exact row
// that was flipped. Two concurrent ClaimNext calls cannot both win the same
// row: the first UPDATE changes that row's status to 'processing', so it no
// longer satisfies the subquery's `status='queued'` filter by the time the
// second runs (and RETURNING returns nothing for the loser).
//
// RETURNING is critical here: an earlier version did UPDATE then re-SELECT'd
// "the most-recently processing job" to recover the id — but that re-SELECT is
// ambiguous under concurrency (two winners both read the newest processing row,
// or one reads a row claimed by the other). RETURNING pins the result to the
// exact row this UPDATE touched, so each caller reliably gets its own job.
// SQLite has supported RETURNING since 3.35 (2021); the mattn/go-sqlite3 driver
// bundled with gorm.io/driver/sqlite v1.5.7 is newer than that.
func (r *subtitleJobRepo) ClaimNext(workerID string) (*model.SubtitleJob, error) {
	var job model.SubtitleJob
	err := r.db.Raw(`UPDATE subtitle_jobs
		SET status = ?, claimed_at = CURRENT_TIMESTAMP, attempt = attempt + 1, claimed_by = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = (
			SELECT id FROM subtitle_jobs
			WHERE status = ?
			ORDER BY priority DESC, created_at ASC
			LIMIT 1
		)
		RETURNING *`,
		model.SubtitleJobProcessing, workerID, model.SubtitleJobQueued).Scan(&job).Error
	if err != nil {
		// gorm Raw().Scan() on zero RETURNING rows yields ErrRecordNotFound.
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

// MarkDone flips a job to done ONLY if it is currently processing. The
// status-guarded WHERE is the defense against a late Complete colliding with a
// reaper/retry that already moved the job: if the row isn't processing anymore
// (e.g. reaped back to queued, then re-claimed by another worker that already
// completed it), RowsAffected is 0 and the caller knows its SRT is stale and
// should be discarded. Returns ErrSubtitleJobNotClaimed when nothing matched.
func (r *subtitleJobRepo) MarkDone(jobID uint) error {
	now := time.Now()
	res := r.db.Model(&model.SubtitleJob{}).
		Where("id = ? AND status = ?", jobID, model.SubtitleJobProcessing).
		Updates(map[string]interface{}{
			"status":       model.SubtitleJobDone,
			"completed_at": now,
			"updated_at":   now,
			"error":        "",
			"progress":     nil,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrSubtitleJobNotClaimed
	}
	return nil
}

func (r *subtitleJobRepo) MarkFailed(jobID uint, errStr string) error {
	return r.db.Model(&model.SubtitleJob{}).Where("id = ?", jobID).
		Updates(map[string]interface{}{
			"status":     model.SubtitleJobFailed,
			"error":      errStr,
			"updated_at": time.Now(),
		}).Error
}

func (r *subtitleJobRepo) MarkSkipped(jobID uint) error {
	return r.db.Model(&model.SubtitleJob{}).Where("id = ?", jobID).
		Updates(map[string]interface{}{
			"status":     model.SubtitleJobSkipped,
			"updated_at": time.Now(),
		}).Error
}

// MarkQueued flips a job back to queued (used by Retry and ReapStale). It clears
// claimed_at/claimed_by so the row looks freshly enqueued and is immediately
// claimable by any worker. It also clears error and progress so a retried job
// doesn't show the previous attempt's failure message or stale percentage while
// sitting in the queue — matching MarkDone's terminal cleanup and the AI job
// RetryJob path (which clears error too).
func (r *subtitleJobRepo) MarkQueued(jobID uint) error {
	return r.db.Model(&model.SubtitleJob{}).Where("id = ?", jobID).
		Updates(map[string]interface{}{
			"status":      model.SubtitleJobQueued,
			"claimed_at":  nil,
			"claimed_by":  "",
			"error":       "",
			"progress":    nil,
			"updated_at":  time.Now(),
		}).Error
}

func (r *subtitleJobRepo) TouchClaim(jobID uint, progress *float64) error {
	updates := map[string]interface{}{
		"claimed_at": time.Now(),
		"updated_at": time.Now(),
	}
	if progress != nil {
		updates["progress"] = *progress
	}
	return r.db.Model(&model.SubtitleJob{}).Where("id = ?", jobID).
		Updates(updates).Error
}

func (r *subtitleJobRepo) ReapStale(staleAfter time.Duration) (int, error) {
	// claimed_at is written by ClaimNext as SQLite CURRENT_TIMESTAMP, which is
	// UTC. The cutoff MUST be computed in UTC too — using time.Now() (local)
	// here used to make the comparison off by the local UTC offset (8h in
	// production), causing freshly-claimed jobs to be reaped within minutes.
	// This is the appclock-class bug CLAUDE.md rule #3 warns about, just in a
	// different spot than the streak math. Same fix: go through UTC explicitly.
	cutoff := time.Now().UTC().Add(-staleAfter)
	res := r.db.Exec(`UPDATE subtitle_jobs
		SET status = ?, claimed_at = NULL, claimed_by = '', progress = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE status = ? AND claimed_at IS NOT NULL AND claimed_at < ?`,
		model.SubtitleJobQueued, model.SubtitleJobProcessing, cutoff)
	if res.Error != nil {
		return 0, res.Error
	}
	return int(res.RowsAffected), nil
}

func (r *subtitleJobRepo) ListByStatus(status string, limit int) ([]model.SubtitleJob, error) {
	var jobs []model.SubtitleJob
	q := r.db.Where("status = ?", status).Order("priority desc, created_at asc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&jobs).Error
	return jobs, err
}

func (r *subtitleJobRepo) ListAll(limit int) ([]model.SubtitleJob, error) {
	var jobs []model.SubtitleJob
	q := r.db.Order("updated_at desc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&jobs).Error
	return jobs, err
}

func (r *subtitleJobRepo) CountByStatus() (map[string]int, error) {
	type row struct {
		Status string
		Cnt    int
	}
	var rows []row
	err := r.db.Model(&model.SubtitleJob{}).
		Select("status, count(*) as cnt").
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Status] = r.Cnt
	}
	return out, nil
}

// ListWithEpisode joins subtitle_jobs to episodes (and, when the job is done,
// to the subtitle row it produced) for the admin list view. status=="" means
// "all". Ordering keeps actionable rows on top: processing, then queued, then
// failed/skipped, then done — each block by recency.
//
// The subtitle join is keyed on (episode_id, language) — the same key
// episodeRepo.SaveSubtitle upserts by. We match the PRIMARY subtitle when one
// exists (the common case: one zh-CN whisper track per episode); if there are
// multiple non-primary rows for the same language we pick the lowest id (oldest,
// which is the whisper-original the worker wrote). NULL when no subtitle row
// matches (job not done yet, or the subtitle was deleted). The LATERAL-style
// subselect is emulated via a correlated subquery in the SELECT so we get
// exactly one row per job without a fanout.
func (r *subtitleJobRepo) ListWithEpisode(status string, limit int) ([]SubtitleJobWithEpisode, error) {
	// Ordering keeps actionable rows on top: processing, then queued, then
	// failed/skipped, then done — each block by recency.
	q := r.db.Table("subtitle_jobs AS j").
		Select(`j.*, e.title AS episode_title, e.course_id AS episode_course_id,
			e.duration_seconds AS duration_seconds,
			(SELECT s.id FROM subtitles s
			 WHERE s.episode_id = j.episode_id AND s.language = j.language
			 ORDER BY s.is_primary DESC, s.id ASC LIMIT 1) AS subtitle_id`).
		Joins("LEFT JOIN episodes e ON e.id = j.episode_id").
		Order("CASE j.status " +
			"WHEN 'processing' THEN 0 " +
			"WHEN 'queued' THEN 1 " +
			"WHEN 'failed' THEN 2 " +
			"WHEN 'skipped' THEN 3 " +
			"WHEN 'done' THEN 4 " +
			"ELSE 5 END, j.updated_at DESC")
	if status != "" {
		q = q.Where("j.status = ?", status)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}

	var rows []SubtitleJobWithEpisode
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
