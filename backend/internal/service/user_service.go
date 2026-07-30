package service

import (
	"errors"
	"time"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

// UserService manages high-level actions on users.
type UserService interface {
	GetUsers() ([]model.User, error)
	CreateUser(nickname, avatarURL, pin, role, grade string) (*model.User, error)
	UpdateUser(id uint, nickname, avatarURL, pin, role, grade string) (*model.User, error)
	Authenticate(userID uint, pin string) (bool, error)
	DeleteUser(id uint) error
	GrantCourseAccess(userID, courseID uint) error
	RevokeCourseAccess(userID, courseID uint) error
	BulkCourseAccess(userID uint, action string) error
	GetUserCourseAccess(userID uint) ([]uint, error)
}

// Default account-lockout thresholds, kept in sync with the per-IP login
// limiter (middleware.LoginRateLimitMiddleware uses the same 5/15min). Exposed
// as consts so tests can reference them and they're easy to audit together.
const (
	DefaultLockoutWindow = 15 * time.Minute
	DefaultLockoutMax    = 5
)

type userService struct {
	repo    repository.UserRepository
	lockout *loginLockout
}

// userServiceOption configures a userService at construction (functional
// option). Used by tests to inject a fixed clock into the lockout.
type userServiceOption func(*userService)

// NewUserService creates an instance of UserService. opts allow tests to
// inject a fixed clock into the login lockout (see WithLockoutClock).
func NewUserService(repo repository.UserRepository, opts ...userServiceOption) UserService {
	s := &userService{
		repo: repo,
		lockout: newLoginLockout(DefaultLockoutWindow, DefaultLockoutMax, func() time.Time {
			return time.Now().UTC()
		}),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// WithLockoutClock injects a custom clock into the login lockout (for tests).
// Production uses time.Now().UTC() via the default constructor.
func WithLockoutClock(now func() time.Time) userServiceOption {
	return func(s *userService) {
		s.lockout = newLoginLockout(DefaultLockoutWindow, DefaultLockoutMax, now)
	}
}

func (s *userService) GetUsers() ([]model.User, error) {
	return s.repo.List()
}

func (s *userService) CreateUser(nickname, avatarURL, pin, role, grade string) (*model.User, error) {
	if len(pin) != 6 {
		return nil, ErrPinInvalid
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	u := &model.User{
		Nickname:  nickname,
		AvatarURL: avatarURL,
		PinHash:   string(hash),
		Role:      role,
		Grade:     grade,
	}

	if err := s.repo.Create(u); err != nil {
		return nil, err
	}
	return u, nil
}

// Authenticate verifies a user's PIN. Returns:
//   - (true, nil)  on a correct PIN
//   - (false, nil) on a PIN mismatch (and records a lockout failure)
//   - (false, ErrAccountLocked) when the account is currently locked
//   - (false, err) on a lookup failure or unknown user
//
// The lockout is keyed by user_id and counts FAILURES (not attempts), and
// resets on success — so an attacker rotating IPs to dodge the per-IP limiter
// still gets throttled per account. An unknown user (no user_id to key on) is
// NOT counted here; it's covered by the IP limiter + the generic 401 response.
func (s *userService) Authenticate(userID uint, pin string) (bool, error) {
	if s.lockout.locked(userID) {
		return false, ErrAccountLocked
	}

	user, err := s.repo.FindByID(userID)
	if err != nil {
		return false, err
	}
	if user == nil {
		return false, errors.New("user not found")
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.PinHash), []byte(pin))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			s.lockout.recordFailure(userID)
			return false, nil
		}
		return false, err
	}
	s.lockout.reset(userID)
	return true, nil
}

func (s *userService) GrantCourseAccess(userID, courseID uint) error {
	return s.repo.GrantAccess(userID, courseID)
}

func (s *userService) RevokeCourseAccess(userID, courseID uint) error {
	return s.repo.RevokeAccess(userID, courseID)
}

func (s *userService) GetUserCourseAccess(userID uint) ([]uint, error) {
	return s.repo.GetAccessList(userID)
}

func (s *userService) DeleteUser(id uint) error {
	return s.repo.Delete(id)
}

func (s *userService) UpdateUser(id uint, nickname, avatarURL, pin, role, grade string) (*model.User, error) {
	u, err := s.repo.FindByID(id)
	if err != nil {
		return nil, err
	}
	// Return (nil, nil) for a missing user — the handler maps that to a 404.
	// The old path returned errors.New("user not found"), which the handler
	// had no way to distinguish from a real failure, so it became a 500.
	if u == nil {
		return nil, nil
	}

	u.Nickname = nickname
	u.AvatarURL = avatarURL
	u.Role = role
	u.Grade = grade

	if pin != "" {
		if len(pin) != 6 {
			return nil, ErrPinInvalid
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		u.PinHash = string(hash)
	}

	if err := s.repo.Update(u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *userService) BulkCourseAccess(userID uint, action string) error {
	if action == "grant_all" {
		return s.repo.GrantAllCoursesAccess(userID)
	} else if action == "revoke_all" {
		return s.repo.RevokeAllAccess(userID)
	}
	return errors.New("unsupported bulk access action: " + action)
}
