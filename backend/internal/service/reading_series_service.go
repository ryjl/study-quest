package service

import (
	"errors"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// ReadingSeriesService handles ReadingSeries business operations and the
// aggregated reading-room view. Mirrors CourseService for the admin-vs-student
// access split (admin pass nil, students pass their access list).
type ReadingSeriesService interface {
	GetSeries(userID uint, userRole string, grade string, subjectID uint) ([]model.ReadingSeries, error)
	GetSeriesByID(id uint) (*model.ReadingSeries, error)
	// HasSeriesAccess checks direct series access for a student. Admin
	// always pass. Used by GetSeries to gate the detail endpoint.
	HasSeriesAccess(userID, seriesID uint) (bool, error)
	CreateSeries(title, description string, grades []model.Grade, subjectID uint, coverURL string, sortOrder int, tagIDs []uint) (*model.ReadingSeries, error)
	UpdateSeries(id uint, title, description string, grades []model.Grade, subjectID uint, coverURL string, sortOrder int, tagIDs []uint) (*model.ReadingSeries, error)
	DeleteSeries(id uint) error

	// GetReadingRoom returns the aggregated shelf view in one call: the series
	// the user can see (with child counts), plus standalone books/articles the
	// user can see that are NOT already covered by a series grant. This avoids
	// three round-trips and client-side de-duplication.
	GetReadingRoom(userID uint, userRole string, grade string, subjectID uint) (*ReadingRoomView, error)
}

// ReadingRoomView is the aggregated payload for the reading-room shelf. Series
// carries its child book/article counts for the card; Books/Articles hold only
// the standalone items (a book whose series the user also has access to is
// shown inside that series, not duplicated in the standalone list).
type ReadingRoomView struct {
	Series   []ReadingSeriesCard
	Books    []model.ReadingBook
	Articles []model.ReadingArticle
}

// ReadingSeriesCard is a series plus its child counts, for shelf cards.
type ReadingSeriesCard struct {
	model.ReadingSeries
	BookCount    int64
	ArticleCount int64
}

type readingSeriesService struct {
	seriesRepo   repository.ReadingSeriesRepository
	bookRepo     repository.ReadingBookRepository
	articleRepo  repository.ReadingArticleRepository
}

// NewReadingSeriesService creates an instance of ReadingSeriesService.
func NewReadingSeriesService(sr repository.ReadingSeriesRepository, br repository.ReadingBookRepository, ar repository.ReadingArticleRepository) ReadingSeriesService {
	return &readingSeriesService{
		seriesRepo:  sr,
		bookRepo:    br,
		articleRepo: ar,
	}
}

func (s *readingSeriesService) GetSeries(userID uint, userRole string, grade string, subjectID uint) ([]model.ReadingSeries, error) {
	if model.IsStaffRole(userRole) {
		return s.seriesRepo.List(grade, subjectID, nil)
	}
	allowedIDs, err := s.seriesRepo.GetAccessList(userID)
	if err != nil {
		return nil, err
	}
	return s.seriesRepo.List(grade, subjectID, allowedIDs)
}

func (s *readingSeriesService) GetSeriesByID(id uint) (*model.ReadingSeries, error) {
	return s.seriesRepo.FindByID(id)
}

// HasSeriesAccess checks direct series access for a student.
func (s *readingSeriesService) HasSeriesAccess(userID, seriesID uint) (bool, error) {
	return s.seriesRepo.HasAccess(userID, seriesID)
}

func (s *readingSeriesService) CreateSeries(title, description string, grades []model.Grade, subjectID uint, coverURL string, sortOrder int, tagIDs []uint) (*model.ReadingSeries, error) {
	for _, g := range grades {
		if !g.Valid() {
			return nil, errors.New("invalid reading series grade value: " + string(g))
		}
	}
	series := &model.ReadingSeries{
		Title:       title,
		Description: description,
		SubjectID:   subjectID,
		CoverURL:    coverURL,
		SortOrder:   sortOrder,
	}
	if err := s.seriesRepo.Create(series); err != nil {
		return nil, err
	}
	if err := s.seriesRepo.SetGrades(series.ID, grades); err != nil {
		return nil, err
	}
	if len(tagIDs) > 0 {
		if err := s.seriesRepo.SetTags(series.ID, tagIDs); err != nil {
			return nil, err
		}
	}
	reloaded, err := s.seriesRepo.FindByID(series.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return series, nil
}

func (s *readingSeriesService) UpdateSeries(id uint, title, description string, grades []model.Grade, subjectID uint, coverURL string, sortOrder int, tagIDs []uint) (*model.ReadingSeries, error) {
	for _, g := range grades {
		if !g.Valid() {
			return nil, errors.New("invalid reading series grade value: " + string(g))
		}
	}
	series, err := s.seriesRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if series == nil {
		return nil, nil
	}
	series.Title = title
	series.Description = description
	series.SubjectID = subjectID
	series.CoverURL = coverURL
	series.SortOrder = sortOrder
	if err := s.seriesRepo.Update(series); err != nil {
		return nil, err
	}
	if err := s.seriesRepo.SetGrades(series.ID, grades); err != nil {
		return nil, err
	}
	if err := s.seriesRepo.SetTags(series.ID, tagIDs); err != nil {
		return nil, err
	}
	reloaded, err := s.seriesRepo.FindByID(series.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return series, nil
}

func (s *readingSeriesService) DeleteSeries(id uint) error {
	return s.seriesRepo.Delete(id)
}

// GetReadingRoom builds the one-shot shelf view. For admin: all series
// + all standalone (series_id=0) books/articles. For students: visible series +
// books/articles the user has direct access to whose parent series is NOT in
// the user's visible-series set (those are reachable inside the series detail,
// so they shouldn't be duplicated on the standalone shelf).
func (s *readingSeriesService) GetReadingRoom(userID uint, userRole string, grade string, subjectID uint) (*ReadingRoomView, error) {
	isAdmin := model.IsStaffRole(userRole)

	// --- Series ---
	var seriesAllowedIDs []uint
	if !isAdmin {
		ids, err := s.seriesRepo.GetAccessList(userID)
		if err != nil {
			return nil, err
		}
		seriesAllowedIDs = ids
	}
	seriesList, err := s.seriesRepo.List(grade, subjectID, seriesAllowedIDs)
	if err != nil {
		return nil, err
	}
	cards := make([]ReadingSeriesCard, 0, len(seriesList))
	seriesIDSet := make(map[uint]bool, len(seriesList))
	for _, ser := range seriesList {
		books, _ := s.bookRepo.ListBySeries(ser.ID)
		articles, _ := s.articleRepo.ListBySeries(ser.ID)
		cards = append(cards, ReadingSeriesCard{
			ReadingSeries: ser,
			BookCount:      int64(len(books)),
			ArticleCount:   int64(len(articles)),
		})
		seriesIDSet[ser.ID] = true
	}

	// --- Books ---
	// Admin: standalone only (series_id=0). Student: all granted books, then
	// post-filter to exclude those whose series is already visible (avoids
	// duplication). A book with series_id=0 is always included.
	var bookAllowedIDs []uint
	if !isAdmin {
		ids, err := s.bookRepo.GetAccessList(userID)
		if err != nil {
			return nil, err
		}
		bookAllowedIDs = ids
	}
	allBooks, err := s.bookRepo.List(grade, subjectID, bookAllowedIDs, isAdmin)
	if err != nil {
		return nil, err
	}
	books := make([]model.ReadingBook, 0, len(allBooks))
	for _, b := range allBooks {
		if b.SeriesID == nil || !seriesIDSet[*b.SeriesID] {
			books = append(books, b)
		}
	}

	// --- Articles --- (same logic as books)
	var articleAllowedIDs []uint
	if !isAdmin {
		ids, err := s.articleRepo.GetAccessList(userID)
		if err != nil {
			return nil, err
		}
		articleAllowedIDs = ids
	}
	allArticles, err := s.articleRepo.List(grade, subjectID, articleAllowedIDs, isAdmin)
	if err != nil {
		return nil, err
	}
	articles := make([]model.ReadingArticle, 0, len(allArticles))
	for _, a := range allArticles {
		if a.SeriesID == nil || !seriesIDSet[*a.SeriesID] {
			articles = append(articles, a)
		}
	}

	return &ReadingRoomView{
		Series:   cards,
		Books:    books,
		Articles: articles,
	}, nil
}
