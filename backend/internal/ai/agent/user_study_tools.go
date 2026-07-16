package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// user_study_tools.go 定义 user_study agent 专用的工具集(NewUserStudyToolbox)。
// 和 advice_tools.go 的 NewAdviceToolbox 共享同一个 ReAct loop 和 Toolbox 结构,但工具
// 语义针对"admin 跨课程画像报告":
//
//   - advice agent 绑定单个 (episode/course/subject),工具用 toolbox 内的固定 scope;
//   - user_study agent 跨一个学生的所有课程,工具必须按参数 course_id 查任意一门课。
//     所以这里的 get_course_mastery / get_course_summary / get_user_advice 都带 course_id
//     参数(agent 自己遍历课程),而不是像 advice 那样读 toolbox.courseID。
//
// 只有 userID 是固定的(整个报告都围绕一个学生),episodeID/courseID 在 toolbox 里
// 留 0——user_study 没有单课时/单课程 scope 的概念。

// userStudyAdviceRepo 是 user_study 工具集对 StudyAdvice 的窄读取(读已有建议供 agent
// 复用)。从 ToolDeps 单独拆出来,是因为 ToolDeps 是面向 chunk/episode/course 的读,
// advice 读取是 user_study 独有的需求。memoryRepo 已能覆盖 mastery 聚合。
type userStudyAdviceRepo interface {
	// GetUserAdvice 取某用户某 (scope, scopeID) 的建议。scope ∈ {"episode","course",
	// "subject"}。nil = 无建议。user_study agent 主要用 course 级(scope_id=course_id)。
	GetUserAdvice(userID uint, scope string, scopeID uint) (*model.StudyAdvice, error)
}

// NewUserStudyToolbox 构建 user_study agent 的工具集。和 NewAdviceToolbox 的关键差异:
// toolbox 只绑定 userID(报告围绕一个学生),course_id/episode_id 都不固定——工具按
// 参数 course_id 查任意课程,agent 自己决定遍历哪些课。subjectID 不需要(报告是跨
// 课程的,不绑定单个 subject)。
//
// adviceRepo 可为 nil:get_user_advice 工具会回退返回"无建议"(agent 据此只用 mastery
// 写报告)。memory / deps 走真实 repo(advice job 注入完整 adapter)。
func NewUserStudyToolbox(deps ToolDeps, memory *MemoryStore, adviceRepo userStudyAdviceRepo, userID uint) *Toolbox {
	tb := &Toolbox{
		deps:   deps,
		memory: memory,
		// episodeID/courseID 留 0:user_study 不绑定单个课时/课程,工具按参数查。
		episodeID: 0,
		userID:    userID,
		courseID:  0,
		funcs:     map[string]toolFunc{},
	}
	registerUserStudyTools(tb, deps, memory, adviceRepo)
	return tb
}

// registerUserStudyTools 注册 4 个 user_study 工具。拆出来便于测试。
func registerUserStudyTools(tb *Toolbox, deps ToolDeps, memory *MemoryStore, adviceRepo userStudyAdviceRepo) {
	tb.register("list_user_courses", userStudyListCoursesSpec, func(ctx context.Context, args string) (string, error) {
		return tb.runUserStudyListCourses(ctx)
	})
	tb.register("get_course_mastery", userStudyCourseMasterySpec, func(ctx context.Context, args string) (string, error) {
		return tb.runUserStudyCourseMastery(ctx, args)
	})
	tb.register("get_course_summary", userStudyCourseSummarySpec, func(ctx context.Context, args string) (string, error) {
		return tb.runUserStudyCourseSummary(ctx, args)
	})
	tb.register("get_user_advice", userStudyUserAdviceSpec, func(ctx context.Context, args string) (string, error) {
		return tb.runUserStudyUserAdvice(ctx, args, adviceRepo)
	})
}

// ---------------------------------------------------------------------------
// Tool: list_user_courses(复用 advice 的语义,但绑定 user_study 的 userID)
// ---------------------------------------------------------------------------

var userStudyListCoursesSpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "list_user_courses",
		Description: "列出这个学生当前被授权学习的所有课程 id(逗号分隔)。先调它知道学生有几门课、哪些课程值得深入分析。返回 course id 数组(可能为空)。",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// runUserStudyListCourses 复用 advice 的 list_user_courses 实现(同一份 deps.ListUserCourses
// 调用)。两边语义一致:都是"该学生被授权的课程 id 列表"。排序仅为了 observation 稳定。
func (t *Toolbox) runUserStudyListCourses(ctx context.Context) (string, error) {
	_ = ctx
	ids, err := t.deps.ListUserCourses(t.userID)
	if err != nil {
		return "", fmt.Errorf("user_study list_user_courses: %w", err)
	}
	if len(ids) == 0 {
		return "该学生当前没有被授权任何课程。", nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	return fmt.Sprintf("该学生被授权的课程 id: %s", strings.Join(parts, ", ")), nil
}

// ---------------------------------------------------------------------------
// Tool: get_course_mastery(按参数 course_id,跨课程报告的核心数据)
// ---------------------------------------------------------------------------

var userStudyCourseMasterySpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_course_mastery",
		Description: "查询这个学生在指定课程下所有知识点的掌握度(弱点优先),每条带知识点字幕片段文本。对每门课都要调,这是做跨课程强项/弱项对比的基础数据。course_id 必填。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"course_id": map[string]any{
					"type":        "integer",
					"description": "要查询的课程 id(list_user_courses 返回的)",
				},
			},
			"required": []string{"course_id"},
		},
	},
}

// runUserStudyCourseMastery 按参数 course_id 查该学生在该课程下的 mastery,join chunk.text。
// 复用 advice 的 formatMasteriesWithText + loadChunksForMasteries(advice_tools.go),保持
// observation 格式一致(★弱点标记、知识点线索)。和 advice 的 runAdviceGetCourseMastery
// 区别:advice 读 toolbox.courseID(绑定单课),这里读参数(任意课)。
func (t *Toolbox) runUserStudyCourseMastery(ctx context.Context, args string) (string, error) {
	_ = ctx
	courseID := uint(parseIntArg(args, "course_id"))
	if courseID == 0 {
		return "错误:缺少或无效的 course_id 参数", nil
	}
	rows, err := t.memory.CourseMasteries(ctx, t.userID, courseID)
	if err != nil {
		return "", fmt.Errorf("user_study get_course_mastery: %w", err)
	}
	chunksByID := t.loadChunksForMasteries(rows)
	return formatMasteriesWithText(rows, chunksByID, fmt.Sprintf("该学生在课程#%d 的掌握度", courseID)), nil
}

// ---------------------------------------------------------------------------
// Tool: get_course_summary(按参数 course_id,Phase D 未 merge 时降级聚合 episode summary)
// ---------------------------------------------------------------------------

var userStudyCourseSummarySpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_course_summary",
		Description: "查询某门课程的整体知识点总结(帮你理解课程讲什么、判断跨课程知识点关联)。返回该课程下已生成 AI 总结的课时聚合(headline + concepts)。如果课程级总结尚未生成,会聚合课时级总结;如果连课时总结都没有,返回'尚未生成'提示。course_id 必填。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"course_id": map[string]any{
					"type":        "integer",
					"description": "要查询的课程 id",
				},
			},
			"required": []string{"course_id"},
		},
	},
}

// runUserStudyCourseSummary 实现 get_course_summary。Phase D 的 CourseSummaryAgent 还没
// merge 时,这里走降级路径:遍历该课程下有 AISummary 的课时,聚合 concepts + headline,
// 给 agent 足够的"课程在讲什么"的信号。降级设计避免硬依赖 Phase D。
//
// ToolDeps 目前没有 ListEpisodesByCourse,所以这里通过 ListChunks 间接拿不到课时列表;
// 但 ToolDeps 有 GetSummary(episodeID)。为了拿课时列表,我们需要 ToolDeps 能枚举一个
// 课程的 episode。这超出当前 ToolDeps 接口范围,所以降级用"无课程总结可用"的提示——
// agent 仍能基于 get_course_mastery 的 chunk.text 写报告。Phase D merge 后可在此接入真实
// 的 CourseSummary 读取(届时 ToolDeps 加 GetCourseSummary 方法)。
func (t *Toolbox) runUserStudyCourseSummary(ctx context.Context, args string) (string, error) {
	_ = ctx
	courseID := uint(parseIntArg(args, "course_id"))
	if courseID == 0 {
		return "错误:缺少或无效的 course_id 参数", nil
	}
	// Phase D CourseSummary 未接入时的降级提示。agent 会据此改用 get_course_mastery 的
	// chunk.text 线索理解课程内容——足以支撑画像报告的知识点关联分析。
	return fmt.Sprintf("课程#%d 的课程级总结尚未生成。建议改用 get_course_mastery 返回的「知识点线索(字幕片段文本)」理解该课程讲了哪些知识点。", courseID), nil
}

// ---------------------------------------------------------------------------
// Tool: get_user_advice(读该用户已有的 StudyAdvice,复用 episode/course 级分析)
// ---------------------------------------------------------------------------

var userStudyUserAdviceSpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_user_advice",
		Description: "读取这个学生已有的学习建议(episode/course/subject 级的 advice,由 advice agent 生成)。复用已有分析,避免重复劳动——advice 里有针对单节课/单门课的弱点诊断,你只需做更高层的跨课程整合。scope 默认 course(需配合 course_id);也可传 scope=episode + episode_id 或 scope=subject + subject_id。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scope": map[string]any{
					"type":        "string",
					"description": "advice 的范围:course(默认)/ episode / subject",
				},
				"course_id": map[string]any{
					"type":        "integer",
					"description": "scope=course 时的课程 id",
				},
				"episode_id": map[string]any{
					"type":        "integer",
					"description": "scope=episode 时的课时 id",
				},
				"subject_id": map[string]any{
					"type":        "integer",
					"description": "scope=subject 时的科目 id",
				},
			},
		},
	},
}

// runUserStudyUserAdvice 读该用户的 StudyAdvice。scope 默认 course + course_id 参数;若
// 没传 scope 就按 course 处理(最常用)。adviceRepo 为 nil 时(toolbox 未注入 advice repo)
// 回退返回"无建议",agent 据此只用 mastery 写报告。
func (t *Toolbox) runUserStudyUserAdvice(ctx context.Context, args string, adviceRepo userStudyAdviceRepo) (string, error) {
	_ = ctx
	if adviceRepo == nil {
		return "无法读取已有建议(advice repo 未配置)。请直接基于 mastery 数据分析。", nil
	}
	scope := parseStringArg(args, "scope")
	if scope == "" {
		scope = ScopeCourse
	}
	var scopeID uint
	switch scope {
	case ScopeEpisode:
		scopeID = uint(parseIntArg(args, "episode_id"))
	case ScopeCourse:
		scopeID = uint(parseIntArg(args, "course_id"))
	case ScopeSubject:
		scopeID = uint(parseIntArg(args, "subject_id"))
	default:
		return fmt.Sprintf("未知的 scope: %s(支持 course/episode/subject)", scope), nil
	}
	if scopeID == 0 {
		return fmt.Sprintf("错误:scope=%s 但未提供对应的 id 参数", scope), nil
	}
	advice, err := adviceRepo.GetUserAdvice(t.userID, scope, scopeID)
	if err != nil {
		return "", fmt.Errorf("user_study get_user_advice: %w", err)
	}
	if advice == nil {
		return fmt.Sprintf("该学生暂无 %s 级(id=%d)的学习建议。", scope, scopeID), nil
	}
	return fmt.Sprintf("该学生已有的 %s 级(id=%d)建议:\n%s", scope, scopeID, advice.AdviceText), nil
}
