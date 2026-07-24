package repository

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"
)

// qptIDs 收集 question_pool_repo 测试 fixture 里各行的主键,供断言用。
type qptIDs struct {
	subjectID                              uint
	mathCourseID, chineseCourseID          uint // 两门课(测按课程过滤 + 跨课程不串)
	ep1ID, ep2ID                           uint // 同课程两 episode(测跨 episode 聚合)
	userID, otherUserID                    uint // 测用户隔离
	chunk1ID, chunk2ID                     uint // 测按 chunk 过滤
	quiz1ID, quiz2ID, archivedQuizID       uint // ep1 active + ep2 active + ep1 archived
	q1ID, q3ID, q4ID, synthQID             uint // q1(ep1 正常) q3(ep2) q4(archived) 合成题 q2
	mathQID                                uint // 另一门课的题(应被按课程过滤排除)
}

// seedQuestionPool 灌入一条完整真实链(subject→course→episode→(chunk,quiz→question)
// →answer)。question_pool_repo 的查询靠 JOIN quizzes/questions/courses,这些行必须
// 真实存在。返回的 db 供测试做额外 raw 操作/断言。只在测试间复用,故放在本文件。
func seedQuestionPool(t *testing.T) (*gorm.DB, qptIDs) {
	t.Helper()
	db := setupTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	mathSubject, chineseSubject := subjects["math"].ID, subjects["chinese"].ID

	mathCourse := &model.Course{Title: "Math", SubjectID: mathSubject}
	chineseCourse := &model.Course{Title: "Chinese", SubjectID: chineseSubject}
	db.Create(mathCourse)
	db.Create(chineseCourse)

	ep1 := &model.Episode{Title: "ep1", CourseID: mathCourse.ID, VideoRelativePath: "/1.mp4", SortOrder: 1}
	ep2 := &model.Episode{Title: "ep2", CourseID: mathCourse.ID, VideoRelativePath: "/2.mp4", SortOrder: 2}
	db.Create(ep1)
	db.Create(ep2)

	user := &model.User{Nickname: "u1", PinHash: "x", Role: "student"}
	otherUser := &model.User{Nickname: "u2", PinHash: "x", Role: "student"}
	db.Create(user)
	db.Create(otherUser)

	chunk1 := &model.ContentChunk{EpisodeID: ep1.ID, CourseID: mathCourse.ID, SourceType: "subtitle", ChunkIndex: 0, Text: "c1"}
	chunk2 := &model.ContentChunk{EpisodeID: ep2.ID, CourseID: mathCourse.ID, SourceType: "subtitle", ChunkIndex: 0, Text: "c2"}
	db.Create(chunk1)
	db.Create(chunk2)

	// ep1 active quiz: q1(正常,chunk1) + q2(合成,chunkID=0)。
	quiz1 := &model.Quiz{EpisodeID: ep1.ID, UserID: user.ID, CourseID: mathCourse.ID, Status: "active"}
	db.Create(quiz1)
	q1 := &model.Question{QuizID: quiz1.ID, ChunkID: chunk1.ID, Type: "choice", Stem: "q1", Options: `["a","b"]`, Scoring: `{"correct_index":0}`}
	q2 := &model.Question{QuizID: quiz1.ID, ChunkID: 0, Type: "choice", Stem: "q2-synth", Options: `["a","b"]`, Scoring: `{"correct_index":0}`}
	db.Create(q1)
	db.Create(q2)

	// ep1 archived quiz: q4(测跨 generation 聚合)。
	archivedQuiz := &model.Quiz{EpisodeID: ep1.ID, UserID: user.ID, CourseID: mathCourse.ID, Status: "archived"}
	db.Create(archivedQuiz)
	q4 := &model.Question{QuizID: archivedQuiz.ID, ChunkID: chunk1.ID, Type: "fill", Stem: "q4-old", Scoring: `{"accept":["x"]}`}
	db.Create(q4)

	// ep2 active quiz: q3。
	quiz2 := &model.Quiz{EpisodeID: ep2.ID, UserID: user.ID, CourseID: mathCourse.ID, Status: "active"}
	db.Create(quiz2)
	q3 := &model.Question{QuizID: quiz2.ID, ChunkID: chunk2.ID, Type: "choice", Stem: "q3", Options: `["a","b"]`, Scoring: `{"correct_index":1}`}
	db.Create(q3)

	// 语文课的题(应被数学课的查询排除)。
	chineseEp := &model.Episode{Title: "ch-ep", CourseID: chineseCourse.ID, VideoRelativePath: "/c.mp4", SortOrder: 1}
	db.Create(chineseEp)
	chineseChunk := &model.ContentChunk{EpisodeID: chineseEp.ID, CourseID: chineseCourse.ID, SourceType: "subtitle", ChunkIndex: 0, Text: "cc"}
	db.Create(chineseChunk)
	chineseQuiz := &model.Quiz{EpisodeID: chineseEp.ID, UserID: user.ID, CourseID: chineseCourse.ID, Status: "active"}
	db.Create(chineseQuiz)
	mathQ := &model.Question{QuizID: chineseQuiz.ID, ChunkID: chineseChunk.ID, Type: "choice", Stem: "excluded", Options: `["a","b"]`, Scoring: `{"correct_index":0}`}
	db.Create(mathQ)

	// Answer: user 在数学课 q1/q3/q4 都错,q2 合成题做对。otherUser 也错 q1。
	now := time.Now().UTC()
	db.Create(&model.Answer{QuestionID: q1.ID, QuizID: quiz1.ID, UserID: user.ID, UserAnswer: 1, Correct: false, AnsweredAt: now})
	db.Create(&model.Answer{QuestionID: q2.ID, QuizID: quiz1.ID, UserID: user.ID, UserAnswer: 0, Correct: true, AnsweredAt: now})
	db.Create(&model.Answer{QuestionID: q3.ID, QuizID: quiz2.ID, UserID: user.ID, UserAnswer: 0, Correct: false, AnsweredAt: now})
	db.Create(&model.Answer{QuestionID: q4.ID, QuizID: archivedQuiz.ID, UserID: user.ID, UserAnswer: -1, UserAnswerText: "wrong", Correct: false, AnsweredAt: now})
	db.Create(&model.Answer{QuestionID: q1.ID, QuizID: quiz1.ID, UserID: otherUser.ID, UserAnswer: 1, Correct: false, AnsweredAt: now})

	return db, qptIDs{
		subjectID:        mathSubject,
		mathCourseID:     mathCourse.ID,
		chineseCourseID:  chineseCourse.ID,
		ep1ID:            ep1.ID,
		ep2ID:            ep2.ID,
		userID:           user.ID,
		otherUserID:      otherUser.ID,
		chunk1ID:         chunk1.ID,
		chunk2ID:         chunk2.ID,
		quiz1ID:          quiz1.ID,
		quiz2ID:          quiz2.ID,
		archivedQuizID:   archivedQuiz.ID,
		q1ID:             q1.ID,
		q3ID:             q3.ID,
		q4ID:             q4.ID,
		synthQID:         q2.ID,
		mathQID:          mathQ.ID,
	}
}

// helper: 把 rows 按 question_id 收集成 set,断言用。
func idsFromRows(rows []WrongBookRow) map[uint]struct{} {
	out := make(map[uint]struct{}, len(rows))
	for _, r := range rows {
		out[r.QuestionID] = struct{}{}
	}
	return out
}
