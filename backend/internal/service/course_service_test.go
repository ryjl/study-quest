package service

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupCourseTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open in-memory SQLite DB: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("Failed to run schema migration: %v", err)
	}

	return db
}

func TestCourseService(t *testing.T) {
	db := setupCourseTestDB(t)
	courseRepo := repository.NewCourseRepository(db)
	userRepo := repository.NewUserRepository(db)
	svc := NewCourseService(courseRepo, userRepo)

	t.Run("CreateCourseSingleGrade", func(t *testing.T) {
		course, err := svc.CreateCourse("Math Grade 3", "3", "math", "http://cover.url", "", "")
		if err != nil {
			t.Fatalf("Failed to create course: %v", err)
		}
		if course.Grade != "3" {
			t.Errorf("Expected grade 3, got: %s", course.Grade)
		}
	})

	t.Run("CreateCourseMultiGrade", func(t *testing.T) {
		course, err := svc.CreateCourse("Science 3-4", "3,4", "physics", "http://cover.url", "science,physics", "")
		if err != nil {
			t.Fatalf("Failed to create course with multi-grade: %v", err)
		}
		if course.Grade != "3,4" {
			t.Errorf("Expected grade 3,4, got: %s", course.Grade)
		}
		if course.Tags != "science,physics" {
			t.Errorf("Expected tags science,physics, got: %s", course.Tags)
		}
	})

	t.Run("CreateCourseInvalidGrade", func(t *testing.T) {
		_, err := svc.CreateCourse("Invalid", "12", "math", "", "", "")
		if err == nil {
			t.Error("Expected error creating course with invalid grade, got nil")
		}

		_, err = svc.CreateCourse("Invalid Parts", "3,invalid", "math", "", "", "")
		if err == nil {
			t.Error("Expected error creating course with invalid grade part, got nil")
		}
	})

	t.Run("UpdateCourse", func(t *testing.T) {
		course, err := svc.CreateCourse("Physics 7", "7", "physics", "", "science", "")
		if err != nil {
			t.Fatalf("Failed to create course: %v", err)
		}

		updated, err := svc.UpdateCourse(course.ID, "Physics 7-8", "7,8", "physics", "http://new.cover", "science,advanced", "")
		if err != nil {
			t.Fatalf("Failed to update course: %v", err)
		}

		if updated.Title != "Physics 7-8" || updated.Grade != "7,8" || updated.CoverURL != "http://new.cover" || updated.Tags != "science,advanced" {
			t.Errorf("Updated fields mismatch, got: %+v", updated)
		}
	})

	t.Run("FilterCoursesByGrade", func(t *testing.T) {
		// Clear courses
		db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Course{})

		// Create test courses
		_, _ = svc.CreateCourse("Course Grade 3 and 4", "3,4", "math", "", "", "")
		_, _ = svc.CreateCourse("Course Grade 4 and 5", "4,5", "math", "", "", "")
		_, _ = svc.CreateCourse("Course Universal", "universal", "math", "", "", "")
		_, _ = svc.CreateCourse("Course Grade 6 Only", "6", "math", "", "", "")

		// Query courses (simulating Admin user role)
		courses, err := svc.GetCourses(0, "admin", "3", "math")
		if err != nil {
			t.Fatalf("GetCourses failed: %v", err)
		}

		// Should match "Course Grade 3 and 4" and "Course Universal"
		if len(courses) != 2 {
			t.Errorf("Expected 2 courses matching grade 3 (Universal + 3,4), got %d", len(courses))
			for _, c := range courses {
				t.Logf("Matched: %s (%s)", c.Title, c.Grade)
			}
		}

		courses, err = svc.GetCourses(0, "admin", "4", "math")
		if err != nil {
			t.Fatalf("GetCourses failed: %v", err)
		}

		// Should match "Course Grade 3 and 4", "Course Grade 4 and 5", "Course Universal"
		if len(courses) != 3 {
			t.Errorf("Expected 3 courses matching grade 4, got %d", len(courses))
		}
	})
}
