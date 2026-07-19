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
	GrantAllCoursesAccess(userID uint) error
	RevokeAccess(userID, courseID uint) error
	RevokeAllAccess(userID uint) error
	GetAccessList(userID uint) ([]uint, error)
	// BatchAccessLists returns user_id → granted course-id slice in one query,
	// so the admin user list can show per-user access without N+1.
	BatchAccessLists() (map[uint][]uint, error)
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
		// Clean up related user data.
		//
		// UserProgress / UserPoint / PointsLedger / UserCourseAccess / WatchEvent
		// 走显式手清(沿用旂史做法,即使部分有 OnDelete:CASCADE 也保留为 defense-in-depth)。
		//
		// AI 表:2026-07-19 这轮把 Quiz/Question/Answer/KnowledgeMemory/
		// UserStudyReport 的 UserID 都加了 OnDelete:CASCADE,删 User 时 DB 自动清。
		// 但仍有两类必须手清:
		//   - AIJob:UserID 是 *uint 无 FK(segment/summary job 不绑用户,UserID 为 nil;
		//     quiz/advice/user_report job 才填),无 FK 就无 CASCADE,必须手清。
		//   - StudyAdvice:UserID 有 FK(CASCADE 会清),但 scope_id 是多态列无 FK。
		//     虽然本表的 user_id 级联能清 user 维度,但为可读性 + 与 episode/course repo
		//     的"多态 scope 显式手清"模式一致,这里也显式删一次(CASCADE 会兜底,无重复)。
		tx.Delete(&model.UserProgress{}, "user_id = ?", id)
		tx.Delete(&model.UserPoint{}, "user_id = ?", id)
		tx.Delete(&model.PointsLedger{}, "user_id = ?", id)
		tx.Delete(&model.UserCourseAccess{}, "user_id = ?", id)
		tx.Delete(&model.WatchEvent{}, "user_id = ?", id)
		tx.Delete(&model.AIJob{}, "user_id = ?", id)
		tx.Delete(&model.StudyAdvice{}, "user_id = ?", id)
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

func (r *userRepo) GrantAllCoursesAccess(userID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var courses []model.Course
		if err := tx.Find(&courses).Error; err != nil {
			return err
		}
		for _, c := range courses {
			access := model.UserCourseAccess{
				UserID:   userID,
				CourseID: c.ID,
			}
			if err := tx.Save(&access).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *userRepo) RevokeAccess(userID, courseID uint) error {
	return r.db.Delete(&model.UserCourseAccess{}, "user_id = ? AND course_id = ?", userID, courseID).Error
}

func (r *userRepo) RevokeAllAccess(userID uint) error {
	return r.db.Delete(&model.UserCourseAccess{}, "user_id = ?", userID).Error
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

// BatchAccessLists loads every user's granted course ids in one query. The
// admin user list uses this to render the per-user access count without
// issuing one GetAccessList per row.
func (r *userRepo) BatchAccessLists() (map[uint][]uint, error) {
	var rows []model.UserCourseAccess
	if err := r.db.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint][]uint)
	for _, a := range rows {
		out[a.UserID] = append(out[a.UserID], a.CourseID)
	}
	return out, nil
}
