package service

import (
	"errors"
	"log"
	"strings"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"

	"gorm.io/gorm"
)

// SubjectService handles Subject CRUD, key-rename cascade, and default seeding.
type SubjectService interface {
	List() ([]model.Subject, error)
	FindByID(id uint) (*model.Subject, error)
	Create(key, label, emoji, color string, sortOrder int) (*model.Subject, error)
	// Update applies the (already-loaded) subject's new fields. If the Key
	// changed, badges.rule_target is cascaded in the same transaction.
	Update(s *model.Subject, oldKey string) error
	Delete(id uint) error
	SeedDefaultSubjects() error
}

type subjectService struct {
	db           *gorm.DB
	repo         repository.SubjectRepository
	badgeRepo    repository.BadgeRepository
	badgeService BadgeService // auto-generate/clean up subject_count badges
}

// NewSubjectService creates an instance of SubjectService. badgeService is
// needed to auto-generate a multi-tier subject badge when a subject is created
// and clean it up on delete; badgeRepo cascades rule_target on rename.
func NewSubjectService(db *gorm.DB, repo repository.SubjectRepository, br repository.BadgeRepository, bs BadgeService) SubjectService {
	return &subjectService{db: db, repo: repo, badgeRepo: br, badgeService: bs}
}

func (s *subjectService) List() ([]model.Subject, error) {
	return s.repo.List()
}

func (s *subjectService) FindByID(id uint) (*model.Subject, error) {
	return s.repo.FindByID(id)
}

func (s *subjectService) Create(key, label, emoji, color string, sortOrder int) (*model.Subject, error) {
	key = strings.TrimSpace(strings.ToLower(key))
	if key == "" {
		return nil, errors.New("subject key is required")
	}
	if label == "" {
		return nil, errors.New("subject label is required")
	}
	subj := &model.Subject{
		Key:       key,
		Label:     label,
		Emoji:     emoji,
		Color:     color,
		SortOrder: sortOrder,
	}
	if err := s.repo.Create(subj); err != nil {
		return nil, err
	}
	// Auto-generate the subject's multi-tier subject_count badge.
	if s.badgeService != nil {
		if err := s.badgeService.SeedSubjectBadge(subj.ID, subj.Key, subj.Label); err != nil {
			log.Printf("Warning: failed to auto-generate badge for subject %s: %v", subj.Key, err)
		}
	}
	return subj, nil
}

// Update saves the subject. If its Key differs from oldKey, every badge rule
// that targeted the old key is rewritten so subject_count rules keep matching,
// and the auto-generated subject badge's code + title are updated to match.
// All writes run in one transaction so a failure partway through can't leave
// the subject renamed but its badge cascade undone (or vice versa).
func (s *subjectService) Update(subj *model.Subject, oldKey string) error {
	newKey := strings.TrimSpace(strings.ToLower(subj.Key))
	if newKey == "" {
		return errors.New("subject key is required")
	}
	subj.Key = newKey

	return s.db.Transaction(func(tx *gorm.DB) error {
		// Save the subject itself first.
		if err := tx.Save(subj).Error; err != nil {
			return err
		}
		if oldKey == "" || newKey == oldKey {
			// Key unchanged — just refresh the auto badge's title/desc if any.
			if subj.Label != "" {
				var b model.Badge
				if err := tx.Where("code = ?", SubjectBadgeCode(newKey)).First(&b).Error; err == nil {
					b.Title = subj.Label + "达人"
					b.Description = "完成的 " + subj.Label + " 视频课时数"
					return tx.Save(&b).Error
				}
			}
			return nil
		}
		// Key changed: cascade rule_target on ALL badges referencing oldKey.
		if err := tx.Model(&model.Badge{}).
			Where("rule_target = ?", oldKey).
			Update("rule_target", newKey).Error; err != nil {
			return err
		}
		// Re-key the auto-generated subject badge (code + title) to the new key.
		var oldBadge model.Badge
		if err := tx.Where("code = ?", SubjectBadgeCode(oldKey)).First(&oldBadge).Error; err == nil {
			oldBadge.Code = SubjectBadgeCode(newKey)
			oldBadge.Title = subj.Label + "达人"
			oldBadge.Description = "完成的 " + subj.Label + " 视频课时数"
			if err := tx.Save(&oldBadge).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Delete refuses system-seeded subjects first (IsSystem check), then refuses
// when HAND-AUTHORED badge rules still reference the subject's key
// (ErrSubjectHasBadges — rule_target has no FK, so this service-level count is
// the only guard against orphaning those rules). It then falls through to the
// DB-level FK RESTRICT guard (ErrSubjectInUse) when a course still references
// the subject. On success it also removes the subject's auto-generated badge
// (and its user_badges). The auto-badge does NOT trigger ErrSubjectHasBadges
// (it's excluded by code from the rule_target count, and cleaned up here).
func (s *subjectService) Delete(id uint) error {
	subj, err := s.repo.FindByID(id)
	if err != nil {
		return err
	}
	if subj == nil {
		return nil
	}
	if subj.IsSystem {
		return ErrSystemProtected
	}
	// Refuse if any HAND-AUTHORED badge still targets this subject's key.
	// Pass the auto-badge's code as the exclude so the subject's own generated
	// badge (which we delete below) doesn't trip the guard.
	handAuthored, err := s.badgeRepo.CountByRuleTargetExcludingCode(subj.Key, SubjectBadgeCode(subj.Key))
	if err != nil {
		return err
	}
	if handAuthored > 0 {
		return ErrSubjectHasBadges
	}
	err = s.repo.Delete(id)
	if err != nil {
		// SQLite emits "FOREIGN KEY constraint failed"; other drivers phrase it
		// differently, so we match loosely on "foreign key" / "constraint".
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "foreign key") || strings.Contains(msg, "constraint") {
			return ErrSubjectInUse
		}
		return err
	}
	// Clean up the auto-generated subject badge (best-effort).
	if delErr := s.badgeRepo.DeleteByCode(SubjectBadgeCode(subj.Key)); delErr != nil {
		log.Printf("Warning: failed to remove subject badge for %s: %v", subj.Key, delErr)
	}
	return nil
}

// SeedDefaultSubjects populates the canonical subject set on first run and
// incrementally backfills any defaults added later (e.g. the junior-high
// subjects added after launch). Keys MUST stay aligned with badge_service.go's
// subject_count default RuleTarget values ("math", "english") or those badge
// rules silently stop matching. All seeded rows are marked IsSystem so they
// can be renamed/recolored but never deleted.
//
// Idempotent + incremental: each default is inserted by key; a key that
// already exists (unique index collision) is skipped. So an existing install
// picks up newly-added defaults on the next boot without re-seeding the ones
// it already has, and a fresh install gets the full set.
func (s *subjectService) SeedDefaultSubjects() error {
	defaults := []model.Subject{
		{Key: "chinese", Label: "语文", Emoji: "📚", Color: "#60a5fa", SortOrder: 1, IsSystem: true},
		{Key: "math", Label: "数学", Emoji: "📐", Color: "#f59e0b", SortOrder: 2, IsSystem: true},
		{Key: "english", Label: "英语", Emoji: "🔠", Color: "#34d399", SortOrder: 3, IsSystem: true},
		{Key: "physics", Label: "物理/科学", Emoji: "🧪", Color: "#a78bfa", SortOrder: 4, IsSystem: true},
		// 初中分科（对齐全学段学校课程）
		{Key: "chemistry", Label: "化学", Emoji: "⚗️", Color: "#22d3ee", SortOrder: 5, IsSystem: true},
		{Key: "biology", Label: "生物", Emoji: "🌱", Color: "#84cc16", SortOrder: 6, IsSystem: true},
		{Key: "history", Label: "历史", Emoji: "📜", Color: "#d97706", SortOrder: 7, IsSystem: true},
		{Key: "geography", Label: "地理", Emoji: "🗺️", Color: "#0ea5e9", SortOrder: 8, IsSystem: true},
		{Key: "politics", Label: "道德与法治", Emoji: "⚖️", Color: "#ef4444", SortOrder: 9, IsSystem: true},
		{Key: "extra", Label: "课外百科", Emoji: "🌎", Color: "#f43f5e", SortOrder: 10, IsSystem: true},
		// Entertainment: the implicit subject for fun videos (no learning stats,
		// no badge). Entertainment courses point SubjectID here to satisfy the
		// NOT NULL constraint. SortOrder 99 keeps it at the end of any list.
		{Key: "entertainment", Label: "娱乐", Emoji: "🎬", Color: "#8b5cf6", SortOrder: 99, IsSystem: true},
	}

	for i := range defaults {
		created := true
		if err := s.repo.Create(&defaults[i]); err != nil {
			// uniqueIndex collision → already present (old install); skip but
			// still make sure its badge exists below.
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				created = false
			} else {
				log.Printf("Failed to seed subject %s: %v", defaults[i].Key, err)
				continue
			}
		}
		// Ensure each default subject has its auto-generated badge (idempotent),
		// EXCEPT entertainment (fun videos carry no badge). Run for both
		// newly-created and already-present subjects so an old install converges.
		if defaults[i].Key != "entertainment" && s.badgeService != nil {
			if err := s.badgeService.SeedSubjectBadge(defaults[i].ID, defaults[i].Key, defaults[i].Label); err != nil {
				log.Printf("Warning: failed to seed badge for subject %s: %v", defaults[i].Key, err)
			}
		}
		_ = created
	}
	return nil
}
