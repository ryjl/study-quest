package service

import (
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// examServiceTestEnv 建真实 aiService(file-backed DB + 全 repo,无 provider),
// 并 seed 一条完整链:course + episode + user + chunk + 3 道 anchor 题(满足开考 minPool=3)。
// 返回 svc + repo + 各 id + 3 个 question id。StartExam/SubmitExam 测试用。
func examServiceTestEnv(t *testing.T, qCount int) (svc *aiService, contentRepo repository.AIContentRepository, examRepo repository.ExamRepository, userID, courseID, episodeID uint, qIDs []uint) {
	t.Helper()
	db := testutil.NewFileDB(t)
	contentRepo = repository.NewAIContentRepository(db)
	examRepo = repository.NewExamRepository(db)
	wrongBookRepo := repository.NewWrongBookRepository(db)
	s := NewAIService(db, contentRepo, repository.NewEpisodeRepository(db), repository.NewCourseRepository(db),
		nil, nil, repository.NewUserRepository(db), nil, nil, nil, nil, wrongBookRepo, examRepo, nil).(*aiService)
	t.Cleanup(s.Stop)

	subjects := testutil.SeedSubjects(t, db)
	course := &model.Course{Title: "Exam Course", SubjectID: subjects["math"].ID}
	db.Create(course)
	episode := &model.Episode{Title: "Exam Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	db.Create(episode)
	user := &model.User{Nickname: "exam-u", PinHash: "x", Role: "student"}
	db.Create(user)
	chunk := &model.ContentChunk{EpisodeID: episode.ID, CourseID: course.ID, SourceType: "subtitle", ChunkIndex: 0, Text: "c"}
	db.Create(chunk)
	// 一个 quiz 挂 qCount 道 anchor 题(chunkID != 0,满足考试池过滤)。
	quiz := &model.Quiz{EpisodeID: episode.ID, UserID: user.ID, CourseID: course.ID, Status: "active"}
	db.Create(quiz)
	ids := make([]uint, 0, qCount)
	for i := 0; i < qCount; i++ {
		q := &model.Question{
			QuizID: quiz.ID, ChunkID: chunk.ID, Type: "choice",
			Stem: "q", Options: `["a","b"]`, Scoring: `{"correct_index":1}`,
		}
		db.Create(q)
		ids = append(ids, q.ID)
	}
	return s, contentRepo, examRepo, user.ID, course.ID, episode.ID, ids
}

// TestStartExam_GateInsufficientPool 题库不足(<3)时 StartExam 返回
// ErrExamInsufficientPool(守纯附加层:没学过的课程不该开考)。
func TestStartExam_GateInsufficientPool(t *testing.T) {
	svc, _, _, userID, courseID, _, _ := examServiceTestEnv(t, 2) // 只 2 道题 < minPool=3
	_, err := svc.StartExam(userID, courseID)
	if err != ErrExamInsufficientPool {
		t.Errorf("want ErrExamInsufficientPool; got %v", err)
	}
}

// TestStartExam_GateStatus GetExamStatus 对题库不足的课程返回 unavailable + 提示。
func TestStartExam_GateStatus(t *testing.T) {
	svc, _, _, _, courseID, _, _ := examServiceTestEnv(t, 2)
	st, err := svc.GetExamStatus(courseID)
	if err != nil {
		t.Fatalf("GetExamStatus: %v", err)
	}
	if st.Available {
		t.Error("want unavailable (only 2 questions)")
	}
	if st.Reason == "" {
		t.Error("unavailable should carry a reason")
	}
}

// TestStartExam_AssemblesFromPool 题库够(≥3)时开考成功,返回的卷子题数 ≤ 题库数
// 且 ≤ targetCount(10),题目不带正确答案(防作弊)。
func TestStartExam_AssemblesFromPool(t *testing.T) {
	svc, _, _, userID, courseID, _, _ := examServiceTestEnv(t, 5)
	view, err := svc.StartExam(userID, courseID)
	if err != nil {
		t.Fatalf("StartExam: %v", err)
	}
	if view.ExamID == 0 {
		t.Fatal("ExamID should be set")
	}
	if len(view.Questions) == 0 {
		t.Fatal("should have questions")
	}
	if len(view.Questions) > 5 {
		t.Errorf("questions = %d; want ≤ 5 (pool size)", len(view.Questions))
	}
	// 题目不带正确答案(防作弊)。
	for _, q := range view.Questions {
		if q.CorrectIndex != nil || q.CorrectText != "" {
			t.Errorf("question %d leaked correct answer: %+v", q.ID, q)
		}
	}
}

// TestSubmitExam_GradesAndScores 交卷判分 + 算得分率 + 写 ExamAnswer + 更新 mastery。
// 5 道题全对(选索引 1)→ Score = 1.0。
func TestSubmitExam_GradesAndScores(t *testing.T) {
	svc, _, examRepo, userID, courseID, _, _ := examServiceTestEnv(t, 5)
	view, err := svc.StartExam(userID, courseID)
	if err != nil {
		t.Fatalf("StartExam: %v", err)
	}
	// 全选索引 1(正确)。
	rightIdx := 1
	answers := make([]QuizAnswerInput, 0, len(view.Questions))
	for _, q := range view.Questions {
		answers = append(answers, QuizAnswerInput{QuestionID: q.ID, AnswerIndex: &rightIdx})
	}
	report, err := svc.SubmitExam(userID, view.ExamID, answers)
	if err != nil {
		t.Fatalf("SubmitExam: %v", err)
	}
	if report.Score != 1.0 {
		t.Errorf("Score = %v; want 1.0 (all correct)", report.Score)
	}
	if len(report.Results) != len(view.Questions) {
		t.Errorf("results = %d; want %d", len(report.Results), len(view.Questions))
	}
	// ExamAnswer 写了。
	ans, _ := examRepo.ListExamAnswers(view.ExamID)
	if len(ans) != len(view.Questions) {
		t.Errorf("ExamAnswer rows = %d; want %d", len(ans), len(view.Questions))
	}
	// Score 落库。
	exam, _ := examRepo.GetExamByID(view.ExamID)
	if exam.Score != 1.0 {
		t.Errorf("exam.Score = %v; want 1.0", exam.Score)
	}
	if exam.SubmittedAt == nil {
		t.Error("SubmittedAt should be set after submit")
	}
}

// TestSubmitExam_WrongAnswersAffectScore 部分错 → Score < 1.0,逐题 reveal 正确答案。
func TestSubmitExam_WrongAnswersAffectScore(t *testing.T) {
	svc, _, _, userID, courseID, _, _ := examServiceTestEnv(t, 4)
	view, _ := svc.StartExam(userID, courseID)
	// 前 2 道对(索引1),后 2 道错(索引0)。
	rightIdx, wrongIdx := 1, 0
	answers := []QuizAnswerInput{}
	for i, q := range view.Questions {
		idx := rightIdx
		if i >= 2 {
			idx = wrongIdx
		}
		answers = append(answers, QuizAnswerInput{QuestionID: q.ID, AnswerIndex: &idx})
	}
	report, err := svc.SubmitExam(userID, view.ExamID, answers)
	if err != nil {
		t.Fatalf("SubmitExam: %v", err)
	}
	if report.Score != 0.5 {
		t.Errorf("Score = %v; want 0.5 (2 of 4 correct)", report.Score)
	}
	// 错题 reveal 正确答案(correct_index=1)。
	for i, r := range report.Results {
		if i >= 2 { // 错题
			if !r.Correct {
				if r.CorrectIndex == nil || *r.CorrectIndex != 1 {
					t.Errorf("wrong question %d should reveal correct_index=1; got %v", r.QuestionID, r.CorrectIndex)
				}
			}
		}
	}
}

// TestSubmitExam_AlreadySubmittedLock 已交卷的 exam 再交 → ErrExamAlreadySubmitted。
// 守交卷锁(消除 TOCTOU)。
func TestSubmitExam_AlreadySubmittedLock(t *testing.T) {
	svc, _, _, userID, courseID, _, _ := examServiceTestEnv(t, 3)
	view, _ := svc.StartExam(userID, courseID)
	rightIdx := 1
	answers := []QuizAnswerInput{{QuestionID: view.Questions[0].ID, AnswerIndex: &rightIdx}}
	if _, err := svc.SubmitExam(userID, view.ExamID, answers); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	// 第二次交 → 拒。
	_, err := svc.SubmitExam(userID, view.ExamID, answers)
	if err != ErrExamAlreadySubmitted {
		t.Errorf("second submit err = %v; want ErrExamAlreadySubmitted", err)
	}
}

// TestSubmitExam_OwnerIsolation 别人的 exam 不能交(守用户隔离)。
func TestSubmitExam_OwnerIsolation(t *testing.T) {
	svc, _, _, userID, courseID, _, _ := examServiceTestEnv(t, 3)
	view, _ := svc.StartExam(userID, courseID)
	// 用一个不存在的 user id 交。
	_, err := svc.SubmitExam(userID+999, view.ExamID, nil)
	if err == nil {
		t.Error("non-owner submit should fail; got nil")
	}
}

// TestSubmitExam_MasteryUpdated 交卷后 mastery 更新(对题 +0.1,用 GetCourseMasteries 验证)。
func TestSubmitExam_MasteryUpdated(t *testing.T) {
	svc, contentRepo, _, userID, courseID, _, _ := examServiceTestEnv(t, 3)
	view, _ := svc.StartExam(userID, courseID)
	rightIdx := 1
	answers := []QuizAnswerInput{}
	for _, q := range view.Questions {
		answers = append(answers, QuizAnswerInput{QuestionID: q.ID, AnswerIndex: &rightIdx})
	}
	svc.SubmitExam(userID, view.ExamID, answers)
	// mastery 应有该 chunk 的记录 + mastery > 0(全对至少 +0.1)。
	mem, err := contentRepo.GetCourseMasteries(userID, courseID)
	if err != nil {
		t.Fatalf("GetCourseMasteries: %v", err)
	}
	if len(mem) == 0 {
		t.Fatal("no mastery rows after exam submit (feedback loop broken)")
	}
	for _, m := range mem {
		if m.Mastery <= 0 {
			t.Errorf("mastery for chunk %d = %v; want > 0 (all correct should raise it)", m.ChunkID, m.Mastery)
		}
	}
}
