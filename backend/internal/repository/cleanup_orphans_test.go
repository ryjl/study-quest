package repository

import (
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"

	"gorm.io/gorm"
)

// setupCleanupTestDB returns a fresh DB with FK enforcement ON, so the
// declared OnDelete:CASCADE relations fire alongside the manual deletes the
// repos perform. The manual deletes for the no-FK AI/observability tables are
// what these tests exercise.
func setupCleanupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testutil.NewDB(t)
	db.Exec("PRAGMA foreign_keys=ON")
	return db
}

// seedCourseWithEpisode creates a course + one episode and returns their ids.
// Helper for the cascade-cleanup tests.
func seedCourseWithEpisode(t *testing.T, db *gorm.DB) (course, episode uint) {
	t.Helper()
	subjects := testutil.SeedSubjects(t, db)
	c := &model.Course{Title: "Cleanup Course", SubjectID: subjects["math"].ID}
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("create course: %v", err)
	}
	ep := &model.Episode{Title: "Cleanup Ep", CourseID: c.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := db.Create(ep).Error; err != nil {
		t.Fatalf("create episode: %v", err)
	}
	return c.ID, ep.ID
}

// seedCascadeFixture seeds a real user + content chunk + (optional) AIJob scope
// so AI tables that now carry FKs (Quiz/Answer/KnowledgeMemory/AIRun) can be
// inserted without "FOREIGN KEY constraint failed". Returns (userID, chunkID,
// aiJobID). Used by the cascade tests after seedCourseWithEpisode.
func seedCascadeFixture(t *testing.T, db *gorm.DB, courseID, epID uint) (user, chunk, aiJob uint) {
	t.Helper()
	u := &model.User{Nickname: "cleanup-user", PinHash: "x", Role: "student"}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	ch := &model.ContentChunk{EpisodeID: epID, CourseID: courseID, SourceType: "subtitle", ChunkIndex: 0, Text: "x"}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create chunk: %v", err)
	}
	epIDCopy, courseIDCopy := epID, courseID
	job := &model.AIJob{JobType: "summary", EpisodeID: &epIDCopy, CourseID: &courseIDCopy, Status: "queued"}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create ai_job: %v", err)
	}
	return u.ID, ch.ID, job.ID
}

// TestEpisodeDeleteCleansNoFKChildren verifies that EpisodeRepository.Delete
// removes the AI/observability child rows.
//
// 2026-07-19 起,AI 表(AISummary/ContentChunk/Quiz→Question→Answer/
// KnowledgeMemory/AIRun via AIJob)都加了 OnDelete:CASCADE,删 episode 时 DB
// 自动级联清。这个测试同时覆盖:
//   - 仍需手动清的 AIJob(EpisodeID 是 *uint 无 FK)和 StudyAdvice(scope_id 多态)
//   - 通过 CASCADE 间接清的 Question/Answer/AIRun(本轮新加 FK 之前是孤儿源)
//
// 任一行残留都说明 CASCADE 或手动清逻辑出问题。
func TestEpisodeDeleteCleansNoFKChildren(t *testing.T) {
	db := setupCleanupTestDB(t)
	courseID, epID := seedCourseWithEpisode(t, db)
	userID, chunkID, _ := seedCascadeFixture(t, db, courseID, epID)

	// Seed one row in each AI/observability child table pointing at the episode.
	db.Create(&model.WatchEvent{UserID: userID, EpisodeID: epID, CourseID: courseID, ContentType: "learning"})
	// AIJob 在 seedCascadeFixture 里已建一条(summary);下面用它的 id 给 AIRun 挂。
	db.Create(&model.AISummary{EpisodeID: epID, CourseID: courseID, SummaryJSON: "{}"})
	// ContentChunk seedCascadeFixture 已建一条(chunkID)。这里再建一条不同 index 的,验证 CASCADE。
	db.Create(&model.ContentChunk{EpisodeID: epID, CourseID: courseID, SourceType: "subtitle", ChunkIndex: 1, Text: "y"})
	db.Create(&model.KnowledgeMemory{UserID: userID, EpisodeID: epID, CourseID: courseID, ChunkID: chunkID})
	quiz := &model.Quiz{EpisodeID: epID, UserID: userID, CourseID: courseID, Status: "active"}
	if err := db.Create(quiz).Error; err != nil {
		t.Fatalf("create quiz: %v", err)
	}
	// Question + Answer 挂在 quiz 上:验证 CASCADE 链 episode→quiz→question→answer。
	db.Create(&model.Question{QuizID: quiz.ID, Type: "choice", Stem: "q1", Options: "[]", Scoring: "{}"})
	q1ID := uint(0)
	{
		var q model.Question
		if err := db.Where("quiz_id = ?", quiz.ID).First(&q).Error; err != nil {
			t.Fatalf("load question: %v", err)
		}
		q1ID = q.ID
	}
	db.Create(&model.Answer{QuestionID: q1ID, QuizID: quiz.ID, UserID: userID, Correct: true, AnsweredAt: db.NowFunc()})
	// AIRun 挂在 seedCascadeFixture 建的 summary AIJob 上:验证 CASCADE 链 episode→ai_job→ai_run。
	{
		var job model.AIJob
		if err := db.Where("episode_id = ?", epID).First(&job).Error; err != nil {
			t.Fatalf("load ai_job: %v", err)
		}
		db.Create(&model.AIRun{JobID: job.ID, Capability: "summary", InputJSON: "{}", ResponseText: "x"})
	}
	db.Create(&model.StudyAdvice{UserID: userID, Scope: "episode", ScopeID: epID, AdviceText: "x", GeneratedAt: db.NowFunc()})

	repo := NewEpisodeRepository(db)
	if err := repo.Delete(epID); err != nil {
		t.Fatalf("episode delete: %v", err)
	}

	asserts := []struct {
		name string
		got  int64
	}{
		{"watch_events", countRows(t, db, &model.WatchEvent{}, "episode_id = ?", epID)},
		{"ai_jobs", countRows(t, db, &model.AIJob{}, "episode_id = ?", epID)},
		{"ai_summaries", countRows(t, db, &model.AISummary{}, "episode_id = ?", epID)},
		{"content_chunks", countRows(t, db, &model.ContentChunk{}, "episode_id = ?", epID)},
		{"knowledge_memory", countRows(t, db, &model.KnowledgeMemory{}, "episode_id = ?", epID)},
		{"quizzes", countRows(t, db, &model.Quiz{}, "episode_id = ?", epID)},
		// Question/Answer/AIRun 通过 CASCADE 间接清:本轮新加的覆盖(以前是孤儿源)。
		{"questions", countRows(t, db, &model.Question{}, "quiz_id = ?", quiz.ID)},
		{"answers", countRows(t, db, &model.Answer{}, "quiz_id = ?", quiz.ID)},
		{"ai_runs", countRows(t, db, &model.AIRun{}, "job_id IN (SELECT id FROM ai_jobs WHERE episode_id = ?)", epID)},
	}
	for _, a := range asserts {
		if a.got != 0 {
			t.Errorf("%s: expected 0 rows after episode delete, got %d", a.name, a.got)
		}
	}
	// StudyAdvice is polymorphic on scope_id; check the scope-qualified row.
	if got := countRows(t, db, &model.StudyAdvice{}, "scope = ? AND scope_id = ?", "episode", epID); got != 0 {
		t.Errorf("study_advice episode-scope: expected 0, got %d", got)
	}
}

// TestCourseDeleteCleansNoFKChildren verifies CourseRepository.Delete removes
// the course-level AI children PLUS the episode-scoped children via the
// per-episode loop, AND that the CASCADE chain course→episode→quiz→question/
// answer 以及 course→ai_job→ai_run 都干净。
func TestCourseDeleteCleansNoFKChildren(t *testing.T) {
	db := setupCleanupTestDB(t)
	courseID, epID := seedCourseWithEpisode(t, db)
	userID, _, _ := seedCascadeFixture(t, db, courseID, epID)

	// Course-level AI rows.
	db.Create(&model.AICourseSummary{CourseID: courseID, SummaryText: "x", GeneratedAt: db.NowFunc()})
	db.Create(&model.StudyAdvice{UserID: userID, Scope: "course", ScopeID: courseID, AdviceText: "x", GeneratedAt: db.NowFunc()})
	// Episode-level:Quiz + Question + Answer(验证 course→episode→quiz→question/answer CASCADE)
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
	// AIRun 挂在 seedCascadeFixture 建的 summary AIJob 上(它也指向这个 course):
	// 验证 course→ai_job→ai_run CASCADE 链。
	{
		var job model.AIJob
		if err := db.Where("course_id = ?", courseID).First(&job).Error; err != nil {
			t.Fatalf("load ai_job for run attach: %v", err)
		}
		db.Create(&model.AIRun{JobID: job.ID, Capability: "summary", InputJSON: "{}", ResponseText: "x"})
	}
	// Episode-level non-AI row (must be caught by the per-episode loop).
	db.Create(&model.WatchEvent{UserID: userID, EpisodeID: epID, CourseID: courseID, ContentType: "learning"})

	repo := NewCourseRepository(db)
	if err := repo.Delete(courseID); err != nil {
		t.Fatalf("course delete: %v", err)
	}

	if got := countRows(t, db, &model.AIJob{}, "course_id = ?", courseID); got != 0 {
		t.Errorf("ai_jobs course_id=%d: expected 0, got %d", courseID, got)
	}
	if got := countRows(t, db, &model.AICourseSummary{}, "course_id = ?", courseID); got != 0 {
		t.Errorf("ai_course_summaries: expected 0, got %d", got)
	}
	if got := countRows(t, db, &model.StudyAdvice{}, "scope = ? AND scope_id = ?", "course", courseID); got != 0 {
		t.Errorf("study_advice course-scope: expected 0, got %d", got)
	}
	if got := countRows(t, db, &model.WatchEvent{}, "episode_id = ?", epID); got != 0 {
		t.Errorf("watch_events (via episode loop): expected 0, got %d", got)
	}
	// CASCADE 间接清的(本轮新覆盖):question/answer 通过 quiz 链,ai_run 通过 job 链。
	if quiz.ID != 0 {
		if got := countRows(t, db, &model.Question{}, "quiz_id = ?", quiz.ID); got != 0 {
			t.Errorf("questions: expected 0, got %d", got)
		}
		if got := countRows(t, db, &model.Answer{}, "quiz_id = ?", quiz.ID); got != 0 {
			t.Errorf("answers: expected 0, got %d", got)
		}
	}
	if got := countRows(t, db, &model.AIRun{}, "job_id IN (SELECT id FROM ai_jobs WHERE course_id = ?)", courseID); got != 0 {
		t.Errorf("ai_runs (via ai_jobs cascade): expected 0, got %d", got)
	}
}

// TestStorageCountReferences verifies the pre-delete reference count used by
// the admin delete handler to refuse deleting an in-use storage source.
func TestStorageCountReferences(t *testing.T) {
	db := setupCleanupTestDB(t)
	repo := NewStorageSourceRepository(db)
	src := &model.StorageSource{Name: "src", Type: "alist", URL: "http://x"}
	if err := repo.Create(src); err != nil {
		t.Fatalf("create source: %v", err)
	}

	// No references yet.
	ep, bk, err := repo.CountReferences(src.ID)
	if err != nil {
		t.Fatalf("count (empty): %v", err)
	}
	if ep != 0 || bk != 0 {
		t.Fatalf("empty source: expected 0/0, got %d/%d", ep, bk)
	}

	// Two episodes + one book on this source.
	subjects := testutil.SeedSubjects(t, db)
	c := &model.Course{Title: "C", SubjectID: subjects["math"].ID}
	db.Create(c)
	for i := 0; i < 2; i++ {
		db.Create(&model.Episode{Title: "e", CourseID: c.ID, VideoRelativePath: "/e.mp4", SortOrder: i, SourceID: &src.ID})
	}
	db.Create(&model.ReadingBook{Title: "b", SubjectID: subjects["math"].ID, FileRelativePath: "/b.pdf", SourceID: &src.ID})

	ep, bk, err = repo.CountReferences(src.ID)
	if err != nil {
		t.Fatalf("count (populated): %v", err)
	}
	if ep != 2 || bk != 1 {
		t.Errorf("populated source: expected 2/1, got %d/%d", ep, bk)
	}
}

// countRows is a tiny COUNT(*) helper to keep the assertions above one-lined.
// dest is a pointer to a zero-valued model (e.g. &model.WatchEvent{}) used to
// anchor the table; GORM derives the table name from the concrete type.
func countRows(t *testing.T, db *gorm.DB, dest any, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(dest).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatalf("count %T: %v", dest, err)
	}
	return n
}
