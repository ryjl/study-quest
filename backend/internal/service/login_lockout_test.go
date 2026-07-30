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
