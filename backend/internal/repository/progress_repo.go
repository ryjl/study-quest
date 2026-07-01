package repository

import (
	"errors"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// ProgressRepository handles UserProgress updates, UserPoints, and PointsLedger transaction auditing.
type ProgressRepository interface {
	GetProgress(userID, episodeID uint) (*model.UserProgress, error)
	SaveProgress(progress *model.UserProgress) error
	GetPoints(userID uint) (*model.UserPoint, error)
	AddPoints(ledger *model.PointsLedger) error
	GetUserProgressOverview(userID uint) ([]model.UserProgress, error)
}

type progressRepo struct {
	db *gorm.DB
}

// NewProgressRepository creates an instance of ProgressRepository.
func NewProgressRepository(db *gorm.DB) ProgressRepository {
	return &progressRepo{db: db}
}

func (r *progressRepo) GetProgress(userID, episodeID uint) (*model.UserProgress, error) {
	var prog model.UserProgress
	err := r.db.Where("user_id = ? AND episode_id = ?", userID, episodeID).First(&prog).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &prog, nil
}

func (r *progressRepo) SaveProgress(progress *model.UserProgress) error {
	var prog model.UserProgress
	err := r.db.Where("user_id = ? AND episode_id = ?", progress.UserID, progress.EpisodeID).First(&prog).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(progress).Error
		}
		return err
	}

	prog.LastPositionSeconds = progress.LastPositionSeconds
	prog.WatchSeconds = progress.WatchSeconds
	prog.IsCompleted = progress.IsCompleted
	if progress.UnlockedAt != nil {
		prog.UnlockedAt = progress.UnlockedAt
	}
	return r.db.Save(&prog).Error
}

func (r *progressRepo) GetPoints(userID uint) (*model.UserPoint, error) {
	var pt model.UserPoint
	err := r.db.First(&pt, "user_id = ?", userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &pt, nil
}

func (r *progressRepo) AddPoints(ledger *model.PointsLedger) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// 1. Create Ledger entry
		if err := tx.Create(ledger).Error; err != nil {
			return err
		}

		// 2. Load User Points
		var pt model.UserPoint
		err := tx.First(&pt, "user_id = ?", ledger.UserID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// Initialize structure
				pt = model.UserPoint{
					UserID:            ledger.UserID,
					CurrentPoints:     0,
					TotalEarnedPoints: 0,
				}
				if err := tx.Create(&pt).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		}

		// 3. Update Balance
		pt.CurrentPoints += ledger.ChangeAmount
		if ledger.ChangeAmount > 0 {
			pt.TotalEarnedPoints += ledger.ChangeAmount
		}
		pt.UpdatedAt = time.Now()

		return tx.Save(&pt).Error
	})
}

func (r *progressRepo) GetUserProgressOverview(userID uint) ([]model.UserProgress, error) {
	var list []model.UserProgress
	err := r.db.Where("user_id = ?", userID).Find(&list).Error
	return list, err
}
