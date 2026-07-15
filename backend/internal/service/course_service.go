package service

import (
	"errors"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// CourseService handles Course business operations.
type CourseService interface {
	GetCourses(userID uint, userRole string, grade string, subjectID uint, contentType model.ContentType) ([]model.Course, error)
	GetCourseByID(id uint) (*model.Course, error)
	CreateCourse(title string, grades []model.Grade, subjectID uint, contentType model.ContentType, coverURL string, tagIDs []uint, attachmentJSON string, aiHint string) (*model.Course, error)
	UpdateCourse(id uint, title string, grades []model.Grade, subjectID uint, contentType model.ContentType, coverURL string, tagIDs []uint, attachmentJSON string, aiHint string) (*model.Course, error)
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

func (s *courseService) GetCourses(userID uint, userRole string, grade string, subjectID uint, contentType model.ContentType) ([]model.Course, error) {
	// Admin or Parent can view all courses
	if userRole == model.RoleAdmin || userRole == model.RoleParent {
		return s.courseRepo.List(grade, subjectID, contentType, nil)
	}

	// Students/Teens are restricted to granted courses only
	allowedIDs, err := s.userRepo.GetAccessList(userID)
	if err != nil {
		return nil, err
	}

	return s.courseRepo.List(grade, subjectID, contentType, allowedIDs)
}

func (s *courseService) GetCourseByID(id uint) (*model.Course, error) {
	return s.courseRepo.FindByID(id)
}

func (s *courseService) CreateCourse(title string, grades []model.Grade, subjectID uint, contentType model.ContentType, coverURL string, tagIDs []uint, attachmentJSON string, aiHint string) (*model.Course, error) {
	for _, g := range grades {
		if !g.Valid() {
			return nil, errors.New("invalid course grade value: " + string(g))
		}
	}
	if !contentType.Valid() {
		contentType = model.ContentLearning
	}

	if attachmentJSON == "" {
		attachmentJSON = "[]"
	}

	c := &model.Course{
		Title:          title,
		SubjectID:      subjectID,
		ContentType:    contentType,
		CoverURL:       coverURL,
		AttachmentJSON: attachmentJSON,
		AIHint:         aiHint,
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
	// Sync the grade set (course_grades join table).
	if err := s.courseRepo.SetGrades(c.ID, grades); err != nil {
		return nil, err
	}
	// Reload so the returned object carries Tags + Grades for DTO projection.
	reloaded, err := s.courseRepo.FindByID(c.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return c, nil
}

func (s *courseService) UpdateCourse(id uint, title string, grades []model.Grade, subjectID uint, contentType model.ContentType, coverURL string, tagIDs []uint, attachmentJSON string, aiHint string) (*model.Course, error) {
	for _, g := range grades {
		if !g.Valid() {
			return nil, errors.New("invalid course grade value: " + string(g))
		}
	}
	if !contentType.Valid() {
		contentType = model.ContentLearning
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
	c.SubjectID = subjectID
	c.ContentType = contentType
	c.CoverURL = coverURL
	c.AttachmentJSON = attachmentJSON
	c.AIHint = aiHint

	if err := s.courseRepo.Update(c); err != nil {
		return nil, err
	}
	// Replace the tag set (handles add/remove/clear atomically).
	if err := s.courseRepo.SetTags(c.ID, tagIDs); err != nil {
		return nil, err
	}
	// Replace the grade set.
	if err := s.courseRepo.SetGrades(c.ID, grades); err != nil {
		return nil, err
	}
	// Reload to reflect the new Tags + Grades associations in the returned object.
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
