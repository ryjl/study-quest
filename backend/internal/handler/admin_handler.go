package handler

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"
	"studyquest/backend/internal/storage"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// AdminHandler handles backend administrator views and control APIs.
type AdminHandler interface {
	// View controllers
	LoginGet(c *gin.Context)
	LoginPost(c *gin.Context)
	Logout(c *gin.Context)
	Dashboard(c *gin.Context)
	UsersGet(c *gin.Context)
	CoursesGet(c *gin.Context)
	ImportGet(c *gin.Context)
	SettingsGet(c *gin.Context)
	BadgesGet(c *gin.Context)

	// API controllers
	CreateUser(c *gin.Context)
	UpdateUser(c *gin.Context)
	DeleteUser(c *gin.Context)
	BulkAccess(c *gin.Context)
	CreateCourse(c *gin.Context)
	UpdateCourse(c *gin.Context)
	DeleteCourse(c *gin.Context)
	CreateEpisode(c *gin.Context)
	UpdateEpisode(c *gin.Context)
	DeleteEpisode(c *gin.Context)
	ReorderEpisodes(c *gin.Context)
	BulkDeleteEpisodes(c *gin.Context)
	BulkMoveEpisodes(c *gin.Context)
	GrantAccess(c *gin.Context)
	RevokeAccess(c *gin.Context)
	Scan(c *gin.Context)
	PreviewTree(c *gin.Context)
	ExecuteImport(c *gin.Context)
	ScanAttachments(c *gin.Context)
	UpdateSettings(c *gin.Context)
	PingStorage(c *gin.Context)

	// Chapter API controllers
	CreateChapter(c *gin.Context)
	UpdateChapter(c *gin.Context)
	DeleteChapter(c *gin.Context)
	ListChaptersByCourse(c *gin.Context)
	ListEpisodesByCourse(c *gin.Context)

	// Subtitle API controllers
	ListSubtitles(c *gin.Context)
	SaveSubtitle(c *gin.Context)
	DeleteSubtitle(c *gin.Context)
	AutoMatchSubtitle(c *gin.Context)

	// Image upload controller
	UploadImage(c *gin.Context)
}

type adminHandler struct {
	settingsRepo   repository.SettingsRepository
	userRepo       repository.UserRepository
	courseRepo     repository.CourseRepository
	episodeRepo    repository.EpisodeRepository
	userService    service.UserService
	courseService  service.CourseService
	importService  service.ImportService
	episodeService service.EpisodeService
	chapterService service.ChapterService
}

// NewAdminHandler creates an instance of AdminHandler.
func NewAdminHandler(
	sr repository.SettingsRepository,
	ur repository.UserRepository,
	cr repository.CourseRepository,
	er repository.EpisodeRepository,
	us service.UserService,
	cs service.CourseService,
	is service.ImportService,
	es service.EpisodeService,
	chs service.ChapterService,
) AdminHandler {
	return &adminHandler{
		settingsRepo:   sr,
		userRepo:       ur,
		courseRepo:     cr,
		episodeRepo:    er,
		userService:    us,
		courseService:  cs,
		importService:  is,
		episodeService: es,
		chapterService: chs,
	}
}

func (h *adminHandler) LoginGet(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", nil)
}

func (h *adminHandler) LoginPost(c *gin.Context) {
	password := c.PostForm("password")

	savedHash, err := h.settingsRepo.Get("admin_password_hash")
	if err != nil {
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{"error": "Database error checking password"})
		return
	}

	// Default fallback password for initial setup
	if savedHash == "" {
		// Default password is "admin"
		defaultHash, _ := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		_ = h.settingsRepo.Set("admin_password_hash", string(defaultHash), "Admin panel password hash")
		savedHash = string(defaultHash)
	}

	err = bcrypt.CompareHashAndPassword([]byte(savedHash), []byte(password))
	if err != nil {
		c.HTML(http.StatusUnauthorized, "login.html", gin.H{"error": "Invalid admin password"})
		return
	}

	// Set session cookie (valid for 1 day)
	c.SetCookie("admin_session", savedHash, 86400, "/", "", false, true)
	c.Redirect(http.StatusFound, "/admin/")
}

func (h *adminHandler) Logout(c *gin.Context) {
	c.SetCookie("admin_session", "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, "/admin/login")
}

func (h *adminHandler) Dashboard(c *gin.Context) {
	users, _ := h.userRepo.List()
	courses, _ := h.courseRepo.List("", "", nil)

	var episodeCount int64
	// Fetch sum of all episodes
	for _, cr := range courses {
		eps, _ := h.episodeRepo.ListByCourse(cr.ID)
		episodeCount += int64(len(eps))
	}

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"UserCount":    len(users),
		"CourseCount":  len(courses),
		"EpisodeCount": episodeCount,
	})
}

func (h *adminHandler) UsersGet(c *gin.Context) {
	users, _ := h.userRepo.List()
	courses, _ := h.courseRepo.List("", "", nil)

	// Map to list which courses are allowed for each user
	userPermissions := make(map[uint][]uint)
	for _, u := range users {
		ids, _ := h.userRepo.GetAccessList(u.ID)
		userPermissions[u.ID] = ids
	}

	gradesList := []struct {
		Key  string
		Name string
	}{
		{"1", "一年级"},
		{"2", "二年级"},
		{"3", "三年级"},
		{"4", "四年级"},
		{"5", "五年级"},
		{"6", "六年级"},
		{"7", "七年级/初一"},
		{"8", "八年级/初二"},
		{"9", "九年级/初三"},
		{"universal", "全学段通用"},
	}

	c.HTML(http.StatusOK, "users.html", gin.H{
		"Users":       users,
		"Courses":     courses,
		"Permissions": userPermissions,
		"GradesList":  gradesList,
	})
}

func getUniqueCourseTags(courses []model.Course) []string {
	tagMap := make(map[string]bool)
	for _, cr := range courses {
		if cr.Tags != "" {
			parts := strings.Split(cr.Tags, ",")
			for _, p := range parts {
				trimmed := strings.TrimSpace(p)
				if trimmed != "" {
					tagMap[trimmed] = true
				}
			}
		}
	}
	var tags []string
	for t := range tagMap {
		tags = append(tags, t)
	}
	return tags
}

func (h *adminHandler) CoursesGet(c *gin.Context) {
	courses, _ := h.courseRepo.List("", "", nil)
	existingTags := getUniqueCourseTags(courses)

	type courseView struct {
		model.Course
		Episodes []model.Episode
		Chapters []model.Chapter
	}

	viewList := make([]courseView, len(courses))
	for i, cr := range courses {
		eps, _ := h.episodeRepo.ListByCourse(cr.ID)
		chaps, _ := h.chapterService.GetChaptersByCourse(cr.ID)
		viewList[i] = courseView{
			Course:   cr,
			Episodes: eps,
			Chapters: chaps,
		}
	}

	gradesList := []struct {
		Key  string
		Name string
	}{
		{"1", "一年级"},
		{"2", "二年级"},
		{"3", "三年级"},
		{"4", "四年级"},
		{"5", "五年级"},
		{"6", "六年级"},
		{"7", "七年级/初一"},
		{"8", "八年级/初二"},
		{"9", "九年级/初三"},
		{"universal", "全学段通用"},
	}

	c.HTML(http.StatusOK, "courses.html", gin.H{
		"Courses":      viewList,
		"GradesList":   gradesList,
		"ExistingTags": existingTags,
	})
}

func (h *adminHandler) ImportGet(c *gin.Context) {
	courses, _ := h.courseRepo.List("", "", nil)
	existingTags := getUniqueCourseTags(courses)
	c.HTML(http.StatusOK, "import.html", gin.H{
		"Courses":      courses,
		"ExistingTags": existingTags,
	})
}

func (h *adminHandler) SettingsGet(c *gin.Context) {
	settings, _ := h.settingsRepo.GetAll()
	c.HTML(http.StatusOK, "settings.html", gin.H{
		"Settings": settings,
	})
}

func (h *adminHandler) BadgesGet(c *gin.Context) {
	c.HTML(http.StatusOK, "badges.html", nil)
}

// API CONTROLLERS

func (h *adminHandler) CreateUser(c *gin.Context) {
	var req struct {
		Nickname  string `json:"nickname" binding:"required"`
		AvatarURL string `json:"avatar_url"`
		Pin       string `json:"pin" binding:"required"`
		Role      string `json:"role" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	user, err := h.userService.CreateUser(req.Nickname, req.AvatarURL, req.Pin, req.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, user)
}

func (h *adminHandler) DeleteUser(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	if err := h.userService.DeleteUser(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) CreateCourse(c *gin.Context) {
	var req struct {
		Title          string `json:"title" binding:"required"`
		Grade          string `json:"grade" binding:"required"`
		Subject        string `json:"subject" binding:"required"`
		CoverURL       string `json:"cover_url"`
		Tags           string `json:"tags"`
		AttachmentJSON string `json:"attachment_json"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	course, err := h.courseService.CreateCourse(req.Title, req.Grade, req.Subject, req.CoverURL, req.Tags, req.AttachmentJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, course)
}

func (h *adminHandler) DeleteCourse(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	if err := h.courseService.DeleteCourse(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) GrantAccess(c *gin.Context) {
	var req struct {
		UserID   uint `json:"user_id" binding:"required"`
		CourseID uint `json:"course_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	if err := h.userService.GrantCourseAccess(req.UserID, req.CourseID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "granted"})
}

func (h *adminHandler) RevokeAccess(c *gin.Context) {
	var req struct {
		UserID   uint `json:"user_id" binding:"required"`
		CourseID uint `json:"course_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	if err := h.userService.RevokeCourseAccess(req.UserID, req.CourseID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

func (h *adminHandler) Scan(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path = "/"
	}

	files, err := h.importService.ScanPath(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage scan failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, files)
}

func (h *adminHandler) PreviewTree(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path = "/"
	}

	tree, err := h.importService.PreviewDeepScan(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage scan failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, tree)
}

func (h *adminHandler) ExecuteImport(c *gin.Context) {
	var req service.ExecuteTreeImportRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format: " + err.Error()})
		return
	}

	err := h.importService.ExecuteTreeImport(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
	})
}

func (h *adminHandler) UpdateSettings(c *gin.Context) {
	var req struct {
		StorageType     string `json:"storage_type"`
		StorageURL      string `json:"storage_url"`
		StorageUsername string `json:"storage_username"`
		StoragePassword string `json:"storage_password"`
		StorageToken    string `json:"storage_token"`
		AdminPassword   string `json:"admin_password"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	_ = h.settingsRepo.Set("storage_type", req.StorageType, "Storage provider type (alist/webdav)")
	_ = h.settingsRepo.Set("storage_url", req.StorageURL, "AList/WebDAV service base endpoint URL")
	_ = h.settingsRepo.Set("storage_username", req.StorageUsername, "Basic authentication username")
	_ = h.settingsRepo.Set("storage_password", req.StoragePassword, "Basic authentication password")
	_ = h.settingsRepo.Set("storage_token", req.StorageToken, "AList API Authorization Token")
	if req.AdminPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.AdminPassword), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt password"})
			return
		}
		_ = h.settingsRepo.Set("admin_password_hash", string(hash), "Admin panel password hash")
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Settings updated successfully"})
}

func (h *adminHandler) PingStorage(c *gin.Context) {
	// Support testing unsaved settings directly from the form
	var req struct {
		StorageType     string `json:"storage_type"`
		StorageURL      string `json:"storage_url"`
		StorageUsername string `json:"storage_username"`
		StoragePassword string `json:"storage_password"`
		StorageToken    string `json:"storage_token"`
	}

	// Try to bind JSON body. If it binds successfully and has a URL, we test it on the fly
	if c.Request.Method == "POST" {
		if err := c.ShouldBindJSON(&req); err == nil && req.StorageURL != "" {
			var provider storage.StorageProvider
			if req.StorageType == "alist" {
				provider = storage.NewAListProvider(req.StorageURL, req.StorageUsername, req.StoragePassword, req.StorageToken)
			} else if req.StorageType == "webdav" {
				provider = storage.NewWebDAVProvider(req.StorageURL, req.StorageUsername, req.StoragePassword)
			}

			if provider != nil {
				if err := provider.Ping(); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "status": "failed"})
					return
				}
				c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Successfully connected to storage source (测试通过)"})
				return
			}
		}
	}

	// Default fallback to saved settings
	if err := h.importService.PingStorage(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "status": "failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Successfully connected to storage source"})
}

func (h *adminHandler) UpdateUser(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	var req struct {
		Nickname  string `json:"nickname" binding:"required"`
		AvatarURL string `json:"avatar_url"`
		Pin       string `json:"pin"`
		Role      string `json:"role" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	user, err := h.userService.UpdateUser(uint(id), req.Nickname, req.AvatarURL, req.Pin, req.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, user)
}

func (h *adminHandler) BulkAccess(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
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

	if err := h.userService.BulkCourseAccess(uint(id), req.Action); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *adminHandler) UpdateCourse(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	var req struct {
		Title          string `json:"title" binding:"required"`
		Grade          string `json:"grade" binding:"required"`
		Subject        string `json:"subject" binding:"required"`
		CoverURL       string `json:"cover_url"`
		Tags           string `json:"tags"`
		AttachmentJSON string `json:"attachment_json"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	course, err := h.courseService.UpdateCourse(uint(id), req.Title, req.Grade, req.Subject, req.CoverURL, req.Tags, req.AttachmentJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, course)
}

func (h *adminHandler) CreateEpisode(c *gin.Context) {
	courseIDStr := c.Param("id")
	courseID, err := strconv.ParseUint(courseIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	var req struct {
		ChapterID            uint   `json:"chapter_id"`
		Title                string `json:"title" binding:"required"`
		VideoRelativePath    string `json:"video_relative_path" binding:"required"`
		AttachmentJSON       string `json:"attachment_json"`
		SortOrder            int    `json:"sort_order"`
		FileHash             string `json:"file_hash"`
		OriginalRelativePath string `json:"original_relative_path"`
		FileSize             *int64 `json:"file_size"`
		DurationSeconds      *int   `json:"duration_seconds"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	if req.AttachmentJSON == "" {
		req.AttachmentJSON = "[]"
	}

	ep, err := h.episodeService.CreateEpisode(
		uint(courseID),
		req.ChapterID,
		req.Title,
		req.VideoRelativePath,
		req.AttachmentJSON,
		req.SortOrder,
		req.FileHash,
		req.OriginalRelativePath,
		req.FileSize,
		req.DurationSeconds,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ep)
}

func (h *adminHandler) UpdateEpisode(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid episode ID"})
		return
	}

	var req struct {
		ChapterID            uint   `json:"chapter_id"`
		Title                string `json:"title" binding:"required"`
		VideoRelativePath    string `json:"video_relative_path" binding:"required"`
		AttachmentJSON       string `json:"attachment_json"`
		SortOrder            int    `json:"sort_order"`
		FileHash             string `json:"file_hash"`
		OriginalRelativePath string `json:"original_relative_path"`
		FileSize             *int64 `json:"file_size"`
		DurationSeconds      *int   `json:"duration_seconds"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	if req.AttachmentJSON == "" {
		req.AttachmentJSON = "[]"
	}

	ep, err := h.episodeService.UpdateEpisode(
		uint(id),
		req.ChapterID,
		req.Title,
		req.VideoRelativePath,
		req.AttachmentJSON,
		req.SortOrder,
		req.FileHash,
		req.OriginalRelativePath,
		req.FileSize,
		req.DurationSeconds,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ep)
}

func (h *adminHandler) DeleteEpisode(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid episode ID"})
		return
	}

	if err := h.episodeService.DeleteEpisode(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete episode: " + err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find episode: " + err.Error()})
			return
		}
		if ep != nil {
			ep.ChapterID = req.ChapterID
			if err := h.episodeRepo.Update(ep); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to move episode: " + err.Error()})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "moved"})
}

// Chapter Controllers
func (h *adminHandler) CreateChapter(c *gin.Context) {
	courseIDStr := c.Param("id")
	courseID, err := strconv.ParseUint(courseIDStr, 10, 32)
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

	ch, err := h.chapterService.CreateChapter(uint(courseID), req.Title, req.Description, req.CoverURL, req.AttachmentJSON, req.SortOrder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ch)
}

func (h *adminHandler) UpdateChapter(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
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

	ch, err := h.chapterService.UpdateChapter(uint(id), req.Title, req.Description, req.CoverURL, req.AttachmentJSON, req.SortOrder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ch)
}

func (h *adminHandler) ScanAttachments(c *gin.Context) {
	entityType := c.Query("type") // "course", "chapter", "episode"
	idStr := c.Query("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	var targetPath string
	switch entityType {
	case "episode":
		ep, err := h.episodeRepo.FindByID(uint(id))
		if err != nil || ep == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Episode not found"})
			return
		}
		if ep.OriginalRelativePath != "" {
			targetPath = filepath.ToSlash(filepath.Dir(ep.OriginalRelativePath))
		}
	case "chapter":
		ch, err := h.chapterService.GetChapterByID(uint(id))
		if err != nil || ch == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Chapter not found"})
			return
		}
		eps, err := h.episodeRepo.ListByCourse(ch.CourseID)
		if err == nil {
			for _, ep := range eps {
				if ep.ChapterID == ch.ID && ep.OriginalRelativePath != "" {
					targetPath = filepath.ToSlash(filepath.Dir(ep.OriginalRelativePath))
					break
				}
			}
		}
	case "course":
		cr, err := h.courseRepo.FindByID(uint(id))
		if err != nil || cr == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Course not found"})
			return
		}
		eps, err := h.episodeRepo.ListByCourse(cr.ID)
		if err == nil && len(eps) > 0 {
			for _, ep := range eps {
				if ep.OriginalRelativePath != "" {
					targetPath = filepath.ToSlash(filepath.Dir(ep.OriginalRelativePath))
					break
				}
			}
		}
	}

	if targetPath == "" || targetPath == "." {
		targetPath = "/"
	}

	files, err := h.importService.ScanDirectoryAttachments(targetPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"path":  targetPath,
		"files": files,
	})
}

func (h *adminHandler) DeleteChapter(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter ID"})
		return
	}

	if err := h.chapterService.DeleteChapter(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *adminHandler) ListChaptersByCourse(c *gin.Context) {
	courseIDStr := c.Param("id")
	courseID, err := strconv.ParseUint(courseIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	chapters, err := h.chapterService.GetChaptersByCourse(uint(courseID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, chapters)
}

func (h *adminHandler) ListEpisodesByCourse(c *gin.Context) {
	courseIDStr := c.Param("id")
	courseID, err := strconv.ParseUint(courseIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	episodes, err := h.episodeService.GetEpisodesByCourse(uint(courseID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, episodes)
}

// Subtitle Controllers
func (h *adminHandler) ListSubtitles(c *gin.Context) {
	episodeIDStr := c.Param("id")
	episodeID, err := strconv.ParseUint(episodeIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid episode ID"})
		return
	}

	subs, err := h.episodeService.ListSubtitles(uint(episodeID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, subs)
}

func (h *adminHandler) SaveSubtitle(c *gin.Context) {
	episodeIDStr := c.Param("id")
	episodeID, err := strconv.ParseUint(episodeIDStr, 10, 32)
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

	err = h.episodeService.SaveSubtitle(uint(episodeID), req.Language, req.Label, req.SrtContent)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "saved"})
}

func (h *adminHandler) DeleteSubtitle(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid subtitle ID"})
		return
	}

	if err := h.episodeService.DeleteSubtitle(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database search failed: " + err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save matched subtitle: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "success",
		"episode_id": ep.ID,
		"title":      ep.Title,
	})
}

// Local image uploads
func (h *adminHandler) UploadImage(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no file uploaded"})
		return
	}

	uploadDir := "./data/uploads"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create upload dir"})
		return
	}

	ext := filepath.Ext(file.Filename)
	randomName := fmt.Sprintf("%d_%s%s", time.Now().UnixNano(), generateRandomString(4), ext)
	dst := filepath.Join(uploadDir, randomName)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}

	urlPath := "/uploads/" + randomName
	c.JSON(http.StatusOK, gin.H{"url": urlPath})
}

func generateRandomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
