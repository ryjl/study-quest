package repository

import (
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/model"
	"testing"
	"time"

	"gorm.io/gorm"
)

// These tests lock in the business-timezone semantics for consecutive_days —
// the rule that previously depended on SQLite's 'localtime'
// (DB process zone) disagreeing with Go's time.Now() (server zone), which
// silently zeroed streaks in containers.
//
// Strategy: inject a FIXED business zone (Asia/Shanghai, +08:00) and a FIXED
// "now" via appclock.SetNow/SetZone, then seed PointsLedger / UserProgress
// rows with explicit UTC timestamps and assert the calendar-day math comes
// out right regardless of the host running the test.

// useFixedShanghaiClock pins the business zone to Asia/Shanghai and "now" to
// the given instant, returning a restore func. Every test MUST restore, since
// appclock is package-global state.
func useFixedShanghaiClock(t *testing.T, now time.Time) func() {
	t.Helper()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		shanghai = time.FixedZone("CST", 8*3600)
	}
	return useFixedClock(t, shanghai, now)
}

// useFixedClock is the zone-agnostic core: pin the business zone to `loc` and
// "now" to the given instant. Used by the negative-offset and half-hour-offset
// tests to prove the SQL offset trick works beyond UTC+8.
func useFixedClock(t *testing.T, loc *time.Location, now time.Time) func() {
	t.Helper()
	prevZone := appclock.Zone()
	appclock.SetZone(loc)
	appclock.SetNow(func() time.Time { return now.In(loc) })
	return func() {
		appclock.SetZone(prevZone)
		appclock.SetNow(time.Now)
	}
}

// shanghaiUTC builds a UTC instant from a Beijing wall-clock time string.
// Lets tests think in Beijing time while storing UTC (the real storage format).
func shanghaiUTC(wallClock string) time.Time {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	t, _ := time.ParseInLocation("2006-01-02 15:04:05", wallClock, loc)
	return t.UTC()
}

// wallToUTC parses a wall-clock string as if it were in `loc`, returns the
// equivalent UTC instant. Generic zone-aware version of shanghaiUTC.
func wallToUTC(loc *time.Location, wallClock string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04:05", wallClock, loc)
	return t.UTC()
}

// seedLedger inserts a PointsLedger row at the given UTC instant.
func seedLedger(t *testing.T, db *gorm.DB, userID uint, at time.Time) {
	t.Helper()
	if err := db.Create(&model.PointsLedger{
		UserID: userID, ChangeAmount: 1, CreatedAt: at,
	}).Error; err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
}

// --- consecutive_days ---

// TestConsecutiveDaysStraightStreak seeds activity on each of the last 3
// Beijing-calendar days and expects streak=3.
func TestConsecutiveDaysStraightStreak(t *testing.T) {
	// "Now" = 2026-03-10 20:00 Beijing.
	restore := useFixedShanghaiClock(t, shanghaiUTC("2026-03-10 20:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 1

	// Activity on Beijing 03-08, 03-09, 03-10 (today).
	seedLedger(t, db, uid, shanghaiUTC("2026-03-08 10:00:00"))
	seedLedger(t, db, uid, shanghaiUTC("2026-03-09 10:00:00"))
	seedLedger(t, db, uid, shanghaiUTC("2026-03-10 10:00:00"))

	got, err := repo.GetConsecutiveActiveDays(uid)
	if err != nil {
		t.Fatalf("GetConsecutiveActiveDays: %v", err)
	}
	if got != 3 {
		t.Fatalf("streak = %d, want 3", got)
	}
}

// TestConsecutiveDaysGapResets seeds 03-10 (today) and 03-08 (gap on 03-09),
// expecting the streak to reset to 1 (only today counts once the chain breaks).
func TestConsecutiveDaysGapResets(t *testing.T) {
	restore := useFixedShanghaiClock(t, shanghaiUTC("2026-03-10 20:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 2

	seedLedger(t, db, uid, shanghaiUTC("2026-03-10 10:00:00")) // today
	seedLedger(t, db, uid, shanghaiUTC("2026-03-08 10:00:00")) // 2 days ago (gap)

	got, _ := repo.GetConsecutiveActiveDays(uid)
	if got != 1 {
		t.Fatalf("streak with a gap = %d, want 1 (only today)", got)
	}
}

// TestConsecutiveDaysStartedYesterday seeds today + yesterday and expects 2.
func TestConsecutiveDaysStartedYesterday(t *testing.T) {
	restore := useFixedShanghaiClock(t, shanghaiUTC("2026-03-10 20:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 3

	seedLedger(t, db, uid, shanghaiUTC("2026-03-09 23:00:00")) // yesterday Beijing
	seedLedger(t, db, uid, shanghaiUTC("2026-03-10 08:00:00")) // today Beijing

	got, _ := repo.GetConsecutiveActiveDays(uid)
	if got != 2 {
		t.Fatalf("yesterday+today streak = %d, want 2", got)
	}
}

// TestConsecutiveDaysAcrossMidnight verifies the Beijing day boundary is
// honored: 2026-03-09 23:30 and 2026-03-10 00:30 are 1 hour apart in UTC but
// belong to DIFFERENT Beijing calendar days, so both count toward a 2-day
// streak (this is the bug 'localtime' caused — UTC bucketing merged or split
// them wrongly).
func TestConsecutiveDaysAcrossMidnight(t *testing.T) {
	restore := useFixedShanghaiClock(t, shanghaiUTC("2026-03-10 12:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 4

	// 23:30 Beijing on the 9th → just before midnight.
	seedLedger(t, db, uid, shanghaiUTC("2026-03-09 23:30:00"))
	// 00:30 Beijing on the 10th → just after midnight.
	seedLedger(t, db, uid, shanghaiUTC("2026-03-10 00:30:00"))

	got, _ := repo.GetConsecutiveActiveDays(uid)
	if got != 2 {
		t.Fatalf("across-midnight streak = %d, want 2 (two distinct Beijing days)", got)
	}
}

// TestConsecutiveDaysNoActivity returns 0 when the user has no ledger rows.
func TestConsecutiveDaysNoActivity(t *testing.T) {
	restore := useFixedShanghaiClock(t, shanghaiUTC("2026-03-10 12:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)

	got, _ := repo.GetConsecutiveActiveDays(99)
	if got != 0 {
		t.Fatalf("no activity streak = %d, want 0", got)
	}
}

// --- consecutive_days additional edge cases ---

// TestConsecutiveDaysOnlyYesterday verifies the yesterday branch: if the last
// activity was yesterday and there's nothing today, the streak should still
// count backward from yesterday (e.g. yesterday + day-before → 2). This guards
// the dates[0] == yesterdayStr acceptance arm.
func TestConsecutiveDaysOnlyYesterday(t *testing.T) {
	// "Now" = 2026-03-10 12:00 Beijing. No activity today; last was 03-09 + 03-08.
	restore := useFixedShanghaiClock(t, shanghaiUTC("2026-03-10 12:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 11

	seedLedger(t, db, uid, shanghaiUTC("2026-03-09 10:00:00")) // yesterday
	seedLedger(t, db, uid, shanghaiUTC("2026-03-08 10:00:00")) // day before

	got, _ := repo.GetConsecutiveActiveDays(uid)
	if got != 2 {
		t.Fatalf("yesterday-only streak = %d, want 2 (yesterday + day-before)", got)
	}
}

// TestConsecutiveDaysSameDayDedupes verifies multiple activities on the SAME
// Beijing day collapse to one day in the streak (DISTINCT in SQL). Without
// dedup, 5 rows on one day would be miscounted.
func TestConsecutiveDaysSameDayDedupes(t *testing.T) {
	restore := useFixedShanghaiClock(t, shanghaiUTC("2026-03-10 12:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 12

	// Three rows today (different times), one yesterday.
	seedLedger(t, db, uid, shanghaiUTC("2026-03-10 08:00:00"))
	seedLedger(t, db, uid, shanghaiUTC("2026-03-10 12:00:00"))
	seedLedger(t, db, uid, shanghaiUTC("2026-03-10 20:00:00"))
	seedLedger(t, db, uid, shanghaiUTC("2026-03-09 09:00:00"))

	got, _ := repo.GetConsecutiveActiveDays(uid)
	if got != 2 {
		t.Fatalf("deduped streak = %d, want 2 (today's 3 rows count as 1)", got)
	}
}

// TestConsecutiveDaysOldActivityReturnsZero verifies that activity older than
// yesterday yields streak=0 (the dates[0] != today && != yesterday guard).
func TestConsecutiveDaysOldActivityReturnsZero(t *testing.T) {
	restore := useFixedShanghaiClock(t, shanghaiUTC("2026-03-10 12:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 13

	// Last activity 3 days ago — no recent anchor.
	seedLedger(t, db, uid, shanghaiUTC("2026-03-07 10:00:00"))
	seedLedger(t, db, uid, shanghaiUTC("2026-03-06 10:00:00"))

	got, _ := repo.GetConsecutiveActiveDays(uid)
	if got != 0 {
		t.Fatalf("stale activity streak = %d, want 0 (no today/yesterday anchor)", got)
	}
}

// --- sqliteOffsetModifier unit tests (the helper that builds '+08:00' etc.) ---

func TestSqliteOffsetModifier(t *testing.T) {
	cases := []struct {
		min  int
		want string
	}{
		{480, "+08:00"},   // Shanghai
		{0, "+00:00"},     // UTC
		{-300, "-05:00"},  // US Eastern
		{-330, "-05:30"},  // India (half-hour offset)
		{210, "+03:30"},   // Iran (half-hour, positive)
	}
	for _, c := range cases {
		if got := sqliteOffsetModifier(c.min); got != c.want {
			t.Errorf("sqliteOffsetModifier(%d) = %q, want %q", c.min, got, c.want)
		}
	}
}

// --- NEGATIVE-offset timezone (US Eastern, UTC-5) ---
//
// These prove the SQL offset trick works for zones WEST of UTC, not just +8.
// A negative offset produces a '-05:00' SQLite modifier; if SQLite mishandled
// the sign (it's historically picky about modifier format), the day bucketing
// and hour extraction would both come out wrong. These would catch that.

// TestNegativeOffsetStreak runs the full consecutive-days logic under a fixed
// negative offset (UTC-5, like US Eastern in winter / non-DST). Activity on
// three consecutive calendar days must yield streak=3.
//
// NOTE on DST: this test uses a FIXED offset zone (time.FixedZone), NOT a
// DST-observing location like America/New_York. The reason is deliberate and
// documents a real, accepted limitation: our SQL applies a SINGLE offset
// (computed from appclock.Now()) to every row, which is exactly right for
// DST-free zones (China, and any FixedZone) but would be off by ~1h on
// historical rows that fell in the other DST half of a DST-observing zone.
// Since the product targets Chinese students (no DST), this is fine; the test
// pins the behavior for the case we actually ship.
func TestNegativeOffsetStreak(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	restore := useFixedClock(t, est, wallToUTC(est, "2026-03-10 20:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 30

	seedLedger(t, db, uid, wallToUTC(est, "2026-03-08 10:00:00"))
	seedLedger(t, db, uid, wallToUTC(est, "2026-03-09 10:00:00"))
	seedLedger(t, db, uid, wallToUTC(est, "2026-03-10 10:00:00"))

	got, err := repo.GetConsecutiveActiveDays(uid)
	if err != nil {
		t.Fatalf("GetConsecutiveActiveDays (EST): %v", err)
	}
	if got != 3 {
		t.Fatalf("EST streak = %d, want 3", got)
	}
}

// TestNegativeOffsetCrossMidnight verifies the EST day boundary: 23:30 and
// 00:30 EST are an hour apart but span two EST calendar days → streak 2.
func TestNegativeOffsetCrossMidnight(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	restore := useFixedClock(t, est, wallToUTC(est, "2026-03-10 12:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 32

	seedLedger(t, db, uid, wallToUTC(est, "2026-03-09 23:30:00"))
	seedLedger(t, db, uid, wallToUTC(est, "2026-03-10 00:30:00"))

	got, _ := repo.GetConsecutiveActiveDays(uid)
	if got != 2 {
		t.Fatalf("EST across-midnight streak = %d, want 2", got)
	}
}

// --- HALF-HOUR-offset timezone (India, UTC+5:30) ---
//
// A few zones use :30 (India) or :45 offsets. The SQL modifier becomes
// '+05:30'. If strftime/datetime truncated or rounded the half-hour, day
// bucketing near midnight would be off by one.

// TestHalfHourOffsetStreak runs consecutive-days in Asia/Kolkata (UTC+5:30).
func TestHalfHourOffsetStreak(t *testing.T) {
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		kolkata = time.FixedZone("IST", 5*3600+30*60)
	}
	restore := useFixedClock(t, kolkata, wallToUTC(kolkata, "2026-03-10 20:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 40

	seedLedger(t, db, uid, wallToUTC(kolkata, "2026-03-08 10:00:00"))
	seedLedger(t, db, uid, wallToUTC(kolkata, "2026-03-09 10:00:00"))
	seedLedger(t, db, uid, wallToUTC(kolkata, "2026-03-10 10:00:00"))

	got, _ := repo.GetConsecutiveActiveDays(uid)
	if got != 3 {
		t.Fatalf("Kolkata streak = %d, want 3", got)
	}
}

// TestHalfHourOffsetBoundaryMidnight checks a tricky case: 23:50 IST and 00:20
// IST are 30 min apart but on different IST calendar days. The half-hour offset
// must not smear them into one day.
func TestHalfHourOffsetBoundaryMidnight(t *testing.T) {
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		kolkata = time.FixedZone("IST", 5*3600+30*60)
	}
	restore := useFixedClock(t, kolkata, wallToUTC(kolkata, "2026-03-10 12:00:00"))
	defer restore()

	db := setupTestDB(t)
	repo := NewBadgeRepository(db).(*badgeRepo)
	const uid uint = 41

	seedLedger(t, db, uid, wallToUTC(kolkata, "2026-03-09 23:50:00"))
	seedLedger(t, db, uid, wallToUTC(kolkata, "2026-03-10 00:20:00"))

	got, _ := repo.GetConsecutiveActiveDays(uid)
	if got != 2 {
		t.Fatalf("Kolkata cross-midnight streak = %d, want 2 (half-hour offset must preserve day split)", got)
	}
}
