package service

import (
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// TestImportTreeRollbackOnMidFailure is the negative test for the Phase 1
// import transaction. It forces the Nth episode create to fail (via the
// test-only SetTestFailHook) and verifies that NOTHING is left behind — no
// course, no chapter, no earlier episodes. Before the transaction wrapping,
// the course + chapter + first episode would persist as orphans.
//
// This is the only test that exercises the rollback path of the import
// transaction; TestImportAtomicity (in cmd/server) covers the happy path.
func TestImportTreeRollbackOnMidFailure(t *testing.T) {
	db := testutil.NewFileDB(t) // file-backed: tx shares the schema
	subjects := testutil.SeedSubjects(t, db)

	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	chapterRepo := repository.NewChapterRepository(db)
	subjectRepo := repository.NewSubjectRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)
	resolver := NewStorageProviderResolver(repository.NewStorageSourceRepository(db), settingsRepo)

	svc := NewImportService(db, episodeRepo, courseRepo, resolver, chapterRepo, subjectRepo, nil).
		(*importService)

	// Force the 2ND episode create to fail. The tree below has 2 episodes under
	// one chapter: ep1 should succeed, ep2 should fail → the whole import
	// (course + chapter + ep1) must roll back.
	svc.SetTestFailHook(2)

	tree := &ImportPreviewNode{
		Name: "Root", IsDir: true, Type: "course",
		Children: []*ImportPreviewNode{
			{Name: "第一章", IsDir: true, Type: "chapter", Children: []*ImportPreviewNode{
				{Name: "第1集", Path: "/a/1.mp4", Type: "episode", Size: 100},
				{Name: "第2集", Path: "/a/2.mp4", Type: "episode", Size: 200},
			}},
		},
	}
	req := &ExecuteTreeImportRequest{
		NewCourse: &NewCourseRequest{Title: "回滚测试课", Grade: "3", Subject: subjects["math"].Key},
		Tree:      tree,
	}

	err := svc.ExecuteTreeImport(req)
	if err == nil {
		t.Fatal("import should have failed on the 2nd episode, got nil error")
	}

	// The transaction must have rolled back: NO course, NO chapter, NO episode.
	var courseCount, chapterCount, episodeCount int64
	db.Model(&model.Course{}).Where("title = ?", "回滚测试课").Count(&courseCount)
	db.Model(&model.Chapter{}).Where("title = ?", "第一章").Count(&chapterCount)
	db.Model(&model.Episode{}).Where("title IN ?", []string{"第1集", "第2集"}).Count(&episodeCount)

	if courseCount != 0 {
		t.Errorf("course count = %d, want 0 (transaction should have rolled back)", courseCount)
	}
	if chapterCount != 0 {
		t.Errorf("chapter count = %d, want 0 (rolled back)", chapterCount)
	}
	if episodeCount != 0 {
		t.Errorf("episode count = %d, want 0 (ep1 should have rolled back with ep2's failure)", episodeCount)
	}
}

func TestImportWithMultiGrades(t *testing.T) {
	db := testutil.NewFileDB(t)
	subjects := testutil.SeedSubjects(t, db)

	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	chapterRepo := repository.NewChapterRepository(db)
	subjectRepo := repository.NewSubjectRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)
	resolver := NewStorageProviderResolver(repository.NewStorageSourceRepository(db), settingsRepo)

	svc := NewImportService(db, episodeRepo, courseRepo, resolver, chapterRepo, subjectRepo, nil)

	tree := &ImportPreviewNode{
		Name: "Root", IsDir: true, Type: "course",
		Children: []*ImportPreviewNode{
			{Name: "第一章", IsDir: true, Type: "chapter", Children: []*ImportPreviewNode{
				{Name: "第1集", Path: "/a/1.mp4", Type: "episode", Size: 100},
			}},
		},
	}
	req := &ExecuteTreeImportRequest{
		NewCourse: &NewCourseRequest{Title: "多年级测试课", Grade: "3,4,5", Subject: subjects["math"].Key},
		Tree:      tree,
	}

	err := svc.ExecuteTreeImport(req)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	// Verify course exists and has correct grades.
	var course model.Course
	if err := db.Preload("Grades").Where("title = ?", "多年级测试课").First(&course).Error; err != nil {
		t.Fatalf("failed to query course: %v", err)
	}

	if len(course.Grades) != 3 {
		t.Fatalf("expected 3 grades, got %d", len(course.Grades))
	}

	expectedGrades := map[model.Grade]bool{
		"3": true,
		"4": true,
		"5": true,
	}
	for _, g := range course.Grades {
		if !expectedGrades[g.Grade] {
			t.Errorf("unexpected grade: %s", g.Grade)
		}
	}
}
