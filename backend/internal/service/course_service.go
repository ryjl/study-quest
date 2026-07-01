package service

import (
	"errors"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// CourseService handles Course business operations.
type CourseService interface {
	GetCourses(userID uint, userRole string, grade, subject string) ([]model.Course, error)
	GetCourseByID(id uint) (*model.Course, error)
	CreateCourse(title, grade, subject, coverURL string) (*model.Course, error)
	UpdateCourse(id uint, title, grade, subject, coverURL string) (*model.Course, error)
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

func (s *courseService) GetCourses(userID uint, userRole string, grade, subject string) ([]model.Course, error) {
	// Admin or Parent can view all courses
	if userRole == "admin" || userRole == "parent" {
		return s.courseRepo.List(grade, subject, nil)
	}

	// Students/Teens are restricted to granted courses only
	allowedIDs, err := s.userRepo.GetAccessList(userID)
	if err != nil {
		return nil, err
	}

	return s.courseRepo.List(grade, subject, allowedIDs)
}

func (s *courseService) GetCourseByID(id uint) (*model.Course, error) {
	return s.courseRepo.FindByID(id)
}

func (s *courseService) CreateCourse(title, grade, subject, coverURL string) (*model.Course, error) {
	g := model.Grade(grade)
	if !g.Valid() {
		return nil, errors.New("invalid course grade value: " + grade)
	}

	c := &model.Course{
		Title:    title,
		Grade:    g,
		Subject:  subject,
		CoverURL: coverURL,
	}
	if err := s.courseRepo.Create(c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *courseService) UpdateCourse(id uint, title, grade, subject, coverURL string) (*model.Course, error) {
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

	c.Title = title
	c.Grade = g
	c.Subject = subject
	c.CoverURL = coverURL

	if err := s.courseRepo.Update(c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *courseService) DeleteCourse(id uint) error {
	return s.courseRepo.Delete(id)
}
