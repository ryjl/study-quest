package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// ReadingArticleRepository handles SQL operations for ReadingArticle (web URL)
// entities and their access table. Mirrors ReadingBookRepository minus the
// hash/progress methods — articles have no file to recover and no page progress.
type ReadingArticleRepository interface {
	WithTx(tx *gorm.DB) ReadingArticleRepository
	List(grade string, subjectID uint, allowedIDs []uint, standaloneOnly bool) ([]model.ReadingArticle, error)
	ListBySeries(seriesID uint) ([]model.ReadingArticle, error)
	FindByID(id uint) (*model.ReadingArticle, error)
	Create(article *model.ReadingArticle) error
	Update(article *model.ReadingArticle) error
	Delete(id uint) error
	SetTags(articleID uint, tagIDs []uint) error

	// Access control
	HasAccess(userID, articleID uint) (bool, error)
	GrantAccess(userID, articleID uint) error
	GrantAll(userID uint) error
	RevokeAccess(userID, articleID uint) error
	RevokeAll(userID uint) error
	GetAccessList(userID uint) ([]uint, error)
	BatchAccessLists() (map[uint][]uint, error)
}

type readingArticleRepo struct {
	db *gorm.DB
}

// NewReadingArticleRepository creates an instance of ReadingArticleRepository.
func NewReadingArticleRepository(db *gorm.DB) ReadingArticleRepository {
	return &readingArticleRepo{db: db}
}

func (r *readingArticleRepo) WithTx(tx *gorm.DB) ReadingArticleRepository {
	return &readingArticleRepo{db: tx}
}

func (r *readingArticleRepo) List(grade string, subjectID uint, allowedIDs []uint, standaloneOnly bool) ([]model.ReadingArticle, error) {
	var articles []model.ReadingArticle
	query := r.db.Model(&model.ReadingArticle{})

	if allowedIDs != nil {
		if len(allowedIDs) == 0 {
			return []model.ReadingArticle{}, nil
		}
		query = query.Where("id IN ?", allowedIDs)
	}

	if standaloneOnly {
		query = query.Where("series_id = 0")
	}
	if grade != "" {
		query = query.Where("grade LIKE ? OR grade = 'universal' OR grade = 'all'", "%"+grade+"%")
	}
	if subjectID != 0 {
		query = query.Where("subject_id = ?", subjectID)
	}

	err := query.Preload("Tags").Order("sort_order asc, id asc").Find(&articles).Error
	return articles, err
}

func (r *readingArticleRepo) ListBySeries(seriesID uint) ([]model.ReadingArticle, error) {
	var articles []model.ReadingArticle
	err := r.db.Where("series_id = ?", seriesID).Preload("Tags").Order("sort_order asc, id asc").Find(&articles).Error
	return articles, err
}

func (r *readingArticleRepo) FindByID(id uint) (*model.ReadingArticle, error) {
	var article model.ReadingArticle
	if err := r.db.Preload("Tags").First(&article, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &article, nil
}

func (r *readingArticleRepo) Create(article *model.ReadingArticle) error {
	return r.db.Create(article).Error
}

func (r *readingArticleRepo) Update(article *model.ReadingArticle) error {
	return r.db.Save(article).Error
}

func (r *readingArticleRepo) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		tx.Delete(&model.UserReadingArticleAccess{}, "article_id = ?", id)
		return tx.Delete(&model.ReadingArticle{}, id).Error
	})
}

func (r *readingArticleRepo) SetTags(articleID uint, tagIDs []uint) error {
	var article model.ReadingArticle
	if err := r.db.First(&article, articleID).Error; err != nil {
		return err
	}
	tagIDs = dedupUint(tagIDs)
	var tags []model.Tag
	if len(tagIDs) > 0 {
		if err := r.db.Where("id IN ?", tagIDs).Find(&tags).Error; err != nil {
			return err
		}
	}
	return r.db.Model(&article).Association("Tags").Replace(&tags)
}

func (r *readingArticleRepo) HasAccess(userID, articleID uint) (bool, error) {
	var count int64
	err := r.db.Model(&model.UserReadingArticleAccess{}).
		Where("user_id = ? AND article_id = ?", userID, articleID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *readingArticleRepo) GrantAccess(userID, articleID uint) error {
	access := model.UserReadingArticleAccess{UserID: userID, ArticleID: articleID}
	return r.db.Save(&access).Error
}

func (r *readingArticleRepo) GrantAll(userID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var articles []model.ReadingArticle
		if err := tx.Find(&articles).Error; err != nil {
			return err
		}
		for _, a := range articles {
			access := model.UserReadingArticleAccess{UserID: userID, ArticleID: a.ID}
			if err := tx.Save(&access).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *readingArticleRepo) RevokeAccess(userID, articleID uint) error {
	return r.db.Delete(&model.UserReadingArticleAccess{}, "user_id = ? AND article_id = ?", userID, articleID).Error
}

func (r *readingArticleRepo) RevokeAll(userID uint) error {
	return r.db.Delete(&model.UserReadingArticleAccess{}, "user_id = ?", userID).Error
}

func (r *readingArticleRepo) GetAccessList(userID uint) ([]uint, error) {
	var rows []model.UserReadingArticleAccess
	if err := r.db.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, len(rows))
	for i, a := range rows {
		ids[i] = a.ArticleID
	}
	return ids, nil
}

func (r *readingArticleRepo) BatchAccessLists() (map[uint][]uint, error) {
	var rows []model.UserReadingArticleAccess
	if err := r.db.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint][]uint)
	for _, a := range rows {
		out[a.UserID] = append(out[a.UserID], a.ArticleID)
	}
	return out, nil
}
