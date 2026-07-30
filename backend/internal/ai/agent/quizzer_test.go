package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

func TestParseQuizGeneration(t *testing.T) {
	// A well-formed generation: 1 choice + 1 fill + feedback.
	raw := `{
		"questions": [
			{"type":"choice","chunk_index":3,"stem":"3+5=?","options":["6","7","8","9"],"answer":2,"explanation":"3+5=8"},
			{"type":"fill","chunk_index":5,"stem":"1/2+1/3=___","answer_text":["5/6","六分之五"],"explanation":"通分"}
		],
		"student_feedback":"计算基础扎实,通分需巩固"
	}`
	draft, err := parseQuizGeneration(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(draft.Questions))
	}
	if draft.Questions[0].Type != "choice" || draft.Questions[1].Type != "fill" {
		t.Errorf("types wrong: %+v", draft.Questions)
	}
	if draft.AgentFeedback != "计算基础扎实,通分需巩固" {
		t.Errorf("feedback wrong: %q", draft.AgentFeedback)
	}
}

func TestParseQuizGenerationDropsMalformed(t *testing.T) {
	// Choice with out-of-range answer, fill with no answer_text, empty stem — all dropped.
	raw := `{"questions":[
		{"type":"choice","stem":"ok","options":["a","b"],"answer":5},
		{"type":"fill","stem":"empty answers","answer_text":[]},
		{"stem":"   "},
		{"type":"choice","stem":"good","options":["a","b","c","d"],"answer":1}
	]}`
	draft, err := parseQuizGeneration(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Questions) != 1 {
		t.Errorf("expected only the 1 valid question to survive, got %d: %+v", len(draft.Questions), draft.Questions)
	}
}

func TestParseQuizGenerationDefaultsTypeToChoice(t *testing.T) {
	raw := `{"questions":[{"stem":"x","options":["a","b"],"answer":0}]}`
	draft, err := parseQuizGeneration(raw)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Questions[0].Type != "choice" {
		t.Errorf("expected default type choice, got %q", draft.Questions[0].Type)
	}
}

func TestParseQuizGenerationRejectsAllEmpty(t *testing.T) {
	raw := `{"questions":[]}`
	if _, err := parseQuizGeneration(raw); err == nil {
		t.Error("expected error for empty questions")
	}
}

// TestParseQuizGenerationRecoversBareQuotes 防回归:LLM 在 string value(题干/解析)里
// 写未转义裸 ASCII 双引号(引语)时,parseQuizGeneration 走的 jsonx.ParseLLMJSON 应靠
// repair(裸引号→中文「」成对替换)救回,而非整批 parse 失败。这是 quiz 接入统一
// chokepoint 的核心保障——之前 quiz 只用 extractJSONObject 无 repair,是最严重遗漏。
func TestParseQuizGenerationRecoversBareQuotes(t *testing.T) {
	// explanation 里含成对裸引号(典型:解析里引用术语)。第一次 unmarshal 会失败
	// (parser 在第一个裸 " 误判字符串结束),repair 把 "楚河"汉界" 替换成「楚河」「汉界」后救回。
	raw := `{
		"questions": [
			{"type":"choice","stem":"象棋棋盘中央是?","options":["楚河","黄河","长江","鸿沟"],"answer":0,"explanation":"棋盘中央以"楚河""汉界"分隔双方"}
		]
	}`
	draft, err := parseQuizGeneration(raw)
	if err != nil {
		t.Fatalf("应靠 repair 救回裸引号,却失败: %v", err)
	}
	if len(draft.Questions) != 1 {
		t.Fatalf("期望 1 道题救回,实际 %d", len(draft.Questions))
	}
	if !strings.Contains(draft.Questions[0].Explanation, "楚河") {
		t.Errorf("explanation 内容异常(应含楚河): %q", draft.Questions[0].Explanation)
	}
}

func TestParseQuizGenerationHandlesFencedJSON(t *testing.T) {
	// Model wraps JSON in ```json fences.
	raw := "```json\n{\"questions\":[{\"type\":\"choice\",\"stem\":\"q\",\"options\":[\"a\",\"b\"],\"answer\":0}]}\n```"
	if _, err := parseQuizGeneration(raw); err != nil {
		t.Fatalf("fenced JSON should parse: %v", err)
	}
}

func TestResolveChunkIDs(t *testing.T) {
	chunks := []model.ContentChunk{
		{ID: 100, ChunkIndex: 3},
		{ID: 200, ChunkIndex: 5},
	}
	drafts := []QuestionDraft{
		{ChunkIndex: 3},
		{ChunkIndex: 5},
		{ChunkIndex: 99}, // not found → 0
		{ChunkIndex: 0},  // synthetic → not in map
	}
	m := ResolveChunkIDs(drafts, chunks)
	if m[3] != 100 {
		t.Errorf("chunk 3 → %d, want 100", m[3])
	}
	if m[5] != 200 {
		t.Errorf("chunk 5 → %d, want 200", m[5])
	}
	if m[99] != 0 {
		t.Errorf("missing chunk should map to 0, got %d", m[99])
	}
	if _, ok := m[0]; ok {
		t.Error("synthetic (index 0) should not be in map")
	}
}

// TestQuizzerGenerateAgentLoopFailurePreservesTrace 防回归:agent loop 失败(ErrMaxSteps)
// 时,quizzer.Generate 必须返回带 partial trace 的非 nil result,让 service 层能落
// ai_runs 排查。生产 job8 曾因 Generate 在此 return nil 丢弃 trace,导致失败时 DB
// 无记录无法排查。这是"agent loop 失败时 result.Trace 非 nil"契约在 quizzer 层的验证。
func TestQuizzerGenerateAgentLoopFailurePreservesTrace(t *testing.T) {
	// genAgent:6 步全调 get_episode_info tool_call,第 7 步(forced)返回空 → ErrMaxSteps。
	// 这样 agent loop 跑满 6 步,trace 累积了 6 步记录,正是要验证透传的部分。
	tb := NewQuizToolbox(&fakeToolDeps{
		episode: &model.Episode{Title: "t", CourseID: 1, VideoRelativePath: "x.mp4"},
	}, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{vecs: [][]float32{{1}}}, 1, 1, 1)
	toolResp := &ai.ChatResponse{FinishReason: "tool_calls", ToolCalls: []ai.ToolCall{toolCall("c", "get_episode_info", "{}")}}
	responses := make([]*ai.ChatResponse, defaultMaxSteps+1)
	for i := range responses {
		responses[i] = toolResp
	}
	responses[defaultMaxSteps] = &ai.ChatResponse{Content: "", FinishReason: "stop"}
	genLLM := &mockLLM{responses: responses}
	genAgent := NewAgent(genLLM, "m", tb, AgentOpts{MaxSteps: defaultMaxSteps})
	// selfCheck 用一个会失败的 LLM——但 agent loop 先失败,走不到 selfCheck。
	checkAgent := NewAgent(&errChatLLM{err: errors.New("check unavailable")}, "m", nil, AgentOpts{MaxSteps: 1})

	q := NewQuizzer(genAgent, checkAgent, NewMemoryStore(&fakeMemoryRepo{}), &fakeToolDeps{}, genLLM, "m")
	res, err := q.Generate(context.Background(), QuizzerRequest{
		EpisodeID: 1, CourseID: 1, UserID: 1, EpisodeTitle: "t",
	})

	// 应该返回 error(ErrMaxSteps 包装),但 res 非 nil 且带 trace。
	if err == nil {
		t.Fatal("期望 agent loop 失败的 error")
	}
	if !errors.Is(err, ErrMaxSteps) {
		t.Errorf("期望 ErrMaxSteps, 实际 %v", err)
	}
	if res == nil {
		t.Fatal("失败时 res 不应为 nil(契约:须带 trace 供 service 层落 ai_runs)")
	}
	if len(res.Trace) != defaultMaxSteps {
		t.Errorf("partial trace 应透传 %d 步, 实际 %d(被丢弃了?)", defaultMaxSteps, len(res.Trace))
	}
}
