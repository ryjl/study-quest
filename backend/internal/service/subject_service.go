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
	repo      repository.SubjectRepository
	badgeRepo repository.BadgeRepository
}

// NewSubjectService creates an instance of SubjectService. badgeRepo is needed
// to cascade rule_target rewrites when a subject's Key is renamed.
func NewSubjectService(repo repository.SubjectRepository, br repository.BadgeRepository) SubjectService {
	return &subjectService{repo: repo, badgeRepo: br}
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
	return subj, nil
}

// Update saves the subject. If its Key differs from oldKey, every badge rule
// that targeted the old key is rewritten so subject_count rules keep matching.
func (s *subjectService) Update(subj *model.Subject, oldKey string) error {
	newKey := strings.TrimSpace(strings.ToLower(subj.Key))
	if newKey == "" {
		return errors.New("subject key is required")
	}
	subj.Key = newKey

	if oldKey != "" && newKey != oldKey {
		// Rename cascade: badges.store rule_target as a plain string key.
		if err := s.repo.UpdateBadgesRuleTarget(oldKey, newKey); err != nil {
			return err
		}
	}
	return s.repo.Update(subj)
}

// Delete refuses system-seeded subjects first (IsSystem check), then falls
// through to the DB-level FK RESTRICT guard (ErrSubjectInUse) when a course
// still references the subject.
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
	err = s.repo.Delete(id)
	if err != nil {
		// SQLite emits "FOREIGN KEY constraint failed"; other drivers phrase it
		// differently, so we match loosely on "foreign key" / "constraint".
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "foreign key") || strings.Contains(msg, "constraint") {
			return ErrSubjectInUse
		}
	}
	return err
}

// SeedDefaultSubjects populates the canonical subject set on first run.
// Idempotent: skips when any subject already exists. Keys MUST stay aligned
// with badge_service.go's subject_count default RuleTarget values ("math",
// "english") or those badge rules silently stop matching. All seeded rows are
// marked IsSystem so they can be renamed/recolored but never deleted.
func (s *subjectService) SeedDefaultSubjects() error {
	list, err := s.repo.List()
	if err != nil {
		return err
	}
	if len(list) > 0 {
		return nil
	}

	defaults := []model.Subject{
		{Key: "chinese", Label: "语文", Emoji: "📚", Color: "#60a5fa", SortOrder: 1, IsSystem: true},
		{Key: "math", Label: "数学", Emoji: "📐", Color: "#f59e0b", SortOrder: 2, IsSystem: true},
		{Key: "english", Label: "英语", Emoji: "🔠", Color: "#34d399", SortOrder: 3, IsSystem: true},
		{Key: "physics", Label: "物理/科学", Emoji: "🧪", Color: "#a78bfa", SortOrder: 4, IsSystem: true},
		{Key: "extra", Label: "课外百科", Emoji: "🌎", Color: "#f43f5e", SortOrder: 5, IsSystem: true},
	}

	for i := range defaults {
		if err := s.repo.Create(&defaults[i]); err != nil {
			// uniqueIndex collision → already seeded concurrently; skip.
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				continue
			}
			log.Printf("Failed to seed subject %s: %v", defaults[i].Key, err)
		}
	}
	return nil
}
