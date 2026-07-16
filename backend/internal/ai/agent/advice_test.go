package agent

import (
	"context"
	"strings"
	"testing"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// advice_tools_test.go + advice_test.go: advice agent 的单元测试。覆盖:
//   - NewAdviceToolbox 注册的 5 个工具(Names/Specs);
//   - get_user_mastery/advice 工具返回带 chunk.text 的 observation;
//   - AdviceAgent.Generate 端到端(用 mockLLM 脚本化,验证 pre-seed + FinalText 直传)。
//
// 复用 agent_test.go 的 mockLLM、toolCall 辅助;tools_test.go 的 fakeToolDeps/
// fakeMemoryRepo(已扩展 advice 字段)。

// TestAdviceToolboxRegistersAllTools 验证 NewAdviceToolbox 注册了 5 个工具,名字正确。
// 工具集是 advice agent 的能力边界——少注册一个,agent 就缺一类查询能力。
func TestAdviceToolboxRegistersAllTools(t *testing.T) {
	tb := NewAdviceToolbox(&fakeToolDeps{}, NewMemoryStore(&fakeMemoryRepo{}), 1, 10, 100, 7)
	want := map[string]bool{
		"get_user_mastery":   false,
		"get_course_mastery": false,
		"get_subject_mastery": false,
		"list_user_courses":  false,
		"get_episode_summary": false,
	}
	for _, n := range tb.Names() {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("advice toolbox missing tool %q; registered: %v", name, tb.Names())
		}
	}
	if len(tb.Specs()) != len(want) {
		t.Errorf("advice toolbox specs count = %d; want %d", len(tb.Specs()), len(want))
	}
}

// TestAdviceGetUserMasteryIncludesChunkText 验证 advice 版的 get_user_mastery
// 在 observation 里带上了 chunk.text 片段(这是 advice agent 能"用人话描述知识点"的
// 关键)。fakeToolDeps 提供了 chunks,memory 提供弱点行,工具应把 chunk 文本拼进去。
func TestAdviceGetUserMasteryIncludesChunkText(t *testing.T) {
	deps := &fakeToolDeps{
		chunks: []model.ContentChunk{
			{ID: 1001, EpisodeID: 100, ChunkIndex: 0, Text: "通分就是把两个分数化成同分母的分数"},
		},
	}
	mem := NewMemoryStore(&fakeMemoryRepo{
		rows: []model.KnowledgeMemory{
			{UserID: 1, EpisodeID: 100, CourseID: 10, ChunkID: 1001, Mastery: 0.2, CorrectCount: 1, WrongCount: 4},
		},
	})
	tb := NewAdviceToolbox(deps, mem, 1, 10, 100, 7)

	out, err := tb.Execute(context.Background(), "get_user_mastery", "{}")
	if err != nil {
		t.Fatalf("get_user_mastery: %v", err)
	}
	// 关键断言:observation 里出现了 chunk 文本("通分"),不是只显示 chunk#1001。
	if !strings.Contains(out, "通分") {
		t.Errorf("observation missing chunk text; got:\n%s", out)
	}
	if !strings.Contains(out, "0.20") {
		t.Errorf("observation missing mastery value; got:\n%s", out)
	}
	if !strings.Contains(out, "★弱点") {
		t.Errorf("mastery 0.2 should be flagged as ★弱点; got:\n%s", out)
	}
}

// TestAdviceGetUserMasteryEmptyForNewStudent 新学生无 mastery 行时,工具返回降级提示
// (不是报错)。agent 据此知道该学生还没数据,走"基于课程内容给通用建议"的路径。
func TestAdviceGetUserMasteryEmptyForNewStudent(t *testing.T) {
	tb := NewAdviceToolbox(&fakeToolDeps{}, NewMemoryStore(&fakeMemoryRepo{}), 1, 10, 100, 7)
	out, err := tb.Execute(context.Background(), "get_user_mastery", "{}")
	if err != nil {
		t.Fatalf("get_user_mastery: %v", err)
	}
	if !strings.Contains(out, "暂无答题记录") {
		t.Errorf("empty mastery should say 暂无答题记录; got:\n%s", out)
	}
}

// TestAdviceGetCourseMastery 跨课程聚合:memory 返回多条,工具都格式化(每条带 chunk 文本)。
func TestAdviceGetCourseMastery(t *testing.T) {
	deps := &fakeToolDeps{
		chunks: []model.ContentChunk{
			{ID: 1, EpisodeID: 10, Text: "知识点A"},
			{ID: 2, EpisodeID: 11, Text: "知识点B"},
		},
	}
	mem := NewMemoryStore(&fakeMemoryRepo{
		courseRows: []model.KnowledgeMemory{
			{UserID: 1, EpisodeID: 10, CourseID: 100, ChunkID: 1, Mastery: 0.1},
			{UserID: 1, EpisodeID: 11, CourseID: 100, ChunkID: 2, Mastery: 0.9},
		},
	})
	tb := NewAdviceToolbox(deps, mem, 1, 100, 10, 7)
	out, err := tb.Execute(context.Background(), "get_course_mastery", "{}")
	if err != nil {
		t.Fatalf("get_course_mastery: %v", err)
	}
	if !strings.Contains(out, "知识点A") || !strings.Contains(out, "知识点B") {
		t.Errorf("course mastery missing chunk texts; got:\n%s", out)
	}
	// mastery ASC 排序:0.1 应排在 0.9 前(弱点优先)。
	idxA := strings.Index(out, "知识点A")
	idxB := strings.Index(out, "知识点B")
	if idxA < 0 || idxB < 0 || idxA > idxB {
		t.Errorf("expected mastery ASC (A=0.1 before B=0.9); positions A=%d B=%d", idxA, idxB)
	}
}

// TestAdviceGetSubjectMasteryMissingSubjectID subjectID=0 时回退提示 agent 用 course 工具。
// 这是 episode 级 advice 的常见场景(不关心 subject)。
func TestAdviceGetSubjectMasteryMissingSubjectID(t *testing.T) {
	tb := NewAdviceToolbox(&fakeToolDeps{}, NewMemoryStore(&fakeMemoryRepo{}), 1, 10, 100, 0)
	out, err := tb.Execute(context.Background(), "get_subject_mastery", "{}")
	if err != nil {
		t.Fatalf("get_subject_mastery: %v", err)
	}
	if !strings.Contains(out, "改用 get_course_mastery") {
		t.Errorf("subject_id=0 should hint to use course mastery; got:\n%s", out)
	}
}

// TestAdviceListUserCourses 返回学生课程 id(逗号分隔 + 升序)。
func TestAdviceListUserCourses(t *testing.T) {
	deps := &fakeToolDeps{userCourses: []uint{3, 1, 2}}
	tb := NewAdviceToolbox(deps, NewMemoryStore(&fakeMemoryRepo{}), 1, 10, 100, 7)
	out, err := tb.Execute(context.Background(), "list_user_courses", "{}")
	if err != nil {
		t.Fatalf("list_user_courses: %v", err)
	}
	// 升序:1, 2, 3。
	if !strings.Contains(out, "1, 2, 3") {
		t.Errorf("list_user_courses should be sorted ascending; got:\n%s", out)
	}
}

// TestAdviceGetEpisodeSummary 工具把 summary JSON 解析成 concepts/key_points。
// 无 summary 时降级提示 agent 基于 mastery 给建议。
func TestAdviceGetEpisodeSummary(t *testing.T) {
	t.Run("with summary", func(t *testing.T) {
		deps := &fakeToolDeps{
			summary: &model.AISummary{
				SummaryJSON: `{"headline":"讲通分","concepts":["通分","公分母"],"key_points":["找最小公倍数"]}`,
			},
		}
		tb := NewAdviceToolbox(deps, NewMemoryStore(&fakeMemoryRepo{}), 1, 10, 100, 7)
		out, err := tb.Execute(context.Background(), "get_episode_summary", "{}")
		if err != nil {
			t.Fatalf("get_episode_summary: %v", err)
		}
		if !strings.Contains(out, "通分") || !strings.Contains(out, "公分母") {
			t.Errorf("summary tool missing concepts; got:\n%s", out)
		}
	})
	t.Run("no summary", func(t *testing.T) {
		tb := NewAdviceToolbox(&fakeToolDeps{}, NewMemoryStore(&fakeMemoryRepo{}), 1, 10, 100, 7)
		out, err := tb.Execute(context.Background(), "get_episode_summary", "{}")
		if err != nil {
			t.Fatalf("get_episode_summary: %v", err)
		}
		if !strings.Contains(out, "还没有 AI 总结") {
			t.Errorf("missing-summary case should hint; got:\n%s", out)
		}
	})
}

// ── AdviceAgent.Generate 端到端(mock LLM)──

// TestAdviceAgentGenerateFinalTextDirect 验证 agent 的 FinalText(自然语言)直接作为
// AdviceText 返回(不解析 JSON)。mockLLM 立即给最终答案(不调工具),adviser 应返回该文本。
// 同时验证 pre-seed 的 mastery 摘要进了 prompt(mockLLM 没法直接验证 prompt,但 pre-seed
// 不报错即可——mastery 行由 fakeMemoryRepo 提供)。
func TestAdviceAgentGenerateFinalTextDirect(t *testing.T) {
	deps := &fakeToolDeps{
		chunks: []model.ContentChunk{{ID: 1, EpisodeID: 10, Text: "通分相关"}},
	}
	mem := NewMemoryStore(&fakeMemoryRepo{
		rows: []model.KnowledgeMemory{
			{UserID: 1, EpisodeID: 10, CourseID: 100, ChunkID: 1, Mastery: 0.2},
		},
	})
	// mockLLM:第一轮就给最终答案(自然语言建议,非 JSON)。
	llm := &mockLLM{responses: []*ai.ChatResponse{
		{Content: "你在这节课的通分上还有提升空间,建议回到视频第3段重新看一遍。", FinishReason: "stop"},
	}}
	// advice agent 不需要 embedder,用 nil toolbox-specs(但 NewAdviceToolbox 注册了工具,
	// agent 会把 specs 发给模型——模型这里选择不调用,直接 stop)。为了测试纯 FinalText 路径,
	// 直接用一个空 toolbox 的 agent(不注册工具),验证 Generate 把 FinalText 透传。
	emptyTB := &Toolbox{funcs: map[string]toolFunc{}}
	gen := NewAgent(llm, "mock-model", emptyTB, AgentOpts{MaxSteps: 3, MaxTokens: 500})
	adviser := NewAdviceAgent(gen, mem, deps)

	res, err := adviser.Generate(context.Background(), AdviceRequest{
		UserID: 1, Scope: ScopeEpisode, ScopeID: 10, EpisodeID: 10, CourseID: 100,
		ScopeTitle: "测试课时",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.AdviceText, "通分") {
		t.Errorf("AdviceText should pass through FinalText; got %q", res.AdviceText)
	}
	// snapshot 应包含 pre-seed 读到的 mastery 行(全量,含已掌握的——这里只有一条 0.2)。
	if len(res.MasterySnapshot) != 1 {
		t.Errorf("MasterySnapshot len = %d; want 1", len(res.MasterySnapshot))
	}
}

// TestAdviceAgentGenerateEmptyFinalTextErrors agent 给空 FinalText 时 Generate 应返回错误
// (而不是存一条空 advice 误导学生)。这是 job 标 failed 的触发条件。
func TestAdviceAgentGenerateEmptyFinalTextErrors(t *testing.T) {
	deps := &fakeToolDeps{}
	mem := NewMemoryStore(&fakeMemoryRepo{})
	llm := &mockLLM{responses: []*ai.ChatResponse{
		{Content: "", FinishReason: "stop"},
	}}
	emptyTB := &Toolbox{funcs: map[string]toolFunc{}}
	gen := NewAgent(llm, "mock-model", emptyTB, AgentOpts{MaxSteps: 3, MaxTokens: 500})
	adviser := NewAdviceAgent(gen, mem, deps)

	_, err := adviser.Generate(context.Background(), AdviceRequest{
		UserID: 1, Scope: ScopeEpisode, ScopeID: 10, EpisodeID: 10, CourseID: 100,
	})
	if err == nil {
		t.Fatal("Generate with empty FinalText should error")
	}
	if !strings.Contains(err.Error(), "empty final answer") {
		t.Errorf("error should mention empty final answer; got %v", err)
	}
}

// TestAdviceAgentGenerateCallsToolThenAnswers 验证 advice agent 也能走完整的 ReAct loop:
// 调一次工具(get_user_mastery)拿到 observation,再给最终答案。证明复用的 ReAct loop 在
// advice 场景下也工作(不是只能单次调用)。
func TestAdviceAgentGenerateCallsToolThenAnswers(t *testing.T) {
	deps := &fakeToolDeps{
		chunks: []model.ContentChunk{{ID: 1, EpisodeID: 10, Text: "通分知识点讲解"}},
	}
	mem := NewMemoryStore(&fakeMemoryRepo{
		rows: []model.KnowledgeMemory{
			{UserID: 1, EpisodeID: 10, CourseID: 100, ChunkID: 1, Mastery: 0.2},
		},
	})
	// 用真实的 advice toolbox(注册了工具),让模型能调 get_user_mastery。
	tb := NewAdviceToolbox(deps, mem, 1, 100, 10, 7)
	llm := &mockLLM{responses: []*ai.ChatResponse{
		// 第一轮:模型要求调 get_user_mastery。
		{
			FinishReason: "tool_calls",
			ToolCalls: []ai.ToolCall{toolCall("call-1", "get_user_mastery", "{}")},
		},
		// 第二轮:模型拿到 observation 后给最终建议。
		{Content: "通分是你的弱点,建议多练习。", FinishReason: "stop"},
	}}
	gen := NewAgent(llm, "mock-model", tb, AgentOpts{MaxSteps: 5, MaxTokens: 500})
	adviser := NewAdviceAgent(gen, mem, deps)

	res, err := adviser.Generate(context.Background(), AdviceRequest{
		UserID: 1, Scope: ScopeEpisode, ScopeID: 10, EpisodeID: 10, CourseID: 100,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.AdviceText, "通分") {
		t.Errorf("AdviceText should reflect mastery observation; got %q", res.AdviceText)
	}
	if len(res.Trace) < 2 {
		t.Errorf("trace should have >=2 steps (tool call + final); got %d", len(res.Trace))
	}
	if llm.calls != 2 {
		t.Errorf("LLM should be called twice (tool + answer); got %d calls", llm.calls)
	}
}

// TestMarshalMasterySnapshot 验证快照序列化成 JSON 数组(给 StudyAdvice.MasterySnapshotJSON)。
// 失败返回 ""(快照是 nice-to-have,不阻断)。
func TestMarshalMasterySnapshot(t *testing.T) {
	rows := []model.KnowledgeMemory{
		{EpisodeID: 10, CourseID: 100, ChunkID: 1, Mastery: 0.2, CorrectCount: 1, WrongCount: 4},
		{EpisodeID: 11, CourseID: 100, ChunkID: 2, Mastery: 0.9, CorrectCount: 5, WrongCount: 1},
	}
	out := MarshalMasterySnapshot(rows)
	if !strings.Contains(out, `"mastery":0.2`) {
		t.Errorf("snapshot missing mastery value; got %s", out)
	}
	if !strings.Contains(out, `"chunk_id":1`) {
		t.Errorf("snapshot missing chunk_id; got %s", out)
	}
	if MarshalMasterySnapshot(nil) != "" {
		t.Errorf("nil snapshot should marshal to empty string")
	}
}
