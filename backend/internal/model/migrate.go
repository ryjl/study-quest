package model

import (
	"fmt"
	"gorm.io/gorm"
)

// Code split from models.go for navigability.

func AutoMigrate(db *gorm.DB) error {
	err := db.AutoMigrate(
		&Setting{},
		&User{},
		&UserCourseAccess{},
		&Subject{},
		&Tag{},
		&Course{},
		&CourseGrade{},
		&Chapter{},
		&Episode{},
		&Subtitle{},
		&SubtitleJob{},
		&UserPoint{},
		&PointsLedger{},
		&UserProgress{},
		&Badge{},
		&UserBadge{},
		&CourseUnlockTemplate{},
		&UserUnlockOverride{},
		&UserUnlockAllowedEpisode{},
		&AppRelease{},
		&EntertainmentProgress{},
		// Reading Room module
		&ReadingSeries{},
		&ReadingSeriesGrade{},
		&ReadingBook{},
		&ReadingBookGrade{},
		&ReadingArticle{},
		&ReadingArticleGrade{},
		&UserReadingSeriesAccess{},
		&UserReadingBookAccess{},
		&UserReadingArticleAccess{},
		&ReadingBookProgress{},
		// Auth module
		&Session{},
		// Watch history module (per-session viewing events; coexists with the
		// aggregate progress tables above, written alongside them on each report).
		&WatchEvent{},
		// Storage sources module (multi-source: admin configures N netdisk
		// backends; content points at one; user whitelist is the 防呆 gate).
		&StorageSource{},
		&UserStorageSource{},
		// AI module (Step 3 — learning agent). Private to internal/ai; empty and
		// inert when no provider is configured / no course has AI enabled.
		&AIProvider{},
		&AIJob{},
		&ContentChunk{},
		&AISummary{},
		&KnowledgeMemory{},
		&Quiz{},
		&Question{},
		&Answer{},
		&AIRun{},
		// Phase C — agent 驱动的学习建议(advice agent 产出)。
		&StudyAdvice{},
		// Phase D — 课程级总结(course-unique 纯内容总结,agent 驱动综合所有 episode)。
		&AICourseSummary{},
		// Phase E — admin 用户学习报告(agent 驱动,跨课程交叉分析)。
		&UserStudyReport{},
		// PR2 — 字幕润色挖出的术语候选(admin 审核后进 TermDict)。pending 池,
		// 由 polish job 写入,PR2.5 的 admin UI 消费。
		&GlossaryCandidate{},
		// 断点续润 — polish job 的 chunk 级检查点(子表)。每个 chunk 完成落库一行,
		// job 重试时跳过已 done 的 chunk,不再重烧 token。复合唯一索引 (job_id, chunk_index)。
		&AIPolishChunk{},
		// 轻量结构化日志(TODO.md P1)。failJob/reaper/polishStats/provider/worker panic
		// 5 个集中点写入,admin 在 /admin/logs 页看,不再依赖 SSH 看 stderr。
		&LogEntry{},
		// 错题本(TODO.md P0)。交卷时对 correct=false 的题 upsert,记录学生做错的题
		// + 重做次数 + mastered 标记。题面现查 Question 表,本表只存 curation 状态。
		&WrongBookItem{},
		// 课程考试(TODO.md P0)。和 Quiz 平行但 scope 是 (user, course)。
		// 题库抽题 + agent 新出迁移题;答案写独立 ExamAnswer,不污染错题本聚合。
		&Exam{},
		&ExamQuestion{},
		&ExamAnswer{},
		// 课后作业卷。episode 级、不绑 user(通用卷)、AI 单次生成、纯打印纸笔做。
		// 和 Quiz/Exam 平行但无 Answer 表(纯打印不判分)。prompt 配置独立表(每 subject 一份
		// 完整 system prompt,admin 可编辑)。
		&Homework{},
		&HomeworkSection{},
		&HomeworkQuestion{},
		&HomeworkPromptConfig{},
	)
	if err != nil {
		return err
	}
	if err := migrateQuizActiveUniqueIndex(db); err != nil {
		return err
	}
	if err := migrateExamActiveUniqueIndex(db); err != nil {
		return err
	}
	return migrateHomeworkActiveUniqueIndex(db)
}

// migrateExamActiveUniqueIndex 同 quiz 的范式:一个 (user, course) 同时只有一个
// active exam。archived(重考时旧的转 archived)可共存做历史。GORM 表达不了 WHERE,
// 故 raw SQL 在 AutoMigrate 后建。幂等。
func migrateExamActiveUniqueIndex(db *gorm.DB) error {
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_exam_user_course_active ON exams(user_id, course_id) WHERE status = 'active'`).Error; err != nil {
		return fmt.Errorf("create partial unique idx_exam_user_course_active: %w", err)
	}
	return nil
}

// migrateHomeworkActiveUniqueIndex 同 quiz/exam 范式:一个 episode 同时只有一份 active
// 作业。archived(重生成时旧的转 archived)可共存做历史。GORM 表达不了 WHERE,故 raw
// SQL 在 AutoMigrate 后建。幂等。
func migrateHomeworkActiveUniqueIndex(db *gorm.DB) error {
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_homework_episode_active ON homeworks(episode_id) WHERE status = 'active'`).Error; err != nil {
		return fmt.Errorf("create partial unique idx_homework_episode_active: %w", err)
	}
	return nil
}

// migrateQuizActiveUniqueIndex creates the partial unique index that enforces
// the "one ACTIVE quiz per (user, episode)" invariant. Archived rows (regen
// superseds the current quiz) may coexist freely for history; only active
// rows participate in the uniqueness constraint.
//
// GORM's struct tags can't express a WHERE clause, so the partial index is
// created in raw SQL after AutoMigrate. Idempotent (CREATE ... IF NOT EXISTS).
// CreateQuiz's archive-then-insert transaction is the primary guarantee; this
// index is defense-in-depth at the DB layer.
func migrateQuizActiveUniqueIndex(db *gorm.DB) error {
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_quiz_user_ep_active ON quizzes(user_id, episode_id) WHERE status = 'active'`).Error; err != nil {
		return fmt.Errorf("create partial unique idx_quiz_user_ep_active: %w", err)
	}
	return nil
}
