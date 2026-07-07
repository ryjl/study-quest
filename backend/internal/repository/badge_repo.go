package repository

import (
	"errors"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

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
	var count int64
	err := r.db.Model(&model.UserProgress{}).
		Where("user_id = ? AND is_completed = 1 AND (STRFTIME('%H', updated_at, 'localtime') >= '22' OR STRFTIME('%H', updated_at, 'localtime') < '05')", userID).
		Count(&count).Error
	return int(count), err
}

func (r *badgeRepo) GetConsecutiveActiveDays(userID uint) (int, error) {
	var dates []string
	err := r.db.Model(&model.PointsLedger{}).
		Select("DISTINCT STRFTIME('%Y-%m-%d', created_at, 'localtime') as active_date").
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
	todayStr := time.Now().Format("2006-01-02")
	yesterdayStr := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	// Verify if the latest activity date is today or yesterday
	if dates[0] != todayStr && dates[0] != yesterdayStr {
		return 0, nil
	}

	currentDate, _ := time.Parse("2006-01-02", dates[0])
	streak = 1

	for i := 1; i < len(dates); i++ {
		nextDate, err := time.Parse("2006-01-02", dates[i])
		if err != nil {
			continue
		}
		diff := currentDate.Sub(nextDate).Hours()
		if diff <= 24.5 { // approximately 1 day difference (allow timezone edge drift)
			streak++
			currentDate = nextDate
		} else {
			break
		}
	}
	return streak, nil
}

// BatchUnlockedBadgeCounts returns user_id → unlocked badge count in one
// query, for the admin user list's "X/Y 徽章" column.
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
