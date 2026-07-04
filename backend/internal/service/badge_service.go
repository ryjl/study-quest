package service

import (
	"fmt"
	"log"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// BadgeService handles badge CRUD, seeding, and event-based rule evaluation.
type BadgeService interface {
	List() ([]model.Badge, error)
	FindByID(id uint) (*model.Badge, error)
	Create(badge *model.Badge) error
	Update(badge *model.Badge) error
	Delete(id uint) error

	ListUserBadges(userID uint) ([]model.Badge, error)
	EvaluateRules(userID uint) ([]model.Badge, error) // Returns newly unlocked badges
	SeedDefaultBadges() error
}

type badgeService struct {
	badgeRepo    repository.BadgeRepository
	progressRepo repository.ProgressRepository
}

// NewBadgeService creates an instance of BadgeService.
func NewBadgeService(br repository.BadgeRepository, pr repository.ProgressRepository) BadgeService {
	return &badgeService{
		badgeRepo:    br,
		progressRepo: pr,
	}
}

func (s *badgeService) List() ([]model.Badge, error) {
	return s.badgeRepo.List()
}

func (s *badgeService) FindByID(id uint) (*model.Badge, error) {
	return s.badgeRepo.FindByID(id)
}

func (s *badgeService) Create(badge *model.Badge) error {
	return s.badgeRepo.Create(badge)
}

func (s *badgeService) Update(badge *model.Badge) error {
	return s.badgeRepo.Update(badge)
}

func (s *badgeService) Delete(id uint) error {
	return s.badgeRepo.Delete(id)
}

func (s *badgeService) ListUserBadges(userID uint) ([]model.Badge, error) {
	return s.badgeRepo.ListUserBadges(userID)
}

func (s *badgeService) EvaluateRules(userID uint) ([]model.Badge, error) {
	badges, err := s.badgeRepo.List()
	if err != nil {
		return nil, err
	}

	var newlyUnlocked []model.Badge

	for _, badge := range badges {
		unlocked, err := s.badgeRepo.HasUnlocked(userID, badge.ID)
		if err != nil || unlocked {
			continue // Already unlocked or query failed
		}

		shouldUnlock := false
		switch badge.RuleType {
		case "watch_duration":
			minutes, err := s.badgeRepo.GetTotalWatchDurationMinutes(userID)
			if err == nil && minutes >= badge.Threshold {
				shouldUnlock = true
			}
		case "consecutive_days":
			days, err := s.badgeRepo.GetConsecutiveActiveDays(userID)
			if err == nil && days >= badge.Threshold {
				shouldUnlock = true
			}
		case "subject_count":
			count, err := s.badgeRepo.GetCompletedEpisodesCountBySubject(userID, badge.RuleTarget)
			if err == nil && count >= badge.Threshold {
				shouldUnlock = true
			}
		case "night_owl_count":
			count, err := s.badgeRepo.GetNightOwlCompletedCount(userID)
			if err == nil && count >= badge.Threshold {
				shouldUnlock = true
			}
		case "points_earned":
			pt, err := s.progressRepo.GetPoints(userID)
			if err == nil && pt != nil && pt.TotalEarnedPoints >= badge.Threshold {
				shouldUnlock = true
			}
		}

		if shouldUnlock {
			if err := s.badgeRepo.UnlockBadge(userID, badge.ID); err == nil {
				newlyUnlocked = append(newlyUnlocked, badge)

				// Log points ledger entry for unlocking achievement
				ledger := &model.PointsLedger{
					UserID:       userID,
					ChangeAmount: 0, // Unlocking achievement itself doesn't award points, or could award points
					ReasonType:   "badge_unlocked",
					Description:  fmt.Sprintf("解锁荣誉徽章：%s (%s)", badge.Title, badge.Description),
				}
				_ = s.progressRepo.AddPoints(ledger)
			}
		}
	}

	return newlyUnlocked, nil
}

func (s *badgeService) SeedDefaultBadges() error {
	list, err := s.badgeRepo.List()
	if err != nil {
		return err
	}
	if len(list) > 0 {
		return nil // Already seeded
	}

	defaults := []model.Badge{
		{
			Code:        "seven_days_pioneer",
			Title:       "七日先锋",
			Description: "连续 7 天产生学习活跃行为",
			IconName:    "badge_streak_7",
			RuleType:    "consecutive_days",
			Threshold:   7,
		},
		{
			Code:        "math_expert",
			Title:       "数学达人",
			Description: "累计完成 5 个数学课时挑战",
			IconName:    "badge_math",
			RuleType:    "subject_count",
			RuleTarget:  "math",
			Threshold:   5,
		},
		{
			Code:        "english_star",
			Title:       "英语之星",
			Description: "累计完成 5 个英语课时挑战",
			IconName:    "badge_english",
			RuleType:    "subject_count",
			RuleTarget:  "english",
			Threshold:   5,
		},
		{
			Code:        "night_owl",
			Title:       "夜猫学者",
			Description: "在晚上 22:00 之后累计学完 3 个课时",
			IconName:    "badge_night_owl",
			RuleType:    "night_owl_count",
			Threshold:   3,
		},
		{
			Code:        "first_blood",
			Title:       "首战告捷",
			Description: "累计视频学习时长达到 1 分钟",
			IconName:    "badge_first_blood",
			RuleType:    "watch_duration",
			Threshold:   1,
		},
	}

	for _, badge := range defaults {
		if err := s.badgeRepo.Create(&badge); err != nil {
			log.Printf("Failed to seed badge %s: %v", badge.Code, err)
		}
	}

	return nil
}
