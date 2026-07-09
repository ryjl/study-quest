package service

import (
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/model"
	"testing"
	"time"
)

// TestComputeUnlockedCount is the table-driven spec for the water-level pure
// function. All cases run with appclock fixed to Asia/Shanghai so "weekly
// Sunday 19:00" etc. are unambiguous. Times are constructed in CST directly.
func TestComputeUnlockedCount(t *testing.T) {
	// Pin the business clock to Shanghai for the whole test (no SetNow needed
	// — we pass explicit `now` values to the function; appclock.In only needs
	// the zone to be correct).
	prev := appclock.Zone()
	appclock.SetZone(time.FixedZone("CST", 8*3600))
	defer appclock.SetZone(prev)

	// Helper: build a CST instant from a wall-clock string.
	cst := func(s string) time.Time {
		tt, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.FixedZone("CST", 8*3600))
		if err != nil {
			t.Fatalf("bad time literal %q: %v", s, err)
		}
		return tt
	}

	weeklySun19 := []model.WeeklyTime{{Weekday: 0, Hour: 19, Minute: 0}}   // every Sunday 19:00
	weeklyMonThu := []model.WeeklyTime{ // Monday & Thursday 19:00
		{Weekday: 1, Hour: 19, Minute: 0},
		{Weekday: 4, Hour: 19, Minute: 0},
	}

	total := 10
	cases := []struct {
		name           string
		strategy       string
		intervalSec    int
		weekly         []model.WeeklyTime
		granted        time.Time
		manual         int
		now            time.Time
		want           int
	}{
		// all_open: always total, regardless of time.
		{"all_open returns total", model.StrategyAllOpen, 0, nil, cst("2026-03-01 08:00:00"), 0, cst("2026-03-01 08:00:00"), total},

		// manual: 1 + manualCount, clamped.
		{"manual initial = 1", model.StrategyManual, 0, nil, cst("2026-03-01 08:00:00"), 0, cst("2026-03-01 08:00:00"), 1},
		{"manual +3 = 4", model.StrategyManual, 0, nil, cst("2026-03-01 08:00:00"), 3, cst("2026-03-01 08:00:00"), 4},
		{"manual clamps to total", model.StrategyManual, 0, nil, cst("2026-03-01 08:00:00"), 100, cst("2026-03-01 08:00:00"), total},

		// interval: 1 + floor(elapsed/interval) + manual.
		{"interval same instant = 1", model.StrategyInterval, 86400, nil, cst("2026-03-01 08:00:00"), 0, cst("2026-03-01 08:00:00"), 1},
		{"interval +1 day = 2", model.StrategyInterval, 86400, nil, cst("2026-03-01 08:00:00"), 0, cst("2026-03-02 08:00:00"), 2},
		{"interval +1.5 days = 2", model.StrategyInterval, 86400, nil, cst("2026-03-01 08:00:00"), 0, cst("2026-03-02 20:00:00"), 2},
		{"interval +3 days = 4", model.StrategyInterval, 86400, nil, cst("2026-03-01 08:00:00"), 0, cst("2026-03-04 08:00:00"), 4},
		{"interval +3 days +2 manual = 6", model.StrategyInterval, 86400, nil, cst("2026-03-01 08:00:00"), 2, cst("2026-03-04 08:00:00"), 6},
		{"interval clamps to total", model.StrategyInterval, 86400, nil, cst("2026-01-01 08:00:00"), 0, cst("2026-06-01 08:00:00"), total},

		// weekly: 1 + elapsed points + manual.
		// 2026-03-01 is a Sunday. granted Sun 08:00. First Sun-19:00 is later that day.
		{"weekly granted before point same day = 1", model.StrategyWeekly, 0, weeklySun19, cst("2026-03-01 08:00:00"), 0, cst("2026-03-01 10:00:00"), 1},
		{"weekly after first point same day = 2", model.StrategyWeekly, 0, weeklySun19, cst("2026-03-01 08:00:00"), 0, cst("2026-03-01 20:00:00"), 2},
		{"weekly +1 week = 3", model.StrategyWeekly, 0, weeklySun19, cst("2026-03-01 08:00:00"), 0, cst("2026-03-08 20:00:00"), 3},
		{"weekly +2 weeks = 4", model.StrategyWeekly, 0, weeklySun19, cst("2026-03-01 08:00:00"), 0, cst("2026-03-15 20:00:00"), 4},
		// Multi-point (Mon+Thu): granted Sun 03-01 08:00. Mon 03-02 19:00 fires →2, Thu 03-05 19:00 →3.
		{"weekly mon+thu after mon only = 2", model.StrategyWeekly, 0, weeklyMonThu, cst("2026-03-01 08:00:00"), 0, cst("2026-03-03 08:00:00"), 2},
		{"weekly mon+thu after both = 3", model.StrategyWeekly, 0, weeklyMonThu, cst("2026-03-01 08:00:00"), 0, cst("2026-03-06 08:00:00"), 3},
		// granted AFTER a point in its own week shouldn't double-count that point.
		{"weekly granted after point skips it", model.StrategyWeekly, 0, weeklySun19, cst("2026-03-01 20:00:00"), 0, cst("2026-03-01 21:00:00"), 1},

		// selected: always 0.
		{"selected always 0", model.StrategySelected, 0, nil, cst("2026-03-01 08:00:00"), 5, cst("2026-06-01 08:00:00"), 0},

		// empty total edge cases.
		{"all_open total=0", model.StrategyAllOpen, 0, nil, cst("2026-03-01 08:00:00"), 0, cst("2026-03-01 08:00:00"), 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Override total per-case via closure: the helper below takes the
			// package-level `total` for most cases; for the total=0 case we
			// special-case by name.
			tot := total
			if tc.name == "all_open total=0" {
				tot = 0
			}
			got := computeUnlockedCount(tc.strategy, tc.intervalSec, tc.weekly, tc.granted, tc.manual, tc.now, tot)
			if got != tc.want {
				t.Errorf("computeUnlockedCount(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestComputeUnlockedCountEmptyCourse is a regression guard: an empty course
// (total=0) must yield 0 unlocked under EVERY strategy, not leak a phantom "1"
// from the manual/interval/weekly base level. The clamp used to guard with
// `total > 0 &&`, which let 1 through when total was 0.
func TestComputeUnlockedCountEmptyCourse(t *testing.T) {
	granted := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	now := granted.AddDate(0, 0, 30)
	weekly := []model.WeeklyTime{{0, 19, 0}}
	for _, strat := range []string{
		model.StrategyAllOpen, model.StrategyManual, model.StrategySelected,
	} {
		if got := computeUnlockedCount(strat, 0, nil, granted, 3, now, 0); got != 0 {
			t.Errorf("empty course strategy=%s: got %d, want 0", strat, got)
		}
	}
	if got := computeUnlockedCount(model.StrategyInterval, 86400, nil, granted, 0, now, 0); got != 0 {
		t.Errorf("empty course interval: got %d, want 0", got)
	}
	if got := computeUnlockedCount(model.StrategyWeekly, 0, weekly, granted, 0, now, 0); got != 0 {
		t.Errorf("empty course weekly: got %d, want 0", got)
	}
}

// TestCountElapsedWeekly is a focused test on the weekly-occurrence counter,
// including the half-open (granted, now] boundary and multi-point cases.
func TestCountElapsedWeekly(t *testing.T) {
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

	sun19 := []model.WeeklyTime{{Weekday: 0, Hour: 19, Minute: 0}}

	cases := []struct {
		name  string
		times []model.WeeklyTime
		granted time.Time
		now    time.Time
		want   int
	}{
		{"none before granted", sun19, cst("2026-03-01 20:00:00"), cst("2026-03-01 21:00:00"), 0},
		{"exactly at point counts (now inclusive)", sun19, cst("2026-03-01 08:00:00"), cst("2026-03-01 19:00:00"), 1},
		{"one week later", sun19, cst("2026-03-01 08:00:00"), cst("2026-03-08 19:00:00"), 2},
		{"just before next week's point", sun19, cst("2026-03-01 08:00:00"), cst("2026-03-08 18:59:59"), 1},
		{"empty times", nil, cst("2026-03-01 08:00:00"), cst("2026-06-01 08:00:00"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := countElapsedWeekly(tc.times, tc.granted, tc.now)
			if got != tc.want {
				t.Errorf("countElapsedWeekly(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestValidateStrategy covers the input validation for template/override saves.
func TestValidateStrategy(t *testing.T) {
	cases := []struct {
		name       string
		strategy   string
		interval   int
		weekly     []model.WeeklyTime
		wantErr    bool
	}{
		{"all_open ok", model.StrategyAllOpen, 0, nil, false},
		{"manual ok", model.StrategyManual, 0, nil, false},
		{"selected ok", model.StrategySelected, 0, nil, false},
		{"interval ok", model.StrategyInterval, 3600, nil, false},
		{"interval zero interval errors", model.StrategyInterval, 0, nil, true},
		{"weekly ok", model.StrategyWeekly, 0, []model.WeeklyTime{{0, 19, 0}}, false},
		{"weekly empty errors", model.StrategyWeekly, 0, nil, true},
		{"weekly bad weekday", model.StrategyWeekly, 0, []model.WeeklyTime{{7, 19, 0}}, true},
		{"weekly bad hour", model.StrategyWeekly, 0, []model.WeeklyTime{{0, 24, 0}}, true},
		{"unknown strategy errors", "bogus", 0, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStrategy(tc.strategy, tc.interval, tc.weekly)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}
