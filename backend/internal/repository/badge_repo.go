package repository

import (
	"errors"
	"fmt"
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// businessZoneOffsetMinutes returns the UTC offset of the active business zone
// (appclock.Zone), in minutes east of UTC. Used to shift stored UTC timestamps
// into business-zone time inside SQLite via a '+HH:MM' modifier, so the DB and
// Go agree on "what day/hour is this" without relying on SQLite's 'localtime'
// (which used the DB process zone and diverged from Go in containers).
func businessZoneOffsetMinutes() int {
	_, off := appclock.Now().Zone()
	return off / 60
}

// sqliteOffsetModifier turns a signed minute offset into a SQLite datetime()
// modifier string like '+08:00' or '-05:30'.
func sqliteOffsetModifier(offsetMin int) string {
	sign := "+"
	if offsetMin < 0 {
		sign = "-"
		offsetMin = -offsetMin
	}
	h := offsetMin / 60
	m := offsetMin % 60
	return fmt.Sprintf("%s%02d:%02d", sign, h, m)
}

// BadgeRepository handles SQL operations for Badge and UserBadge entities.
type BadgeRepository interface {
	List() ([]model.Badge, error)
	FindByID(id uint) (*model.Badge, error)
	FindByCode(code string) (*model.Badge, error)
	Create(badge *model.Badge) error
	Update(badge *model.Badge) error
	Delete(id uint) error

	ListUserBadges(userID uint) ([]model.Badge, error)
	HasUnlocked(userID, badgeID uint) (bool, error)
	UnlockBadge(userID, badgeID uint) error

	// Rule verification helper aggregates
	GetTotalWatchDurationMinutes(userID uint) (int, error)
	GetCompletedEpisodesCountBySubject(userID uint, subject string) (int, error)
	GetNightOwlCompletedCount(userID uint) (int, error)
	GetConsecutiveActiveDays(userID uint) (int, error)
	// GetDistinctSubjectCompletedCount returns how many DISTINCT subjects the
	// user has at least one completed episode in. Powers the
	// distinct_subject_count rule type (e.g. "博学多闻" badge).
	GetDistinctSubjectCompletedCount(userID uint) (int, error)

	// Batch aggregates for the admin user list.
	// BatchUnlockedBadgeCounts returns user_id → unlocked badge count in one
	// query. CountBadges returns the global badge total (denominator for "X/Y").
	BatchUnlockedBadgeCounts() (map[uint]int64, error)
	CountBadges() (int64, error)
}

type badgeRepo struct {
	db *gorm.DB
}

// NewBadgeRepository creates an instance of BadgeRepository.
func NewBadgeRepository(db *gorm.DB) BadgeRepository {
	return &badgeRepo{db: db}
}

func (r *badgeRepo) List() ([]model.Badge, error) {
	var badges []model.Badge
	err := r.db.Order("id ASC").Find(&badges).Error
	return badges, err
}

func (r *badgeRepo) FindByID(id uint) (*model.Badge, error) {
	var badge model.Badge
	if err := r.db.First(&badge, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &badge, nil
}

func (r *badgeRepo) FindByCode(code string) (*model.Badge, error) {
	var badge model.Badge
	if err := r.db.Where("code = ?", code).First(&badge).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &badge, nil
}

func (r *badgeRepo) Create(badge *model.Badge) error {
	return r.db.Create(badge).Error
}

func (r *badgeRepo) Update(badge *model.Badge) error {
	return r.db.Save(badge).Error
}

func (r *badgeRepo) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Clean up user unlocked relationships
		if err := tx.Delete(&model.UserBadge{}, "badge_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Badge{}, id).Error
	})
}

func (r *badgeRepo) ListUserBadges(userID uint) ([]model.Badge, error) {
	var badges []model.Badge
	err := r.db.Joins("JOIN user_badges ON user_badges.badge_id = badges.id").
		Where("user_badges.user_id = ?", userID).
		Order("user_badges.unlocked_at DESC").
		Find(&badges).Error
	return badges, err
}

func (r *badgeRepo) HasUnlocked(userID, badgeID uint) (bool, error) {
	var count int64
	err := r.db.Model(&model.UserBadge{}).
		Where("user_id = ? AND badge_id = ?", userID, badgeID).
		Count(&count).Error
	return count > 0, err
}

func (r *badgeRepo) UnlockBadge(userID, badgeID uint) error {
	ub := model.UserBadge{
		UserID:     userID,
		BadgeID:    badgeID,
		UnlockedAt: time.Now(),
	}
	return r.db.Create(&ub).Error
}

func (r *badgeRepo) GetTotalWatchDurationMinutes(userID uint) (int, error) {
	var sum int64
	err := r.db.Model(&model.UserProgress{}).
		Where("user_id = ?", userID).
		Select("COALESCE(SUM(watch_seconds), 0)").
		Row().Scan(&sum)
	return int(sum / 60), err
}

func (r *badgeRepo) GetCompletedEpisodesCountBySubject(userID uint, subject string) (int, error) {
	var count int64
	err := r.db.Table("user_progresses").
		Joins("JOIN episodes ON episodes.id = user_progresses.episode_id").
		Joins("JOIN courses ON courses.id = episodes.course_id").
		Joins("JOIN subjects ON subjects.id = courses.subject_id").
		Where("user_progresses.user_id = ? AND user_progresses.is_completed = 1 AND subjects.key = ?", userID, subject).
		Count(&count).Error
	return int(count), err
}

func (r *badgeRepo) GetNightOwlCompletedCount(userID uint) (int, error) {
	// "深夜" is defined in the BUSINESS timezone (Asia/Shanghai), not UTC: for
	// a Chinese student, 22:00 Beijing is 14:00 UTC, so we must shift stored
	// UTC timestamps into the business zone before asking "what hour is this?".
	// SQLite's 'localtime' modifier used the DB process zone, which diverged
	// from Go's time.Now() in containers and fired at the wrong hours; instead
	// we apply the business-zone offset explicitly via a Go-computed modifier.
	offsetMin := businessZoneOffsetMinutes()
	return r.countNightOwl(userID, offsetMin)
}

// countNightOwl is the offset-parameterized core, separated so tests can pass
// a fixed offset (via the appclock injection) and get deterministic results
// regardless of where the test runs.
func (r *badgeRepo) countNightOwl(userID uint, offsetMin int) (int, error) {
	var count int64
	// Shift the stored UTC instant by the business offset, then take the hour.
	// '22:00–04:59' business-time → night-owl window.
	mod := sqliteOffsetModifier(offsetMin)
	err := r.db.Model(&model.UserProgress{}).
		Where("user_id = ? AND is_completed = 1 AND (CAST(strftime('%H', datetime(updated_at, ?)) AS INTEGER) >= 22 OR CAST(strftime('%H', datetime(updated_at, ?)) AS INTEGER) < 5)", userID, mod, mod).
		Count(&count).Error
	return int(count), err
}

func (r *badgeRepo) GetConsecutiveActiveDays(userID uint) (int, error) {
	// Streak is computed in the BUSINESS timezone: "today" for a Chinese
	// student is the Beijing calendar day, not the UTC day. We shift stored
	// UTC timestamps by the business offset inside SQLite (replacing the old
	// 'localtime' modifier, which used the DB process zone and could disagree
	// with Go's time.Now()), and compare against today/yesterday derived from
	// the same business zone (appclock). Both sides now read the same clock.
	offsetMin := businessZoneOffsetMinutes()
	mod := sqliteOffsetModifier(offsetMin)

	var dates []string
	// datetime(created_at, '+08:00') converts the UTC instant to business-zone
	// wall time; strftime then extracts the business calendar day.
	err := r.db.Model(&model.PointsLedger{}).
		Select("DISTINCT strftime('%Y-%m-%d', datetime(created_at, ?)) as active_date", mod).
		Where("user_id = ?", userID).
		Order("active_date DESC").
		Find(&dates).Error
	if err != nil {
		return 0, err
	}
	if len(dates) == 0 {
		return 0, nil
	}

	streak := 0
	now := appclock.Now()
	todayStr := now.Format("2006-01-02")
	yesterdayStr := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Verify if the latest activity date is today or yesterday
	if dates[0] != todayStr && dates[0] != yesterdayStr {
		return 0, nil
	}

	// Parse dates in the business zone so day arithmetic isn't UTC-skewed.
	zone := appclock.Zone()
	currentDate, err := time.ParseInLocation("2006-01-02", dates[0], zone)
	if err != nil {
		return 0, err
	}
	streak = 1

	for i := 1; i < len(dates); i++ {
		nextDate, err := time.ParseInLocation("2006-01-02", dates[i], zone)
		if err != nil {
			continue
		}
		// Exactly one calendar day earlier = consecutive. Use a small tolerance
		// (23.5–24.5h) to absorb DST edges; China has no DST so this is exactly
		// 24h in practice.
		diff := currentDate.Sub(nextDate).Hours()
		if diff > 23.5 && diff <= 24.5 {
			streak++
			currentDate = nextDate
		} else {
			break
		}
	}
	return streak, nil
}

// GetDistinctSubjectCompletedCount counts how many distinct subjects the user
// has completed at least one episode in. Joins user_progresses → episodes →
// courses → subjects and counts DISTINCT subject ids (only completed rows).
func (r *badgeRepo) GetDistinctSubjectCompletedCount(userID uint) (int, error) {
	var count int64
	err := r.db.Table("user_progresses").
		Joins("JOIN episodes ON episodes.id = user_progresses.episode_id").
		Joins("JOIN courses ON courses.id = episodes.course_id").
		Where("user_progresses.user_id = ? AND user_progresses.is_completed = 1", userID).
		Distinct("courses.subject_id").
		Count(&count).Error
	return int(count), err
}
func (r *badgeRepo) BatchUnlockedBadgeCounts() (map[uint]int64, error) {
	type row struct {
		UserID uint
		Count  int64
	}
	var rows []row
	err := r.db.Model(&model.UserBadge{}).
		Select("user_id, COUNT(*) AS count").
		Group("user_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]int64, len(rows))
	for _, r := range rows {
		out[r.UserID] = r.Count
	}
	return out, nil
}

// CountBadges returns the total number of defined badges (the denominator for
// "X/Y unlocked").
func (r *badgeRepo) CountBadges() (int64, error) {
	var count int64
	err := r.db.Model(&model.Badge{}).Count(&count).Error
	return count, err
}
