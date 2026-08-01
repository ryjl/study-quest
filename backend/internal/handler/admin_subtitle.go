package handler

import (
	"io"
	"net/http"
	"strconv"
	"github.com/gin-gonic/gin"
)

// Code split from admin_content.go for navigability.
// Subtitle CRUD + extract + automatch.

func (h *adminHandler) ListSubtitles(c *gin.Context) {
	episodeID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid episode ID"})
		return
	}

	subs, err := h.episodeService.ListSubtitles(episodeID)
	if err != nil {
		respondError(c, err)
		return
	}

	out := make([]subtitleDTO, 0, len(subs))
	for _, s := range subs {
		out = append(out, toSubtitleDTO(s))
	}
	c.JSON(http.StatusOK, out)
}

func (h *adminHandler) SaveSubtitle(c *gin.Context) {
	episodeID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid episode ID"})
		return
	}

	var req struct {
		Language   string `json:"language" binding:"required"`
		Label      string `json:"label" binding:"required"`
		SrtContent string `json:"srt_content" binding:"required"`
	}

	if !bindJSON(c, &req) { return }

	// Admin manual upload: source=manual so the polish pipeline skips it
	// (human-made subtitles are already correct).
	err = h.episodeService.SaveSubtitleWithSource(episodeID, req.Language, req.Label, req.SrtContent, "manual")
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "saved"})
}

// ExtractSubtitle pulls an embedded subtitle stream out of the episode's video
// container (ffmpeg -map 0:<idx> -c:s webvtt) and persists it with
// source="embedded". The episode must have been probed first so the stream
// list is visible in media_meta_json; the client passes the stream_index it
// picked from that list.
//
// Bitmap subtitle codecs (PGS / VOBSUB / DVB) can't be transcoded to text —
// the service returns ErrBitmapSubtitleNotSupported, which respondError maps
// to a 400 with an actionable "use Whisper" hint.
func (h *adminHandler) ExtractSubtitle(c *gin.Context) {
	episodeID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid episode ID"})
		return
	}

	var req struct {
		StreamIndex int    `json:"stream_index"`
		Language    string `json:"language"`
		Label       string `json:"label"`
	}
	if !bindJSON(c, &req) { return }
	// Defaults are applied by SaveSubtitleWithSource, but mirroring them here
	// keeps the openAPI-style contract obvious and lets the client omit them.

	err = h.episodeService.ExtractEmbeddedSubtitle(episodeID, req.StreamIndex, req.Language, req.Label)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "saved"})
}

// GetSubtitle returns the full subtitle (including VTT content) for admin
// preview. ListSubtitles deliberately omits vtt_content (it can be large and
// the list view only needs metadata); this endpoint fetches one by id when the
// admin wants to read the actual text.
//
// raw_vtt_content is the immutable pre-polish snapshot (see model.Subtitle
// doc). It's populated only when the subtitle has been polished
// (source="llm_optimized") — for whisper/embedded/manual tracks it's empty,
// since there's no "prior version" to compare against. The admin subtitle
// version UI uses this to render a polished-vs-original diff so polish
// results are auditable (a polish run that introduced a hallucinated rewrite
// is visible at a glance, even though validation no longer blocks such
// rewrites upstream).
func (h *adminHandler) GetSubtitle(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid subtitle ID"})
		return
	}
	sub, err := h.episodeService.GetSubtitleByID(uint(id))
	if err != nil {
		respondError(c, err)
		return
	}
	if sub == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Subtitle not found"})
		return
	}
	// Only surface the raw snapshot when it's meaningful — i.e. when the
	// current VttContent is a polished version of something else. For raw
	// whisper tracks the two would be identical (or raw is empty for legacy
	// rows), so returning it would just bloat the response for no UI value.
	// The frontend gates the version toggle on source=="llm_optimized" anyway,
	// but keeping the field empty here too avoids any confusion.
	rawVtt := ""
	if sub.Source == "llm_optimized" {
		rawVtt = sub.RawVttContent
	}
	c.JSON(http.StatusOK, gin.H{
		"id":               sub.ID,
		"episode_id":       sub.EpisodeID,
		"language":         sub.Language,
		"label":            sub.Label,
		"vtt_content":      sub.VttContent,
		"raw_vtt_content":  rawVtt,
		"source":           sub.Source,
		"optimized":        sub.Optimized,
		"created_at":       formatTime(sub.CreatedAt),
	})
}

func (h *adminHandler) DeleteSubtitle(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid subtitle ID"})
		return
	}

	if err := h.episodeService.DeleteSubtitle(uint(id)); err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) AutoMatchSubtitle(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no subtitle file uploaded"})
		return
	}

	videoBasename := c.PostForm("video_basename")
	if videoBasename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "video_basename is required"})
		return
	}

	language := c.PostForm("language")
	if language == "" {
		language = "zh-CN"
	}

	label := c.PostForm("label")
	if label == "" {
		label = "中文"
	}

	var sizeVal *int64
	sizeStr := c.PostForm("video_size")
	if sizeStr != "" {
		if s, err := strconv.ParseInt(sizeStr, 10, 64); err == nil {
			sizeVal = &s
		}
	}

	pathHint := c.PostForm("video_path_hint")

	fileSrc, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open uploaded file"})
		return
	}
	defer fileSrc.Close()

	fileBytes, err := io.ReadAll(fileSrc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read uploaded file"})
		return
	}
	srtContent := string(fileBytes)

	// Search matching episodes in database
	episodes, err := h.episodeRepo.FindByCriteria(videoBasename, sizeVal, pathHint)
	if err != nil {
		respondError(c, err)
		return
	}

	if len(episodes) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no matching video episode found"})
		return
	}

	if len(episodes) > 1 {
		c.JSON(http.StatusConflict, gin.H{"error": "multiple matching video episodes found, please refine parameters (e.g. provide size or path hint)"})
		return
	}

	// Exactly one matched
	ep := episodes[0]
	err = h.episodeService.SaveSubtitle(ep.ID, language, label, srtContent)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "success",
		"episode_id": ep.ID,
		"title":      ep.Title,
	})
}
