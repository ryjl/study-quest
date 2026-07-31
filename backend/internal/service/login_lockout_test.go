package service

import (
	"testing"
	"time"
)

// lockoutClock returns a clock backed by a *time.Time the test can mutate to
// advance time, exercising the sliding-window logic without real sleeping.
func lockoutClock(t *testing.T) (*loginLockout, *time.Time) {
	t.Helper()
	t0 := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return t0 }
	return newLoginLockout(DefaultLockoutWindow, DefaultLockoutMax, now), &t0
}

func TestLoginLockout_LocksAtThreshold(t *testing.T) {
	l, _ := lockoutClock(t)
	uid := uint(1)

	// Up to max-1 failures: not locked (locked() is checked before recording,
	// mirroring how Authenticate uses it).
	for i := 0; i < DefaultLockoutMax-1; i++ {
		l.recordFailure(uid)
	}
	if l.locked(uid) {
		t.Fatalf("account locked after only %d failures (threshold %d)", DefaultLockoutMax-1, DefaultLockoutMax)
	}

	// One more failure reaches the threshold → locked.
	l.recordFailure(uid)
	if !l.locked(uid) {
		t.Fatal("account should be locked at threshold")
	}
}

func TestLoginLockout_WindowExpires(t *testing.T) {
	l, t0 := lockoutClock(t)
	uid := uint(2)

	// Hit the threshold.
	for i := 0; i < DefaultLockoutMax; i++ {
		l.recordFailure(uid)
	}
	if !l.locked(uid) {
		t.Fatal("expected locked at threshold")
	}

	// Advance past the window. locked() prunes expired failures, so the account
	// unlocks without an explicit reset.
	*t0 = t0.Add(DefaultLockoutWindow + time.Second)
	if l.locked(uid) {
		t.Fatal("account should unlock once failures age out of the window")
	}
}

func TestLoginLockout_ResetClearsFailures(t *testing.T) {
	l, _ := lockoutClock(t)
	uid := uint(3)

	for i := 0; i < DefaultLockoutMax; i++ {
		l.recordFailure(uid)
	}
	if !l.locked(uid) {
		t.Fatal("expected locked before reset")
	}

	l.reset(uid)
	if l.locked(uid) {
		t.Fatal("reset should clear the lockout")
	}
}

func TestLoginLockout_PerUserIsolation(t *testing.T) {
	l, _ := lockoutClock(t)
	a, b := uint(10), uint(11)

	for i := 0; i < DefaultLockoutMax; i++ {
		l.recordFailure(a)
	}
	if !l.locked(a) {
		t.Fatal("user a should be locked")
	}
	if l.locked(b) {
		t.Fatal("user b should be unaffected by user a's failures")
	}
}

func TestLoginLockout_OldFailuresPruned(t *testing.T) {
	// Verify the sliding window only counts failures INSIDE the window: an old
	// failure just outside + fresh failures up to threshold-1 must not lock.
	l, t0 := lockoutClock(t)
	uid := uint(20)

	// An old failure, then advance so it's just outside the window.
	l.recordFailure(uid)
	*t0 = t0.Add(DefaultLockoutWindow + time.Second)

	// Now add threshold-1 fresh failures; the stale one must not count.
	for i := 0; i < DefaultLockoutMax-1; i++ {
		l.recordFailure(uid)
	}
	if l.locked(uid) {
		t.Fatal("stale failure should have been pruned; account should not be locked")
	}
}

func TestLoginLockout_Remaining(t *testing.T) {
	l, t0 := lockoutClock(t)
	uid := uint(30)

	// Not locked → 0.
	if r := l.remaining(uid); r != 0 {
		t.Fatalf("unlocked remaining = %d, want 0", r)
	}

	// Hit the threshold. The unlock instant = oldest failure + window.
	// Record failures across a span so we can verify remaining reflects the
	// OLDEST counted failure, not the latest. Each recordFailure is followed
	// by a 2-min advance, so after max iterations: failures landed at
	// +0,+2,+4,+6,+8 min and the clock is at +10 min. Oldest = 10 min ago.
	for i := 0; i < DefaultLockoutMax; i++ {
		l.recordFailure(uid)
		*t0 = t0.Add(2 * time.Minute)
	}
	// remaining = window(15min) − 10min since oldest = 5min = 300s.
	r := l.remaining(uid)
	if r != 300 {
		t.Fatalf("locked remaining = %ds, want 300 (window − oldest span)", r)
	}

	// 1 min later → oldest 11min ago → 4min = 240s left.
	*t0 = t0.Add(time.Minute)
	if r := l.remaining(uid); r != 240 {
		t.Errorf("after 1 min: remaining = %d, want 240", r)
	}

	// Once the oldest failure ages out of the window the count drops below
	// threshold and the account unlocks → remaining 0. Oldest was 11min ago;
	// advance 5 more min so it's 16min ago (> 15min window).
	*t0 = t0.Add(5 * time.Minute)
	if r := l.remaining(uid); r != 0 {
		t.Errorf("once oldest ages out: remaining = %d, want 0 (unlocked)", r)
	}
}

func TestLoginLockout_RemainingUnlocked(t *testing.T) {
	l, _ := lockoutClock(t)
	uid := uint(31)
	// Below threshold: remaining must read 0 even with some failures.
	for i := 0; i < DefaultLockoutMax-1; i++ {
		l.recordFailure(uid)
	}
	if r := l.remaining(uid); r != 0 {
		t.Fatalf("below threshold: remaining = %d, want 0", r)
	}
}
