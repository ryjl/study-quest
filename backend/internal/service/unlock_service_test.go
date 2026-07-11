package service

import (
	"studyquest/backend/internal/testutil"
	"sort"
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"testing"
	"time"

	"gorm.io/gorm"
)

// setupUnlockServiceTestDB mirrors the repo tests' in-memory DB setup but
// lives in the service package (tests are black-box against the service).
func setupUnlockServiceTestDB(t *testing.T) *gorm.DB {
	return testutil.NewDB(t)

}

// seedCourseWithEpisodes builds a course with `n` episodes (sort_order 1..n)
// and a user granted access at `grantedAt`. Returns courseID, userID and the
// ordered episode ids.
func seedCourseWithEpisodes(t *testing.T, db *gorm.DB, n int, grantedAt time.Time) (courseID, userID uint, epIDs []uint) {
	t.Helper()
	subj := model.Subject{Key: "math", Label: "数学"}
	if err := db.Create(&subj).Error; err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	c := model.Course{Title: "C", Grade: "3", SubjectID: subj.ID}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("seed course: %v", err)
	}
	for i := 1; i <= n; i++ {
		ep := model.Episode{
			CourseID: c.ID, SortOrder: i, Title: "ep" + itoa(i),
			VideoRelativePath: "/p/" + itoa(i),
		}
		if err := db.Create(&ep).Error; err != nil {
			t.Fatalf("seed episode %d: %v", i, err)
		}
		epIDs = append(epIDs, ep.ID)
	}
	u := model.User{Nickname: "u", PinHash: "x"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// GrantedAt is set explicitly so time-based strategies are deterministic.
	a := model.UserCourseAccess{UserID: u.ID, CourseID: c.ID, GrantedAt: grantedAt}
	if err := db.Create(&a).Error; err != nil {
		t.Fatalf("seed access: %v", err)
	}
	return c.ID, u.ID, epIDs
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func newUnlockService(t *testing.T) (UnlockService, *gorm.DB) {
	t.Helper()
	db := setupUnlockServiceTestDB(t)
	ur := repository.NewUnlockRepository(db)
	er := repository.NewEpisodeRepository(db)
	return NewUnlockService(ur, er), db
}

// idsEqual compares two uint slices ignoring order (since visibility is a set).
func idsEqual(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}
	sa := append([]uint(nil), a...)
	sb := append([]uint(nil), b...)
	sort.Slice(sa, func(i, j int) bool { return sa[i] < sa[j] })
	sort.Slice(sb, func(i, j int) bool { return sb[i] < sb[j] })
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

func TestResolveVisibleAllOpen(t *testing.T) {
	svc, db := newUnlockService(t)
	courseID, userID, epIDs := seedCourseWithEpisodes(t, db, 4, time.Now())

	vis, err := svc.ResolveVisibleEpisodes(userID, courseID)
	if err != nil {
		t.Fatalf("ResolveVisibleEpisodes: %v", err)
	}
	if vis.Total != 4 || vis.UnlockedN != 4 {
		t.Errorf("all_open: total=%d n=%d want 4/4", vis.Total, vis.UnlockedN)
	}
	if !idsEqual(vis.VisibleIDs, epIDs) {
		t.Errorf("all_open visible=%v want %v", vis.VisibleIDs, epIDs)
	}
}

func TestResolveVisibleManualAndBump(t *testing.T) {
	svc, db := newUnlockService(t)
	courseID, userID, epIDs := seedCourseWithEpisodes(t, db, 5, time.Now())

	// Template = manual → only ep1 visible initially.
	if _, err := svc.SaveTemplate(courseID, model.StrategyManual, 0, nil); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}
	vis, _ := svc.ResolveVisibleEpisodes(userID, courseID)
	if !idsEqual(vis.VisibleIDs, epIDs[:1]) {
		t.Errorf("manual initial visible=%v want %v", vis.VisibleIDs, epIDs[:1])
	}

	// Manual +1 → ep1, ep2.
	if err := svc.IncrementManualUnlock(userID, courseID); err != nil {
		t.Fatalf("IncrementManualUnlock: %v", err)
	}
	vis, _ = svc.ResolveVisibleEpisodes(userID, courseID)
	if !idsEqual(vis.VisibleIDs, epIDs[:2]) {
		t.Errorf("after manual +1 visible=%v want %v", vis.VisibleIDs, epIDs[:2])
	}
}

func TestResolveVisibleSelectedCherryPick(t *testing.T) {
	svc, db := newUnlockService(t)
	courseID, userID, epIDs := seedCourseWithEpisodes(t, db, 6, time.Now())

	// Override = selected, allowlist = [ep3, ep5] (cherry-pick, out of order).
	if _, err := svc.SaveOverride(userID, courseID, model.StrategySelected, 0, nil, []uint{epIDs[2], epIDs[4]}); err != nil {
		t.Fatalf("SaveOverride: %v", err)
	}
	vis, err := svc.ResolveVisibleEpisodes(userID, courseID)
	if err != nil {
		t.Fatalf("ResolveVisibleEpisodes: %v", err)
	}
	if vis.UnlockedN != 0 {
		t.Errorf("selected UnlockedN=%d want 0", vis.UnlockedN)
	}
	want := []uint{epIDs[2], epIDs[4]}
	if !idsEqual(vis.VisibleIDs, want) {
		t.Errorf("selected visible=%v want %v", vis.VisibleIDs, want)
	}
	// Visibility MUST come back in SortOrder, not insertion order.
	if len(vis.VisibleIDs) == 2 && vis.VisibleIDs[0] > vis.VisibleIDs[1] {
		t.Errorf("visible not sorted by rank: %v", vis.VisibleIDs)
	}
}

func TestResolveVisibleWaterUnionAllowlist(t *testing.T) {
	svc, db := newUnlockService(t)
	courseID, userID, epIDs := seedCourseWithEpisodes(t, db, 6, time.Now())

	// manual template (N=1 → ep1) PLUS allowlist [ep4] → union {ep1, ep4}.
	if _, err := svc.SaveTemplate(courseID, model.StrategyManual, 0, nil); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}
	if err := svc.SetAllowedEpisodes(userID, courseID, []uint{epIDs[3]}); err != nil {
		t.Fatalf("SetAllowedEpisodes: %v", err)
	}
	vis, _ := svc.ResolveVisibleEpisodes(userID, courseID)
	want := []uint{epIDs[0], epIDs[3]}
	if !idsEqual(vis.VisibleIDs, want) {
		t.Errorf("union visible=%v want %v", vis.VisibleIDs, want)
	}
}

func TestResolveVisibleAllowlistAutoPrunesDeleted(t *testing.T) {
	svc, db := newUnlockService(t)
	courseID, userID, epIDs := seedCourseWithEpisodes(t, db, 5, time.Now())

	// selected + allowlist [ep1, ep3]. Then delete ep1 from the course.
	if _, err := svc.SaveOverride(userID, courseID, model.StrategySelected, 0, nil, []uint{epIDs[0], epIDs[2]}); err != nil {
		t.Fatalf("SaveOverride: %v", err)
	}
	if err := db.Delete(&model.Episode{}, epIDs[0]).Error; err != nil {
		t.Fatalf("delete episode: %v", err)
	}
	vis, _ := svc.ResolveVisibleEpisodes(userID, courseID)
	// ep1 pruned; only ep3 remains. No error, no dangling id.
	want := []uint{epIDs[2]}
	if !idsEqual(vis.VisibleIDs, want) {
		t.Errorf("after delete visible=%v want %v", vis.VisibleIDs, want)
	}
	if vis.Total != 4 {
		t.Errorf("total after delete=%d want 4", vis.Total)
	}
}

func TestResolveVisibleWaterShrinksOnDeleteInRank(t *testing.T) {
	svc, db := newUnlockService(t)
	courseID, userID, epIDs := seedCourseWithEpisodes(t, db, 5, time.Now())

	// manual +2 → N=3 (ep1,ep2,ep3). Delete ep2 (inside rank). Rank now
	// renumbers; N stays 3 but covers [ep1, ep3, ep4] (the first 3 by SortOrder
	// after the delete).
	if _, err := svc.SaveTemplate(courseID, model.StrategyManual, 0, nil); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}
	svc.IncrementManualUnlock(userID, courseID)
	svc.IncrementManualUnlock(userID, courseID) // N=3
	if err := db.Delete(&model.Episode{}, epIDs[1]).Error; err != nil {
		t.Fatalf("delete ep2: %v", err)
	}
	vis, _ := svc.ResolveVisibleEpisodes(userID, courseID)
	// Remaining episodes in SortOrder: ep1, ep3, ep4, ep5. First 3 → ep1,ep3,ep4.
	want := []uint{epIDs[0], epIDs[2], epIDs[3]}
	if !idsEqual(vis.VisibleIDs, want) {
		t.Errorf("after in-rank delete visible=%v want %v", vis.VisibleIDs, want)
	}
}

func TestIsEpisodeVisibleGate(t *testing.T) {
	svc, db := newUnlockService(t)
	courseID, userID, epIDs := seedCourseWithEpisodes(t, db, 4, time.Now())

	// manual → only ep1 visible. ep2 must be gated false.
	if _, err := svc.SaveTemplate(courseID, model.StrategyManual, 0, nil); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}
	ok, err := svc.IsEpisodeVisible(userID, epIDs[0])
	if err != nil || !ok {
		t.Errorf("ep1 visible=%v err=%v want true", ok, err)
	}
	ok, err = svc.IsEpisodeVisible(userID, epIDs[1])
	if err != nil || ok {
		t.Errorf("ep2 visible=%v err=%v want false (locked)", ok, err)
	}
}

func TestNextUnlockAtWeekly(t *testing.T) {
	// Pin zone to CST. grantedAt Sunday 08:00; weekly Sun 19:00. The next
	// unlock "now=Sunday 10:00" should be today 19:00.
	prev := appclock.Zone()
	appclock.SetZone(time.FixedZone("CST", 8*3600))
	defer appclock.SetZone(prev)

	cst := func(s string) time.Time {
		tt, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.FixedZone("CST", 8*3600))
		if err != nil {
			t.Fatal(err)
		}
		return tt
	}
	granted := cst("2026-03-01 08:00:00") // Sunday
	now := cst("2026-03-01 10:00:00")
	wt := []model.WeeklyTime{{0, 19, 0}}
	next := nextUnlockAt(model.StrategyWeekly, 0, wt, granted, now, 10, 1)
	if next == nil {
		t.Fatal("expected next unlock, got nil")
	}
	want := cst("2026-03-01 19:00:00")
	if !next.Equal(want) {
		t.Errorf("next=%v want %v", *next, want)
	}
}
