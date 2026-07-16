package agent

import (
	"context"
	"fmt"
	"strings"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// course_summary_tools.go 定义 course summary agent 专用的工具集(NewCourseSummaryToolbox)。
// 和 advice_tools.go 的 NewAdviceToolbox 共享同一个 ReAct loop 和 Toolbox 结构,但工具语义
// 不同:
//
//   - advice agent 关心"这个学生哪里弱"(per-user mastery + chunk.text);
//   - course summary agent 关心"这门课整体讲什么"(遍历 episode + 读 summary,综合课程脉络)。
//
// 工具集只有 2 个(精简为上——课程总结的输入就是 episode + summary,不需要 mastery/embedder):
//
//   - get_course_episodes:列课程所有 episode(id + 标题),供 agent 遍历(虽然 pre-seed 已
//     给了列表 + headline,但 agent 想确认结构时仍可调);
//   - get_episode_summary:读指定 episode 的 summary,带 episode_id 参数(关键:和 advice 的
//     同名工具不同,advice 版按 toolbox scope 的 t.episodeID 查;course summary 版必须能查
//     任意 episode,因为 agent 要遍历整门课)。
//
// 工具集内每个工具仍受 Toolbox 的 courseID scope 约束(server-enforced):course summary
// 工具按 courseID 限定,工具返回的 episode 必须属于这门课(get_course_episodes 用 courseID
// 过滤;get_episode_summary 校验 episode_id 属于 courseID,防止 agent 越权查别的课程的
// episode summary)。和 advice/quiz 工具集同一套安全模型。

// NewCourseSummaryToolbox 构建 course summary agent 的工具集。和 NewAdviceToolbox 平行,但
// 不带 memory/advice 字段(course summary 不读 mastery),也不需要 subjectID/userID(course-
// unique,不针对个人)。courseID 是唯一的 scope:工具只能访问这门课程下的 episode。
func NewCourseSummaryToolbox(deps ToolDeps, courseID uint) *Toolbox {
	tb := &Toolbox{
		deps:      deps,
		episodeID: 0, // course summary 工具集不绑定单一 episode(要遍历整门课)
		userID:    0, // course summary 不读个人数据
		courseID:  courseID,
		funcs:     map[string]toolFunc{},
	}
	registerCourseSummaryTools(tb)
	return tb
}

// registerCourseSummaryTools 注册 2 个 course summary 工具。拆出来便于测试(单独验证注册结果)。
func registerCourseSummaryTools(tb *Toolbox) {
	tb.register("get_course_episodes", courseSummaryEpisodesSpec, func(ctx context.Context, args string) (string, error) {
		return tb.runCourseSummaryEpisodes(ctx)
	})
	tb.register("get_episode_summary", courseSummaryEpisodeSummarySpec, func(ctx context.Context, args string) (string, error) {
		return tb.runCourseSummaryEpisodeSummary(ctx, args)
	})
}

// ---------------------------------------------------------------------------
// Tool: get_course_episodes
// ---------------------------------------------------------------------------

var courseSummaryEpisodesSpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_course_episodes",
		Description: "列出这门课程的所有 episode(按顺序),每条带 id + 标题。用于了解课程结构和章节划分、决定接下来深入查看哪些 episode。用户消息里通常会预先给你这份列表(带每集概括),本工具用于确认或刷新。",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// runCourseSummaryEpisodes 调 deps.ListCourseEpisodes(courseID),返回 id + 标题列表。
// 不附带 summary(那要走 get_episode_summary,按需调用,避免一次性返回所有 summary 撑爆
// observation)。列表按 deps 返回的顺序(episodeRepo.ListByCourse 按 sort_order asc)。
func (t *Toolbox) runCourseSummaryEpisodes(ctx context.Context) (string, error) {
	_ = ctx
	episodes, err := t.deps.ListCourseEpisodes(t.courseID)
	if err != nil {
		return "", fmt.Errorf("course_summary get_course_episodes: %w", err)
	}
	if len(episodes) == 0 {
		return "这门课程暂无 episode(可能是空课程或导入异常)。请如实说明信息有限。", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "课程 #%d 的 episode 列表(共 %d 节,按顺序):\n", t.courseID, len(episodes))
	for i, ep := range episodes {
		fmt.Fprintf(&b, "%d. [id=%d] %s\n", i+1, ep.ID, ep.Title)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ---------------------------------------------------------------------------
// Tool: get_episode_summary (course summary 版,带 episode_id 参数)
// ---------------------------------------------------------------------------

var courseSummaryEpisodeSummarySpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_episode_summary",
		Description: "读取这门课程里某节 episode 的 AI 总结(核心概念、要点、概括),用于深入了解某节课讲了什么知识点。必须传 episode_id(从 get_course_episodes 或 pre-seed 列表里拿)。只能查本课程的 episode;传别课程的 episode_id 会返回错误。多次调用可串起整门课的知识脉络。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"episode_id": map[string]any{
					"type":        "integer",
					"description": "要查看的 episode id(必须属于当前课程)",
				},
			},
			"required": []string{"episode_id"},
		},
	},
}

// runCourseSummaryEpisodeSummary 读指定 episode 的 summary。和 advice 的同名工具关键差异:
// 接受 episode_id 参数(模型传入),而不是用 toolbox scope 的 t.episodeID——因为 course
// summary agent 要遍历整门课的多个 episode,不能被绑死在某一节。
//
// 安全:先校验 episode 属于本课程(deps.GetEpisode → ep.CourseID == t.courseID)。这是工具集
// 的 server-enforced scope(同 quiz/advice):agent 不能用这个工具查别课程的 episode summary,
// 即使它猜了一个别的课程的 episode_id。
func (t *Toolbox) runCourseSummaryEpisodeSummary(ctx context.Context, args string) (string, error) {
	_ = ctx
	// parseIntArg 返回 -1 表示缺失/无效(哨兵值,见 helpers.go)。先判 -1 再转 uint,
	// 否则 uint(-1) 会变成巨大的无符号值,scope 校验会走到"不属于本课程"分支而不是
	// "缺少参数"分支——错误消息对 agent 不够精确。
	idx := parseIntArg(args, "episode_id")
	if idx <= 0 {
		return "错误:缺少或无效的 episode_id 参数", nil
	}
	episodeID := uint(idx)
	// scope 校验:episode 必须属于本课程。GetEpisode 返回 nil(ep 不存在)或 CourseID 不匹配
	// 都拒绝——防止 agent 越权查别课程的 summary。
	ep, err := t.deps.GetEpisode(episodeID)
	if err != nil {
		return "", fmt.Errorf("course_summary get_episode_summary: load episode: %w", err)
	}
	if ep == nil || ep.CourseID != t.courseID {
		return fmt.Sprintf("错误:episode #%d 不属于本课程(或不存在),不能查看。", episodeID), nil
	}
	sum, err := t.deps.GetSummary(episodeID)
	if err != nil {
		return "", fmt.Errorf("course_summary get_episode_summary: %w", err)
	}
	if sum == nil {
		return fmt.Sprintf("episode #%d (%s) 还没有 AI 总结。", episodeID, ep.Title), nil
	}
	parsed, perr := parseSummaryForTools(sum.SummaryJSON)
	if perr != nil {
		return fmt.Sprintf("episode #%d (%s) 的总结解析失败。", episodeID, ep.Title), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "episode #%d (%s) 的总结:\n", episodeID, ep.Title)
	if parsed.Headline != "" {
		fmt.Fprintf(&b, "- 概括: %s\n", parsed.Headline)
	}
	if len(parsed.Concepts) > 0 {
		fmt.Fprintf(&b, "- 核心概念: %s\n", strings.Join(parsed.Concepts, "、"))
	}
	if len(parsed.KeyPoints) > 0 {
		b.WriteString("- 要点:\n")
		for _, kp := range parsed.KeyPoints {
			fmt.Fprintf(&b, "  · %s\n", kp)
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return fmt.Sprintf("episode #%d (%s) 的总结为空。", episodeID, ep.Title), nil
	}
	return out, nil
}

// Compile-time: course summary 工具集依赖的 ToolDeps 方法(ListCourseEpisodes)在接口里
// 已声明(见 tools.go)。model import 保留给未来可能的工具扩展(目前工具返回纯文本,但
// deps 接口签名引用 model.Episode)。
var _ = model.ContentChunk{}
