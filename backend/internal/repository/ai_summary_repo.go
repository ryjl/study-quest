package repository

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"studyquest/backend/internal/model"
)

// Code split from ai_content_repo.go for navigability. The interface
// and constructor remain in ai_content_repo.go.

func (r *aiContentRepo) GetSummary(episodeID uint) (*model.AISummary, error) {
	var s model.AISummary
	if err := r.db.Where("episode_id = ?", episodeID).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *aiContentRepo) UpsertSummary(s *model.AISummary) error {
	// UPSERT on episode_id (uniqueIndex). 重新生成必须覆盖旧行——admin 点"重新生成"
	// 时旧行还在,如果用 db.Save(s) 而 s.ID=0,GORM 会走 INSERT,撞 uniqueIndex 报
	// "duplicated key not allowed"。改用 ON CONFLICT DO UPDATE 与 UpsertCourseSummary
	// 同模式:冲突时更新所有可变业务列(保留原 ID/CreatedAt)。
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "episode_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"course_id", "summary_json", "model_used", "updated_at"}),
	}).Create(s).Error
}

// DeleteSummary 物理删某 episode 的 summary(unique on episode_id,最多一条)。
func (r *aiContentRepo) DeleteSummary(episodeID uint) error {
	return r.db.Where("episode_id = ?", episodeID).Delete(&model.AISummary{}).Error
}

// ListEpisodeIDsWithSummaryByCourse 返回某课程下已有 summary 的 episode id 列表。
// AISummary 自带 CourseID(冗余字段,enqueue 时写入),所以不用 JOIN episodes。
func (r *aiContentRepo) ListEpisodeIDsWithSummaryByCourse(courseID uint) ([]uint, error) {
	var ids []uint
	if err := r.db.Model(&model.AISummary{}).
		Where("course_id = ?", courseID).
		Pluck("episode_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// CountEpisodesWithSummaryByCourse 给课程总览的陈旧检测用。
func (r *aiContentRepo) CountEpisodesWithSummaryByCourse(courseID uint) (int64, error) {
	var count int64
	if err := r.db.Model(&model.AISummary{}).
		Where("course_id = ?", courseID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// --- ai_jobs ---
