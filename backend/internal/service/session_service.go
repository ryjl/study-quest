package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"time"
)

// SessionService issues, validates, and revokes opaque login sessions.
//
// The token is a 32-byte random value hex-encoded to a 64-char string. It is
// returned to the client exactly once at login and never stored client-side
// in the DB in any reversible form — the stored row IS the source of truth,
// and revocation = deleting that row. There is no signing key to rotate.
//
// Sessions are fixed-TTL (no sliding renewal): ExpiresAt is set at Issue time
// and never moved. Validate also updates LastSeenAt so the admin device list
// can show "last active".
type SessionService interface {
	// Issue creates a new session for userID and returns its token. The same
	// user may hold many concurrent sessions (one per device); Issue never
	// invalidates existing tokens.
	Issue(userID uint, deviceName, userAgent string) (token string, err error)
	// Validate returns the userID bound to a non-expired token, or an error if
	// the token is unknown/expired/revoked. Updates LastSeenAt as a side effect.
	Validate(token string) (userID uint, err error)
	// Revoke deletes a single session by token. Revoking an already-absent
	// token is a no-op (not an error) so logout is idempotent.
	Revoke(token string) error
	// RevokeAllByUser deletes every session belonging to userID.
	RevokeAllByUser(userID uint) error
	// ListDevices returns the user's active sessions, newest first.
	ListDevices(userID uint) ([]model.Session, error)
	// SetDeviceNote sets the admin-editable note on a device session.
	SetDeviceNote(token, note string) error
}

// ErrSessionInvalid is returned by Validate for unknown/expired/revoked tokens.
// The middleware turns this into a 401.
var ErrSessionInvalid = errors.New("session invalid or expired")

type sessionService struct {
	repo repository.SessionRepository
	ttl  time.Duration
	// now is injectable for tests; production uses time.Now.
	now func() time.Time
}

// NewSessionService creates a SessionService with the given fixed TTL.
func NewSessionService(repo repository.SessionRepository, ttl time.Duration) SessionService {
	// Storage timestamps are UTC (CLAUDE.md #3): CreatedAt/LastSeenAt/ExpiresAt
	// are persisted, so the clock must return UTC to match CURRENT_TIMESTAMP and
	// every other DB timestamp. Tests override via newSessionServiceWithClock.
	return &sessionService{repo: repo, ttl: ttl, now: func() time.Time { return time.Now().UTC() }}
}

// newSessionServiceWithClock is the testable constructor (clock injectable).
func newSessionServiceWithClock(repo repository.SessionRepository, ttl time.Duration, now func() time.Time) SessionService {
	return &sessionService{repo: repo, ttl: ttl, now: now}
}

func (s *sessionService) Issue(userID uint, deviceName, userAgent string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)

	now := s.now()
	sess := &model.Session{
		Token:      token,
		UserID:     userID,
		DeviceName: deviceName,
		UserAgent:  userAgent,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.ttl),
	}
	if err := s.repo.Create(sess); err != nil {
		return "", err
	}
	return token, nil
}

func (s *sessionService) Validate(token string) (uint, error) {
	sess, err := s.repo.FindByToken(token, s.now())
	if err != nil {
		return 0, err
	}
	if sess == nil {
		return 0, ErrSessionInvalid
	}
	// Best-effort last-seen refresh; a failure here must NOT invalidate an
	// otherwise valid session (the row exists and is non-expired).
	_ = s.repo.TouchLastSeen(token, s.now())
	return sess.UserID, nil
}

func (s *sessionService) Revoke(token string) error {
	return s.repo.DeleteByToken(token)
}

func (s *sessionService) RevokeAllByUser(userID uint) error {
	return s.repo.DeleteByUser(userID)
}

func (s *sessionService) ListDevices(userID uint) ([]model.Session, error) {
	return s.repo.ListByUser(userID, s.now())
}

func (s *sessionService) SetDeviceNote(token, note string) error {
	return s.repo.UpdateNote(token, note)
}
