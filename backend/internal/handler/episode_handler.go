package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// EpisodeHandler manages details, subtitle streams, and AI content.
type EpisodeHandler interface {
	GetEpisodeByID(c *gin.Context)
	Stream(c *gin.Context)
	GetPlayInfo(c *gin.Context)
	GetSubtitle(c *gin.Context)
	GetSubtitleVTT(c *gin.Context)
	GetAttachments(c *gin.Context)
	StreamAttachment(c *gin.Context)
}

type episodeHandler struct {
	episodeService    service.EpisodeService
	progressService   service.ProgressService
	settingsRepo      repository.SettingsRepository
	unlockService     service.UnlockService
	storageSourceRepo repository.StorageSourceRepository
}

// NewEpisodeHandler creates an instance of EpisodeHandler. The storage source
// repo backs the access-time whitelist gate in GetPlayInfo (nil = gate
// disabled, for setups that haven't configured sources).
func NewEpisodeHandler(es service.EpisodeService, ps service.ProgressService, sr repository.SettingsRepository, us service.UnlockService, ssr repository.StorageSourceRepository) EpisodeHandler {
	return &episodeHandler{episodeService: es, progressService: ps, settingsRepo: sr, unlockService: us, storageSourceRepo: ssr}
}

func (h *episodeHandler) GetEpisodeByID(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID format"})
		return
	}

	ep, err := h.episodeService.GetEpisodeByID(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query episode: " + err.Error()})
		return
	}

	if ep == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "episode not found"})
		return
	}

	c.JSON(http.StatusOK, ep)
}

func (h *episodeHandler) GetSubtitleVTT(c *gin.Context) {
	// The route is /subtitles/:id.vtt. Gin does NOT split on the dot — the full
	// param name is "id.vtt", so c.Param("id") returns "". We read the whole
	// segment ("1.vtt") and strip the ".vtt" suffix ourselves.
	raw := c.Param("id.vtt")
	idStr := strings.TrimSuffix(raw, ".vtt")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid subtitle ID format")
		return
	}

	sub, err := h.episodeService.GetSubtitleByID(uint(id))
	if err != nil {
		c.String(http.StatusInternalServerError, "failed to query subtitle: "+err.Error())
		return
	}

	if sub == nil {
		c.String(http.StatusNotFound, "subtitle not found")
		return
	}

	vttContent := srtToVtt(sub.SrtContent)

	c.Header("Content-Type", "text/vtt; charset=utf-8")
	c.String(http.StatusOK, vttContent)
}

func srtToVtt(srt string) string {
	vtt := "WEBVTT\n\n"
	content := strings.ReplaceAll(srt, ",", ".")
	vtt += content
	return vtt
}

// checkEpisodeSourceAccess is the storage-source allow-list gate shared by all
// episode media endpoints (Stream, StreamAttachment, GetPlayInfo). A non-staff
// user may reach an episode only if its SourceID is in the user's allow-list
// (default-deny: an empty list allows nothing). Returns true to proceed; on a
// denial it writes the response (401/403/404/500) and returns false so the
// caller can `return` immediately.
//
// Fail-closed: a non-staff request with no trustworthy userID is rejected
// (401). Staff roles (admin/parent) bypass entirely. A nil storageSourceRepo
// (feature not wired) short-circuits to allow. An episode with no SourceID is
// denied (it can't stream without a bound source).
func (h *episodeHandler) checkEpisodeSourceAccess(c *gin.Context, episodeID uint) bool {
	if h.storageSourceRepo == nil {
		return true
	}
	role, _ := c.Get("userRole")
	roleStr, _ := role.(string)
	if model.IsStaffRole(roleStr) {
		return true
	}
	uidVal, ok := c.Get("userID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return false
	}
	uid, ok := uidVal.(uint)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return false
	}
	ep, err := h.episodeService.GetEpisodeByID(episodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load episode"})
		return false
	}
	if ep == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "episode not found"})
		return false
	}
	if ep.SourceID == nil {
		// No source bound (admin-created but never imported, or stale data).
		// With the global fallback removed this episode can't stream, so deny.
		c.JSON(http.StatusForbidden, gin.H{"error": "该课时未绑定存储源"})
		return false
	}
	allowed, aerr := h.storageSourceRepo.IsAllowed(uid, *ep.SourceID)
	if aerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check storage access"})
		return false
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "该用户不被允许访问此存储源"})
		return false
	}
	return true
}

func (h *episodeHandler) Stream(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID format"})
		return
	}

	// Storage-source whitelist gate (访问兜底). Stream hands out the direct
	// netdisk URL via 302, so it MUST be gated the same way as GetPlayInfo —
	// otherwise a user can bypass the whitelist by calling /stream directly
	// with a guessed episode id. Staff bypass; fail-closed on missing identity.
	if !h.checkEpisodeSourceAccess(c, uint(id)) {
		return
	}

	link, err := h.episodeService.GetStreamURL(uint(id), c.Request.UserAgent())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve stream link: " + err.Error()})
		return
	}

	// Apply special storage provider headers if required (like Referer for 115)
	for k, v := range link.Header {
		c.Header(k, v)
	}

	// Rewrite localhost/127.0.0.1 back to server's request IP for remote clients
	streamURL := rewriteLocalhostURL(link.URL, c.Request.Host)

	c.Redirect(http.StatusFound, streamURL)
}

func (h *episodeHandler) GetPlayInfo(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID format"})
		return
	}

	// Unlock gate: a student/teen must have the episode visible under their
	// (user, course) unlock resolution before we hand out the stream URL.
	// Without this, the episode-list filtering could be bypassed by simply
	// guessing an episode id. Admin/parent roles bypass the gate (they manage
	// content, not consume it under a drip schedule).
	//
	// This gate must FAIL CLOSED: if we can't establish a valid userID for a
	// non-admin role, we reject (401) rather than fall through to GetStreamURL.
	// The route sits behind UserAuthMiddleware which normally injects userID,
	// but the gate as a security control must not depend on that always being
	// true — a missing/invalid userID on a non-admin request is treated as an
	// access denial, never an implicit grant.
	if h.unlockService != nil {
		roleVal, hasRole := c.Get("userRole")
		role, _ := roleVal.(string)
		if role != "admin" && role != "parent" {
			uidVal, hasUID := c.Get("userID")
			uid, uidOK := uidVal.(uint)
			if !hasRole || !hasUID || !uidOK {
				// No trustworthy identity on a non-admin request → deny.
				c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
				return
			}
			visible, gerr := h.unlockService.IsEpisodeVisible(uid, uint(id))
			if gerr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check episode access"})
				return
			}
			if !visible {
				c.JSON(http.StatusForbidden, gin.H{"error": "episode is locked"})
				return
			}
		}
	}

	// Storage-source allow-list gate (访问兜底). Shared with Stream /
	// StreamAttachment via checkEpisodeSourceAccess so every media-exfiltration
	// endpoint enforces the same constraint. Default-deny: an empty allow-list
	// denies everything; staff bypass; fail-closed on missing identity.
	if !h.checkEpisodeSourceAccess(c, uint(id)) {
		return
	}

	link, err := h.episodeService.GetStreamURL(uint(id), c.Request.UserAgent())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve stream link: " + err.Error()})
		return
	}

	// Rewrite localhost/127.0.0.1 back to server's request IP for remote clients
	streamURL := rewriteLocalhostURL(link.URL, c.Request.Host)

	// Fetch user progress if authenticated
	var progressObj interface{} = nil
	if userIDVal, exists := c.Get("userID"); exists {
		if userID, ok := userIDVal.(uint); ok {
			prog, err := h.progressService.GetProgress(userID, uint(id))
			if err == nil && prog != nil {
				progressObj = gin.H{
					"last_position_seconds": prog.LastPositionSeconds,
					"watch_seconds":         prog.WatchSeconds,
					"is_completed":          prog.IsCompleted,
				}
			}
		}
	}

	// Fetch subtitles list
	var subtitlesList []gin.H = []gin.H{}
	subs, err := h.episodeService.ListSubtitles(uint(id))
	if err == nil {
		for _, s := range subs {
			subtitlesList = append(subtitlesList, gin.H{
				"id":       s.ID,
				"language": s.Language,
				"label":    s.Label,
				"url":      "/api/v1/subtitles/" + strconv.Itoa(int(s.ID)) + ".vtt",
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"url":       streamURL,
		"headers":   link.Header,
		"progress":  progressObj,
		"subtitles": subtitlesList,
	})
}


func rewriteLocalhostURL(originalURL string, requestHost string) string {
	reqHostOnly := requestHost
	if idx := strings.Index(requestHost, ":"); idx != -1 {
		reqHostOnly = requestHost[:idx]
	}

	u, err := url.Parse(originalURL)
	if err != nil {
		return originalURL
	}

	hostname := u.Hostname()
	isLocal := hostname == "localhost" || hostname == "127.0.0.1"

	if isLocal {
		port := u.Port()
		if port != "" {
			u.Host = reqHostOnly + ":" + port
		} else {
			u.Host = reqHostOnly
		}
		return u.String()
	}
	return originalURL
}

func (h *episodeHandler) GetSubtitle(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID format"})
		return
	}

	sub, err := h.episodeService.GetSubtitle(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query subtitle: " + err.Error()})
		return
	}

	if sub == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no subtitles generated for this episode"})
		return
	}

	c.JSON(http.StatusOK, sub)
}

func (h *episodeHandler) GetAttachments(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID format"})
		return
	}

	ep, err := h.episodeService.GetEpisodeByID(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query episode: " + err.Error()})
		return
	}

	if ep == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "episode not found"})
		return
	}

	var attachments []interface{}
	if ep.AttachmentJSON != "" {
		if err := json.Unmarshal([]byte(ep.AttachmentJSON), &attachments); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse attachments configuration"})
			return
		}
	}

	c.JSON(http.StatusOK, attachments)
}

// StreamAttachment resolves the Nth attachment of an episode into a 302
// redirect, mirroring Stream() but for non-video files (PDFs, docs, ...).
// Index is provided via the :index path parameter.
func (h *episodeHandler) StreamAttachment(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID format"})
		return
	}

	indexStr := c.Param("index")
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid attachment index"})
		return
	}

	// Storage-source whitelist gate — same rationale as Stream: attachments are
	// gated by the episode's source, and this endpoint hands out a direct URL.
	if !h.checkEpisodeSourceAccess(c, uint(id)) {
		return
	}

	link, _, err := h.episodeService.GetAttachmentStreamURL(uint(id), index, c.Request.UserAgent())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve attachment: " + err.Error()})
		return
	}

	for k, v := range link.Header {
		c.Header(k, v)
	}

	streamURL := rewriteLocalhostURL(link.URL, c.Request.Host)
	c.Redirect(http.StatusFound, streamURL)
}
