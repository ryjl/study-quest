package repository

import (
	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// LogRepository persists LogEntry rows — the lightweight structured log layer
// (TODO.md P1). Unlike AIRun (full per-LLM-call trace), LogEntry captures
// operational EVENTS (job failed, reaper ran, provider error, worker panic).
//
// Append is best-effort by design: the callers (failJob, reaper, polishStats,
// provider-resolve, worker-panic) are in hot paths where a logging failure
// must NOT derail the business logic. So the service-layer wrapper (appendLog)
// swallows Append errors (logs them via log.Printf to stderr) and the callers
// never see an error. This keeps the write path non-blocking and fail-safe.
type LogRepository interface {
	// Append inserts one log entry. Best-effort: callers should treat an error
	// as non-fatal (the wrapper does). CreatedAt is left to GORM's auto-stamp.
	Append(entry *model.LogEntry) error
	// ListRecent returns recent entries, optionally filtered by level and/or
	// source ("" = no filter on that axis) and/or job_id (nil = no filter).
	// Newest first — the /admin/logs page is a tail view.
	ListRecent(level, source string, jobID *uint, limit int) ([]model.LogEntry, error)
}

type logRepo struct {
	db *gorm.DB
}

// NewLogRepository creates a LogRepository bound to db.
func NewLogRepository(db *gorm.DB) LogRepository {
	return &logRepo{db: db}
}

func (r *logRepo) Append(entry *model.LogEntry) error {
	return r.db.Create(entry).Error
}

// ListRecent filters then orders newest-first. limit is clamped server-side by
// the handler (parseLimit) so this repo just applies it directly.
func (r *logRepo) ListRecent(level, source string, jobID *uint, limit int) ([]model.LogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := r.db.Model(&model.LogEntry{})
	if level != "" {
		q = q.Where("level = ?", level)
	}
	if source != "" {
		q = q.Where("source = ?", source)
	}
	if jobID != nil {
		q = q.Where("job_id = ?", *jobID)
	}
	var out []model.LogEntry
	err := q.Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}
