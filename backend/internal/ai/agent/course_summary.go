package agent

import (
	"context"
	"fmt"
	"strings"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/jsonx"
)

// course_summary.go 编排 agent 驱动的课程级总结(Phase D 的核心)。和 advice.go 平行:
// advice 跑 ReAct loop 出"针对某学生的复习建议";course summary 跑同一个 ReAct loop,只换
// system prompt(CourseSummarySystemPrompt)+ 工具集(NewCourseSummaryToolbox),产出
// "所有人共享的课程导览"。
//
// 复用 agent.Run(通用 ReAct 引擎),不复制 loop 代码——这是 Phase D "全 agent 驱动"的关键:
// 课程总结不是一次性 prompt engineering(把所有 episode summary 塞进一个 prompt),而是
// agent 自己用工具遍历 episode 摘要、自己决定综合的深度和取舍。这样大课程(几十节)和小
// 课程(几节)都能合理处理,不会撑爆 context。
//
// 和 advice 的关键差异:
//   - course-unique:课程总结不含个人 mastery(那是 advice 的事),按 course 唯一存储,
//     所有学生共享,admin 生成一次即可;
//   - pre-seed 是 episode 列表 + 每集 headline(不是 mastery 摘要),省掉 agent 逐个调
//     get_episode_summary 的初始轮次;
//   - 工具集只有 2 个:get_course_episodes + get_episode_summary(带 episode_id 参数版),
//     没有 mastery 类工具(总结是纯内容,与具体学生无关)。

// CourseSummaryRequest 是一次课程总结生成的输入。CourseID/CourseTitle/Subject 是核心上下文
// (agent 据此知道"这是哪门课、什么科目");UserID 仅用于 job 追踪/admin 可观测(课程总结本身
// 是 course-unique 的纯内容总结,不含个人维度)。
type CourseSummaryRequest struct {
	CourseID    uint
	CourseTitle string
	Subject     string // 科目名(如"数学"),供 prompt 引用
	// UserID 仅用于 admin 触发链路上的 job 归属/可观测(存 job.UserID)。课程总结本身不含
	// 个人维度——SummaryText 是 course-unique 的,所有学生共享。
	UserID uint
}

// CourseSummaryResult 是 Generate 的返回:总结文本 + agent trace(供 ai_runs 记录)+ usage。
// 没有 MasterySnapshot(advice 有,因为 advice 按 user 存要对比进步;course summary 不涉及
// 个人 mastery,无需快照)。
type CourseSummaryResult struct {
	SummaryText string
	Trace       []TraceStep
	Usage       ai.Usage
	Turns       int
	// SystemPrompt / UserPrompt 透传自 AgentResult:本次课程总结发给 LLM 的开场
	// system+user prompt。供 service 层写进 ai_runs.system_prompt_text /
	// user_prompt_text,让 admin "查看回放"能看到这次到底发了什么 prompt。
	SystemPrompt string
	UserPrompt   string
}

// CourseSummaryAgent 用 ReAct loop 生成课程级总结。持有一个带 course summary 工具集的 agent。
// 没有 self-check agent(同 advice:开放文本,无客观正误,只做非空 + 长度合理性检查)。
type CourseSummaryAgent struct {
	agent *Agent   // 带 NewCourseSummaryToolbox 的 ReAct 引擎
	deps  ToolDeps // pre-seed 用:遍历 course 的 episodes + 读 summary
}

// NewCourseSummaryAgent 构造一个 CourseSummaryAgent。agentLoop 必须已经注入
// NewCourseSummaryToolbox。deps 用于 pre-seed(读课程下所有 episode + headline)。
func NewCourseSummaryAgent(agentLoop *Agent, deps ToolDeps) *CourseSummaryAgent {
	return &CourseSummaryAgent{agent: agentLoop, deps: deps}
}

// Generate 跑一次课程总结生成。流程:
//  1. pre-seed:遍历课程下所有 episode,读每集的 AISummary 解析出 headline,拼成 episode 列表
//     塞进 user prompt。省掉 agent 逐个调 get_course_episodes + N 次 get_episode_summary
//     的初始轮次——agent 一上来就有完整的课程结构 + 概括,可以直接开写或选择性深入。
//  2. agent.Run(ctx, CourseSummarySystemPrompt, userPrompt) —— ReAct loop,agent 自己按需
//     调 get_episode_summary 深入查看某些 episode 的核心概念。
//  3. 轻量校验:FinalText 非空(空答案返回错误,让 job 标 failed,而不是存一条空总结)。
//
// 和 advice.Generate 的差异:pre-seed 来源不同(advice 读 mastery,course summary 读 episode
// 列表 + headline);校验更宽松(不收集 mastery snapshot)。
func (a *CourseSummaryAgent) Generate(ctx context.Context, req CourseSummaryRequest) (*CourseSummaryResult, error) {
	episodeSeed := a.buildCourseSummarySeed(ctx, req)
	userPrompt := buildCourseSummaryUserPrompt(req, episodeSeed)
	res, err := a.agent.Run(ctx, CourseSummarySystemPrompt, userPrompt)
	if err != nil {
		// agent.Run 失败时(含 ErrMaxSteps)仍返回带 partial trace 的 result——透传给
		// service 层落 ai_runs 排查(和 quizzer/advice/user_study 一致)。
		return &CourseSummaryResult{Trace: res.Trace, Usage: res.Usage, Turns: res.Turns,
			SystemPrompt: res.SystemPrompt, UserPrompt: res.UserPrompt},
			fmt.Errorf("course summary agent: %w", err)
	}
	summaryText := strings.TrimSpace(res.FinalText)
	if summaryText == "" {
		// agent 给了空答案(罕见,通常是模型异常)。返回错误让 job 标 failed,
		// 而不是存一条空总结误导学生。
		return nil, fmt.Errorf("course summary agent: empty final answer")
	}
	return &CourseSummaryResult{
		SummaryText: summaryText,
		Trace:       res.Trace,
		Usage:       res.Usage,
		Turns:       res.Turns,
		// 透传 seed prompt 供 service 层落 ai_runs(诊断时能看到这次发了什么)。
		SystemPrompt: res.SystemPrompt,
		UserPrompt:   res.UserPrompt,
	}, nil
}

// buildCourseSummarySeed 遍历课程下所有 episode,读每集的 AISummary 解析出 headline,拼成
// 给 prompt 的 episode 列表。每行格式:"[episode_id] 标题 — 概括(无总结)"。
//
// 这是 pre-seed 的核心:让 agent 开写前就有完整的课程结构 + 每集一句话概括,不必逐个调
// get_episode_summary。headline 来自 AISummary.SummaryJSON 的解析(tools.go 的
// parseSummaryForTools 同款解析逻辑,这里内联一份更轻量的——只需要 headline);没有 summary
// 的 episode 标"(无总结)",agent 据此知道哪些 episode 信息有限。
//
// 失败容忍:任一 episode 的 ListChunks/GetSummary 出错都不中断——pre-seed 是 nice-to-have,
// agent 仍可以用工具补查。episode 列表本身从 deps.ListCourseEpisodes 拿,这是 pre-seed 的
// 唯一硬依赖(连 episode 都列不出来,pre-seed 就没意义,但仍返回空串让 prompt 降级)。
func (a *CourseSummaryAgent) buildCourseSummarySeed(ctx context.Context, req CourseSummaryRequest) string {
	_ = ctx
	episodes, err := a.deps.ListCourseEpisodes(req.CourseID)
	if err != nil || len(episodes) == 0 {
		return "" // pre-seed 失败,prompt 会显示"暂无 episode"提示
	}
	var b strings.Builder
	for _, ep := range episodes {
		headline := "(无总结)"
		if sum, serr := a.deps.GetSummary(ep.ID); serr == nil && sum != nil {
			if h := parseHeadlineOnly(sum.SummaryJSON); h != "" {
				headline = h
			}
		}
		fmt.Fprintf(&b, "- [%d] %s — %s\n", ep.ID, ep.Title, headline)
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseHeadlineOnly 从 AISummary.SummaryJSON 里只取 headline 字段(比 parseSummaryForTools 更
// 轻量——pre-seed 只要一句话概括,不需要 concepts/key_points 数组)。宽松解析:任何错误
// 返回 "",调用方据此标"(无总结)"。走 jsonx.ParseLLMJSON 统一兜底,容忍模型把 JSON
// 包在 prose/fences 里 + 裸引号修复。
func parseHeadlineOnly(raw string) string {
	if raw == "" {
		return ""
	}
	var s struct {
		Headline string `json:"headline"`
	}
	if _, err := jsonx.ParseLLMJSON(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s.Headline)
}
