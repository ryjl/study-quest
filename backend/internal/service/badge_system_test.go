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

// TestRemoveDeprecatedDefaultsNightOwl verifies that the retired "夜猫学者"
// (night_owl) default badge is removed on startup, BUT only when it is still a
// pristine system default. If an admin has taken ownership (IsSystem=false),
// it is left alone.
func TestRemoveDeprecatedDefaultsNightOwl(t *testing.T) {
	t.Run("removes_pristine_system_default", func(t *testing.T) {
		db := setupIntegrationDB(t)
		repo := repository.NewBadgeRepository(db)
		progressRepo := repository.NewProgressRepository(db)
		svc := NewBadgeService(db, repo, progressRepo)

		// Seed a pristine system night_owl (as an old install would have it).
		if err := repo.Create(&model.Badge{Code: "night_owl", Title: "夜猫学者", IconName: "x", RuleType: "night_owl_count", Threshold: 3, IsSystem: true}); err != nil {
			t.Fatalf("seed night_owl: %v", err)
		}

		if err := svc.RemoveDeprecatedDefaults(); err != nil {
			t.Fatalf("RemoveDeprecatedDefaults: %v", err)
		}
		if got, _ := repo.FindByCode("night_owl"); got != nil {
			t.Fatal("pristine night_owl system default should have been removed")
		}
	})

	t.Run("keeps_admin_owned_copy", func(t *testing.T) {
		db := setupIntegrationDB(t)
		repo := repository.NewBadgeRepository(db)
		progressRepo := repository.NewProgressRepository(db)
		svc := NewBadgeService(db, repo, progressRepo)

		// Same code, but an admin flipped IsSystem off (took ownership). Must
		// survive the cleanup.
		if err := repo.Create(&model.Badge{Code: "night_owl", Title: "我的夜猫", IconName: "x", RuleType: "night_owl_count", Threshold: 3, IsSystem: false}); err != nil {
			t.Fatalf("seed night_owl: %v", err)
		}

		if err := svc.RemoveDeprecatedDefaults(); err != nil {
			t.Fatalf("RemoveDeprecatedDefaults: %v", err)
		}
		if got, _ := repo.FindByCode("night_owl"); got == nil {
			t.Fatal("admin-owned night_owl must NOT be removed by cleanup")
		}
	})
}

// TestSeedDefaultBadgesIncludesNewDefaults verifies the curated seed set has
// the new badges (hard_worker, explorer) and no longer includes night_owl.
func TestSeedDefaultBadgesIncludesNewDefaults(t *testing.T) {
	db := setupIntegrationDB(t)
	repo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	svc := NewBadgeService(db, repo, progressRepo)

	if err := svc.SeedDefaultBadges(); err != nil {
		t.Fatalf("SeedDefaultBadges: %v", err)
	}

	wantCodes := []string{"first_blood", "seven_days_pioneer", "math_expert", "english_star", "hard_worker", "explorer"}
	for _, code := range wantCodes {
		b, err := repo.FindByCode(code)
		if err != nil || b == nil {
			t.Errorf("expected seeded default badge %q, got %v %v", code, b, err)
			continue
		}
		if !b.IsSystem {
			t.Errorf("seeded badge %q must be IsSystem=true", code)
		}
	}

	// night_owl must NOT be in the fresh seed (and SeedDefaultBadges runs the
	// cleanup, so even if a row somehow existed it'd be gone).
	if b, _ := repo.FindByCode("night_owl"); b != nil {
		t.Error("night_owl should not be part of the default badge set")
	}
}
