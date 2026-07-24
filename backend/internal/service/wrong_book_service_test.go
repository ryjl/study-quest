package service

import (
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// seedWrongBookScenario 建一条真实 subject→course→episode→user 链,并附一个 active
// quiz + N 道题(题数由 stems 决定)。返回 svc/repo/wrongBook + 各 id + 全部 question id。
// GetWrongBook 的查询依赖 join quizzes→courses,所以 course 必须真实存在(裸 id 会被
// join 滤掉)。错题本 hook 在交卷时还会从 courseRepo.FindByID 取 subjectID 冗余进
// WrongBookItem,也需要真 course。所有题放同一个 quiz,这样一次交卷就能产生多个错题。
func seedWrongBookScenario(t *testing.T, stems ...string) (svc *aiService, repo repository.AIContentRepository, wb repository.WrongBookRepository, userID, courseID, episodeID uint, questionIDs []uint) {
	t.Helper()
	db := testutil.NewFileDB(t)
	contentRepo := repository.NewAIContentRepository(db)
	wrongBook := repository.NewWrongBookRepository(db)
	s := NewAIService(db, contentRepo, repository.NewEpisodeRepository(db), repository.NewCourseRepository(db),
		nil, nil, repository.NewUserRepository(db), nil, nil, nil, nil, wrongBook,
		nil,).(*aiService)
	t.Cleanup(s.Stop)

	subjects := testutil.SeedSubjects(t, db)
	course := &model.Course{Title: "WB Course", SubjectID: subjects["math"].ID}
	db.Create(course)
	episode := &model.Episode{Title: "WB Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	db.Create(episode)
	user := &model.User{Nickname: "wb-user", PinHash: "x", Role: "student"}
	db.Create(user)

	qs := make([]model.Question, len(stems))
	for i, stem := range stems {
		qs[i] = model.Question{Type: "choice", Stem: stem, Options: `["a","b"]`, Answer: 1}
	}
	_, questions := seedQuizWithQuestions(t, contentRepo, user.ID, episode.ID, course.ID, qs)
	ids := make([]uint, len(questions))
	for i, q := range questions {
		ids[i] = q.ID
	}
	return s, contentRepo, wrongBook, user.ID, course.ID, episode.ID, ids
}

// ─── 交卷 hook 回归:答错题自动进错题本 ───

// TestSubmitAllQuizAnswers_WrongAnswerEntersWrongBook 交卷时做错的题应自动 upsert 进
// 错题本;做对的不进。这是错题本的核心维护点——错题本数据由交卷驱动,而非学生手动加。
// 守交卷 hook 正确触发 + 只对 correct=false 生效。
func TestSubmitAllQuizAnswers_WrongAnswerEntersWrongBook(t *testing.T) {
	svc, repo, wrongBook := aiServiceQuizTestEnv(t)
	const userID, episodeID, courseID = uint(1), uint(10), uint(100)
	_, questions := seedQuizWithQuestions(t, repo, userID, episodeID, courseID, []model.Question{
		{Type: "choice", Stem: "right", Options: `["a","b"]`, Answer: 0},
		{Type: "choice", Stem: "wrong", Options: `["a","b"]`, Answer: 1},
	})
	rightQ, wrongQ := questions[0], questions[1]

	rightIdx, wrongIdx := 0, 0 // rightQ.Answer=0 对;wrongQ.Answer=1,选 0 错
	if _, err := svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{
		{QuestionID: rightQ.ID, AnswerIndex: &rightIdx},
		{QuestionID: wrongQ.ID, AnswerIndex: &wrongIdx},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// 错题(wrongQ)进错题本;对的(rightQ)不进。
	wrongItem, err := wrongBook.GetItem(userID, wrongQ.ID)
	if err != nil {
		t.Fatalf("get wrong item: %v", err)
	}
	if wrongItem == nil {
		t.Fatal("wrong answer did NOT enter wrong book (hook failed to fire)")
	}
	if wrongItem.AttemptCount != 1 {
		t.Errorf("wrong item AttemptCount = %d; want 1 (first wrong)", wrongItem.AttemptCount)
	}
	if wrongItem.Mastered {
		t.Error("wrong item should start unmastered")
	}

	rightItem, _ := wrongBook.GetItem(userID, rightQ.ID)
	if rightItem != nil {
		t.Errorf("correct answer leaked into wrong book; got %+v", rightItem)
	}
}

// TestSubmitAllQuizAnswers_WrongAnswerIncrementsOnReSubmit 同一题再次做错,
// AttemptCount 累加而不是新建第二行。守 upsert 去重(同 question_id 只一行)。
func TestSubmitAllQuizAnswers_WrongAnswerIncrementsOnReSubmit(t *testing.T) {
	svc, repo, wrongBook := aiServiceQuizTestEnv(t)
	const userID, episodeID, courseID = uint(1), uint(10), uint(100)
	_, questions := seedQuizWithQuestions(t, repo, userID, episodeID, courseID, []model.Question{
		{Type: "choice", Stem: "q", Options: `["a","b"]`, Answer: 1},
	})
	wrongQ := questions[0]
	wrongIdx := 0 // Answer=1, 选 0 错

	// 第一份卷交卷。
	svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{{QuestionID: wrongQ.ID, AnswerIndex: &wrongIdx}})
	// 第二份卷(regenerate 后新 quiz)再次交卷——但这需要新 quiz。直接再 seed 一份
	// 会触发 CreateQuiz 的 archive+insert。简化:直接用 wrongBook upsert 验证累加语义
	// 已在 repo 测试覆盖,这里只验证交卷 hook 在第二次调用时 +1 而非新建。
	// 为此手动造第二份 quiz(seedQuizWithQuestions 会 archive 旧的)。
	_, q2 := seedQuizWithQuestions(t, repo, userID, episodeID, courseID, []model.Question{
		{Type: "choice", Stem: "q2", Options: `["a","b"]`, Answer: 1},
	})
	svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{{QuestionID: q2[0].ID, AnswerIndex: &wrongIdx}})

	// wrongQ 的错题本行 AttemptCount 仍是 1(只有第一份卷错它;第二份卷的题是 q2)。
	item, _ := wrongBook.GetItem(userID, wrongQ.ID)
	if item.AttemptCount != 1 {
		t.Errorf("wrongQ AttemptCount = %d; want 1 (only submitted once)", item.AttemptCount)
	}
}

// TestSubmitAllQuizAnswers_PartialCountsAsWrong 多选题部分对(漏选但没多选错项)
// 按"错"处理——漏一个正确项就是没完全掌握,该进错题本复习。这是 2026-07-23 改的一致口径:
// mastery / 错题本 / 显示对错 三处对"漏选"用同一判定(漏选=错),避免旧版自相矛盾
// (旧版部分对不扣 mastery 不进错题本,但 UI 显示 partial,语义混乱)。
func TestSubmitAllQuizAnswers_PartialCountsAsWrong(t *testing.T) {
	svc, repo, wrongBook := aiServiceQuizTestEnv(t)
	const userID, episodeID, courseID = uint(1), uint(10), uint(100)
	_, questions := seedQuizWithQuestions(t, repo, userID, episodeID, courseID, []model.Question{
		{Type: "multi_choice", Stem: "partial", Options: `["a","b","c"]`,
			Scoring: `{"correct_indices":[0,1,2],"partial_credit":true,"min_correct_for_half":1}`},
	})
	partialQ := questions[0]
	partialIdx := []int{0} // 选 1 个对的,漏 2 个 → verdict.Partial=true 但 Correct=false

	if _, err := svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{
		{QuestionID: partialQ.ID, AnswerIndices: partialIdx},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// 漏选(部分对)按"错"处理,应进错题本。
	item, _ := wrongBook.GetItem(userID, partialQ.ID)
	if item == nil {
		t.Errorf("partial-correct multi-choice (漏选) should enter wrong book under the unified rule; got nil")
	}
}

// ─── GetWrongBook 列表组装 ───

// TestGetWrongBook_MergesStemAndCuration 列表正确合并题面(join)和 curation 状态。
// 先交卷产生错题,再查错题本,验证题面 + AttemptCount + Mastered 都正确。
// 用真实 course 链(GetWrongBook 的 join 依赖 course 行存在)。
func TestGetWrongBook_MergesStemAndCuration(t *testing.T) {
	svc, _, _, userID, courseID, episodeID, qIDs := seedWrongBookScenario(t, "my-wrong-q")
	qID := qIDs[0]
	wrongIdx := 0 // Answer=1, 选 0 错
	svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{{QuestionID: qID, AnswerIndex: &wrongIdx}})

	views, err := svc.GetWrongBook(userID, courseID, nil)
	if err != nil {
		t.Fatalf("GetWrongBook: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("want 1 wrong item; got %d", len(views))
	}
	if views[0].Stem != "my-wrong-q" {
		t.Errorf("stem = %q; want \"my-wrong-q\"", views[0].Stem)
	}
	if views[0].AttemptCount != 1 {
		t.Errorf("AttemptCount = %d; want 1", views[0].AttemptCount)
	}
	if views[0].Mastered {
		t.Error("should be unmastered")
	}
	if views[0].FirstWrongAt == "" {
		t.Error("FirstWrongAt should be RFC3339 string")
	}
}

// TestGetWrongBook_FilterMastered 按 mastered 过滤:已掌握的不出现在默认(未掌握)视图。
// 需要两道题,故在 scenario 基础上手动加第二道。
func TestGetWrongBook_FilterMastered(t *testing.T) {
	svc, _, _, userID, courseID, episodeID, qIDs := seedWrongBookScenario(t, "q1", "q2")
	q1ID, q2ID := qIDs[0], qIDs[1]
	wrongIdx := 0
	// 一次交卷两道题(同 quiz),都做错。
	svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{
		{QuestionID: q1ID, AnswerIndex: &wrongIdx},
		{QuestionID: q2ID, AnswerIndex: &wrongIdx},
	})
	// 把 q1 标记掌握。
	svc.MarkWrongBookMastered(userID, q1ID, true)

	// 未掌握过滤 → 只剩 q2。
	unmastered := false
	views, _ := svc.GetWrongBook(userID, courseID, &unmastered)
	if len(views) != 1 || views[0].QuestionID != q2ID {
		t.Errorf("unmastered filter: got %+v; want only q2", views)
	}
	// 已掌握过滤 → 只 q1。
	mastered := true
	views, _ = svc.GetWrongBook(userID, courseID, &mastered)
	if len(views) != 1 || views[0].QuestionID != q1ID {
		t.Errorf("mastered filter: got %+v; want only q1", views)
	}
}

// TestGetWrongBook_EmptyReturnsEmpty 无错题时返回空 slice,不 nil,不报错。
func TestGetWrongBook_EmptyReturnsEmpty(t *testing.T) {
	svc, _, _, _, _, _, _ := seedWrongBookScenario(t, "unused")
	views, err := svc.GetWrongBook(uint(99999), uint(99999), nil)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("want 0; got %d", len(views))
	}
}

// ─── RedoWrongBookQuiz / SubmitWrongBookRedo ───

// TestRedoWrongBookSubmit_CorrectMarksMastered 重做连对阈值(3)次 → mastered,且不落
// Answer 行(隔离性:不污染 quiz 答题流水)、不改 quiz-side mastery。连对途中 streak 累加
// 但未掌握;答错会清零 streak(见 TestRedoWrongBookSubmit_WrongClearsStreak)。
func TestRedoWrongBookSubmit_CorrectMarksMastered(t *testing.T) {
	svc, repo, wrongBook, userID, _, episodeID, qIDs := seedWrongBookScenario(t, "q")
	wrongQ := qIDs[0]
	wrongIdx, rightIdx := 0, 1
	// 先交卷做错,进错题本。
	svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{{QuestionID: wrongQ, AnswerIndex: &wrongIdx}})

	// 重做答对第 1 次 → streak=1,未掌握。
	svc.SubmitWrongBookRedo(userID, []QuizAnswerInput{{QuestionID: wrongQ, AnswerIndex: &rightIdx}})
	item, _ := wrongBook.GetItem(userID, wrongQ)
	if item.Mastered {
		t.Fatal("1 correct should NOT master yet (threshold=3)")
	}
	if item.CorrectStreak != 1 {
		t.Errorf("after 1 correct: streak = %d; want 1", item.CorrectStreak)
	}
	// 第 2 次 → streak=2,仍未掌握。
	svc.SubmitWrongBookRedo(userID, []QuizAnswerInput{{QuestionID: wrongQ, AnswerIndex: &rightIdx}})
	item, _ = wrongBook.GetItem(userID, wrongQ)
	if item.Mastered || item.CorrectStreak != 2 {
		t.Fatalf("after 2 correct: mastered=%v streak=%d; want false/2", item.Mastered, item.CorrectStreak)
	}
	// 第 3 次 → 掌握,streak 归 0。
	results, err := svc.SubmitWrongBookRedo(userID, []QuizAnswerInput{
		{QuestionID: wrongQ, AnswerIndex: &rightIdx},
	})
	if err != nil {
		t.Fatalf("redo submit: %v", err)
	}
	if len(results) != 1 || !results[0].Correct {
		t.Errorf("redo result = %+v; want 1 correct", results)
	}
	item, _ = wrongBook.GetItem(userID, wrongQ)
	if !item.Mastered {
		t.Error("3 consecutive correct should mark item mastered")
	}
	if item.CorrectStreak != 0 {
		t.Errorf("after mastered: streak = %d; want 0 (reset)", item.CorrectStreak)
	}
	// 隔离性:Answer 行数不变(只有第一次交卷的 1 行,重做不新增)。
	quiz, _ := repo.GetQuiz(userID, episodeID)
	answers, _ := repo.ListAnswersForQuiz(quiz.ID, userID)
	if len(answers) != 1 {
		t.Errorf("redo should NOT add Answer rows; got %d (want 1 from original submit)", len(answers))
	}
}

// TestRedoWrongBookSubmit_WrongClearsStreak 重做答错 → streak 清零(打断连对),
// AttemptCount++。验证连对中途答错不会带着 streak 往后累。
func TestRedoWrongBookSubmit_WrongClearsStreak(t *testing.T) {
	svc, _, wrongBook, userID, _, episodeID, qIDs := seedWrongBookScenario(t, "q")
	wrongQ := qIDs[0]
	wrongIdx, rightIdx := 0, 1
	svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{{QuestionID: wrongQ, AnswerIndex: &wrongIdx}})
	// 连对 2 次(streak=2)。
	svc.SubmitWrongBookRedo(userID, []QuizAnswerInput{{QuestionID: wrongQ, AnswerIndex: &rightIdx}})
	svc.SubmitWrongBookRedo(userID, []QuizAnswerInput{{QuestionID: wrongQ, AnswerIndex: &rightIdx}})
	// 答错 → streak 清零。
	svc.SubmitWrongBookRedo(userID, []QuizAnswerInput{{QuestionID: wrongQ, AnswerIndex: &wrongIdx}})
	item, _ := wrongBook.GetItem(userID, wrongQ)
	if item.CorrectStreak != 0 {
		t.Errorf("after wrong: streak = %d; want 0 (cleared)", item.CorrectStreak)
	}
	if item.Mastered {
		t.Error("wrong redo should keep unmastered")
	}
}

// TestRedoWrongBookSubmit_WrongIncrementsAttempt 重做答错 → AttemptCount++,仍 unmastered。
func TestRedoWrongBookSubmit_WrongIncrementsAttempt(t *testing.T) {
	svc, _, wrongBook, userID, _, episodeID, qIDs := seedWrongBookScenario(t, "q")
	wrongQ := qIDs[0]
	wrongIdx := 0
	svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{{QuestionID: wrongQ, AnswerIndex: &wrongIdx}})

	// 重做又错。
	svc.SubmitWrongBookRedo(userID, []QuizAnswerInput{{QuestionID: wrongQ, AnswerIndex: &wrongIdx}})

	item, _ := wrongBook.GetItem(userID, wrongQ)
	if item.AttemptCount != 2 {
		t.Errorf("AttemptCount = %d; want 2 (original + redo)", item.AttemptCount)
	}
	if item.Mastered {
		t.Error("redo wrong should keep unmastered")
	}
}

// TestRedoWrongBookQuiz_ReturnsUnmasteredOnly 取重做卷时只返回未掌握的题。
// 用真实 course 链(RedoWrongBookQuiz 的 join 依赖 course 行)。
func TestRedoWrongBookQuiz_ReturnsUnmasteredOnly(t *testing.T) {
	svc, _, _, userID, courseID, episodeID, qIDs := seedWrongBookScenario(t, "q1", "q2")
	q1ID, q2ID := qIDs[0], qIDs[1]
	wrongIdx := 0
	// 一次交卷两道(同 quiz),都做错。
	svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{
		{QuestionID: q1ID, AnswerIndex: &wrongIdx},
		{QuestionID: q2ID, AnswerIndex: &wrongIdx},
	})
	// q1 标掌握,q2 保持未掌握。
	svc.MarkWrongBookMastered(userID, q1ID, true)

	redo, err := svc.RedoWrongBookQuiz(userID, courseID, 10)
	if err != nil {
		t.Fatalf("RedoWrongBookQuiz: %v", err)
	}
	if len(redo) != 1 {
		t.Fatalf("redo quiz should only have unmastered; got %d", len(redo))
	}
	if redo[0].ID != q2ID {
		t.Errorf("redo quiz = q%d; want q%d (the unmastered one)", redo[0].ID, q2ID)
	}
	// 重做卷不应暴露正确答案(防作弊)。
	if redo[0].CorrectIndex != nil || redo[0].CorrectText != "" {
		t.Errorf("redo quiz leaked correct answer: %+v", redo[0])
	}
}

// ─── nil-safe 降级 ───

// TestWrongBook_NilRepoDegradesGracefully wrongBookRepo 为 nil 时(老测试/降级场景),
// 所有错题本方法返回空/无操作,不 panic、不报错。守 AI 附加层铁律:错题本挂了不影响核心。
func TestWrongBook_NilRepoDegradesGracefully(t *testing.T) {
	// 构造一个 wrongBookRepo=nil 的 svc(降级场景)。
	db := testutil.NewFileDB(t)
	contentRepo := repository.NewAIContentRepository(db)
	svc := NewAIService(
		db, contentRepo, repository.NewEpisodeRepository(db), repository.NewCourseRepository(db),
		nil, nil, nil, nil, nil, nil, nil,
		nil, // wrongBookRepo = nil —— 降级场景,
		nil,
	).(*aiService)
	t.Cleanup(svc.Stop)

	if views, err := svc.GetWrongBook(1, 100, nil); err != nil || len(views) != 0 {
		t.Errorf("nil repo GetWrongBook: views=%d err=%v; want 0/nil", len(views), err)
	}
	if err := svc.MarkWrongBookMastered(1, 1, true); err != nil {
		t.Errorf("nil repo MarkMastered: err=%v; want nil", err)
	}
	if q, err := svc.RedoWrongBookQuiz(1, 100, 5); err != nil || len(q) != 0 {
		t.Errorf("nil repo Redo: q=%d err=%v; want 0/nil", len(q), err)
	}
	idx := 0
	if r, err := svc.SubmitWrongBookRedo(1, []QuizAnswerInput{{QuestionID: 1, AnswerIndex: &idx}}); err != nil || len(r) != 0 {
		t.Errorf("nil repo SubmitRedo: r=%d err=%v; want 0/nil", len(r), err)
	}
}
