package service

import (
	"studyquest/backend/internal/testutil"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"
	"time"

	"gorm.io/gorm"
)

func setupProgressTestDB(t *testing.T) *gorm.DB {
	return testutil.NewDB(t)

}

func TestProgressServiceLastWatched(t *testing.T) {
	db := setupProgressTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	progressRepo := repository.NewProgressRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	entertainmentRepo := repository.NewEntertainmentRepository(db)
	svc := NewProgressService(db, progressRepo, episodeRepo, nil, courseRepo, entertainmentRepo, nil, 0)

	t.Run("GetLastWatchedEpisode", func(t *testing.T) {
		// Create User
		userRepo := repository.NewUserRepository(db)
		user := &model.User{Nickname: "StudentA", PinHash: "xxxx", Role: "student"}
		_ = userRepo.Create(user)

		// Create Course
		course := &model.Course{Title: "Resume Course", SubjectID: subjects["math"].ID}
		_ = courseRepo.Create(course)
		db.Create(&model.CourseGrade{CourseID: course.ID, Grade: model.Grade("3")})

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

// TestProgressCompletionAtomicity locks in the transaction boundary: when a
// report crosses the 90% completion threshold, BOTH the is_completed flag AND
// the 10-point award must land together (same transaction). The old code did
// these as two separate writes and swallowed the AddPoints error via
// fmt.Printf, so a points failure left the episode "completed" with no points —
// and the next heartbeat wouldn't re-award them (IsCompleted==1 skips the
// block). This test verifies they're now atomic.
func TestProgressCompletionAtomicity(t *testing.T) {
	db := setupProgressTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	progressRepo := repository.NewProgressRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	entertainmentRepo := repository.NewEntertainmentRepository(db)
	userRepo := repository.NewUserRepository(db)
	svc := NewProgressService(db, progressRepo, episodeRepo, nil, courseRepo, entertainmentRepo, nil, 0)

	user := &model.User{Nickname: "Completer", PinHash: "x", Role: "student"}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	course := &model.Course{Title: "C", SubjectID: subjects["math"].ID}
	if err := courseRepo.Create(course); err != nil {
		t.Fatalf("create course: %v", err)
	}
	db.Create(&model.CourseGrade{CourseID: course.ID, Grade: model.Grade("1")})
	// 100-second episode → 90% threshold = 90s.
	dur := 100
	ep := &model.Episode{CourseID: course.ID, Title: "E1", VideoRelativePath: "/p.mp4", SortOrder: 1, DurationSeconds: &dur}
	if err := episodeRepo.Create(ep); err != nil {
		t.Fatalf("create episode: %v", err)
	}

	// Report at position 95s (>90%) — should complete AND award 10 points in
	// the same transaction.
	prog, err := svc.ReportProgress(user.ID, ep.ID, 95, 95)
	if err != nil {
		t.Fatalf("ReportProgress: %v", err)
	}
	if prog.IsCompleted != true {
		t.Fatalf("IsCompleted = %v, want true", prog.IsCompleted)
	}

	// Points must be recorded too — proving both writes committed together.
	points, err := progressRepo.GetPoints(user.ID)
	if err != nil {
		t.Fatalf("GetPoints: %v", err)
	}
	if points == nil || points.CurrentPoints != 10 {
		got := 0
		if points != nil {
			got = points.CurrentPoints
		}
		t.Errorf("CurrentPoints = %d, want 10 (completion + award must be atomic)", got)
	}

	// A second report at the same position must NOT double-award: IsCompleted
	// is already 1, so the completion block is skipped entirely.
	_, err = svc.ReportProgress(user.ID, ep.ID, 95, 10)
	if err != nil {
		t.Fatalf("second ReportProgress: %v", err)
	}
	points2, _ := progressRepo.GetPoints(user.ID)
	got := 0
	if points2 != nil {
		got = points2.CurrentPoints
	}
	if got != 10 {
		t.Errorf("after second report, CurrentPoints = %d, want 10 (no double-award)", got)
	}
}
