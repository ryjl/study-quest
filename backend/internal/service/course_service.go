package service

import (
	"errors"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// CourseService handles Course business operations.
type CourseService interface {
	GetCourses(userID uint, userRole string, grade string, subjectID uint) ([]model.Course, error)
	GetCourseByID(id uint) (*model.Course, error)
	CreateCourse(title, grade string, subjectID uint, coverURL string, tagIDs []uint, attachmentJSON string) (*model.Course, error)
	UpdateCourse(id uint, title, grade string, subjectID uint, coverURL string, tagIDs []uint, attachmentJSON string) (*model.Course, error)
	DeleteCourse(id uint) error
}

type courseService struct {
	courseRepo repository.CourseRepository
	userRepo   repository.UserRepository
}

// NewCourseService creates an instance of CourseService.
func NewCourseService(cr repository.CourseRepository, ur repository.UserRepository) CourseService {
	return &courseService{
		courseRepo: cr,
		userRepo:   ur,
	}
}

func (s *courseService) GetCourses(userID uint, userRole string, grade string, subjectID uint) ([]model.Course, error) {
	// Admin or Parent can view all courses
	if userRole == "admin" || userRole == "parent" {
		return s.courseRepo.List(grade, subjectID, nil)
	}

	// Students/Teens are restricted to granted courses only
	allowedIDs, err := s.userRepo.GetAccessList(userID)
	if err != nil {
		return nil, err
	}

	return s.courseRepo.List(grade, subjectID, allowedIDs)
}

func (s *courseService) GetCourseByID(id uint) (*model.Course, error) {
	return s.courseRepo.FindByID(id)
}

func (s *courseService) CreateCourse(title, grade string, subjectID uint, coverURL string, tagIDs []uint, attachmentJSON string) (*model.Course, error) {
	g := model.Grade(grade)
	if !g.Valid() {
		return nil, errors.New("invalid course grade value: " + grade)
	}

	if attachmentJSON == "" {
		attachmentJSON = "[]"
	}

	c := &model.Course{
		Title:          title,
		Grade:          g,
		SubjectID:      subjectID,
		CoverURL:       coverURL,
		AttachmentJSON: attachmentJSON,
	}
	if err := s.courseRepo.Create(c); err != nil {
		return nil, err
	}
	// Sync the many2many tag association after the course row exists.
	if len(tagIDs) > 0 {
		if err := s.courseRepo.SetTags(c.ID, tagIDs); err != nil {
			return nil, err
		}
	}
	// Reload so the returned object carries Tags for DTO projection.
	reloaded, err := s.courseRepo.FindByID(c.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return c, nil
}

func (s *courseService) UpdateCourse(id uint, title, grade string, subjectID uint, coverURL string, tagIDs []uint, attachmentJSON string) (*model.Course, error) {
	g := model.Grade(grade)
	if !g.Valid() {
		return nil, errors.New("invalid course grade value: " + grade)
	}

	c, err := s.courseRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}

	if attachmentJSON == "" {
		attachmentJSON = "[]"
	}

	c.Title = title
	c.Grade = g
	c.SubjectID = subjectID
	c.CoverURL = coverURL
	c.AttachmentJSON = attachmentJSON

	if err := s.courseRepo.Update(c); err != nil {
		return nil, err
	}
	// Replace the tag set (handles add/remove/clear atomically).
	if err := s.courseRepo.SetTags(c.ID, tagIDs); err != nil {
		return nil, err
	}
	// Reload to reflect the new Tags association in the returned object.
	reloaded, err := s.courseRepo.FindByID(c.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return c, nil
}

func (s *courseService) DeleteCourse(id uint) error {
	return s.courseRepo.Delete(id)
}
