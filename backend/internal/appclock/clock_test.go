package appclock

import (
	"testing"
	"time"
)

// TestZoneIsShanghaiByDefault verifies the package initializes to Asia/Shanghai
// (the product targets Chinese students). This is the single source of truth
// every other layer trusts, so a wrong default here silently corrupts all
// "today / yesterday / late-night" math.
func TestZoneIsShanghaiByDefault(t *testing.T) {
	// Restore in case another test flipped it (defensive — tests should be
	// independent, and appclock is global state).
	defer SetZone(mustShanghai())
	zone := Zone()
	if zone.String() != "Asia/Shanghai" {
		t.Errorf("default zone = %q, want Asia/Shanghai", zone.String())
	}
	// Offset sanity: Shanghai is UTC+8 (480 min), and China has no DST, so the
	// offset is constant. We check at two points 6 months apart to catch a
	// regression where someone wires in a DST-observing zone by mistake.
	jan := time.Date(2026, 1, 15, 12, 0, 0, 0, zone)
	jul := time.Date(2026, 7, 15, 12, 0, 0, 0, zone)
	_, janOff := jan.Zone()
	_, julOff := jul.Zone()
	if janOff != 8*3600 || julOff != 8*3600 {
		t.Errorf("Shanghai offset should be +08:00 year-round (no DST); got Jan=%d Jul=%d", janOff, julOff)
	}
}

// TestNowReturnsBusinessZone verifies Now() returns an instant whose Location
// is the business zone, NOT the host's local zone — so comparisons against
// stored UTC never inherit the host TZ. Run on any host, the result must
// report Asia/Shanghai.
func TestNowReturnsBusinessZone(t *testing.T) {
	now := Now()
	if now.Location().String() != "Asia/Shanghai" {
		t.Errorf("Now() location = %q, want Asia/Shanghai (host zone leaked in)", now.Location().String())
	}
}

// TestInConvertsUTC verifies In() shifts a UTC instant into the business zone,
// leaving the instant's physical moment unchanged (only the wall-clock
// representation moves).
func TestInConvertsUTC(t *testing.T) {
	// 2026-03-10 00:30 UTC = 2026-03-10 08:30 Beijing.
	utc := time.Date(2026, 3, 10, 0, 30, 0, 0, time.UTC)
	got := In(utc)
	if got.Location().String() != "Asia/Shanghai" {
		t.Errorf("In() location = %q, want Asia/Shanghai", got.Location().String())
	}
	if got.Hour() != 8 || got.Minute() != 30 {
		t.Errorf("In() wall time = %02d:%02d, want 08:30", got.Hour(), got.Minute())
	}
	// Physical instant preserved (converting back to UTC yields the original).
	if !got.UTC().Equal(utc) {
		t.Errorf("In() changed the physical instant: %v != %v", got.UTC(), utc)
	}
}

// TestTodayStringBusinessDay verifies the calendar-day string follows the
// business zone, not UTC. A UTC instant at 2026-03-10 00:30 is "2026-03-10" in
// UTC but still "2026-03-10" 08:30 Beijing → both happen to agree here, so we
// also test the cross-day case: 2026-03-10 17:00 UTC = 2026-03-11 01:00 Beijing,
// which must report the Beijing day "2026-03-11".
func TestTodayStringBusinessDay(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"UTC midnight stays same Beijing day", time.Date(2026, 3, 10, 0, 30, 0, 0, time.UTC), "2026-03-10"},
		{"UTC 17:00 rolls to next Beijing day", time.Date(2026, 3, 10, 17, 0, 0, 0, time.UTC), "2026-03-11"},
		{"explicit Beijing instant", time.Date(2026, 3, 9, 23, 59, 0, 0, mustShanghai()), "2026-03-09"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TodayString(c.t); got != c.want {
				t.Errorf("TodayString(%v) = %q, want %q", c.t, got, c.want)
			}
		})
	}

	// Zero value → falls back to Now(), which is non-empty and well-formed.
	if s := TodayString(time.Time{}); len(s) != 10 {
		t.Errorf("TodayString(zero) = %q, want a 10-char YYYY-MM-DD", s)
	}
}

// TestSetZoneAndNowInjectAndRestore locks in the test-injection contract:
// SetZone/SetNow override the globals, and the caller MUST restore them. We
// verify (a) the override takes effect, (b) restoring returns to the prior
// value (even if that prior was itself an override — stack discipline).
func TestSetZoneAndNowInjectAndRestore(t *testing.T) {
	origZone := Zone()
	origNow := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	// Override zone to a fake UTC+03:00 and "now" to a fixed instant.
	fakeZone := time.FixedZone("FAKE+03", 3*3600)
	SetZone(fakeZone)
	SetNow(func() time.Time { return origNow })

	if Zone() != fakeZone {
		t.Errorf("after SetZone: Zone() = %p, want fake", Zone())
	}
	if got := Now(); !got.Equal(origNow) {
		t.Errorf("after SetNow: Now() = %v, want %v", got, origNow)
	}
	if got := Now(); got.Location() != fakeZone {
		t.Errorf("Now() location after SetZone = %p, want fake", got.Location())
	}

	// Restore.
	SetZone(origZone)
	SetNow(time.Now)
	if Zone() != origZone {
		t.Errorf("after restore: Zone() = %p, want original %p", Zone(), origZone)
	}
	// Now() should track real wall-clock time again (within a generous window).
	before := time.Now()
	got := Now()
	after := time.Now()
	if got.Before(before.Add(-1*time.Second)) || got.After(after.Add(1*time.Second)) {
		t.Errorf("after restore: Now() = %v not within [%v, %v]", got, before, after)
	}
}
