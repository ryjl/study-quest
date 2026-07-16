package handler

import (
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"studyquest/backend/internal/model"

	"github.com/gin-gonic/gin"
)

// parseGrades splits a comma-separated grade string (e.g. "3,4,5" or "universal")
// into a []model.Grade. Whitespace is trimmed; invalid values are dropped.
func parseGrades(s string) []model.Grade {
	if s == "" {
		return []model.Grade{model.GradeUniversal}
	}
	parts := strings.Split(s, ",")
	out := make([]model.Grade, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		g := model.Grade(p)
		if g.Valid() {
			out = append(out, g)
		}
	}
	if len(out) == 0 {
		return []model.Grade{model.GradeUniversal}
	}
	return out
}

func (h *adminHandler) ListCourses(c *gin.Context) {
	courses, err := h.courseRepo.List("", 0, "all", nil)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]courseDTO, 0, len(courses))
	for _, cr := range courses {
		out = append(out, h.toCourseDTO(cr))
	}
	c.JSON(http.StatusOK, out)
}

// GetCourseDetail returns a single course plus its episodes and chapters in one
// round-trip — used when expanding a course card.
func (h *adminHandler) GetCourseDetail(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}
	cr, err := h.courseRepo.FindByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if cr == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Course not found"})
		return
	}
	eps, _ := h.episodeRepo.ListByCourse(id)
	chs, _ := h.chapterService.GetChaptersByCourse(id)

	epDTOs := make([]episodeDTO, 0, len(eps))
	for _, e := range eps {
		epDTOs = append(epDTOs, toEpisodeDTO(e))
	}
	// Stamp subtitle_count in one batch query (avoids an N+1 across episodes).
	ids := make([]uint, 0, len(eps))
	for _, e := range eps {
		ids = append(ids, e.ID)
	}
	if counts, cerr := h.episodeRepo.CountSubtitlesByEpisodes(ids); cerr == nil {
		withSubtitleCounts(epDTOs, counts)
	}
	chDTOs := make([]chapterDTO, 0, len(chs))
	for _, ch := range chs {
		chDTOs = append(chDTOs, toChapterDTO(ch))
	}
	c.JSON(http.StatusOK, gin.H{
		"course":   h.toCourseDTO(*cr),
		"episodes": epDTOs,
		"chapters": chDTOs,
	})
}

// GetSettings returns the storage + (non-sensitive) admin config as JSON.
func (h *adminHandler) CreateCourse(c *gin.Context) {
	var req struct {
		Title            string `json:"title" binding:"required"`
		Grades           string `json:"grades"`
		Subject          string `json:"subject" binding:"required"`
		ContentType      string `json:"content_type"`
		CoverURL         string `json:"cover_url"`
		TagIDs           []uint `json:"tag_ids"`
		AttachmentJSON   string `json:"attachment_json"`
		AIHint           string `json:"ai_hint"`
		AISummaryEnabled bool   `json:"ai_summary_enabled"`
		AIQuizEnabled    bool   `json:"ai_quiz_enabled"`
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

	grades := parseGrades(req.Grades)
	contentType := model.ContentType(req.ContentType)
	if !contentType.Valid() {
		contentType = model.ContentLearning
	}

	course, err := h.courseService.CreateCourse(req.Title, grades, subjectID, contentType, req.CoverURL, req.TagIDs, req.AttachmentJSON, req.AIHint, req.AISummaryEnabled, req.AIQuizEnabled)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, h.toCourseDTO(*course))
}

// resolveSubjectID maps a subject key (e.g. "math") to its subjects.id. Returns
// a user-facing error string if the key doesn't match any subject.
func (h *adminHandler) DeleteCourse(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	if err := h.courseService.DeleteCourse(id); err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) UpdateCourse(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	var req struct {
		Title            string `json:"title" binding:"required"`
		Grades           string `json:"grades"`
		Subject          string `json:"subject" binding:"required"`
		ContentType      string `json:"content_type"`
		CoverURL         string `json:"cover_url"`
		TagIDs           []uint `json:"tag_ids"`
		AttachmentJSON   string `json:"attachment_json"`
		AIHint           string `json:"ai_hint"`
		AISummaryEnabled bool   `json:"ai_summary_enabled"`
		AIQuizEnabled    bool   `json:"ai_quiz_enabled"`
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

	grades := parseGrades(req.Grades)
	contentType := model.ContentType(req.ContentType)
	if !contentType.Valid() {
		contentType = model.ContentLearning
	}

	// 读取旧课程,用于检测 AI 开关是否从 false→true。开关从关到开意味着
	// admin 现在想让历史字幕也参与 AI 处理,需要把该课程下所有"已有字幕"
	// 的 episode 批量入队 segment job(交给 aiService.EnqueueSegmentForCourse)。
	// 读在 Update 之前,避免更新后的值污染比较。
	oldCourse, _ := h.courseRepo.FindByID(id)

	course, err := h.courseService.UpdateCourse(id, req.Title, grades, subjectID, contentType, req.CoverURL, req.TagIDs, req.AttachmentJSON, req.AIHint, req.AISummaryEnabled, req.AIQuizEnabled)
	if err != nil {
		respondError(c, err)
		return
	}
	if course == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Course not found"})
		return
	}

	// 开关 off→on:为该课程已有字幕的 episode 批量补入 segment 队列。AI 关着
	// 时新到的字幕不会触发任何 AI 工作(OnSubtitleCompleted 早返回),所以历史
	// 字幕需要在这个时机一次性补齐。任一开关被打开都触发:summary/quiz 都依赖
	// segment 产出的 chunks 作为源材料。
	if h.aiService != nil && oldCourse != nil {
		summaryOn := !oldCourse.AISummaryEnabled && req.AISummaryEnabled
		quizOn := !oldCourse.AIQuizEnabled && req.AIQuizEnabled
		if summaryOn || quizOn {
			if n, err := h.aiService.EnqueueSegmentForCourse(id); err != nil {
				// 入队失败不阻断课程更新本身(主操作已成功);只记录日志,admin 可
				// 之后在 AI Workflow 页手动触发。
				log.Printf("[ai] EnqueueSegmentForCourse(course=%d) after AI toggle-on failed: %v", id, err)
			} else if n > 0 {
				log.Printf("[ai] course %d AI toggle-on: enqueued %d segment job(s)", id, n)
			}
		}
	}

	c.JSON(http.StatusOK, h.toCourseDTO(*course))
}

func (h *adminHandler) CreateEpisode(c *gin.Context) {
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	var req struct {
		ChapterID         uint   `json:"chapter_id"`
		Title             string `json:"title" binding:"required"`
		VideoRelativePath string `json:"video_relative_path" binding:"required"`
		AttachmentJSON    string `json:"attachment_json"`
		SortOrder         int    `json:"sort_order"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	if req.AttachmentJSON == "" {
		req.AttachmentJSON = "[]"
	}

	var chapterIDPtr *uint
	if req.ChapterID > 0 {
		chapterIDPtr = &req.ChapterID
	}

	ep, err := h.episodeService.CreateEpisode(
		courseID,
		chapterIDPtr,
		req.Title,
		req.VideoRelativePath,
		req.AttachmentJSON,
		req.SortOrder,
		"", nil, nil,
	)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, toEpisodeDTO(*ep))
}

func (h *adminHandler) UpdateEpisode(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid episode ID"})
		return
	}

	var req struct {
		ChapterID         uint   `json:"chapter_id"`
		Title             string `json:"title" binding:"required"`
		VideoRelativePath string `json:"video_relative_path" binding:"required"`
		SortOrder         int    `json:"sort_order"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	var chapterIDPtr *uint
	if req.ChapterID > 0 {
		chapterIDPtr = &req.ChapterID
	}

	// Use the PATCH-style admin update so media metadata is never clobbered.
	ep, err := h.episodeService.UpdateEpisodeAdmin(id, chapterIDPtr, req.Title, req.VideoRelativePath, req.SortOrder)
	if err != nil {
		respondError(c, err)
		return
	}
	if ep == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Episode not found"})
		return
	}

	c.JSON(http.StatusOK, toEpisodeDTO(*ep))
}

func (h *adminHandler) DeleteEpisode(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid episode ID"})
		return
	}

	if err := h.episodeService.DeleteEpisode(id); err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) ReorderEpisodes(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	if err := h.episodeService.ReorderEpisodes(req.IDs); err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "reordered"})
}

func (h *adminHandler) BulkDeleteEpisodes(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	for _, id := range req.IDs {
		if err := h.episodeService.DeleteEpisode(id); err != nil {
			respondError(c, err)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) BulkMoveEpisodes(c *gin.Context) {
	var req struct {
		IDs       []uint `json:"ids" binding:"required"`
		ChapterID uint   `json:"chapter_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	for _, id := range req.IDs {
		ep, err := h.episodeRepo.FindByID(id)
		if err != nil {
			respondError(c, err)
			return
		}
		if ep != nil {
			var chapterIDPtr *uint
			if req.ChapterID > 0 {
				chapterIDPtr = &req.ChapterID
			}
			ep.ChapterID = chapterIDPtr
			if err := h.episodeRepo.Update(ep); err != nil {
				respondError(c, err)
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "moved"})
}

// Chapter Controllers
func (h *adminHandler) CreateChapter(c *gin.Context) {
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	var req struct {
		Title          string `json:"title" binding:"required"`
		Description    string `json:"description"`
		CoverURL       string `json:"cover_url"`
		AttachmentJSON string `json:"attachment_json"`
		SortOrder      int    `json:"sort_order"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ch, err := h.chapterService.CreateChapter(courseID, req.Title, req.Description, req.CoverURL, req.AttachmentJSON, req.SortOrder)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, toChapterDTO(*ch))
}

func (h *adminHandler) UpdateChapter(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter ID"})
		return
	}

	var req struct {
		Title          string `json:"title" binding:"required"`
		Description    string `json:"description"`
		CoverURL       string `json:"cover_url"`
		AttachmentJSON string `json:"attachment_json"`
		SortOrder      int    `json:"sort_order"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ch, err := h.chapterService.UpdateChapter(id, req.Title, req.Description, req.CoverURL, req.AttachmentJSON, req.SortOrder)
	if err != nil {
		respondError(c, err)
		return
	}
	if ch == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chapter not found"})
		return
	}

	c.JSON(http.StatusOK, toChapterDTO(*ch))
}

// ReorderChapters rewrites sort_order for the given chapter IDs (in order).
func (h *adminHandler) ReorderChapters(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}
	for i, id := range req.IDs {
		ch, err := h.chapterService.GetChapterByID(id)
		if err != nil {
			respondError(c, err)
			return
		}
		if ch == nil {
			continue
		}
		ch.SortOrder = i + 1
		if _, err := h.chapterService.UpdateChapter(id, ch.Title, ch.Description, ch.CoverURL, ch.AttachmentJSON, ch.SortOrder); err != nil {
			respondError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "reordered"})
}

func (h *adminHandler) DeleteChapter(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter ID"})
		return
	}

	if err := h.chapterService.DeleteChapter(id); err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) ListChaptersByCourse(c *gin.Context) {
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	chapters, err := h.chapterService.GetChaptersByCourse(courseID)
	if err != nil {
		respondError(c, err)
		return
	}

	out := make([]chapterDTO, 0, len(chapters))
	for _, ch := range chapters {
		out = append(out, toChapterDTO(ch))
	}
	c.JSON(http.StatusOK, out)
}

func (h *adminHandler) ListEpisodesByCourse(c *gin.Context) {
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	episodes, err := h.episodeService.GetEpisodesByCourse(courseID)
	if err != nil {
		respondError(c, err)
		return
	}

	out := make([]episodeDTO, 0, len(episodes))
	for _, ep := range episodes {
		out = append(out, toEpisodeDTO(ep))
	}
	// Stamp subtitle_count in one batch query (avoids an N+1 across episodes).
	ids := make([]uint, 0, len(episodes))
	for _, ep := range episodes {
		ids = append(ids, ep.ID)
	}
	if counts, cerr := h.episodeRepo.CountSubtitlesByEpisodes(ids); cerr == nil {
		withSubtitleCounts(out, counts)
	}
	c.JSON(http.StatusOK, out)
}

// Subtitle Controllers
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

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err = h.episodeService.SaveSubtitle(episodeID, req.Language, req.Label, req.SrtContent)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "saved"})
}

// GetSubtitle returns the full subtitle (including SRT content) for admin
// preview. ListSubtitles deliberately omits srt_content (it can be large and
// the list view only needs metadata); this endpoint fetches one by id when the
// admin wants to read the actual text.
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
	c.JSON(http.StatusOK, gin.H{
		"id":          sub.ID,
		"episode_id":  sub.EpisodeID,
		"language":    sub.Language,
		"label":       sub.Label,
		"srt_content": sub.SrtContent,
		"created_at":  formatTime(sub.CreatedAt),
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

// Local image uploads