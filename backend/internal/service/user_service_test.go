package service

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open in-memory SQLite DB: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("Failed to run schema migration: %v", err)
	}

	return db
}

// seedTestSubjects inserts the canonical subject set and returns a key→Subject
// map, so test fixtures can reference a real SubjectID when building Courses.
func seedTestSubjects(t *testing.T, db *gorm.DB) map[string]model.Subject {
	t.Helper()
	defaults := []model.Subject{
		{Key: "chinese", Label: "语文", SortOrder: 1},
		{Key: "math", Label: "数学", SortOrder: 2},
		{Key: "english", Label: "英语", SortOrder: 3},
		{Key: "physics", Label: "物理/科学", SortOrder: 4},
	}
	for i := range defaults {
		if err := db.Create(&defaults[i]).Error; err != nil {
			t.Fatalf("seed subject %s: %v", defaults[i].Key, err)
		}
	}
	out := make(map[string]model.Subject, len(defaults))
	for _, s := range defaults {
		out[s.Key] = s
	}
	return out
}

func TestUserService(t *testing.T) {
	db := setupTestDB(t)
	subjects := seedTestSubjects(t, db)
	userRepo := repository.NewUserRepository(db)
	svc := NewUserService(userRepo)

	t.Run("CreateUserValidPIN", func(t *testing.T) {
		user, err := svc.CreateUser("ChildA", "http://avatar.url", "1234", "student")
		if err != nil {
			t.Fatalf("Expected no error creating user, got: %v", err)
		}

		if user.Nickname != "ChildA" {
			t.Errorf("Expected nickname ChildA, got: %s", user.Nickname)
		}
		if user.Role != "student" {
			t.Errorf("Expected role student, got: %s", user.Role)
		}
		if user.PinHash == "1234" {
			t.Error("Expected PIN to be encrypted/hashed, got plain text")
		}
	})

	t.Run("CreateUserInvalidPIN", func(t *testing.T) {
		// PIN must be 4-6 digits
		_, err := svc.CreateUser("ChildB", "http://avatar.url", "12", "student")
		if err == nil {
			t.Error("Expected error creating user with short PIN, got nil")
		}

		_, err = svc.CreateUser("ChildB", "http://avatar.url", "1234567", "student")
		if err == nil {
			t.Error("Expected error creating user with long PIN, got nil")
		}
	})

	t.Run("Authenticate", func(t *testing.T) {
		user, err := svc.CreateUser("ChildC", "http://avatar.url", "5678", "student")
		if err != nil {
			t.Fatalf("Failed to create test user: %v", err)
		}

		// Authenticate with valid PIN
		ok, err := svc.Authenticate(user.ID, "5678")
		if err != nil {
			t.Fatalf("Expected no error during authentication, got: %v", err)
		}
		if !ok {
			t.Error("Expected authentication to pass for correct PIN")
		}

		// Authenticate with incorrect PIN
		ok, err = svc.Authenticate(user.ID, "1111")
		if err != nil {
			t.Fatalf("Expected no error during authentication, got: %v", err)
		}
		if ok {
			t.Error("Expected authentication to fail for incorrect PIN")
		}

		// Authenticate non-existent user
		_, err = svc.Authenticate(999, "5678")
		if err == nil {
			t.Error("Expected authentication to error out for invalid user ID, got nil")
		}
	})

	t.Run("UpdateUser", func(t *testing.T) {
		user, err := svc.CreateUser("OrigName", "http://avatar.orig", "1111", "student")
		if err != nil {
			t.Fatalf("Failed to create user: %v", err)
		}

		// Update fields without changing PIN
		updated, err := svc.UpdateUser(user.ID, "NewName", "http://avatar.new", "", "teen")
		if err != nil {
			t.Fatalf("UpdateUser failed: %v", err)
		}

		if updated.Nickname != "NewName" || updated.AvatarURL != "http://avatar.new" || updated.Role != "teen" {
			t.Errorf("Updated fields mismatch, got: %+v", updated)
		}

		// Old PIN should still work
		ok, err := svc.Authenticate(user.ID, "1111")
		if err != nil || !ok {
			t.Errorf("Old PIN should still work when not reset: ok=%v, err=%v", ok, err)
		}

		// Update PIN
		_, err = svc.UpdateUser(user.ID, "NewName", "http://avatar.new", "2222", "teen")
		if err != nil {
			t.Fatalf("UpdateUser with PIN failed: %v", err)
		}

		// New PIN should work, old PIN should fail
		ok, err = svc.Authenticate(user.ID, "2222")
		if err != nil || !ok {
			t.Errorf("New PIN should work: ok=%v, err=%v", ok, err)
		}

		ok, err = svc.Authenticate(user.ID, "1111")
		if err != nil || ok {
			t.Errorf("Old PIN should no longer work: ok=%v, err=%v", ok, err)
		}
	})

	t.Run("BulkCourseAccess", func(t *testing.T) {
		user, err := svc.CreateUser("BulkUser", "http://avatar", "1234", "student")
		if err != nil {
			t.Fatalf("Failed to create user: %v", err)
		}

		courseRepo := repository.NewCourseRepository(db)
		courseSvc := NewCourseService(courseRepo, userRepo)

		_, _ = courseSvc.CreateCourse("Course1", "3", subjects["math"].ID, "", nil, "")
		_, _ = courseSvc.CreateCourse("Course2", "4", subjects["physics"].ID, "", nil, "")

		// Grant all
		err = svc.BulkCourseAccess(user.ID, "grant_all")
		if err != nil {
			t.Fatalf("BulkCourseAccess grant_all failed: %v", err)
		}

		accessList, err := svc.GetUserCourseAccess(user.ID)
		if err != nil {
			t.Fatalf("GetUserCourseAccess failed: %v", err)
		}

		if len(accessList) != 2 {
			t.Errorf("Expected access list size 2, got %d", len(accessList))
		}

		// Revoke all
		err = svc.BulkCourseAccess(user.ID, "revoke_all")
		if err != nil {
			t.Fatalf("BulkCourseAccess revoke_all failed: %v", err)
		}

		accessList, err = svc.GetUserCourseAccess(user.ID)
		if err != nil {
			t.Fatalf("GetUserCourseAccess failed: %v", err)
		}

		if len(accessList) != 0 {
			t.Errorf("Expected empty access list, got %d", len(accessList))
		}
	})
}

