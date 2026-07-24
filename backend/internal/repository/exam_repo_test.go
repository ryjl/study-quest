package repository

import (
	"testing"
	"time"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"
)

// examRepoTestEnv 建一条最小真实链(course + user + chunk + quiz + question),
// 因为 ExamQuestion.QuestionID 是 FK(测试 DB FK off,但 join 查询要行存在)。
// 返回 db + repo + 各 id,供 exam_repo 测试 seed exam。
type examIDs struct {
	courseID, userID, chunkID, quizID, qID uint
}

func seedExamFixture(t *testing.T) (*examRepo, examIDs) {
	t.Helper()
	db := setupTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	course := &model.Course{Title: "Exam Course", SubjectID: subjects["math"].ID}
	db.Create(course)
	episode := &model.Episode{Title: "Exam Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	db.Create(episode)
	user := &model.User{Nickname: "exam-u", PinHash: "x", Role: "student"}
	db.Create(user)
	chunk := &model.ContentChunk{EpisodeID: episode.ID, CourseID: course.ID, SourceType: "subtitle", ChunkIndex: 0, Text: "c"}
	db.Create(chunk)
	quiz := &model.Quiz{EpisodeID: episode.ID, UserID: user.ID, CourseID: course.ID, Status: "active"}
	db.Create(quiz)
	q := &model.Question{QuizID: quiz.ID, ChunkID: chunk.ID, Type: "choice", Stem: "q1", Options: `["a","b"]`, Scoring: `{"correct_index":1}`}
	db.Create(q)
	return &examRepo{db: db}, examIDs{course.ID, user.ID, chunk.ID, quiz.ID, q.ID}
}

// TestCreateExam_ArchivesOldExam 重开考(同 user+course 第二次 CreateExam)应 archive
// 旧的 active exam 而非删,历史保留。守 quiz 的同款 invariant。
func TestCreateExam_ArchivesOldExam(t *testing.T) {
	repo, ids := seedExamFixture(t)
	eq := []model.ExamQuestion{
		{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 0},
	}
	id1, err := repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID}, eq)
	if err != nil {
		t.Fatalf("CreateExam #1: %v", err)
	}
	id2, err := repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID}, eq)
	if err != nil {
		t.Fatalf("CreateExam #2: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("second CreateExam returned same id %d; want fresh row", id1)
	}
	// GetActiveExam 只返回新的 active。
	got, _ := repo.GetActiveExam(ids.userID, ids.courseID)
	if got == nil || got.ID != id2 {
		t.Errorf("active exam = %v; want id %d", got, id2)
	}
	// 旧的 exam 行还在(status=archived)。
	var old model.Exam
	repo.db.First(&old, id1)
	if old.Status != "archived" {
		t.Errorf("old exam status = %q; want archived", old.Status)
	}
	if old.ArchivedAt == nil {
		t.Error("old exam ArchivedAt should be set")
	}
}

// TestCreateExam_ActiveUniqueInvariant 三次重考后必须恰好一个 active。
func TestCreateExam_ActiveUniqueInvariant(t *testing.T) {
	repo, ids := seedExamFixture(t)
	eq := []model.ExamQuestion{{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 0}}
	for i := 0; i < 3; i++ {
		if _, err := repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID}, eq); err != nil {
			t.Fatalf("CreateExam #%d: %v", i, err)
		}
	}
	// 恰好一个 active。
	active, _ := repo.GetActiveExam(ids.userID, ids.courseID)
	if active == nil {
		t.Fatal("expected exactly one active exam; got nil")
	}
	// 两个 archived。
	var archivedCount int64
	repo.db.Model(&model.Exam{}).Where("user_id = ? AND course_id = ? AND status = 'archived'", ids.userID, ids.courseID).Count(&archivedCount)
	if archivedCount != 2 {
		t.Errorf("archived count = %d; want 2", archivedCount)
	}
}

// TestTryMarkExamSubmitted_ConcurrentLock 条件 UPDATE 抢占:第一次成功(claimed=true),
// 第二次失败(claimed=false,已盖戳)。守交卷锁 TOCTOU。
func TestTryMarkExamSubmitted_ConcurrentLock(t *testing.T) {
	repo, ids := seedExamFixture(t)
	id, _ := repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID},
		[]model.ExamQuestion{{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 0}})
	now := time.Now().UTC()

	claimed1, err := repo.TryMarkExamSubmitted(id, now)
	if err != nil || !claimed1 {
		t.Fatalf("first claim: claimed=%v err=%v; want true/nil", claimed1, err)
	}
	claimed2, _ := repo.TryMarkExamSubmitted(id, now)
	if claimed2 {
		t.Error("second claim should fail (already submitted); got claimed=true")
	}
}

// TestGetExamQuestions_Ordered 按 OrderIdx 排序返回。
func TestGetExamQuestions_Ordered(t *testing.T) {
	repo, ids := seedExamFixture(t)
	// 建第二道题(seedExamFixture 建了 qID 一道)。
	q2 := &model.Question{QuizID: ids.quizID, ChunkID: ids.chunkID, Type: "choice", Stem: "q2", Options: `["a","b"]`, Scoring: `{"correct_index":0}`}
	repo.db.Create(q2)
	id, _ := repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID}, []model.ExamQuestion{
		{QuestionID: q2.ID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 1},
		{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 0},
	})
	got, err := repo.GetExamQuestions(id)
	if err != nil {
		t.Fatalf("GetExamQuestions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 questions; got %d", len(got))
	}
	if got[0].OrderIdx != 0 || got[1].OrderIdx != 1 {
		t.Errorf("order = [%d,%d]; want [0,1] (sorted by OrderIdx)", got[0].OrderIdx, got[1].OrderIdx)
	}
}

// TestExamSourceQuality_PoolVsGenerated 对比题源正确率。灌 2 pool 题(1 对 1 错)+ 1 generated
// 题(错),验证聚合分对组 + 正确率。
func TestExamSourceQuality_PoolVsGenerated(t *testing.T) {
	repo, ids := seedExamFixture(t)
	// 建第二道题做 generated。
	q2 := &model.Question{QuizID: ids.quizID, ChunkID: ids.chunkID, Type: "choice", Stem: "gen", Options: `["a","b"]`, Scoring: `{"correct_index":0}`}
	repo.db.Create(q2)

	examID, _ := repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID}, []model.ExamQuestion{
		{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 0},
		{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 1},
		{QuestionID: q2.ID, ChunkID: ids.chunkID, Source: "generated", OrderIdx: 2},
	})
	eqs, _ := repo.GetExamQuestions(examID)
	// 灌交卷作答:pool 两个(1 对 1 错),generated 一个(错)。
	now := time.Now().UTC()
	repo.CreateExamAnswer(&model.ExamAnswer{ExamID: examID, ExamQuestionID: eqs[0].ID, UserID: ids.userID, QuestionID: ids.qID, ChunkID: ids.chunkID, Correct: true, AnsweredAt: now})
	repo.CreateExamAnswer(&model.ExamAnswer{ExamID: examID, ExamQuestionID: eqs[1].ID, UserID: ids.userID, QuestionID: ids.qID, ChunkID: ids.chunkID, Correct: false, AnsweredAt: now})
	repo.CreateExamAnswer(&model.ExamAnswer{ExamID: examID, ExamQuestionID: eqs[2].ID, UserID: ids.userID, QuestionID: q2.ID, ChunkID: ids.chunkID, Correct: false, AnsweredAt: now})

	rows, err := repo.ExamSourceQuality()
	if err != nil {
		t.Fatalf("ExamSourceQuality: %v", err)
	}
	bySrc := map[string]ExamSourceQualityRow{}
	for _, r := range rows {
		bySrc[r.Source] = r
	}
	if p, ok := bySrc["pool"]; !ok {
		t.Error("missing pool row")
	} else {
		if p.Total != 2 || p.Correct != 1 {
			t.Errorf("pool = total%d correct%d; want 2/1", p.Total, p.Correct)
		}
		if p.Rate != 0.5 {
			t.Errorf("pool rate = %v; want 0.5", p.Rate)
		}
	}
	if g, ok := bySrc["generated"]; !ok {
		t.Error("missing generated row")
	} else if g.Total != 1 || g.Correct != 0 || g.Rate != 0 {
		t.Errorf("generated = %+v; want total1 correct0 rate0", g)
	}
}

// TestExamStats_Aggregates 灌几份已交卷 + 未交卷 exam,验证 stats 聚合。
func TestExamStats_Aggregates(t *testing.T) {
	repo, ids := seedExamFixture(t)
	now := time.Now().UTC()
	// 两份已交卷(score 0.8, 0.6),一份未交卷。
	id1, _ := repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID}, []model.ExamQuestion{{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 0}})
	id2, _ := repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID}, []model.ExamQuestion{{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 0}})
	repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID}, []model.ExamQuestion{{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 0}}) // 未交卷
	repo.db.Model(&model.Exam{}).Where("id IN ?", []uint{id1, id2}).Updates(map[string]interface{}{"submitted_at": now, "score": 0.0})
	repo.db.Model(&model.Exam{}).Where("id = ?", id1).Update("score", 0.8)
	repo.db.Model(&model.Exam{}).Where("id = ?", id2).Update("score", 0.6)

	stats, err := repo.ExamStats()
	if err != nil {
		t.Fatalf("ExamStats: %v", err)
	}
	if stats.Total != 3 {
		t.Errorf("total = %d; want 3", stats.Total)
	}
	if stats.Submitted != 2 {
		t.Errorf("submitted = %d; want 2", stats.Submitted)
	}
	// 平均 = (0.8 + 0.6) / 2 = 0.7
	if stats.AvgScore < 0.69 || stats.AvgScore > 0.71 {
		t.Errorf("avg score = %v; want ~0.7", stats.AvgScore)
	}
	if stats.ThisWeek != 3 {
		t.Errorf("this_week = %d; want 3 (all created today)", stats.ThisWeek)
	}
}

// TestListExamsForCourse 列某课程的 exam,按 created DESC。
func TestListExamsForCourse(t *testing.T) {
	repo, ids := seedExamFixture(t)
	repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID}, []model.ExamQuestion{{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 0}})
	repo.CreateExam(&model.Exam{UserID: ids.userID, CourseID: ids.courseID}, []model.ExamQuestion{{QuestionID: ids.qID, ChunkID: ids.chunkID, Source: "pool", OrderIdx: 0}})
	got, err := repo.ListExamsForCourse(ids.courseID, 10)
	if err != nil {
		t.Fatalf("ListExamsForCourse: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2; got %d", len(got))
	}
}
