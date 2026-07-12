package repository

import (
	"errors"
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// EntertainmentRepository tracks playback of entertainment videos. Physically
// separate from ProgressRepository so learning-stat queries are zero-contaminated.
// The upsert accumulates watch_seconds atomically (same INSERT ... ON CONFLICT
// DO UPDATE pattern as UpsertAndAccumulateWatch), and also stores the resume
// position so entertainment videos pick up where the kid left off — identical
// playback UX to learning videos, just without completion/points/badges.
type EntertainmentRepository interface {
	WithTx(tx *gorm.DB) EntertainmentRepository
	// UpsertProgress atomically accumulates watch_seconds and overwrites the
	// resume position. Mirrors ProgressRepository.UpsertAndAccumulateWatch.
	UpsertProgress(userID, episodeID uint, positionSec, deltaWatchSec int) (*model.EntertainmentProgress, error)
	GetProgress(userID, episodeID uint) (*model.EntertainmentProgress, error)
	// GetLastEpisodeID returns the most recently touched entertainment episode
	// for a user, for "continue watching". Returns (0, nil) if none.
	GetLastEpisodeID(userID uint) (uint, error)
	// Watch-time aggregates for the future time-limit feature.
	GetTodayWatchSeconds(userID uint) (int64, error)
	GetWeekWatchSeconds(userID uint) (int64, error)
}

type entertainmentRepo struct {
	db *gorm.DB
}

func NewEntertainmentRepository(db *gorm.DB) EntertainmentRepository {
	return &entertainmentRepo{db: db}
}

func (r *entertainmentRepo) WithTx(tx *gorm.DB) EntertainmentRepository {
	return &entertainmentRepo{db: tx}
}

func (r *entertainmentRepo) UpsertProgress(userID, episodeID uint, positionSec, deltaWatchSec int) (*model.EntertainmentProgress, error) {
	if positionSec < 0 {
		positionSec = 0
	}
	if deltaWatchSec < 0 {
		deltaWatchSec = 0
	}
	now := time.Now()
	err := r.db.Exec(`
		INSERT INTO entertainment_progresses (user_id, episode_id, last_position_seconds, watch_seconds, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, episode_id) DO UPDATE SET
			last_position_seconds = excluded.last_position_seconds,
			watch_seconds = entertainment_progresses.watch_seconds + excluded.watch_seconds,
			updated_at = excluded.updated_at
	`, userID, episodeID, positionSec, deltaWatchSec, now, now).Error
	if err != nil {
		return nil, err
	}
	var prog model.EntertainmentProgress
	if err := r.db.Where("user_id = ? AND episode_id = ?", userID, episodeID).First(&prog).Error; err != nil {
		return nil, err
	}
	return &prog, nil
}

func (r *entertainmentRepo) GetProgress(userID, episodeID uint) (*model.EntertainmentProgress, error) {
	var prog model.EntertainmentProgress
	if err := r.db.Where("user_id = ? AND episode_id = ?", userID, episodeID).First(&prog).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &prog, nil
}

func (r *entertainmentRepo) GetLastEpisodeID(userID uint) (uint, error) {
	var prog model.EntertainmentProgress
	if err := r.db.Where("user_id = ?", userID).Order("updated_at desc").First(&prog).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return prog.EpisodeID, nil
}

// GetTodayWatchSeconds sums watch_seconds for episodes touched today (business
// calendar day). Used by the future daily-cap time-limit feature.
func (r *entertainmentRepo) GetTodayWatchSeconds(userID uint) (int64, error) {
	mod := sqliteOffsetModifier(businessZoneOffsetMinutes())
	today := appclock.Now()
	todayStr := today.Format("2006-01-02")
	var sum int64
	err := r.db.Model(&model.EntertainmentProgress{}).
		Where("user_id = ? AND strftime('%Y-%m-%d', datetime(updated_at, ?)) = ?",
			userID, mod, todayStr).
		Select("COALESCE(SUM(watch_seconds), 0)").Scan(&sum).Error
	return sum, err
}

// GetWeekWatchSeconds sums watch_seconds for episodes touched in the last 7
// business calendar days. Used by the future weekly-cap time-limit feature.
func (r *entertainmentRepo) GetWeekWatchSeconds(userID uint) (int64, error) {
	mod := sqliteOffsetModifier(businessZoneOffsetMinutes())
	since := appclock.Now().AddDate(0, 0, -6) // last 7 days including today
	sinceMidnight := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location())
	var sum int64
	err := r.db.Model(&model.EntertainmentProgress{}).
		Where("user_id = ? AND updated_at >= ?", userID, sinceMidnight.UTC()).
		Select("COALESCE(SUM(watch_seconds), 0)").Scan(&sum).Error
	_ = mod
	return sum, err
}
