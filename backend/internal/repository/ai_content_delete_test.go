package repository

import (
	"testing"

	"studyquest/backend/internal/model"
	"gorm.io/gorm"
)

// setupDeleteTestDB 和 cleanup_orphans_test 共用同一套 DB 准备逻辑(in-memory + FK ON)。
// 单独命名避免 setupCleanupTestDB 的语义蔓延(本文件聚焦 DELETE 端到端,不是级联 cleanup)。
func setupDeleteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return setupCleanupTestDB(t)
}

// TestDeleteQuiz_CascadesQuestionsAndAnswers 验证删 quiz 物理 CASCADE 到 Question + Answer。
// 这是 2026-07-19 加 FK 之后的回归保护:以前删 quiz 留 Question/Answer 成孤儿。
func TestDeleteQuiz_CascadesQuestionsAndAnswers(t *testing.T) {
	db := setupDeleteTestDB(t)
	courseID, epID := seedCourseWithEpisode(t, db)
	userID, _, _ := seedCascadeFixture(t, db, courseID, epID)

	// 建一条 quiz + 2 题 + 1 answer。
	quiz := &model.Quiz{EpisodeID: epID, UserID: userID, CourseID: courseID, Status: "active"}
	if err := db.Create(quiz).Error; err != nil {
		t.Fatalf("create quiz: %v", err)
	}
	q1 := &model.Question{QuizID: quiz.ID, Type: "choice", Stem: "q1", Options: "[]", Scoring: "{}"}
	q2 := &model.Question{QuizID: quiz.ID, Type: "fill", Stem: "q2", Scoring: "{}"}
	if err := db.Create(q1).Error; err != nil {
		t.Fatalf("create q1: %v", err)
	}
	if err := db.Create(q2).Error; err != nil {
		t.Fatalf("create q2: %v", err)
	}
	db.Create(&model.Answer{QuestionID: q1.ID, QuizID: quiz.ID, UserID: userID, Correct: true, AnsweredAt: db.NowFunc()})

	// 删 quiz(走 repo 的 DeleteQuiz 方法)。
	repo := NewAIContentRepository(db)
	if err := repo.DeleteQuiz(quiz.ID); err != nil {
		t.Fatalf("DeleteQuiz: %v", err)
	}

	// 验证 quiz + 它的 question + answer 全清(FK CASCADE)。
	if got := countRows(t, db, &model.Quiz{}, "id = ?", quiz.ID); got != 0 {
		t.Errorf("quiz: expected 0, got %d", got)
	}
	if got := countRows(t, db, &model.Question{}, "quiz_id = ?", quiz.ID); got != 0 {
		t.Errorf("questions: expected 0 (CASCADE), got %d", got)
	}
	if got := countRows(t, db, &model.Answer{}, "quiz_id = ?", quiz.ID); got != 0 {
		t.Errorf("answers: expected 0 (CASCADE), got %d", got)
	}
}

// TestDeleteAdvice_ScopeIsolation 验证删某 (user, scope, scope_id) 的 advice 不影响
// 同 user 其它 scope_id 的 advice(回归保护:多态 scope_id 删除 WHERE 必须严格三元组匹配)。
func TestDeleteAdvice_ScopeIsolation(t *testing.T) {
	db := setupDeleteTestDB(t)
	courseID, epID1 := seedCourseWithEpisode(t, db)
	userID, _, _ := seedCascadeFixture(t, db, courseID, epID1)
	// 再建一集 episode,模拟"同 user 的不同 episode advice"。
	ep2 := &model.Episode{Title: "ep2", CourseID: courseID, VideoRelativePath: "/y.mp4", SortOrder: 2}
	if err := db.Create(ep2).Error; err != nil {
		t.Fatalf("create ep2: %v", err)
	}
	now := db.NowFunc()
	adv1 := &model.StudyAdvice{UserID: userID, Scope: "episode", ScopeID: epID1, AdviceText: "adv-ep1", GeneratedAt: now}
	adv2 := &model.StudyAdvice{UserID: userID, Scope: "episode", ScopeID: ep2.ID, AdviceText: "adv-ep2", GeneratedAt: now}
	advCourse := &model.StudyAdvice{UserID: userID, Scope: "course", ScopeID: courseID, AdviceText: "adv-course", GeneratedAt: now}
	for _, a := range []*model.StudyAdvice{adv1, adv2, advCourse} {
		if err := db.Create(a).Error; err != nil {
			t.Fatalf("create advice: %v", err)
		}
	}

	repo := NewAIContentRepository(db)
	// 只删 ep1 的 advice。
	if err := repo.DeleteAdvice(userID, "episode", epID1); err != nil {
		t.Fatalf("DeleteAdvice: %v", err)
	}

	// ep1 的 advice 应没了。
	if got := countRows(t, db, &model.StudyAdvice{}, "user_id = ? AND scope = ? AND scope_id = ?", userID, "episode", epID1); got != 0 {
		t.Errorf("deleted advice (ep1): expected 0, got %d", got)
	}
	// ep2 的 advice 必须完好。
	if got := countRows(t, db, &model.StudyAdvice{}, "user_id = ? AND scope = ? AND scope_id = ?", userID, "episode", ep2.ID); got != 1 {
		t.Errorf("ep2 advice (must be untouched): expected 1, got %d", got)
	}
	// course scope 的 advice 也必须完好(scope 不同,绝不该误删)。
	if got := countRows(t, db, &model.StudyAdvice{}, "user_id = ? AND scope = ? AND scope_id = ?", userID, "course", courseID); got != 1 {
		t.Errorf("course advice (must be untouched): expected 1, got %d", got)
	}
}

// TestDeleteSummary/TestDeleteCourseSummary/TestDeleteUserReport 是几个 DELETE
// 端点的小覆盖,主要验证"按 unique key 删一行"的语义 + 不影响其他行。
func TestDeleteSummary_Isolated(t *testing.T) {
	db := setupDeleteTestDB(t)
	courseID, epID1 := seedCourseWithEpisode(t, db)
	_, _, _ = seedCascadeFixture(t, db, courseID, epID1)
	ep2 := &model.Episode{Title: "ep2", CourseID: courseID, VideoRelativePath: "/y.mp4", SortOrder: 2}
	db.Create(ep2)
	db.Create(&model.AISummary{EpisodeID: epID1, CourseID: courseID, SummaryJSON: "{}"})
	db.Create(&model.AISummary{EpisodeID: ep2.ID, CourseID: courseID, SummaryJSON: "{}"})

	repo := NewAIContentRepository(db)
	if err := repo.DeleteSummary(epID1); err != nil {
		t.Fatalf("DeleteSummary: %v", err)
	}
	if got := countRows(t, db, &model.AISummary{}, "episode_id = ?", epID1); got != 0 {
		t.Errorf("deleted summary: expected 0, got %d", got)
	}
	if got := countRows(t, db, &model.AISummary{}, "episode_id = ?", ep2.ID); got != 1 {
		t.Errorf("other summary (untouched): expected 1, got %d", got)
	}
}

// TestListUserAdvice_OrderByGeneratedAtDesc 验证 ListUserAdvice 按 generated_at DESC 返回。
func TestListUserAdvice_OrderByGeneratedAtDesc(t *testing.T) {
	db := setupDeleteTestDB(t)
	courseID, epID := seedCourseWithEpisode(t, db)
	userID, _, _ := seedCascadeFixture(t, db, courseID, epID)
	// 建第二个 episode + 第二个 course,这样 (user, scope, scope_id) 三元组各不同,
	// 不触发 unique 约束。
	ep2 := &model.Episode{Title: "ep2", CourseID: courseID, VideoRelativePath: "/y.mp4", SortOrder: 2}
	db.Create(ep2)
	course2 := &model.Course{Title: "course2"}
	db.Create(course2)
	now := db.NowFunc()
	// 三条不同时间的 advice(三个不同三元组)。
	db.Create(&model.StudyAdvice{UserID: userID, Scope: "episode", ScopeID: ep2.ID, AdviceText: "old", GeneratedAt: now.Add(-2e9)})
	db.Create(&model.StudyAdvice{UserID: userID, Scope: "course", ScopeID: course2.ID, AdviceText: "newest", GeneratedAt: now})
	db.Create(&model.StudyAdvice{UserID: userID, Scope: "episode", ScopeID: epID, AdviceText: "mid", GeneratedAt: now.Add(-1e9)})

	repo := NewAIContentRepository(db)
	rows, err := repo.ListUserAdvice(userID)
	if err != nil {
		t.Fatalf("ListUserAdvice: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].AdviceText != "newest" {
		t.Errorf("expected newest first, got %q", rows[0].AdviceText)
	}
	if rows[len(rows)-1].AdviceText != "old" {
		t.Errorf("expected old last, got %q", rows[len(rows)-1].AdviceText)
	}
}
