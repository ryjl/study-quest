// Package ai is the self-contained AI subsystem for StudyQuest.
//
// It is deliberately an ADD-ON layer: the rest of the backend (model /
// repository / service / handler) keeps working exactly as before when no AI
// provider is configured. This package only READS core data (episodes,
// subtitles, users, progress) and maintains its OWN private tables.
//
// Nothing here uses an agent framework. The ReAct loop, tool dispatch and the
// OpenAI-compatible HTTP wire format are all written by hand — that is the
// point: the agent's decision flow stays readable in code, not hidden behind a
// library.
//
// Three capabilities (chat LLM / embedding / rerank) each have their OWN
// provider config, so embedding can run locally on ONNX while chat goes to a
// relay endpoint, and either can be swapped independently.
package ai

import (
	"context"
	"encoding/json"
	"net/http"
)

// ---------------------------------------------------------------------------
// LLM (chat) provider
// ---------------------------------------------------------------------------

// LLMProvider is the abstraction over any chat-completion backend.
//
// The only built-in implementation is OpenAICompatProvider (see
// openai_compat.go), which speaks the OpenAI-compatible /v1/chat/completions
// wire format — the de-facto standard relayed by services like one-api/new-api.
// Any vendor following that format (DeepSeek, Moonshot, a self-hosted relay,
// real OpenAI) works by just changing base_url + api_key + model in config,
// without touching agent code.
//
// Why an interface and not a concrete struct: the agent logic (agent/) depends
// only on this contract. A future provider (Anthropic native, a local llama.cpp
// server, a mock for tests) implements this interface and is selected by the
// ProviderResolver — no agent code changes. This is the same interface+resolver
// pattern the storage subsystem uses (see internal/storage + storage_resolver).
type LLMProvider interface {
	// Chat performs one chat-completion turn.
	//
	// A "turn" is a single HTTP round-trip to the model. It is NOT the whole
	// agent conversation: an agent loop calls Chat repeatedly, appending tool
	// results between calls (see agent.go). Returning one turn at a time keeps
	// the provider honest about what it actually does (one network request) and
	// leaves the loop logic where it belongs — in the agent.
	//
	// The context is respected for cancellation/timeout; pass one with a
	// deadline so a stuck model call doesn't hang a job forever.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// Ping verifies the provider is reachable and the credentials work. Used by
	// the admin "test connection" button. It should be cheap (one tiny request)
	// and must NOT mutate anything. A non-nil error means "not usable right
	// now"; the error text is safe to show the admin.
	Ping(ctx context.Context) error

	// ProviderType returns the stable identifier stored on the ai_providers row
	// (e.g. "openai_compat"). Used so the resolver can round-trip a stored
	// config back to the right constructor.
	ProviderType() string
}

// Role identifies who said a ChatMessage. Only the three standard roles are
// supported; "system" for instructions, "user" for human/teacher turns and
// "assistant" for model turns (including tool-call requests), plus "tool" for
// feeding a tool's result back.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // a returned tool result addressed to the model
)

// ChatMessage is one entry in the conversation history sent to / returned from
// the model. It mirrors the OpenAI message object closely enough to serialize
// directly, but is our own type so agent code doesn't import any SDK.
type ChatMessage struct {
	Role Role `json:"role"`

	// Content is the textual body. May be empty when the assistant message is a
	// pure tool-call request (ToolCalls set, Content ""). Kept as a plain
	// string, not a multipart array — StudyQuest's prompts are all plain text,
	// so the added complexity of content blocks isn't warranted.
	Content string `json:"content,omitempty"`

	// Name is set on RoleTool messages to identify WHICH tool produced this
	// result (the model needs it to match the result to its tool_call). Also
	// optionally set on RoleUser/RoleSystem for multi-party framing; rarely used
	// here.
	Name string `json:"name,omitempty"`

	// ToolCallID is set on RoleTool messages: the id of the assistant
	// tool_call this result answers. Required by the OpenAI format when role=tool.
	ToolCallID string `json:"tool_call_id,omitempty"`

	// ToolCalls is set on RoleAssistant messages when the model asks to call
	// tools. Empty for a plain text answer. See ToolCall.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall is the model's request to execute one tool (function). When the
// model decides it needs information before answering, it emits one or more of
// these instead of a final text answer; the agent executes them, appends the
// results as RoleTool messages, and calls Chat again. This is the mechanism
// that makes the system an agent rather than a single-shot prompt.
type ToolCall struct {
	// ID is the model-assigned identifier for this call. Must be echoed back on
	// the RoleTool result message (ToolCallID) so the model can correlate them.
	ID string `json:"id"`

	// Type is always "function" in the OpenAI tool-calling spec today; kept on
	// the struct so we serialize faithfully and stay forward-compatible if new
	// tool types appear.
	Type string `json:"type"`

	// Function carries the name + arguments the model chose.
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the inner payload of a ToolCall.
type ToolCallFunction struct {
	// Name is the tool name; it MUST match a tool registered on the request
	// (agent/tools.go). An unknown name is a model mistake — the agent handles
	// it by returning an error string as the tool result rather than crashing.
	Name string `json:"name"`

	// Arguments is the model-produced argument JSON, as a RAW STRING (not parsed
	// into a map). The OpenAI spec sends arguments as a JSON-in-string; keeping
	// it raw means we (a) don't impose a schema on the agent's tool dispatch and
	// (b) can log the exact bytes the model produced — important for debugging
	// "why did the model call my tool with weird args".
	Arguments string `json:"arguments"`
}

// ChatRequest is what we send to the model on one turn.
type ChatRequest struct {
	// Model is the model name as the provider expects it (e.g. "gpt-5.4-mini",
	// "deepseek-chat"). Comes from the ai_providers row, not hardcoded —
	// swapping models is a config change.
	Model string `json:"model"`

	// Messages is the full conversation so far, INCLUDING any tool results from
	// earlier turns in this agent run. The model is stateless between calls, so
	// every turn re-sends the whole history. (This is why long agent loops cost
	// tokens — each turn's input grows.)
	Messages []ChatMessage `json:"messages"`

	// Tools advertises the callable tools to the model. Empty means "no tools,
	// just answer". When non-empty, the model MAY respond with ToolCalls. Set
	// only by agent runs that want tool-calling; a plain summarizer leaves it
	// empty.
	Tools []Tool `json:"tools,omitempty"`

	// ToolChoice controls tool selection. "auto" (default, model decides),
	// "none" (forbid tools this turn), or {"type":"function","function":{"name":...}}
	// to force a specific tool. Left as a flexible json.RawMessage-shaped value
	// (serialized via MarshalJSON on the provider) so we don't constrain the
	// spec. Most callers leave it empty (= auto).
	ToolChoice any `json:"tool_choice,omitempty"`

	// Temperature controls randomness (0 = deterministic, 2 = wild). 0 is right
	// for factual extraction/quiz answer keys; ~0.3-0.7 for generation where you
	// want some variety. 0 when unset — safer for an educational product.
	Temperature float64 `json:"temperature,omitempty"`

	// MaxTokens caps the response length. Set on generation steps to avoid a
	// runaway model burning tokens; leave 0 to let the provider default apply.
	MaxTokens int `json:"max_tokens,omitempty"`

	// ResponseFormat constrains the model's output to a known shape (OpenAI
	// "structured outputs"). When the backend supports it, json_schema strict
	// mode enables grammar-based constrained decoding — the model physically
	// cannot emit a token that violates the schema (e.g. a bare " inside a
	// JSON string value), which is the only true root-cause fix for the LLM
	// bare-quote problem. See docs/pitfalls/llm-json-quotes.md.
	//
	// 留空(默认)= 不约束,走 jsonx.ParseLLMJSON 的事后 repair 兜底(当前所有
	// 生成点的状态)。探测(2026-07-29)确认当前中转站/后端对 response_format
	// 返回 400 unsupported_parameter,故此字段当前无人设置;换支持约束解码的
	// 后端后,各生成点用 JSONSchemaResponseFormat(...) 构造它即可启用根治。
	// 用 json.RawMessage 而非具体 struct:OpenAI 的 response_format 形状多样
	// (json_object / json_schema),RawMessage 让调用方塞任意形状且 omitempty 生效。
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
}

// JSONSchemaResponseFormat 构造一个 OpenAI "structured outputs" 的 response_format
// (json_schema strict),用于 ChatRequest.ResponseFormat。strict=true 让后端走约束解码
// (grammar-based),模型物理上发不出违反 schema 的 token——这是 LLM 裸引号问题的唯一
// 根治路径(详见 docs/pitfalls/llm-json-quotes.md)。
//
// name 是 schema 的逻辑名(OpenAI 要求);schemaJSON 是期望输出的 JSON Schema(已序列化
// 成 JSON 字符串的,通常由 json.Marshal(struct tag) 产出)。返回的 RawMessage 可直接赋给
// ChatRequest.ResponseFormat。
//
// 当前所有后端不支持 response_format(探测 400),故此函数当前无调用方;换支持约束解码
// 的后端后,各生成点用它构造自己的 schema 即可启用根治,parse 点无需改动(已统一走
// jsonx.ParseLLMJSON,届时 repair 兜底保留作第二保险)。
func JSONSchemaResponseFormat(name string, schemaJSON json.RawMessage) (json.RawMessage, error) {
	out := struct {
		Type       string          `json:"type"`
		JSONSchema jsonSchemaBody `json:"json_schema"`
	}{
		Type: "json_schema",
		JSONSchema: jsonSchemaBody{
			Name:   name,
			Strict: true,
			Schema: schemaJSON,
		},
	}
	return json.Marshal(out)
}

// jsonSchemaBody 是 response_format.json_schema 的内层结构(见 JSONSchemaResponseFormat)。
type jsonSchemaBody struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

// Tool advertises one callable function to the model. The model sees the
// Function spec (name + description + JSON-schema parameters) and decides
// whether/when to call it. The actual Go execution lives in agent/tools.go and
// is keyed by Function.Name.
type Tool struct {
	Type     string         `json:"type"` // always "function"
	Function ToolSpec       `json:"function"`
}

// ToolSpec is the schema the model uses to decide how to call a tool. Writing a
// good description and a precise parameter schema is the main lever for getting
// the model to call tools correctly — the agent spends real effort here (see
// agent/tools.go).
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // a JSON Schema object
}

// ChatResponse is what one Chat turn returns. Note this is ONE turn: it might
// be a tool-call request (FinishReason "tool_calls", ToolCalls populated,
// Content empty) OR a final answer (FinishReason "stop", Content populated).
// The agent loop branches on FinishReason to decide which.
type ChatResponse struct {
	// Content is the model's text answer. Empty when the model is requesting
	// tool calls instead of answering. For the common case (a final answer)
	// this is what the caller wants.
	Content string

	// ToolCalls is non-empty when the model wants tools executed. The agent
	// runs each, appends RoleTool results, and loops. Empty for a final answer.
	ToolCalls []ToolCall

	// FinishReason is why the model stopped this turn:
	//   "stop"        — normal completion (final answer in Content)
	//   "tool_calls"  — wants tools run (see ToolCalls)
	//   "length"      — hit MaxTokens mid-output (output likely truncated)
	//   "content_filter" — provider blocked something
	// The agent treats anything other than "tool_calls" as "turn is over" and
	// either uses the answer or surfaces a problem.
	FinishReason string

	// Usage is token accounting for this single turn. Aggregated across turns
	// by the agent and written to ai_runs for observability/cost tracking.
	Usage Usage

	// Headers 暴露本次响应的原始 HTTP 响应头。正常业务路径(总结/出题/建议)用不到,
	// 它是为 admin 的"实战测试"加的——通过 server / via / x-served-by 等头启发式推测
	// 中转站背后的真实模型后端(Gemini / OpenAI 系 / DeepSeek...)。nil 表示调用方未
	// 填充(例如 Ping 或被 mock 的实现),读之前判空。不影响现有调用方,默认零值。
	Headers http.Header
}

// Usage is the token accounting for one model call. The agent sums these across
// all turns in a run and persists them, so the admin can see "this summary cost
// 2,341 prompt tokens + 480 completion tokens".
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ---------------------------------------------------------------------------
// Embedding provider
// ---------------------------------------------------------------------------

// Embedder turns text into fixed-dimension vectors for similarity search (RAG).
//
// Decoupled from LLMProvider on purpose: the chat model can be a remote relay
// while embeddings run locally on ONNX (BGE-small-zh), or vice versa. The
// agent's retrieval code depends only on this contract, so swapping the
// embedding backend never touches retrieval logic.
//
// Batched (Embed takes a slice) because the common case is embedding a whole
// episode's subtitle chunks at once — one batched call beats N single calls
// whether the backend is a local model (one session.Run) or an API (one HTTP).
type Embedder interface {
	// Embed produces one vector per input text, in order. All vectors share the
	// same dimension (Dim()). A nil/empty input slice returns an empty slice,
	// not an error. The context is respected for timeout/cancellation.
	Embed(ctx context.Context, texts []string) ([][]float32, error)

	// Dim is the dimensionality of every vector this embedder produces.
	// Callers store it alongside vectors (content_chunks) so cosine similarity
	// can sanity-check shape on read. Must be constant for the lifetime of one
	// Embedder — changing models means re-embedding everything.
	Dim() int

	// Ping verifies the model is loaded and a trivial embed works. Used by the
	// admin test-connection button.
	Ping(ctx context.Context) error

	// ProviderType returns the stable identifier ("onnx_local", "openai_compat",
	// ...) stored on the ai_providers row.
	ProviderType() string
}
