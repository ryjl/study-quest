package repository

import (
	"errors"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// SessionRepository handles SQL operations for Session entities.
//
// All read methods (FindByToken, ListByUser) implicitly filter out expired
// rows (ExpiresAt <= now) so callers never have to re-check. Writes that need
// "now" accept it as a parameter so the service layer can inject a fake clock
// in tests.
type SessionRepository interface {
	Create(s *model.Session) error
	// FindByToken returns the session for a token, or (nil, nil) if the token
	// does not exist OR the session has expired (now >= ExpiresAt).
	FindByToken(token string, now time.Time) (*model.Session, error)
	DeleteByToken(token string) error
	DeleteByUser(userID uint) error
	// ListByUser returns the user's non-expired sessions, newest first by
	// LastSeenAt. Used by the admin device list.
	ListByUser(userID uint, now time.Time) ([]model.Session, error)
	UpdateNote(token, note string) error
	TouchLastSeen(token string, now time.Time) error
	DeleteExpired(now time.Time) (int64, error)
}

type sessionRepo struct {
	db *gorm.DB
}

// NewSessionRepository creates an instance of SessionRepository.
func NewSessionRepository(db *gorm.DB) SessionRepository {
	return &sessionRepo{db: db}
}

func (r *sessionRepo) Create(s *model.Session) error {
	return r.db.Create(s).Error
}

func (r *sessionRepo) FindByToken(token string, now time.Time) (*model.Session, error) {
	var s model.Session
	err := r.db.Where("token = ? AND expires_at > ?", token, now).First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *sessionRepo) DeleteByToken(token string) error {
	return r.db.Where("token = ?", token).Delete(&model.Session{}).Error
}

func (r *sessionRepo) DeleteByUser(userID uint) error {
	return r.db.Where("user_id = ?", userID).Delete(&model.Session{}).Error
}

func (r *sessionRepo) ListByUser(userID uint, now time.Time) ([]model.Session, error) {
	var sessions []model.Session
	err := r.db.Where("user_id = ? AND expires_at > ?", userID, now).
		Order("last_seen_at DESC").
		Find(&sessions).Error
	return sessions, err
}

func (r *sessionRepo) UpdateNote(token, note string) error {
	return r.db.Model(&model.Session{}).
		Where("token = ?", token).
		Update("note", note).Error
}

func (r *sessionRepo) TouchLastSeen(token string, now time.Time) error {
	return r.db.Model(&model.Session{}).
		Where("token = ?", token).
		Update("last_seen_at", now).Error
}

// DeleteExpired removes all sessions whose ExpiresAt has passed. Returns the
// number of rows deleted. Intended for an optional background GC; correctness
// does not depend on it (expired rows are invisible to reads regardless).
func (r *sessionRepo) DeleteExpired(now time.Time) (int64, error) {
	res := r.db.Where("expires_at <= ?", now).Delete(&model.Session{})
	return res.RowsAffected, res.Error
}
