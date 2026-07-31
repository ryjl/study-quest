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

// TestLoginLockout_RemainingConsistentWithLocked asserts the two views never
// disagree: whenever locked() is true remaining() must be >0, and whenever
// remaining() is 0 locked() must be false. This guards against the two methods
// drifting (e.g. one pruning differently than the other), which would let the
// handler advertise a Retry-After that contradicts the actual gate.
func TestLoginLockout_RemainingConsistentWithLocked(t *testing.T) {
	l, t0 := lockoutClock(t)
	uid := uint(40)

	// Not locked ↔ remaining 0.
	check := func() {
		t.Helper()
		lok := l.locked(uid)
		rem := l.remaining(uid)
		if lok && rem <= 0 {
			t.Fatalf("locked=true but remaining=%d (must be >0)", rem)
		}
		if !lok && rem != 0 {
			t.Fatalf("locked=false but remaining=%d (must be 0)", rem)
		}
	}

	for i := 0; i < DefaultLockoutMax; i++ {
		l.recordFailure(uid)
		check()
	}
	// Locked now.
	if !l.locked(uid) {
		t.Fatal("expected locked at threshold")
	}
	// Advance time in steps; consistency must hold at every point including
	// after the lock naturally expires.
	for step := 0; step < 20; step++ {
		*t0 = t0.Add(time.Minute)
		check()
	}
}

// TestLoginLockout_SlidingWindowNewFailureDoesNotExtendLock documents the
// counter-intuitive-but-correct sliding-window property: the unlock instant is
// pinned to the OLDEST counted failure + window, so a new failure recorded
// WHILE already locked does NOT push the unlock later. The lock lifts when the
// oldest failure ages out, regardless of newer failures. (Newer failures only
// matter once the oldest has aged out — they then form the next window.) This
// is the standard sliding-window limiter semantic and a likely-to-regress
// spot, hence the explicit test.
func TestLoginLockout_SlidingWindowNewFailureDoesNotExtendLock(t *testing.T) {
	l, t0 := lockoutClock(t)
	uid := uint(41)

	// Reach the threshold; unlock instant = oldest(now) + window = 900s.
	for i := 0; i < DefaultLockoutMax; i++ {
		l.recordFailure(uid)
	}
	r0 := l.remaining(uid)
	if r0 != 900 {
		t.Fatalf("at lock: remaining = %d, want 900", r0)
	}

	// Advance 5 min, record a fresh failure (user keeps retrying while locked),
	// then check remaining. Without the new failure it would be 600s (900−300).
	// Because unlock is pinned to the OLDEST failure, the new failure must NOT
	// extend it — remaining stays ~600, NOT bumped back toward 900.
	*t0 = t0.Add(5 * time.Minute)
	l.recordFailure(uid)
	r1 := l.remaining(uid)
	if r1 != 600 {
		t.Fatalf("after +5min + new failure: remaining = %d, want 600 (oldest-pinned, not extended)", r1)
	}
}

// TestLoginLockout_CutoffBoundary verifies the prune predicate t.After(cutoff)
// is strict: a failure exactly at the cutoff (now−window) is pruned, one just
// after is kept. Guards an off-by-one that would either over- or under-lock.
func TestLoginLockout_CutoffBoundary(t *testing.T) {
	l, t0 := lockoutClock(t)
	uid := uint(42)

	// Record exactly max failures at t0, then advance to exactly window later.
	// cutoff = now − window = t0, and the predicate is t.After(cutoff), so the
	// t0 failures are NOT after cutoff → pruned → account unlocks.
	for i := 0; i < DefaultLockoutMax; i++ {
		l.recordFailure(uid)
	}
	*t0 = t0.Add(DefaultLockoutWindow)
	if l.locked(uid) {
		t.Fatal("failures exactly at cutoff (now−window) should be pruned (strict After), account unlocked")
	}

	// Now record max failures again, but advance just shy of the window (1s
	// before). Failures are still just-after-cutoff → kept → still locked.
	for i := 0; i < DefaultLockoutMax; i++ {
		l.recordFailure(uid)
	}
	*t0 = t0.Add(DefaultLockoutWindow - time.Second)
	if !l.locked(uid) {
		t.Fatal("failures 1s before window expiry should still be counted → locked")
	}
}
