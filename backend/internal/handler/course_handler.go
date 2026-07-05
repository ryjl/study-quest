package handler

import (
	"net/http"
	"strconv"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// CourseHandler manages course listings and details.
type CourseHandler interface {
	GetCourses(c *gin.Context)
	GetCourseByID(c *gin.Context)
	GetEpisodesByCourse(c *gin.Context)
	GetChaptersByCourse(c *gin.Context)
}

type courseHandler struct {
	courseService  service.CourseService
	episodeService service.EpisodeService
	chapterService service.ChapterService
}

// NewCourseHandler creates an instance of CourseHandler.
func NewCourseHandler(cs service.CourseService, es service.EpisodeService, chs service.ChapterService) CourseHandler {
	return &courseHandler{
		courseService:  cs,
		episodeService: es,
		chapterService: chs,
	}
}

func (h *courseHandler) GetCourses(c *gin.Context) {
	grade := c.Query("grade")
	subject := c.Query("subject")

	// Read user credentials set by UserAuthMiddleware
	userIDVal, existsUserID := c.Get("userID")
	userRoleVal, existsUserRole := c.Get("userRole")

	if !existsUserID || !existsUserRole {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user authentication details missing in context"})
		return
	}

	userID := userIDVal.(uint)
	userRole := userRoleVal.(string)

	courses, err := h.courseService.GetCourses(userID, userRole, grade, subject)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list courses: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, courses)
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

	c.JSON(http.StatusOK, course)
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

	c.JSON(http.StatusOK, episodes)
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
