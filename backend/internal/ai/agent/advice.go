package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// advice.go 编排 agent 驱动的学习建议生成(Phase C 的核心)。和 quizzer.go 平行:
// quizzer 跑 ReAct loop 出结构化题目;advice 跑同一个 ReAct loop,只换 system prompt +
// 工具集(NewAdviceToolbox),产出自然语言建议。
//
// 复用 agent.Run(通用 ReAct 引擎),不复制 loop 代码——advice 是"全 agent 驱动"的
// 典型:不是一次性 prompt engineering,而是 agent 自己用工具查跨课程 mastery、
// 自己决定分析深度。其他能力(course_summary / user_report)复用这套 loop,只换工具集。
//
// 和 quizzer 的差异:
//   - 输出是自然语言(agentRes.FinalText 直接用),不解析 JSON;
//   - 无 self-check(quiz 需要"审题"保证答案正确性;advice 是开放文本,无客观正误,
//     只做轻量的非空 + 长度合理性检查);
//   - pre-seed 按 scope 不同:episode 级 pre-seed episode mastery;course/subject 级
//     pre-seed 对应聚合 mastery。省一轮工具调用,agent 直接有素材开写。

// Advice scope 常量。存进 StudyAdvice.Scope 列,也用于 prompt 选择工作方式。
const (
	ScopeEpisode = "episode"
	ScopeCourse  = "course"
	ScopeSubject = "subject"
)

// AdviceRequest 是一次建议生成的输入。Scope 决定 agent 用哪档 mastery 工具
// (episode/course/subject);ScopeID 是对应实体的 id。其它字段是给 prompt 的上下文,
// 让 agent 不用每次都调工具拿标题/科目。
type AdviceRequest struct {
	UserID       uint
	Scope        string // ScopeEpisode | ScopeCourse | ScopeSubject
	ScopeID      uint   // episode_id / course_id / subject_id
	ScopeTitle   string // 课时/课程/科目标题(供 prompt 引用)
	Subject      string // 科目名(如"数学"),episode/course 级有,subject 级就是自身
	EpisodeID    uint   // episode 级必备;course/subject 级可为 0(用不到 get_episode_summary)
	CourseID     uint   // course/episode 级必备;subject 级可为 0(用不到 get_course_mastery)
	SubjectID    uint   // subject 级必备;episode/course 级可填(供 get_subject_mastery 深入)
	ExtraContext string // 额外上下文(如"刚交卷,错了 3 题"),可选
	// AdviceHint 喂 advice agent 的风格/侧重点(如"象棋重实战练习""数学重计算巩固")。
	// 来自 Course.EffectiveAdviceHint(subject) —— 课程级 AIConfig 空时回退学科级。
	// episode/course scope 时有值;subject scope 没有 course 留空。
	AdviceHint string
	// TermDict 是横切的术语纠错字典(Course.EffectiveTermDict(subject) —— 课程级+学科级合并)。
	// 注入到 user 消息的【术语字典】段,advice 输出时按此纠正字幕同音错字。
	TermDict string
}

// AdviceResult 是 Generate 的返回:建议文本 + agent trace(供 ai_runs 记录)+ usage。
type AdviceResult struct {
	AdviceText string
	// MasterySnapshot 是生成时的 mastery 快照,供调用方序列化存 StudyAdvice.
	// MasterySnapshotJSON(后续对比"上次建议后进步多少")。agent 自己不写这个,
	// 调用方(runAdviceJob)决定存哪些行的快照。
	MasterySnapshot []model.KnowledgeMemory
	Trace           []TraceStep
	Usage           ai.Usage
	Turns           int
	// SystemPrompt / UserPrompt 透传自 AgentResult:本次建议生成发给 LLM 的开场
	// system+user prompt(即 Generate 入口拼好的那对 seed)。供 service 层写进
	// ai_runs.system_prompt_text / user_prompt_text,让 admin "查看回放"能看到
	// 这次到底发了什么 prompt。
	SystemPrompt string
	UserPrompt   string
}

// AdviceAgent 用 ReAct loop 生成自然语言学习建议。持有一个带 advice 工具集的 agent。
// 没有 self-check agent(advice 是开放文本,不像 quiz 要审题)。
type AdviceAgent struct {
	agent  *Agent       // 带 NewAdviceToolbox 的 ReAct 引擎
	memory *MemoryStore // pre-seed 用:读 mastery 塞进 prompt
	deps   ToolDeps     // pre-seed 用:取 episode/course 元数据(若需要)
}

// NewAdviceAgent 构造一个 AdviceAgent。agentLoop 必须已经注入 NewAdviceToolbox。
func NewAdviceAgent(agentLoop *Agent, memory *MemoryStore, deps ToolDeps) *AdviceAgent {
	return &AdviceAgent{agent: agentLoop, memory: memory, deps: deps}
}

// Generate 跑一次 advice 生成。流程:
//  1. pre-seed:按 req.Scope 读对应 mastery(episode/course/subject),塞进 user prompt
//     省一轮工具调用 + 同时收集 masterySnapshot(存进结果,供后续对比)。
//  2. agent.Run(ctx, AdviceSystemPrompt, userPrompt) —— ReAct loop,agent 自己调工具
//     深入查询(跨课程聚合时尤其需要)。
//  3. 轻量校验:FinalText 非空且长度合理(<20 字或 >5000 字视为可疑,但仍是合法建议,
//     不阻断;调用方可据此降级)。不做 LLM self-check。
//
// 复用 quizzer 的 pre-seed 思路,但 mastery 来源按 scope 不同。
func (a *AdviceAgent) Generate(ctx context.Context, req AdviceRequest) (*AdviceResult, error) {
	masterySeed, snapshot := a.buildAdviceSeed(ctx, req)
	userPrompt := buildAdviceUserPrompt(req, masterySeed)
	res, err := a.agent.Run(ctx, AdviceSystemPrompt, userPrompt)
	if err != nil {
		// agent.Run 失败时(含 ErrMaxSteps)仍返回带 partial trace 的 result——透传给
		// service 层落 ai_runs,否则失败时模型调了什么工具全丢,无法排查(和 quizzer
		// 一致;service 层 runAdviceJob 的 if res != nil 会接住落 trace)。
		return &AdviceResult{Trace: res.Trace, Usage: res.Usage, Turns: res.Turns,
			SystemPrompt: res.SystemPrompt, UserPrompt: res.UserPrompt},
			fmt.Errorf("advice agent: %w", err)
	}
	adviceText := strings.TrimSpace(res.FinalText)
	if adviceText == "" {
		// agent 给了空答案(罕见,通常是模型异常)。返回错误让 job 标 failed,
		// 而不是存一条空 advice 误导学生。
		return nil, fmt.Errorf("advice agent: empty final answer")
	}
	return &AdviceResult{
		AdviceText:      adviceText,
		MasterySnapshot: snapshot,
		Trace:           res.Trace,
		Usage:           res.Usage,
		Turns:           res.Turns,
		// 透传 seed prompt 供 service 层落 ai_runs(诊断时能看到这次发了什么)。
		SystemPrompt: res.SystemPrompt,
		UserPrompt:   res.UserPrompt,
	}, nil
}

// buildAdviceSeed 按 scope 读 mastery,返回 (promptSeed, snapshot)。
// promptSeed 是给 prompt 的弱点摘要(省一轮工具调用);snapshot 是完整 mastery 行(供
// 调用方存 StudyAdvice.MasterySnapshotJSON)。两者读同一份数据,避免二次查询。
//
// 对每个 scope:
//   - episode: MemoryStore.Masteries(userID, episodeID)
//   - course:  MemoryStore.CourseMasteries(userID, courseID)
//   - subject: MemoryStore.SubjectMasteries(userID, subjectID)
//
// promptSeed 只取 mastery<0.8 的(已掌握的不值得在 prompt 里列);snapshot 存全量
// (供对比用,要看到进步也不能只看弱点)。
func (a *AdviceAgent) buildAdviceSeed(ctx context.Context, req AdviceRequest) (string, []model.KnowledgeMemory) {
	var rows []model.KnowledgeMemory
	var err error
	switch req.Scope {
	case ScopeEpisode:
		rows, err = a.memory.Masteries(ctx, req.UserID, req.EpisodeID)
	case ScopeCourse:
		rows, err = a.memory.CourseMasteries(ctx, req.UserID, req.CourseID)
	case ScopeSubject:
		rows, err = a.memory.SubjectMasteries(ctx, req.UserID, req.SubjectID)
	default:
		return "", nil
	}
	if err != nil || len(rows) == 0 {
		return "", rows // snapshot 也空
	}
	var b strings.Builder
	for _, r := range rows {
		if r.Mastery >= 0.8 {
			continue // 已掌握,不在 seed 里列
		}
		fmt.Fprintf(&b, "- mastery=%.2f(对%d 错%d)\n", r.Mastery, r.CorrectCount, r.WrongCount)
	}
	return strings.TrimRight(b.String(), "\n"), rows
}

// MarshalMasterySnapshot 把 mastery 行序列化成 StudyAdvice.MasterySnapshotJSON 的格式。
// 抽出来供 runAdviceJob 调用(避免 service 层重复 model.KnowledgeMemory 的字段知识)。
// 失败返回 ""(快照是 nice-to-have,不阻断 advice 生成)。
func MarshalMasterySnapshot(rows []model.KnowledgeMemory) string {
	if len(rows) == 0 {
		return ""
	}
	type snapItem struct {
		EpisodeID    uint    `json:"episode_id"`
		CourseID     uint    `json:"course_id"`
		ChunkID      uint    `json:"chunk_id"`
		Mastery      float64 `json:"mastery"`
		CorrectCount int     `json:"correct_count"`
		WrongCount   int     `json:"wrong_count"`
	}
	out := make([]snapItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, snapItem{
			EpisodeID:    r.EpisodeID,
			CourseID:     r.CourseID,
			ChunkID:      r.ChunkID,
			Mastery:      r.Mastery,
			CorrectCount: r.CorrectCount,
			WrongCount:   r.WrongCount,
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}
