package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// SubjectRepository handles SQL operations for the Subject entity.
// Mirrors the BadgeRepository pattern. FK RESTRICT on courses.subject_id
// makes Delete fail at the DB level when a course still references the subject,
// so no application-level pre-check is needed.
type SubjectRepository interface {
	List() ([]model.Subject, error)               // ORDER BY sort_order, id
	FindByKey(key string) (*model.Subject, error) // nil, nil if absent
	FindByID(id uint) (*model.Subject, error)     // nil, nil if absent
	Create(s *model.Subject) error
	Update(s *model.Subject) error                // db.Save
	Delete(id uint) error                         // raw delete; DB FK RESTRICT guards it
	// UpdateBadgesRuleTarget rewrites badge.rule_target from oldKey to newKey.
	// Used when a subject's Key is renamed (badges store the key as a string,
	// not a FK, so they need an application-level cascade).
	UpdateBadgesRuleTarget(oldKey, newKey string) error
}

type subjectRepo struct {
	db *gorm.DB
}

// NewSubjectRepository creates an instance of SubjectRepository.
func NewSubjectRepository(db *gorm.DB) SubjectRepository {
	return &subjectRepo{db: db}
}

func (r *subjectRepo) List() ([]model.Subject, error) {
	var subjects []model.Subject
	err := r.db.Order("sort_order ASC, id ASC").Find(&subjects).Error
	return subjects, err
}

func (r *subjectRepo) FindByKey(key string) (*model.Subject, error) {
	var subject model.Subject
	if err := r.db.Where("key = ?", key).First(&subject).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &subject, nil
}

func (r *subjectRepo) FindByID(id uint) (*model.Subject, error) {
	var subject model.Subject
	if err := r.db.First(&subject, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &subject, nil
}

func (r *subjectRepo) Create(s *model.Subject) error {
	return r.db.Create(s).Error
}

func (r *subjectRepo) Update(s *model.Subject) error {
	return r.db.Save(s).Error
}

func (r *subjectRepo) Delete(id uint) error {
	return r.db.Delete(&model.Subject{}, id).Error
}

func (r *subjectRepo) UpdateBadgesRuleTarget(oldKey, newKey string) error {
	return r.db.Model(&model.Badge{}).
		Where("rule_target = ?", oldKey).
		Update("rule_target", newKey).Error
}
