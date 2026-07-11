package handler

import (
	"net/http"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// UserHandler manages auth and endpoint requests from mobile PAD.
type UserHandler interface {
	GetUsers(c *gin.Context)
	Login(c *gin.Context)
}

type userHandler struct {
	userService service.UserService
}

// NewUserHandler creates an instance of UserHandler.
func NewUserHandler(us service.UserService) UserHandler {
	return &userHandler{userService: us}
}

func (h *userHandler) GetUsers(c *gin.Context) {
	users, err := h.userService.GetUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
		return
	}

	// Filter out sensitive PIN hash from client response
	type userResponse struct {
		ID        uint   `json:"id"`
		Nickname  string `json:"nickname"`
		AvatarURL string `json:"avatar_url"`
		Role      string `json:"role"`
	}

	res := make([]userResponse, len(users))
	for i, u := range users {
		res[i] = userResponse{
			ID:        u.ID,
			Nickname:  u.Nickname,
			AvatarURL: u.AvatarURL,
			Role:      u.Role,
		}
	}

	c.JSON(http.StatusOK, res)
}

func (h *userHandler) Login(c *gin.Context) {
	var req struct {
		UserID uint   `json:"user_id" binding:"required"`
		Pin    string `json:"pin" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body format"})
		return
	}

	valid, err := h.userService.Authenticate(req.UserID, req.Pin)
	if err != nil {
		respondError(c, err)
		return
	}

	if !valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "incorrect user PIN code"})
		return
	}

	// Fetch target user metadata to attach role to session
	users, err := h.userService.GetUsers()
	var role string
	if err == nil {
		for _, u := range users {
			if u.ID == req.UserID {
				role = u.Role
				break
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"token": req.UserID, // simple token format for MVP
		"role":  role,
	})
}
