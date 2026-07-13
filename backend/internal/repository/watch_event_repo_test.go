package repository

import (
	"testing"
	"time"

	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/testutil"
)

func newWatchEventRepo(t *testing.T) WatchEventRepository {
	t.Helper()
	return NewWatchEventRepository(testutil.NewDB(t))
}

// TestWatchEvent_AppendOrMerge_FirstCallInserts verifies the first heartbeat
// for a (user, episode) creates a new row with the delta as its duration.
func TestWatchEvent_AppendOrMerge_FirstCallInserts(t *testing.T) {
	repo := newWatchEventRepo(t)
	now := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)

	ev, err := repo.AppendOrMerge(1, 10, 100, "learning", 30, now, 60*time.Second)
	if err != nil {
		t.Fatalf("AppendOrMerge: %v", err)
	}
	if ev.DurationSeconds != 30 {
		t.Fatalf("duration = %d, want 30", ev.DurationSeconds)
	}
	if !ev.StartedAt.Equal(now) || !ev.EndedAt.Equal(now) {
		t.Fatalf("first event should have started==ended==now; got start=%v end=%v", ev.StartedAt, ev.EndedAt)
	}
}

// TestWatchEvent_AppendOrMerge_WithinWindowMerges verifies a second heartbeat
// close enough in time folds into the existing row (no new row).
func TestWatchEvent_AppendOrMerge_WithinWindowMerges(t *testing.T) {
	repo := newWatchEventRepo(t)
	t0 := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)

	_, _ = repo.AppendOrMerge(1, 10, 100, "learning", 30, t0, 60*time.Second)
	// 40s later — within the 60s window → merge.
	ev2, err := repo.AppendOrMerge(1, 10, 100, "learning", 20, t0.Add(40*time.Second), 60*time.Second)
	if err != nil {
		t.Fatalf("second AppendOrMerge: %v", err)
	}
	if ev2.DurationSeconds != 50 {
		t.Fatalf("merged duration = %d, want 50 (30+20)", ev2.DurationSeconds)
	}
	if !ev2.StartedAt.Equal(t0) {
		t.Fatalf("merge must keep original StartedAt; got %v", ev2.StartedAt)
	}
	if !ev2.EndedAt.Equal(t0.Add(40 * time.Second)) {
		t.Fatalf("merge must bump EndedAt to now; got %v", ev2.EndedAt)
	}

	// Confirm only one row exists.
	day := appclock.TodayString(t0)
	events, err := repo.ListByUserAndDay(1, day)
	if err != nil {
		t.Fatalf("ListByUserAndDay: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 merged row, got %d", len(events))
	}
}

// TestWatchEvent_AppendOrMerge_BeyondWindowStartsNewRow verifies a heartbeat
// past the merge window opens a new row instead of merging.
func TestWatchEvent_AppendOrMerge_BeyondWindowStartsNewRow(t *testing.T) {
	repo := newWatchEventRepo(t)
	t0 := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)

	_, _ = repo.AppendOrMerge(1, 10, 100, "learning", 30, t0, 60*time.Second)
	// 90s later — past the 60s window → new row.
	ev2, err := repo.AppendOrMerge(1, 10, 100, "learning", 15, t0.Add(90*time.Second), 60*time.Second)
	if err != nil {
		t.Fatalf("second AppendOrMerge: %v", err)
	}
	if ev2.DurationSeconds != 15 {
		t.Fatalf("new row duration = %d, want 15", ev2.DurationSeconds)
	}
	if !ev2.StartedAt.Equal(t0.Add(90 * time.Second)) {
		t.Fatalf("new row StartedAt should be the later time; got %v", ev2.StartedAt)
	}

	day := appclock.TodayString(t0)
	events, _ := repo.ListByUserAndDay(1, day)
	if len(events) != 2 {
		t.Fatalf("want 2 rows after a beyond-window gap, got %d", len(events))
	}
}

// TestWatchEvent_AppendOrMerge_DifferentEpisodeNoMerge verifies heartbeats for
// different episodes don't merge into each other even within the window.
func TestWatchEvent_AppendOrMerge_DifferentEpisodeNoMerge(t *testing.T) {
	repo := newWatchEventRepo(t)
	now := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)

	_, _ = repo.AppendOrMerge(1, 10, 100, "learning", 30, now, 60*time.Second)
	// Same user, same instant, different episode → must NOT merge.
	_, _ = repo.AppendOrMerge(1, 11, 100, "learning", 20, now, 60*time.Second)

	day := appclock.TodayString(now)
	events, _ := repo.ListByUserAndDay(1, day)
	if len(events) != 2 {
		t.Fatalf("different episodes must produce 2 rows, got %d", len(events))
	}
}

// TestWatchEvent_AppendOrMerge_DifferentUserNoMerge verifies heartbeats from
// different users don't merge into each other.
func TestWatchEvent_AppendOrMerge_DifferentUserNoMerge(t *testing.T) {
	repo := newWatchEventRepo(t)
	now := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)

	_, _ = repo.AppendOrMerge(1, 10, 100, "learning", 30, now, 60*time.Second)
	_, _ = repo.AppendOrMerge(2, 10, 100, "learning", 20, now, 60*time.Second)

	day := appclock.TodayString(now)
	if events, _ := repo.ListByUserAndDay(1, day); len(events) != 1 {
		t.Fatalf("user 1 should have exactly 1 row, got %d", len(events))
	}
	if events, _ := repo.ListByUserAndDay(2, day); len(events) != 1 {
		t.Fatalf("user 2 should have exactly 1 row, got %d", len(events))
	}
}

// TestWatchEvent_AppendOrMerge_ZeroWindowDisablesMerge verifies mergeWindow=0
// forces every heartbeat into its own row.
func TestWatchEvent_AppendOrMerge_ZeroWindowDisablesMerge(t *testing.T) {
	repo := newWatchEventRepo(t)
	t0 := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)

	_, _ = repo.AppendOrMerge(1, 10, 100, "learning", 5, t0, 0)
	_, _ = repo.AppendOrMerge(1, 10, 100, "learning", 5, t0.Add(1*time.Second), 0) // 1s apart, but window=0

	day := appclock.TodayString(t0)
	events, _ := repo.ListByUserAndDay(1, day)
	if len(events) != 2 {
		t.Fatalf("window=0 must produce 2 rows, got %d", len(events))
	}
}

// TestWatchEvent_ListByUserAndDay_ScopingAndOrder verifies the day filter is
// per-user, per-business-day, and rows return ascending by StartedAt.
func TestWatchEvent_ListByUserAndDay_ScopingAndOrder(t *testing.T) {
	repo := newWatchEventRepo(t)
	// Two events on the same UTC day for user 1, plus one for user 2 and one
	// on a faraway day. Only user 1's same-day events should come back, oldest
	// first.
	dayMidnight := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC) // 04:00 UTC = 12:00 CST, same day
	_, _ = repo.AppendOrMerge(1, 10, 100, "learning", 10, dayMidnight.Add(2*time.Hour), 0)
	_, _ = repo.AppendOrMerge(1, 11, 100, "learning", 10, dayMidnight.Add(1*time.Hour), 0) // earlier, should sort first
	_, _ = repo.AppendOrMerge(2, 10, 100, "learning", 10, dayMidnight, 0)                   // other user
	_, _ = repo.AppendOrMerge(1, 12, 100, "learning", 10, dayMidnight.AddDate(0, 0, 5), 0)  // other day

	// appclock is Asia/Shanghai (UTC+8); 04:00 UTC → 12:00 CST on 2026-07-13.
	day := "2026-07-13"
	events, err := repo.ListByUserAndDay(1, day)
	if err != nil {
		t.Fatalf("ListByUserAndDay: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events for user 1 on %s, got %d", day, len(events))
	}
	// Ascending by StartedAt: the +1h event before the +2h event.
	if !events[0].StartedAt.Before(events[1].StartedAt) {
		t.Fatalf("events not ascending; first=%v second=%v", events[0].StartedAt, events[1].StartedAt)
	}
}

// TestWatchEvent_DailyDurationsInRange verifies the heatmap aggregation sums
// per business-day and respects the [from, to) bounds.
func TestWatchEvent_DailyDurationsInRange(t *testing.T) {
	repo := newWatchEventRepo(t)
	// Spread events across two consecutive UTC days; in CST (UTC+8) they may
	// collapse or straddle — we pick noon UTC so each lands cleanly in its CST
	// day (12:00 UTC = 20:00 CST, same calendar day).
	d1 := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	_, _ = repo.AppendOrMerge(1, 10, 100, "learning", 60, d1, 0)
	_, _ = repo.AppendOrMerge(1, 11, 100, "learning", 30, d1, 0)
	_, _ = repo.AppendOrMerge(1, 12, 100, "learning", 45, d2, 0)
	// Different user — must be excluded.
	_, _ = repo.AppendOrMerge(2, 10, 100, "learning", 999, d1, 0)

	from := time.Date(2026, 7, 13, 0, 0, 0, 0, appclock.Zone())
	to := time.Date(2026, 7, 15, 0, 0, 0, 0, appclock.Zone())
	got, err := repo.DailyDurationsInRange(1, from, to)
	if err != nil {
		t.Fatalf("DailyDurationsInRange: %v", err)
	}
	d1Str := appclock.TodayString(d1)
	d2Str := appclock.TodayString(d2)
	if got[d1Str] != 90 {
		t.Fatalf("day %s = %d, want 90 (60+30)", d1Str, got[d1Str])
	}
	if got[d2Str] != 45 {
		t.Fatalf("day %s = %d, want 45", d2Str, got[d2Str])
	}
}

// TestWatchEvent_DeleteByUser verifies deletion clears a user's events without
// touching another user's.
func TestWatchEvent_DeleteByUser(t *testing.T) {
	repo := newWatchEventRepo(t)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	_, _ = repo.AppendOrMerge(1, 10, 100, "learning", 10, now, 0)
	_, _ = repo.AppendOrMerge(2, 10, 100, "learning", 10, now, 0)

	if err := repo.DeleteByUser(1); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	day := appclock.TodayString(now)
	if events, _ := repo.ListByUserAndDay(1, day); len(events) != 0 {
		t.Fatalf("user 1 events should be gone, got %d", len(events))
	}
	if events, _ := repo.ListByUserAndDay(2, day); len(events) != 1 {
		t.Fatalf("user 2 events must remain, got %d", len(events))
	}
}
