package repository

import (
	"errors"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// WatchEventRepository persists per-session viewing events for the watch-
// history feature. Events are written alongside the aggregate progress tables
// (UserProgress / EntertainmentProgress) on every heartbeat, then read here
// for the admin per-day timeline + heatmap.
type WatchEventRepository interface {
	// AppendOrMerge records one heartbeat. If the most recent event for this
	// (user, episode) ended within mergeWindow of `now`, the heartbeat is
	// folded into that row (EndedAt = now, DurationSeconds += deltaSec);
	// otherwise a new row is inserted. mergeWindow <= 0 disables merging.
	//
	// This is NOT wrapped in a transaction — the caller (progress service) may
	// wrap it with the aggregate upsert if it wants atomicity. A failed merge
	// just degrades to a new row, so missing-transaction is safe-by-design.
	AppendOrMerge(userID, episodeID, courseID uint, contentType string, deltaSec int, now time.Time, mergeWindow time.Duration) (*model.WatchEvent, error)

	// ListByUserAndDay returns the user's events whose StartedAt falls on the
	// given business-calendar day (YYYY-MM-DD), ascending by StartedAt. Used by
	// the "selected day detail" view.
	ListByUserAndDay(userID uint, day string) ([]model.WatchEvent, error)

	// DailyDurationsInRange returns, for each business-calendar day in
	// [from, to), the sum of DurationSeconds for the user. `from`/`to` are
	// business-zone instants (callers pass midnight boundaries). The map is
	// keyed by YYYY-MM-DD; days with no events are absent (caller treats
	// missing as zero). Used by the month heatmap.
	DailyDurationsInRange(userID uint, from, to time.Time) (map[string]int64, error)

	// DeleteByUser removes all of a user's events. Called from the user-delete
	// transaction so a deleted user leaves no history.
	DeleteByUser(userID uint) error
}

type watchEventRepo struct {
	db *gorm.DB
}

// NewWatchEventRepository creates an instance of WatchEventRepository.
func NewWatchEventRepository(db *gorm.DB) WatchEventRepository {
	return &watchEventRepo{db: db}
}

func (r *watchEventRepo) AppendOrMerge(userID, episodeID, courseID uint, contentType string, deltaSec int, now time.Time, mergeWindow time.Duration) (*model.WatchEvent, error) {
	if deltaSec < 0 {
		deltaSec = 0
	}

	// Look for a recent event to merge into. The composite index
	// (user_id, episode_id, started_at) makes this cheap.
	if mergeWindow > 0 {
		var last model.WatchEvent
		err := r.db.Where("user_id = ? AND episode_id = ?", userID, episodeID).
			Order("started_at DESC").
			Take(&last).Error
		if err == nil && !last.EndedAt.Before(now.Add(-mergeWindow)) {
			// Merge: bump EndedAt to now, add the delta. Don't move StartedAt —
			// the row's span grows from the front only.
			err := r.db.Model(&model.WatchEvent{}).Where("id = ?", last.ID).
				Updates(map[string]any{
					"ended_at":         now,
					"duration_seconds": last.DurationSeconds + deltaSec,
				}).Error
			if err != nil {
				return nil, err
			}
			last.EndedAt = now
			last.DurationSeconds += deltaSec
			return &last, nil
		}
		// Any non-ErrRecordNotFound error is a real failure; surface it.
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	ev := &model.WatchEvent{
		UserID:          userID,
		EpisodeID:       episodeID,
		CourseID:        courseID,
		ContentType:     contentType,
		StartedAt:       now,
		EndedAt:         now,
		DurationSeconds: deltaSec,
	}
	if err := r.db.Create(ev).Error; err != nil {
		return nil, err
	}
	return ev, nil
}

func (r *watchEventRepo) ListByUserAndDay(userID uint, day string) ([]model.WatchEvent, error) {
	// Day-bucket in the business timezone (storage is UTC). See
	// badge_repo.go's sqliteOffsetModifier for why we don't use 'localtime'.
	mod := sqliteOffsetModifier(businessZoneOffsetMinutes())
	var events []model.WatchEvent
	err := r.db.Where("user_id = ? AND strftime('%Y-%m-%d', datetime(started_at, ?)) = ?", userID, mod, day).
		Order("started_at ASC").
		Find(&events).Error
	return events, err
}

func (r *watchEventRepo) DailyDurationsInRange(userID uint, from, to time.Time) (map[string]int64, error) {
	mod := sqliteOffsetModifier(businessZoneOffsetMinutes())
	type row struct {
		Date    string
		Seconds int64
	}
	var rows []row
	err := r.db.Model(&model.WatchEvent{}).
		Select("strftime('%Y-%m-%d', datetime(started_at, ?)) AS date, COALESCE(SUM(duration_seconds), 0) AS seconds", mod).
		Where("user_id = ? AND started_at >= ? AND started_at < ?", userID, from.UTC(), to.UTC()).
		Group("date").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Date] = r.Seconds
	}
	return out, nil
}

func (r *watchEventRepo) DeleteByUser(userID uint) error {
	return r.db.Where("user_id = ?", userID).Delete(&model.WatchEvent{}).Error
}
