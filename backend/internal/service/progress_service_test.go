package service

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupProgressTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open in-memory SQLite DB: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("Failed to run schema migration: %v", err)
	}

	return db
}

func TestProgressServiceLastWatched(t *testing.T) {
	db := setupProgressTestDB(t)
	progressRepo := repository.NewProgressRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	svc := NewProgressService(progressRepo, episodeRepo, nil)

	t.Run("GetLastWatchedEpisode", func(t *testing.T) {
		// Create User
		userRepo := repository.NewUserRepository(db)
		user := &model.User{Nickname: "StudentA", PinHash: "xxxx", Role: "student"}
		_ = userRepo.Create(user)

		// Create Course
		course := &model.Course{Title: "Resume Course", Grade: "3", Subject: "math"}
		_ = courseRepo.Create(course)

		// Create Episodes
		ep1 := &model.Episode{CourseID: course.ID, Title: "Ep 1", VideoRelativePath: "/path1.mp4", SortOrder: 1}
		ep2 := &model.Episode{CourseID: course.ID, Title: "Ep 2", VideoRelativePath: "/path2.mp4", SortOrder: 2}
		_ = episodeRepo.Create(ep1)
		_ = episodeRepo.Create(ep2)

		// Report progress on ep1 first
		_, err := svc.ReportProgress(user.ID, ep1.ID, 50, 10)
		if err != nil {
			t.Fatalf("Failed to report progress: %v", err)
		}

		// Check last watched, should be ep1
		lastEp, lastProg, err := svc.GetLastWatchedEpisode(user.ID, course.ID)
		if err != nil {
			t.Fatalf("GetLastWatchedEpisode failed: %v", err)
		}
		if lastEp == nil || lastEp.ID != ep1.ID {
			t.Errorf("Expected ep1, got: %+v", lastEp)
		}
		if lastProg.LastPositionSeconds != 50 {
			t.Errorf("Expected position 50, got %d", lastProg.LastPositionSeconds)
		}

		// Wait slightly to guarantee a different timestamp, then report progress on ep2
		time.Sleep(1 * time.Second)
		_, err = svc.ReportProgress(user.ID, ep2.ID, 120, 30)
		if err != nil {
			t.Fatalf("Failed to report progress: %v", err)
		}

		// Now last watched should be ep2
		lastEp, lastProg, err = svc.GetLastWatchedEpisode(user.ID, course.ID)
		if err != nil {
			t.Fatalf("GetLastWatchedEpisode failed: %v", err)
		}
		if lastEp == nil || lastEp.ID != ep2.ID {
			t.Errorf("Expected ep2, got: %+v", lastEp)
		}
		if lastProg.LastPositionSeconds != 120 {
			t.Errorf("Expected position 120, got %d", lastProg.LastPositionSeconds)
		}

		// Wait and update ep1 again
		time.Sleep(1 * time.Second)
		_, err = svc.ReportProgress(user.ID, ep1.ID, 90, 10)
		if err != nil {
			t.Fatalf("Failed to report progress: %v", err)
		}

		// Now last watched should be ep1 again
		lastEp, lastProg, err = svc.GetLastWatchedEpisode(user.ID, course.ID)
		if err != nil {
			t.Fatalf("GetLastWatchedEpisode failed: %v", err)
		}
		if lastEp == nil || lastEp.ID != ep1.ID {
			t.Errorf("Expected ep1, got: %+v", lastEp)
		}
		if lastProg.LastPositionSeconds != 90 {
			t.Errorf("Expected position 90, got %d", lastProg.LastPositionSeconds)
		}
	})
}
