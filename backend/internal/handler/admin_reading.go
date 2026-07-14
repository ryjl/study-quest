package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"

	"studyquest/backend/internal/service"
)

// errInvalidTargetType is returned by readingGrant/readingRevoke when the
// target_type is not one of series/book/article.
var errInvalidTargetType = errors.New("invalid target_type: must be series, book, or article")

// Admin Reading Room handlers — series / books / articles CRUD + access grant.
// All methods are on *adminHandler (declared in the AdminHandler interface) and
// follow the same inline-request-struct + respondError + gin.H{"status":...}
// convention as admin_content.go / admin_user.go.

// ── Series ──

func (h *adminHandler) ListReadingSeries(c *gin.Context) {
	series, err := h.readingSeriesRepo.List("", 0, nil)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]readingSeriesDTO, 0, len(series))
	for _, s := range series {
		out = append(out, h.toReadingSeriesDTO(s))
	}
	c.JSON(http.StatusOK, out)
}

func (h *adminHandler) GetReadingSeriesDetail(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid series ID"})
		return
	}
	series, err := h.readingSeriesRepo.FindByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if series == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Series not found"})
		return
	}
	books, _ := h.readingBookRepo.ListBySeries(id)
	articles, _ := h.readingArticleRepo.ListBySeries(id)
	bookDTOs := make([]readingBookDTO, 0, len(books))
	for _, b := range books {
		bookDTOs = append(bookDTOs, h.toReadingBookDTO(b))
	}
	articleDTOs := make([]readingArticleDTO, 0, len(articles))
	for _, a := range articles {
		articleDTOs = append(articleDTOs, h.toReadingArticleDTO(a))
	}
	c.JSON(http.StatusOK, gin.H{
		"series":   h.toReadingSeriesDTO(*series),
		"books":    bookDTOs,
		"articles": articleDTOs,
	})
}

func (h *adminHandler) CreateReadingSeries(c *gin.Context) {
	var req struct {
		Title       string `json:"title" binding:"required"`
		Description string `json:"description"`
		Grade       string `json:"grade" binding:"required"`
		Subject     string `json:"subject" binding:"required"`
		CoverURL    string `json:"cover_url"`
		SortOrder   int    `json:"sort_order"`
		TagIDs      []uint `json:"tag_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	subjectID, err := h.resolveSubjectID(req.Subject)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	series, err := h.readingSeriesService.CreateSeries(req.Title, req.Description, parseGrades(req.Grade), subjectID, req.CoverURL, req.SortOrder, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.toReadingSeriesDTO(*series))
}

func (h *adminHandler) UpdateReadingSeries(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid series ID"})
		return
	}
	var req struct {
		Title       string `json:"title" binding:"required"`
		Description string `json:"description"`
		Grade       string `json:"grade" binding:"required"`
		Subject     string `json:"subject" binding:"required"`
		CoverURL    string `json:"cover_url"`
		SortOrder   int    `json:"sort_order"`
		TagIDs      []uint `json:"tag_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	subjectID, err := h.resolveSubjectID(req.Subject)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	series, err := h.readingSeriesService.UpdateSeries(id, req.Title, req.Description, parseGrades(req.Grade), subjectID, req.CoverURL, req.SortOrder, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	if series == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Series not found"})
		return
	}
	c.JSON(http.StatusOK, h.toReadingSeriesDTO(*series))
}

func (h *adminHandler) DeleteReadingSeries(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid series ID"})
		return
	}
	if err := h.readingSeriesService.DeleteSeries(id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ── Books ──

func (h *adminHandler) ListReadingBooks(c *gin.Context) {
	books, err := h.readingBookRepo.List("", 0, nil, false)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]readingBookDTO, 0, len(books))
	for _, b := range books {
		out = append(out, h.toReadingBookDTO(b))
	}
	c.JSON(http.StatusOK, out)
}

func (h *adminHandler) CreateReadingBook(c *gin.Context) {
	var req struct {
		SeriesID         uint   `json:"series_id"`
		SortOrder        int    `json:"sort_order"`
		Title            string `json:"title" binding:"required"`
		FileRelativePath string `json:"file_relative_path" binding:"required"`
		CoverURL         string `json:"cover_url"`
		Grade            string `json:"grade" binding:"required"`
		Subject          string `json:"subject" binding:"required"`
		TagIDs           []uint `json:"tag_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	subjectID, err := h.resolveSubjectID(req.Subject)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	book, err := h.readingBookService.CreateBook(req.SeriesID, req.SortOrder, req.Title, req.FileRelativePath, req.CoverURL, parseGrades(req.Grade), subjectID, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.toReadingBookDTO(*book))
}

func (h *adminHandler) UpdateReadingBook(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book ID"})
		return
	}
	var req struct {
		SeriesID         uint   `json:"series_id"`
		SortOrder        int    `json:"sort_order"`
		Title            string `json:"title" binding:"required"`
		FileRelativePath string `json:"file_relative_path" binding:"required"`
		CoverURL         string `json:"cover_url"`
		Grade            string `json:"grade" binding:"required"`
		Subject          string `json:"subject" binding:"required"`
		TagIDs           []uint `json:"tag_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	subjectID, err := h.resolveSubjectID(req.Subject)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	book, err := h.readingBookService.UpdateBook(id, req.SeriesID, req.SortOrder, req.Title, req.FileRelativePath, req.CoverURL, parseGrades(req.Grade), subjectID, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	if book == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}
	c.JSON(http.StatusOK, h.toReadingBookDTO(*book))
}

func (h *adminHandler) DeleteReadingBook(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book ID"})
		return
	}
	if err := h.readingBookService.DeleteBook(id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ── Articles ──

func (h *adminHandler) ListReadingArticles(c *gin.Context) {
	articles, err := h.readingArticleRepo.List("", 0, nil, false)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]readingArticleDTO, 0, len(articles))
	for _, a := range articles {
		out = append(out, h.toReadingArticleDTO(a))
	}
	c.JSON(http.StatusOK, out)
}

func (h *adminHandler) CreateReadingArticle(c *gin.Context) {
	var req struct {
		SeriesID         uint   `json:"series_id"`
		SortOrder        int    `json:"sort_order"`
		Title            string `json:"title" binding:"required"`
		SourceURL        string `json:"source_url" binding:"required"`
		WhitelistDomains string `json:"whitelist_domains"`
		CoverURL         string `json:"cover_url"`
		Grade            string `json:"grade" binding:"required"`
		Subject          string `json:"subject" binding:"required"`
		TagIDs           []uint `json:"tag_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	subjectID, err := h.resolveSubjectID(req.Subject)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	article, err := h.readingArticleService.CreateArticle(req.SeriesID, req.SortOrder, req.Title, req.SourceURL, normalizeWhitelistJSON(req.WhitelistDomains), req.CoverURL, parseGrades(req.Grade), subjectID, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.toReadingArticleDTO(*article))
}

func (h *adminHandler) UpdateReadingArticle(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid article ID"})
		return
	}
	var req struct {
		SeriesID         uint   `json:"series_id"`
		SortOrder        int    `json:"sort_order"`
		Title            string `json:"title" binding:"required"`
		SourceURL        string `json:"source_url" binding:"required"`
		WhitelistDomains string `json:"whitelist_domains"`
		CoverURL         string `json:"cover_url"`
		Grade            string `json:"grade" binding:"required"`
		Subject          string `json:"subject" binding:"required"`
		TagIDs           []uint `json:"tag_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	subjectID, err := h.resolveSubjectID(req.Subject)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	article, err := h.readingArticleService.UpdateArticle(id, req.SeriesID, req.SortOrder, req.Title, req.SourceURL, normalizeWhitelistJSON(req.WhitelistDomains), req.CoverURL, parseGrades(req.Grade), subjectID, req.TagIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	if article == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Article not found"})
		return
	}
	c.JSON(http.StatusOK, h.toReadingArticleDTO(*article))
}

func (h *adminHandler) DeleteReadingArticle(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid article ID"})
		return
	}
	if err := h.readingArticleService.DeleteArticle(id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ── Access ──

// GrantReadingAccess grants a user access to a reading resource. target_type is
// "series" | "book" | "article".
func (h *adminHandler) GrantReadingAccess(c *gin.Context) {
	var req struct {
		UserID     uint   `json:"user_id" binding:"required"`
		TargetType string `json:"target_type" binding:"required"`
		TargetID   uint   `json:"target_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	if err := h.readingGrant(req.UserID, req.TargetType, req.TargetID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "granted"})
}

func (h *adminHandler) RevokeReadingAccess(c *gin.Context) {
	var req struct {
		UserID     uint   `json:"user_id" binding:"required"`
		TargetType string `json:"target_type" binding:"required"`
		TargetID   uint   `json:"target_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	if err := h.readingRevoke(req.UserID, req.TargetType, req.TargetID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

// BulkReadingAccess grants or revokes ALL reading resources of all three types
// for a user. action = "grant_all" | "revoke_all".
func (h *adminHandler) BulkReadingAccess(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	var req struct {
		Action string `json:"action" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	switch req.Action {
	case "grant_all":
		// Articles have no storage dimension (web URLs) — always grant in bulk.
		if err := h.readingArticleRepo.GrantAll(id); err != nil {
			respondError(c, err)
			return
		}
		// Series + books DO have a storage dimension (series access implies
		// child-book access via StreamBook's CanAccess inheritance). When the
		// user has a non-empty whitelist we grant per-item, skipping any series
		// or book whose source isn't allowed — otherwise a series grant would
		// smuggle through access to a restricted book. Empty whitelist = the
		// fast bulk GrantAll path (unrestricted).
		if h.storageSourceRepo != nil {
			wl, werr := h.storageSourceRepo.WhitelistForUser(id)
			if werr != nil {
				// Fail-closed: can't enforce the gate without the whitelist.
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read storage whitelist"})
				return
			}
			if len(wl) > 0 {
				// Load every book ONCE (instead of ListBySeries per series) and
				// group source ids by series in memory — the per-series loop
				// below then consults this map instead of issuing N queries.
				allBooks, berr := h.readingBookRepo.List("", 0, nil, false)
				if berr != nil {
					respondError(c, berr)
					return
				}
				seriesSources := make(map[uint][]uint)
				for _, b := range allBooks {
					if b.SeriesID != nil && b.SourceID != nil {
						seriesSources[*b.SeriesID] = append(seriesSources[*b.SeriesID], *b.SourceID)
					}
				}
				seriesGranted, seriesSkipped := 0, 0
				allSeries, serr := h.readingSeriesRepo.List("", 0, nil)
				if serr != nil {
					respondError(c, serr)
					return
				}
				for _, s := range allSeries {
					// A series grant implies access to ALL its books (via
					// StreamBook's CanAccess series inheritance), so the series
					// is allowed only if EVERY one of its books' sources is in
					// the whitelist. checkStorageWhitelist handles that (it
					// rejects if any needed source is missing).
					if werr := checkStorageWhitelist(h.storageSourceRepo, id, seriesSources[s.ID]); werr != nil {
						seriesSkipped++
						continue
					}
					if err := h.readingSeriesRepo.GrantAccess(id, s.ID); err != nil {
						respondError(c, err)
						return
					}
					seriesGranted++
				}
				// Grant each standalone book (and the allowed-source books of
				// a skipped series) individually. Books whose series was just
				// granted are re-granted harmlessly (idempotent upsert).
				booksGranted, booksSkipped := 0, 0
				for _, b := range allBooks {
					var ids []uint
					if b.SourceID != nil {
						ids = []uint{*b.SourceID}
					}
					if werr := checkStorageWhitelist(h.storageSourceRepo, id, ids); werr != nil {
						booksSkipped++
						continue
					}
					if err := h.readingBookRepo.GrantAccess(id, b.ID); err != nil {
						respondError(c, err)
						return
					}
					booksGranted++
				}
				c.JSON(http.StatusOK, gin.H{
					"status":  "success",
					"message": fmt.Sprintf("已授权 %d 套/%d 本，跳过 %d 套/%d 本（存储源白名单）", seriesGranted, booksGranted, seriesSkipped, booksSkipped),
				})
				return
			}
		}
		// No whitelist (or feature unwired) → unrestricted bulk grant.
		if err := h.readingSeriesRepo.GrantAll(id); err != nil {
			respondError(c, err)
			return
		}
		if err := h.readingBookRepo.GrantAll(id); err != nil {
			respondError(c, err)
			return
		}
	case "revoke_all":
		if err := h.readingSeriesRepo.RevokeAll(id); err != nil {
			respondError(c, err)
			return
		}
		if err := h.readingBookRepo.RevokeAll(id); err != nil {
			respondError(c, err)
			return
		}
		if err := h.readingArticleRepo.RevokeAll(id); err != nil {
			respondError(c, err)
			return
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown action: " + req.Action})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// readingSourceIDsForGrant expands a reading grant target into the set of
// storage source ids it would expose. Articles have no source dimension (nil);
// books contribute their own SourceID; a series contributes the union of its
// books' source ids (series access implies child access).
func (h *adminHandler) readingSourceIDsForGrant(targetType string, targetID uint) ([]uint, error) {
	switch targetType {
	case "series":
		books, err := h.readingBookRepo.ListBySeries(targetID)
		if err != nil {
			return nil, err
		}
		ids := make([]uint, 0, len(books))
		for _, b := range books {
			if b.SourceID != nil {
				ids = append(ids, *b.SourceID)
			}
		}
		return ids, nil
	case "book":
		book, err := h.readingBookRepo.FindByID(targetID)
		if err != nil {
			return nil, err
		}
		if book == nil {
			// A miss should NOT be treated as "allowed" — surface it so the
			// caller refuses instead of silently granting access to nothing.
			return nil, fmt.Errorf("reading book %d not found", targetID)
		}
		if book.SourceID == nil {
			return nil, nil
		}
		return []uint{*book.SourceID}, nil
	case "article":
		return nil, nil
	}
	return nil, nil
}

func (h *adminHandler) readingGrant(userID uint, targetType string, targetID uint) error {
	// Storage-source whitelist gate (防呆), mirroring the course grant gate.
	if h.storageSourceRepo != nil {
		sourceIDs, serr := h.readingSourceIDsForGrant(targetType, targetID)
		if serr != nil {
			return serr
		}
		if werr := checkStorageWhitelist(h.storageSourceRepo, userID, sourceIDs); werr != nil {
			return werr
		}
	}
	switch targetType {
	case "series":
		return h.readingSeriesRepo.GrantAccess(userID, targetID)
	case "book":
		return h.readingBookRepo.GrantAccess(userID, targetID)
	case "article":
		return h.readingArticleRepo.GrantAccess(userID, targetID)
	}
	return errInvalidTargetType
}

func (h *adminHandler) readingRevoke(userID uint, targetType string, targetID uint) error {
	switch targetType {
	case "series":
		return h.readingSeriesRepo.RevokeAccess(userID, targetID)
	case "book":
		return h.readingBookRepo.RevokeAccess(userID, targetID)
	case "article":
		return h.readingArticleRepo.RevokeAccess(userID, targetID)
	}
	return errInvalidTargetType
}

// ── Folder Import ──

// PreviewReadingImport scans a storage folder and returns a preview tree.
// GET /admin/api/reading-import/preview-tree?path=
func (h *adminHandler) PreviewReadingImport(c *gin.Context) {
	path := strings.TrimSpace(c.Query("path"))
	if path == "" {
		path = "/"
	}
	tree, err := h.readingImportService.PreviewReadingFolder(path, parseSourceIDQuery(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to scan folder: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, tree)
}

// ExecuteReadingImport creates a ReadingSeries + ReadingBook rows from a preview tree.
// POST /admin/api/reading-import/execute
func (h *adminHandler) ExecuteReadingImport(c *gin.Context) {
	var req service.ExecuteReadingImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	if err := h.readingImportService.ExecuteReadingImport(&req); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// ── Whitelist suggestion ──

// SuggestWhitelist fetches the article URL server-side, extracts all unique
// domains from the static HTML (img/script/link/iframe sources + CSS url()),
// and returns them as a suggested whitelist for the admin to review.
//
// Known limitation: JS-injected resources (e.g. mpvideo.qpic.cn video shards)
// won't appear in static HTML. The 4 core WeChat domains cover the vast
// majority of articles; the admin can manually add any missing domain.
func (h *adminHandler) SuggestWhitelist(c *gin.Context) {
	var req struct {
		SourceURL string `json:"source_url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	domains, err := extractDomainsFromURL(req.SourceURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch article: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"domains": domains})
}

// urlAttrSet is the set of HTML attributes whose values may contain resource
// URLs. The critical entry here is "data-src" — WeChat 公众号 articles store
// the real image URL in data-src (for lazy loading), NOT in src. An extractor
// that only checks src will miss every body image.
var urlAttrSet = map[string]bool{
	"src":          true,
	"href":         true,
	"data-src":     true,
	"data-srcset":  true,
	"srcset":       true,
	"poster":       true,
	"data-url":     true,
	"data-original": true,
}

// cssURLRe matches url(...) references inside inline CSS / style attributes.
var cssURLRe = regexp.MustCompile(`url\(\s*['"]?([^'")\s]+)`)

// extractDomainsFromURL fetches the given URL and extracts all unique hostnames
// from resource references in the static HTML. Returns a sorted slice. Always
// includes the source URL's own host as a fallback — JS-rendered pages (e.g.
// Vue/Nuxt H5 apps) may have no resource URLs in their static HTML, but the
// page itself still loads from its own host and internal navigation within that
// host must be allowed.
func extractDomainsFromURL(rawURL string) ([]string, error) {
	client := &http.Client{Timeout: 12 * 1000_000_000} // 12s
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	// Mobile + MicroMessenger UA: WeChat serves mobile-optimized HTML to this UA,
	// which is what the in-app WebView actually renders.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/120.0.0.0 Mobile Safari/537.36 MMWEBID/1234 MicroMessenger/8.0.40")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("article URL returned HTTP " + resp.Status)
	}

	hosts := collectDomainsFromHTML(resp.Body)

	// Always include the source URL's own host. For JS-rendered H5 pages where
	// the static HTML has no resource URLs, this is the only domain we can find
	// — but it's also the most important one (the page itself + internal nav).
	if sourceURL, err := url.Parse(rawURL); err == nil && sourceURL.Host != "" {
		hosts[sourceURL.Host] = true
	}

	out := make([]string, 0, len(hosts))
	for h := range hosts {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// collectDomainsFromHTML streams the HTML body through x/net/html tokenizer,
// extracting hostnames from all resource-bearing attributes + inline CSS url().
func collectDomainsFromHTML(body io.Reader) map[string]bool {
	hosts := make(map[string]bool)
	collect := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "data:") || strings.HasPrefix(raw, "blob:") {
			return
		}
		// srcset may contain multiple "url descriptor, url descriptor" entries.
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			// Strip descriptor (e.g. "url 2x" → "url").
			if idx := strings.IndexByte(part, ' '); idx > 0 {
				part = part[:idx]
			}
			if u, err := url.Parse(part); err == nil && u.Host != "" {
				hosts[u.Host] = true
			}
		}
	}

	z := html.NewTokenizer(body)
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			for {
				k, v, more := z.TagAttr()
				if urlAttrSet[string(k)] {
					collect(string(v))
				}
				// Also scan style="..." for url(...) references.
				if string(k) == "style" {
					for _, m := range cssURLRe.FindAllStringSubmatch(string(v), -1) {
						collect(m[1])
					}
				}
				if !more {
					break
				}
			}
		}
	}

	return hosts
}

// normalizeWhitelistJSON coerces the input into a valid JSON []string. Accepts
// an already-JSON array, a comma-separated string, or empty (→ "[]"). This lets
// the admin SPA send either a raw JSON array string or a simple comma list.
func normalizeWhitelistJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "[]"
	}
	// If it already parses as a []string, keep it as-is.
	var arr []string
	if json.Unmarshal([]byte(raw), &arr) == nil {
		if arr == nil {
			return "[]"
		}
		return raw
	}
	// Treat as comma-separated.
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	buf, _ := json.Marshal(out)
	return string(buf)
}
