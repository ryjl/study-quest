package handler

import (
	"log"
	"net/http"
	"strings"
	"studyquest/backend/internal/model"
	"github.com/gin-gonic/gin"
)

// Code split from admin_content.go for navigability.
// Course CRUD + parseGrades helper.

func parseGrades(s string) []model.Grade {
	if s == "" {
		return []model.Grade{model.GradeUniversal}
	}
	parts := strings.Split(s, ",")
	out := make([]model.Grade, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, model.Grade(p))
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
		Title            string            `json:"title" binding:"required"`
		Grades           string            `json:"grades"`
		Subject          string            `json:"subject" binding:"required"`
		ContentType      string            `json:"content_type"`
		CoverURL         string            `json:"cover_url"`
		TagIDs           []uint            `json:"tag_ids"`
		AttachmentJSON   string            `json:"attachment_json"`
		// AIConfig 是 5 字段的课程级 AI 提示对象(替代老的 WhisperHint/QuizHint
		// 双字段)。非 nil → 整体覆盖;nil → 走空配置(留待老客户端兼容,空表单提交)。
		AIConfig         *aiConfigRequest  `json:"ai_config"`
		// WhisperHint/QuizHint 顶层字段保留绑定:兼容老表单 POST,但服务层只看
		// ai_config;若 ai_config 为 nil 而老字段非空,回退填充(平滑迁移)。
		WhisperHint      string            `json:"whisper_hint"`
		QuizHint         string            `json:"quiz_hint"`
		AISummaryEnabled bool              `json:"ai_summary_enabled"`
		AIQuizEnabled    bool              `json:"ai_quiz_enabled"`
	}

	if !bindJSON(c, &req) { return }

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

	// 解析 AI 配置:优先 ai_config 对象;若没带,回退到老的双字段(whisper/quiz),
	// 兼容旧客户端/旧表单(其它 3 字段为空)。
	aiCfg, hasCfg := req.AIConfig.toModel()
	if !hasCfg {
		aiCfg = model.AIConfig{WhisperHint: req.WhisperHint, QuizHint: req.QuizHint}
	}

	course, err := h.courseService.CreateCourse(req.Title, grades, subjectID, contentType, req.CoverURL, req.TagIDs, req.AttachmentJSON, aiCfg, req.AISummaryEnabled, req.AIQuizEnabled)
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
		Title            string            `json:"title" binding:"required"`
		Grades           string            `json:"grades"`
		Subject          string            `json:"subject" binding:"required"`
		ContentType      string            `json:"content_type"`
		CoverURL         string            `json:"cover_url"`
		TagIDs           []uint            `json:"tag_ids"`
		AttachmentJSON   string            `json:"attachment_json"`
		// AIConfig 是 5 字段的课程级 AI 提示对象(替代老的 WhisperHint/QuizHint
		// 双字段)。非 nil → 整体覆盖;nil → 回退到老双字段(兼容旧表单)。
		AIConfig         *aiConfigRequest  `json:"ai_config"`
		WhisperHint      string            `json:"whisper_hint"`
		QuizHint         string            `json:"quiz_hint"`
		AISummaryEnabled bool              `json:"ai_summary_enabled"`
		AIQuizEnabled    bool              `json:"ai_quiz_enabled"`
	}

	if !bindJSON(c, &req) { return }

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

	// 解析 AI 配置:优先 ai_config 对象;若没带,回退到老的双字段(whisper/quiz)。
	aiCfg, hasCfg := req.AIConfig.toModel()
	if !hasCfg {
		aiCfg = model.AIConfig{WhisperHint: req.WhisperHint, QuizHint: req.QuizHint}
	}

	course, err := h.courseService.UpdateCourse(id, req.Title, grades, subjectID, contentType, req.CoverURL, req.TagIDs, req.AttachmentJSON, aiCfg, req.AISummaryEnabled, req.AIQuizEnabled)
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
