package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"studyquest/backend/internal/ai"
)

// user_study.go 编排 agent 驱动的 admin 用户学习报告(Phase E 的核心)。和 advice.go
// 平行:advice 给学生本人写复习建议(episode/course/subject 单点);user_study 给 admin
// (老师/家长)写这个学生跨所有课程的画像报告。
//
// 复用 agent.Run(通用 ReAct 引擎)——和 advice 一样不复制 loop 代码。差异:
//   - 工具集 NewUserStudyToolbox:工具按参数 course_id 查任意课程(advice 绑定单课 scope),
//     让 agent 能跨课程遍历;
//   - pre-seed 聚合"该学生所有课程的掌握度概要"(每课程平均 mastery + 最弱知识点),
//     不是单 scope 的 mastery;
//   - MaxSteps 10(跨课程数据量大,agent 可能要多次调 get_course_mastery);
//   - 输出自然语言报告(用 FinalText),综合整体掌握度/强弱项/跨课程关联/重点建议。

// UserStudyCourse 是 pre-seed 用的"该学生一门课程"的概要:课程 id、标题(若已知)、平均
// mastery、最弱知识点(按 mastery 最低取一条 + chunk 文本线索)。UserStudyAgent.buildSeed
// 遍历用户所有课程填这个切片,塞进 prompt,让 agent 不用第一轮调工具也能开写。
type UserStudyCourse struct {
	CourseID      uint
	Title         string // 课程标题,若 caller 已知则填(更友好);未知留空
	AverageMastery float64
	// WeakestPoint 是该课程 mastery 最低知识点的摘要文本(含 chunk.text 线索),
	// 让 prompt 直接能说"X 课程最弱的是通分"。空表示该课程无答题记录(新课)。
	WeakestPoint string
}

// UserStudyRequest 是一次用户学习报告生成的输入。UserID 是报告对象;UserNickname 给
// prompt 称呼该学生(报告里"小明同学"比"#42"友好);Courses 是该学生所有课程的
// pre-seed 概要(由 runUserReportJob 预先填好,见 ai_service_user_report.go)。
type UserStudyRequest struct {
	UserID      uint
	UserNickname string
	Courses     []UserStudyCourse
}

// UserStudyResult 是 Generate 的返回:报告文本 + agent trace(供 ai_runs 记录)+ usage。
// 和 AdviceResult 形状一致(advice 也存 trace + usage),但不含 mastery snapshot——
// 报告是跨课程聚合,snapshot 意义不大,pre-seed 的 Courses 切片已经在 prompt 里了。
type UserStudyResult struct {
	ReportText string
	Trace      []TraceStep
	Usage      ai.Usage
	Turns      int
}

// UserStudyAgent 用 ReAct loop 生成 admin 用户学习报告。持有一个带 user_study 工具集的
// agent + memory(读跨课程 mastery 做 pre-seed)。和 AdviceAgent 一样无 self-check
// (开放文本报告,无客观正误)。
type UserStudyAgent struct {
	agent  *Agent        // 带 NewUserStudyToolbox 的 ReAct 引擎
	memory *MemoryStore  // buildUserStudySeed 用(若需现场读 mastery)
	deps   ToolDeps      // buildUserStudySeed 用(若需反查课程标题)
}

// NewUserStudyAgent 构造一个 UserStudyAgent。agentLoop 必须已经注入
// NewUserStudyToolbox。memory/deps 主要给 buildUserStudySeed 用;若 caller(runUserReportJob)
// 已在 service 层预算好 Courses 概要并塞进 req,这里可以不依赖 buildUserStudySeed。
func NewUserStudyAgent(agentLoop *Agent, memory *MemoryStore, deps ToolDeps) *UserStudyAgent {
	return &UserStudyAgent{agent: agentLoop, memory: memory, deps: deps}
}

// Generate 跑一次用户学习报告生成。流程(仿 AdviceAgent.Generate):
//  1. pre-seed:把 req.Courses 概要(每课程平均 mastery + 最弱知识点)渲染成文本塞进 prompt,
//     省第一轮 list_user_courses + get_course_mastery 调用。
//  2. agent.Run(ctx, UserStudySystemPrompt, userPrompt) —— ReAct loop,agent 自己调工具
//     深入查某门课细节 / 已有 advice。
//  3. 轻量校验:FinalText 非空(空则报错,让 job 标 failed,不存空报告)。
func (a *UserStudyAgent) Generate(ctx context.Context, req UserStudyRequest) (*UserStudyResult, error) {
	masterySeed := buildUserStudySeed(req)
	userPrompt := buildUserStudyUserPrompt(req, masterySeed)
	res, err := a.agent.Run(ctx, UserStudySystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("user_study agent: %w", err)
	}
	reportText := strings.TrimSpace(res.FinalText)
	if reportText == "" {
		// 空答案(模型异常)→ 返回错误让 job 标 failed,不存空报告误导 admin。
		return nil, fmt.Errorf("user_study agent: empty final answer")
	}
	return &UserStudyResult{
		ReportText: reportText,
		Trace:      res.Trace,
		Usage:      res.Usage,
		Turns:      res.Turns,
	}, nil
}

// buildUserStudySeed 把 req.Courses 概要渲染成给 prompt 的文本。按平均 mastery 升序排列
// (弱项课程在前,让 agent 优先关注需要加强的课程)。每课程输出:标题/id、平均 mastery、
// 最弱知识点(mastery 数值 + chunk 线索)。无答题记录的课程标"(无答题记录)"。
//
// 注意:这里不重新查 DB——req.Courses 是 service 层(runUserReportJob)预算好的,service
// 层已经有 CourseMasteries 的聚合结果,避免在 agent 层重复查询。UserStudyAgent 的
// memory/deps 字段是为将来"现场补查"预留的,当前 buildUserStudySeed 只用 req。
func buildUserStudySeed(req UserStudyRequest) string {
	if len(req.Courses) == 0 {
		return ""
	}
	// 复制一份避免改原切片(排序按平均 mastery 升序:弱项在前)。
	sorted := make([]UserStudyCourse, len(req.Courses))
	copy(sorted, req.Courses)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].AverageMastery < sorted[j].AverageMastery
	})
	var b strings.Builder
	for _, c := range sorted {
		label := c.Title
		if label == "" {
			label = fmt.Sprintf("课程#%d", c.CourseID)
		}
		if c.WeakestPoint == "" {
			fmt.Fprintf(&b, "- %s:平均 mastery=%.2f(无答题记录,新课)\n", label, c.AverageMastery)
			continue
		}
		fmt.Fprintf(&b, "- %s:平均 mastery=%.2f | 最弱:%s\n", label, c.AverageMastery, c.WeakestPoint)
	}
	return strings.TrimRight(b.String(), "\n")
}
