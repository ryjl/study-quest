package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"studyquest/backend/internal/service"
)

// exam_handler.go — 课程考试的 client-facing 端点(Flutter 学生端)。
//
// 端点组(PascalCase, v1Restricted 组):
//   GET   /api/v1/courses/:id/exam/status  是否可考(gate,题库够不够)
//   POST  /api/v1/courses/:id/exam/start   开考(组卷,返回 exam + questions)
//   GET   /api/v1/courses/:id/exam         取已开考的 active exam
//   POST  /api/v1/exams/:id/submit         交卷(逐题判分 + 报告)
//
// 访问控制:start/submit 走 canAccessCourse(考试数据按 user_id 键存不泄露,但
// 不应让未授权课程触发组卷/抽题);status/gate 只需登录。

// GetExamStatus 是否可考。GET /api/v1/courses/:id/exam/status
func (h *aiHandler) GetExamStatus(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, gin.H{"available": false, "reason": "考试功能未启用"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID"})
		return
	}
	st, err := h.aiService.GetExamStatus(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "检查考试状态失败"})
		return
	}
	c.JSON(http.StatusOK, st)
}

// StartExam 开考。POST /api/v1/courses/:id/exam/start
func (h *aiHandler) StartExam(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	if !h.canAccessCourse(c, userID, uint(id)) {
		return
	}
	view, err := h.aiService.StartExam(userID, uint(id))
	if err != nil {
		if errors.Is(err, service.ErrExamInsufficientPool) {
			c.JSON(http.StatusConflict, gin.H{"error": "课程题库不足,学完更多课后解锁考试"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "组卷失败"})
		return
	}
	c.JSON(http.StatusOK, view)
}

// GetActiveExam 取已开考的 active exam。GET /api/v1/courses/:id/exam
func (h *aiHandler) GetActiveExam(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid course ID"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	if !h.canAccessCourse(c, userID, uint(id)) {
		return
	}
	view, err := h.aiService.GetActiveExamView(userID, uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载考试卷失败"})
		return
	}
	if view == nil {
		c.JSON(http.StatusOK, gin.H{"status": "none"}) // 未开考
		return
	}
	c.JSON(http.StatusOK, view)
}

// SubmitExam 交卷。POST /api/v1/exams/:id/submit
func (h *aiHandler) SubmitExam(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid exam ID"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req submitAllQuizRequest // 复用 {answers:[...]} 形状
	if !bindJSON(c, &req) {
		return
	}
	report, err := h.aiService.SubmitExam(userID, uint(id), req.Answers)
	if err != nil {
		if errors.Is(err, service.ErrExamAlreadySubmitted) {
			c.JSON(http.StatusConflict, gin.H{"error": "这套考试卷已交卷,不能重复提交"})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "考试卷不存在或无权作答"})
		return
	}
	c.JSON(http.StatusOK, report)
}
