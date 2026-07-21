package handler

import (
	"net/http"
	"strconv"
	"github.com/gin-gonic/gin"
)

// Code split from admin_ai.go for navigability.

type regenerateUserQuizRequest struct {
	EpisodeID uint `json:"episode_id" binding:"required"`
}

// RegenerateUserQuiz 给某学生重出一套某 episode 的题(archive 旧 active quiz,插新 active)。
// POST /admin/api/ai/users/:userID/quizzes/regenerate  body: {"episode_id":123}
// 返回 {status: "generating" | "unavailable"}(对齐客户端 RegenerateQuiz 语义)。
func (h *adminHandler) RegenerateUserQuiz(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	var req regenerateUserQuizRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效(需要 episode_id)"})
		return
	}
	status, err := h.aiService.RegenerateQuizForUser(userID, req.EpisodeID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// regenerateUserAdviceRequest 是 POST /admin/api/ai/users/:userID/advice/regenerate 的 body。
type regenerateUserAdviceRequest struct {
	Scope   string `json:"scope" binding:"required"`             // episode | course | subject
	ScopeID uint   `json:"scope_id" binding:"required"`          // 对应实体 id
}

// RegenerateUserAdvice 强制重生成某 (user, scope, scopeID) 的 advice(覆盖旧记录)。
// 三档 scope 都支持;这是 course/subject 级 advice 的唯一刷新入口。
// POST /admin/api/ai/users/:userID/advice/regenerate  body: {"scope":"course","scope_id":5}
func (h *adminHandler) RegenerateUserAdvice(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	var req regenerateUserAdviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效(需要 scope + scope_id)"})
		return
	}
	if req.Scope != "episode" && req.Scope != "course" && req.Scope != "subject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope 必须是 episode / course / subject"})
		return
	}
	status, err := h.aiService.RegenerateAdvice(userID, req.Scope, req.ScopeID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// ListUserAdvice 列出某用户的所有 advice(三档 scope,所有 scope_id),按 generated_at DESC。
// GET /admin/api/ai/users/:userID/advice
// 给 AI 控制台"这个学生有哪些 advice + 删除按钮"用。
func (h *adminHandler) ListUserAdvice(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	rows, err := h.aiService.ListUserAdvice(userID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// DeleteAISummary 删除某 episode 的 summary(物理删,覆盖式重新生成的对照操作)。
// DELETE /admin/api/ai/summaries/:episodeID
func (h *adminHandler) DeleteAISummary(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	episodeID, err := parseUintParam(c, "episodeID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 episodeID"})
		return
	}
	if err := h.aiService.DeleteSummary(episodeID); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteAIQuiz 删除一条 quiz(物理删,Fk CASCADE 自动清 Question + Answer)。
// DELETE /admin/api/ai/quizzes/:quizID
// 注意:这和 archive 不同 —— archive 保留历史(只翻 status='archived'),delete 彻底清除。
func (h *adminHandler) DeleteAIQuiz(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	quizID, err := parseUintParam(c, "quizID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 quizID"})
		return
	}
	if err := h.aiService.DeleteQuiz(quizID); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteUserAdvice 删除某 (user, scope, scope_id) 的 advice。scope/scope_id 从 query 取
// (DELETE body 用得不普遍,且语义上是"删除某条",query 参数更直观)。
// DELETE /admin/api/ai/users/:userID/advice?scope=episode&scope_id=123
func (h *adminHandler) DeleteUserAdvice(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	scope := c.Query("scope")
	if scope != "episode" && scope != "course" && scope != "subject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope 参数必须是 episode / course / subject"})
		return
	}
	scopeIDStr := c.Query("scope_id")
	scopeID, err := strconv.ParseUint(scopeIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope_id 参数无效"})
		return
	}
	if err := h.aiService.DeleteAdvice(userID, scope, uint(scopeID)); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteCourseSummary 删除某课程的总结。
// DELETE /admin/api/ai/courses/:id/course-summary
func (h *adminHandler) DeleteCourseSummary(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	if err := h.aiService.DeleteCourseSummary(courseID); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteUserStudyReport 删除某用户的学习报告。
// DELETE /admin/api/ai/users/:userID/study-report
func (h *adminHandler) DeleteUserStudyReport(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	if err := h.aiService.DeleteUserReport(userID); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
