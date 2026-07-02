package service

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupEpisodeTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open in-memory SQLite DB: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("Failed to run schema migration: %v", err)
	}

	return db
}

func TestEpisodeService(t *testing.T) {
	db := setupEpisodeTestDB(t)
	episodeRepo := repository.NewEpisodeRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)
	svc := NewEpisodeService(episodeRepo, settingsRepo)

	t.Run("ReorderEpisodes", func(t *testing.T) {
		courseRepo := repository.NewCourseRepository(db)
		c := &model.Course{Title: "Sort Course", Grade: "3", Subject: "math"}
		_ = courseRepo.Create(c)

		ep1, _ := svc.CreateEpisode(c.ID, 0, "Episode A", "/path/a.mp4", "[]", 1, "", "", nil, nil)
		ep2, _ := svc.CreateEpisode(c.ID, 0, "Episode B", "/path/b.mp4", "[]", 2, "", "", nil, nil)
		ep3, _ := svc.CreateEpisode(c.ID, 0, "Episode C", "/path/c.mp4", "[]", 3, "", "", nil, nil)

		// Reorder to C, A, B
		newOrder := []uint{ep3.ID, ep1.ID, ep2.ID}
		err := svc.ReorderEpisodes(newOrder)
		if err != nil {
			t.Fatalf("ReorderEpisodes failed: %v", err)
		}

		eps, err := svc.GetEpisodesByCourse(c.ID)
		if err != nil {
			t.Fatalf("GetEpisodesByCourse failed: %v", err)
		}

		if len(eps) != 3 {
			t.Fatalf("Expected 3 episodes, got %d", len(eps))
		}

		// Because GetEpisodesByCourse returns ordered by sort_order asc
		if eps[0].ID != ep3.ID || eps[0].SortOrder != 1 {
			t.Errorf("First episode should be ep3 with sort order 1, got ID=%d, order=%d", eps[0].ID, eps[0].SortOrder)
		}
		if eps[1].ID != ep1.ID || eps[1].SortOrder != 2 {
			t.Errorf("Second episode should be ep1 with sort order 2, got ID=%d, order=%d", eps[1].ID, eps[1].SortOrder)
		}
		if eps[2].ID != ep2.ID || eps[2].SortOrder != 3 {
			t.Errorf("Third episode should be ep2 with sort order 3, got ID=%d, order=%d", eps[2].ID, eps[2].SortOrder)
		}
	})

	t.Run("SubtitlesAndAutoMatch", func(t *testing.T) {
		courseRepo := repository.NewCourseRepository(db)
		c := &model.Course{Title: "Sub Course", Grade: "4", Subject: "english"}
		_ = courseRepo.Create(c)

		// Create an episode
		size := int64(1048576)
		ep, err := svc.CreateEpisode(c.ID, 0, "English Lesson 01", "/english/01.mp4", "[]", 1, "", "/english/lesson1/01.mp4", &size, nil)
		if err != nil {
			t.Fatalf("CreateEpisode failed: %v", err)
		}

		// Save first subtitle (Chinese)
		err = svc.SaveSubtitle(ep.ID, "zh-CN", "中文简体", "1\n00:00:01,000 --> 00:00:04,000\n你好")
		if err != nil {
			t.Fatalf("SaveSubtitle failed: %v", err)
		}

		// Save second subtitle (English)
		err = svc.SaveSubtitle(ep.ID, "en-US", "English", "1\n00:00:01,000 --> 00:00:04,000\nHello")
		if err != nil {
			t.Fatalf("SaveSubtitle failed: %v", err)
		}

		// List subtitles
		subs, err := svc.ListSubtitles(ep.ID)
		if err != nil {
			t.Fatalf("ListSubtitles failed: %v", err)
		}
		if len(subs) != 2 {
			t.Errorf("Expected 2 subtitles, got %d", len(subs))
		}

		// Check values
		if subs[0].Language != "zh-CN" || subs[0].Label != "中文简体" {
			t.Errorf("Sub 0 language/label mismatch: %s / %s", subs[0].Language, subs[0].Label)
		}
		if subs[1].Language != "en-US" || subs[1].Label != "English" {
			t.Errorf("Sub 1 language/label mismatch: %s / %s", subs[1].Language, subs[1].Label)
		}

		// Test Auto-Match using FileSize
		matches, err := episodeRepo.FindByCriteria("01", &size, "lesson1")
		if err != nil {
			t.Fatalf("FindByCriteria failed: %v", err)
		}
		if len(matches) != 1 {
			t.Errorf("Expected exactly 1 match, got %d", len(matches))
		} else if matches[0].ID != ep.ID {
			t.Errorf("Matched wrong episode ID: expected %d, got %d", ep.ID, matches[0].ID)
		}

		// Delete a subtitle
		err = svc.DeleteSubtitle(subs[0].ID)
		if err != nil {
			t.Fatalf("DeleteSubtitle failed: %v", err)
		}

		subs, _ = svc.ListSubtitles(ep.ID)
		if len(subs) != 1 {
			t.Errorf("Expected 1 subtitle after deletion, got %d", len(subs))
		}
	})
}
