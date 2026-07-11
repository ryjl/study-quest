package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// ReadingHandler serves the /api/v1/readings* client endpoints (Flutter).
// Mirrors EpisodeHandler/CourseHandler: the stream endpoint 302-redirects to the
// storage provider URL (same as episode.Stream), and access is gated fail-closed
// for non-admin/parent roles.
type ReadingHandler interface {
	GetReadingRoom(c *gin.Context)
	GetSeries(c *gin.Context)
	StreamBook(c *gin.Context)
	GetBookProgress(c *gin.Context)
	ReportBookProgress(c *gin.Context)
	GetArticle(c *gin.Context)
}

type readingHandler struct {
	seriesService  service.ReadingSeriesService
	bookService    service.ReadingBookService
	articleService service.ReadingArticleService
	subjectRepo    repository.SubjectRepository
}

// NewReadingHandler creates an instance of ReadingHandler.
func NewReadingHandler(ss service.ReadingSeriesService, bs service.ReadingBookService, as service.ReadingArticleService, subj repository.SubjectRepository) ReadingHandler {
	return &readingHandler{
		seriesService:  ss,
		bookService:    bs,
		articleService: as,
		subjectRepo:    subj,
	}
}

// subjectKeyOf resolves a SubjectID to its key string; "" on miss.
func (h *readingHandler) subjectKeyOf(subjectID uint) string {
	if subj, _ := h.subjectRepo.FindByID(subjectID); subj != nil {
		return subj.Key
	}
	return ""
}

func (h *readingHandler) toClientSeriesDTO(s service.ReadingSeriesCard) clientReadingSeriesDTO {
	return clientReadingSeriesDTO{
		ID:           s.ID,
		Title:        s.Title,
		Description:  s.Description,
		Grade:        string(s.Grade),
		Subject:      h.subjectKeyOf(s.SubjectID),
		CoverURL:     s.CoverURL,
		Tags:         s.TagsJoined(),
		TagsList:     s.TagsList(),
		TagIDs:       tagIDsOf(s.Tags),
		GradeDisplay: s.GradeDisplay(),
		SortOrder:    s.SortOrder,
		BookCount:    s.BookCount,
		ArticleCount: s.ArticleCount,
	}
}

func (h *readingHandler) toClientBookDTO(b model.ReadingBook) clientReadingBookDTO {
	return clientReadingBookDTO{
		ID:        b.ID,
		SeriesID:  b.SeriesID,
		SortOrder: b.SortOrder,
		Title:     b.Title,
		FileHash:  b.FileHash,
		PageCount: b.PageCount,
		CoverURL:  b.CoverURL,
		Grade:     string(b.Grade),
		Subject:   h.subjectKeyOf(b.SubjectID),
	}
}

func (h *readingHandler) toClientArticleDTO(a model.ReadingArticle) clientReadingArticleDTO {
	var domains []string
	if a.WhitelistDomains != "" {
		_ = json.Unmarshal([]byte(a.WhitelistDomains), &domains)
	}
	if domains == nil {
		domains = []string{}
	}
	return clientReadingArticleDTO{
		ID:               a.ID,
		SeriesID:         a.SeriesID,
		SortOrder:        a.SortOrder,
		Title:            a.Title,
		SourceURL:        h.articleService.EffectiveURL(&a),
		WhitelistDomains: domains,
		CoverURL:         a.CoverURL,
		Grade:            string(a.Grade),
		Subject:          h.subjectKeyOf(a.SubjectID),
	}
}

// requireStudentIdentity extracts (userID, role) from the gin context and
// returns ok=false for a non-admin/parent request lacking trustworthy identity.
// Mirrors the fail-closed gate in episode_handler.GetPlayInfo.
func requireStudentIdentity(c *gin.Context) (uint, string, bool) {
	roleVal, hasRole := c.Get("userRole")
	role, _ := roleVal.(string)
	if role == "admin" || role == "parent" {
		return 0, role, true
	}
	uidVal, hasUID := c.Get("userID")
	uid, uidOK := uidVal.(uint)
	if !hasRole || !hasUID || !uidOK {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return 0, role, false
	}
	return uid, role, true
}

func (h *readingHandler) GetReadingRoom(c *gin.Context) {
	grade := c.Query("grade")
	subjectKey := strings.TrimSpace(c.Query("subject"))

	userIDVal, existsUserID := c.Get("userID")
	userRoleVal, existsUserRole := c.Get("userRole")
	if !existsUserID || !existsUserRole {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user authentication details missing in context"})
		return
	}
	userID := userIDVal.(uint)
	userRole := userRoleVal.(string)

	var subjectID uint
	if subjectKey != "" {
		if subj, _ := h.subjectRepo.FindByKey(subjectKey); subj != nil {
				subjectID = subj.ID
			} else {
				c.JSON(http.StatusOK, clientReadingRoomDTO{
					Series:   []clientReadingSeriesDTO{},
					Books:    []clientReadingBookDTO{},
					Articles: []clientReadingArticleDTO{},
				})
				return
			}
	}

	view, err := h.seriesService.GetReadingRoom(userID, userRole, grade, subjectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load reading room: " + err.Error()})
		return
	}

	out := clientReadingRoomDTO{
		Series:   make([]clientReadingSeriesDTO, 0, len(view.Series)),
		Books:    make([]clientReadingBookDTO, 0, len(view.Books)),
		Articles: make([]clientReadingArticleDTO, 0, len(view.Articles)),
	}
	for _, s := range view.Series {
		out.Series = append(out.Series, h.toClientSeriesDTO(s))
	}
	for _, b := range view.Books {
		out.Books = append(out.Books, h.toClientBookDTO(b))
	}
	for _, a := range view.Articles {
		out.Articles = append(out.Articles, h.toClientArticleDTO(a))
	}
	c.JSON(http.StatusOK, out)
}

// GetSeries returns a series with its child books and articles. Access-gated
// fail-closed: a student without series access gets 403. Series access implies
// child access (matching Course→Episode semantics), so all children are listed.
func (h *readingHandler) GetSeries(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid series ID format"})
		return
	}

	uid, role, ok := requireStudentIdentity(c)
	if !ok {
		return
	}

	series, err := h.seriesService.GetSeriesByID(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query series: " + err.Error()})
		return
	}
	if series == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "series not found"})
		return
	}

	// Access gate: admin/parent bypass; students need series access.
	if role != "admin" && role != "parent" {
		allowed, aerr := h.seriesService.HasSeriesAccess(uid, uint(id))
		if aerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check series access"})
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "series is locked"})
			return
		}
	}

	books, _ := h.bookService.GetBooksBySeries(uint(id))
	articles, _ := h.articleService.GetArticlesBySeries(uint(id))

	bookDTOs := make([]clientReadingBookDTO, 0, len(books))
	for _, b := range books {
		bookDTOs = append(bookDTOs, h.toClientBookDTO(b))
	}
	articleDTOs := make([]clientReadingArticleDTO, 0, len(articles))
	for _, a := range articles {
		articleDTOs = append(articleDTOs, h.toClientArticleDTO(a))
	}

	c.JSON(http.StatusOK, gin.H{
		"Series":   h.toClientSeriesDTO(service.ReadingSeriesCard{ReadingSeries: *series}),
		"Books":    bookDTOs,
		"Articles": articleDTOs,
	})
}

// StreamBook 302-redirects to the PDF's storage provider download URL, mirroring
// episode.Stream. Access-gated fail-closed via CanAccess (series inheritance).
func (h *readingHandler) StreamBook(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid book ID format"})
		return
	}

	uid, role, ok := requireStudentIdentity(c)
	if !ok {
		return
	}
	allowed, aerr := h.bookService.CanAccess(uid, role, uint(id))
	if aerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check book access"})
		return
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "book is locked"})
		return
	}

	link, err := h.bookService.GetStreamURL(uint(id), c.Request.UserAgent())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve stream link: " + err.Error()})
		return
	}
	for k, v := range link.Header {
		c.Header(k, v)
	}
	streamURL := rewriteLocalhostURL(link.URL, c.Request.Host)
	c.Redirect(http.StatusFound, streamURL)
}

func (h *readingHandler) GetBookProgress(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid book ID format"})
		return
	}
	uid, role, ok := requireStudentIdentity(c)
	if !ok {
		return
	}
	allowed, aerr := h.bookService.CanAccess(uid, role, uint(id))
	if aerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check book access"})
		return
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "book is locked"})
		return
	}
	prog, err := h.bookService.GetProgress(uid, uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load progress: " + err.Error()})
		return
	}
	if prog == nil {
		c.JSON(http.StatusOK, gin.H{"lastPage": 0})
		return
	}
	c.JSON(http.StatusOK, gin.H{"lastPage": prog.LastPage})
}

func (h *readingHandler) ReportBookProgress(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid book ID format"})
		return
	}
	uid, role, ok := requireStudentIdentity(c)
	if !ok {
		return
	}
	allowed, aerr := h.bookService.CanAccess(uid, role, uint(id))
	if aerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check book access"})
		return
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "book is locked"})
		return
	}
	var req struct {
		LastPage int `json:"lastPage"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}
	prog, err := h.bookService.ReportProgress(uid, uint(id), req.LastPage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save progress: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"lastPage": prog.LastPage})
}

func (h *readingHandler) GetArticle(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid article ID format"})
		return
	}

	uid, role, ok := requireStudentIdentity(c)
	if !ok {
		return
	}
	allowed, aerr := h.articleService.CanAccess(uid, role, uint(id))
	if aerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check article access"})
		return
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "article is locked"})
		return
	}

	article, err := h.articleService.GetArticleByID(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query article: " + err.Error()})
		return
	}
	if article == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "article not found"})
		return
	}
	c.JSON(http.StatusOK, h.toClientArticleDTO(*article))
}
