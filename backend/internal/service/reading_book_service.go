package service

import (
	"errors"
	"fmt"
	"path/filepath"
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
	CreateBook(seriesID uint, sortOrder int, title, fileRelativePath, coverURL string, grades []model.Grade, subjectID uint, tagIDs []uint) (*model.ReadingBook, error)
	UpdateBook(id uint, seriesID uint, sortOrder int, title, fileRelativePath, coverURL string, grades []model.Grade, subjectID uint, tagIDs []uint) (*model.ReadingBook, error)
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
	bookRepo   repository.ReadingBookRepository
	seriesRepo repository.ReadingSeriesRepository
	resolver   *StorageProviderResolver
}

// NewReadingBookService creates an instance of ReadingBookService. The resolver
// replaces the old settingsRepo-backed getActiveProvider: books resolve their
// provider via book.SourceID (nil → global settings fallback).
func NewReadingBookService(br repository.ReadingBookRepository, resolver *StorageProviderResolver, ssr repository.ReadingSeriesRepository) ReadingBookService {
	return &readingBookService{
		bookRepo:   br,
		seriesRepo: ssr,
		resolver:   resolver,
	}
}

func (s *readingBookService) GetBooks(userID uint, userRole string, grade string, subjectID uint, standaloneOnly bool) ([]model.ReadingBook, error) {
	if model.IsStaffRole(userRole) {
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

func (s *readingBookService) CreateBook(seriesID uint, sortOrder int, title, fileRelativePath, coverURL string, grades []model.Grade, subjectID uint, tagIDs []uint) (*model.ReadingBook, error) {
	for _, g := range grades {
		if !g.Valid() {
			return nil, errors.New("invalid reading book grade value: " + string(g))
		}
	}
	var seriesIDPtr *uint
	if seriesID > 0 {
		seriesIDPtr = &seriesID
	}
	book := &model.ReadingBook{
		SeriesID:         seriesIDPtr,
		SortOrder:        sortOrder,
		Title:            title,
		FileRelativePath: fileRelativePath,
		CoverURL:         coverURL,
		SubjectID:        subjectID,
	}
	if err := s.bookRepo.Create(book); err != nil {
		return nil, err
	}
	if err := s.bookRepo.SetGrades(book.ID, grades); err != nil {
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

func (s *readingBookService) UpdateBook(id uint, seriesID uint, sortOrder int, title, fileRelativePath, coverURL string, grades []model.Grade, subjectID uint, tagIDs []uint) (*model.ReadingBook, error) {
	for _, g := range grades {
		if !g.Valid() {
			return nil, errors.New("invalid reading book grade value: " + string(g))
		}
	}
	book, err := s.bookRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if book == nil {
		return nil, nil
	}
	var seriesIDPtr *uint
	if seriesID > 0 {
		seriesIDPtr = &seriesID
	}
	book.SeriesID = seriesIDPtr
	book.SortOrder = sortOrder
	book.Title = title
	book.FileRelativePath = fileRelativePath
	book.CoverURL = coverURL
	book.SubjectID = subjectID
	if err := s.bookRepo.Update(book); err != nil {
		return nil, err
	}
	if err := s.bookRepo.SetGrades(book.ID, grades); err != nil {
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
// EpisodeService.GetStreamURL: provider lookup → basename+size fallback (with
// self-healing cached path write-back).
func (s *readingBookService) GetStreamURL(bookID uint, userAgent string) (*storage.DownloadLink, error) {
	book, err := s.bookRepo.FindByID(bookID)
	if err != nil {
		return nil, err
	}
	if book == nil {
		return nil, errors.New("reading book not found")
	}

	provider, err := s.resolver.Resolve(book.SourceID)
	if err != nil {
		return nil, err
	}

	// Try regular path lookup
	link, err := provider.GetDownloadURL(book.FileRelativePath, userAgent)
	if err == nil {
		return link, nil
	}

	// Disaster recovery: basename + size fallback (mirrors episode service).
	// Scoped to book's own source so a file in source A never self-heals onto
	// source B's path; nil SourceID → legacy unscoped.
	if book.FileSize != nil && book.FileRelativePath != "" {
		basename := filepath.Base(book.FileRelativePath)
		if resolved, rErr := s.bookRepo.FindByBasenameAndSizeScoped(basename, *book.FileSize, book.SourceID); rErr == nil && resolved != nil && resolved.FileRelativePath != book.FileRelativePath {
			book.FileRelativePath = resolved.FileRelativePath
			_ = s.bookRepo.Update(book)
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
	if model.IsStaffRole(userRole) {
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
	if book.SeriesID == nil {
		return false, nil // standalone, no series to inherit from
	}
	return s.seriesRepo.HasAccess(userID, *book.SeriesID)
}

func (s *readingBookService) ReportProgress(userID, bookID uint, lastPage int) (*model.ReadingBookProgress, error) {
	return s.bookRepo.UpsertProgress(userID, bookID, lastPage)
}

func (s *readingBookService) GetProgress(userID, bookID uint) (*model.ReadingBookProgress, error) {
	return s.bookRepo.GetProgress(userID, bookID)
}
