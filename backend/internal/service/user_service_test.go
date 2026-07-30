package service

import (
	"errors"
	"testing"
	"time"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"

	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	return testutil.NewDB(t)
}

func TestUserService(t *testing.T) {
	db := setupTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	userRepo := repository.NewUserRepository(db)
	svc := NewUserService(userRepo)

	t.Run("CreateUserValidPIN", func(t *testing.T) {
		user, err := svc.CreateUser("ChildA", "http://avatar.url", "123456", "student", "")
		if err != nil {
			t.Fatalf("Expected no error creating user, got: %v", err)
		}

		if user.Nickname != "ChildA" {
			t.Errorf("Expected nickname ChildA, got: %s", user.Nickname)
		}
		if user.Role != "student" {
			t.Errorf("Expected role student, got: %s", user.Role)
		}
		if user.PinHash == "123456" {
			t.Error("Expected PIN to be encrypted/hashed, got plain text")
		}
	})

	t.Run("CreateUserInvalidPIN", func(t *testing.T) {
		// PIN must be exactly 6 digits: too short, too long, and a 5-digit
		// value that used to be valid under the old 4-6 rule must all reject.
		cases := []string{"12", "1234", "12345", "1234567"}
		for _, pin := range cases {
			_, err := svc.CreateUser("ChildB", "http://avatar.url", pin, "student", "")
			if err == nil {
				t.Errorf("Expected error creating user with PIN %q, got nil", pin)
			}
			if !errors.Is(err, ErrPinInvalid) {
				t.Errorf("CreateUser PIN %q: expected ErrPinInvalid, got %v", pin, err)
			}
		}

		// Exactly 6 must pass.
		if _, err := svc.CreateUser("ChildB6", "http://avatar.url", "123456", "student", ""); err != nil {
			t.Errorf("6-digit PIN should be accepted, got: %v", err)
		}
	})

	t.Run("CreateUserStoresGrade", func(t *testing.T) {
		user, err := svc.CreateUser("Graded", "http://avatar.url", "123456", "student", "四年级")
		if err != nil {
			t.Fatalf("CreateUser with grade failed: %v", err)
		}
		if user.Grade != "四年级" {
			t.Errorf("expected grade 四年级, got %q", user.Grade)
		}
		// Read back from DB to confirm persistence, not just the returned struct.
		loaded, err := userRepo.FindByID(user.ID)
		if err != nil || loaded == nil {
			t.Fatalf("FindByID: %v / %v", loaded, err)
		}
		if loaded.Grade != "四年级" {
			t.Errorf("persisted grade = %q, want 四年级", loaded.Grade)
		}
	})

	t.Run("Authenticate", func(t *testing.T) {
		user, err := svc.CreateUser("ChildC", "http://avatar.url", "567890", "student", "")
		if err != nil {
			t.Fatalf("Failed to create test user: %v", err)
		}

		// Authenticate with valid PIN
		ok, err := svc.Authenticate(user.ID, "567890")
		if err != nil {
			t.Fatalf("Expected no error during authentication, got: %v", err)
		}
		if !ok {
			t.Error("Expected authentication to pass for correct PIN")
		}

		// Authenticate with incorrect PIN
		ok, err = svc.Authenticate(user.ID, "111111")
		if err != nil {
			t.Fatalf("Expected no error during authentication, got: %v", err)
		}
		if ok {
			t.Error("Expected authentication to fail for incorrect PIN")
		}

		// Authenticate non-existent user
		_, err = svc.Authenticate(999, "567890")
		if err == nil {
			t.Error("Expected authentication to error out for invalid user ID, got nil")
		}
	})

	t.Run("UpdateUser", func(t *testing.T) {
		user, err := svc.CreateUser("OrigName", "http://avatar.orig", "111111", "student", "三年级")
		if err != nil {
			t.Fatalf("Failed to create user: %v", err)
		}

		// Update fields without changing PIN (empty pin = keep), and change grade.
		updated, err := svc.UpdateUser(user.ID, "NewName", "http://avatar.new", "", "admin", "四年级")
		if err != nil {
			t.Fatalf("UpdateUser failed: %v", err)
		}

		if updated.Nickname != "NewName" || updated.AvatarURL != "http://avatar.new" || updated.Role != "admin" {
			t.Errorf("Updated fields mismatch, got: %+v", updated)
		}
		if updated.Grade != "四年级" {
			t.Errorf("grade not updated: got %q", updated.Grade)
		}

		// Old PIN should still work
		ok, err := svc.Authenticate(user.ID, "111111")
		if err != nil || !ok {
			t.Errorf("Old PIN should still work when not reset: ok=%v, err=%v", ok, err)
		}

		// Update PIN (must be 6 digits now)
		_, err = svc.UpdateUser(user.ID, "NewName", "http://avatar.new", "222222", "admin", "四年级")
		if err != nil {
			t.Fatalf("UpdateUser with PIN failed: %v", err)
		}

		// New PIN should work, old PIN should fail
		ok, err = svc.Authenticate(user.ID, "222222")
		if err != nil || !ok {
			t.Errorf("New PIN should work: ok=%v, err=%v", ok, err)
		}

		ok, err = svc.Authenticate(user.ID, "111111")
		if err != nil || ok {
			t.Errorf("Old PIN should no longer work: ok=%v, err=%v", ok, err)
		}
	})

	t.Run("UpdateUserClearsGrade", func(t *testing.T) {
		user, err := svc.CreateUser("Graded2", "http://avatar.url", "123456", "student", "五年级")
		if err != nil {
			t.Fatalf("CreateUser failed: %v", err)
		}
		updated, err := svc.UpdateUser(user.ID, "Graded2", "", "123456", "student", "")
		if err != nil {
			t.Fatalf("UpdateUser failed: %v", err)
		}
		if updated.Grade != "" {
			t.Errorf("grade should be cleared, got %q", updated.Grade)
		}
	})

	t.Run("BulkCourseAccess", func(t *testing.T) {
		user, err := svc.CreateUser("BulkUser", "http://avatar", "123456", "student", "")
		if err != nil {
			t.Fatalf("Failed to create user: %v", err)
		}

		courseRepo := repository.NewCourseRepository(db)
		courseSvc := NewCourseService(courseRepo, userRepo)

		_, _ = courseSvc.CreateCourse("Course1", []model.Grade{model.Grade("3")}, subjects["math"].ID, model.ContentLearning, "", nil, "", model.AIConfig{}, false, false)
		_, _ = courseSvc.CreateCourse("Course2", []model.Grade{model.Grade("4")}, subjects["physics"].ID, model.ContentLearning, "", nil, "", model.AIConfig{}, false, false)

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

// TestAuthenticate_AccountLockout exercises the per-user lockout integration in
// Authenticate via an injectable clock (so we can advance the window without
// real sleeping). Lockout is keyed by user_id, counts FAILURES, and resets on
// success.
func TestAuthenticate_AccountLockout(t *testing.T) {
	db := setupTestDB(t)
	userRepo := repository.NewUserRepository(db)

	// Fixed clock the test can advance; copied by value into the lockout so
	// mutating t0 moves time forward for the service.
	t0 := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return t0 }
	svc := NewUserService(userRepo, WithLockoutClock(now)).(*userService)

	user, err := svc.CreateUser("LockTest", "", "654321", "student", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// 5 wrong PINs reach the threshold but don't lock yet (locked checks BEFORE
	// recording). The 6th call — even with the correct PIN — must be locked.
	wrongPin := "000000"
	for i := 0; i < DefaultLockoutMax; i++ {
		ok, err := svc.Authenticate(user.ID, wrongPin)
		if err != nil || ok {
			t.Fatalf("attempt %d: expected (false,nil) for wrong PIN, got (%v,%v)", i, ok, err)
		}
	}

	// 6th call with the CORRECT pin is still locked.
	_, err = svc.Authenticate(user.ID, "654321")
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("after %d failures: expected ErrAccountLocked, got %v", DefaultLockoutMax, err)
	}

	// Advance past the window: oldest failure ages out, account unlocks, and a
	// correct PIN succeeds (which also resets the counter).
	t0 = t0.Add(DefaultLockoutWindow + time.Second)
	ok, err := svc.Authenticate(user.ID, "654321")
	if err != nil || !ok {
		t.Fatalf("after window: expected success, got (%v,%v)", ok, err)
	}

	// Success resets the failure count: 4 more failures (below threshold) must
	// NOT lock, and then a correct PIN still works.
	for i := 0; i < DefaultLockoutMax-1; i++ {
		if ok, err := svc.Authenticate(user.ID, wrongPin); err != nil || ok {
			t.Fatalf("post-reset attempt %d: expected (false,nil), got (%v,%v)", i, ok, err)
		}
	}
	if ok, err := svc.Authenticate(user.ID, "654321"); err != nil || !ok {
		t.Fatalf("post-reset correct PIN should work, got (%v,%v)", ok, err)
	}
}

// TestAuthenticate_LockoutIsolatedPerUser verifies failures for one account do
// not lock a different account.
func TestAuthenticate_LockoutIsolatedPerUser(t *testing.T) {
	db := setupTestDB(t)
	userRepo := repository.NewUserRepository(db)
	t0 := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return t0 }
	svc := NewUserService(userRepo, WithLockoutClock(now)).(*userService)

	a, _ := svc.CreateUser("UserA", "", "111111", "student", "")
	b, _ := svc.CreateUser("UserB", "", "222222", "student", "")

	// Hammer user A into a lockout.
	for i := 0; i < DefaultLockoutMax; i++ {
		_, _ = svc.Authenticate(a.ID, "000000")
	}
	if _, err := svc.Authenticate(a.ID, "111111"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("user A should be locked, got %v", err)
	}

	// User B is unaffected and logs in fine.
	ok, err := svc.Authenticate(b.ID, "222222")
	if err != nil || !ok {
		t.Fatalf("user B should be unaffected: (%v,%v)", ok, err)
	}
}
