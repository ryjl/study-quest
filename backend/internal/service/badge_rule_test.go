package service

import (
	"encoding/json"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// These tests cover the composite-rule evaluator (evalComposite/evalNode/
// evalLeaf) end-to-end through EvaluateRules, the same path production hits.
//
// They deliberately use only DETERMINISTIC rule types — watch_duration and
// points_earned — whose underlying stats read from user_progresses /
// user_points, which we can seed freely. consecutive_days compares against
// time.Now() and is therefore not unit-testable without a clock seam, so it's
// exercised only via the integration suite.

// setupRuleTestDB builds a fresh in-memory DB plus a student user.
func setupRuleTestDB(t *testing.T) (*gorm.DB, *model.User) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	u := &model.User{Nickname: "rule-kid", PinHash: "x", Role: "student"}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return db, u
}

// makeRuleJSON serializes a composite rule tree (logic + leaves).
func makeRuleJSON(t *testing.T, logic string, leaves ...model.CompositeRule) string {
	t.Helper()
	b, err := json.Marshal(model.CompositeRule{Logic: logic, SubRules: leaves})
	if err != nil {
		t.Fatalf("marshal rule: %v", err)
	}
	return string(b)
}

// mustMarshalTree serializes an arbitrary tree (for nested-group tests).
func mustMarshalTree(t *testing.T, tree model.CompositeRule) string {
	t.Helper()
	b, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("marshal tree: %v", err)
	}
	return string(b)
}

// makeCompositeBadge inserts a badge evaluated purely via RuleJSON.
func makeCompositeBadge(t *testing.T, repo repository.BadgeRepository, code, ruleJSON string) model.Badge {
	t.Helper()
	b := &model.Badge{Code: code, Title: code, IconName: "x", RuleType: "composite", RuleJSON: ruleJSON, Threshold: 0}
	if err := repo.Create(b); err != nil {
		t.Fatalf("create badge %s: %v", code, err)
	}
	return *b
}

// seedWatchSeconds writes a progress row carrying N seconds of watch time so
// watch_duration rules can read it.
func seedWatchSeconds(t *testing.T, db *gorm.DB, userID uint, seconds int) {
	t.Helper()
	p := model.UserProgress{UserID: userID, EpisodeID: 1, WatchSeconds: seconds, LastPositionSeconds: seconds}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("seed progress: %v", err)
	}
}

// seedUserPoints writes a user_points row with the given total-earned value.
func seedUserPoints(t *testing.T, db *gorm.DB, userID uint, totalEarned int) {
	t.Helper()
	p := &model.UserPoint{UserID: userID, CurrentPoints: totalEarned, TotalEarnedPoints: totalEarned}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed points: %v", err)
	}
}

// TestCompositeRuleAND exercises an AND group: both leaves must pass for the
// badge to unlock; satisfying only one does NOT unlock.
func TestCompositeRuleAND(t *testing.T) {
	db, user := setupRuleTestDB(t)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	svc := NewBadgeService(db, badgeRepo, progressRepo)

	// Rule: watch_duration >= 5 AND points_earned >= 100.
	ruleJSON := makeRuleJSON(t, "and",
		model.CompositeRule{Type: "watch_duration", Threshold: 5},
		model.CompositeRule{Type: "points_earned", Threshold: 100},
	)
	makeCompositeBadge(t, badgeRepo, "and_badge", ruleJSON)

	// Satisfy only the watch arm (5 min = 300s). points arm unsatisfied.
	seedWatchSeconds(t, db, user.ID, 300)

	unlocked, err := svc.EvaluateRules(user.ID)
	if err != nil {
		t.Fatalf("EvaluateRules: %v", err)
	}
	if hasCode(unlocked, "and_badge") {
		t.Fatalf("AND with one arm unsatisfied: expected NO unlock, got %v", codesOf(unlocked))
	}

	// Now also satisfy points → both arms pass → unlock.
	seedUserPoints(t, db, user.ID, 100)
	unlocked, err = svc.EvaluateRules(user.ID)
	if err != nil {
		t.Fatalf("EvaluateRules (both arms): %v", err)
	}
	if !hasCode(unlocked, "and_badge") {
		t.Fatalf("AND with both arms satisfied: expected and_badge to unlock, got %v", codesOf(unlocked))
	}
}

// TestCompositeRuleOR exercises an OR group: any single passing leaf unlocks.
func TestCompositeRuleOR(t *testing.T) {
	db, user := setupRuleTestDB(t)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	svc := NewBadgeService(db, badgeRepo, progressRepo)

	ruleJSON := makeRuleJSON(t, "or",
		model.CompositeRule{Type: "watch_duration", Threshold: 60},
		model.CompositeRule{Type: "points_earned", Threshold: 1000},
	)
	makeCompositeBadge(t, badgeRepo, "or_badge", ruleJSON)

	// Satisfy only the watch arm. OR → unlock even though points arm fails.
	seedWatchSeconds(t, db, user.ID, 3600 /*60 min*/)

	unlocked, err := svc.EvaluateRules(user.ID)
	if err != nil {
		t.Fatalf("EvaluateRules: %v", err)
	}
	if !hasCode(unlocked, "or_badge") {
		t.Fatalf("OR with one arm satisfied: expected or_badge to unlock, got %v", codesOf(unlocked))
	}
}

// TestCompositeRuleNested verifies the recursive evaluator handles a nested
// tree: AND( OR(a, b), c ). Unlocks only when the OR sub-group passes AND c.
func TestCompositeRuleNested(t *testing.T) {
	db, user := setupRuleTestDB(t)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	svc := NewBadgeService(db, badgeRepo, progressRepo)

	// AND( OR(watch>=30, points>=9999), watch>=1 )
	tree := model.CompositeRule{
		Logic: "and",
		SubRules: []model.CompositeRule{
			{Logic: "or", SubRules: []model.CompositeRule{
				{Type: "watch_duration", Threshold: 30},
				{Type: "points_earned", Threshold: 9999},
			}},
			{Type: "watch_duration", Threshold: 1},
		},
	}
	makeCompositeBadge(t, badgeRepo, "nested_badge", mustMarshalTree(t, tree))

	// Satisfy: 30 min watch (passes the OR's first arm) + >=1 min (passes the
	// trailing leaf). Both arms of the top AND pass → unlock.
	seedWatchSeconds(t, db, user.ID, 1800 /*30 min*/)

	unlocked, err := svc.EvaluateRules(user.ID)
	if err != nil {
		t.Fatalf("EvaluateRules: %v", err)
	}
	if !hasCode(unlocked, "nested_badge") {
		t.Fatalf("nested AND(OR(..), watch>=1): expected nested_badge to unlock, got %v", codesOf(unlocked))
	}
}

// TestCompositeRuleBadJSON locks in fail-closed behavior: a corrupt RuleJSON
// must NEVER unlock the badge (returns false rather than panicking).
func TestCompositeRuleBadJSON(t *testing.T) {
	db, user := setupRuleTestDB(t)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	svc := NewBadgeService(db, badgeRepo, progressRepo)

	makeCompositeBadge(t, badgeRepo, "broken_badge", "{not valid json")
	seedWatchSeconds(t, db, user.ID, 99999)

	unlocked, err := svc.EvaluateRules(user.ID)
	if err != nil {
		t.Fatalf("EvaluateRules on bad JSON should not error: %v", err)
	}
	if hasCode(unlocked, "broken_badge") {
		t.Fatalf("badge with corrupt RuleJSON must not unlock; got %v", codesOf(unlocked))
	}
}

// TestCompositeRuleAlreadyUnlocked verifies idempotency: a previously-unlocked
// badge is not re-awarded on subsequent evaluations.
func TestCompositeRuleAlreadyUnlocked(t *testing.T) {
	db, user := setupRuleTestDB(t)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	svc := NewBadgeService(db, badgeRepo, progressRepo)

	b := makeCompositeBadge(t, badgeRepo, "idem_badge", makeRuleJSON(t, "and",
		model.CompositeRule{Type: "watch_duration", Threshold: 1},
	))
	seedWatchSeconds(t, db, user.ID, 60 /*1 min*/)

	first, err := svc.EvaluateRules(user.ID)
	if err != nil {
		t.Fatalf("first eval: %v", err)
	}
	if len(first) != 1 || first[0].ID != b.ID {
		t.Fatalf("first eval expected only idem_badge, got %v", codesOf(first))
	}

	second, err := svc.EvaluateRules(user.ID)
	if err != nil {
		t.Fatalf("second eval: %v", err)
	}
	if hasCode(second, "idem_badge") {
		t.Fatalf("already-unlocked badge must not be re-awarded; got %v", codesOf(second))
	}
}

// --- helpers ---

func hasCode(bs []model.Badge, code string) bool {
	for _, b := range bs {
		if b.Code == code {
			return true
		}
	}
	return false
}

func codesOf(bs []model.Badge) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Code
	}
	return out
}
