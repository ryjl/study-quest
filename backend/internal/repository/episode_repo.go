package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// EpisodeRepository implements core GORM functions and double-protection search.
type EpisodeRepository interface {
	ListByCourse(courseID uint) ([]model.Episode, error)
	FindByID(id uint) (*model.Episode, error)
	FindByHash(hash string) (*model.Episode, error)
	FindByPathAndSize(path string, size int64) (*model.Episode, error)
	Create(episode *model.Episode) error
	Update(episode *model.Episode) error
	Delete(id uint) error

	// Subtitle operations
	GetSubtitle(episodeID uint) (*model.Subtitle, error)
	SaveSubtitle(subtitle *model.Subtitle) error

	// AI Lesson Content operations
	GetAIContent(episodeID uint) (*model.AILessonContent, error)
	SaveAIContent(content *model.AILessonContent) error
}

type episodeRepo struct {
	db *gorm.DB
}

// NewEpisodeRepository creates an instance of EpisodeRepository.
func NewEpisodeRepository(db *gorm.DB) EpisodeRepository {
	return &episodeRepo{db: db}
}

func (r *episodeRepo) ListByCourse(courseID uint) ([]model.Episode, error) {
	var episodes []model.Episode
	// Order by sort_order ascending
	err := r.db.Where("course_id = ?", courseID).Order("sort_order asc").Find(&episodes).Error
	return episodes, err
}

func (r *episodeRepo) FindByID(id uint) (*model.Episode, error) {
	var ep model.Episode
	if err := r.db.First(&ep, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ep, nil
}

func (r *episodeRepo) FindByHash(hash string) (*model.Episode, error) {
	var ep model.Episode
	if err := r.db.Where("file_hash = ?", hash).First(&ep).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ep, nil
}

func (r *episodeRepo) FindByPathAndSize(path string, size int64) (*model.Episode, error) {
	var ep model.Episode
	if err := r.db.Where("video_relative_path = ? AND file_size = ?", path, size).First(&ep).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ep, nil
}

func (r *episodeRepo) Create(episode *model.Episode) error {
	return r.db.Create(episode).Error
}

func (r *episodeRepo) Update(episode *model.Episode) error {
	return r.db.Save(episode).Error
}

func (r *episodeRepo) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		tx.Delete(&model.Subtitle{}, "episode_id = ?", id)
		tx.Delete(&model.AILessonContent{}, "episode_id = ?", id)
		tx.Delete(&model.UserProgress{}, "episode_id = ?", id)
		return tx.Delete(&model.Episode{}, id).Error
	})
}

func (r *episodeRepo) GetSubtitle(episodeID uint) (*model.Subtitle, error) {
	var sub model.Subtitle
	if err := r.db.First(&sub, "episode_id = ?", episodeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

func (r *episodeRepo) SaveSubtitle(subtitle *model.Subtitle) error {
	var sub model.Subtitle
	err := r.db.First(&sub, "episode_id = ?", subtitle.EpisodeID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(subtitle).Error
		}
		return err
	}
	sub.SrtContent = subtitle.SrtContent
	return r.db.Save(&sub).Error
}

func (r *episodeRepo) GetAIContent(episodeID uint) (*model.AILessonContent, error) {
	var ai model.AILessonContent
	if err := r.db.First(&ai, "episode_id = ?", episodeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ai, nil
}

func (r *episodeRepo) SaveAIContent(content *model.AILessonContent) error {
	var ai model.AILessonContent
	err := r.db.First(&ai, "episode_id = ?", content.EpisodeID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(content).Error
		}
		return err
	}
	ai.PreAdventureJSON = content.PreAdventureJSON
	ai.PostReviewJSON = content.PostReviewJSON
	return r.db.Save(&ai).Error
}
