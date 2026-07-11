package handler

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"studyquest/backend/internal/appclock"
)


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
		respondError(c, err)
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
		respondError(c, err)
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
		respondError(c, err)
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
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

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
		respondError(c, err)
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
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
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
		respondError(c, err)
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
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
