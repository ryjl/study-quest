package service

import (
	"errors"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// ReadingArticleService manages web-article CRUD and retrieval. The effective
// URL returned to the client is SourceURL today; when Phase 2 offline-mirroring
// lands, GetArticle will switch to MirroredURL when MirrorStatus == "ready".
// That switch is the only future change point — the rest of the read path is
// transparent to mirror status.
type ReadingArticleService interface {
	GetArticles(userID uint, userRole string, grade string, subjectID uint, standaloneOnly bool) ([]model.ReadingArticle, error)
	GetArticlesBySeries(seriesID uint) ([]model.ReadingArticle, error)
	GetArticleByID(id uint) (*model.ReadingArticle, error)
	CreateArticle(seriesID uint, sortOrder int, title, sourceURL, whitelistDomains, coverURL string, grades []model.Grade, subjectID uint, tagIDs []uint) (*model.ReadingArticle, error)
	UpdateArticle(id uint, seriesID uint, sortOrder int, title, sourceURL, whitelistDomains, coverURL string, grades []model.Grade, subjectID uint, tagIDs []uint) (*model.ReadingArticle, error)
	DeleteArticle(id uint) error

	// CanAccess checks whether a user can access an article. Admin
	// always pass. A student passes if they have direct article access OR
	// series access to the article's parent series (same inheritance model as
	// books).
	CanAccess(userID uint, userRole string, articleID uint) (bool, error)

	// EffectiveURL returns the URL the client should load. Phase 1: always
	// SourceURL. Phase 2 (future): MirroredURL when MirrorStatus == "ready".
	EffectiveURL(a *model.ReadingArticle) string
}

type readingArticleService struct {
	articleRepo repository.ReadingArticleRepository
	seriesRepo  repository.ReadingSeriesRepository
}

// NewReadingArticleService creates an instance of ReadingArticleService.
func NewReadingArticleService(ar repository.ReadingArticleRepository, ssr repository.ReadingSeriesRepository) ReadingArticleService {
	return &readingArticleService{articleRepo: ar, seriesRepo: ssr}
}

func (s *readingArticleService) GetArticles(userID uint, userRole string, grade string, subjectID uint, standaloneOnly bool) ([]model.ReadingArticle, error) {
	if model.IsStaffRole(userRole) {
		return s.articleRepo.List(grade, subjectID, nil, standaloneOnly)
	}
	allowedIDs, err := s.articleRepo.GetAccessList(userID)
	if err != nil {
		return nil, err
	}
	return s.articleRepo.List(grade, subjectID, allowedIDs, standaloneOnly)
}

func (s *readingArticleService) GetArticlesBySeries(seriesID uint) ([]model.ReadingArticle, error) {
	return s.articleRepo.ListBySeries(seriesID)
}

func (s *readingArticleService) GetArticleByID(id uint) (*model.ReadingArticle, error) {
	return s.articleRepo.FindByID(id)
}

func (s *readingArticleService) CreateArticle(seriesID uint, sortOrder int, title, sourceURL, whitelistDomains, coverURL string, grades []model.Grade, subjectID uint, tagIDs []uint) (*model.ReadingArticle, error) {
	for _, g := range grades {
		if !g.Valid() {
			return nil, errors.New("invalid reading article grade value: " + string(g))
		}
	}
	var seriesIDPtr *uint
	if seriesID > 0 {
		seriesIDPtr = &seriesID
	}
	article := &model.ReadingArticle{
		SeriesID:         seriesIDPtr,
		SortOrder:        sortOrder,
		Title:            title,
		SourceURL:        sourceURL,
		WhitelistDomains: whitelistDomains,
		CoverURL:         coverURL,
		SubjectID:        subjectID,
	}
	if err := s.articleRepo.Create(article); err != nil {
		return nil, err
	}
	if err := s.articleRepo.SetGrades(article.ID, grades); err != nil {
		return nil, err
	}
	if len(tagIDs) > 0 {
		if err := s.articleRepo.SetTags(article.ID, tagIDs); err != nil {
			return nil, err
		}
	}
	reloaded, err := s.articleRepo.FindByID(article.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return article, nil
}

func (s *readingArticleService) UpdateArticle(id uint, seriesID uint, sortOrder int, title, sourceURL, whitelistDomains, coverURL string, grades []model.Grade, subjectID uint, tagIDs []uint) (*model.ReadingArticle, error) {
	for _, g := range grades {
		if !g.Valid() {
			return nil, errors.New("invalid reading article grade value: " + string(g))
		}
	}
	article, err := s.articleRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if article == nil {
		return nil, nil
	}
	var seriesIDPtr *uint
	if seriesID > 0 {
		seriesIDPtr = &seriesID
	}
	article.SeriesID = seriesIDPtr
	article.SortOrder = sortOrder
	article.Title = title
	article.SourceURL = sourceURL
	article.WhitelistDomains = whitelistDomains
	article.CoverURL = coverURL
	article.SubjectID = subjectID
	if err := s.articleRepo.Update(article); err != nil {
		return nil, err
	}
	if err := s.articleRepo.SetGrades(article.ID, grades); err != nil {
		return nil, err
	}
	if err := s.articleRepo.SetTags(article.ID, tagIDs); err != nil {
		return nil, err
	}
	reloaded, err := s.articleRepo.FindByID(article.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return article, nil
}

func (s *readingArticleService) DeleteArticle(id uint) error {
	return s.articleRepo.Delete(id)
}

// EffectiveURL returns the URL the client should load.
// Phase 1: always SourceURL.
// Phase 2 (future): return MirroredURL when MirrorStatus == "ready".
func (s *readingArticleService) EffectiveURL(a *model.ReadingArticle) string {
	// Phase 2 hook — intentionally not active yet:
	// if a.MirrorStatus == "ready" && a.MirroredURL != "" {
	//     return a.MirroredURL
	// }
	return a.SourceURL
}

// CanAccess implements the series-inheritance access model for articles,
// mirroring ReadingBookService.CanAccess.
func (s *readingArticleService) CanAccess(userID uint, userRole string, articleID uint) (bool, error) {
	if model.IsStaffRole(userRole) {
		return true, nil
	}
	ok, err := s.articleRepo.HasAccess(userID, articleID)
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	article, err := s.articleRepo.FindByID(articleID)
	if err != nil || article == nil {
		return false, err
	}
	if article.SeriesID == nil {
		return false, nil
	}
	return s.seriesRepo.HasAccess(userID, *article.SeriesID)
}
