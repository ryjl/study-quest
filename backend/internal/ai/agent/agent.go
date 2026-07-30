package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"studyquest/backend/internal/ai"
)

// agent.go is the heart of Phase C: a hand-written ReAct (Reason+Act) loop.
//
// WHAT IS REAct?
//   A ReAct agent interleaves REASONING ("I need to find what the student is
//   weak at") and ACTING (calling a tool — get_user_mastery) in a loop. Each
//   loop iteration:
//     1. OBSERVE: the model sees the conversation so far, including the results
//        of any tools it called in earlier iterations.
//     2. THINK:   the model reasons about what to do next. We don't force it to
//        emit explicit "thought" text — with OpenAI-style function calling the
//        model's tool_choice IS its reasoning made visible (which tool, which
//        args). The trace records this as the "thought" implied by the action.
//     3. ACT:     the model either (a) requests a tool call, or (b) emits a
//        final text answer. On (a) we execute the tool, append its result, and
//        loop. On (b) we're done.
//
// WHY NOT USE A FRAMEWORK?
//   The loop is ~60 lines. Hiding it in a library would defeat the purpose of
//   this project, which exists to make agent mechanics readable. Everything the
//   agent "decides" is visible here.
//
// TERMINATION & SAFETY:
//   - The model gives a final answer (FinishReason != "tool_calls") → return.
//   - We hit maxSteps without a final answer → force one more call with
//     ToolChoice="none" so the model MUST answer (it can't call more tools).
//     If that still fails, return ErrMaxSteps so the caller can mark the run
//     failed rather than silently producing nothing.
//   - An unknown tool (model hallucination) → the tool returns an error string;
//     the model sees it and self-corrects. We do NOT abort the run.
//   - A tool's Go error (DB down) → we abort, because feeding the model a fake
//     observation could lead it to confidently wrong conclusions.

// ErrMaxSteps means the agent exhausted its step budget and couldn't produce a
// final answer even after a forced no-tools call. The caller marks the job
// failed; the partial trace is still recorded for debugging.
var ErrMaxSteps = errors.New("agent: reached max steps without a final answer")

// defaultMaxSteps bounds the loop. 6 is enough for the quizzer to: get episode
// info → search subtitles → get mastery → maybe fetch a related chunk → answer.
// Each step re-sends the full conversation (token cost grows linearly), so this
// also caps cost. Tunable per-run via AgentOpts.
const defaultMaxSteps = 6

// TraceStep records one iteration of the loop for observability. The whole
// trace is JSON-encoded into ai_runs.trace_json so the admin "思考时间线" view
// can replay exactly what the agent did, in order.
type TraceStep struct {
	Step int `json:"step"` // 1-based iteration number
	// Thought is the model's implied reasoning for this step. With function
	// calling there's no separate "thought" token; we record a short summary
	// derived from the action (e.g. "调用 search_subtitles"). For a final-answer
	// step, this is "给出最终答案".
	Thought string `json:"thought"`
	// Action describes what the model did this step: a tool call (name + args),
	// or nil for a final answer. Populated from the model's tool_calls.
	Action *TraceAction `json:"action,omitempty"`
	// Observation is the tool's result text (what the model sees next). Empty
	// for the final-answer step. Truncated to keep trace_json scannable.
	Observation string `json:"observation,omitempty"`
	// IsFinal marks the step that produced the final answer.
	IsFinal bool `json:"is_final,omitempty"`
}

// TraceAction is the tool invocation part of a trace step.
type TraceAction struct {
	Tool string `json:"tool"`
	Args string `json:"args"` // raw args JSON from the model, for debugging
}

// AgentResult is what Run returns: the final answer text plus the full trace
// and aggregated token usage (the caller persists these to ai_runs).
type AgentResult struct {
	FinalText string      // the model's final answer (what the caller parses)
	Trace     []TraceStep // every loop iteration, for observability
	Usage     ai.Usage    // summed across all Chat turns in this run
	Turns     int         // number of Chat calls made
	// SystemPrompt / UserPrompt 是本次 Run 发给 LLM 的开场消息(即入参本身)。
	// ReAct 循环内部可能多次调 LLM(每步重发完整历史),但 system+user 这对 seed
	// 只记一次——它就是最终发给 LLM 的首条 system + 首条 user 消息。透传给 service
	// 层写进 ai_runs.system_prompt_text / user_prompt_text,供 admin "查看回放"
	// 时还原本次到底发了什么 prompt(原来这两段不存,调 prompt 是盲调)。
	SystemPrompt string // 最终发给 LLM 的 system prompt(= Run 入参)
	UserPrompt   string // 最终拼好的 user prompt(= Run 入参)
}

// Agent runs a ReAct loop over an LLMProvider with a set of tools. It is the
// generic engine; the quizzer (quizzer.go) configures it with quiz-specific
// prompts and parses the result.
type Agent struct {
	llm       ai.LLMProvider
	model     string // model name stamped on the request (from provider config)
	toolbox   *Toolbox
	maxSteps  int
	maxTokens int // per-turn output cap; 0 = provider default
}

// AgentOpts configures an Agent. Zero-value defaults apply where sensible.
type AgentOpts struct {
	MaxSteps  int // default defaultMaxSteps
	MaxTokens int // per-turn output token cap; 0 = provider default. Set generously for generation runs so a structured final answer (e.g. a multi-question quiz JSON) isn't truncated mid-stream.
}

// NewAgent builds a ReAct agent. toolbox may be nil for a no-tools run (the loop
// then reduces to a single call — useful for the self-check pass, which must NOT
// call tools, only judge). model is the model name to request.
func NewAgent(llm ai.LLMProvider, modelName string, toolbox *Toolbox, opts AgentOpts) *Agent {
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = defaultMaxSteps
	}
	return &Agent{llm: llm, model: modelName, toolbox: toolbox, maxSteps: opts.MaxSteps, maxTokens: opts.MaxTokens}
}

// Run executes the ReAct loop. systemPrompt + userPrompt seed the conversation;
// capability labels the run for logging. Returns the final answer + trace.
//
// The loop body is annotated step-by-step because it IS the learning material.
func (a *Agent) Run(ctx context.Context, systemPrompt, userPrompt string) (*AgentResult, error) {
	// ── Seed the conversation ──
	// The model is stateless between calls, so we keep the FULL history in
	// `messages` and re-send it every turn. Tool results are appended as
	// RoleTool messages so the model can correlate them to its requests.
	messages := []ai.ChatMessage{
		{Role: ai.RoleSystem, Content: systemPrompt},
		{Role: ai.RoleUser, Content: userPrompt},
	}

	// 记下本次 seed(system+user prompt),透传到返回值供 service 层落 ai_runs。
	// Run 内部 ReAct 循环可能多次调 LLM,但 seed 就是开场消息,这里一次记全。
	result := &AgentResult{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
	}
	trace := []TraceStep{}

	for step := 1; step <= a.maxSteps; step++ {
		// ── THINK + ACT: ask the model what to do next ──
		req := ai.ChatRequest{
			Model:     a.model,
			Messages:  messages,
			MaxTokens: a.maxTokens, // 0 = provider default; the quizzer sets a generous budget so the final JSON isn't truncated mid-generation
		}
		if a.toolbox != nil && len(a.toolbox.Specs()) > 0 {
			req.Tools = a.toolbox.Specs()
			// ToolChoice left unset = "auto" (model decides). Only forced to
			// "none" in the post-loop fallback below.
		}
		resp, err := a.llm.Chat(ctx, req)
		if err != nil {
			result.Trace = trace
			return result, fmt.Errorf("agent: chat at step %d: %w", step, err)
		}
		result.Turns++
		result.Usage.PromptTokens += resp.Usage.PromptTokens
		result.Usage.CompletionTokens += resp.Usage.CompletionTokens

		// ── Branch: did the model request tools, or answer? ──
		if resp.FinishReason != "tool_calls" || len(resp.ToolCalls) == 0 {
			// FINAL ANSWER. The model is done reasoning (or it ignored tools).
			trace = append(trace, TraceStep{
				Step:        step,
				Thought:     "给出最终答案",
				IsFinal:     true,
				Observation: truncate(resp.Content, 600),
			})
			result.FinalText = resp.Content
			result.Trace = trace
			return result, nil
		}

		// TOOL CALLS. Append the assistant's tool-call message to history (the
		// model needs it for correlation), then execute each tool and append its
		// result as a RoleTool message.
		messages = append(messages, ai.ChatMessage{
			Role:      ai.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		for _, tc := range resp.ToolCalls {
			observation, execErr := a.toolbox.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
			if execErr != nil {
				// A tool's hard error (DB down) aborts the run: a fake
				// observation could mislead the model into a confidently wrong
				// answer. The caller marks the job failed with this error.
				result.Trace = trace
				return result, fmt.Errorf("agent: tool %q failed: %w", tc.Function.Name, execErr)
			}
			trace = append(trace, TraceStep{
				Step:    step,
				Thought: summarizeThought(tc),
				Action: &TraceAction{
					Tool: tc.Function.Name,
					Args: truncate(tc.Function.Arguments, 300),
				},
				Observation: truncate(observation, 500),
			})
			messages = append(messages, ai.ChatMessage{
				Role:       ai.RoleTool,
				Name:       tc.Function.Name,
				ToolCallID: tc.ID,
				Content:    observation,
			})
		}
	}

	// ── Loop exhausted without a final answer ──
	// Force one more call with ToolChoice="none" so the model MUST produce a
	// text answer (no more tools). This recovers the common case where the model
	// wanted "just one more" tool call.
	forcedReq := ai.ChatRequest{
		Model:      a.model,
		Messages:   messages,
		MaxTokens:  a.maxTokens,
		ToolChoice: "none",
	}
	if a.toolbox != nil && len(a.toolbox.Specs()) > 0 {
		forcedReq.Tools = a.toolbox.Specs() // still advertised, but choice=none forbids calling
	}
	resp, err := a.llm.Chat(ctx, forcedReq)
	if err != nil {
		result.Trace = trace
		return result, fmt.Errorf("agent: forced final call: %w", err)
	}
	result.Turns++
	result.Usage.PromptTokens += resp.Usage.PromptTokens
	result.Usage.CompletionTokens += resp.Usage.CompletionTokens

	if resp.Content == "" {
		result.Trace = trace
		return result, ErrMaxSteps
	}
	trace = append(trace, TraceStep{
		Step:        a.maxSteps + 1,
		Thought:     "达到步数上限,强制给出最终答案",
		IsFinal:     true,
		Observation: truncate(resp.Content, 600),
	})
	result.FinalText = resp.Content
	result.Trace = trace
	return result, nil
}

// summarizeThought produces a short human-readable label for a tool call, used
// as the trace's "thought" field. With function calling the model doesn't emit
// a separate reasoning trace, so we derive one from the action — this is what
// shows up in the admin "思考时间线" so a reader can follow the agent's logic
// without parsing raw tool args.
func summarizeThought(tc ai.ToolCall) string {
	name := tc.Function.Name
	// Pull a short hint from the args if it's a query-type tool.
	var hint string
	if name == "search_subtitles" {
		hint = parseStringArg(tc.Function.Arguments, "query")
	} else if name == "get_related_chunks" {
		if idx := parseIntArg(tc.Function.Arguments, "chunk_index"); idx >= 0 {
			hint = fmt.Sprintf("片段#%d", idx)
		}
	}
	switch {
	case hint != "":
		return fmt.Sprintf("调用 %s(%s)", name, hint)
	default:
		return fmt.Sprintf("调用 %s", name)
	}
}

// TraceJSON serializes the trace for storage on ai_runs.trace_json. Returns ""
// for an empty trace (single-shot capabilities don't have one).
func TraceJSON(trace []TraceStep) string {
	if len(trace) == 0 {
		return ""
	}
	b, err := json.Marshal(trace)
	if err != nil {
		return ""
	}
	return string(b)
}

// ParseTrace decodes a trace_json value back into steps for the admin view.
// Returns nil for empty/malformed input (the view then shows "无思考记录").
func ParseTrace(raw string) []TraceStep {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var steps []TraceStep
	if err := json.Unmarshal([]byte(raw), &steps); err != nil {
		return nil
	}
	return steps
}
