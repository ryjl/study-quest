package handler

import (
	"net/http"
	"strconv"
	"strings"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
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
	subjectRepo    repository.SubjectRepository
}

// NewCourseHandler creates an instance of CourseHandler.
func NewCourseHandler(cs service.CourseService, es service.EpisodeService, chs service.ChapterService, subj repository.SubjectRepository) CourseHandler {
	return &courseHandler{
		courseService:  cs,
		episodeService: es,
		chapterService: chs,
		subjectRepo:    subj,
	}
}

// clientCourseDTO is the shape the Flutter client expects: a flat object
// where `subject` is the subject key string (not the GORM relation struct).
// Without this projection, encoding the Course model would emit the nested
// Subject{} struct (id/key/label/...) and break `course.fromJson`.
type clientCourseDTO struct {
	ID             uint     `json:"ID"`
	Title          string   `json:"Title"`
	Grade          string   `json:"Grade"`
	Subject        string   `json:"Subject"` // subject key, e.g. "math"
	CoverURL       string   `json:"CoverURL"`
	Tags           string   `json:"Tags"`      // comma-joined labels (legacy)
	TagsList       []string `json:"TagsList"`  // tag labels in sort order
	TagIDs         []uint   `json:"TagIDs"`    // tag ids (for ID-based filtering)
	GradeDisplay   string   `json:"GradeDisplay"`
	AttachmentJSON string   `json:"AttachmentJSON"`
	CreatedAt      string   `json:"CreatedAt"`
	UpdatedAt      string   `json:"UpdatedAt"`
}

func (h *courseHandler) GetCourses(c *gin.Context) {
	grade := c.Query("grade")
	subjectKey := strings.TrimSpace(c.Query("subject"))

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

	courses, err := h.courseService.GetCourses(userID, userRole, grade, subjectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list courses: " + err.Error()})
		return
	}

	out := make([]clientCourseDTO, 0, len(courses))
	for _, course := range courses {
		out = append(out, h.toClientDTO(course))
	}
	c.JSON(http.StatusOK, out)
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
		Grade:          string(c.Grade),
		Subject:        subjectKey,
		CoverURL:       c.CoverURL,
		Tags:           c.TagsJoined(),  // comma-joined labels (legacy Flutter contract)
		TagsList:       c.TagsList(),    // []string labels
		TagIDs:         tagIDsOf(c.Tags), // []uint tag ids (ID-based filtering)
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
