package service

import (
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// aiServiceQuizTestEnv wires a real aiService (with in-memory DB, real repos, no
// providers) for the unified-submit flow tests. The providers/unlockService are
// nil because SubmitAllQuizAnswers doesn't touch them — it only reads/writes
// quiz/question/answer/memory rows via the content repo.
func aiServiceQuizTestEnv(t *testing.T) (*aiService, repository.AIContentRepository) {
	t.Helper()
	db := setupTestDB(t)
	contentRepo := repository.NewAIContentRepository(db)
	svc := NewAIService(
		db,
		contentRepo,
		repository.NewEpisodeRepository(db),
		repository.NewCourseRepository(db),
		nil, // no provider resolver — submit path doesn't need it
		nil, // no unlock service — not exercised here
	).(*aiService)
	return svc, contentRepo
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
	svc, repo := aiServiceQuizTestEnv(t)
	const userID, episodeID, courseID = uint(1), uint(10), uint(100)
	quiz, questions := seedQuizWithQuestions(t, repo, userID, episodeID, courseID, []model.Question{
		{Type: "choice", Stem: "right when idx matches", Options: `["a","b","c","d"]`, Answer: 1},
		{Type: "choice", Stem: "wrong when idx differs", Options: `["a","b","c","d"]`, Answer: 2},
		{Type: "fill", Stem: "skip me", AnswerText: `["42"]`},
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

	// SubmittedAt stamped → second submit must be rejected with ErrQuizAlreadySubmitted.
	refreshed, err := repo.GetQuiz(userID, episodeID)
	if err != nil || refreshed == nil {
		t.Fatalf("GetQuiz after submit: %v %v", refreshed, err)
	}
	if refreshed.SubmittedAt == nil {
		t.Fatal("SubmittedAt is nil after submit; want a timestamp (quiz should be locked)")
	}
	if _, err := svc.SubmitAllQuizAnswers(userID, episodeID, []QuizAnswerInput{
		{QuestionID: rightQ.ID, AnswerIndex: &rightIdx},
	}); err != ErrQuizAlreadySubmitted {
		t.Errorf("second submit err = %v; want ErrQuizAlreadySubmitted", err)
	}
}

// TestGetQuizForClient_ReportsSubmitted confirms the Submitted flag flips on the
// client view once the quiz is handed in — the frontend reads this to switch
// from the editable answering state to read-only results.
func TestGetQuizForClient_ReportsSubmitted(t *testing.T) {
	svc, repo := aiServiceQuizTestEnv(t)
	const userID, episodeID, courseID = uint(2), uint(20), uint(200)
	seedQuizWithQuestions(t, repo, userID, episodeID, courseID, []model.Question{
		{Type: "choice", Stem: "Q", Options: `["a","b"]`, Answer: 0, HasJump: true},
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
	view, err = svc.GetQuizForClient(userID, episodeID)
	if err != nil {
		t.Fatalf("GetQuizForClient after submit: %v", err)
	}
	if !view.Submitted {
		t.Error("Submitted = false after submit; want true (frontend uses this to lock)")
	}
}
