package service

import (
	"encoding/json"
	"fmt"
	"log"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"

	"gorm.io/gorm"
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
	// RemoveDeprecatedDefaults deletes system-seeded badges retired from the
	// default set (currently night_owl). Only touches pristine IsSystem rows;
	// admin-owned copies are preserved. Safe to call on every boot.
	RemoveDeprecatedDefaults() error
}

type badgeService struct {
	db           *gorm.DB
	badgeRepo    repository.BadgeRepository
	progressRepo repository.ProgressRepository
}

// NewBadgeService creates an instance of BadgeService.
func NewBadgeService(db *gorm.DB, br repository.BadgeRepository, pr repository.ProgressRepository) BadgeService {
	return &badgeService{
		db:           db,
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

// Delete refuses system-seeded badges (IsSystem=true) so the curated default
// set survives; user-created badges are freely deletable.
func (s *badgeService) Delete(id uint) error {
	b, err := s.badgeRepo.FindByID(id)
	if err != nil {
		return err
	}
	if b == nil {
		return nil
	}
	if b.IsSystem {
		return ErrSystemProtected
	}
	return s.badgeRepo.Delete(id)
}

func (s *badgeService) ListUserBadges(userID uint) ([]model.Badge, error) {
	return s.badgeRepo.ListUserBadges(userID)
}

// EvaluateRules walks every badge, evaluates its rule(s) against the user's
// current stats, and unlocks any newly-satisfied badge. A badge with a
// populated RuleJSON is evaluated as a composite rule tree (AND/OR of leaves);
// otherwise it falls back to the legacy single-rule path (RuleType/Target/
// Threshold). Each leaf type maps to one repo aggregate query.
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
		if badge.RuleJSON != "" {
			shouldUnlock = s.evalComposite(userID, badge.RuleJSON)
		} else {
			shouldUnlock = s.evalLeaf(userID, badge.RuleType, badge.RuleTarget, badge.Threshold)
		}

		if shouldUnlock {
			// UnlockBadge + the ledger entry are written in one transaction so a
			// crash between them can't leave a badge unlocked with no ledger
			// record (or vice versa). The old code called UnlockBadge then
			// ignored AddPoints's error with `_ =`, so a ledger failure was
			// invisible while the unlock persisted.
			ledger := &model.PointsLedger{
				UserID:       userID,
				ChangeAmount: 0, // Unlocking an achievement itself awards no points
				ReasonType:   "badge_unlocked",
				Description:  fmt.Sprintf("解锁荣誉徽章：%s (%s)", badge.Title, badge.Description),
			}
			err := s.db.Transaction(func(tx *gorm.DB) error {
				if err := s.badgeRepo.WithTx(tx).UnlockBadge(userID, badge.ID); err != nil {
					return err
				}
				return s.progressRepo.WithTx(tx).AddPoints(ledger)
			})
			if err == nil {
				newlyUnlocked = append(newlyUnlocked, badge)
			}
		}
	}

	return newlyUnlocked, nil
}

// evalLeaf evaluates a single (non-composite) rule against the user's stats.
// Returns false on any aggregate error so a broken query never falsely unlocks.
func (s *badgeService) evalLeaf(userID uint, ruleType, ruleTarget string, threshold int) bool {
	switch ruleType {
	case "watch_duration":
		minutes, err := s.badgeRepo.GetTotalWatchDurationMinutes(userID)
		return err == nil && minutes >= threshold
	case "consecutive_days":
		days, err := s.badgeRepo.GetConsecutiveActiveDays(userID)
		return err == nil && days >= threshold
	case "subject_count":
		count, err := s.badgeRepo.GetCompletedEpisodesCountBySubject(userID, ruleTarget)
		return err == nil && count >= threshold
	case "night_owl_count":
		count, err := s.badgeRepo.GetNightOwlCompletedCount(userID)
		return err == nil && count >= threshold
	case "distinct_subject_count":
		count, err := s.badgeRepo.GetDistinctSubjectCompletedCount(userID)
		return err == nil && count >= threshold
	case "points_earned":
		pt, err := s.progressRepo.GetPoints(userID)
		return err == nil && pt != nil && pt.TotalEarnedPoints >= threshold
	}
	return false
}

// evalComposite parses a CompositeRule JSON tree and evaluates it. A group
// node combines its children with AND (all pass) or OR (any pass); a leaf
// node (no SubRules) delegates to evalLeaf. Malformed JSON or an unknown
// logic defaults to AND-fail (returns false) so a corrupt rule can't unlock.
func (s *badgeService) evalComposite(userID uint, ruleJSON string) bool {
	var root model.CompositeRule
	if err := json.Unmarshal([]byte(ruleJSON), &root); err != nil {
		return false
	}
	return s.evalNode(userID, root)
}

func (s *badgeService) evalNode(userID uint, node model.CompositeRule) bool {
	// Leaf: evaluate directly. A node is a leaf when it has no sub-rules.
	if len(node.SubRules) == 0 {
		return s.evalLeaf(userID, node.Type, node.Target, node.Threshold)
	}
	// Group: combine children.
	switch node.Logic {
	case "or":
		for _, child := range node.SubRules {
			if s.evalNode(userID, child) {
				return true
			}
		}
		return false
	default: // "and" (and any unrecognized logic — fail-closed)
		for _, child := range node.SubRules {
			if !s.evalNode(userID, child) {
				return false
			}
		}
		return true
	}
}

// SeedDefaultBadges populates a curated default badge set on first run.
// Idempotent: skips when any badge already exists.
//
// The "夜猫学者 / night_owl" badge was removed from the defaults because it
// rewards late-night studying — inappropriate for young students. The
// night_owl_count rule TYPE is intentionally kept available so an admin who
// specifically wants it can still create one. For instances that already
// seeded the old night_owl default, RemoveDeprecatedDefaults deletes it so
// existing installs converge to the new set.
//
// Defaults are marked IsSystem so they survive deletion but stay editable.
func (s *badgeService) SeedDefaultBadges() error {
	list, err := s.badgeRepo.List()
	if err != nil {
		return err
	}
	if len(list) > 0 {
		// Already seeded — but still clean up badges deprecated since the last
		// seed run (e.g. night_owl on upgraded installs).
		return s.RemoveDeprecatedDefaults()
	}

	defaults := []model.Badge{
		{
			Code:        "first_blood",
			Title:       "首战告捷",
			Description: "累计视频学习时长达到 1 分钟",
			IconName:    "badge_first_blood",
			RuleType:    "watch_duration",
			Threshold:   1,
			IsSystem:    true,
		},
		{
			Code:        "seven_days_pioneer",
			Title:       "七日先锋",
			Description: "连续 7 天产生学习活跃行为",
			IconName:    "badge_streak_7",
			RuleType:    "consecutive_days",
			Threshold:   7,
			IsSystem:    true,
		},
		{
			Code:        "math_expert",
			Title:       "数学达人",
			Description: "累计完成 5 个数学课时挑战",
			IconName:    "badge_math",
			RuleType:    "subject_count",
			RuleTarget:  "math",
			Threshold:   5,
			IsSystem:    true,
		},
		{
			Code:        "english_star",
			Title:       "英语之星",
			Description: "累计完成 5 个英语课时挑战",
			IconName:    "badge_english",
			RuleType:    "subject_count",
			RuleTarget:  "english",
			Threshold:   5,
			IsSystem:    true,
		},
		{
			Code:        "hard_worker",
			Title:       "勤学小达人",
			Description: "累计视频学习时长达到 60 分钟",
			IconName:    "badge_first_blood",
			RuleType:    "watch_duration",
			Threshold:   60,
			IsSystem:    true,
		},
		{
			Code:        "explorer",
			Title:       "博学多闻",
			Description: "完成 3 个不同科目的课时挑战",
			IconName:    "badge_english",
			RuleType:    "distinct_subject_count",
			Threshold:   3,
			IsSystem:    true,
		},
	}

	for i := range defaults {
		if err := s.badgeRepo.Create(&defaults[i]); err != nil {
			log.Printf("Failed to seed badge %s: %v", defaults[i].Code, err)
		}
	}

	return nil
}

// RemoveDeprecatedDefaults deletes system-seeded badges whose defaults have
// been retired. Currently targets "night_owl" (the late-night badge). It only
// removes badges that still carry IsSystem=true — if an admin hand-edited a
// default into a custom badge they want to keep, the IsSystem flag would have
// been cleared by that edit and the badge is left alone. Safe to run on every
// boot.
func (s *badgeService) RemoveDeprecatedDefaults() error {
	deprecated := []string{"night_owl"}
	for _, code := range deprecated {
		b, err := s.badgeRepo.FindByCode(code)
		if err != nil || b == nil {
			continue
		}
		// Only delete if it's still a pristine system default; never clobber a
		// badge the admin has taken ownership of (IsSystem=false).
		if b.IsSystem {
			if err := s.badgeRepo.Delete(b.ID); err != nil {
				log.Printf("Failed to remove deprecated default badge %s: %v", code, err)
			} else {
				log.Printf("Removed deprecated default badge %s", code)
			}
		}
	}
	return nil
}
