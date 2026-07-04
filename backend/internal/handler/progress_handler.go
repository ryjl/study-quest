package handler

import (
	"net/http"
	"strconv"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// ProgressHandler manages client playback progress and points retrieval.
type ProgressHandler interface {
	ReportProgress(c *gin.Context)
	GetProgressOverview(c *gin.Context)
	GetPoints(c *gin.Context)
	GetLastWatched(c *gin.Context)
	GetPointsLedger(c *gin.Context)
}

type progressHandler struct {
	progressService service.ProgressService
}

// NewProgressHandler creates an instance of ProgressHandler.
func NewProgressHandler(ps service.ProgressService) ProgressHandler {
	return &progressHandler{progressService: ps}
}

func (h *progressHandler) ReportProgress(c *gin.Context) {
	userIDVal, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user authentication context missing"})
		return
	}
	userID := userIDVal.(uint)

	var req struct {
		EpisodeID           uint `json:"episode_id" binding:"required"`
		PositionSeconds     int  `json:"position_seconds"`
		DeltaWatchSeconds   int  `json:"delta_watch_seconds" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body format"})
		return
	}

	prog, err := h.progressService.ReportProgress(userID, req.EpisodeID, req.PositionSeconds, req.DeltaWatchSeconds)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record progress: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, prog)
}

func (h *progressHandler) GetProgressOverview(c *gin.Context) {
	userIDVal, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user authentication context missing"})
		return
	}
	userID := userIDVal.(uint)

	progList, err := h.progressService.GetUserProgressOverview(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query progress: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, progList)
}

func (h *progressHandler) GetPoints(c *gin.Context) {
	userIDVal, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user authentication context missing"})
		return
	}
	userID := userIDVal.(uint)

	pt, err := h.progressService.GetPoints(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query points: " + err.Error()})
		return
	}

	if pt == nil {
		c.JSON(http.StatusOK, gin.H{
			"user_id":             userID,
			"current_points":      0,
			"total_earned_points": 0,
		})
		return
	}

	c.JSON(http.StatusOK, pt)
}

func (h *progressHandler) GetLastWatched(c *gin.Context) {
	userIDVal, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user authentication context missing"})
		return
	}
	userID := userIDVal.(uint)

	courseIDStr := c.Param("id")
	courseID, err := strconv.ParseUint(courseIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID format"})
		return
	}

	ep, prog, err := h.progressService.GetLastWatchedEpisode(userID, uint(courseID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query last watched: " + err.Error()})
		return
	}

	if ep == nil || prog == nil {
		c.JSON(http.StatusOK, gin.H{
			"episode":  nil,
			"progress": nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"episode":  ep,
		"progress": gin.H{
			"last_position_seconds": prog.LastPositionSeconds,
			"watch_seconds":         prog.WatchSeconds,
			"is_completed":          prog.IsCompleted,
		},
	})
}

func (h *progressHandler) GetPointsLedger(c *gin.Context) {
	userIDVal, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user authentication context missing"})
		return
	}
	userID := userIDVal.(uint)

	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	ledger, err := h.progressService.GetPointsLedger(userID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query ledger: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, ledger)
}
