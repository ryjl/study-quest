package repository

import (
	"testing"

	"studyquest/backend/internal/model"
	"gorm.io/gorm"
)

// TestUserDeleteCleansAIChildren verifies that UserRepository.Delete removes
// the user's AI data. 2026-07-19 这轮把 Quiz/Question/Answer/KnowledgeMemory/
// UserStudyReport 的 UserID 都加了 OnDelete:CASCADE,删 User 时 DB 自动级联清;
// AIJob/StudyAdvice 仍需手动清(UserID 是 *uint 多态 / scope_id 多态)。
//
// 之前 userRepo.Delete 完全不动任何 AI 表 —— 删 user 后所有 AI 数据成孤儿,
// 这是孤儿数据的主源。本测试覆盖 CASCADE + 手动清两类。
func TestUserDeleteCleansAIChildren(t *testing.T) {
	db := setupCleanupTestDB(t)
	courseID, epID := seedCourseWithEpisode(t, db)
	userID, chunkID, _ := seedCascadeFixture(t, db, courseID, epID)

	// Seed AI 数据挂在 user 上。
	// AIJob(已由 seedCascadeFixture 创建一条 summary job,UserID=nil 通用)。
	// 再建一条带 UserID 的 quiz job,验证 user_repo.Delete 的手动 ai_job 清理。
	uidCopy := userID
	job2 := &model.AIJob{JobType: "quiz", EpisodeID: &epID, CourseID: &courseID, UserID: &uidCopy, Status: "queued"}
	if err := db.Create(job2).Error; err != nil {
		t.Fatalf("create user-scoped ai_job: %v", err)
	}
	// Quiz + Question + Answer:验证 CASCADE 链 user→quiz→question/answer。
	quiz := &model.Quiz{EpisodeID: epID, UserID: userID, CourseID: courseID, Status: "active"}
	if err := db.Create(quiz).Error; err != nil {
		t.Fatalf("create quiz: %v", err)
	}
	db.Create(&model.Question{QuizID: quiz.ID, Type: "choice", Stem: "q", Options: "[]", Scoring: "{}"})
	q1ID := uint(0)
	{
		var q model.Question
		if err := db.Where("quiz_id = ?", quiz.ID).First(&q).Error; err == nil {
			q1ID = q.ID
		}
	}
	if q1ID != 0 {
		db.Create(&model.Answer{QuestionID: q1ID, QuizID: quiz.ID, UserID: userID, Correct: true, AnsweredAt: db.NowFunc()})
	}
	// KnowledgeMemory(CASCADE via UserID)。
	db.Create(&model.KnowledgeMemory{UserID: userID, EpisodeID: epID, CourseID: courseID, ChunkID: chunkID})
	// StudyAdvice:三档 scope 各一条(多态 scope_id 无 FK,user_id 手动清)。
	now := db.NowFunc()
	db.Create(&model.StudyAdvice{UserID: userID, Scope: "episode", ScopeID: epID, AdviceText: "x", GeneratedAt: now})
	db.Create(&model.StudyAdvice{UserID: userID, Scope: "course", ScopeID: courseID, AdviceText: "x", GeneratedAt: now})
	db.Create(&model.StudyAdvice{UserID: userID, Scope: "subject", ScopeID: 1, AdviceText: "x", GeneratedAt: now})
	// UserStudyReport(CASCADE via UserID)。
	db.Create(&model.UserStudyReport{UserID: userID, ReportText: "x", GeneratedAt: now})
	// AIRun 挂在 user-scoped job 上:验证 user→ai_job→ai_run 链(job 先手动清,run 跟着 CASCADE)。
	db.Create(&model.AIRun{JobID: job2.ID, Capability: "quiz", InputJSON: "{}", ResponseText: "x"})
	// 也挂一条到通用 job(jobID 那条,UserID=nil):这条不应被 user 删除影响(验证不误删)。
	// (此处只断言 user_id=job2 的 run 清干净;通用 job 的 run 由 episode 删除场景测。)

	repo := NewUserRepository(db)
	if err := repo.Delete(userID); err != nil {
		t.Fatalf("user delete: %v", err)
	}

	// CASCADE 清的表(本轮新加 FK):Quiz/Question/Answer/KnowledgeMemory/UserStudyReport。
	if got := countRows(t, db, &model.Quiz{}, "user_id = ?", userID); got != 0 {
		t.Errorf("quizzes: expected 0, got %d", got)
	}
	if quiz.ID != 0 {
		if got := countRows(t, db, &model.Question{}, "quiz_id = ?", quiz.ID); got != 0 {
			t.Errorf("questions: expected 0, got %d", got)
		}
		if got := countRows(t, db, &model.Answer{}, "quiz_id = ?", quiz.ID); got != 0 {
			t.Errorf("answers: expected 0, got %d", got)
		}
	}
	if got := countRows(t, db, &model.KnowledgeMemory{}, "user_id = ?", userID); got != 0 {
		t.Errorf("knowledge_memories: expected 0, got %d", got)
	}
	if got := countRows(t, db, &model.UserStudyReport{}, "user_id = ?", userID); got != 0 {
		t.Errorf("user_study_reports: expected 0, got %d", got)
	}
	// 手动清的表:AIJob(user_id 多态) + StudyAdvice(user_id + scope_id 多态)。
	if got := countRows(t, db, &model.AIJob{}, "user_id = ?", userID); got != 0 {
		t.Errorf("ai_jobs (manual cleanup): expected 0, got %d", got)
	}
	if got := countRows(t, db, &model.StudyAdvice{}, "user_id = ?", userID); got != 0 {
		t.Errorf("study_advices (manual cleanup): expected 0, got %d", got)
	}
	// AIRun 应跟着它挂的 job2 一起清(job2 是 user-scoped,被手动删;AIRun via FK CASCADE)。
	if got := countRows(t, db, &model.AIRun{}, "job_id = ?", job2.ID); got != 0 {
		t.Errorf("ai_runs (cascade via ai_job): expected 0, got %d", got)
	}
}

// TestUserDelete_DoesNotLeakToOtherUsers 验证删 user1 不影响 user2 的 AI 数据。
// 这条测试的必要性:曾有过 episode/course 级联测试只断言"目标实体的数据清了",
// 但 SQL 语句一旦 WHERE 写漏会把别的 user 的也清掉,这里专门盯这个回归。
func TestUserDelete_DoesNotLeakToOtherUsers(t *testing.T) {
	db := setupCleanupTestDB(t)
	courseID, epID := seedCourseWithEpisode(t, db)
	_, chunkID, _ := seedCascadeFixture(t, db, courseID, epID)

	// 两个 user,各自挂一条 advice。
	u1 := &model.User{Nickname: "u1", PinHash: "x", Role: "student"}
	u2 := &model.User{Nickname: "u2", PinHash: "x", Role: "student"}
	if err := db.Create(u1).Error; err != nil {
		t.Fatalf("create u1: %v", err)
	}
	if err := db.Create(u2).Error; err != nil {
		t.Fatalf("create u2: %v", err)
	}
	now := db.NowFunc()
	db.Create(&model.StudyAdvice{UserID: u1.ID, Scope: "episode", ScopeID: epID, AdviceText: "u1 advice", GeneratedAt: now})
	db.Create(&model.StudyAdvice{UserID: u2.ID, Scope: "episode", ScopeID: epID, AdviceText: "u2 advice", GeneratedAt: now})
	db.Create(&model.KnowledgeMemory{UserID: u1.ID, EpisodeID: epID, CourseID: courseID, ChunkID: chunkID})
	db.Create(&model.KnowledgeMemory{UserID: u2.ID, EpisodeID: epID, CourseID: courseID, ChunkID: chunkID})

	repo := NewUserRepository(db)
	if err := repo.Delete(u1.ID); err != nil {
		t.Fatalf("delete u1: %v", err)
	}

	// u1 的数据应清干净。
	if got := countRows(t, db, &model.StudyAdvice{}, "user_id = ?", u1.ID); got != 0 {
		t.Errorf("u1 study_advice: expected 0, got %d", got)
	}
	if got := countRows(t, db, &model.KnowledgeMemory{}, "user_id = ?", u1.ID); got != 0 {
		t.Errorf("u1 knowledge_memory: expected 0, got %d", got)
	}
	// u2 的数据必须完好 —— 删 user 不能误伤其它 user。
	if got := countRows(t, db, &model.StudyAdvice{}, "user_id = ?", u2.ID); got != 1 {
		t.Errorf("u2 study_advice: expected 1 (untouched), got %d", got)
	}
	if got := countRows(t, db, &model.KnowledgeMemory{}, "user_id = ?", u2.ID); got != 1 {
		t.Errorf("u2 knowledge_memory: expected 1 (untouched), got %d", got)
	}
}

// Compile-time: silence unused import warnings if gorm is only referenced via
// helpers above (it is used through db.NowFunc() / *gorm.DB signatures).
var _ = gorm.ErrRecordNotFound
