package repository

import (
	"studyquest/backend/internal/model"
)

// Code split from ai_content_repo.go for navigability. The interface
// and constructor remain in ai_content_repo.go.
//
// 本文件服务「错题本 + 课程考试」主题(见 docs/modules/ai/ 与 TODO.md)。两类查询:
//   - 错题聚合:从 append-only 的 answers 表里把 correct=false 的行 join 出来,带
//     上 question 的题面/chunk 和 quiz 的 course/episode/subject 上下文。错题本
//     的"视图"语义——原始题面永远现查,不冗余拷贝。
//   - 考试抽题池:跨 episode 取某 course 下全部 question(去重题面),供 exam_selector
//     按 mastery 弱点加权抽题。CourseID 在 ContentChunk/Quiz 上都冗余,一次 WHERE 就够。

// WrongBookRow 是错题本列表的一行:把 Answer(谁、何时、错没错)和 Question(题面、
// 知识点 chunk)、Quiz(归属哪门课哪节课)的上下文 join 到一起。供 service 层组装给
// 客户端;WrongBookItem 表只额外存 curation 状态(mastered / 重做次数)。
type WrongBookRow struct {
	AnswerID    uint
	QuestionID  uint
	QuizID      uint
	UserID      uint
	ChunkID     uint
	EpisodeID   uint
	CourseID    uint
	SubjectID   uint
	Stem        string
	Type        string
	Options     string
	// Scoring 带下来供 service 层派生正确答案(choice→CorrectIndex, fill→CorrectText,
	// multi→CorrectIndices)。Scoring 是唯一判分元数据来源(2026-07-27 删 Answer/AnswerText 列后)。
	Scoring     string
	Explanation string
	HasJump     bool
	AnsweredAt  string // RFC3339,直接给客户端展示
}

// ListWrongAnswersByUserCourse 列出某用户在某课程下全部做错的题(跨 episode、
// 跨 quiz generation)。是错题本「按课程」视图的数据源。
//
// 作用域:`answers.user_id = ? AND answers.correct = false AND answers.quiz_id IN
// (SELECT id FROM quizzes WHERE course_id = ?)`。用 quiz_id 子查询而不是直接 JOIN
// question,因为 regenerate(换题)会删旧 question——直接 JOIN 会漏掉指向已删 question
// 的历史 answer(参考 ListAnswersForQuiz 的同款设计)。QuizID 在 answer 上是快照,
// 指向答题当时存在的 quiz 行;archived quiz 行也保留,所以跨 generation 的错题都纳入。
func (r *aiContentRepo) ListWrongAnswersByUserCourse(userID, courseID uint) ([]WrongBookRow, error) {
	var rows []WrongBookRow
	err := r.db.Table("answers").
		Select(`answers.id AS answer_id, answers.question_id AS question_id, answers.quiz_id AS quiz_id,
			answers.user_id AS user_id,
			COALESCE(questions.chunk_id, 0) AS chunk_id,
			quizzes.episode_id AS episode_id,
			quizzes.course_id AS course_id,
			courses.subject_id AS subject_id,
			questions.stem AS stem, questions.type AS type, questions.options AS options,
			questions.scoring AS scoring,
			questions.explanation AS explanation,
			questions.has_jump AS has_jump,
			answers.answered_at AS answered_at`).
		Joins("LEFT JOIN questions ON questions.id = answers.question_id").
		Joins("JOIN quizzes ON quizzes.id = answers.quiz_id").
		Joins("JOIN courses ON courses.id = quizzes.course_id").
		Where("answers.user_id = ? AND answers.correct = 0 AND quizzes.course_id = ?", userID, courseID).
		Order("answers.answered_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ListWrongAnswersByUser 列出某用户的全部错题(全平台,跨课程)。是错题本顶层
// 「全部错题」视图的数据源。可按 subject/course/chunk 过滤(过滤值 0 = 不过滤)。
func (r *aiContentRepo) ListWrongAnswersByUser(userID, subjectID, courseID, chunkID uint) ([]WrongBookRow, error) {
	q := r.db.Table("answers").
		Select(`answers.id AS answer_id, answers.question_id AS question_id, answers.quiz_id AS quiz_id,
			answers.user_id AS user_id,
			COALESCE(questions.chunk_id, 0) AS chunk_id,
			quizzes.episode_id AS episode_id,
			quizzes.course_id AS course_id,
			courses.subject_id AS subject_id,
			questions.stem AS stem, questions.type AS type, questions.options AS options,
			questions.scoring AS scoring,
			questions.explanation AS explanation,
			questions.has_jump AS has_jump,
			answers.answered_at AS answered_at`).
		Joins("LEFT JOIN questions ON questions.id = answers.question_id").
		Joins("JOIN quizzes ON quizzes.id = answers.quiz_id").
		Joins("JOIN courses ON courses.id = quizzes.course_id").
		Where("answers.user_id = ? AND answers.correct = 0", userID)
	if subjectID != 0 {
		q = q.Where("courses.subject_id = ?", subjectID)
	}
	if courseID != 0 {
		q = q.Where("quizzes.course_id = ?", courseID)
	}
	if chunkID != 0 {
		q = q.Where("questions.chunk_id = ?", chunkID)
	}
	var rows []WrongBookRow
	if err := q.Order("answers.answered_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ExamPoolQuestion 是考试抽题池里的一道题:question 本体 + 它所属的 episode/chunk
// 上下文(用于覆盖度约束,确保一张卷子覆盖多个 episode/chunk 而不是全挤在一个上)。
type ExamPoolQuestion struct {
	model.Question
	EpisodeID uint
}

// ListQuestionsByCourseForExam 取某课程下全部 questions(跨 episode、跨用户)作为考试
// 抽题池。去重题面(同一 stem 只保留最新一条),避免同题反复出现。JOIN quiz 拿 episode
// 上下文 + subject(给 service 做题型均衡 / 覆盖度)。
//
// 注意:这里按 quiz.course_id 作用域,archived quiz 的题也算——题库是历史积累,不该
// 因为 regenerate 丢掉旧题。只取有 chunk_id 的题(合成题 chunkID=0 没有知识点锚点,
// 考试抽题价值低,排除)。
func (r *aiContentRepo) ListQuestionsByCourseForExam(courseID uint) ([]ExamPoolQuestion, error) {
	var rows []ExamPoolQuestion
	err := r.db.Table("questions").
		Select(`questions.id, questions.quiz_id, questions.chunk_id, questions.type, questions.stem,
			questions.options, questions.scoring,
			questions.explanation, questions.has_jump, questions.created_at,
			quizzes.episode_id AS episode_id`).
		Joins("JOIN quizzes ON quizzes.id = questions.quiz_id").
		Where("quizzes.course_id = ? AND questions.chunk_id > 0", courseID).
		Order("questions.id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ListChunksByCourseForExam 取某课程下全部 content chunks(跨 episode,subtitle 源),
// 供 exam_selector 在「无足够题库」时退化用——或给 quizzer agent 在课程级新出题时
// 作为上下文输入。去掉了 ListChunks 的单 episode 过滤。
func (r *aiContentRepo) ListChunksByCourseForExam(courseID uint) ([]model.ContentChunk, error) {
	var chunks []model.ContentChunk
	if err := r.db.Where("course_id = ? AND source_type = ?", courseID, "subtitle").
		Order("episode_id ASC, chunk_index ASC").
		Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}
