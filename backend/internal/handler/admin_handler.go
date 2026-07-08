package handler

import (
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"
	"studyquest/backend/internal/storage"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// AdminHandler handles backend administrator views and control APIs.
type AdminHandler interface {
	// Auth (public)
	LoginAPI(c *gin.Context)
	LogoutAPI(c *gin.Context)
	Me(c *gin.Context)

	// Stats / probe
	DashboardStats(c *gin.Context)
	ScanMissingDurations(c *gin.Context)
	ProbeProgress(c *gin.Context)

	// Users
	ListUsers(c *gin.Context)
	CreateUser(c *gin.Context)
	UpdateUser(c *gin.Context)
	DeleteUser(c *gin.Context)
	BulkAccess(c *gin.Context)
	UserLedger(c *gin.Context)
	UserBadges(c *gin.Context)

	// Courses
	ListCourses(c *gin.Context)
	GetCourseDetail(c *gin.Context)
	CreateCourse(c *gin.Context)
	UpdateCourse(c *gin.Context)
	DeleteCourse(c *gin.Context)

	// Episodes
	ListEpisodesByCourse(c *gin.Context)
	CreateEpisode(c *gin.Context)
	UpdateEpisode(c *gin.Context)
	DeleteEpisode(c *gin.Context)
	ReorderEpisodes(c *gin.Context)
	BulkDeleteEpisodes(c *gin.Context)
	BulkMoveEpisodes(c *gin.Context)

	// Chapters
	ListChaptersByCourse(c *gin.Context)
	CreateChapter(c *gin.Context)
	UpdateChapter(c *gin.Context)
	DeleteChapter(c *gin.Context)
	ReorderChapters(c *gin.Context)

	// Access
	GrantAccess(c *gin.Context)
	RevokeAccess(c *gin.Context)

	// Import / Settings / Storage
	Scan(c *gin.Context)
	PreviewTree(c *gin.Context)
	ExecuteImport(c *gin.Context)
	GetSettings(c *gin.Context)
	UpdateSettings(c *gin.Context)
	PingStorage(c *gin.Context)

	// Subtitles
	ListSubtitles(c *gin.Context)
	SaveSubtitle(c *gin.Context)
	DeleteSubtitle(c *gin.Context)
	AutoMatchSubtitle(c *gin.Context)

	// Attachments + Uploads
	ScanAttachments(c *gin.Context)
	UploadImage(c *gin.Context)
}

type adminHandler struct {
	settingsRepo   repository.SettingsRepository
	userRepo       repository.UserRepository
	courseRepo     repository.CourseRepository
	episodeRepo    repository.EpisodeRepository
	chapterRepo    repository.ChapterRepository
	progressRepo   repository.ProgressRepository
	subjectRepo    repository.SubjectRepository
	badgeRepo      repository.BadgeRepository
	userService    service.UserService
	courseService  service.CourseService
	importService  service.ImportService
	episodeService service.EpisodeService
	chapterService service.ChapterService
	badgeService   service.BadgeService
	probeWorker    *service.ProbeWorker
}

// NewAdminHandler creates an instance of AdminHandler.
func NewAdminHandler(
	sr repository.SettingsRepository,
	ur repository.UserRepository,
	cr repository.CourseRepository,
	er repository.EpisodeRepository,
	chrep repository.ChapterRepository,
	prep repository.ProgressRepository,
	subj repository.SubjectRepository,
	br repository.BadgeRepository,
	us service.UserService,
	cs service.CourseService,
	is service.ImportService,
	es service.EpisodeService,
	chs service.ChapterService,
	bs service.BadgeService,
	pw *service.ProbeWorker,
) AdminHandler {
	return &adminHandler{
		settingsRepo:   sr,
		userRepo:       ur,
		courseRepo:     cr,
		episodeRepo:    er,
		chapterRepo:    chrep,
		progressRepo:   prep,
		subjectRepo:    subj,
		badgeRepo:      br,
		userService:    us,
		courseService:  cs,
		importService:  is,
		episodeService: es,
		chapterService: chs,
		badgeService:   bs,
		probeWorker:    pw,
	}
}

// LoginAPI authenticates the admin password and sets the session cookie.
// Replaces the old form-based LoginPost with a JSON endpoint for the SPA.
func (h *adminHandler) LoginAPI(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return
	}

	savedHash, err := h.settingsRepo.Get("admin_password_hash")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error checking password"})
		return
	}

	// Default fallback password for initial setup. Only generate when there's
	// truly no hash yet, AND require the write to succeed before using it — if
	// we generated a fresh random hash every login (salt differs each call) but
	// Set silently failed, the cookie (the generated hash) would never match
	// what's in the DB and the session would be rejected on the next request.
	// Re-reading after Set guarantees cookie == stored value.
	if savedHash == "" {
		defaultHash, _ := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		if err := h.settingsRepo.Set("admin_password_hash", string(defaultHash), "Admin panel password hash"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "初始化默认密码失败"})
			return
		}
		// Re-read so we set the cookie to exactly what's persisted (avoids any
		// divergence between the in-memory hash and the DB row).
		savedHash, _ = h.settingsRepo.Get("admin_password_hash")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(savedHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "密码错误"})
		return
	}

	c.SetCookie("admin_session", savedHash, 86400, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// LogoutAPI clears the admin session cookie (JSON version).
func (h *adminHandler) LogoutAPI(c *gin.Context) {
	c.SetCookie("admin_session", "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Me reports the current admin session state. The SPA calls this on boot to
// decide between routing to the login page or the dashboard.
func (h *adminHandler) Me(c *gin.Context) {
	cookie, err := c.Cookie("admin_session")
	if err != nil || cookie == "" {
		c.JSON(http.StatusOK, gin.H{"authenticated": false})
		return
	}
	savedHash, _ := h.settingsRepo.Get("admin_password_hash")
	if savedHash == "" || cookie != savedHash {
		c.JSON(http.StatusOK, gin.H{"authenticated": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"authenticated": true})
}

// DashboardStats aggregates the headline numbers + charts shown on the
// admin landing page. Each stat is a single SQL query so the page stays snappy.
// A failure in any one stat degrades to a zero value rather than 500-ing the
// whole dashboard, but each error is logged so silent breakage is visible.
func (h *adminHandler) DashboardStats(c *gin.Context) {
	users, err := h.userRepo.List()
	if err != nil {
		log.Printf("DashboardStats: userRepo.List failed: %v", err)
	}
	courses, err := h.courseRepo.List("", 0, nil)
	if err != nil {
		log.Printf("DashboardStats: courseRepo.List failed: %v", err)
	}
	episodeCount, err := h.episodeRepo.CountAll()
	if err != nil {
		log.Printf("DashboardStats: episodeRepo.CountAll failed: %v", err)
	}
	totalDur, err := h.episodeRepo.SumTotalDurationSeconds()
	if err != nil {
		log.Printf("DashboardStats: episodeRepo.SumTotalDurationSeconds failed: %v", err)
	}
	pending, err := h.episodeRepo.CountByNullDuration()
	if err != nil {
		log.Printf("DashboardStats: episodeRepo.CountByNullDuration failed: %v", err)
	}
	subjectMap, err := h.episodeRepo.CountBySubject()
	if err != nil {
		log.Printf("DashboardStats: episodeRepo.CountBySubject failed: %v", err)
	}
	recent, err := h.episodeRepo.RecentDailyCount(7)
	if err != nil {
		log.Printf("DashboardStats: episodeRepo.RecentDailyCount failed: %v", err)
	}

	// Learning-activity aggregates. Each degrades to a zero value on error
	// (logged) so one broken stat never takes down the whole dashboard.
	totalWatch, err := h.progressRepo.SumTotalWatchSeconds()
	if err != nil {
		log.Printf("DashboardStats: SumTotalWatchSeconds failed: %v", err)
	}
	completed, err := h.progressRepo.CountCompletedEpisodes()
	if err != nil {
		log.Printf("DashboardStats: CountCompletedEpisodes failed: %v", err)
	}
	// "Today" = midnight in the BUSINESS timezone (Asia/Shanghai), not the
	// server's host zone — a Beijing student's "今天" follows the Beijing
	// calendar. The host zone (often UTC in containers) would've shifted the
	// day boundary and under/over-counted.
	now := appclock.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, appclock.Zone())
	activeToday, err := h.progressRepo.CountActiveUsersSince(todayStart)
	if err != nil {
		log.Printf("DashboardStats: CountActiveUsersSince failed: %v", err)
	}
	unlockedBadges, err := h.badgeRepo.BatchUnlockedBadgeCounts()
	if err != nil {
		log.Printf("DashboardStats: BatchUnlockedBadgeCounts failed: %v", err)
	}
	var totalUnlocks int64
	for _, n := range unlockedBadges {
		totalUnlocks += n
	}
	recentWatch, err := h.progressRepo.RecentDailyWatchSeconds(7)
	if err != nil {
		log.Printf("DashboardStats: RecentDailyWatchSeconds failed: %v", err)
	}
	topUserRows, err := h.progressRepo.TopUsersByWatchSeconds(5)
	if err != nil {
		log.Printf("DashboardStats: TopUsersByWatchSeconds failed: %v", err)
	}
	topCourseRows, err := h.progressRepo.TopCoursesByCompletions(5)
	if err != nil {
		log.Printf("DashboardStats: TopCoursesByCompletions failed: %v", err)
	}

	// Resolve friendly labels for the leaderboards (nickname / course title).
	userNameByID := make(map[uint]string, len(users))
	for _, u := range users {
		userNameByID[u.ID] = u.Nickname
	}
	courseTitleByID := make(map[uint]string, len(courses))
	for _, cr := range courses {
		courseTitleByID[cr.ID] = cr.Title
	}
	topUsers := make([]dashboardLeaderRow, 0, len(topUserRows))
	for _, r := range topUserRows {
		topUsers = append(topUsers, dashboardLeaderRow{
			ID:    r.UserID,
			Label: userNameByID[r.UserID],
			Value: r.WatchSeconds,
		})
	}
	topCourses := make([]dashboardLeaderRow, 0, len(topCourseRows))
	for _, r := range topCourseRows {
		topCourses = append(topCourses, dashboardLeaderRow{
			ID:    r.CourseID,
			Label: courseTitleByID[r.CourseID],
			Value: r.CompletedEpisodes,
		})
	}

	subjectDist := make([]subjectCountDTO, 0, len(subjectMap))
	for subj, cnt := range subjectMap {
		s := subj
		if s == "" {
			s = "unknown"
		}
		subjectDist = append(subjectDist, subjectCountDTO{Subject: s, Count: cnt})
	}

	recentOut := make([]repositoryDailyCountAlias, 0, len(recent))
	for _, r := range recent {
		recentOut = append(recentOut, repositoryDailyCountAlias{Date: r.Date, Count: r.Count})
	}
	recentWatchOut := make([]repositoryDailyWatchAlias, 0, len(recentWatch))
	for _, r := range recentWatch {
		recentWatchOut = append(recentWatchOut, repositoryDailyWatchAlias{Date: r.Date, Seconds: r.Seconds})
	}

	c.JSON(http.StatusOK, dashboardStatsDTO{
		UserCount:            int64(len(users)),
		CourseCount:          int64(len(courses)),
		EpisodeCount:         episodeCount,
		TotalDurationSeconds: totalDur,
		PendingProbeCount:    pending,
		SubjectDistribution:  subjectDist,
		RecentDailyEpisodes:  recentOut,
		TotalWatchSeconds:    totalWatch,
		CompletedEpisodes:    completed,
		ActiveUsersToday:     activeToday,
		UnlockedBadgeCount:   totalUnlocks,
		RecentDailyWatch:     recentWatchOut,
		TopUsers:             topUsers,
		TopCourses:           topCourses,
	})
}

// ListUsers returns all users as snake_case DTOs (with points + course access).
func (h *adminHandler) ListUsers(c *gin.Context) {
	users, err := h.userRepo.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Batch-fetch all per-user aggregates in a fixed number of queries so the
	// list stays O(users) and not O(users × stats). Each map is keyed by
	// user id; missing entries read as zero-values. A failure in any single
	// batch degrades gracefully (its stats show as zero) rather than 500-ing
	// the whole list — but we log the error so silent breakage (the kind that
	// previously hid the BatchUserProgressSummary SQLite timestamp bug for a
	// whole release) can't recur unnoticed.
	points, err := h.progressRepo.BatchPoints()
	if err != nil {
		log.Printf("ListUsers: BatchPoints failed: %v", err)
	}
	access, err := h.userRepo.BatchAccessLists()
	if err != nil {
		log.Printf("ListUsers: BatchAccessLists failed: %v", err)
	}
	progress, err := h.progressRepo.BatchUserProgressSummary()
	if err != nil {
		log.Printf("ListUsers: BatchUserProgressSummary failed: %v", err)
	}
	accessible, err := h.episodeRepo.BatchAccessibleEpisodeCounts()
	if err != nil {
		log.Printf("ListUsers: BatchAccessibleEpisodeCounts failed: %v", err)
	}
	badges, err := h.badgeRepo.BatchUnlockedBadgeCounts()
	if err != nil {
		log.Printf("ListUsers: BatchUnlockedBadgeCounts failed: %v", err)
	}
	totalBadges, err := h.badgeRepo.CountBadges()
	if err != nil {
		log.Printf("ListUsers: CountBadges failed: %v", err)
	}

	batch := userStatsBatch{
		points:      points,
		access:      access,
		progress:    progress,
		accessible:  accessible,
		badges:      badges,
		totalBadges: totalBadges,
	}

	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, h.toUserDTO(u, batch))
	}
	c.JSON(http.StatusOK, out)
}

// ListCourses returns all courses with per-course episode/chapter counts and
// total duration, ready for the course-library grid.
func (h *adminHandler) ListCourses(c *gin.Context) {
	courses, err := h.courseRepo.List("", 0, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
func (h *adminHandler) GetSettings(c *gin.Context) {
	all, err := h.settingsRepo.GetAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	get := func(k string) string {
		if v, ok := all[k]; ok {
			return v
		}
		return ""
	}
	c.JSON(http.StatusOK, gin.H{
		"storage_type":     get("storage_type"),
		"storage_url":      get("storage_url"),
		"storage_username": get("storage_username"),
		"storage_password": get("storage_password"),
		"storage_token":    get("storage_token"),
	})
}

// UserLedger returns a paginated slice of a user's point transactions.
func (h *adminHandler) UserLedger(c *gin.Context) {
	userID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	limit := 20
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	ledger, err := h.progressRepo.GetPointsLedger(userID, limit, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]ledgerDTO, 0, len(ledger))
	for _, l := range ledger {
		out = append(out, toLedgerDTO(l))
	}
	c.JSON(http.StatusOK, out)
}

// UserBadges returns every badge with an `unlocked` flag for the given user.
// ListUserBadges already returns only the unlocked subset, so we build a set
// of unlocked badge IDs from it.
func (h *adminHandler) UserBadges(c *gin.Context) {
	userID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	all, _ := h.badgeService.List()
	unlockedList, _ := h.badgeService.ListUserBadges(userID)
	unlockedSet := make(map[uint]bool, len(unlockedList))
	for _, b := range unlockedList {
		unlockedSet[b.ID] = true
	}
	out := make([]badgeDTO, 0, len(all))
	for _, b := range all {
		out = append(out, toBadgeDTO(b, unlockedSet[b.ID], ""))
	}
	c.JSON(http.StatusOK, out)
}

// parseUintParam is a tiny helper to read a :id path param as uint.
func parseUintParam(c *gin.Context, name string) (uint, error) {
	id, err := strconv.ParseUint(c.Param(name), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint(id), nil
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

	c.JSON(http.StatusOK, h.toUserDTO(*user, userStatsBatch{}))
}

func (h *adminHandler) DeleteUser(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	if err := h.userService.DeleteUser(id); err != nil {
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
		TagIDs         []uint `json:"tag_ids"`
		AttachmentJSON string `json:"attachment_json"`
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

	course, err := h.courseService.CreateCourse(req.Title, req.Grade, subjectID, req.CoverURL, req.TagIDs, req.AttachmentJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, h.toCourseDTO(*course))
}

// resolveSubjectID maps a subject key (e.g. "math") to its subjects.id. Returns
// a user-facing error string if the key doesn't match any subject.
func (h *adminHandler) resolveSubjectID(subjectKey string) (uint, error) {
	subjectKey = strings.TrimSpace(subjectKey)
	if subjectKey == "" {
		return 0, fmt.Errorf("subject is required")
	}
	subj, err := h.subjectRepo.FindByKey(subjectKey)
	if err != nil {
		// A real DB error (not "not found") — surface it distinctly so it
		// isn't masked as a bad subject key.
		return 0, fmt.Errorf("failed to look up subject: %w", err)
	}
	if subj == nil {
		return 0, fmt.Errorf("unknown subject: %s", subjectKey)
	}
	return subj.ID, nil
}

func (h *adminHandler) DeleteCourse(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	if err := h.courseService.DeleteCourse(id); err != nil {
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
	id, err := parseUintParam(c, "id")
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

	user, err := h.userService.UpdateUser(id, req.Nickname, req.AvatarURL, req.Pin, req.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	c.JSON(http.StatusOK, h.toUserDTO(*user, userStatsBatch{}))
}

func (h *adminHandler) BulkAccess(c *gin.Context) {
	id, err := parseUintParam(c, "id")
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

	if err := h.userService.BulkCourseAccess(id, req.Action); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *adminHandler) UpdateCourse(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid course ID"})
		return
	}

	var req struct {
		Title          string `json:"title" binding:"required"`
		Grade          string `json:"grade" binding:"required"`
		Subject        string `json:"subject" binding:"required"`
		CoverURL       string `json:"cover_url"`
		TagIDs         []uint `json:"tag_ids"`
		AttachmentJSON string `json:"attachment_json"`
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

	course, err := h.courseService.UpdateCourse(id, req.Title, req.Grade, subjectID, req.CoverURL, req.TagIDs, req.AttachmentJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if course == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Course not found"})
		return
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

	ep, err := h.episodeService.CreateEpisode(
		courseID,
		req.ChapterID,
		req.Title,
		req.VideoRelativePath,
		req.AttachmentJSON,
		req.SortOrder,
		"", "", nil, nil,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	// Use the PATCH-style admin update so media metadata is never clobbered.
	ep, err := h.episodeService.UpdateEpisodeAdmin(id, req.ChapterID, req.Title, req.VideoRelativePath, req.SortOrder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if ch == nil {
			continue
		}
		ch.SortOrder = i + 1
		if _, err := h.chapterService.UpdateChapter(id, ch.Title, ch.Description, ch.CoverURL, ch.AttachmentJSON, ch.SortOrder); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "reordered"})
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
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter ID"})
		return
	}

	if err := h.chapterService.DeleteChapter(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	out := make([]episodeDTO, 0, len(episodes))
	for _, ep := range episodes {
		out = append(out, toEpisodeDTO(ep))
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

// ScanMissingDurations finds every episode whose duration_seconds is NULL and
// enqueues it onto the background probe worker. The worker probes one at a
// time with a fixed gap (see probe_worker.go); progress is polled separately
// via ProbeProgress.
func (h *adminHandler) ScanMissingDurations(c *gin.Context) {
	if h.probeWorker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"error":  "probe worker is not initialized",
		})
		return
	}
	episodes, err := h.episodeRepo.ListByNullDuration()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "failed",
			"error":  "failed to query episodes: " + err.Error(),
		})
		return
	}

	ids := make([]uint, 0, len(episodes))
	for _, ep := range episodes {
		ids = append(ids, ep.ID)
	}
	enqueued := h.probeWorker.EnqueueBatch(ids)
	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"queued":   enqueued,
		"total":    len(ids),
		"message":  fmt.Sprintf("已排队 %d 集等待探测时长（串行限速，约每集 4 秒）", enqueued),
	})
}

// ProbeProgress returns the worker's current progress snapshot. The admin UI
// polls this every couple of seconds while a scan is running.
func (h *adminHandler) ProbeProgress(c *gin.Context) {
	if h.probeWorker == nil {
		c.JSON(http.StatusOK, gin.H{"running": false})
		return
	}
	c.JSON(http.StatusOK, h.probeWorker.Stats())
}
