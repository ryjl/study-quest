package repository

import (
	"gorm.io/gorm"
	"studyquest/backend/internal/model"
)

// Code split from ai_content_repo.go for navigability. The interface
// and constructor remain in ai_content_repo.go.

func (r *aiContentRepo) ReplaceChunksForEpisode(episodeID, courseID uint, sourceType string, chunks []model.ContentChunk) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("episode_id = ? AND source_type = ?", episodeID, sourceType).
			Delete(&model.ContentChunk{}).Error; err != nil {
			return err
		}
		if len(chunks) == 0 {
			return nil
		}
		// Stamp the FK fields the caller shouldn't have to repeat per row.
		for i := range chunks {
			chunks[i].EpisodeID = episodeID
			chunks[i].CourseID = courseID
			chunks[i].SourceType = sourceType
		}
		return tx.Create(&chunks).Error
	})
}

func (r *aiContentRepo) ListChunks(episodeID uint, sourceType string) ([]model.ContentChunk, error) {
	var chunks []model.ContentChunk
	q := r.db.Where("episode_id = ?", episodeID)
	if sourceType != "" {
		q = q.Where("source_type = ?", sourceType)
	}
	if err := q.Order("chunk_index ASC").Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

func (r *aiContentRepo) HasChunks(episodeID uint, sourceType string) (bool, error) {
	var count int64
	if err := r.db.Model(&model.ContentChunk{}).
		Where("episode_id = ? AND source_type = ?", episodeID, sourceType).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *aiContentRepo) CountChunks(episodeID uint) (int64, error) {
	var count int64
	if err := r.db.Model(&model.ContentChunk{}).
		Where("episode_id = ?", episodeID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// --- ai_summaries ---
