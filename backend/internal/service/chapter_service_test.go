package service

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupChapterTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open in-memory SQLite DB: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("Failed to run schema migration: %v", err)
	}

	return db
}

func TestChapterService(t *testing.T) {
	db := setupChapterTestDB(t)
	chapterRepo := repository.NewChapterRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)

	chapterSvc := NewChapterService(chapterRepo)
	episodeSvc := NewEpisodeService(episodeRepo, repository.NewSettingsRepository(db))

	course := &model.Course{Title: "Test Course", Grade: "3", Subject: "chinese"}
	_ = courseRepo.Create(course)

	var ch1 *model.Chapter

	t.Run("CreateChapter", func(t *testing.T) {
		var err error
		ch1, err = chapterSvc.CreateChapter(course.ID, "Reading Chapter", "Description of reading", "/path/cover.jpg", "", 1)
		if err != nil {
			t.Fatalf("CreateChapter failed: %v", err)
		}
		if ch1.Title != "Reading Chapter" || ch1.CourseID != course.ID {
			t.Errorf("Chapter fields mismatch: Title=%s, CourseID=%d", ch1.Title, ch1.CourseID)
		}
	})

	t.Run("GetChaptersByCourse", func(t *testing.T) {
		list, err := chapterSvc.GetChaptersByCourse(course.ID)
		if err != nil {
			t.Fatalf("GetChaptersByCourse failed: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("Expected 1 chapter, got %d", len(list))
		} else if list[0].ID != ch1.ID {
			t.Errorf("Chapter ID mismatch: expected %d, got %d", ch1.ID, list[0].ID)
		}
	})

	t.Run("UpdateChapter", func(t *testing.T) {
		ch, err := chapterSvc.UpdateChapter(ch1.ID, "Updated Reading Chapter", "New desc", "/path/new.jpg", "", 2)
		if err != nil {
			t.Fatalf("UpdateChapter failed: %v", err)
		}
		if ch.Title != "Updated Reading Chapter" || ch.SortOrder != 2 {
			t.Errorf("Update fields mismatch: Title=%s, SortOrder=%d", ch.Title, ch.SortOrder)
		}
	})

	t.Run("DeleteChapterDissociatesEpisodes", func(t *testing.T) {
		// Create episode belonging to this chapter
		ep, err := episodeSvc.CreateEpisode(course.ID, ch1.ID, "Lesson 1", "/path/1.mp4", "[]", 1, "", "/path/1.mp4", nil, nil)
		if err != nil {
			t.Fatalf("CreateEpisode failed: %v", err)
		}

		// Verify it belongs to chapter
		if ep.ChapterID != ch1.ID {
			t.Fatalf("Expected episode ChapterID to be %d, got %d", ch1.ID, ep.ChapterID)
		}

		// Delete Chapter
		err = chapterSvc.DeleteChapter(ch1.ID)
		if err != nil {
			t.Fatalf("DeleteChapter failed: %v", err)
		}

		// Verify chapter is deleted
		deletedCh, _ := chapterSvc.GetChapterByID(ch1.ID)
		if deletedCh != nil {
			t.Errorf("Expected chapter to be deleted, but still found")
		}

		// Verify episode's ChapterID is reset to 0
		updatedEp, _ := episodeSvc.GetEpisodeByID(ep.ID)
		if updatedEp.ChapterID != 0 {
			t.Errorf("Expected episode's ChapterID to be dissociated to 0, got %d", updatedEp.ChapterID)
		}
	})
}
