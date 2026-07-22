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
		&ChatSession{},
		&ChatMessage{},
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
	)
	if err != nil {
		return err
	}
	return migrateQuizActiveUniqueIndex(db)
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
