package repository

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"studyquest/backend/internal/model"
)

// Code split from ai_content_repo.go for navigability. The interface
// and constructor remain in ai_content_repo.go.

func (r *aiContentRepo) GetAdvice(userID uint, scope string, scopeID uint) (*model.StudyAdvice, error) {
	var a model.StudyAdvice
	if err := r.db.Where("user_id = ? AND scope = ? AND scope_id = ?", userID, scope, scopeID).
		First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// UpsertAdvice 替换同一 (user, scope, scope_id) 上的旧 advice。用 GORM
// clause.OnConflict 走 INSERT ... ON CONFLICT DO UPDATE(和 UpsertMemoryOnAnswer 同
// 语义),而不是之前的 delete-then-insert:功能等价(重新生成完全覆盖旧快照,符合 advice
// 的"当前建议"语义——不像 quiz 要保留历史),且无 delete+insert 之间的并发窗口。
// 冲突目标 = uniqueIndex(user_id, scope, scope_id);更新所有可变业务列。
func (r *aiContentRepo) UpsertAdvice(a *model.StudyAdvice) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"}, {Name: "scope"}, {Name: "scope_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"advice_text", "mastery_snapshot_json", "model_used", "generated_at", "updated_at",
		}),
	}).Create(a).Error
}

// ListUserAdvice 列出某用户的所有 advice(所有 scope 和 scope_id)。给 admin 控制台
// "这个学生有哪些 advice + 删除按钮"用。按 generated_at DESC。
func (r *aiContentRepo) ListUserAdvice(userID uint) ([]model.StudyAdvice, error) {
	var rows []model.StudyAdvice
	if err := r.db.Where("user_id = ?", userID).Order("generated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// DeleteAdvice 物理删某 (user, scope, scope_id) 的 advice。多态 scope_id 不影响
// 这里 —— 删除按三元组定位,语义同 GetAdvice。
func (r *aiContentRepo) DeleteAdvice(userID uint, scope string, scopeID uint) error {
	return r.db.Where("user_id = ? AND scope = ? AND scope_id = ?", userID, scope, scopeID).
		Delete(&model.StudyAdvice{}).Error
}

// --- ai_course_summaries (Phase D: course-unique 课程级总结) ---

// GetCourseSummary 按 course_id 取该课程的总结(unique index 保证最多一条)。无记录返回
// (nil, nil),让客户端 handler 据此返回 "无总结"(404),或让 admin handler 触发生成。
// 课程总结是 course-unique 的,不按 user 区分。
func (r *aiContentRepo) GetCourseSummary(courseID uint) (*model.AICourseSummary, error) {
	var s model.AICourseSummary
	if err := r.db.Where("course_id = ?", courseID).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// UpsertCourseSummary 替换同一 course_id 上的旧课程总结(ON CONFLICT,同 UpsertAdvice 语义):
// 重新生成完全覆盖旧总结,符合课程总结的"当前导览"语义(course-unique,所有学生共享,不需要
// 历史)。冲突目标 = uniqueIndex(course_id);更新所有可变业务列。
func (r *aiContentRepo) UpsertCourseSummary(s *model.AICourseSummary) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "course_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"summary_text", "model_used", "generated_at", "episode_count_at_gen", "updated_at"}),
	}).Create(s).Error
}

// DeleteCourseSummary 物理删某课程的总结(unique on course_id,最多一条)。
func (r *aiContentRepo) DeleteCourseSummary(courseID uint) error {
	return r.db.Where("course_id = ?", courseID).Delete(&model.AICourseSummary{}).Error
}

// --- user_study_reports (Phase E: admin 跨课程学习报告) ---

// GetUserStudyReport 按 user_id 取该用户的最新学习报告(unique index 保证最多一条)。
// 无记录返回 (nil, nil),调用方(admin handler)据此返回 "无报告" 或触发生成。
func (r *aiContentRepo) GetUserStudyReport(userID uint) (*model.UserStudyReport, error) {
	var rep model.UserStudyReport
	if err := r.db.Where("user_id = ?", userID).First(&rep).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rep, nil
}

// UpsertUserStudyReport 替换该用户的旧报告(ON CONFLICT,同 UpsertAdvice 语义):
// 重新生成完全覆盖旧报告,符合"当前最新画像"的语义。冲突目标 = uniqueIndex(user_id);
// 更新所有可变业务列。无 delete+insert 并发窗口。
func (r *aiContentRepo) UpsertUserStudyReport(rep *model.UserStudyReport) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"report_text", "model_used", "generated_at", "updated_at"}),
	}).Create(rep).Error
}

// DeleteUserReport 物理删某用户的学习报告(unique on user_id,最多一条)。
func (r *aiContentRepo) DeleteUserReport(userID uint) error {
	return r.db.Where("user_id = ?", userID).Delete(&model.UserStudyReport{}).Error
}
