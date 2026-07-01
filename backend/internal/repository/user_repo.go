package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// UserRepository handles SQL operations for User entity and user-course access relationships.
type UserRepository interface {
	List() ([]model.User, error)
	FindByID(id uint) (*model.User, error)
	FindByNickname(nickname string) (*model.User, error)
	Create(user *model.User) error
	Update(user *model.User) error
	Delete(id uint) error

	// Course Access controls
	HasAccess(userID, courseID uint) (bool, error)
	GrantAccess(userID, courseID uint) error
	RevokeAccess(userID, courseID uint) error
	GetAccessList(userID uint) ([]uint, error)
}

type userRepo struct {
	db *gorm.DB
}

// NewUserRepository creates an instance of UserRepository.
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepo{db: db}
}

func (r *userRepo) List() ([]model.User, error) {
	var users []model.User
	err := r.db.Find(&users).Error
	return users, err
}

func (r *userRepo) FindByID(id uint) (*model.User, error) {
	var user model.User
	if err := r.db.First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *userRepo) FindByNickname(nickname string) (*model.User, error) {
	var user model.User
	if err := r.db.Where("nickname = ?", nickname).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *userRepo) Create(user *model.User) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		// Initialize user points structure automatically
		pt := model.UserPoint{
			UserID:            user.ID,
			CurrentPoints:     0,
			TotalEarnedPoints: 0,
		}
		return tx.Create(&pt).Error
	})
}

func (r *userRepo) Update(user *model.User) error {
	return r.db.Save(user).Error
}

func (r *userRepo) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Clean up related user data
		tx.Delete(&model.UserProgress{}, "user_id = ?", id)
		tx.Delete(&model.UserPoint{}, "user_id = ?", id)
		tx.Delete(&model.PointsLedger{}, "user_id = ?", id)
		tx.Delete(&model.UserCourseAccess{}, "user_id = ?", id)
		return tx.Delete(&model.User{}, id).Error
	})
}

func (r *userRepo) HasAccess(userID, courseID uint) (bool, error) {
	var count int64
	err := r.db.Model(&model.UserCourseAccess{}).
		Where("user_id = ? AND course_id = ?", userID, courseID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *userRepo) GrantAccess(userID, courseID uint) error {
	access := model.UserCourseAccess{
		UserID:   userID,
		CourseID: courseID,
	}
	return r.db.Save(&access).Error
}

func (r *userRepo) RevokeAccess(userID, courseID uint) error {
	return r.db.Delete(&model.UserCourseAccess{}, "user_id = ? AND course_id = ?", userID, courseID).Error
}

func (r *userRepo) GetAccessList(userID uint) ([]uint, error) {
	var accessList []model.UserCourseAccess
	err := r.db.Where("user_id = ?", userID).Find(&accessList).Error
	if err != nil {
		return nil, err
	}
	ids := make([]uint, len(accessList))
	for i, a := range accessList {
		ids[i] = a.CourseID
	}
	return ids, nil
}
