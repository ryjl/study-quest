package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"studyquest/backend/internal/service"
)

// admin_wrongbook_handler.go — 错题本的 client-facing 端点(Flutter 学生端)。
//
// 端点组(PascalCase, v1Restricted 组,只需登录):
//   GET    /api/v1/wrong-book?course_id=&mastered=   列错题本(可按课程/掌握状态过滤)
//   POST   /api/v1/wrong-book/:qid/master            标记掌握
//   POST   /api/v1/wrong-book/:qid/unmaster          取消掌握
//   GET    /api/v1/wrong-book/redo?course_id=&limit= 取一批未掌握错题做重做卷
//   POST   /api/v1/wrong-book/redo/submit            重做交卷
//
// 访问控制:错题本数据按 user_id 键存,绝不泄露跨用户数据(和 advice course/subject
// 级同口径),所以只需 requireUserID,无需 course access gate——学生查一个没权限的
// course 只会拿到他自己在这个 course 上的(多半空的)错题,看不到别人的。

// wrongBookListResponse 列错题本的响应。UnmasteredCount 是该用户未掌握错题总数
// (独立于 items 的过滤——默认"全部"视图时 items 含已掌握,但角标只数未掌握的)。
type wrongBookListResponse struct {
	Items          []service.WrongBookItemView `json:"items"`
	UnmasteredCount int64                       `json:"unmastered_count"`
}

// GetWrongBook 列错题本。course_id=0(缺省)表全局;mastered=true/false 过滤掌握状态,
// 缺省不过滤(返回全部)。nil-safe:AI 未配置时返回空列表。
// GET /api/v1/wrong-book
func (h *aiHandler) GetWrongBook(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, wrongBookListResponse{Items: []service.WrongBookItemView{}})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	courseID, _ := strconv.ParseUint(c.Query("course_id"), 10, 64)
	var mastered *bool
	if m := c.Query("mastered"); m == "true" {
		b := true
		mastered = &b
	} else if m == "false" {
		b := false
		mastered = &b
	}
	items, err := h.aiService.GetWrongBook(userID, uint(courseID), mastered)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载错题本失败"})
		return
	}
	// 角标数:未掌握总数(独立于 items 的过滤,给 tab 红点用)。
	unmastered, _ := h.aiService.UnmasteredCount(userID)
	c.JSON(http.StatusOK, wrongBookListResponse{Items: items, UnmasteredCount: unmastered})
}

// MarkWrongBookMastered 标记/取消掌握。POST /api/v1/wrong-book/:id/master 或 /unmaster
func (h *aiHandler) MarkWrongBookMastered(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
		return
	}
	qid, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid question ID"})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	// /master → true, /unmaster → false。按路径末段区分。
	mastered := !strings.HasSuffix(c.Request.URL.Path, "/unmaster")
	if err := h.aiService.MarkWrongBookMastered(userID, uint(qid), mastered); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新掌握状态失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "mastered": mastered})
}

// GetWrongBookRedo 取重做卷。GET /api/v1/wrong-book/redo?course_id=&limit=
func (h *aiHandler) GetWrongBookRedo(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, gin.H{"questions": []interface{}{}})
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	courseID, _ := strconv.ParseUint(c.Query("course_id"), 10, 64)
	limit, _ := strconv.Atoi(c.Query("limit"))
	questions, err := h.aiService.RedoWrongBookQuiz(userID, uint(courseID), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载重做题失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"questions": questions})
}

// SubmitWrongBookRedo 重做交卷。POST /api/v1/wrong-book/redo/submit
func (h *aiHandler) SubmitWrongBookRedo(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI not available"})
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
	results, err := h.aiService.SubmitWrongBookRedo(userID, req.Answers)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "重做判分失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}
