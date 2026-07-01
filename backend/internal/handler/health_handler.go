package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HealthHandler handles basic monitoring health check.
type HealthHandler interface {
	Check(c *gin.Context)
}

type healthHandler struct{}

// NewHealthHandler creates a HealthHandler.
func NewHealthHandler() HealthHandler {
	return &healthHandler{}
}

func (h *healthHandler) Check(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":      "ok",
		"environment": "production",
	})
}
