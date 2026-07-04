package handler

import (
	"net/http"
	"strconv"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// BadgeHandler handles client queries and admin CRUD actions for Badges.
type BadgeHandler interface {
	GetUserBadges(c *gin.Context)
	
	// Admin API endpoints
	AdminListBadges(c *gin.Context)
	AdminCreateBadge(c *gin.Context)
	AdminUpdateBadge(c *gin.Context)
	AdminDeleteBadge(c *gin.Context)
}

type badgeHandler struct {
	badgeService service.BadgeService
}

// NewBadgeHandler creates an instance of BadgeHandler.
func NewBadgeHandler(bs service.BadgeService) BadgeHandler {
	return &badgeHandler{badgeService: bs}
}

func (h *badgeHandler) GetUserBadges(c *gin.Context) {
	idStr := c.Param("id")
	userID, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user ID"})
		return
	}

	// Fetch all defined badges
	allBadges, err := h.badgeService.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list badges: " + err.Error()})
		return
	}

	// Fetch unlocked badges
	unlockedBadges, err := h.badgeService.ListUserBadges(uint(userID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query user badges: " + err.Error()})
		return
	}

	unlockedMap := make(map[uint]bool)
	for _, ub := range unlockedBadges {
		unlockedMap[ub.ID] = true
	}

	type badgeStatusResponse struct {
		model.Badge
		Unlocked bool `json:"unlocked"`
	}

	var response []badgeStatusResponse
	for _, b := range allBadges {
		response = append(response, badgeStatusResponse{
			Badge:    b,
			Unlocked: unlockedMap[b.ID],
		})
	}

	c.JSON(http.StatusOK, response)
}

func (h *badgeHandler) AdminListBadges(c *gin.Context) {
	list, err := h.badgeService.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

func (h *badgeHandler) AdminCreateBadge(c *gin.Context) {
	var req struct {
		Code        string `json:"code" binding:"required"`
		Title       string `json:"title" binding:"required"`
		Description string `json:"description"`
		IconName    string `json:"icon_name" binding:"required"`
		RuleType    string `json:"rule_type" binding:"required"`
		RuleTarget  string `json:"rule_target"`
		Threshold   int    `json:"threshold" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	badge := &model.Badge{
		Code:        req.Code,
		Title:       req.Title,
		Description: req.Description,
		IconName:    req.IconName,
		RuleType:    req.RuleType,
		RuleTarget:  req.RuleTarget,
		Threshold:   req.Threshold,
	}

	if err := h.badgeService.Create(badge); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create badge: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, badge)
}

func (h *badgeHandler) AdminUpdateBadge(c *gin.Context) {
	idStr := c.Param("id")
	badgeID, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid badge ID"})
		return
	}

	var req struct {
		Code        string `json:"code" binding:"required"`
		Title       string `json:"title" binding:"required"`
		Description string `json:"description"`
		IconName    string `json:"icon_name" binding:"required"`
		RuleType    string `json:"rule_type" binding:"required"`
		RuleTarget  string `json:"rule_target"`
		Threshold   int    `json:"threshold" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	badge, err := h.badgeService.FindByID(uint(badgeID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if badge == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "badge not found"})
		return
	}

	badge.Code = req.Code
	badge.Title = req.Title
	badge.Description = req.Description
	badge.IconName = req.IconName
	badge.RuleType = req.RuleType
	badge.RuleTarget = req.RuleTarget
	badge.Threshold = req.Threshold

	if err := h.badgeService.Update(badge); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update badge: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, badge)
}

func (h *badgeHandler) AdminDeleteBadge(c *gin.Context) {
	idStr := c.Param("id")
	badgeID, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid badge ID"})
		return
	}

	if err := h.badgeService.Delete(uint(badgeID)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
