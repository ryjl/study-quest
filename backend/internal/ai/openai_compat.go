package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// OpenAICompatProvider is the single LLMProvider implementation: it speaks the
// OpenAI-compatible /v1/chat/completions wire format.
//
// "OpenAI-compatible" is the de-facto standard relayed by services like one-api
// and new-api (which is what hi-code.cc and similar relays run): the request
// and response shapes match OpenAI's public API, so any compliant backend
// (real OpenAI, DeepSeek, Moonshot, a self-hosted vLLM/llama.cpp server, a
// relay aggregator) works with the SAME code by changing only base_url + api_key
// + model in config. We never hardcode a vendor.
//
// We hand-roll the HTTP rather than importing an SDK: it's a small surface
// (one POST), keeps the agent free of SDK coupling, and makes the exact bytes
// on the wire inspectable when debugging. This is the same "no framework"
// posture used throughout the package.
type OpenAICompatProvider struct {
	baseURL    string // e.g. "https://www.hi-code.cc" (no trailing /v1)
	apiKey     string // bearer token
	model      string // default model when ChatRequest.Model is empty (set by resolver)
	httpClient *http.Client
}

// NewOpenAICompatProvider builds an LLMProvider for an OpenAI-compatible
// endpoint. baseURL should be the host root WITHOUT "/v1" (we append the
// versioned path per call) so the same base works whether the relay exposes
// /v1 or some other version.
//
// The HTTP timeout is fixed at 120s: model calls are slow (10-60s for a long
// generation is normal), and the agent passes its own per-call context deadline
// on top. The client-level timeout is a safety net against a totally stuck
// connection that never honors context cancellation.
func NewOpenAICompatProvider(baseURL, apiKey string) *OpenAICompatProvider {
	return &OpenAICompatProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (p *OpenAICompatProvider) ProviderType() string { return "openai_compat" }

// Chat performs one chat-completion turn against /v1/chat/completions.
//
// One HTTP request, one response. The caller (the agent loop) decides whether
// to call again after a tool_calls response — that branching lives in agent.go,
// not here. This method is intentionally "dumb": send request, decode response,
// surface a typed error. All policy (temperature, tools, when to stop) is set
// by the caller via ChatRequest.
func (p *OpenAICompatProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Fall back to the provider's configured model when the caller omits it
	// (e.g. Ping). Agent turns set it explicitly per request.
	if req.Model == "" {
		req.Model = p.model
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	url := p.baseURL + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		// Distinguish context-deadline/cancellation from other network errors so
		// the agent can report "timed out" vs "endpoint unreachable" usefully.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("chat request cancelled/timed out: %w", err)
		}
		return nil, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	return decodeChatResponse(resp)
}

// Ping does the cheapest possible valid call: one short user message with no
// tools and a tiny max_tokens. It verifies the endpoint is reachable, the key
// works, and the model name is accepted. Non-nil error = unusable.
func (p *OpenAICompatProvider) Ping(ctx context.Context) error {
	req := ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "ping"}},
		// Intentionally tiny: we only want to confirm the round-trip works, not
		// generate anything. Model is left empty so Chat falls back to the
		// provider's configured model (set by the resolver from config).
		MaxTokens: 5,
	}
	_, err := p.Chat(ctx, req)
	return err
}

// SetModel lets the resolver stamp the configured model name onto a provider
// after construction (the provider is built from connection params; the model
// is a separate field on the ai_providers row and may change without rebuilding
// the HTTP client). Used as the default when a ChatRequest leaves Model empty
// (e.g. the Ping self-test).
func (p *OpenAICompatProvider) SetModel(model string) { p.model = model }

// ListModels fetches the available model ids from the relay's /v1/models
// endpoint (OpenAI-compatible). Used by the admin UI so the operator picks a
// model from a dropdown instead of typing a possibly-wrong model id. It hits
// the relay directly with the provider's base_url + api_key — no DB row needed,
// which lets the admin probe a relay's model catalog BEFORE saving a config.
//
// Returns the sorted, deduplicated list of model ids. A non-nil error means the
// relay is unreachable, the key is bad, or the response isn't OpenAI-shaped.
func (p *OpenAICompatProvider) ListModels(ctx context.Context) ([]string, error) {
	url := p.baseURL + "/v1/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build models request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("models request cancelled/timed out: %w", err)
		}
		return nil, fmt.Errorf("models request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Try to surface the relay's structured error; fall back to the raw body.
		var errBody modelsListResponse
		if json.Unmarshal(raw, &errBody) == nil && errBody.Error != nil && errBody.Error.Message != "" {
			return nil, fmt.Errorf("models API error (HTTP %d): %s", resp.StatusCode, errBody.Error.Message)
		}
		snippet := string(raw)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		return nil, fmt.Errorf("models API error (HTTP %d): %s", resp.StatusCode, snippet)
	}

	var body modelsListResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}
	seen := make(map[string]struct{}, len(body.Data))
	out := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// modelsListResponse is the OpenAI-compatible /v1/models response shape. Only
// the fields we use (data[].id, top-level error) are mapped.
type modelsListResponse struct {
	Data []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by,omitempty"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// --- response decoding ---

// chatCompletionResponse is the raw JSON shape returned by OpenAI-compatible
// /v1/chat/completions. We decode into this then translate to ChatResponse.
// Only the fields we use are mapped; unknown fields are ignored.
type chatCompletionResponse struct {
	Choices []struct {
		Index int `json:"index"`
		Message struct {
			Role      string      `json:"role"`
			Content   string      `json:"content"`
			ToolCalls []ToolCall  `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// decodeChatResponse reads an HTTP response and returns our ChatResponse, or a
// descriptive error. Handles the three failure shapes a relay can return:
//   - non-2xx with a JSON error body (most common: 401 bad key, 429 rate limit)
//   - non-2xx with a plain-text body (relay infra errors, HTML error pages)
//   - 2xx with choices[0] present (the happy path)
func decodeChatResponse(resp *http.Response) (*ChatResponse, error) {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read chat response: %w", err)
	}

	// Non-2xx: try to parse a structured error; fall back to the raw body so the
	// admin sees what the relay actually returned (HTML, plaintext, etc.).
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody chatCompletionResponse
		if json.Unmarshal(raw, &errBody) == nil && errBody.Error != nil && errBody.Error.Message != "" {
			return nil, fmt.Errorf("chat API error (HTTP %d): %s", resp.StatusCode, errBody.Error.Message)
		}
		snippet := string(raw)
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return nil, fmt.Errorf("chat API error (HTTP %d): %s", resp.StatusCode, snippet)
	}

	var parsed chatCompletionResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		// Some relays return 200 with an empty choices array on internal errors;
		// surface it rather than panicking on Choices[0].
		return nil, errors.New("chat API returned no choices")
	}

	first := parsed.Choices[0]
	return &ChatResponse{
		Content:      first.Message.Content,
		ToolCalls:    first.Message.ToolCalls,
		FinishReason: first.FinishReason,
		Usage:        parsed.Usage,
		// 响应头在 io.ReadAll(body) 之后仍可读(HTTP 头在 body 之前就已解析完毕),
		// 这里顺手 clone 一份供"实战测试"探测中转站后端模型用;正常业务路径不读它。
		Headers: resp.Header.Clone(),
	}, nil
}
