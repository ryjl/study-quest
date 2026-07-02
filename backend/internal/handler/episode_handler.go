package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	GetAIContent(c *gin.Context)
	GetAttachments(c *gin.Context)
}

type episodeHandler struct {
	episodeService  service.EpisodeService
	progressService service.ProgressService
	settingsRepo    repository.SettingsRepository
}

// NewEpisodeHandler creates an instance of EpisodeHandler.
func NewEpisodeHandler(es service.EpisodeService, ps service.ProgressService, sr repository.SettingsRepository) EpisodeHandler {
	return &episodeHandler{episodeService: es, progressService: ps, settingsRepo: sr}
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
	idStr := c.Param("id")
	// Note: id here is the subtitle ID
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

func (h *episodeHandler) Stream(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID format"})
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

func (h *episodeHandler) GetAIContent(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid episode ID format"})
		return
	}

	ai, err := h.episodeService.GetAIContent(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query AI content: " + err.Error()})
		return
	}

	if ai == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI lesson content not generated for this episode"})
		return
	}

	c.JSON(http.StatusOK, ai)
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
