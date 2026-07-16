package repository

import (
	"testing"
	"time"

	"studyquest/backend/internal/model"
)

// TestCreateQuiz_ArchivesOldQuiz verifies the Phase 3 invariant: regen (a second
// CreateQuiz for the same user+episode) ARCHIVES the prior quiz instead of
// deleting it, and the new quiz is the single active one. Answers + memory
// must be untouched.
func TestCreateQuiz_ArchivesOldQuiz(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)

	const (
		userID    = uint(1)
		episodeID = uint(10)
		courseID  = uint(100)
	)

	// 1st generation: insert an active quiz with 1 question + 1 answer.
	q1 := &model.Quiz{EpisodeID: episodeID, UserID: userID, CourseID: courseID, Difficulty: "adaptive"}
	questions1 := []model.Question{{Type: "choice", Stem: "Q1", Options: "[\"a\",\"b\"]", Answer: 0}}
	id1, err := repo.CreateQuiz(q1, questions1)
	if err != nil {
		t.Fatalf("CreateQuiz #1: %v", err)
	}
	// An answer attached to the first quiz (snapshot QuizID).
	if err := repo.CreateAnswer(&model.Answer{
		QuestionID: questions1[0].ID, QuizID: id1, UserID: userID, UserAnswer: 0, Correct: true, AnsweredAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateAnswer: %v", err)
	}

	// 2nd generation (regen): must archive q1 and create a new active quiz.
	q2 := &model.Quiz{EpisodeID: episodeID, UserID: userID, CourseID: courseID, Difficulty: "adaptive"}
	questions2 := []model.Question{{Type: "fill", Stem: "Q2", AnswerText: "[\"42\"]"}}
	id2, err := repo.CreateQuiz(q2, questions2)
	if err != nil {
		t.Fatalf("CreateQuiz #2: %v", err)
	}
	if id2 == id1 {
		t.Fatalf("new quiz id %d == old quiz id %d (should be a fresh row)", id2, id1)
	}

	// GetQuiz returns ONLY the new active quiz.
	got, err := repo.GetQuiz(userID, episodeID)
	if err != nil {
		t.Fatalf("GetQuiz: %v", err)
	}
	if got == nil {
		t.Fatal("GetQuiz returned nil; want the active quiz")
	}
	if got.ID != id2 {
		t.Errorf("GetQuiz returned quiz %d; want active %d", got.ID, id2)
	}
	if got.Status != "active" {
		t.Errorf("active quiz Status = %q; want \"active\"", got.Status)
	}

	// The old quiz is archived (not deleted): ListArchivedQuizzes returns it.
	archived, err := repo.ListArchivedQuizzes(userID, episodeID)
	if err != nil {
		t.Fatalf("ListArchivedQuizzes: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("ListArchivedQuizzes returned %d rows; want 1 (the archived prior gen)", len(archived))
	}
	if archived[0].ID != id1 {
		t.Errorf("archived quiz id = %d; want %d", archived[0].ID, id1)
	}
	if archived[0].Status != "archived" {
		t.Errorf("archived quiz Status = %q; want \"archived\"", archived[0].Status)
	}
	if archived[0].ArchivedAt == nil {
		t.Error("archived quiz ArchivedAt is nil; want a timestamp")
	}

	// The archived quiz's questions survive (history needs them).
	oldQuestions, err := repo.GetQuestions(id1)
	if err != nil {
		t.Fatalf("GetQuestions(archived): %v", err)
	}
	if len(oldQuestions) != 1 || oldQuestions[0].Stem != "Q1" {
		t.Errorf("archived quiz questions = %+v; want the original Q1", oldQuestions)
	}

	// The answer survives: scoped by the archived quiz's id, still 1 row.
	answers, err := repo.ListAnswersForQuiz(id1, userID)
	if err != nil {
		t.Fatalf("ListAnswersForQuiz(archived): %v", err)
	}
	if len(answers) != 1 {
		t.Errorf("answers for archived quiz = %d; want 1 (answers must survive regen)", len(answers))
	}
}

// TestCreateQuiz_ActiveUniqueInvariant exercises the partial unique index: after
// two regens there must be exactly one active quiz and two archived ones. If the
// archive step ever fails to flip status first, the partial unique index would
// reject the insert and CreateQuiz would error.
func TestCreateQuiz_ActiveUniqueInvariant(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)
	const userID, episodeID, courseID = uint(2), uint(20), uint(200)

	// Three generations in a row.
	for i := 0; i < 3; i++ {
		q := &model.Quiz{EpisodeID: episodeID, UserID: userID, CourseID: courseID, Difficulty: "adaptive"}
		_, err := repo.CreateQuiz(q, []model.Question{{Type: "choice", Stem: "stem", Options: "[\"x\"]", Answer: 0}})
		if err != nil {
			t.Fatalf("CreateQuiz #%d: %v", i, err)
		}
	}

	// Exactly one active.
	active, err := repo.GetQuiz(userID, episodeID)
	if err != nil {
		t.Fatalf("GetQuiz: %v", err)
	}
	if active == nil {
		t.Fatal("expected exactly one active quiz; got nil")
	}

	// Two archived, newest-archive first (ArchivedAt DESC).
	archived, err := repo.ListArchivedQuizzes(userID, episodeID)
	if err != nil {
		t.Fatalf("ListArchivedQuizzes: %v", err)
	}
	if len(archived) != 2 {
		t.Fatalf("expected 2 archived quizzes; got %d", len(archived))
	}
	if archived[0].ArchivedAt == nil || archived[1].ArchivedAt == nil {
		t.Fatal("archived quizzes must have ArchivedAt set")
	}
	if archived[0].ArchivedAt.Before(*archived[1].ArchivedAt) {
		t.Error("ListArchivedQuizzes not newest-first: archived[0].ArchivedAt < archived[1].ArchivedAt")
	}
}

// TestListArchivedQuizzes_Empty confirms an episode with no history returns an
// empty (not nil) slice, so the client can render "no history yet" cleanly.
func TestListArchivedQuizzes_Empty(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)
	got, err := repo.ListArchivedQuizzes(uint(1), uint(1))
	if err != nil {
		t.Fatalf("ListArchivedQuizzes on empty: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 archived; got %d", len(got))
	}
}

// TestGetQuiz_FiltersArchived confirms GetQuiz returns only the active quiz,
// even when archived rows for the same (user, episode) exist. This guards the
// lazy-generation trigger: if GetQuiz ever returned an archived quiz, the
// client would render a stale, un-answerable set.
func TestGetQuiz_FiltersArchived(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)
	const userID, episodeID, courseID = uint(3), uint(30), uint(300)

	// Create + immediately archive a quiz manually (simulate a history-only
	// state, e.g. a failed regen left only an archived row).
	_, err := repo.CreateQuiz(&model.Quiz{EpisodeID: episodeID, UserID: userID, CourseID: courseID}, []model.Question{{Type: "choice", Stem: "s", Options: "[\"a\"]", Answer: 0}})
	if err != nil {
		t.Fatalf("CreateQuiz: %v", err)
	}
	// Flip it to archived directly so there's NO active quiz.
	if err := db.Model(&model.Quiz{}).Where("user_id = ? AND episode_id = ?", userID, episodeID).
		Updates(map[string]interface{}{"status": "archived", "archived_at": time.Now()}).Error; err != nil {
		t.Fatalf("archive quiz: %v", err)
	}

	got, err := repo.GetQuiz(userID, episodeID)
	if err != nil {
		t.Fatalf("GetQuiz: %v", err)
	}
	if got != nil {
		t.Errorf("GetQuiz returned %v; want nil (no active quiz should be visible)", got)
	}
}
