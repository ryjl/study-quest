package service

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupIntegrationDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open in-memory SQLite DB: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("Failed to run schema migration: %v", err)
	}

	return db
}

func TestBadgeAndProgressIntegration(t *testing.T) {
	db := setupIntegrationDB(t)
	subjects := seedTestSubjects(t, db)

	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)

	badgeSvc := NewBadgeService(badgeRepo, progressRepo)
	progressSvc := NewProgressService(progressRepo, episodeRepo, badgeSvc)

	// 1. Seed badges
	err := badgeSvc.SeedDefaultBadges()
	if err != nil {
		t.Fatalf("Failed to seed badges: %v", err)
	}

	// Verify math_expert has been seeded
	mathBadge, err := badgeRepo.FindByCode("math_expert")
	if err != nil || mathBadge == nil {
		t.Fatalf("Failed to find seeded math_expert badge: %v", err)
	}

	// 2. Setup user and courses
	userRepo := repository.NewUserRepository(db)
	user := &model.User{Nickname: "KidCoder", PinHash: "123456", Role: "student"}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	course := &model.Course{Title: "快乐数学", Grade: "3", SubjectID: subjects["math"].ID}
	if err := courseRepo.Create(course); err != nil {
		t.Fatalf("Failed to create course: %v", err)
	}

	// Create 5 episodes, with duration 100 seconds
	dur := 100
	var eps []*model.Episode
	for i := 1; i <= 5; i++ {
		ep := &model.Episode{
			CourseID:        course.ID,
			Title:           "第",
			SortOrder:       i,
			DurationSeconds: &dur,
		}
		if err := episodeRepo.Create(ep); err != nil {
			t.Fatalf("Failed to create episode: %v", err)
		}
		eps = append(eps, ep)
	}

	// Verify user points initially
	pt, err := progressSvc.GetPoints(user.ID)
	if err != nil {
		t.Fatalf("GetPoints failed: %v", err)
	}
	if pt != nil && pt.CurrentPoints > 0 {
		t.Errorf("Expected 0 points, got %d", pt.CurrentPoints)
	}

	// Verify math_expert is initially locked
	unlocked, err := badgeRepo.HasUnlocked(user.ID, mathBadge.ID)
	if err != nil || unlocked {
		t.Errorf("Expected badge to be locked, got unlocked=%v", unlocked)
	}

	// 3. Simulating watch logs to complete 5 math episodes
	// Complete Ep 1 to Ep 4 (4 episodes completed)
	for i := 0; i < 4; i++ {
		_, err := progressSvc.ReportProgress(user.ID, eps[i].ID, 90, 90) // 90% watched (>80%)
		if err != nil {
			t.Fatalf("ReportProgress failed: %v", err)
		}
	}

	// Points should be 4 * 10 = 40
	pt, err = progressSvc.GetPoints(user.ID)
	if err != nil || pt == nil || pt.CurrentPoints != 40 {
		t.Errorf("Expected 40 points, got %v", pt)
	}

	// math_expert badge should STILL be locked (threshold is 5)
	unlocked, err = badgeRepo.HasUnlocked(user.ID, mathBadge.ID)
	if err != nil || unlocked {
		t.Errorf("Expected math badge to be locked after 4 completions, got unlocked=%v", unlocked)
	}

	// Complete the 5th episode
	_, err = progressSvc.ReportProgress(user.ID, eps[4].ID, 90, 90)
	if err != nil {
		t.Fatalf("ReportProgress failed for 5th episode: %v", err)
	}

	// Points should be 5 * 10 = 50
	pt, err = progressSvc.GetPoints(user.ID)
	if err != nil || pt == nil || pt.CurrentPoints != 50 {
		t.Errorf("Expected 50 points, got %v", pt)
	}

	// 4. Verify math_expert is now UNLOCKED!
	unlocked, err = badgeRepo.HasUnlocked(user.ID, mathBadge.ID)
	if err != nil || !unlocked {
		t.Errorf("Expected math badge to be unlocked after 5 completions, got unlocked=%v", unlocked)
	}

	// Verify ledger contains the points logs and achievement unlock log
	ledger, err := progressSvc.GetPointsLedger(user.ID, 10, 0)
	if err != nil {
		t.Fatalf("GetPointsLedger failed: %v", err)
	}

	// Should contain 5 video completions + 1 badge unlock = 6 logs
	for i, item := range ledger {
		t.Logf("Ledger [%d]: ReasonType=%s, ChangeAmount=%d, Description=%s, CreatedAt=%v", i, item.ReasonType, item.ChangeAmount, item.Description, item.CreatedAt)
	}

	if len(ledger) != 7 {
		t.Errorf("Expected 7 logs in points ledger, got %d", len(ledger))
	}

	// The latest ledger log should be the math_expert badge unlocking log!
	latest := ledger[0]
	if latest.ReasonType != "badge_unlocked" {
		t.Errorf("Expected latest ledger log to be badge_unlocked, got: %s", latest.ReasonType)
	}
	if latest.ChangeAmount != 0 {
		t.Errorf("Expected badge unlocking transaction amount to be 0, got %d", latest.ChangeAmount)
	}
}
