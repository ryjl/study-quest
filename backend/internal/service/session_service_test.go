package service

import (
	"encoding/hex"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// fixedClock returns a frozen time, advancing only when the test advances it.
type fixedClock struct{ t time.Time }

func (f *fixedClock) now() time.Time { return f.t }

func newTestSessionService(t *testing.T, ttl time.Duration, clk func() time.Time) (SessionService, repository.SessionRepository) {
	t.Helper()
	db := testutil.NewDB(t)
	repo := repository.NewSessionRepository(db)
	return newSessionServiceWithClock(repo, ttl, clk), repo
}

func TestSessionService_IssueReturnsHexToken(t *testing.T) {
	clk := &fixedClock{t: time.Now()}
	svc, _ := newTestSessionService(t, time.Hour, clk.now)

	tok, err := svc.Issue(5, "iPad", "ua")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(tok) != 64 {
		t.Fatalf("token len = %d, want 64 (32 hex bytes)", len(tok))
	}
	if _, err := hex.DecodeString(tok); err != nil {
		t.Fatalf("token is not valid hex: %v", err)
	}
}

func TestSessionService_IssueTwiceDifferentTokens(t *testing.T) {
	// File DB so concurrent-ish Issue calls don't hit the per-connection
	// :memory: isolation problem if we later parallelize. Single goroutine here
	// but the DB choice is forward-compatible.
	clk := &fixedClock{t: time.Now()}
	svc, _ := newTestSessionService(t, time.Hour, clk.now)

	a, _ := svc.Issue(5, "iPad", "ua")
	b, _ := svc.Issue(5, "TV", "ua2")
	if a == b {
		t.Fatal("two Issue calls returned the same token")
	}
}

func TestSessionService_ValidateReturnsUserID(t *testing.T) {
	clk := &fixedClock{t: time.Now()}
	svc, _ := newTestSessionService(t, time.Hour, clk.now)

	tok, _ := svc.Issue(42, "d", "ua")
	uid, err := svc.Validate(tok)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if uid != 42 {
		t.Fatalf("uid = %d, want 42", uid)
	}
}

func TestSessionService_ValidateUnknownTokenErrors(t *testing.T) {
	clk := &fixedClock{t: time.Now()}
	svc, _ := newTestSessionService(t, time.Hour, clk.now)
	if _, err := svc.Validate("nonexistent"); err == nil {
		t.Fatal("Validate unknown token should error")
	}
}

func TestSessionService_ValidateExpiredTokenErrors(t *testing.T) {
	clk := &fixedClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	svc, repo := newTestSessionService(t, time.Hour, clk.now)

	tok, _ := svc.Issue(7, "d", "ua")
	// Advance time past TTL without touching the row (no sliding renewal).
	clk.t = clk.t.Add(2 * time.Hour)

	// FindByToken at the advanced time should yield nil. Use the service path.
	_, err := svc.Validate(tok)
	if err == nil {
		t.Fatal("Validate of expired token should error")
	}
	// And the repo also reports it as absent at the advanced time.
	if g, _ := repo.FindByToken(tok, clk.now()); g != nil {
		t.Fatalf("expired token should be invisible, got %+v", g)
	}
}

func TestSessionService_ValidateUpdatesLastSeen(t *testing.T) {
	clk := &fixedClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	db := testutil.NewDB(t)
	repo := repository.NewSessionRepository(db)
	svc := newSessionServiceWithClock(repo, time.Hour, clk.now)

	tok, _ := svc.Issue(1, "d", "ua")
	original := clk.now()

	// Advance 5 minutes; Validate should write the new LastSeenAt.
	clk.t = clk.t.Add(5 * time.Minute)
	if _, err := svc.Validate(tok); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	var row model.Session
	_ = db.First(&row, "token = ?", tok).Error
	if row.LastSeenAt.Equal(original) {
		t.Fatalf("LastSeenAt not updated; still %v", row.LastSeenAt)
	}
}

func TestSessionService_RevokeInvalidates(t *testing.T) {
	clk := &fixedClock{t: time.Now()}
	svc, _ := newTestSessionService(t, time.Hour, clk.now)
	tok, _ := svc.Issue(1, "d", "ua")

	if err := svc.Revoke(tok); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := svc.Validate(tok); err == nil {
		t.Fatal("Validate after Revoke should error")
	}
}

func TestSessionService_RevokeIdempotent(t *testing.T) {
	clk := &fixedClock{t: time.Now()}
	svc, _ := newTestSessionService(t, time.Hour, clk.now)
	// Revoking a token that never existed must NOT error (logout is idempotent).
	if err := svc.Revoke("never-issued"); err != nil {
		t.Fatalf("Revoke of absent token should be no-op, got: %v", err)
	}
}

func TestSessionService_RevokeAllByUser(t *testing.T) {
	clk := &fixedClock{t: time.Now()}
	svc, _ := newTestSessionService(t, time.Hour, clk.now)
	a, _ := svc.Issue(1, "iPad", "ua")
	b, _ := svc.Issue(1, "TV", "ua")
	c, _ := svc.Issue(2, "Phone", "ua")

	if err := svc.RevokeAllByUser(1); err != nil {
		t.Fatalf("RevokeAllByUser: %v", err)
	}
	if _, err := svc.Validate(a); err == nil {
		t.Fatal("user 1 token a should be invalid")
	}
	if _, err := svc.Validate(b); err == nil {
		t.Fatal("user 1 token b should be invalid")
	}
	if _, err := svc.Validate(c); err != nil {
		t.Fatal("user 2 token c must remain valid")
	}
}

func TestSessionService_ListDevices(t *testing.T) {
	clk := &fixedClock{t: time.Now()}
	svc, _ := newTestSessionService(t, time.Hour, clk.now)
	_, _ = svc.Issue(1, "iPad", "ua")
	_, _ = svc.Issue(1, "TV", "ua")

	devs, err := svc.ListDevices(1)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("want 2 devices, got %d", len(devs))
	}
	names := map[string]bool{devs[0].DeviceName: true, devs[1].DeviceName: true}
	if !names["iPad"] || !names["TV"] {
		t.Fatalf("device names missing; got %v", names)
	}
}

func TestSessionService_SetDeviceNote(t *testing.T) {
	clk := &fixedClock{t: time.Now()}
	svc, _ := newTestSessionService(t, time.Hour, clk.now)
	tok, _ := svc.Issue(1, "iPad", "ua")

	if err := svc.SetDeviceNote(tok, "客厅那台"); err != nil {
		t.Fatalf("SetDeviceNote: %v", err)
	}
	devs, _ := svc.ListDevices(1)
	if len(devs) != 1 || devs[0].Note != "客厅那台" {
		t.Fatalf("note not persisted; got %+v", devs)
	}
}

// TestSessionService_ConcurrentIssueNoTokenCollision verifies the token
// generator itself doesn't collide under concurrency: many goroutines call
// Issue, and we assert every returned token is distinct. The DB is opened with
// a long busy_timeout so SQLite serializes the concurrent writes instead of
// erroring out — the test is about token uniqueness, not SQLite throughput.
func TestSessionService_ConcurrentIssueNoTokenCollision(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "test.db?_busy_timeout=30000")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewSessionRepository(db)
	svc := NewSessionService(repo, time.Hour)

	const n = 200
	tokens := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := svc.Issue(uint(i), "d", "ua")
			if err != nil {
				t.Errorf("Issue %d: %v", i, err)
				return
			}
			tokens[i] = tok
		}(i)
	}
	wg.Wait()

	seen := make(map[string]int, n)
	for i, tok := range tokens {
		if tok == "" {
			t.Fatalf("token %d was empty (Issue failed)", i)
		}
		if prev, ok := seen[tok]; ok {
			t.Fatalf("token collision between issue %d and %d: %s", prev, i, tok)
		}
		seen[tok] = i
	}
	if len(seen) != n {
		t.Fatalf("unique tokens = %d, want %d", len(seen), n)
	}
}
