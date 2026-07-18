package handler

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

// CourseHandler manages course listings and details.
type CourseHandler interface {
	GetCourses(c *gin.Context)
	GetCourseByID(c *gin.Context)
	GetEpisodesByCourse(c *gin.Context)
	GetChaptersByCourse(c *gin.Context)
	GetUnlockStatus(c *gin.Context)
}

type courseHandler struct {
	courseService  service.CourseService
	episodeService service.EpisodeService
	chapterService service.ChapterService
	subjectRepo    repository.SubjectRepository
	unlockService  service.UnlockService
	// courseRepo/episodeRepo 用于在 episode 列表里回显课程的 AI 开关和每个
	// episode 的 HasSubtitle。两个都是只读批量查询(一次 course + 一次字幕计数),
	// 避免 per-episode N+1。
	courseRepo     repository.CourseRepository
	episodeRepo    repository.EpisodeRepository
}

// NewCourseHandler creates an instance of CourseHandler.
func NewCourseHandler(cs service.CourseService, es service.EpisodeService, chs service.ChapterService, subj repository.SubjectRepository, us service.UnlockService, cr repository.CourseRepository, er repository.EpisodeRepository) CourseHandler {
	return &courseHandler{
		courseService:  cs,
		episodeService: es,
		chapterService: chs,
		subjectRepo:    subj,
		unlockService:  us,
		courseRepo:     cr,
		episodeRepo:    er,
	}
}

func (h *courseHandler) GetCourses(c *gin.Context) {
	grade := c.Query("grade")
	subjectKey := strings.TrimSpace(c.Query("subject"))
	// content_type defaults to "learning" so the Study Hall only shows learning
	// courses. The entertainment tab passes "entertainment".
	contentType := model.ContentLearning
	if ct := c.Query("content_type"); ct == string(model.ContentEntertainment) {
		contentType = model.ContentEntertainment
	}

	// Read user credentials set by UserAuthMiddleware
	userIDVal, existsUserID := c.Get("userID")
	userRoleVal, existsUserRole := c.Get("userRole")

	if !existsUserID || !existsUserRole {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user authentication details missing in context"})
		return
	}

	userID := userIDVal.(uint)
	userRole := userRoleVal.(string)

	// Resolve subject key → subjectID (0 means "no filter").
	var subjectID uint
	if subjectKey != "" {
		if subj, _ := h.subjectRepo.FindByKey(subjectKey); subj != nil {
			subjectID = subj.ID
		} else {
			// Unknown subject key → no matches.
			c.JSON(http.StatusOK, []interface{}{})
			return
		}
	}

	courses, err := h.courseService.GetCourses(userID, userRole, grade, subjectID, contentType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list courses: " + err.Error()})
		return
	}

	out := make([]clientCourseDTO, 0, len(courses))
	for _, course := range courses {
		out = append(out, h.toClientDTO(course))
	}

	// Annotate each course card with the drip-unlock summary so the student can
	// see the cadence + next unlock from the course grid without entering each
	// course. Only resolved for student/teen roles: admins/parents manage
	// content and the drip schedule is irrelevant to their view (and resolving
	// per-course would do needless work). Cheap enough for typical course-list
	// sizes (<~20), each resolve is in-memory math + one small query.
	if userRole != "admin" && userRole != "parent" && h.unlockService != nil {
		for i := range out {
			annotateWithUnlock(&out[i], h.unlockService, userID)
		}
	}

	c.JSON(http.StatusOK, out)
}

// annotateWithUnlock fills the Unlock* fields on a course DTO by resolving the
// per-(user, course) visibility. all_open / empty-strategy courses are left
// unannotated (zero values) so the client can hide the badge — there's nothing
// to tell the student. Errors degrade silently to no-annotation (the card just
// won't show a badge) rather than failing the whole list.
func annotateWithUnlock(dto *clientCourseDTO, us service.UnlockService, userID uint) {
	vis, err := us.ResolveVisibleEpisodes(userID, dto.ID)
	if err != nil {
		return
	}
	// all_open with the full catalog visible = no drip cadence to advertise.
	// Hide the badge in that case (and when strategy is empty/unknown).
	if vis.Strategy == "" || vis.Strategy == model.StrategyAllOpen {
		return
	}
	dto.UnlockStrategy = vis.Strategy
	dto.UnlockStrategyLabel = vis.StrategyLabel
	dto.UnlockedCount = len(vis.VisibleIDs)
	dto.EpisodeTotal = vis.Total
	if vis.NextUnlockAt != nil {
		dto.NextUnlockAt = vis.NextUnlockAt.In(appclock.Zone()).Format(time.RFC3339)
	}
}

// toClientDTO projects a model.Course into the flat shape the Flutter client
// expects, resolving the subject key from SubjectID.
func (h *courseHandler) toClientDTO(c model.Course) clientCourseDTO {
	subjectKey := ""
	if subj, _ := h.subjectRepo.FindByID(c.SubjectID); subj != nil {
		subjectKey = subj.Key
	}
	return clientCourseDTO{
		ID:             c.ID,
		Title:          c.Title,
		Grade:          strings.Join(c.GradeKeys(), ","),
		Subject:        subjectKey,
		ContentType:    string(c.ContentType),
		CoverURL:       c.CoverURL,
		TagsList:       tagLabelsOf(c.Tags), // []string labels
		TagIDs:         tagIDsOf(c.Tags),    // []uint tag ids (ID-based filtering)
		GradeDisplay:   c.GradeDisplay(),
		AttachmentJSON: c.AttachmentJSON,
		CreatedAt:      formatTime(c.CreatedAt),
		UpdatedAt:      formatTime(c.UpdatedAt),
	}
}

func (h *courseHandler) GetCourseByID(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID format"})
		return
	}

	course, err := h.courseService.GetCourseByID(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query course: " + err.Error()})
		return
	}

	if course == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
		return
	}

	c.JSON(http.StatusOK, h.toClientDTO(*course))
}

func (h *courseHandler) GetEpisodesByCourse(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID format"})
		return
	}

	episodes, err := h.episodeService.GetEpisodesByCourse(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query episodes: " + err.Error()})
		return
	}

	// Apply unlock gating for student/teen roles. Admin/parent see everything
	// (they manage content, not consume it under a drip schedule). For gated
	// roles we resolve the visible-episode set and mark the rest locked so the
	// client can render them greyed-out with a lock affordance.
	userRoleVal, _ := c.Get("userRole")
	userIDVal, _ := c.Get("userID")
	role, _ := userRoleVal.(string)
	if role == "admin" || role == "parent" {
		c.JSON(http.StatusOK, episodes)
		return
	}

	visibleSet := map[uint]struct{}{}
	if uid, ok := userIDVal.(uint); ok && h.unlockService != nil {
		vis, err := h.unlockService.ResolveVisibleEpisodes(uid, uint(id))
		if err != nil {
			// Fail CLOSED on resolver error: an empty visible set renders every
			// episode locked rather than accidentally leaking locked content.
			// Log so the failure is diagnosable instead of silently locking
			// students out. (The resolver itself rarely errors — it degrades to
			// "no access" via a zero GrantedAt, which yields empty visibility.)
			log.Printf("[unlock] ResolveVisibleEpisodes(user=%d course=%d): %v", uid, id, err)
		} else {
			for _, eid := range vis.VisibleIDs {
				visibleSet[eid] = struct{}{}
			}
		}
	}

	out := make([]clientEpisodeDTO, 0, len(episodes))
	// 课程的 AI 开关同课程内一致,查一次即可;HasSubtitle 需要每 episode 一个,
	// 用批量计数避免 N+1。两查询都 best-effort:失败时退化为零值(false),不阻断
	// episode 列表本身(客户端把 false 当作"无 AI/无字幕"处理,不会崩)。
	aiSummary, aiQuiz := false, false
	if course, cerr := h.courseRepo.FindByID(uint(id)); cerr == nil && course != nil {
		aiSummary = course.AISummaryEnabled
		aiQuiz = course.AIQuizEnabled
	}
	epIDs := make([]uint, 0, len(episodes))
	for _, ep := range episodes {
		epIDs = append(epIDs, ep.ID)
	}
	subCounts, _ := h.episodeRepo.CountSubtitlesByEpisodes(epIDs)
	for _, ep := range episodes {
		_, visible := visibleSet[ep.ID]
		out = append(out, toClientEpisodeDTO(ep, !visible, aiSummary, aiQuiz, subCounts[ep.ID] > 0))
	}
	c.JSON(http.StatusOK, out)
}

// GetUnlockStatus returns the per-(user, course) unlock resolution: how many
// of the course's episodes are visible, the effective strategy label, and the
// next scheduled automatic-unlock instant (for interval/weekly). Drives the
// Flutter course-detail header ("已解锁 X/Y · 下次解锁：周日 19:00").
func (h *courseHandler) GetUnlockStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID format"})
		return
	}

	userIDVal, _ := c.Get("userID")
	uid, ok := userIDVal.(uint)
	if !ok || h.unlockService == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user authentication required"})
		return
	}

	vis, err := h.unlockService.ResolveVisibleEpisodes(uid, uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve unlock status: " + err.Error()})
		return
	}

	nextStr := ""
	if vis.NextUnlockAt != nil {
		nextStr = vis.NextUnlockAt.In(appclock.Zone()).Format(time.RFC3339)
	}
	c.JSON(http.StatusOK, gin.H{
		"visible_count":  len(vis.VisibleIDs),
		"total":          vis.Total,
		"unlocked_n":     vis.UnlockedN,
		"strategy":       vis.Strategy,
		"strategy_label": vis.StrategyLabel,
		"next_unlock_at": nextStr,
	})
}

// GetChaptersByCourse returns the chapter tree for a course (client-facing).
// Used by the course detail screen to render the real chapter structure
// instead of a fabricated two-chapter split.
func (h *courseHandler) GetChaptersByCourse(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID format"})
		return
	}

	chapters, err := h.chapterService.GetChaptersByCourse(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query chapters: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, chapters)
}
