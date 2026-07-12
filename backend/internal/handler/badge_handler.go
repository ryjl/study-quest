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

	statuses, err := h.badgeService.UserBadgeStatuses(uint(userID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query user badges: " + err.Error()})
		return
	}
	if statuses == nil {
		statuses = []service.BadgeStatus{}
	}
	c.JSON(http.StatusOK, statuses)
}

func (h *badgeHandler) AdminListBadges(c *gin.Context) {
	list, err := h.badgeService.List()
	if err != nil {
		respondError(c, err)
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
		Threshold   int    `json:"threshold"`
		Tiers       string `json:"tiers"`     // multi-tier JSON; when set, takes precedence over Threshold
		RuleJSON    string `json:"rule_json"` // composite rule tree; when set, RuleType="composite"
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	// When a composite rule JSON is supplied, normalize RuleType to "composite"
	// so list views can distinguish composite from single-rule badges. The
	// legacy single-rule fields are kept as-is for back-compat display.
	ruleType := req.RuleType
	if req.RuleJSON != "" {
		ruleType = "composite"
	}
	if req.Threshold == 0 && ruleType != "composite" && req.Tiers == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "threshold or tiers is required for non-composite badges"})
		return
	}

	badge := &model.Badge{
		Code:        req.Code,
		Title:       req.Title,
		Description: req.Description,
		IconName:    req.IconName,
		RuleType:    ruleType,
		RuleTarget:  req.RuleTarget,
		Threshold:   req.Threshold,
		Tiers:       req.Tiers,
		RuleJSON:    req.RuleJSON,
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
		Threshold   int    `json:"threshold"`
		Tiers       string `json:"tiers"`
		RuleJSON    string `json:"rule_json"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	ruleType := req.RuleType
	if req.RuleJSON != "" {
		ruleType = "composite"
	}
	if req.Threshold == 0 && ruleType != "composite" && req.Tiers == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "threshold or tiers is required for non-composite badges"})
		return
	}

	badge, err := h.badgeService.FindByID(uint(badgeID))
	if err != nil {
		respondError(c, err)
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
	badge.RuleType = ruleType
	badge.RuleTarget = req.RuleTarget
	badge.Threshold = req.Threshold
	badge.Tiers = req.Tiers
	badge.RuleJSON = req.RuleJSON
	// IsSystem is preserved from the loaded row — admins can't flip it via the
	// update endpoint, only the seeder marks defaults.

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
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
