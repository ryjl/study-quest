package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"studyquest/backend/internal/model"
)

// admin_homework.go — 课后作业卷(Homework)的 admin handler 层。作业是 episode 级、
// 不绑 user 的通用卷(AI 单次生成、纯打印纸笔做、家长手批),和 Quiz(user×episode 个性化
// 小测)、Exam(user×course 题库抽题)平行但定位不同。详见 model/homework.go 的字段注释。
//
// 本文件只负责 HTTP 适配:解析路径参数 → 调 AIService 的已冻结方法 → JSON 返回。
// service 层(ai_service_homework.go)已全部实现并测试通过,homeworkRepo 未注入时返回
// ErrHomeworkNotEnabled(在 service_errors_init.go 注册到 503,文案"作业功能未启用")。
//
// 设计取舍:
//   - 单/批量 episode 重生成(v2 已实现):走通用 POST /admin/api/ai/jobs 端点的
//     case "homework"(handler admin_ai_jobs.go),前端在 RegenTab 勾选 episode 后入队。
//     service 层对应 EnqueueHomework(episodeIDs []uint),与 EnqueueSegment/Summary/Polish
//     三兄弟同形。单集重生成 = 勾选那一集;批量重生成 = 勾选多集。
//   - TriggerHomework(course-level 整门课)**已废弃**(v2 标注,二期清):前端不再用它,
//     保留仅为兜底/向后兼容。新代码请走 POST /admin/api/ai/jobs {job_type:"homework"}。
//   - ListHomeworks 只返回 homeworks 数组,不附 pending_episodes(避免 N+1 调
//     HasPendingHomeworkJob)。在途状态前端按 created_at + 轮询推断,或二期补一个
//     批量状态端点。
//   - prompt 配置端点的 subjectKey 走 query param ?key=math(不查 subjectRepo)。前端
//     在 subject 列表页已知 key,直接透传;service 用它算默认 prompt 配方(题型/题量)。

// TriggerHomework [DEPRECATED v2] 触发为某课程批量生成作业卷(整门课)。
// v2 起前端改用勾选式:POST /admin/api/ai/jobs {job_type:"homework", episode_ids:[...]},
// 走 admin_ai_jobs.go 的通用 switch + service.EnqueueHomework(逐 episode 入队 + skipped map)。
// 本端点(course-level)保留兜底但不再被前端调用,二期清理时连路由一起删。
//
//	POST /admin/api/ai/courses/:id/homework/generate
//
// 返回 enqueued = 本次新入队的 episode 数(0 不算错,只是全都在途或没素材)。
func (h *adminHandler) TriggerHomework(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	n, err := h.aiService.EnqueueHomeworkForCourse(courseID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enqueued": n})
}

// ListHomeworks 列某课程下所有作业卷(admin 列表页)。
//
//	GET /admin/api/ai/courses/:id/homeworks
//
// 直接返回 service 给的 []model.Homework(每个含 id/episode_id/course_id/version/
// status/created_at)。active 和 archived 都返回,前端按 status 分组(历史卷折叠显示)。
// 第一版不附在途状态(见文件头注释的取舍说明)。
func (h *adminHandler) ListHomeworks(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	homeworks, err := h.aiService.ListHomeworksByCourse(courseID)
	if err != nil {
		respondError(c, err)
		return
	}
	// 空切片(而非 nil)序列化成 [],前端不用判 null。
	if homeworks == nil {
		homeworks = []model.Homework{}
	}
	c.JSON(http.StatusOK, homeworks)
}

// GetHomework 取单份作业的完整内容(预览 / 打印用)。service 已组装好三层
// (Homework + Sections + Questions),前端直接渲染。
//
//	GET /admin/api/ai/homeworks/:id
//
// nil view = 无此作业(已删或从未生成)→ 404。
func (h *adminHandler) GetHomework(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的作业 id"})
		return
	}
	view, err := h.aiService.GetHomeworkViewByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if view == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "作业不存在"})
		return
	}
	c.JSON(http.StatusOK, view)
}

// GetHomeworkPromptConfig 取某 subject 的作业 system prompt 配置。
// 首次访问时 service 会 lazy 创建并灌入默认 prompt(按 subjectKey 选配方),所以总会有值。
//
//	GET /admin/api/ai/subjects/:id/homework-prompt?key=math
//
// subjectKey 走 query param(不查 subjectRepo):前端在 subject 列表页已知 key,直接透传。
// 缺省空串 → service 用通用默认配方。
func (h *adminHandler) GetHomeworkPromptConfig(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	subjectID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的科目 id"})
		return
	}
	subjectKey := c.Query("key")
	cfg, err := h.aiService.GetHomeworkPromptConfig(subjectID, subjectKey)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"subject_id":    cfg.SubjectID,
		"system_prompt": cfg.SystemPrompt,
		"updated_at":    cfg.UpdatedAt,
	})
}

// saveHomeworkPromptReq 是 PUT .../homework-prompt 的 body。SystemPrompt 必填
// (空串 = 清空 prompt,会让作业生成退回 service 内置兜底,对 admin 是不可控状态,故拦)。
type saveHomeworkPromptReq struct {
	SystemPrompt string `json:"system_prompt" binding:"required"`
}

// SaveHomeworkPromptConfig 覆盖某 subject 的作业 system prompt(admin 编辑后保存)。
//
//	PUT /admin/api/ai/subjects/:id/homework-prompt?key=math
//
// 全量覆盖(不是 patch)——前端 GET 拿到完整 prompt → 编辑 → 整体 PUT 回来,语义简单。
func (h *adminHandler) SaveHomeworkPromptConfig(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	subjectID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的科目 id"})
		return
	}
	var req saveHomeworkPromptReq
	if !bindJSON(c, &req) {
		return
	}
	subjectKey := c.Query("key")
	if err := h.aiService.SaveHomeworkPromptConfig(subjectID, subjectKey, req.SystemPrompt); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ResetHomeworkPromptConfig 把某 subject 的 prompt 重置回默认(admin"恢复默认"按钮)。
// service 会用 defaultHomeworkPrompt(subjectKey) 重灌一遍。
//
//	POST /admin/api/ai/subjects/:id/homework-prompt/reset?key=math
func (h *adminHandler) ResetHomeworkPromptConfig(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	subjectID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的科目 id"})
		return
	}
	subjectKey := c.Query("key")
	if err := h.aiService.ResetHomeworkPromptConfig(subjectID, subjectKey); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
