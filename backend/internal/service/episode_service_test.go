package service

import (
	"studyquest/backend/internal/testutil"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"

	"gorm.io/gorm"
)

func setupEpisodeTestDB(t *testing.T) *gorm.DB {
	return testutil.NewDB(t)

}

func TestEpisodeService(t *testing.T) {
	db := setupEpisodeTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	episodeRepo := repository.NewEpisodeRepository(db)
	settingsRepo := repository.NewSettingsRepository(db)
	svc := NewEpisodeService(episodeRepo, settingsRepo)

	t.Run("ReorderEpisodes", func(t *testing.T) {
		courseRepo := repository.NewCourseRepository(db)
		c := &model.Course{Title: "Sort Course", Grade: "3", SubjectID: subjects["math"].ID}
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
		c := &model.Course{Title: "Sub Course", Grade: "4", SubjectID: subjects["english"].ID}
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

	t.Run("UpdateEpisodeAdminPreservesMedia", func(t *testing.T) {
		courseRepo := repository.NewCourseRepository(db)
		c := &model.Course{Title: "Patch Course", Grade: "5", SubjectID: subjects["physics"].ID}
		_ = courseRepo.Create(c)

		// Create an episode with full media metadata (as if ffprobe had run).
		size := int64(2048576)
		dur := 750
		ep, err := svc.CreateEpisode(c.ID, 0, "原始标题", "/physics/orig.mp4", "[]", 1, "abc123hash", "/physics/orig.mp4", &size, &dur)
		if err != nil {
			t.Fatalf("CreateEpisode failed: %v", err)
		}
		// Simulate a probed media_meta_json being written directly.
		ep.MediaMetaJSON = `{"duration_seconds":750,"width":1920,"height":1080,"video_codec":"h264"}`
		if err := episodeRepo.Update(ep); err != nil {
			t.Fatalf("update media_meta: %v", err)
		}

		// Admin edits only the title + path. The PATCH-style update must NOT
		// touch file_hash / file_size / duration / media_meta_json.
		updated, err := svc.UpdateEpisodeAdmin(ep.ID, 0, "新标题", "/physics/renamed.mp4", 1)
		if err != nil {
			t.Fatalf("UpdateEpisodeAdmin failed: %v", err)
		}
		if updated == nil {
			t.Fatal("UpdateEpisodeAdmin returned nil")
		}

		// Editable fields changed.
		if updated.Title != "新标题" || updated.VideoRelativePath != "/physics/renamed.mp4" {
			t.Errorf("editable fields not updated: title=%q path=%q", updated.Title, updated.VideoRelativePath)
		}
		// Media fields preserved.
		if updated.FileHash != "abc123hash" {
			t.Errorf("file_hash clobbered: got %q want %q", updated.FileHash, "abc123hash")
		}
		if updated.FileSize == nil || *updated.FileSize != size {
			t.Errorf("file_size clobbered: got %v want %d", updated.FileSize, size)
		}
		if updated.DurationSeconds == nil || *updated.DurationSeconds != dur {
			t.Errorf("duration_seconds clobbered: got %v want %d", updated.DurationSeconds, dur)
		}
		if updated.MediaMetaJSON == "" {
			t.Error("media_meta_json was cleared by admin update")
		}

		// UpdateEpisodeAdmin on a non-existent episode returns nil, nil.
		missing, err := svc.UpdateEpisodeAdmin(99999, 0, "x", "/x.mp4", 1)
		if err != nil {
			t.Errorf("expected nil error for missing episode, got %v", err)
		}
		if missing != nil {
			t.Errorf("expected nil for missing episode, got %+v", missing)
		}
	})
}
