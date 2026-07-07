package service

import (
	"errors"
	"log"
	"strings"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"

	"gorm.io/gorm"
)

// TagService handles Tag CRUD and default seeding.
type TagService interface {
	List() ([]model.Tag, error)
	FindByID(id uint) (*model.Tag, error)
	Create(key, label, color string, sortOrder int) (*model.Tag, error)
	Update(t *model.Tag) error
	Delete(id uint) error
	SeedDefaultTags() error
}

type tagService struct {
	repo repository.TagRepository
}

// NewTagService creates an instance of TagService.
func NewTagService(repo repository.TagRepository) TagService {
	return &tagService{repo: repo}
}

func (s *tagService) List() ([]model.Tag, error) {
	return s.repo.List()
}

func (s *tagService) FindByID(id uint) (*model.Tag, error) {
	return s.repo.FindByID(id)
}

func (s *tagService) Create(key, label, color string, sortOrder int) (*model.Tag, error) {
	key = strings.TrimSpace(strings.ToLower(key))
	if key == "" {
		return nil, errors.New("tag key is required")
	}
	if label == "" {
		return nil, errors.New("tag label is required")
	}
	tag := &model.Tag{
		Key:       key,
		Label:     label,
		Color:     color,
		SortOrder: sortOrder,
	}
	if err := s.repo.Create(tag); err != nil {
		return nil, err
	}
	return tag, nil
}

func (s *tagService) Update(tag *model.Tag) error {
	tag.Key = strings.TrimSpace(strings.ToLower(tag.Key))
	if tag.Key == "" {
		return errors.New("tag key is required")
	}
	// Course stores tag IDs, not keys/labels, so renaming the label or key
	// requires no cascade — courses automatically reflect the new label via
	// the relation when reloaded.
	return s.repo.Update(tag)
}

func (s *tagService) Delete(id uint) error {
	return s.repo.Delete(id)
}

// SeedDefaultTags populates a starter tag set on first run. Idempotent: skips
// when any tag already exists. Keys are stable identifiers; labels mirror the
// tags the Flutter client used to hardcode so existing UX stays familiar.
func (s *tagService) SeedDefaultTags() error {
	list, err := s.repo.List()
	if err != nil {
		return err
	}
	if len(list) > 0 {
		return nil
	}

	defaults := []model.Tag{
		{Key: "required", Label: "必修", Color: "#ef4444", SortOrder: 1},
		{Key: "thinking", Label: "思维", Color: "#f59e0b", SortOrder: 2},
		{Key: "extension", Label: "拓展", Color: "#8b5cf6", SortOrder: 3},
		{Key: "explore", Label: "探索", Color: "#06b6d4", SortOrder: 4},
		{Key: "extracurricular", Label: "课外", Color: "#10b981", SortOrder: 5},
		{Key: "logic", Label: "逻辑", Color: "#ec4899", SortOrder: 6},
		{Key: "horizon", Label: "视野", Color: "#3b82f6", SortOrder: 7},
	}

	for i := range defaults {
		if err := s.repo.Create(&defaults[i]); err != nil {
			// uniqueIndex collision → already seeded concurrently; skip.
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				continue
			}
			log.Printf("Failed to seed tag %s: %v", defaults[i].Key, err)
		}
	}
	return nil
}
