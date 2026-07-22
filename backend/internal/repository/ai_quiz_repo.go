package repository

import (
	"errors"
	"gorm.io/gorm"
	"studyquest/backend/internal/model"
	"time"
)

// Code split from ai_content_repo.go for navigability. The interface
// and constructor remain in ai_content_repo.go.

func (r *aiContentRepo) GetQuiz(userID, episodeID uint) (*model.Quiz, error) {
	var q model.Quiz
	// Only the active quiz is "current" — archived rows are history. The default
	// 'active' on the column means rows created before Phase 3 (no explicit
	// status) are also treated as active, preserving the old single-quiz view.
	if err := r.db.Where("user_id = ? AND episode_id = ? AND status = 'active'", userID, episodeID).
		First(&q).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &q, nil
}

func (r *aiContentRepo) GetQuizByID(quizID uint) (*model.Quiz, error) {
	var q model.Quiz
	if err := r.db.First(&q, quizID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &q, nil
}

// DeleteQuiz 物理删一条 quiz。Question/Answer 通过 FK OnDelete:CASCADE 自动跟随清除
// (2026-07-19 加的 FK,以前删 quiz 会把 Question/Answer 留成孤儿)。和 archive 不同:
// archive 翻 status='archived' 保留历史;delete 彻底清除。供 admin 控制台"删除"按钮。
func (r *aiContentRepo) DeleteQuiz(quizID uint) error {
	return r.db.Delete(&model.Quiz{}, quizID).Error
}

func (r *aiContentRepo) GetQuestions(quizID uint) ([]model.Question, error) {
	var qs []model.Question
	if err := r.db.Where("quiz_id = ?", quizID).Order("id ASC").Find(&qs).Error; err != nil {
		return nil, err
	}
	return qs, nil
}

// CreateQuiz replaces the (user, episode) quiz in one transaction: ARCHIVE the
// prior active quiz (keep its row + questions for history), then insert the new
// active quiz + questions. Answers + memory are NOT touched — a quiz refresh is
// not an amnesia event for the student.
//
// Why archive (not delete): Phase 3 keeps the student's past generations visible
// in the read-only history panel. The old quiz's questions stay attached to it
// (quiz_id FK) so history can show what was asked. Answers already snapshot
// QuizID, so they keep pointing at the archived quiz regardless.
func (r *aiContentRepo) CreateQuiz(quiz *model.Quiz, questions []model.Question) (uint, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Find the existing ACTIVE quiz for this (user, episode) and archive it.
		// Scoping to status='active' is important: archived rows from earlier
		// regens must be left untouched, otherwise we'd overwrite their
		// ArchivedAt and lose ordering in the history panel.
		var old model.Quiz
		findErr := tx.Where("user_id = ? AND episode_id = ? AND status = 'active'", quiz.UserID, quiz.EpisodeID).
			First(&old).Error
		if findErr == nil {
			// Flip the old quiz to archived in place — keep the row + its
			// questions (the history view lists them). ArchivedAt drives the
			// newest-first ordering of the history panel.
			now := time.Now().UTC()
			if err := tx.Model(&old).Updates(map[string]interface{}{
				"status":      "archived",
				"archived_at": now,
			}).Error; err != nil {
				return err
			}
		} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		// Insert the new active quiz row. The partial unique index
		// idx_quiz_user_ep_active is the DB-level guard that the archive step
		// ran first; within this single transaction the app-layer ordering above
		// already guarantees it.
		quiz.ID = 0      // ensure insert, not accidental update
		quiz.Status = "" // let the column default 'active' apply (avoid hardcoding)
		if err := tx.Create(quiz).Error; err != nil {
			return err
		}
		// Stamp the FK on each question and bulk-insert.
		for i := range questions {
			questions[i].QuizID = quiz.ID
		}
		if len(questions) > 0 {
			if err := tx.Create(&questions).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return quiz.ID, nil
}

// ListArchivedQuizzes returns a (user, episode)'s superseded quiz generations,
// newest-archive-first. Each archived row carries its original questions (read
// via GetQuestions) so the history panel can render a fully read-only past
// attempt including correct answers.
func (r *aiContentRepo) ListArchivedQuizzes(userID, episodeID uint) ([]model.Quiz, error) {
	var quizzes []model.Quiz
	if err := r.db.Where("user_id = ? AND episode_id = ? AND status = 'archived'", userID, episodeID).
		Order("archived_at DESC").Find(&quizzes).Error; err != nil {
		return nil, err
	}
	return quizzes, nil
}

func (r *aiContentRepo) ListQuizzesForUser(userID uint) ([]model.Quiz, error) {
	var quizzes []model.Quiz
	if err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&quizzes).Error; err != nil {
		return nil, err
	}
	return quizzes, nil
}

func (r *aiContentRepo) ListAnswersForQuiz(quizID, userID uint) ([]model.Answer, error) {
	// Answers carry a snapshot QuizID (set at submit time), so we scope by user +
	// quiz WITHOUT joining questions. This is deliberate: regenerate (换题) deletes
	// old questions, which would orphan a question-join. The QuizID on historical
	// answers refers to the quiz row that existed at answer time; since a (user,
	// episode) has at most one quiz row at a time but it's replaced on regen, we
	// also include answers whose QuizID differs from the current one but share the
	// episode — giving the full attempt history across regenerations.
	// Resolve the episode from the current quiz first.
	var quiz model.Quiz
	if err := r.db.Select("episode_id").First(&quiz, quizID).Error; err != nil {
		return nil, err
	}
	var answers []model.Answer
	// All answers by this user on this episode (across all quiz generations).
	// We match on episode via the quiz_id → quiz → episode relationship for
	// answers that still point at a live quiz, OR fall back to the snapshot. The
	// simplest correct query: answers.user_id match + the answer's question or
	// snapshot quiz belongs to this episode. Since QuizID is snapshotted and
	// points to potentially-deleted quiz rows, join through a subquery of quiz
	// ids for this episode.
	err := r.db.
		Where("user_id = ? AND quiz_id IN (SELECT id FROM quizzes WHERE episode_id = ?)", userID, quiz.EpisodeID).
		Order("answered_at DESC").
		Find(&answers).Error
	if err != nil {
		return nil, err
	}
	return answers, nil
}

func (r *aiContentRepo) CreateAnswer(a *model.Answer) error {
	return r.db.Create(a).Error
}

// MarkQuizSubmitted 给 quiz 盖已交卷时间戳。用 map 形式 Updates 只写一列,避免
// GORM 把零值字段一起带写。幂等:重复调用只刷新时间戳。
func (r *aiContentRepo) MarkQuizSubmitted(quizID uint, at time.Time) error {
	return r.db.Model(&model.Quiz{}).Where("id = ?", quizID).
		Updates(map[string]interface{}{"submitted_at": at}).Error
}

// TryMarkQuizSubmitted 是并发安全的"抢占式交卷戳":仅当 submitted_at 仍为 NULL 时
// 才盖戳。返回 (claimed, error):claimed=true 表示本次调用抢到了(本请求是唯一赢家),
// claimed=false 表示已被别人抢过(并发重复交卷)。用条件 UPDATE 实现,SQLite/MySQL
// 都能原子完成"check + set",消除 SubmitAllQuizAnswers 的 TOCTOU 窗口
// (GetQuiz 的非事务 nil 检查到 MarkQuizSubmitted 之间,两个请求都能通过)。
//
// 用法:SubmitAllQuizAnswers 在 GetQuiz 之后、落任何 answer/memory 之前调这个;
// claimed=false 直接返回 ErrQuizAlreadySubmitted。这样败者不会落重复 Answer 行、
// 不会重复扣 mastery。SQLite 下 UPDATE 自动拿行锁,两并发请求串行化于此。
func (r *aiContentRepo) TryMarkQuizSubmitted(quizID uint, at time.Time) (bool, error) {
	res := r.db.Model(&model.Quiz{}).
		Where("id = ? AND submitted_at IS NULL", quizID).
		Update("submitted_at", at)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// --- knowledge_memories (Phase C feedback loop) ---
