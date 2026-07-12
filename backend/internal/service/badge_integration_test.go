package service

import (
	"strings"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
	"testing"

	"gorm.io/gorm"
)

func setupIntegrationDB(t *testing.T) *gorm.DB {
	return testutil.NewDB(t)

}

func TestBadgeAndProgressIntegration(t *testing.T) {
	db := setupIntegrationDB(t)
	subjects := testutil.SeedSubjects(t, db)

	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)

	badgeSvc := NewBadgeService(db, badgeRepo, progressRepo)
	progressSvc := NewProgressService(db, progressRepo, episodeRepo, badgeSvc)

	// 1. Seed the multi-tier default badges. episode_master (tier 0 threshold=3)
	// is the multi-tier episode-count badge we'll exercise here.
	if err := badgeSvc.SeedDefaultBadges(); err != nil {
		t.Fatalf("Failed to seed badges: %v", err)
	}
	episodeBadge, err := badgeRepo.FindByCode("episode_master")
	if err != nil || episodeBadge == nil {
		t.Fatalf("Failed to find seeded episode_master badge: %v", err)
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

	// episode_master multi-tier badge is initially NOT unlocked.
	ub, err := badgeRepo.FindUserBadge(user.ID, episodeBadge.ID)
	if err != nil {
		t.Fatalf("FindUserBadge: %v", err)
	}
	if ub != nil {
		t.Errorf("episode_master should not be unlocked yet, got %+v", ub)
	}

	// 3. Complete 2 episodes — below episode_master tier 0 (threshold=3).
	for i := 0; i < 2; i++ {
		if _, err := progressSvc.ReportProgress(user.ID, eps[i].ID, 90, 90); err != nil {
			t.Fatalf("ReportProgress failed: %v", err)
		}
	}
	ub, _ = badgeRepo.FindUserBadge(user.ID, episodeBadge.ID)
	if ub != nil {
		t.Errorf("episode_master tier 0 (threshold=3) should NOT unlock after 2 completions, got %+v", ub)
	}

	// 4. Complete the 3rd episode — now meets episode_master tier 0 (threshold=3).
	if _, err := progressSvc.ReportProgress(user.ID, eps[2].ID, 90, 90); err != nil {
		t.Fatalf("ReportProgress failed for 3rd episode: %v", err)
	}
	ub, _ = badgeRepo.FindUserBadge(user.ID, episodeBadge.ID)
	if ub == nil {
		t.Fatal("episode_master should be unlocked at tier 0 after 3 completions, got nil")
	}
	if ub.Tier != 0 {
		t.Errorf("episode_master tier = %d, want 0 after 3 completions", ub.Tier)
	}

	// 5. Complete 2 more (5 total) — still tier 0 (next tier is 10). Tier must
	// NOT advance and must NOT re-award.
	if _, err := progressSvc.ReportProgress(user.ID, eps[3].ID, 90, 90); err != nil {
		t.Fatalf("ReportProgress failed for 4th episode: %v", err)
	}
	if _, err := progressSvc.ReportProgress(user.ID, eps[4].ID, 90, 90); err != nil {
		t.Fatalf("ReportProgress failed for 5th episode: %v", err)
	}
	ub, _ = badgeRepo.FindUserBadge(user.ID, episodeBadge.ID)
	if ub == nil || ub.Tier != 0 {
		t.Errorf("episode_master tier = %v after 5 completions, want 0 (next tier is 10)", ub)
	}

	// The ledger must contain a badge_unlocked entry for episode_master with
	// the tier-0 reward (10). It was awarded exactly once (on the 3rd
	// completion), not re-awarded on completions 4 and 5.
	ledger, err := progressSvc.GetPointsLedger(user.ID, 50, 0)
	if err != nil {
		t.Fatalf("GetPointsLedger failed: %v", err)
	}
	var unlockCount, unlockReward int
	for _, item := range ledger {
		t.Logf("Ledger: ReasonType=%s, ChangeAmount=%d, Description=%s", item.ReasonType, item.ChangeAmount, item.Description)
		if item.ReasonType == "badge_unlocked" && strings.Contains(item.Description, "课时大师") {
			unlockCount++
			unlockReward = item.ChangeAmount
		}
	}
	if unlockCount != 1 {
		t.Errorf("episode_master unlock log count = %d, want 1 (no re-award on tier-stable completions)", unlockCount)
	}
	if unlockReward != 10 {
		t.Errorf("episode_master tier 0 reward = %d, want 10", unlockReward)
	}
}
