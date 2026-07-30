package service

import (
	"sync"
	"time"
)

// loginLockout is a per-user sliding-window failure counter for logins. It is
// the account-level counterpart to middleware.loginRateLimiter (which throttles
// per source IP). The two are intentionally layered:
//
//   - IP limiter (middleware): counts attempts, pre-handler, can't see results.
//     Stops a single attacker host. Trivially bypassed by an IP pool.
//   - account lockout (here): counts FAILED authentications per user_id, lives
//     in the service layer so it sees success/failure and resets on success.
//     Stops brute-force across many IPs targeting one account.
//
// Intentionally in-memory: a server restart clears all counters, which is fine
// for a family deployment and keeps the hot login path off the DB (same
// philosophy as the IP limiter).
//
// Concurrency: a single mutex guards the whole map. Login throughput is not a
// concern for this app, so the coarse lock is simpler than a sharded structure.
type loginLockout struct {
	mu     sync.Mutex
	fails  map[uint][]time.Time // user_id -> failure timestamps within the window
	window time.Duration        // how long a failure counts toward the lockout
	max    int                  // failures within the window before lockout
	now    func() time.Time     // injectable clock for tests
}

func newLoginLockout(window time.Duration, max int, now func() time.Time) *loginLockout {
	return &loginLockout{
		fails:  make(map[uint][]time.Time),
		window: window,
		max:    max,
		now:    now,
	}
}

// locked reports whether user_id has reached the failure threshold within the
// window. It prunes expired timestamps first (sliding window), so once the
// oldest failure ages out the account unlocks without any explicit action.
//
// Slice-alias invariant: `fresh := l.fails[uid][:0]` reuses the backing array.
// Safe only because we append each kept element before the range cursor could
// reach a position we'd overwrite — we never append more than we consume per
// iteration. Same pattern as middleware.loginRateLimiter.allow; do NOT reorder.
func (l *loginLockout) locked(userID uint) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-l.window)
	fresh := l.fails[userID][:0]
	for _, t := range l.fails[userID] {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	l.fails[userID] = fresh
	return len(fresh) >= l.max
}

// recordFailure appends a failure timestamp for user_id. Called by
// Authenticate after a PIN mismatch (not after a "user not found" lookup —
// those have no user_id to key on and are already covered by the IP limiter +
// the unified "incorrect user PIN code" response).
func (l *loginLockout) recordFailure(userID uint) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[userID] = append(l.fails[userID], l.now())
}

// reset clears the failure history for user_id. Called by Authenticate on a
// successful login so a user who fat-fingers their PIN a few times then gets it
// right doesn't stay one failure away from a lockout.
func (l *loginLockout) reset(userID uint) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, userID)
}
