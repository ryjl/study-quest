package repository

import (
	"errors"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// ReadingBookRepository handles SQL operations for ReadingBook (PDF) entities,
// their access table, and per-user page progress. Mirrors EpisodeRepository for
// the basename-based disaster-recovery method and progress_repo for the atomic
// upsert.
type ReadingBookRepository interface {
	WithTx(tx *gorm.DB) ReadingBookRepository
	// List filters by access (nil = all, empty = none) and optionally by grade
	// and subject. When standaloneOnly is true, only SeriesID=0 books are
	// returned (the "散本" shelf section).
	List(grade string, subjectID uint, allowedIDs []uint, standaloneOnly bool) ([]model.ReadingBook, error)
	ListBySeries(seriesID uint) ([]model.ReadingBook, error)
	FindByID(id uint) (*model.ReadingBook, error)
	FindByBasenameAndSize(basename string, size int64) (*model.ReadingBook, error)
	// FindByBasenameAndSizeScoped is the multi-source-aware disaster-recovery
	// lookup. The match is restricted to rows in the given source so a file in
	// source A never self-heals onto source B's path. sourceID is REQUIRED: a
	// nil sourceID yields no match (every content row must carry a non-nil
	// SourceID post-import).
	FindByBasenameAndSizeScoped(basename string, size int64, sourceID *uint) (*model.ReadingBook, error)
	Create(book *model.ReadingBook) error
	Update(book *model.ReadingBook) error
	Delete(id uint) error
	SetTags(bookID uint, tagIDs []uint) error
	SetGrades(bookID uint, grades []model.Grade) error

	// Access control
	HasAccess(userID, bookID uint) (bool, error)
	GrantAccess(userID, bookID uint) error
	GrantAll(userID uint) error
	RevokeAccess(userID, bookID uint) error
	RevokeAll(userID uint) error
	GetAccessList(userID uint) ([]uint, error)
	BatchAccessLists() (map[uint][]uint, error)

	// Progress (page memory)
	UpsertProgress(userID, bookID uint, lastPage int) (*model.ReadingBookProgress, error)
	GetProgress(userID, bookID uint) (*model.ReadingBookProgress, error)
}

type readingBookRepo struct {
	db *gorm.DB
}

// NewReadingBookRepository creates an instance of ReadingBookRepository.
func NewReadingBookRepository(db *gorm.DB) ReadingBookRepository {
	return &readingBookRepo{db: db}
}

func (r *readingBookRepo) WithTx(tx *gorm.DB) ReadingBookRepository {
	return &readingBookRepo{db: tx}
}

func (r *readingBookRepo) List(grade string, subjectID uint, allowedIDs []uint, standaloneOnly bool) ([]model.ReadingBook, error) {
	var books []model.ReadingBook
	query := r.db.Model(&model.ReadingBook{})

	if allowedIDs != nil {
		if len(allowedIDs) == 0 {
			return []model.ReadingBook{}, nil
		}
		query = query.Where("id IN ?", allowedIDs)
	}

	if standaloneOnly {
		query = query.Where("series_id IS NULL")
	}
	if grade != "" {
		query = query.Where(
			"id IN (SELECT book_id FROM reading_book_grades WHERE grade = ? OR grade = ?)",
			grade, string(model.GradeUniversal),
		)
	}
	if subjectID != 0 {
		query = query.Where("subject_id = ?", subjectID)
	}

	err := query.Preload("Tags").Preload("Grades").Order("sort_order asc, id asc").Find(&books).Error
	return books, err
}

func (r *readingBookRepo) ListBySeries(seriesID uint) ([]model.ReadingBook, error) {
	var books []model.ReadingBook
	err := r.db.Where("series_id = ?", seriesID).Preload("Tags").Preload("Grades").Order("sort_order asc, id asc").Find(&books).Error
	return books, err
}

func (r *readingBookRepo) FindByID(id uint) (*model.ReadingBook, error) {
	var book model.ReadingBook
	if err := r.db.Preload("Tags").Preload("Grades").First(&book, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &book, nil
}

// FindByBasenameAndSize finds a book whose stored path ends with the given
// basename and whose file_size matches. Used for disaster recovery when the
// primary path 404s on the storage backend.
func (r *readingBookRepo) FindByBasenameAndSize(basename string, size int64) (*model.ReadingBook, error) {
	var book model.ReadingBook
	basenameLike := "%/" + basename
	if err := r.db.Where("file_relative_path LIKE ? AND file_size = ?", basenameLike, size).First(&book).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &book, nil
}

// FindByBasenameAndSizeScoped adds a source_id filter on top of
// FindByBasenameAndSize. sourceID is required: a nil sourceID yields no match
// (every content row must carry a non-nil SourceID post-import).
func (r *readingBookRepo) FindByBasenameAndSizeScoped(basename string, size int64, sourceID *uint) (*model.ReadingBook, error) {
	if sourceID == nil {
		return nil, nil
	}
	var book model.ReadingBook
	basenameLike := "%/" + basename
	if err := r.db.Where("file_relative_path LIKE ? AND file_size = ? AND source_id = ?",
		basenameLike, size, *sourceID).First(&book).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &book, nil
}

func (r *readingBookRepo) Create(book *model.ReadingBook) error {
	return r.db.Create(book).Error
}

func (r *readingBookRepo) Update(book *model.ReadingBook) error {
	return r.db.Save(book).Error
}

func (r *readingBookRepo) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		tx.Delete(&model.ReadingBookProgress{}, "book_id = ?", id)
		tx.Delete(&model.UserReadingBookAccess{}, "book_id = ?", id)
		tx.Delete(&model.ReadingBookGrade{}, "book_id = ?", id)
		return tx.Delete(&model.ReadingBook{}, id).Error
	})
}

func (r *readingBookRepo) SetTags(bookID uint, tagIDs []uint) error {
	var book model.ReadingBook
	if err := r.db.First(&book, bookID).Error; err != nil {
		return err
	}
	tagIDs = dedupUint(tagIDs)
	var tags []model.Tag
	if len(tagIDs) > 0 {
		if err := r.db.Where("id IN ?", tagIDs).Find(&tags).Error; err != nil {
			return err
		}
	}
	return r.db.Model(&book).Association("Tags").Replace(&tags)
}

// SetGrades replaces the book's applicable-grade set.
func (r *readingBookRepo) SetGrades(bookID uint, grades []model.Grade) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("book_id = ?", bookID).Delete(&model.ReadingBookGrade{}).Error; err != nil {
			return err
		}
		seen := make(map[model.Grade]bool, len(grades))
		for _, g := range grades {
			if seen[g] || !g.Valid() {
				continue
			}
			seen[g] = true
			if err := tx.Create(&model.ReadingBookGrade{BookID: bookID, Grade: g}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *readingBookRepo) HasAccess(userID, bookID uint) (bool, error) {
	var count int64
	err := r.db.Model(&model.UserReadingBookAccess{}).
		Where("user_id = ? AND book_id = ?", userID, bookID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *readingBookRepo) GrantAccess(userID, bookID uint) error {
	access := model.UserReadingBookAccess{UserID: userID, BookID: bookID}
	return r.db.Save(&access).Error
}

func (r *readingBookRepo) GrantAll(userID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var books []model.ReadingBook
		if err := tx.Find(&books).Error; err != nil {
			return err
		}
		for _, b := range books {
			access := model.UserReadingBookAccess{UserID: userID, BookID: b.ID}
			if err := tx.Save(&access).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *readingBookRepo) RevokeAccess(userID, bookID uint) error {
	return r.db.Delete(&model.UserReadingBookAccess{}, "user_id = ? AND book_id = ?", userID, bookID).Error
}

func (r *readingBookRepo) RevokeAll(userID uint) error {
	return r.db.Delete(&model.UserReadingBookAccess{}, "user_id = ?", userID).Error
}

func (r *readingBookRepo) GetAccessList(userID uint) ([]uint, error) {
	var rows []model.UserReadingBookAccess
	if err := r.db.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, len(rows))
	for i, a := range rows {
		ids[i] = a.BookID
	}
	return ids, nil
}

func (r *readingBookRepo) BatchAccessLists() (map[uint][]uint, error) {
	var rows []model.UserReadingBookAccess
	if err := r.db.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint][]uint)
	for _, a := range rows {
		out[a.UserID] = append(out[a.UserID], a.BookID)
	}
	return out, nil
}

// UpsertProgress writes the last-read page, overwriting any prior value. Unlike
// UserProgress.watch_seconds there is no concurrent accumulation to worry
// about — a page number is a simple last-writer-wins overwrite, so the upsert
// does not add. Mirrors the INSERT ... ON CONFLICT pattern from progress_repo.
func (r *readingBookRepo) UpsertProgress(userID, bookID uint, lastPage int) (*model.ReadingBookProgress, error) {
	if lastPage < 0 {
		lastPage = 0
	}
	now := time.Now().UTC()
	err := r.db.Exec(`
		INSERT INTO reading_book_progresses (user_id, book_id, last_page, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, book_id) DO UPDATE SET
			last_page = excluded.last_page,
			updated_at = excluded.updated_at
	`, userID, bookID, lastPage, now, now).Error
	if err != nil {
		return nil, err
	}
	var prog model.ReadingBookProgress
	if err := r.db.Where("user_id = ? AND book_id = ?", userID, bookID).First(&prog).Error; err != nil {
		return nil, err
	}
	return &prog, nil
}

func (r *readingBookRepo) GetProgress(userID, bookID uint) (*model.ReadingBookProgress, error) {
	var prog model.ReadingBookProgress
	if err := r.db.Where("user_id = ? AND book_id = ?", userID, bookID).First(&prog).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &prog, nil
}
