package service

import (
	"errors"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"
)

// TestBadgeDeleteRefusesSystem locks in that an IsSystem badge cannot be
// deleted (returns ErrSystemProtected), while a user-created badge can.
func TestBadgeDeleteRefusesSystem(t *testing.T) {
	db := setupIntegrationDB(t)
	repo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	svc := NewBadgeService(db, repo, progressRepo)

	system := &model.Badge{Code: "sys_one", Title: "系统", IconName: "x", RuleType: "watch_duration", Threshold: 1, IsSystem: true}
	if err := repo.Create(system); err != nil {
		t.Fatalf("create system badge: %v", err)
	}
	custom := &model.Badge{Code: "usr_one", Title: "自建", IconName: "x", RuleType: "watch_duration", Threshold: 1, IsSystem: false}
	if err := repo.Create(custom); err != nil {
		t.Fatalf("create custom badge: %v", err)
	}

	// System badge → must be refused.
	if err := svc.Delete(system.ID); !errors.Is(err, ErrSystemProtected) {
		t.Fatalf("delete system badge: expected ErrSystemProtected, got %v", err)
	}
	// And the row must still exist.
	if got, _ := repo.FindByID(system.ID); got == nil {
		t.Fatal("system badge was deleted despite protection")
	}

	// Custom badge → deletes cleanly.
	if err := svc.Delete(custom.ID); err != nil {
		t.Fatalf("delete custom badge: %v", err)
	}
	if got, _ := repo.FindByID(custom.ID); got != nil {
		t.Fatal("custom badge was not deleted")
	}
}

// TestSeedDefaultBadgesSeedsMultiTier verifies the curated multi-tier seed set
// is created with the expected codes, each multi-tier badge has a non-empty
// Tiers JSON, and legacy single-tier codes are gone.
func TestSeedDefaultBadgesSeedsMultiTier(t *testing.T) {
	db := setupIntegrationDB(t)
	repo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	svc := NewBadgeService(db, repo, progressRepo)

	if err := svc.SeedDefaultBadges(); err != nil {
		t.Fatalf("SeedDefaultBadges: %v", err)
	}

	// Multi-tier defaults must exist, be IsSystem, and carry Tiers.
	multiTier := []string{"streak", "episode_master", "time_master", "points_hero", "explorer", "course_master", "weekly_dedication"}
	for _, code := range multiTier {
		b, err := repo.FindByCode(code)
		if err != nil || b == nil {
			t.Errorf("expected seeded badge %q, got %v %v", code, b, err)
			continue
		}
		if !b.IsSystem {
			t.Errorf("badge %q must be IsSystem=true", code)
		}
		if b.Tiers == "" {
			t.Errorf("badge %q must have non-empty Tiers", code)
		}
	}
	// first_blood is single-tier (no Tiers, uses Threshold).
	fb, _ := repo.FindByCode("first_blood")
	if fb == nil || fb.Tiers != "" || fb.Threshold != 1 {
		t.Errorf("first_blood should be single-tier Threshold=1, got %+v", fb)
	}

	// Legacy retired codes must NOT survive the seed.
	for _, code := range []string{"seven_days_pioneer", "three_day_streak", "ten_episodes", "points_100", "night_owl"} {
		if b, _ := repo.FindByCode(code); b != nil {
			t.Errorf("legacy badge %q should not exist after seed", code)
		}
	}
}

// TestSeedDefaultBadgesRebuildsLegacyInstall was removed: the legacy
// single-tier → multi-tier rebuild (detectLegacyBadges / rebuildBadgeTables)
// was deleted in the v1 schema cleanup. A fresh v1 DB has no legacy badges.
