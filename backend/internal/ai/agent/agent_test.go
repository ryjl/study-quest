package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// mockLLM is a scriptable LLMProvider for testing the ReAct loop. It returns a
// canned sequence of responses, one per Chat call, so a test can drive the loop
// through a known trajectory (call tool → get result → final answer).
type mockLLM struct {
	responses []*ai.ChatResponse
	calls     int
	// lastToolChoice lets a test assert the loop forced "none" on the fallback.
	lastToolChoice any
	toolChoices    []any // per-call ToolChoice the loop sent
}

func (m *mockLLM) Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	if m.calls >= len(m.responses) {
		return nil, errors.New("mockLLM: no more scripted responses")
	}
	m.lastToolChoice = req.ToolChoice
	m.toolChoices = append(m.toolChoices, req.ToolChoice)
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}
func (m *mockLLM) Ping(ctx context.Context) error        { return nil }
func (m *mockLLM) ProviderType() string                  { return "mock" }

// toolCall builds a ToolCall the way the OpenAI relay would.
func toolCall(id, name, args string) ai.ToolCall {
	return ai.ToolCall{ID: id, Type: "function", Function: ai.ToolCallFunction{Name: name, Arguments: args}}
}

func TestAgentLoopFinalAnswerImmediately(t *testing.T) {
	// Model answers on the first turn, no tools. The loop should make ONE call
	// and return, with a single final trace step.
	llm := &mockLLM{responses: []*ai.ChatResponse{
		{Content: "{\"questions\":[]}", FinishReason: "stop", Usage: ai.Usage{PromptTokens: 10, CompletionTokens: 5}},
	}}
	a := NewAgent(llm, "test-model", nil, AgentOpts{})
	res, err := a.Run(context.Background(), "sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "{\"questions\":[]}" {
		t.Errorf("got final %q", res.FinalText)
	}
	if res.Turns != 1 {
		t.Errorf("expected 1 turn, got %d", res.Turns)
	}
	if len(res.Trace) != 1 || !res.Trace[0].IsFinal {
		t.Errorf("expected 1 final trace step, got %+v", res.Trace)
	}
	if res.Usage.PromptTokens != 10 || res.Usage.CompletionTokens != 5 {
		t.Errorf("usage not aggregated: %+v", res.Usage)
	}
}

func TestAgentLoopCallsToolThenAnswers(t *testing.T) {
	// Scripted trajectory:
	//   turn 1: model calls get_episode_info
	//   turn 2: model calls search_subtitles(query="通分")
	//   turn 3: model gives final answer
	deps := &fakeToolDeps{
		episode: &model.Episode{Title: "测试课", CourseID: 1, VideoRelativePath: "x/lesson.mp4"},
		course:  &model.Course{Title: "测试课程", Subject: model.Subject{Label: "数学"}},
	}
	tb := NewQuizToolbox(deps, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{vecs: [][]float32{{1}}}, 1, 1, 1)

	llm := &mockLLM{responses: []*ai.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: []ai.ToolCall{toolCall("c1", "get_episode_info", "{}")}},
		{FinishReason: "tool_calls", ToolCalls: []ai.ToolCall{toolCall("c2", "search_subtitles", `{"query":"通分"}`)}},
		{Content: "{\"questions\":[...]}", FinishReason: "stop"},
	}}
	a := NewAgent(llm, "m", tb, AgentOpts{})
	res, err := a.Run(context.Background(), "sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns != 3 {
		t.Errorf("expected 3 turns, got %d", res.Turns)
	}
	// 3 trace steps: 2 tool calls + 1 final
	if len(res.Trace) != 3 {
		t.Fatalf("expected 3 trace steps, got %d: %+v", len(res.Trace), res.Trace)
	}
	if res.Trace[2].IsFinal != true {
		t.Error("last trace step should be final")
	}
	// The search step's thought should mention the query hint.
	if !contains(res.Trace[1].Thought, "通分") {
		t.Errorf("expected thought to mention query, got %q", res.Trace[1].Thought)
	}
	// get_episode_info observation should carry the episode title (proves the
	// tool actually ran and its result was recorded).
	if !contains(res.Trace[0].Observation, "测试课") {
		t.Errorf("expected observation to contain episode title, got %q", res.Trace[0].Observation)
	}
}

func TestAgentLoopUnknownToolDoesNotCrash(t *testing.T) {
	// Model hallucinates a non-existent tool. The tool returns an error STRING
	// (not a Go error); the model then answers. The run must NOT abort.
	tb := NewQuizToolbox(&fakeToolDeps{}, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{}, 1, 1, 1)
	llm := &mockLLM{responses: []*ai.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: []ai.ToolCall{toolCall("c1", "invented_tool", "{}")}},
		{Content: "{\"questions\":[]}", FinishReason: "stop"},
	}}
	a := NewAgent(llm, "m", tb, AgentOpts{})
	res, err := a.Run(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("unknown tool should not error the run: %v", err)
	}
	// The observation should contain the "tool does not exist" message.
	if !contains(res.Trace[0].Observation, "不存在") {
		t.Errorf("expected unknown-tool message in trace, got %q", res.Trace[0].Observation)
	}
}

func TestAgentLoopMaxStepsForcesFinalAnswer(t *testing.T) {
	// Model keeps calling tools forever (6 times), hitting maxSteps. The loop
	// should then force a ToolChoice="none" call and get a final answer.
	tb := NewQuizToolbox(&fakeToolDeps{
		episode: &model.Episode{Title: "t", CourseID: 1, VideoRelativePath: "x.mp4"},
	}, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{vecs: [][]float32{{1}}}, 1, 1, 1)

	toolResp := &ai.ChatResponse{FinishReason: "tool_calls", ToolCalls: []ai.ToolCall{toolCall("c", "get_episode_info", "{}")}}
	responses := []*ai.ChatResponse{}
	for i := 0; i < defaultMaxSteps; i++ {
		responses = append(responses, toolResp)
	}
	// The forced 7th call answers.
	responses = append(responses, &ai.ChatResponse{Content: "forced answer", FinishReason: "stop"})
	llm := &mockLLM{responses: responses}

	a := NewAgent(llm, "m", tb, AgentOpts{})
	res, err := a.Run(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("expected success via forced answer, got %v", err)
	}
	if res.FinalText != "forced answer" {
		t.Errorf("expected forced answer, got %q", res.FinalText)
	}
	// The last call must have had ToolChoice="none".
	if llm.lastToolChoice != "none" {
		t.Errorf("expected final call to force ToolChoice=none, got %v", llm.lastToolChoice)
	}
	// Final trace step should note the force.
	last := res.Trace[len(res.Trace)-1]
	if !last.IsFinal || !contains(last.Thought, "强制") {
		t.Errorf("expected forced final trace step, got %+v", last)
	}
}

func TestAgentLoopMaxStepsNoAnswerReturnsErr(t *testing.T) {
	// Even the forced call returns empty → ErrMaxSteps.
	tb := NewQuizToolbox(&fakeToolDeps{
		episode: &model.Episode{Title: "t", CourseID: 1, VideoRelativePath: "x.mp4"},
	}, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{vecs: [][]float32{{1}}}, 1, 1, 1)

	toolResp := &ai.ChatResponse{FinishReason: "tool_calls", ToolCalls: []ai.ToolCall{toolCall("c", "get_episode_info", "{}")}}
	responses := make([]*ai.ChatResponse, defaultMaxSteps+1)
	for i := range responses {
		responses[i] = toolResp
	}
	// The 7th (forced) call ALSO requests tools — but ToolChoice=none should make
	// the relay return stop... simulate a relay that ignored it (returns tool_calls
	// with empty content anyway). Loop treats empty content as failure.
	responses[defaultMaxSteps] = &ai.ChatResponse{Content: "", FinishReason: "stop"}
	llm := &mockLLM{responses: responses}

	a := NewAgent(llm, "m", tb, AgentOpts{})
	res, err := a.Run(context.Background(), "sys", "user")
	if err == nil {
		t.Fatal("expected ErrMaxSteps")
	}
	if !errors.Is(err, ErrMaxSteps) {
		t.Errorf("expected ErrMaxSteps, got %v", err)
	}
	// 可观测性契约:失败时 result 非 nil 且 Trace 已填充 partial 记录,
	// 供调用方(quizzer.Generate 等)透传给 service 层落 ai_runs。这里跑了 6 步 tool
	// 调用,trace 应有 6 步——若丢失则失败路径无 trace 可排查。
	if res == nil {
		t.Fatal("失败时 result 不应为 nil(契约:Trace 须填充供排查)")
	}
	if len(res.Trace) != defaultMaxSteps {
		t.Errorf("失败时 partial trace 应有 %d 步,实际 %d", defaultMaxSteps, len(res.Trace))
	}
}

func TestAgentLoopChatErrorAborts(t *testing.T) {
	// A Chat call fails (network) → run aborts with that error.
	errLLM := &errChatLLM{err: errors.New("network down")}
	a := NewAgent(errLLM, "m", nil, AgentOpts{})
	res, err := a.Run(context.Background(), "sys", "user")
	if err == nil || !contains(err.Error(), "network down") {
		t.Errorf("expected network error, got %v", err)
	}
	// 可观测性契约:chat 失败时 result 也不应为 nil(第一步就失败,trace 是空切片,
	// 但 result 必须非 nil 让调用方能安全取 res.SystemPrompt 落 ai_runs)。
	if res == nil {
		t.Fatal("chat 失败时 result 不应为 nil")
	}
}

func TestTraceJSONRoundTrip(t *testing.T) {
	trace := []TraceStep{
		{Step: 1, Thought: "调用 search", Action: &TraceAction{Tool: "search_subtitles", Args: `{"query":"x"}`}, Observation: "结果"},
		{Step: 2, Thought: "最终答案", IsFinal: true},
	}
	js := TraceJSON(trace)
	if js == "" {
		t.Fatal("expected non-empty json")
	}
	parsed := ParseTrace(js)
	if len(parsed) != 2 || parsed[1].IsFinal != true {
		t.Errorf("round-trip lost data: %+v", parsed)
	}
	if ParseTrace("") != nil {
		t.Error("empty trace should parse to nil")
	}
}

// errChatLLM always fails Chat.
type errChatLLM struct{ err error }

func (m *errChatLLM) Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	return nil, m.err
}
func (m *errChatLLM) Ping(ctx context.Context) error { return fmt.Errorf("ping: %w", m.err) }
func (m *errChatLLM) ProviderType() string           { return "err" }
