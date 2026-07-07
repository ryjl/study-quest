package repository

import (
	"errors"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// UserProgressSummary is the per-user aggregate computed in one batch query
// for the admin user list. All fields are 0 when the user has no progress
// rows yet.
type UserProgressSummary struct {
	CompletedEpisodes int64
	TotalWatchSeconds int64
	LastActiveAt      *time.Time
}

// ProgressRepository handles UserProgress updates, UserPoints, and PointsLedger transaction auditing.
type ProgressRepository interface {
	GetProgress(userID, episodeID uint) (*model.UserProgress, error)
	SaveProgress(progress *model.UserProgress) error
	GetPoints(userID uint) (*model.UserPoint, error)
	AddPoints(ledger *model.PointsLedger) error
	GetUserProgressOverview(userID uint) ([]model.UserProgress, error)
	GetLastWatchedEpisode(userID, courseID uint) (*model.UserProgress, error)
	GetPointsLedger(userID uint, limit, offset int) ([]model.PointsLedger, error)

	// BatchUserProgressSummary returns completed-episode count, accumulated
	// watch seconds, and last-active timestamp per user — in a single query,
	// keyed by user id. Used by the admin user list to avoid N+1.
	BatchUserProgressSummary() (map[uint]UserProgressSummary, error)
	// BatchPoints loads current+total points for many users in one query.
	BatchPoints() (map[uint]model.UserPoint, error)
}

type progressRepo struct {
	db *gorm.DB
}

// NewProgressRepository creates an instance of ProgressRepository.
func NewProgressRepository(db *gorm.DB) ProgressRepository {
	return &progressRepo{db: db}
}

func (r *progressRepo) GetProgress(userID, episodeID uint) (*model.UserProgress, error) {
	var prog model.UserProgress
	err := r.db.Where("user_id = ? AND episode_id = ?", userID, episodeID).First(&prog).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &prog, nil
}

func (r *progressRepo) SaveProgress(progress *model.UserProgress) error {
	var prog model.UserProgress
	err := r.db.Where("user_id = ? AND episode_id = ?", progress.UserID, progress.EpisodeID).First(&prog).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(progress).Error
		}
		return err
	}

	prog.LastPositionSeconds = progress.LastPositionSeconds
	prog.WatchSeconds = progress.WatchSeconds
	prog.IsCompleted = progress.IsCompleted
	if progress.UnlockedAt != nil {
		prog.UnlockedAt = progress.UnlockedAt
	}
	return r.db.Save(&prog).Error
}

func (r *progressRepo) GetPoints(userID uint) (*model.UserPoint, error) {
	var pt model.UserPoint
	err := r.db.First(&pt, "user_id = ?", userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &pt, nil
}

func (r *progressRepo) AddPoints(ledger *model.PointsLedger) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// 1. Create Ledger entry
		if err := tx.Create(ledger).Error; err != nil {
			return err
		}

		// 2. Load User Points
		var pt model.UserPoint
		err := tx.First(&pt, "user_id = ?", ledger.UserID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// Initialize structure
				pt = model.UserPoint{
					UserID:            ledger.UserID,
					CurrentPoints:     0,
					TotalEarnedPoints: 0,
				}
				if err := tx.Create(&pt).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		}

		// 3. Update Balance
		pt.CurrentPoints += ledger.ChangeAmount
		if ledger.ChangeAmount > 0 {
			pt.TotalEarnedPoints += ledger.ChangeAmount
		}
		pt.UpdatedAt = time.Now()

		return tx.Save(&pt).Error
	})
}

func (r *progressRepo) GetUserProgressOverview(userID uint) ([]model.UserProgress, error) {
	var list []model.UserProgress
	err := r.db.Where("user_id = ?", userID).Find(&list).Error
	return list, err
}

func (r *progressRepo) GetLastWatchedEpisode(userID, courseID uint) (*model.UserProgress, error) {
	var prog model.UserProgress
	err := r.db.Table("user_progresses").
		Joins("JOIN episodes ON episodes.id = user_progresses.episode_id").
		Where("user_progresses.user_id = ? AND episodes.course_id = ?", userID, courseID).
		Order("user_progresses.updated_at DESC").
		First(&prog).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &prog, nil
}

func (r *progressRepo) GetPointsLedger(userID uint, limit, offset int) ([]model.PointsLedger, error) {
	var ledger []model.PointsLedger
	query := r.db.Where("user_id = ?", userID).Order("created_at DESC, id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	err := query.Find(&ledger).Error
	return ledger, err
}

// BatchUserProgressSummary aggregates per-user: completed-episode count
// (SUM CASE), total watch seconds, and the most recent updated_at. One query
// regardless of user count. Users with no progress rows are absent from the
// map (callers treat missing as zero).
func (r *progressRepo) BatchUserProgressSummary() (map[uint]UserProgressSummary, error) {
	// NOTE: last_active_at is scanned into a *string rather than *time.Time.
	// SQLite returns MAX(updated_at) as a driver.Value string, and the
	// mattn/go-sqlite3 driver refuses to scan a string column into *time.Time
	// (it only auto-parses timestamps for columns declared with the
	// datetime-ish affinities). Scanning into *time.Time therefore panics the
	// whole query with "unsupported Scan, storing driver.Value type string
	// into type *time.Time", which previously zeroed out every aggregate on
	// the user list. We parse the string back into time.Time in Go instead.
	type row struct {
		UserID            uint
		CompletedEpisodes int64
		TotalWatchSeconds int64
		LastActiveAt      *string
	}
	var rows []row
	err := r.db.Table("user_progresses").
		Select(`
			user_id AS user_id,
			SUM(CASE WHEN is_completed = 1 THEN 1 ELSE 0 END) AS completed_episodes,
			COALESCE(SUM(watch_seconds), 0) AS total_watch_seconds,
			MAX(updated_at) AS last_active_at
		`).
		Group("user_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]UserProgressSummary, len(rows))
	for _, r := range rows {
		var lastActive *time.Time
		if r.LastActiveAt != nil && *r.LastActiveAt != "" {
			// SQLite stores time.Time as "2006-01-02 15:04:05.999999999-07:00"
			// (its own datetime string, NOT RFC3339 — note the space and the
			// fractional seconds). Try the SQLite layout first, then fall back
			// to the two RFC3339 variants for other drivers (postgres/mysql).
			layouts := []string{
				"2006-01-02 15:04:05.999999999-07:00",
				"2006-01-02 15:04:05.999999999",
				time.RFC3339Nano,
				time.RFC3339,
			}
			for _, layout := range layouts {
				if t, perr := time.Parse(layout, *r.LastActiveAt); perr == nil {
					lastActive = &t
					break
				}
			}
			// If none parse, leave lastActive nil (treated as "never active"
			// downstream) rather than failing the whole batch.
		}
		out[r.UserID] = UserProgressSummary{
			CompletedEpisodes: r.CompletedEpisodes,
			TotalWatchSeconds: r.TotalWatchSeconds,
			LastActiveAt:      lastActive,
		}
	}
	return out, nil
}

// BatchPoints loads every user's UserPoint in one query. Absent for users
// with no points row (shouldn't happen since Create seeds one, but be safe).
func (r *progressRepo) BatchPoints() (map[uint]model.UserPoint, error) {
	var pts []model.UserPoint
	if err := r.db.Find(&pts).Error; err != nil {
		return nil, err
	}
	out := make(map[uint]model.UserPoint, len(pts))
	for _, p := range pts {
		out[p.UserID] = p
	}
	return out, nil
}
