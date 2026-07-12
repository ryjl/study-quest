// Package appclock provides a single, explicit business timezone for all
// "human date" semantics in the app (今天 / 昨天 / 连续学习几天).
//
// Why this exists:
//
// Storage is UTC (the right call — timestamps are physical instants). But a
// lot of app logic asks "is this TODAY?" or "was this before 22:00?" — and
// "today" is a cultural, timezone-relative concept, not a physical one. For a
// Chinese student, "今天" means the Beijing-calendar day, not the UTC day.
//
// The old code mixed two timezone sources:
//   - Go:   time.Now()            → the server process's local zone (often UTC
//                                   in containers, host zone elsewhere)
//   - SQLite: STRFTIME(...,'localtime') → the database process's local zone
// When those two disagreed (very common in Docker), consecutive-day streaks
// silently computed to 0 and fired at the wrong days.
//
// The fix: pick ONE business timezone (Asia/Shanghai — the user base is
// Chinese students), make it explicit and injectable, and have every layer use
// exactly that zone for human-date math. UTC stays the storage format; this
// package is only consulted when converting stored UTC instants into
// calendar-day / hour-of-day for business rules.
//
// AppTZ is settable so tests can inject a fixed zone (or, via SetNow, a fixed
// instant) to make streak/day-boundary logic deterministic.
package appclock

import (
	"sync"
	"time"
)

// defaultZone is the business timezone. Asia/Shanghai because the product
// targets Chinese students; "今天/昨天" all follow the Beijing
// calendar. Override via SetZone (mainly for tests).
var (
	mu      sync.RWMutex
	zone    = mustShanghai()
	nowFunc = time.Now // overridable clock for tests
)

// mustShanghai loads Asia/Shanghai, panicking only if the binary is built
// without the tzdata embed (it isn't, by default — Go ships the zone table).
func mustShanghai() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		// Fall back to a fixed +08:00 offset so we never hard-fail even on a
		// stripped system without the zone database. This loses DST handling,
		// but China hasn't observed DST since 1991, so it's equivalent here.
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

// Zone returns the active business timezone (UTC-converted "today" math uses
// this). Always non-nil.
func Zone() *time.Location {
	mu.RLock()
	defer mu.RUnlock()
	return zone
}

// Now returns the current instant in the business timezone. Use for all
// "what day is it" / "what hour is it" comparisons; UTC storage timestamps are
// converted via In(t) before comparison.
func Now() time.Time {
	mu.RLock()
	defer mu.RUnlock()
	return nowFunc().In(zone)
}

// In converts a stored UTC instant into the business timezone. Apply this to
// any DB timestamp before asking "what calendar-day / hour is this?".
func In(t time.Time) time.Time {
	mu.RLock()
	defer mu.RUnlock()
	return t.In(zone)
}

// TodayString returns the business-zone "YYYY-MM-DD" of a given instant (or now
// if t is zero). Centralizes the calendar-day formatting so streak logic can't
// drift between callers.
func TodayString(t time.Time) string {
	if t.IsZero() {
		t = Now()
	}
	return In(t).Format("2006-01-02")
}

// SetZone overrides the business timezone. Intended for tests; callers should
// restore the previous value (defer SetZone(prev)) to avoid leaking state.
func SetZone(loc *time.Location) {
	mu.Lock()
	defer mu.Unlock()
	zone = loc
}

// SetNow overrides the clock source. Intended for tests; restore with
// SetNow(time.Now) or a captured original.
func SetNow(f func() time.Time) {
	mu.Lock()
	defer mu.Unlock()
	nowFunc = f
}
