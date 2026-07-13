package repository

import (
	"testing"
	"time"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"
)

func makeRow(token string, userID uint, expiresAt time.Time) *model.Session {
	now := time.Now()
	return &model.Session{
		Token:      token,
		UserID:     userID,
		DeviceName: "dev-" + token,
		UserAgent:  "ua-" + token,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  expiresAt,
	}
}

func TestSessionRepository_CreateAndFind(t *testing.T) {
	db := testutil.NewDB(t)
	repo := NewSessionRepository(db)
	now := time.Now()

	if err := repo.Create(makeRow("tok-1", 5, now.Add(time.Hour))); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.FindByToken("tok-1", now)
	if err != nil {
		t.Fatalf("FindByToken: %v", err)
	}
	if got == nil || got.UserID != 5 {
		t.Fatalf("FindByToken = %+v, want user 5", got)
	}
}

func TestSessionRepository_FindByTokenExpiredReturnsNil(t *testing.T) {
	db := testutil.NewDB(t)
	repo := NewSessionRepository(db)
	past := time.Now().Add(-time.Minute)
	if err := repo.Create(makeRow("old", 5, past)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.FindByToken("old", time.Now())
	if err != nil {
		t.Fatalf("FindByToken err: %v", err)
	}
	if got != nil {
		t.Fatalf("expired session should return nil, got %+v", got)
	}
}

func TestSessionRepository_FindByTokenUnknownReturnsNil(t *testing.T) {
	db := testutil.NewDB(t)
	repo := NewSessionRepository(db)
	got, err := repo.FindByToken("nope", time.Now())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Fatalf("unknown token should return nil, got %+v", got)
	}
}

func TestSessionRepository_DeleteByToken(t *testing.T) {
	db := testutil.NewDB(t)
	repo := NewSessionRepository(db)
	now := time.Now()
	_ = repo.Create(makeRow("a", 5, now.Add(time.Hour)))
	_ = repo.Create(makeRow("b", 5, now.Add(time.Hour)))

	if err := repo.DeleteByToken("a"); err != nil {
		t.Fatalf("DeleteByToken: %v", err)
	}
	if g, _ := repo.FindByToken("a", now); g != nil {
		t.Fatal("a should be gone")
	}
	if g, _ := repo.FindByToken("b", now); g == nil {
		t.Fatal("b should remain")
	}
}

func TestSessionRepository_DeleteByUserOnlyAffectsThatUser(t *testing.T) {
	db := testutil.NewDB(t)
	repo := NewSessionRepository(db)
	now := time.Now()
	_ = repo.Create(makeRow("u1-a", 1, now.Add(time.Hour)))
	_ = repo.Create(makeRow("u1-b", 1, now.Add(time.Hour)))
	_ = repo.Create(makeRow("u2-a", 2, now.Add(time.Hour)))

	if err := repo.DeleteByUser(1); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if g, _ := repo.FindByToken("u1-a", now); g != nil {
		t.Fatal("u1-a should be gone")
	}
	if g, _ := repo.FindByToken("u1-b", now); g != nil {
		t.Fatal("u1-b should be gone")
	}
	if g, _ := repo.FindByToken("u2-a", now); g == nil {
		t.Fatal("u2-a must remain (different user)")
	}
}

func TestSessionRepository_ListByUserFiltersExpiredAndSortsByLastSeen(t *testing.T) {
	db := testutil.NewDB(t)
	repo := NewSessionRepository(db)
	now := time.Now()

	older := makeRow("older", 1, now.Add(time.Hour))
	older.LastSeenAt = now.Add(-time.Hour)
	_ = repo.Create(older)
	newer := makeRow("newer", 1, now.Add(time.Hour))
	newer.LastSeenAt = now.Add(-time.Minute)
	_ = repo.Create(newer)
	_ = repo.Create(makeRow("expired", 1, now.Add(-time.Minute)))
	_ = repo.Create(makeRow("other", 2, now.Add(time.Hour)))

	got, err := repo.ListByUser(1, now)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 active sessions for user 1, got %d: %+v", len(got), got)
	}
	if got[0].Token != "newer" {
		t.Fatalf("expected newest first, got %q", got[0].Token)
	}
}

func TestSessionRepository_UpdateNoteOnlyChangesNote(t *testing.T) {
	db := testutil.NewDB(t)
	repo := NewSessionRepository(db)
	now := time.Now()
	row := makeRow("x", 7, now.Add(time.Hour))
	row.DeviceName = "iPad"
	_ = repo.Create(row)

	if err := repo.UpdateNote("x", "客厅那台"); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	got, _ := repo.FindByToken("x", now)
	if got == nil {
		t.Fatal("row missing after UpdateNote")
	}
	if got.Note != "客厅那台" {
		t.Fatalf("note = %q, want 客厅那台", got.Note)
	}
	if got.DeviceName != "iPad" {
		t.Fatalf("UpdateNote mutated DeviceName to %q", got.DeviceName)
	}
}

func TestSessionRepository_TouchLastSeenOnlyChangesLastSeen(t *testing.T) {
	db := testutil.NewDB(t)
	repo := NewSessionRepository(db)
	base := time.Now().Add(-time.Hour)
	row := makeRow("y", 7, base.Add(2*time.Hour))
	row.LastSeenAt = base
	_ = repo.Create(row)

	newSeen := time.Now()
	if err := repo.TouchLastSeen("y", newSeen); err != nil {
		t.Fatalf("TouchLastSeen: %v", err)
	}
	// Read raw to avoid FindByToken's expiry filter masking issues.
	var fresh model.Session
	if err := db.First(&fresh, "token = ?", "y").Error; err != nil {
		t.Fatalf("raw read: %v", err)
	}
	if !fresh.LastSeenAt.Equal(newSeen) {
		t.Fatalf("LastSeenAt = %v, want %v", fresh.LastSeenAt, newSeen)
	}
	if fresh.DeviceName != "dev-y" {
		t.Fatalf("TouchLastSeen mutated DeviceName to %q", fresh.DeviceName)
	}
}

func TestSessionRepository_DeleteExpiredOnlyDeletesExpired(t *testing.T) {
	db := testutil.NewDB(t)
	repo := NewSessionRepository(db)
	now := time.Now()
	_ = repo.Create(makeRow("exp1", 1, now.Add(-time.Minute)))
	_ = repo.Create(makeRow("exp2", 1, now.Add(-time.Second)))
	_ = repo.Create(makeRow("live", 1, now.Add(time.Hour)))

	n, err := repo.DeleteExpired(now)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted = %d, want 2", n)
	}
	if g, _ := repo.FindByToken("live", now); g == nil {
		t.Fatal("live session was wrongly deleted")
	}
}
