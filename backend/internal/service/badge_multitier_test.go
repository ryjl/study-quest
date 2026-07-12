package service

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// TestMultiTierSkipUp verifies that when a user's progress jumps past multiple
// tiers at once, EvaluateRules advances them one tier per call (so each tier's
// reward is awarded separately, not all at once and not skipped).
func TestMultiTierSkipUp(t *testing.T) {
	db := testutil.NewDB(t)
	subjects := testutil.SeedSubjects(t, db)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	entertainmentRepo := repository.NewEntertainmentRepository(db)
	userRepo := repository.NewUserRepository(db)
	bs := NewBadgeService(db, badgeRepo, progressRepo)
	ps := NewProgressService(db, progressRepo, episodeRepo, bs, courseRepo, entertainmentRepo)
	user := &model.User{Nickname: "k", PinHash: "x", Role: "student"}
	userRepo.Create(user)

	// Create a custom badge with tiers 3/10/30 (episode_completed_count).
	badge := &model.Badge{
		Code: "test_skip", Title: "跳级测试", IconName: "x",
		RuleType: "episode_completed_count",
		Tiers:    tiers(3, 10, 10, 20, 30, 30),
		IsSystem: false,
	}
	if err := badgeRepo.Create(badge); err != nil {
		t.Fatalf("create badge: %v", err)
	}

	dur := 100
	course := &model.Course{Title: "C", SubjectID: subjects["math"].ID}
	courseRepo.Create(course)
	db.Create(&model.CourseGrade{CourseID: course.ID, Grade: model.Grade("3")})
	var eps []*model.Episode
	for i := 1; i <= 15; i++ {
		ep := &model.Episode{CourseID: course.ID, SortOrder: i, DurationSeconds: &dur}
		episodeRepo.Create(ep)
		eps = append(eps, ep)
	}

	// Complete 15 episodes in one go (progress jumps past tier 0 and tier 1).
	for i := 0; i < 15; i++ {
		ps.ReportProgress(user.ID, eps[i].ID, 95, 95)
	}

	// After 15 completions, the user should be at tier 2 (threshold 30 not met,
	// but tier 1 threshold 10 is met). Each EvaluateRules call advances at most
	// one tier, so after 15 calls (one per ReportProgress) we expect tier 1.
	ub, _ := badgeRepo.FindUserBadge(user.ID, badge.ID)
	if ub == nil {
		t.Fatal("badge should be unlocked after 15 completions")
	}
	if ub.Tier != 1 {
		t.Errorf("tier = %d, want 1 (thresholds 3,10 met; 30 not met with 15)", ub.Tier)
	}

	// Verify rewards: tier 0 (+10) + tier 1 (+20) = 30 from this badge.
	ledger, _ := progressRepo.GetPointsLedger(user.ID, 100, 0)
	var badgeReward int
	for _, item := range ledger {
		if item.ReasonType == "badge_unlocked" && strings.Contains(item.Description, "跳级测试") {
			badgeReward += item.ChangeAmount
		}
	}
	if badgeReward != 30 {
		t.Errorf("badge reward total = %d, want 30 (tier0=10 + tier1=20)", badgeReward)
	}
}

// TestUnlockBadgeTierAtomicNoDowngrade verifies the atomic UPDATE guard: when
// a user is already at tier 2, attempting to "unlock" tier 0 is a no-op
// (changed=false) and does NOT downgrade the tier.
func TestUnlockBadgeTierAtomicNoDowngrade(t *testing.T) {
	db := testutil.NewDB(t)
	repo := repository.NewBadgeRepository(db)

	// Create a badge + user, then manually insert a tier-2 UserBadge.
	badge := &model.Badge{Code: "nd", Title: "x", IconName: "x", RuleType: "watch_duration", Threshold: 1}
	repo.Create(badge)
	ub := &model.UserBadge{UserID: 1, BadgeID: badge.ID, Tier: 2}
	db.Create(ub)

	// Attempt to "unlock" tier 0 — must be rejected (no downgrade).
	changed, err := repo.UnlockBadgeTier(1, badge.ID, 0)
	if err != nil {
		t.Fatalf("UnlockBadgeTier: %v", err)
	}
	if changed {
		t.Error("changed=true, want false (tier 0 < existing tier 2, should be no-op)")
	}
	after, _ := repo.FindUserBadge(1, badge.ID)
	if after.Tier != 2 {
		t.Errorf("tier downgraded to %d, want 2 (must not downgrade)", after.Tier)
	}

	// Attempt to unlock tier 3 — should advance (3 > 2).
	changed, err = repo.UnlockBadgeTier(1, badge.ID, 3)
	if err != nil {
		t.Fatalf("UnlockBadgeTier: %v", err)
	}
	if !changed {
		t.Error("changed=false, want true (tier 3 > existing tier 2, should advance)")
	}
	after, _ = repo.FindUserBadge(1, badge.ID)
	if after.Tier != 3 {
		t.Errorf("tier = %d, want 3 after upgrade", after.Tier)
	}
}

// TestEvaluateRulesConcurrentIdempotent runs two EvaluateRules goroutines
// concurrently against the same user and verifies no double-award.
func TestEvaluateRulesConcurrentIdempotent(t *testing.T) {
	db := testutil.NewDB(t)
	subjects := testutil.SeedSubjects(t, db)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	entertainmentRepo := repository.NewEntertainmentRepository(db)
	userRepo := repository.NewUserRepository(db)
	bs := NewBadgeService(db, badgeRepo, progressRepo)
	ps := NewProgressService(db, progressRepo, episodeRepo, bs, courseRepo, entertainmentRepo)
	user := &model.User{Nickname: "k", PinHash: "x", Role: "student"}
	userRepo.Create(user)
	bs.SeedDefaultBadges()

	dur := 100
	course := &model.Course{Title: "C", SubjectID: subjects["math"].ID}
	courseRepo.Create(course)
	db.Create(&model.CourseGrade{CourseID: course.ID, Grade: model.Grade("3")})
	ep := &model.Episode{CourseID: course.ID, SortOrder: 1, DurationSeconds: &dur}
	episodeRepo.Create(ep)

	// Complete one episode so there's progress to evaluate.
	ps.ReportProgress(user.ID, ep.ID, 95, 95)

	// Now run EvaluateRules twice concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bs.EvaluateRules(user.ID)
		}()
	}
	wg.Wait()

	// Count badge_unlocked ledger entries — must not have duplicates per badge.
	ledger, _ := progressRepo.GetPointsLedger(user.ID, 100, 0)
	type badgeKey struct{ id uint; tier int }
	seen := map[badgeKey]int{}
	for _, item := range ledger {
		if item.ReasonType == "badge_unlocked" {
			// We can't easily get badge_id from the ledger description, but we
			// can count total badge_unlocked entries and check they're reasonable.
			_ = item
		}
	}
	// The key assertion: points must not be double-awarded. After completing
	// one episode (the course has only 1 episode, so it counts as a full
	// course completion):
	//   10 (system_watch) + 10 (time_master tier0) + 10 (course_master tier0)
	//   + 0 (first_blood, single-tier) = 30.
	// If concurrent EvaluateRules double-awarded any badge, points would be > 30.
	pt, _ := progressRepo.GetPoints(user.ID)
	if pt == nil {
		t.Fatal("no points row")
	}
	// Count badge_unlocked entries — each badge tier should appear at most once.
	ledger2, _ := progressRepo.GetPointsLedger(user.ID, 50, 0)
	unlockCount := 0
	for _, item := range ledger2 {
		t.Logf("ledger: reason=%s amount=%d desc=%s", item.ReasonType, item.ChangeAmount, item.Description)
		if item.ReasonType == "badge_unlocked" {
			unlockCount++
		}
	}
	if pt.CurrentPoints != 30 {
		t.Errorf("points = %d, want 30 (concurrent must not double-award)", pt.CurrentPoints)
	}
	// 3 badge unlocks (course_master, time_master, first_blood) — no duplicates.
	if unlockCount != 3 {
		t.Errorf("badge_unlocked ledger entries = %d, want 3 (no duplicates from concurrency)", unlockCount)
	}
	_ = seen
}

// TestSubjectCreateGeneratesBadge verifies that creating a subject
// auto-generates its multi-tier subject_count badge.
func TestSubjectCreateGeneratesBadge(t *testing.T) {
	db, svc := newSubjectSvc(t)
	badgeRepo := repository.NewBadgeRepository(db)

	subj, err := svc.Create("coding", "编程", "💻", "#000", 99)
	if err != nil {
		t.Fatalf("create subject: %v", err)
	}

	b, _ := badgeRepo.FindByCode(SubjectBadgeCode(subj.Key))
	if b == nil {
		t.Fatal("auto-generated subject badge not found")
	}
	if b.RuleType != "subject_count" || b.RuleTarget != "coding" {
		t.Errorf("badge rule = %s/%s, want subject_count/coding", b.RuleType, b.RuleTarget)
	}
	if b.Tiers == "" {
		t.Error("subject badge should have multi-tier Tiers")
	}
}

// TestSubjectDeleteCleansBadge verifies that deleting a user-created subject
// also removes its auto-generated badge.
func TestSubjectDeleteCleansBadge(t *testing.T) {
	db, svc := newSubjectSvc(t)
	badgeRepo := repository.NewBadgeRepository(db)

	subj, _ := svc.Create("typing", "打字", "⌨️", "#000", 99)
	code := SubjectBadgeCode(subj.Key)
	if b, _ := badgeRepo.FindByCode(code); b == nil {
		t.Fatal("badge should exist after create")
	}

	if err := svc.Delete(subj.ID); err != nil {
		t.Fatalf("delete subject: %v", err)
	}
	if b, _ := badgeRepo.FindByCode(code); b != nil {
		t.Error("badge should be removed after subject delete")
	}
}

// TestUserBadgeStatusesFields verifies UserBadgeStatuses returns correct
// tier/tierCount/progress/nextTier for a multi-tier badge at various states.
func TestUserBadgeStatusesFields(t *testing.T) {
	db := testutil.NewDB(t)
	subjects := testutil.SeedSubjects(t, db)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	entertainmentRepo := repository.NewEntertainmentRepository(db)
	userRepo := repository.NewUserRepository(db)
	bs := NewBadgeService(db, badgeRepo, progressRepo)
	ps := NewProgressService(db, progressRepo, episodeRepo, bs, courseRepo, entertainmentRepo)
	user := &model.User{Nickname: "k", PinHash: "x", Role: "student"}
	userRepo.Create(user)

	// Custom multi-tier badge: thresholds 3/10/30.
	badge := &model.Badge{
		Code: "status_test", Title: "状态测试", IconName: "x",
		RuleType: "episode_completed_count",
		Tiers:    tiers(3, 10, 10, 20, 30, 30),
	}
	badgeRepo.Create(badge)

	dur := 100
	course := &model.Course{Title: "C", SubjectID: subjects["math"].ID}
	courseRepo.Create(course)
	db.Create(&model.CourseGrade{CourseID: course.ID, Grade: model.Grade("3")})
	var eps []*model.Episode
	for i := 1; i <= 5; i++ {
		ep := &model.Episode{CourseID: course.ID, SortOrder: i, DurationSeconds: &dur}
		episodeRepo.Create(ep)
		eps = append(eps, ep)
	}

	// Complete 2 episodes — below tier 0 (threshold 3).
	for i := 0; i < 2; i++ {
		ps.ReportProgress(user.ID, eps[i].ID, 95, 95)
	}
	statuses, _ := bs.UserBadgeStatuses(user.ID)
	var st *BadgeStatus
	for i := range statuses {
		if statuses[i].Code == "status_test" {
			st = &statuses[i]
		}
	}
	if st == nil {
		t.Fatal("status_test not found")
	}
	if st.Unlocked {
		t.Error("should not be unlocked with 2 < 3")
	}
	if st.Tier != -1 {
		t.Errorf("tier = %d, want -1 (not unlocked)", st.Tier)
	}
	if st.TierCount != 3 {
		t.Errorf("tierCount = %d, want 3", st.TierCount)
	}
	if st.Progress != 2 {
		t.Errorf("progress = %d, want 2", st.Progress)
	}
	if st.NextTier != 3 {
		t.Errorf("nextTier = %d, want 3 (first tier threshold)", st.NextTier)
	}

	// Complete 1 more (3 total) — now at tier 0.
	ps.ReportProgress(user.ID, eps[2].ID, 95, 95)
	statuses, _ = bs.UserBadgeStatuses(user.ID)
	for i := range statuses {
		if statuses[i].Code == "status_test" {
			st = &statuses[i]
		}
	}
	if !st.Unlocked || st.Tier != 0 {
		t.Errorf("after 3 completions: unlocked=%v tier=%d, want true/0", st.Unlocked, st.Tier)
	}
	if st.NextTier != 10 {
		t.Errorf("nextTier = %d, want 10 (second tier threshold)", st.NextTier)
	}
}

// TestEvalMultiTierSortsUnsorted verifies evalMultiTier sorts non-ascending
// tiers before evaluating, so unsorted admin input doesn't break the loop.
func TestEvalMultiTierSortsUnsorted(t *testing.T) {
	db := testutil.NewDB(t)
	subjects := testutil.SeedSubjects(t, db)
	badgeRepo := repository.NewBadgeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	entertainmentRepo := repository.NewEntertainmentRepository(db)
	userRepo := repository.NewUserRepository(db)
	bs := NewBadgeService(db, badgeRepo, progressRepo)
	ps := NewProgressService(db, progressRepo, episodeRepo, bs, courseRepo, entertainmentRepo)
	user := &model.User{Nickname: "k", PinHash: "x", Role: "student"}
	userRepo.Create(user)

	// Badge with DELIBERATELY unsorted tiers: 30, 3, 10.
	unsorted, _ := json.Marshal([]model.TierDef{{T: 30, R: 30}, {T: 3, R: 10}, {T: 10, R: 20}})
	badge := &model.Badge{
		Code: "unsorted", Title: "乱序", IconName: "x",
		RuleType: "episode_completed_count",
		Tiers:    string(unsorted),
	}
	badgeRepo.Create(badge)

	dur := 100
	course := &model.Course{Title: "C", SubjectID: subjects["math"].ID}
	courseRepo.Create(course)
	db.Create(&model.CourseGrade{CourseID: course.ID, Grade: model.Grade("3")})
	var eps []*model.Episode
	for i := 1; i <= 5; i++ {
		ep := &model.Episode{CourseID: course.ID, SortOrder: i, DurationSeconds: &dur}
		episodeRepo.Create(ep)
		eps = append(eps, ep)
	}

	// Complete 5 episodes. After sorting (3,10,30), 5 >= 3 → tier 0, 5 < 10 → stop.
	for i := 0; i < 5; i++ {
		ps.ReportProgress(user.ID, eps[i].ID, 95, 95)
	}
	ub, _ := badgeRepo.FindUserBadge(user.ID, badge.ID)
	if ub == nil {
		t.Fatal("badge should be unlocked at tier 0 after 5 completions (>=3)")
	}
	if ub.Tier != 0 {
		t.Errorf("tier = %d, want 0 (5 >= sorted tier0=3, but < tier1=10)", ub.Tier)
	}

	// Also verify UserBadgeStatuses returns sorted nextTier.
	statuses, _ := bs.UserBadgeStatuses(user.ID)
	for i := range statuses {
		if statuses[i].Code == "unsorted" {
			if statuses[i].NextTier != 10 {
				t.Errorf("nextTier = %d, want 10 (sorted second tier)", statuses[i].NextTier)
			}
		}
	}
}
