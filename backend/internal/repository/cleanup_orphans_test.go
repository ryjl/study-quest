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

// TestEpisodeDeleteCleansNoFKChildren verifies that EpisodeRepository.Delete
// removes the AI/observability child rows that have NO FK declaration
// (watch_events, ai_jobs, ai_summaries, content_chunks, knowledge_memory,
// quizzes, study_advice scope=episode). Without the manual deletes these would
// orphan and keep referencing a now-deleted episode_id.
func TestEpisodeDeleteCleansNoFKChildren(t *testing.T) {
	db := setupCleanupTestDB(t)
	_, epID := seedCourseWithEpisode(t, db)

	// Seed one row in each no-FK child table pointing at the episode.
	db.Create(&model.WatchEvent{UserID: 1, EpisodeID: epID, CourseID: 1, ContentType: "learning"})
	db.Create(&model.AIJob{JobType: "summary", EpisodeID: epID, CourseID: 1, Status: "queued"})
	db.Create(&model.AISummary{EpisodeID: epID, CourseID: 1, SummaryJSON: "{}"})
	db.Create(&model.ContentChunk{EpisodeID: epID, CourseID: 1, SourceType: "subtitle", ChunkIndex: 0, Text: "x"})
	db.Create(&model.KnowledgeMemory{UserID: 1, EpisodeID: epID, CourseID: 1, ChunkID: 1})
	db.Create(&model.Quiz{EpisodeID: epID, UserID: 1, CourseID: 1, Status: "active"})
	db.Create(&model.StudyAdvice{UserID: 1, Scope: "episode", ScopeID: epID, AdviceText: "x"})

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
// the course-level no-FK children (ai_jobs by course_id, ai_course_summaries,
// study_advice scope=course) PLUS the episode-scoped children via the
// per-episode loop.
func TestCourseDeleteCleansNoFKChildren(t *testing.T) {
	db := setupCleanupTestDB(t)
	courseID, epID := seedCourseWithEpisode(t, db)

	// Course-level no-FK rows.
	db.Create(&model.AIJob{JobType: "course_summary", EpisodeID: epID, CourseID: courseID, Status: "queued"})
	db.Create(&model.AICourseSummary{CourseID: courseID, SummaryText: "x"})
	db.Create(&model.StudyAdvice{UserID: 1, Scope: "course", ScopeID: courseID, AdviceText: "x"})
	// Episode-level no-FK row (must be caught by the per-episode loop).
	db.Create(&model.WatchEvent{UserID: 1, EpisodeID: epID, CourseID: courseID, ContentType: "learning"})

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
