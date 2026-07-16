package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// advice_tools.go 定义 advice agent 专用的工具集(NewAdviceToolbox)。和 quiz 工具集
// (tools.go 的 NewQuizToolbox)共享同一个 ReAct loop 和 Toolbox 结构,但工具语义不同:
//
//   - quiz agent 关心"这节课考什么"(检索字幕、锚定 chunk 出题);
//   - advice agent 关心"这个学生哪里薄弱"(跨课程查 mastery + chunk.text,用人话给
//     弱点分析 + 复习建议)。
//
// 关键设计:advice 工具返回 mastery 时同时带上 chunk.text 片段。理由——没有 chunk.text,
// agent 只看到"chunk#37 mastery=0.2",没法写出"通分掌握不好"这样的建议。chunk.text 是
// 唯一能推断知识点名的线索(目前 chunk 无独立的知识点名字段,Phase D 再加)。observation
// 里截断 chunk.text 到 ~120 字,够 agent 理解又不会撑爆 context。
//
// 工具集内每个工具仍受 Toolbox 的 (episodeID, userID, courseID) 三元组 scope 约束(服务端
// 强制),model 无法越权查别人的数据 —— 和 quiz 工具集同一套安全模型。

// NewAdviceToolbox 构建 advice agent 的工具集。和 NewQuizToolbox 平行,但不带 embedder
// (advice 不做向量检索,只读 mastery + 元数据)。episodeID/userID/courseID scope 每个工具:
// episode 级工具按 episodeID;course/subject 级工具按 courseID(以及从 courseID 反查 subject)。
//
// scope 的处理:advice 的三档(episode/course/subject)由调用方(runAdviceJob)决定用哪
// 些工具的 pre-seed,但工具集本身把所有工具都注册上,agent 自己按需调。subject 级工具
// (get_subject_mastery)需要 subject_id,由 toolbox 构造时传入(subjectID,0 表示未知→
// 工具回退"无法查询")。
func NewAdviceToolbox(deps ToolDeps, memory *MemoryStore, userID, courseID, episodeID, subjectID uint) *Toolbox {
	tb := &Toolbox{
		deps:      deps,
		memory:    memory,
		episodeID: episodeID,
		userID:    userID,
		courseID:  courseID,
		funcs:     map[string]toolFunc{},
	}
	// subjectID 存到 toolbox 供 subject 级工具读取。Toolbox 没有 subjectID 字段,用一个
	// 闭包捕获即可(下面每个工具是闭包,能读到 subjectID)。
	registerAdviceTools(tb, deps, memory, subjectID)
	return tb
}

// registerAdviceTools 注册 5 个 advice 工具。拆出来便于测试(可以单独验证注册结果)。
func registerAdviceTools(tb *Toolbox, deps ToolDeps, memory *MemoryStore, subjectID uint) {
	tb.register("get_user_mastery", adviceUserMasterySpec, func(ctx context.Context, args string) (string, error) {
		return tb.runAdviceGetUserMastery(ctx)
	})
	tb.register("get_course_mastery", adviceCourseMasterySpec, func(ctx context.Context, args string) (string, error) {
		return tb.runAdviceGetCourseMastery(ctx)
	})
	tb.register("get_subject_mastery", adviceSubjectMasterySpec, func(ctx context.Context, args string) (string, error) {
		return tb.runAdviceGetSubjectMastery(ctx, subjectID)
	})
	tb.register("list_user_courses", adviceListUserCoursesSpec, func(ctx context.Context, args string) (string, error) {
		return tb.runAdviceListUserCourses(ctx)
	})
	tb.register("get_episode_summary", adviceEpisodeSummarySpec, func(ctx context.Context, args string) (string, error) {
		return tb.runAdviceGetEpisodeSummary(ctx)
	})
}

// ── mastery 行 → 文本 observation 的公共格式化 ──
//
// 把 []model.KnowledgeMemory 渲染成给 agent 看的文本。核心:每条都带 chunk.text 片段
// (从 chunkID → ContentChunk.Text join),让 agent 能用人话描述知识点("通分掌握不好"
// 而非"chunk#37")。chunk 的 join 由调用方通过 chunksByID map 提供(advice 工具自己
// 先 ListChunks 建 map),避免这里依赖具体的 episode。
//
// mastery 行可能跨多个 episode(course/subject 级),所以 chunksByID 是 chunkID → chunk
// 的全局 map,调用方负责把相关 episode 的 chunks 都塞进来。找不到 chunk 的行(被删除
// 等)显示"(无字幕片段)"——不阻塞 agent,它仍能看到 mastery 数值。
func formatMasteriesWithText(rows []model.KnowledgeMemory, chunksByID map[uint]model.ContentChunk, header string) string {
	if len(rows) == 0 {
		return header + "暂无答题记录(新学生)。建议基于课程内容给出通用的学习建议。"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s(弱点优先,mastery 越低越需要加强):\n\n", header)
	for _, r := range rows {
		verdict := "已掌握"
		if r.Mastery < 0.4 {
			verdict = "★弱点"
		} else if r.Mastery < 0.7 {
			verdict = "一般"
		}
		chunkText := "(无字幕片段)"
		if ch, ok := chunksByID[r.ChunkID]; ok && ch.Text != "" {
			chunkText = truncate(strings.TrimSpace(ch.Text), 120)
		}
		fmt.Fprintf(&b,
			"- mastery=%.2f (%s) | 对%d 错%d | 最近复习:%s\n  知识点线索: %s\n",
			r.Mastery, verdict, r.CorrectCount, r.WrongCount,
			formatReviewTime(r.LastReviewed), chunkText,
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

// loadChunksForMasteries 把 mastery 行涉及的 episode 的 chunks 一次性加载成 chunkID→chunk
// map。收集 mastery 行里的 episode_id(去重),逐个 ListChunks(每个 episode 一次查询,
// episode 数通常几个到十几个,可接受;真要优化可以加一个 IN 查询的 repo 方法,但当前
// 简单为先)。返回 nil map 表示无 chunk 可用(工具会回退到"(无字幕片段)")。
func (t *Toolbox) loadChunksForMasteries(rows []model.KnowledgeMemory) map[uint]model.ContentChunk {
	if len(rows) == 0 {
		return nil
	}
	seen := make(map[uint]bool, len(rows))
	out := make(map[uint]model.ContentChunk, len(rows))
	for _, r := range rows {
		seen[r.EpisodeID] = true
	}
	for epID := range seen {
		chunks, err := t.deps.ListChunks(epID)
		if err != nil {
			continue // 单个 episode 失败不阻塞整个 join
		}
		for _, c := range chunks {
			out[c.ID] = c
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Tool: get_user_mastery (advice 增强版,带 chunk.text)
// ---------------------------------------------------------------------------

var adviceUserMasterySpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_user_mastery",
		Description: "查询这个学生在这节课各知识点的掌握度(mastery 0-1)、对错次数、最近复习时间,并附上每个知识点对应的字幕片段文本(帮你用人话描述弱点,如'通分掌握不好'而非'chunk#37')。弱点(mastery<0.4)会标★。新学生无记录时返回空。",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// runAdviceGetUserMastery 返回 episode 级 mastery + chunk.text。和 quiz 的同名工具区别:
// observation 里多了 chunk 文本片段。agent 据此把"chunk#37 mastery=0.2"翻译成"通分掌握不好"。
func (t *Toolbox) runAdviceGetUserMastery(ctx context.Context) (string, error) {
	rows, err := t.memory.Masteries(ctx, t.userID, t.episodeID)
	if err != nil {
		return "", fmt.Errorf("advice get_user_mastery: %w", err)
	}
	chunksByID := t.loadChunksForMasteries(rows)
	return formatMasteriesWithText(rows, chunksByID, "该学生本课时的掌握度"), nil
}

// ---------------------------------------------------------------------------
// Tool: get_course_mastery
// ---------------------------------------------------------------------------

var adviceCourseMasterySpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_course_mastery",
		Description: "跨课时聚合:查询这个学生在整门课程下所有课时的掌握度(弱点优先),每条带知识点字幕片段文本。用于'这门课你哪里薄弱'的整体分析。比 get_user_mastery 视野更广(跨多节课)。",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// runAdviceGetCourseMastery 跨课程聚合,调 MemoryStore.CourseMasteries(mastery ASC 排序)
// 再 join chunk.text。
func (t *Toolbox) runAdviceGetCourseMastery(ctx context.Context) (string, error) {
	rows, err := t.memory.CourseMasteries(ctx, t.userID, t.courseID)
	if err != nil {
		return "", fmt.Errorf("advice get_course_mastery: %w", err)
	}
	chunksByID := t.loadChunksForMasteries(rows)
	return formatMasteriesWithText(rows, chunksByID, fmt.Sprintf("该学生在课程#%d 的掌握度(跨所有课时)", t.courseID)), nil
}

// ---------------------------------------------------------------------------
// Tool: get_subject_mastery
// ---------------------------------------------------------------------------

var adviceSubjectMasterySpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_subject_mastery",
		Description: "科目级聚合:查询这个学生在整个科目(可能跨多门课程)下的掌握度(弱点优先),每条带知识点字幕片段文本。用于'整个数学/英语科目你哪里薄弱'的宏观分析。需要 toolbox 构造时传入 subject_id;若未提供返回提示。",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// runAdviceGetSubjectMastery 科目级聚合。subjectID 由 toolbox 构造时传入(从 courseID →
// course.subject_id 反查);0 表示未知(例如 episode 级 advice 不关心 subject),工具
// 回退提示 agent 用 course 级工具。
func (t *Toolbox) runAdviceGetSubjectMastery(ctx context.Context, subjectID uint) (string, error) {
	if subjectID == 0 {
		return "无法查询科目级掌握度(toolbox 未提供 subject_id)。请改用 get_course_mastery 做课程级分析。", nil
	}
	rows, err := t.memory.SubjectMasteries(ctx, t.userID, subjectID)
	if err != nil {
		return "", fmt.Errorf("advice get_subject_mastery: %w", err)
	}
	chunksByID := t.loadChunksForMasteries(rows)
	return formatMasteriesWithText(rows, chunksByID, fmt.Sprintf("该学生在科目#%d 的掌握度(跨所有课程)", subjectID)), nil
}

// ---------------------------------------------------------------------------
// Tool: list_user_courses
// ---------------------------------------------------------------------------

var adviceListUserCoursesSpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "list_user_courses",
		Description: "列出这个学生当前被授权学习的所有课程 id。用于'建议从哪门课开始复习'或'你最近在学哪几门课'。返回的是 course id 数组(可能为空)。",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// runAdviceListUserCourses 调 deps.ListUserCourses(走 userRepo.GetAccessList)。返回
// 纯 id 数组——不附带课程名,因为 ToolDeps 不该为了一个工具把 courseRepo 的查询拉进来;
// agent 若需要课程名可以从它已有的上下文里推(或后续加一个 course 元数据工具)。简单为先。
func (t *Toolbox) runAdviceListUserCourses(ctx context.Context) (string, error) {
	_ = ctx
	ids, err := t.deps.ListUserCourses(t.userID)
	if err != nil {
		return "", fmt.Errorf("advice list_user_courses: %w", err)
	}
	if len(ids) == 0 {
		return "该学生当前没有被授权任何课程。", nil
	}
	// 排序仅为了让 observation 稳定(便于测试断言 + agent 看着整齐)。
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	return fmt.Sprintf("该学生被授权的课程 id: %s", strings.Join(parts, ", ")), nil
}

// ---------------------------------------------------------------------------
// Tool: get_episode_summary
// ---------------------------------------------------------------------------

var adviceEpisodeSummarySpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_episode_summary",
		Description: "读取这节课已生成的 AI 总结(核心概念、要点、概括),帮你引用知识点名(如'通分''公分母')写进建议。如果没有总结返回提示。和 get_user_mastery 配合:总结告诉你知识点叫什么,mastery 告诉你学生哪里弱。",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

// runAdviceGetEpisodeSummary 复用 deps.GetSummary,把 summary JSON 解析成 concepts/key_points
// 给 agent。和 quiz 工具集的 get_episode_info 共享 parseSummaryForTools(那里已实现 JSON
// 解析);这里只取 concepts + key_points + headline,不带 episode 元数据(advice 的 episode
// 元数据已经在 pre-seed prompt 里给过了,无需重复)。
func (t *Toolbox) runAdviceGetEpisodeSummary(ctx context.Context) (string, error) {
	_ = ctx
	sum, err := t.deps.GetSummary(t.episodeID)
	if err != nil {
		return "", fmt.Errorf("advice get_episode_summary: %w", err)
	}
	if sum == nil {
		return "这节课还没有 AI 总结。可以基于 mastery 数据和 chunk 文本片段给建议。", nil
	}
	parsed, perr := parseSummaryForTools(sum.SummaryJSON)
	if perr != nil {
		return "总结解析失败,请直接基于 mastery 数据给建议。", nil
	}
	var b strings.Builder
	if parsed.Headline != "" {
		fmt.Fprintf(&b, "概括: %s\n", parsed.Headline)
	}
	if len(parsed.Concepts) > 0 {
		fmt.Fprintf(&b, "核心概念: %s\n", strings.Join(parsed.Concepts, "、"))
	}
	if len(parsed.KeyPoints) > 0 {
		b.WriteString("要点:\n")
		for _, kp := range parsed.KeyPoints {
			fmt.Fprintf(&b, "  · %s\n", kp)
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return "总结为空。", nil
	}
	return out, nil
}
