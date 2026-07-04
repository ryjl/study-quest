package service

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// ChapterService manages business operations for Chapters.
type ChapterService interface {
	GetChaptersByCourse(courseID uint) ([]model.Chapter, error)
	GetChapterByID(id uint) (*model.Chapter, error)
	CreateChapter(courseID uint, title, description, coverURL, attachmentJSON string, sortOrder int) (*model.Chapter, error)
	UpdateChapter(id uint, title, description, coverURL, attachmentJSON string, sortOrder int) (*model.Chapter, error)
	DeleteChapter(id uint) error
}

type chapterService struct {
	chapterRepo repository.ChapterRepository
}

// NewChapterService creates a new ChapterService instance.
func NewChapterService(cr repository.ChapterRepository) ChapterService {
	return &chapterService{chapterRepo: cr}
}

func (s *chapterService) GetChaptersByCourse(courseID uint) ([]model.Chapter, error) {
	return s.chapterRepo.ListByCourse(courseID)
}

func (s *chapterService) GetChapterByID(id uint) (*model.Chapter, error) {
	return s.chapterRepo.FindByID(id)
}

func (s *chapterService) CreateChapter(courseID uint, title, description, coverURL, attachmentJSON string, sortOrder int) (*model.Chapter, error) {
	if attachmentJSON == "" {
		attachmentJSON = "[]"
	}
	ch := &model.Chapter{
		CourseID:       courseID,
		Title:          title,
		Description:    description,
		CoverURL:       coverURL,
		AttachmentJSON: attachmentJSON,
		SortOrder:      sortOrder,
	}
	if err := s.chapterRepo.Create(ch); err != nil {
		return nil, err
	}
	return ch, nil
}

func (s *chapterService) UpdateChapter(id uint, title, description, coverURL, attachmentJSON string, sortOrder int) (*model.Chapter, error) {
	ch, err := s.chapterRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, nil
	}

	if attachmentJSON == "" {
		attachmentJSON = "[]"
	}

	ch.Title = title
	ch.Description = description
	ch.CoverURL = coverURL
	ch.AttachmentJSON = attachmentJSON
	ch.SortOrder = sortOrder

	if err := s.chapterRepo.Update(ch); err != nil {
		return nil, err
	}
	return ch, nil
}

func (s *chapterService) DeleteChapter(id uint) error {
	return s.chapterRepo.Delete(id)
}
