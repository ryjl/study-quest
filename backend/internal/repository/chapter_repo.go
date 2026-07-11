package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// ChapterRepository handles database operations for the Chapter model.
type ChapterRepository interface {
	// WithTx returns a copy bound to an in-progress transaction.
	WithTx(tx *gorm.DB) ChapterRepository
	ListByCourse(courseID uint) ([]model.Chapter, error)
	FindByID(id uint) (*model.Chapter, error)
	Create(chapter *model.Chapter) error
	Update(chapter *model.Chapter) error
	Delete(id uint) error
	CountByCourse(courseID uint) (int64, error)
}

type chapterRepo struct {
	db *gorm.DB
}

// NewChapterRepository creates a new ChapterRepository.
func NewChapterRepository(db *gorm.DB) ChapterRepository {
	return &chapterRepo{db: db}
}

func (r *chapterRepo) WithTx(tx *gorm.DB) ChapterRepository {
	return &chapterRepo{db: tx}
}

func (r *chapterRepo) ListByCourse(courseID uint) ([]model.Chapter, error) {
	var chapters []model.Chapter
	err := r.db.Where("course_id = ?", courseID).Order("sort_order asc, id asc").Find(&chapters).Error
	return chapters, err
}

func (r *chapterRepo) FindByID(id uint) (*model.Chapter, error) {
	var ch model.Chapter
	if err := r.db.First(&ch, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ch, nil
}

func (r *chapterRepo) Create(chapter *model.Chapter) error {
	return r.db.Create(chapter).Error
}

func (r *chapterRepo) Update(chapter *model.Chapter) error {
	return r.db.Save(chapter).Error
}

func (r *chapterRepo) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Dissociate episodes from this chapter (set chapter_id to 0/default)
		if err := tx.Model(&model.Episode{}).Where("chapter_id = ?", id).Update("chapter_id", 0).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Chapter{}, id).Error
	})
}

// CountByCourse returns the number of chapters in a course — used for the
// chapter_count stat shown on each course card.
func (r *chapterRepo) CountByCourse(courseID uint) (int64, error) {
	var count int64
	err := r.db.Model(&model.Chapter{}).Where("course_id = ?", courseID).Count(&count).Error
	return count, err
}
