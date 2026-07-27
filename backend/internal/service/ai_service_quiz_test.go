package service

import (
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// aiServiceQuizTestEnv wires a real aiService (file-backed DB, real repos, no
// providers) for the unified-submit flow tests. See aiServiceTestEnv for why
// file-backed: the runWorker goroutine needs a cross-connection-shared DB.
// Returns wrongBookRepo too so the 错题本 hook regression tests can assert on it.
func aiServiceQuizTestEnv(t *testing.T) (*aiService, repository.AIContentRepository, repository.WrongBookRepository) {
	t.Helper()
	db := testutil.NewFileDB(t)
	contentRepo := repository.NewAIContentRepository(db)
	wrongBookRepo := repository.NewWrongBookRepository(db)
	svc := NewAIService(
		db,
		contentRepo,
		repository.NewEpisodeRepository(db),
		repository.NewCourseRepository(db),
		nil,                  // no provider resolver — submit path doesn't need it
		nil,                  // no unlock service — not exercised here
		repository.NewUserRepository(db), // advice tools need userRepo, but submit path won't call it
		nil,                  // no glossary repo — quiz path doesn't touch it
		nil,                  // no subject repo — polish-only
		nil,                  // no polishChunkRepo — polish-only
		nil,                  // no logRepo — structured-log writes not asserted,
		wrongBookRepo,        // 错题本 hook — real repo so submit regression tests can assert,
		nil,
		nil, // no homeworkRepo — quiz path doesn't touch homework
	).(*aiService)
	t.Cleanup(svc.Stop) // release the worker goroutine (see ai_service_test.go)
	return svc, contentRepo, wrongBookRepo
}


// seedQuizWithQuestions inserts one active quiz with the given questions and
// returns the quiz + the created question rows. Mirrors the shape the quizzer
// job would persist.
func seedQuizWithQuestions(t *testing.T, repo repository.AIContentRepository, userID, episodeID, courseID uint, qs []model.Question) (*model.Quiz, []model.Question) {
	t.Helper()
	quiz := &model.Quiz{EpisodeID: episodeID, UserID: userID, CourseID: courseID, Difficulty: "adaptive"}
	id, err := repo.CreateQuiz(quiz, qs)
	if err != nil {
		t.Fatalf("CreateQuiz: %v", err)
	}
	got, err := repo.GetQuiz(userID, episodeID)
	if err != nil || got == nil {
		t.Fatalf("GetQuiz after seed: %v %v", got, err)
	}
	questions, err := repo.GetQuestions(id)
	if err != nil {
		t.Fatalf("GetQuestions: %v", err)
	}
	return got, questions
}

// TestSubmitAllQuizAnswers_GradesPersistsLocks exercises the unified submit:
// a correct choice + a wrong choice + a skipped question. Verifies it (a)
// returns a result per question in quiz order, (b) reveals the correct answer
// for every question post-submit, (c) writes answer rows only for answered
// questions, (d) stamps SubmittedAt so a second submit is rejected.
func TestSubmitAllQuizAnswers_GradesPersistsLocks(t *testing.T) {
	svc, repo, _ := aiServiceQuizTestEnv(t)
	const userID, episodeID, courseID = uint(1), uint(10), uint(100)
	quiz, questions := seedQuizWithQuestions(t, repo, userID, episodeID, courseID, []model.Question{
		{Type: "choice", Stem: "right when idx matches", Options: `["a","b","c","d"]`, Scoring: `{"correct_index":1}`},
		{Type: "choice", Stem: "wrong when idx differs", Options: `["a","b","c","d"]`, Scoring: `{"correct_index":2}`},
		{Type: "fill", Stem: "skip me", Scoring: `{"accept":["42"]}`},
	})
	rightQ := questions[0]
	wrongQ := questions[1]
	_ = questions[2] // skippedQ intentionally omitted from submit (asserted via result[2])

	rightIdx := 1 // matches Answer
	wrongIdx := 0 // Answer is 2, so 0 is wrong
	results, err := svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{
		{QuestionID: rightQ.ID, AnswerIndex: &rightIdx},
		{QuestionID: wrongQ.ID, AnswerIndex: &wrongIdx},
		// skippedQ intentionally omitted — counts as unanswered/wrong.
	})
	if err != nil {
		t.Fatalf("SubmitAllQuizAnswers: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results; want 3 (one per question in quiz order)", len(results))
	}
	// Result order follows question order, not input order.
	if results[0].Correct != true || results[1].Correct != false || results[2].Correct != false {
		t.Errorf("correctness = [%v,%v,%v]; want [true,false,false] (right/wrong/skipped)",
			results[0].Correct, results[1].Correct, results[2].Correct)
	}
	// Correct answer revealed for every question post-submit (阅卷后全揭示).
	if results[0].CorrectIndex == nil || *results[0].CorrectIndex != 1 {
		t.Errorf("results[0] correct_index = %v; want 1", results[0].CorrectIndex)
	}
	if results[2].CorrectText != "42" {
		t.Errorf("skipped fill question correct_text = %q; want \"42\"", results[2].CorrectText)
	}

	// Answer rows: only for the two answered questions (skipped leaves no row).
	answers, err := repo.ListAnswersForQuiz(quiz.ID, userID)
	if err != nil {
		t.Fatalf("ListAnswersForQuiz: %v", err)
	}
	if len(answers) != 2 {
		t.Errorf("answer rows = %d; want 2 (skipped question has no answer)", len(answers))
	}

	// 交卷即归档:quiz 翻成 archived。验证它在历史里 + SubmittedAt 已盖戳(交卷锁),
	// 且再次 submit 被拒为 ErrQuizAlreadySubmitted(无 active quiz 但有历史 → 409)。
	archived, err := repo.ListArchivedQuizzes(userID, episodeID)
	if err != nil {
		t.Fatalf("ListArchivedQuizzes after submit: %v", err)
	}
	if len(archived) != 1 || archived[0].ID != quiz.ID {
		t.Fatalf("archived = %+v; want the just-submitted quiz %d", archived, quiz.ID)
	}
	if archived[0].SubmittedAt == nil {
		t.Fatal("SubmittedAt is nil after submit; want a timestamp (quiz should be locked)")
	}
	if _, err := svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{
		{QuestionID: rightQ.ID, AnswerIndex: &rightIdx},
	}); err != ErrQuizAlreadySubmitted {
		t.Errorf("second submit err = %v; want ErrQuizAlreadySubmitted", err)
	}
}

// TestGetQuizForClient_ReportsSubmitted confirms the Submitted flag is false on
// the client view before submit, and that HasJump passes through. After submit
// the quiz is archived (交卷即归档), so GetQuizForClient returns nil — the
// post-submit review is served via ListQuizHistory instead (covered by
// TestSubmitAllQuizAnswers_ArchivesQuiz).
func TestGetQuizForClient_ReportsSubmitted(t *testing.T) {
	svc, repo, _ := aiServiceQuizTestEnv(t)
	const userID, episodeID, courseID = uint(2), uint(20), uint(200)
	seedQuizWithQuestions(t, repo, userID, episodeID, courseID, []model.Question{
		{Type: "choice", Stem: "Q", Options: `["a","b"]`, Scoring: `{"correct_index":0}`, HasJump: true},
	})

	view, err := svc.GetQuizForClient(userID, episodeID)
	if err != nil {
		t.Fatalf("GetQuizForClient before submit: %v", err)
	}
	if view.Submitted {
		t.Error("Submitted = true before any submit; want false")
	}
	if len(view.Questions) != 1 || !view.Questions[0].HasJump {
		t.Errorf("HasJump not passed through: %+v", view.Questions)
	}

	if _, err := svc.SubmitAllQuizAnswers(userID, episodeID, nil); err != nil {
		t.Fatalf("SubmitAllQuizAnswers (all-skipped): %v", err)
	}
	// 交卷即归档:quiz 已 archived,GetQuiz(只查 active)返回 nil → 客户端只读复习视图
	// 也跟着 nil。复习视图改由历史面板(ListQuizHistory)承载。
	view, err = svc.GetQuizForClient(userID, episodeID)
	if err != nil {
		t.Fatalf("GetQuizForClient after submit: %v", err)
	}
	if view != nil {
		t.Errorf("GetQuizForClient after submit = %+v; want nil (quiz archived → review via history)", view)
	}
}

// TestSubmitAllQuizAnswers_ArchivesQuiz verifies the 交卷即归档 contract: after a
// successful submit-all, the quiz is flipped to archived (no longer active) and
// shows up in ListQuizHistory for read-only review. archived_at equals the
// submit timestamp so newest-first ordering puts it on top.
func TestSubmitAllQuizAnswers_ArchivesQuiz(t *testing.T) {
	svc, repo, _ := aiServiceQuizTestEnv(t)
	const userID, episodeID, courseID = uint(3), uint(30), uint(300)
	seedQuizWithQuestions(t, repo, userID, episodeID, courseID, []model.Question{
		{Type: "choice", Stem: "Q", Options: `["a","b"]`, Scoring: `{"correct_index":0}`},
	})

	if _, err := svc.SubmitAllQuizAnswers(userID, episodeID, nil); err != nil {
		t.Fatalf("SubmitAllQuizAnswers: %v", err)
	}

	// No longer active — GetQuiz returns nil.
	if got, err := repo.GetQuiz(userID, episodeID); err != nil || got != nil {
		t.Errorf("GetQuiz after submit = %v %v; want nil (archived)", got, err)
	}
	// Shows up in history, fully revealed.
	history, err := svc.ListQuizHistory(userID, episodeID)
	if err != nil {
		t.Fatalf("ListQuizHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len = %d; want 1 (just-submitted quiz)", len(history))
	}
	if history[0].QuestionCount != 1 {
		t.Errorf("history[0].QuestionCount = %d; want 1", history[0].QuestionCount)
	}
	// A re-submit of the same (user, episode) is rejected as already-submitted
	// (there's no active quiz, but history exists → ErrQuizAlreadySubmitted → 409).
	if _, err := svc.SubmitAllQuizAnswers(userID, episodeID, nil); err != ErrQuizAlreadySubmitted {
		t.Errorf("re-submit err = %v; want ErrQuizAlreadySubmitted", err)
	}
}

// TestGetOrEnqueueQuiz_DoneAfterSubmit verifies the 不自动出新题 contract: after a
// quiz has been handed in (archived), GetOrEnqueueQuiz returns "done" instead of
// enqueuing a fresh generation — the student must tap 重新生成 to start a new set.
// Only the very first visit (no quiz rows at all) auto-enqueues.
func TestGetOrEnqueueQuiz_DoneAfterSubmit(t *testing.T) {
	svc, repo, _ := aiServiceQuizTestEnv(t)
	const userID, episodeID, courseID = uint(4), uint(40), uint(400)
	seedQuizWithQuestions(t, repo, userID, episodeID, courseID, []model.Question{
		{Type: "choice", Stem: "Q", Options: `["a","b"]`, Scoring: `{"correct_index":0}`},
	})

	if _, err := svc.SubmitAllQuizAnswers(userID, episodeID, nil); err != nil {
		t.Fatalf("SubmitAllQuizAnswers: %v", err)
	}

	status, quiz, err := svc.GetOrEnqueueQuiz(userID, episodeID)
	if err != nil {
		t.Fatalf("GetOrEnqueueQuiz after submit: %v", err)
	}
	if status != "done" {
		t.Errorf("status = %q; want \"done\" (no auto-regen after submit)", status)
	}
	if quiz != nil {
		t.Errorf("quiz = %+v; want nil (done returns no active quiz)", quiz)
	}
}
