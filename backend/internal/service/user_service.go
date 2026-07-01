package service

import (
	"errors"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

// UserService manages high-level actions on users.
type UserService interface {
	GetUsers() ([]model.User, error)
	CreateUser(nickname, avatarURL, pin, role string) (*model.User, error)
	Authenticate(userID uint, pin string) (bool, error)
	DeleteUser(id uint) error
	GrantCourseAccess(userID, courseID uint) error
	RevokeCourseAccess(userID, courseID uint) error
	GetUserCourseAccess(userID uint) ([]uint, error)
}

type userService struct {
	repo repository.UserRepository
}

// NewUserService creates an instance of UserService.
func NewUserService(repo repository.UserRepository) UserService {
	return &userService{repo: repo}
}

func (s *userService) GetUsers() ([]model.User, error) {
	return s.repo.List()
}

func (s *userService) CreateUser(nickname, avatarURL, pin, role string) (*model.User, error) {
	if len(pin) < 4 || len(pin) > 6 {
		return nil, errors.New("PIN code must be between 4 and 6 digits")
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
	}

	if err := s.repo.Create(u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *userService) Authenticate(userID uint, pin string) (bool, error) {
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
			return false, nil
		}
		return false, err
	}
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
