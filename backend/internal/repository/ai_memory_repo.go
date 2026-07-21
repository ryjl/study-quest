package repository

import (
	"studyquest/backend/internal/model"
)

// Code split from ai_content_repo.go for navigability. The interface
// and constructor remain in ai_content_repo.go.

func (r *aiContentRepo) GetMasteries(userID, episodeID uint) ([]model.KnowledgeMemory, error) {
	var rows []model.KnowledgeMemory
	if err := r.db.Where("user_id = ? AND episode_id = ?", userID, episodeID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// UpsertMemoryOnAnswer atomically applies the +0.1/-0.2 mastery update using
// INSERT ... ON CONFLICT DO UPDATE. This is the feedback-loop write path and
// MUST be atomic: a concurrent answer (e.g. rapid-fire submits) doing
// read-modify-write would lose deltas — same bug class as the progress
// watch-seconds accumulation, prevented the same way (single SQL statement).
//
// The mastery math is duplicated from agent.ApplyMastery intentionally — the DB
// is the source of truth here (the Go helper is for tests that don't touch the
// DB). Both apply identical clamp + step rules; keep them in sync.
func (r *aiContentRepo) UpsertMemoryOnAnswer(userID, chunkID, episodeID, courseID uint, correct bool) error {
	// Both the INSERT (first answer for this chunk) and the ON CONFLICT UPDATE
	// (subsequent answers) must apply the same mastery delta + counter bump. The
	// INSERT path uses the initial mastery (0) + the delta; the UPDATE path reads
	// the existing mastery. deltas: correct +0.1 (clamp 1.0), wrong -0.2 (clamp 0).
	var deltaUpdate, fieldUpdate, initMastery string
	var initCorrect, initWrong int
	if correct {
		deltaUpdate = "MIN(mastery + 0.1, 1.0)"
		fieldUpdate = "correct_count = correct_count + 1"
		initMastery = "0.1"
		initCorrect = 1
	} else {
		deltaUpdate = "MAX(mastery - 0.2, 0.0)"
		fieldUpdate = "wrong_count = wrong_count + 1"
		initMastery = "0.0" // 0 - 0.2 clamped to 0
		initWrong = 1
	}
	// uniqueIndex(user_id, chunk_id) is the conflict target. episode_id/course_id
	// are part of the INSERT (for new rows) but not the conflict key.
	sql := `INSERT INTO knowledge_memories (user_id, episode_id, course_id, chunk_id, mastery, correct_count, wrong_count, last_reviewed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, chunk_id) DO UPDATE SET
			mastery = ` + deltaUpdate + `,
			` + fieldUpdate + `,
			last_reviewed = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP`
	return r.db.Exec(sql, userID, episodeID, courseID, chunkID, initMastery, initCorrect, initWrong).Error
}

// --- cross-course aggregation (Phase C: advice agent) ---

// GetCourseMasteries 跨课时聚合:KnowledgeMemory 已冗余 course_id,所以一次 WHERE
// 取出该学生在该课程下所有 (episode, chunk) 的掌握度行。不在此处排序——
// MemoryStore.CourseMasteries 统一按 mastery ASC 排序,保持与 Masteries 一致
// (弱点优先)的语义。
func (r *aiContentRepo) GetCourseMasteries(userID, courseID uint) ([]model.KnowledgeMemory, error) {
	var rows []model.KnowledgeMemory
	if err := r.db.Where("user_id = ? AND course_id = ?", userID, courseID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetSubjectMasteries 科目级聚合:JOIN courses(courses.subject_id = ?)取该科目下
// 所有课程的 mastery 行。一次 SQL,避免在应用层逐课程循环(科目下课程数通常十几条,
// 但 JOIN 更省往返)。用 Joins 而不是 Preload,因为我们只要 knowledge_memories 行,
// 不要把 Course 整个加载进每条 memory(预加载会塞进 CourseID 的关联对象,浪费且改变结构)。
func (r *aiContentRepo) GetSubjectMasteries(userID, subjectID uint) ([]model.KnowledgeMemory, error) {
	var rows []model.KnowledgeMemory
	if err := r.db.
		Joins("JOIN courses ON courses.id = knowledge_memories.course_id").
		Where("knowledge_memories.user_id = ? AND courses.subject_id = ?", userID, subjectID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// --- study_advices (Phase C: advice generation result) ---

// GetAdvice 按 (user, scope, scope_id) 唯一定位一条 advice。scope ∈
// {"episode","course","subject"}。scope_id 是对应实体(id)。无记录返回 (nil, nil),
// 让调用方决定是入队 job 还是返回 unavailable。
