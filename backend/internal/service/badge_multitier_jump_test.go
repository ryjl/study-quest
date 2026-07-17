package service

import (
	"strings"
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// TestMultiTierJumpCreditsSum verifies Bug #7: when a single EvaluateRules call
// advances a user across MULTIPLE tiers at once (currentTier+1 .. tierIdx), the
// points ledger credits the SUM of every crossed tier's reward — not just the
// final tier's. This happens when progress is already far past several
// thresholds the first time EvaluateRules runs for a badge (e.g. the badge was
// created after the user already had the progress, or the first evaluation
// after a long absence).
//
// Setup: complete many episodes (progress accumulates with NO matching badge
// present yet, so prior EvaluateRules calls had nothing to unlock), THEN create
// a multi-tier badge whose thresholds the progress already clears several of,
// THEN call EvaluateRules once and assert the ledger amount is the SUM.
func TestMultiTierJumpCreditsSum(t *testing.T) {
	db := testutil.NewDB(t)
	subjects := testutil.SeedSubjects(t, db)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	entertainmentRepo := repository.NewEntertainmentRepository(db)
	userRepo := repository.NewUserRepository(db)
	bs := NewBadgeService(db, badgeRepo, progressRepo)
	ps := NewProgressService(db, progressRepo, episodeRepo, bs, courseRepo, entertainmentRepo, nil, 0)
	user := &model.User{Nickname: "jump", PinHash: "x", Role: "student"}
	userRepo.Create(user)

	dur := 100
	course := &model.Course{Title: "C", SubjectID: subjects["math"].ID}
	courseRepo.Create(course)
	db.Create(&model.CourseGrade{CourseID: course.ID, Grade: model.Grade("3")})

	// 20 episodes, each marked completed via ReportProgress. No badge exists
	// yet, so each of these EvaluateRules invocations has nothing to unlock for
	// the test badge (it's created below). Progress count persists = 20.
	var eps []*model.Episode
	for i := 1; i <= 20; i++ {
		ep := &model.Episode{CourseID: course.ID, SortOrder: i, DurationSeconds: &dur}
		episodeRepo.Create(ep)
		eps = append(eps, ep)
	}
	for i := 0; i < 20; i++ {
		ps.ReportProgress(user.ID, eps[i].ID, 95, 95)
	}

	// Now create a badge whose tiers the user's progress (20 completed) clears
	// several of at once: tiers at 3/10/30 with rewards 10/20/30. Progress 20
	// clears tier 0 (3) and tier 1 (10) but NOT tier 2 (30), so this is a
	// 2-tier jump from currentTier -1 → tier 1.
	badge := &model.Badge{
		Code: "jump_sum", Title: "跳级求和", IconName: "x",
		RuleType: "episode_completed_count",
		Tiers:    tiers(3, 10, 10, 20, 30, 30),
		IsSystem: false,
	}
	if err := badgeRepo.Create(badge); err != nil {
		t.Fatalf("create badge: %v", err)
	}

	// Single EvaluateRules — should advance from -1 to tier 1 in ONE call and
	// credit tier0 reward (10) + tier1 reward (20) = 30.
	if _, err := bs.EvaluateRules(user.ID); err != nil {
		t.Fatalf("EvaluateRules: %v", err)
	}

	ub, _ := badgeRepo.FindUserBadge(user.ID, badge.ID)
	if ub == nil {
		t.Fatal("badge should be unlocked after EvaluateRules")
	}
	if ub.Tier != 1 {
		t.Errorf("tier = %d, want 1 (progress 20 clears tiers 3 and 10, not 30)", ub.Tier)
	}

	// The points ledger entry for this jump must be the SUM (10 + 20 = 30),
	// not just the final tier's reward (20).
	ledger, _ := progressRepo.GetPointsLedger(user.ID, 100, 0)
	var jumpAmount int
	var jumpEntries int
	for _, item := range ledger {
		if item.ReasonType == "badge_unlocked" && strings.Contains(item.Description, "跳级求和") {
			jumpAmount += item.ChangeAmount
			jumpEntries++
		}
	}
	if jumpEntries != 1 {
		t.Errorf("badge_unlocked ledger entries for jump badge = %d, want 1 (one EvaluateRules call, one entry)", jumpEntries)
	}
	if jumpAmount != 30 {
		t.Errorf("multi-tier jump reward = %d, want 30 (tier0=10 + tier1=20, the SUM of crossed tiers)", jumpAmount)
	}
}
