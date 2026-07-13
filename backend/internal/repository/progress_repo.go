package repository

import (
	"errors"
	"studyquest/backend/internal/appclock"
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
	// WithTx returns a copy of this repository bound to an in-progress
	// transaction. Lets callers run multiple repo methods atomically (e.g. a
	// service that writes progress AND awards points in the same tx) without
	// giving every repo method a *gorm.DB parameter. The returned repo shares
	// no mutable state with the original.
	WithTx(tx *gorm.DB) ProgressRepository
	GetProgress(userID, episodeID uint) (*model.UserProgress, error)
	SaveProgress(progress *model.UserProgress) error
	// UpsertAndAccumulateWatch atomically adds deltaWatchSec to a progress
	// row's watch_seconds and sets its last_position_seconds, creating the row
	// if missing. Unlike the old GetProgress → mutate → SaveProgress sequence
	// (which lost watch_seconds under concurrent reports — the player's 5s
	// timer and the quiz ping can interleave), this performs the increment in
	// a single UPDATE so concurrent writers never clobber each other's delta.
	// It does NOT touch IsCompleted (completion gating still happens via
	// MarkCompleted in the service layer). Returns the post-update row.
	UpsertAndAccumulateWatch(userID, episodeID uint, positionSec, deltaWatchSec int) (*model.UserProgress, error)
	// MarkCompleted flips is_completed to 1 for a (user, episode) WITHOUT
	// rewriting watch_seconds — completion is the one place the service layer
	// still writes a progress row, and doing it via SaveProgress (full-row
	// write) would re-introduce the very lost-update race UpsertAndAccumulateWatch
	// fixed: two requests crossing the 90% threshold near-simultaneously would
	// each Save a stale watch_seconds and clobber the atomic increment. This
	// single-column UPDATE touches only is_completed, so watch_seconds is never
	// regressed by the completion path.
	MarkCompleted(userID, episodeID uint) error
	// HasCompletedToday returns true if the user already has at least one
	// is_completed=1 row whose updated_at falls on today's business-calendar
	// day. Used for the daily-first-completion bonus.
	HasCompletedToday(userID uint) (bool, error)
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

	// Dashboard aggregates.
	// SumTotalWatchSeconds sums watch_seconds across ALL users (platform-wide
	// total learning time). Used by the admin dashboard headline cards.
	SumTotalWatchSeconds() (int64, error)
	// CountCompletedEpisodes counts completed progress rows platform-wide.
	CountCompletedEpisodes() (int64, error)
	// CountActiveUsersSince returns how many distinct users have a progress
	// row updated since the given time (e.g. "today" / "last 7 days").
	CountActiveUsersSince(since time.Time) (int64, error)
	// RecentDailyWatchSeconds returns per-day summed watch_seconds for the last
	// `days` days, oldest first — the dashboard's "learning trend" chart.
	RecentDailyWatchSeconds(days int) ([]DailyWatch, error)
	// TopUsersByWatchSeconds returns the top-N users by total watch_seconds,
	// for the dashboard "most active users" leaderboard.
	TopUsersByWatchSeconds(limit int) ([]UserLeaderboardRow, error)
	// TopCoursesByCompletions returns the top-N courses by completed-episode
	// count, for the dashboard "popular courses" card.
	TopCoursesByCompletions(limit int) ([]CourseLeaderboardRow, error)
}

// DailyWatch is one day's aggregate learning time for the dashboard trend chart.
type DailyWatch struct {
	Date    string `json:"date"`
	Seconds int64  `json:"seconds"`
}

// UserLeaderboardRow is one row of the "most active users" leaderboard.
type UserLeaderboardRow struct {
	UserID      uint   `json:"user_id"`
	WatchSeconds int64 `json:"watch_seconds"`
}

// CourseLeaderboardRow is one row of the "popular courses" card, keyed by
// course id with a completed-episode count.
type CourseLeaderboardRow struct {
	CourseID            uint   `json:"course_id"`
	CompletedEpisodes   int64  `json:"completed_episodes"`
}

type progressRepo struct {
	db *gorm.DB
}

// NewProgressRepository creates an instance of ProgressRepository.
func NewProgressRepository(db *gorm.DB) ProgressRepository {
	return &progressRepo{db: db}
}

// WithTx returns a repo backed by tx instead of the connection's own DB.
// Used by services to run several writes atomically across repos.
func (r *progressRepo) WithTx(tx *gorm.DB) ProgressRepository {
	return &progressRepo{db: tx}
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

// UpsertAndAccumulateWatch atomically increments watch_seconds by delta and
// sets last_position_seconds. Uses SQLite's INSERT ... ON CONFLICT so the
// read-modify-write race between concurrent reports (5s player timer vs quiz
// ping) can't lose deltas: the increment happens inside a single statement.
// unlocked_at is seeded on first insert; is_completed is left at its existing
// value (completion gating lives in the service layer via SaveProgress).
func (r *progressRepo) UpsertAndAccumulateWatch(userID, episodeID uint, positionSec, deltaWatchSec int) (*model.UserProgress, error) {
	// Clamp: a single report should never carry a huge delta (player sends ~5s
	// at a time). Anything larger smells like a bug or abuse, so cap it.
	if deltaWatchSec < 0 {
		deltaWatchSec = 0
	}
	if deltaWatchSec > 600 {
		deltaWatchSec = 600
	}
	if positionSec < 0 {
		positionSec = 0
	}

	// ON CONFLICT upsert keyed on (user_id, episode_id). The uniqueIndex on
	// those columns guarantees the conflict target.
	now := time.Now()
	err := r.db.Exec(`
		INSERT INTO user_progresses (user_id, episode_id, last_position_seconds, watch_seconds, is_completed, unlocked_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?)
		ON CONFLICT(user_id, episode_id) DO UPDATE SET
			watch_seconds = watch_seconds + excluded.watch_seconds,
			last_position_seconds = excluded.last_position_seconds,
			updated_at = excluded.updated_at
	`, userID, episodeID, positionSec, deltaWatchSec, now, now, now).Error
	if err != nil {
		return nil, err
	}

	// Re-read to return the authoritative post-update row (watch_seconds is
	// computed server-side, so we can't just reuse the input).
	var prog model.UserProgress
	if err := r.db.Where("user_id = ? AND episode_id = ?", userID, episodeID).First(&prog).Error; err != nil {
		return nil, err
	}
	return &prog, nil
}

// MarkCompleted sets is_completed=1 for a (user, episode) via a single-column
// UPDATE. It deliberately does NOT touch watch_seconds: the completion path is
// the one spot the service still writes a progress row, and a full-row Save
// there would let two requests crossing the 90% threshold near-simultaneously
// each write a stale watch_seconds and clobber the atomic increment from
// UpsertAndAccumulateWatch. Touching only is_completed means watch_seconds is
// never regressed here, regardless of concurrent completions.
func (r *progressRepo) MarkCompleted(userID, episodeID uint) error {
	return r.db.Model(&model.UserProgress{}).
		Where("user_id = ? AND episode_id = ?", userID, episodeID).
		Update("is_completed", 1).Error
}

// HasCompletedToday checks whether the user has any is_completed=1 row whose
// updated_at is on today's business-calendar day. The check runs BEFORE
// MarkCompleted in the same request, so on the FIRST completion of the day it
// returns false (→ daily-first bonus applies); on subsequent completions it
// returns true (→ no bonus).
func (r *progressRepo) HasCompletedToday(userID uint) (bool, error) {
	offsetMin := businessZoneOffsetMinutes()
	mod := sqliteOffsetModifier(offsetMin)
	todayStr := appclock.Now().Format("2006-01-02")
	var count int64
	err := r.db.Model(&model.UserProgress{}).
		Where("user_id = ? AND is_completed = 1 AND strftime('%Y-%m-%d', datetime(updated_at, ?)) = ?",
			userID, mod, todayStr).
		Count(&count).Error
	return count > 0, err
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
	// When r.db is already a transaction (via WithTx), open a savepoint; when
	// it's the bare connection, start a fresh transaction. Either way the two
	// writes (ledger + balance) stay atomic.
	return r.db.Transaction(func(tx *gorm.DB) error {
		return addPointsInTx(tx, ledger)
	})
}

// addPointsInTx performs the ledger insert + balance update against the given
// session. Extracted so it can run inside an outer transaction without nesting
// a second one.
func addPointsInTx(tx *gorm.DB, ledger *model.PointsLedger) error {
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

// --- Dashboard aggregates ---

// SumTotalWatchSeconds sums every user's watch_seconds platform-wide. Used by
// the dashboard "total learning time" headline card.
func (r *progressRepo) SumTotalWatchSeconds() (int64, error) {
	var sum int64
	err := r.db.Model(&model.UserProgress{}).
		Select("COALESCE(SUM(watch_seconds), 0)").Scan(&sum).Error
	return sum, err
}

// CountCompletedEpisodes counts progress rows marked completed platform-wide.
func (r *progressRepo) CountCompletedEpisodes() (int64, error) {
	var count int64
	err := r.db.Model(&model.UserProgress{}).Where("is_completed = 1").Count(&count).Error
	return count, err
}

// CountActiveUsersSince returns the distinct count of users with a watch event
// at/after `since`. Used for "active today" / "active this week".
//
// Sourced from watch_events (not user_progresses) so the count reflects actual
// viewing activity on/after `since`. The old version read user_progresses and
// would miscount: a row touched yesterday and re-touched today counts as
// "today-active" even though today's touch might itself be a different episode
// whose event isn't captured by the aggregate row's single updated_at.
func (r *progressRepo) CountActiveUsersSince(since time.Time) (int64, error) {
	var count int64
	err := r.db.Model(&model.WatchEvent{}).
		Where("started_at >= ?", since).
		Distinct("user_id").
		Count(&count).Error
	return count, err
}

// RecentDailyWatchSeconds sums watch-event durations grouped by the business-
// calendar day of the event's started_at, for the last `days` days, oldest
// first. Powers the dashboard learning-trend chart.
//
// Sourced from watch_events.duration_seconds (per-heartbeat deltas) rather
// than user_progresses.watch_seconds (a per-(user,episode) lifetime total).
// The old version bucketed the lifetime total by the row's last-touch day,
// which dumped a video's entire accumulated seconds into whichever day it was
// last touched — wildly over-counting that day and under-counting the days the
// watching actually happened. The event log records each delta with its own
// timestamp, so day-bucketing is now correct.
func (r *progressRepo) RecentDailyWatchSeconds(days int) ([]DailyWatch, error) {
	type row struct {
		Date    string
		Seconds int64
	}
	// Bucket by BUSINESS-zone day so the chart's "today" matches the user's
	// calendar (UTC bucketing would split a Beijing day across two bars).
	mod := sqliteOffsetModifier(businessZoneOffsetMinutes())
	since := appclock.Now().AddDate(0, 0, -days+1)
	sinceMidnight := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location())
	var rows []row
	err := r.db.Model(&model.WatchEvent{}).
		Select("strftime('%Y-%m-%d', datetime(started_at, ?)) AS date, COALESCE(SUM(duration_seconds), 0) AS seconds", mod).
		Where("started_at >= ?", sinceMidnight.UTC()).
		Group("date").
		Order("date asc").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]DailyWatch, len(rows))
	for i, r := range rows {
		out[i] = DailyWatch{Date: r.Date, Seconds: r.Seconds}
	}
	return out, nil
}

// TopUsersByWatchSeconds returns the top-N users by total watch_seconds,
// descending. Used by the dashboard "most active users" leaderboard.
func (r *progressRepo) TopUsersByWatchSeconds(limit int) ([]UserLeaderboardRow, error) {
	if limit <= 0 {
		limit = 5
	}
	var rows []UserLeaderboardRow
	err := r.db.Model(&model.UserProgress{}).
		Select("user_id, SUM(watch_seconds) AS watch_seconds").
		Group("user_id").
		Order("watch_seconds DESC").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}

// TopCoursesByCompletions returns the top-N courses by completed-episode
// count (user_progresses.is_completed=1 JOIN episodes ON course_id),
// descending. Used by the dashboard "popular courses" card.
func (r *progressRepo) TopCoursesByCompletions(limit int) ([]CourseLeaderboardRow, error) {
	if limit <= 0 {
		limit = 5
	}
	var rows []CourseLeaderboardRow
	err := r.db.Table("user_progresses").
		Select("episodes.course_id AS course_id, COUNT(*) AS completed_episodes").
		Joins("JOIN episodes ON episodes.id = user_progresses.episode_id").
		Where("user_progresses.is_completed = 1").
		Group("episodes.course_id").
		Order("completed_episodes DESC").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}
