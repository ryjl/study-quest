package service

import (
	"errors"
	"fmt"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/storage"
)

// ReadingBookService manages PDF book CRUD, the 302 stream-URL resolution
// (mirrors EpisodeService.GetStreamURL with hash/size disaster recovery), and
// page-progress memory.
type ReadingBookService interface {
	GetBooks(userID uint, userRole string, grade string, subjectID uint, standaloneOnly bool) ([]model.ReadingBook, error)
	GetBooksBySeries(seriesID uint) ([]model.ReadingBook, error)
	GetBookByID(id uint) (*model.ReadingBook, error)
	CreateBook(seriesID uint, sortOrder int, title, fileRelativePath, fileHash, coverURL, grade string, subjectID uint, tagIDs []uint) (*model.ReadingBook, error)
	UpdateBook(id uint, seriesID uint, sortOrder int, title, fileRelativePath, fileHash, coverURL, grade string, subjectID uint, tagIDs []uint) (*model.ReadingBook, error)
	DeleteBook(id uint) error

	// GetStreamURL resolves the Alist/WebDAV direct download link for the PDF,
	// with the same disaster-recovery fallbacks as EpisodeService.GetStreamURL
	// (hash lookup → size+path lookup → self-heal the cached path).
	GetStreamURL(bookID uint, userAgent string) (*storage.DownloadLink, error)

	// CanAccess checks whether a user can access a book. Admin/parent always
	// pass. A student passes if they have direct book access OR series access
	// to the book's parent series (series access implies child access, matching
	// the Course→Episode model where course access grants all episodes).
	CanAccess(userID uint, userRole string, bookID uint) (bool, error)

	// Progress (page memory)
	ReportProgress(userID, bookID uint, lastPage int) (*model.ReadingBookProgress, error)
	GetProgress(userID, bookID uint) (*model.ReadingBookProgress, error)
}

type readingBookService struct {
	bookRepo     repository.ReadingBookRepository
	seriesRepo   repository.ReadingSeriesRepository
	settingsRepo repository.SettingsRepository
}

// NewReadingBookService creates an instance of ReadingBookService.
func NewReadingBookService(br repository.ReadingBookRepository, sr repository.SettingsRepository, ssr repository.ReadingSeriesRepository) ReadingBookService {
	return &readingBookService{
		bookRepo:     br,
		seriesRepo:   ssr,
		settingsRepo: sr,
	}
}

func (s *readingBookService) getActiveProvider() (storage.StorageProvider, error) {
	sType := s.settingsRepo.GetWithDefault("storage_type", "alist")
	sURL := s.settingsRepo.GetWithDefault("storage_url", "http://localhost:5244")
	sUser, _ := s.settingsRepo.Get("storage_username")
	sPass, _ := s.settingsRepo.Get("storage_password")
	sToken, _ := s.settingsRepo.Get("storage_token")

	if sType == "alist" {
		return storage.NewAListProvider(sURL, sUser, sPass, sToken), nil
	} else if sType == "webdav" {
		return storage.NewWebDAVProvider(sURL, sUser, sPass), nil
	}
	return nil, errors.New("unsupported storage_type configured: " + sType)
}

func (s *readingBookService) GetBooks(userID uint, userRole string, grade string, subjectID uint, standaloneOnly bool) ([]model.ReadingBook, error) {
	if userRole == "admin" || userRole == "parent" {
		return s.bookRepo.List(grade, subjectID, nil, standaloneOnly)
	}
	allowedIDs, err := s.bookRepo.GetAccessList(userID)
	if err != nil {
		return nil, err
	}
	return s.bookRepo.List(grade, subjectID, allowedIDs, standaloneOnly)
}

func (s *readingBookService) GetBooksBySeries(seriesID uint) ([]model.ReadingBook, error) {
	return s.bookRepo.ListBySeries(seriesID)
}

func (s *readingBookService) GetBookByID(id uint) (*model.ReadingBook, error) {
	return s.bookRepo.FindByID(id)
}

func (s *readingBookService) CreateBook(seriesID uint, sortOrder int, title, fileRelativePath, fileHash, coverURL, grade string, subjectID uint, tagIDs []uint) (*model.ReadingBook, error) {
	g := model.Grade(grade)
	if !g.Valid() {
		return nil, errors.New("invalid reading book grade value: " + grade)
	}
	book := &model.ReadingBook{
		SeriesID:         seriesID,
		SortOrder:        sortOrder,
		Title:            title,
		FileRelativePath: fileRelativePath,
		FileHash:         fileHash,
		CoverURL:         coverURL,
		Grade:            g,
		SubjectID:        subjectID,
	}
	if err := s.bookRepo.Create(book); err != nil {
		return nil, err
	}
	if len(tagIDs) > 0 {
		if err := s.bookRepo.SetTags(book.ID, tagIDs); err != nil {
			return nil, err
		}
	}
	reloaded, err := s.bookRepo.FindByID(book.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return book, nil
}

func (s *readingBookService) UpdateBook(id uint, seriesID uint, sortOrder int, title, fileRelativePath, fileHash, coverURL, grade string, subjectID uint, tagIDs []uint) (*model.ReadingBook, error) {
	g := model.Grade(grade)
	if !g.Valid() {
		return nil, errors.New("invalid reading book grade value: " + grade)
	}
	book, err := s.bookRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if book == nil {
		return nil, nil
	}
	book.SeriesID = seriesID
	book.SortOrder = sortOrder
	book.Title = title
	book.FileRelativePath = fileRelativePath
	book.FileHash = fileHash
	book.CoverURL = coverURL
	book.Grade = g
	book.SubjectID = subjectID
	if err := s.bookRepo.Update(book); err != nil {
		return nil, err
	}
	if err := s.bookRepo.SetTags(book.ID, tagIDs); err != nil {
		return nil, err
	}
	reloaded, err := s.bookRepo.FindByID(book.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return book, nil
}

func (s *readingBookService) DeleteBook(id uint) error {
	return s.bookRepo.Delete(id)
}

// GetStreamURL resolves the PDF download link with disaster recovery, mirroring
// EpisodeService.GetStreamURL exactly: provider lookup → hash fallback (with
// self-healing cached path write-back) → size+path fallback.
func (s *readingBookService) GetStreamURL(bookID uint, userAgent string) (*storage.DownloadLink, error) {
	book, err := s.bookRepo.FindByID(bookID)
	if err != nil {
		return nil, err
	}
	if book == nil {
		return nil, errors.New("reading book not found")
	}

	provider, err := s.getActiveProvider()
	if err != nil {
		return nil, err
	}

	// Try regular path lookup
	link, err := provider.GetDownloadURL(book.FileRelativePath, userAgent)
	if err == nil {
		return link, nil
	}

	// Hash fallback + self-heal
	if provider.SupportsHash() && book.FileHash != "" {
		resolved, rErr := s.bookRepo.FindByHash(book.FileHash)
		if rErr == nil && resolved != nil && resolved.FileRelativePath != book.FileRelativePath {
			book.FileRelativePath = resolved.FileRelativePath
			_ = s.bookRepo.Update(book)
			return provider.GetDownloadURL(resolved.FileRelativePath, userAgent)
		}
	}

	// Size + path fallback
	if book.FileSize != nil {
		resolved, rErr := s.bookRepo.FindByPathAndSize(book.FileRelativePath, *book.FileSize)
		if rErr == nil && resolved != nil {
			return provider.GetDownloadURL(resolved.FileRelativePath, userAgent)
		}
	}

	return nil, fmt.Errorf("failed to stream reading book: resource unavailable (path mismatches)")
}

// CanAccess implements the series-inheritance access model: a student with
// series access can open any book in that series, even without a direct
// UserReadingBookAccess row. This matches the Course→Episode semantics where
// course access grants all episodes. Admin/parent bypass entirely.
func (s *readingBookService) CanAccess(userID uint, userRole string, bookID uint) (bool, error) {
	if userRole == "admin" || userRole == "parent" {
		return true, nil
	}
	// Direct book access.
	ok, err := s.bookRepo.HasAccess(userID, bookID)
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	// Inherited: series access to the book's parent series.
	book, err := s.bookRepo.FindByID(bookID)
	if err != nil || book == nil {
		return false, err
	}
	if book.SeriesID == 0 {
		return false, nil // standalone, no series to inherit from
	}
	return s.seriesRepo.HasAccess(userID, book.SeriesID)
}

func (s *readingBookService) ReportProgress(userID, bookID uint, lastPage int) (*model.ReadingBookProgress, error) {
	return s.bookRepo.UpsertProgress(userID, bookID, lastPage)
}

func (s *readingBookService) GetProgress(userID, bookID uint) (*model.ReadingBookProgress, error) {
	return s.bookRepo.GetProgress(userID, bookID)
}
