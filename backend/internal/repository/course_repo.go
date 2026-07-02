package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// CourseRepository handles SQL operations for Courses entity.
type CourseRepository interface {
	List(grade, subject string, allowedIDs []uint) ([]model.Course, error)
	FindByID(id uint) (*model.Course, error)
	Create(course *model.Course) error
	Update(course *model.Course) error
	Delete(id uint) error
}

type courseRepo struct {
	db *gorm.DB
}

// NewCourseRepository creates an instance of CourseRepository.
func NewCourseRepository(db *gorm.DB) CourseRepository {
	return &courseRepo{db: db}
}

func (r *courseRepo) List(grade, subject string, allowedIDs []uint) ([]model.Course, error) {
	var courses []model.Course
	query := r.db.Model(&model.Course{})

	// Filter by user course access permissions
	if allowedIDs != nil {
		if len(allowedIDs) == 0 {
			return []model.Course{}, nil
		}
		query = query.Where("id IN ?", allowedIDs)
	}

	// Double-dimension filters
	if grade != "" {
		query = query.Where("grade LIKE ? OR grade = 'universal' OR grade = 'all'", "%"+grade+"%")
	}
	if subject != "" {
		query = query.Where("subject = ?", subject)
	}

	err := query.Find(&courses).Error
	return courses, err
}

func (r *courseRepo) FindByID(id uint) (*model.Course, error) {
	var course model.Course
	if err := r.db.First(&course, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &course, nil
}

func (r *courseRepo) Create(course *model.Course) error {
	return r.db.Create(course).Error
}

func (r *courseRepo) Update(course *model.Course) error {
	return r.db.Save(course).Error
}

func (r *courseRepo) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Clean up related episodes and assets
		var episodes []model.Episode
		tx.Where("course_id = ?", id).Find(&episodes)
		for _, ep := range episodes {
			tx.Delete(&model.Subtitle{}, "episode_id = ?", ep.ID)
			tx.Delete(&model.AILessonContent{}, "episode_id = ?", ep.ID)
			tx.Delete(&model.UserProgress{}, "episode_id = ?", ep.ID)
		}
		tx.Delete(&model.Episode{}, "course_id = ?", id)
		tx.Delete(&model.UserCourseAccess{}, "course_id = ?", id)
		return tx.Delete(&model.Course{}, id).Error
	})
}
