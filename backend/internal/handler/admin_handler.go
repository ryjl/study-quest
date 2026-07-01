package handler

import (
	"net/http"
	"strconv"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"
	"studyquest/backend/internal/storage"

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

	// API controllers
	CreateUser(c *gin.Context)
	DeleteUser(c *gin.Context)
	CreateCourse(c *gin.Context)
	DeleteCourse(c *gin.Context)
	GrantAccess(c *gin.Context)
	RevokeAccess(c *gin.Context)
	Scan(c *gin.Context)
	ExecuteImport(c *gin.Context)
	UpdateSettings(c *gin.Context)
	PingStorage(c *gin.Context)
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

	c.HTML(http.StatusOK, "users.html", gin.H{
		"Users":       users,
		"Courses":     courses,
		"Permissions": userPermissions,
	})
}

func (h *adminHandler) CoursesGet(c *gin.Context) {
	courses, _ := h.courseRepo.List("", "", nil)

	type courseView struct {
		model.Course
		Episodes []model.Episode
	}

	viewList := make([]courseView, len(courses))
	for i, cr := range courses {
		eps, _ := h.episodeRepo.ListByCourse(cr.ID)
		viewList[i] = courseView{
			Course:   cr,
			Episodes: eps,
		}
	}

	c.HTML(http.StatusOK, "courses.html", gin.H{
		"Courses": viewList,
	})
}

func (h *adminHandler) ImportGet(c *gin.Context) {
	courses, _ := h.courseRepo.List("", "", nil)
	c.HTML(http.StatusOK, "import.html", gin.H{
		"Courses": courses,
	})
}

func (h *adminHandler) SettingsGet(c *gin.Context) {
	settings, _ := h.settingsRepo.GetAll()
	c.HTML(http.StatusOK, "settings.html", gin.H{
		"Settings": settings,
	})
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
		Title    string `json:"title" binding:"required"`
		Grade    string `json:"grade" binding:"required"`
		Subject  string `json:"subject" binding:"required"`
		CoverURL string `json:"cover_url"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	course, err := h.courseService.CreateCourse(req.Title, req.Grade, req.Subject, req.CoverURL)
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

func (h *adminHandler) ExecuteImport(c *gin.Context) {
	var req struct {
		CourseID uint     `json:"course_id" binding:"required"`
		Paths    []string `json:"paths" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	imported, err := h.importService.ImportEpisodes(req.CourseID, req.Paths)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"imported": len(imported),
		"details":  imported,
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
