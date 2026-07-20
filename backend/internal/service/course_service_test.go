package service

import (
	"studyquest/backend/internal/testutil"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"

	"gorm.io/gorm"
)

func setupCourseTestDB(t *testing.T) *gorm.DB {
	return testutil.NewDB(t)

}

func TestCourseService(t *testing.T) {
	db := setupCourseTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	courseRepo := repository.NewCourseRepository(db)
	userRepo := repository.NewUserRepository(db)
	svc := NewCourseService(courseRepo, userRepo)

	t.Run("CreateCourseSingleGrade", func(t *testing.T) {
		course, err := svc.CreateCourse("Math Grade 3", []model.Grade{model.Grade("3")}, subjects["math"].ID, model.ContentLearning, "http://cover.url", nil, "", model.AIConfig{}, false, false)
		if err != nil {
			t.Fatalf("Failed to create course: %v", err)
		}
		if course.GradeDisplay() != "3年级" {
			t.Errorf("Expected grade 3年级, got: %s", course.GradeDisplay())
		}
	})

	t.Run("CreateCourseMultiGrade", func(t *testing.T) {
		course, err := svc.CreateCourse("Science 3-4", []model.Grade{model.Grade("3"), model.Grade("4")}, subjects["physics"].ID, model.ContentLearning, "http://cover.url", nil, "", model.AIConfig{}, false, false)
		if err != nil {
			t.Fatalf("Failed to create course with multi-grade: %v", err)
		}
		if course.GradeDisplay() != "3年级, 4年级" {
			t.Errorf("Expected grade 3年级, 4年级, got: %s", course.GradeDisplay())
		}
	})

	t.Run("CreateCourseCustomGradeTag", func(t *testing.T) {
		// 2026-07-20: grade 是开放 tag 体系,任意非空 trim 字符串都合法。
		// 不再有硬编码 enum 校验。"考研" 是自定义 tag,合法且原样显示。
		course, err := svc.CreateCourse("Custom Tag", []model.Grade{model.Grade("考研")}, subjects["math"].ID, model.ContentLearning, "", nil, "", model.AIConfig{}, false, false)
		if err != nil {
			t.Fatalf("custom grade tag '考研' should be accepted, got error: %v", err)
		}
		if course.GradeDisplay() != "考研" {
			t.Errorf("custom tag '考研' should display as-is, got: %s", course.GradeDisplay())
		}

		// 混合预设 + 自定义 tag 也应该接受。
		mixed, err := svc.CreateCourse("Mixed Tags", []model.Grade{model.GradePrimary, model.Grade("职场")}, subjects["math"].ID, model.ContentLearning, "", nil, "", model.AIConfig{}, false, false)
		if err != nil {
			t.Fatalf("mixed preset+custom grade tags should be accepted, got error: %v", err)
		}
		if mixed == nil || mixed.ID == 0 {
			t.Fatal("mixed-tag course should be created with valid ID")
		}
		if mixed.GradeDisplay() != "小学, 职场" {
			t.Errorf("mixed tags should display localized + raw, got: %s", mixed.GradeDisplay())
		}
	})

	t.Run("UpdateCourse", func(t *testing.T) {
		course, err := svc.CreateCourse("Physics 7", []model.Grade{model.Grade("7")}, subjects["physics"].ID, model.ContentLearning, "", nil, "", model.AIConfig{}, false, false)
		if err != nil {
			t.Fatalf("Failed to create course: %v", err)
		}

		updated, err := svc.UpdateCourse(course.ID, "Physics 7-8", []model.Grade{model.Grade("7"), model.Grade("8")}, subjects["physics"].ID, model.ContentLearning, "http://new.cover", nil, "", model.AIConfig{}, false, false)
		if err != nil {
			t.Fatalf("Failed to update course: %v", err)
		}

		if updated.Title != "Physics 7-8" || updated.GradeDisplay() != "7年级, 8年级" || updated.CoverURL != "http://new.cover" {
			t.Errorf("Updated fields mismatch, got: %+v", updated)
		}
	})

	t.Run("FilterCoursesByGrade", func(t *testing.T) {
		// Clear courses
		db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Course{})

		// Create test courses
		_, _ = svc.CreateCourse("Course Grade 3 and 4", []model.Grade{model.Grade("3"), model.Grade("4")}, subjects["math"].ID, model.ContentLearning, "", nil, "", model.AIConfig{}, false, false)
		_, _ = svc.CreateCourse("Course Grade 4 and 5", []model.Grade{model.Grade("4"), model.Grade("5")}, subjects["math"].ID, model.ContentLearning, "", nil, "", model.AIConfig{}, false, false)
		_, _ = svc.CreateCourse("Course Universal", []model.Grade{model.GradeUniversal}, subjects["math"].ID, model.ContentLearning, "", nil, "", model.AIConfig{}, false, false)
		_, _ = svc.CreateCourse("Course Grade 6 Only", []model.Grade{model.Grade("6")}, subjects["math"].ID, model.ContentLearning, "", nil, "", model.AIConfig{}, false, false)

		// Query courses (simulating Admin user role)
		courses, err := svc.GetCourses(0, "admin", "3", subjects["math"].ID, model.ContentLearning)
		if err != nil {
			t.Fatalf("GetCourses failed: %v", err)
		}

		// Should match "Course Grade 3 and 4" and "Course Universal"
		if len(courses) != 2 {
			t.Errorf("Expected 2 courses matching grade 3 (Universal + 3,4), got %d", len(courses))
			for _, c := range courses {
				t.Logf("Matched: %s (%s)", c.Title, c.GradeDisplay())
			}
		}

		courses, err = svc.GetCourses(0, "admin", "4", subjects["math"].ID, model.ContentLearning)
		if err != nil {
			t.Fatalf("GetCourses failed: %v", err)
		}

		// Should match "Course Grade 3 and 4", "Course Grade 4 and 5", "Course Universal"
		if len(courses) != 3 {
			t.Errorf("Expected 3 courses matching grade 4, got %d", len(courses))
		}
	})
}
