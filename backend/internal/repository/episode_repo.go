package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// EpisodeRepository implements core GORM functions and double-protection search.
type EpisodeRepository interface {
	ListByCourse(courseID uint) ([]model.Episode, error)
	ListByNullDuration() ([]model.Episode, error)
	FindByID(id uint) (*model.Episode, error)
	FindByHash(hash string) (*model.Episode, error)
	FindByPathAndSize(path string, size int64) (*model.Episode, error)
	Create(episode *model.Episode) error
	Update(episode *model.Episode) error
	Delete(id uint) error
	FindByCriteria(basename string, size *int64, pathHint string) ([]model.Episode, error)

	// Subtitle operations
	GetSubtitle(episodeID uint) (*model.Subtitle, error)
	ListSubtitles(episodeID uint) ([]model.Subtitle, error)
	GetSubtitleByID(id uint) (*model.Subtitle, error)
	SaveSubtitle(subtitle *model.Subtitle) error
	DeleteSubtitle(id uint) error

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

// ListByNullDuration returns every episode whose duration_seconds is NULL —
// i.e. ones that still need an ffprobe backfill. Used by the admin
// "scan missing durations" action.
func (r *episodeRepo) ListByNullDuration() ([]model.Episode, error) {
	var episodes []model.Episode
	err := r.db.Where("duration_seconds IS NULL").Order("id asc").Find(&episodes).Error
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
	if err := r.db.Where("episode_id = ?", episodeID).Order("id asc").First(&sub).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

func (r *episodeRepo) ListSubtitles(episodeID uint) ([]model.Subtitle, error) {
	var subs []model.Subtitle
	err := r.db.Where("episode_id = ?", episodeID).Order("id asc").Find(&subs).Error
	return subs, err
}

func (r *episodeRepo) GetSubtitleByID(id uint) (*model.Subtitle, error) {
	var sub model.Subtitle
	if err := r.db.First(&sub, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

func (r *episodeRepo) SaveSubtitle(subtitle *model.Subtitle) error {
	var sub model.Subtitle
	err := r.db.Where("episode_id = ? AND language = ?", subtitle.EpisodeID, subtitle.Language).First(&sub).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(subtitle).Error
		}
		return err
	}
	sub.SrtContent = subtitle.SrtContent
	sub.Label = subtitle.Label
	return r.db.Save(&sub).Error
}

func (r *episodeRepo) DeleteSubtitle(id uint) error {
	return r.db.Delete(&model.Subtitle{}, id).Error
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

func (r *episodeRepo) FindByCriteria(basename string, size *int64, pathHint string) ([]model.Episode, error) {
	var episodes []model.Episode
	query := r.db.Model(&model.Episode{})

	// Match basename at the end of path (e.g., /01.mp4 or /01.mkv)
	basenameLike := "%/" + basename + ".%"
	query = query.Where("(original_relative_path LIKE ? OR video_relative_path LIKE ?)", basenameLike, basenameLike)

	if size != nil {
		query = query.Where("file_size = ?", *size)
	}

	if pathHint != "" {
		pathHintLike := "%" + pathHint + "%"
		query = query.Where("(original_relative_path LIKE ? OR video_relative_path LIKE ?)", pathHintLike, pathHintLike)
	}

	err := query.Find(&episodes).Error
	return episodes, err
}
