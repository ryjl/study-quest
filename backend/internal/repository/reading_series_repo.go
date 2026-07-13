package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// ReadingSeriesRepository handles SQL operations for the ReadingSeries entity
// and its access table. Mirrors CourseRepository: List filters by the access
// three-state (nil = admin sees all, empty slice = student with zero grants),
// FindByID returns (nil, nil) on not-found, and Delete cascades children.
type ReadingSeriesRepository interface {
	WithTx(tx *gorm.DB) ReadingSeriesRepository
	List(grade string, subjectID uint, allowedIDs []uint) ([]model.ReadingSeries, error)
	FindByID(id uint) (*model.ReadingSeries, error)
	Create(series *model.ReadingSeries) error
	Update(series *model.ReadingSeries) error
	Delete(id uint) error
	SetTags(seriesID uint, tagIDs []uint) error
	SetGrades(seriesID uint, grades []model.Grade) error

	// Access control — mirrors UserCourseAccess methods on UserRepository.
	HasAccess(userID, seriesID uint) (bool, error)
	GrantAccess(userID, seriesID uint) error
	GrantAll(userID uint) error
	RevokeAccess(userID, seriesID uint) error
	RevokeAll(userID uint) error
	GetAccessList(userID uint) ([]uint, error)
	// BatchAccessLists returns user_id → granted series-id slice in one query,
	// so the admin user list can show per-user access without N+1.
	BatchAccessLists() (map[uint][]uint, error)
}

type readingSeriesRepo struct {
	db *gorm.DB
}

// NewReadingSeriesRepository creates an instance of ReadingSeriesRepository.
func NewReadingSeriesRepository(db *gorm.DB) ReadingSeriesRepository {
	return &readingSeriesRepo{db: db}
}

func (r *readingSeriesRepo) WithTx(tx *gorm.DB) ReadingSeriesRepository {
	return &readingSeriesRepo{db: tx}
}

func (r *readingSeriesRepo) List(grade string, subjectID uint, allowedIDs []uint) ([]model.ReadingSeries, error) {
	var series []model.ReadingSeries
	query := r.db.Model(&model.ReadingSeries{})

	if allowedIDs != nil {
		if len(allowedIDs) == 0 {
			return []model.ReadingSeries{}, nil
		}
		query = query.Where("id IN ?", allowedIDs)
	}

	if grade != "" {
		query = query.Where(
			"id IN (SELECT series_id FROM reading_series_grades WHERE grade = ? OR grade = ?)",
			grade, string(model.GradeUniversal),
		)
	}
	if subjectID != 0 {
		query = query.Where("subject_id = ?", subjectID)
	}

	err := query.Preload("Tags").Preload("Grades").Order("sort_order asc, id asc").Find(&series).Error
	return series, err
}

func (r *readingSeriesRepo) FindByID(id uint) (*model.ReadingSeries, error) {
	var series model.ReadingSeries
	if err := r.db.Preload("Tags").Preload("Grades").First(&series, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &series, nil
}

func (r *readingSeriesRepo) Create(series *model.ReadingSeries) error {
	return r.db.Create(series).Error
}

func (r *readingSeriesRepo) Update(series *model.ReadingSeries) error {
	return r.db.Save(series).Error
}

// Delete removes a series and cascades: its tag associations, the three access
// tables (series + orphaned book/article access), child books/articles, and
// book progress. Books/articles under the series become standalone (SeriesID=0)
// rather than being destroyed, so an admin dissolving a series doesn't lose
// the PDFs — mirrors the principle that content outlives its grouping.
func (r *readingSeriesRepo) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Detach child books/articles → standalone (SeriesID=NULL), preserve them.
		if err := tx.Model(&model.ReadingBook{}).Where("series_id = ?", id).Updates(map[string]interface{}{"series_id": nil}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ReadingArticle{}).Where("series_id = ?", id).Updates(map[string]interface{}{"series_id": nil}).Error; err != nil {
			return err
		}
		tx.Delete(&model.UserReadingSeriesAccess{}, "series_id = ?", id)
		return tx.Delete(&model.ReadingSeries{}, id).Error
	})
}

func (r *readingSeriesRepo) SetTags(seriesID uint, tagIDs []uint) error {
	var series model.ReadingSeries
	if err := r.db.First(&series, seriesID).Error; err != nil {
		return err
	}
	tagIDs = dedupUint(tagIDs)
	var tags []model.Tag
	if len(tagIDs) > 0 {
		if err := r.db.Where("id IN ?", tagIDs).Find(&tags).Error; err != nil {
			return err
		}
	}
	return r.db.Model(&series).Association("Tags").Replace(&tags)
}

// SetGrades replaces the series's applicable-grade set.
func (r *readingSeriesRepo) SetGrades(seriesID uint, grades []model.Grade) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("series_id = ?", seriesID).Delete(&model.ReadingSeriesGrade{}).Error; err != nil {
			return err
		}
		seen := make(map[model.Grade]bool, len(grades))
		for _, g := range grades {
			if seen[g] || !g.Valid() {
				continue
			}
			seen[g] = true
			if err := tx.Create(&model.ReadingSeriesGrade{SeriesID: seriesID, Grade: g}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *readingSeriesRepo) HasAccess(userID, seriesID uint) (bool, error) {
	var count int64
	err := r.db.Model(&model.UserReadingSeriesAccess{}).
		Where("user_id = ? AND series_id = ?", userID, seriesID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *readingSeriesRepo) GrantAccess(userID, seriesID uint) error {
	access := model.UserReadingSeriesAccess{UserID: userID, SeriesID: seriesID}
	return r.db.Save(&access).Error
}

func (r *readingSeriesRepo) GrantAll(userID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var series []model.ReadingSeries
		if err := tx.Find(&series).Error; err != nil {
			return err
		}
		for _, s := range series {
			access := model.UserReadingSeriesAccess{UserID: userID, SeriesID: s.ID}
			if err := tx.Save(&access).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *readingSeriesRepo) RevokeAccess(userID, seriesID uint) error {
	return r.db.Delete(&model.UserReadingSeriesAccess{}, "user_id = ? AND series_id = ?", userID, seriesID).Error
}

func (r *readingSeriesRepo) RevokeAll(userID uint) error {
	return r.db.Delete(&model.UserReadingSeriesAccess{}, "user_id = ?", userID).Error
}

func (r *readingSeriesRepo) GetAccessList(userID uint) ([]uint, error) {
	var rows []model.UserReadingSeriesAccess
	if err := r.db.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, len(rows))
	for i, a := range rows {
		ids[i] = a.SeriesID
	}
	return ids, nil
}

func (r *readingSeriesRepo) BatchAccessLists() (map[uint][]uint, error) {
	var rows []model.UserReadingSeriesAccess
	if err := r.db.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint][]uint)
	for _, a := range rows {
		out[a.UserID] = append(out[a.UserID], a.SeriesID)
	}
	return out, nil
}
