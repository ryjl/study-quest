package agent

import (
	"encoding/json"
	"time"

	"studyquest/backend/internal/ai/jsonx"
)

// Small shared helpers for the agent package: loose JSON argument parsing for
// tools, text truncation, time formatting. JSON-object extraction(围栏剥离/
// 截断兜底)和裸引号修复已下沉到 jsonx 包,本包通过 jsonx.ParseLLMJSON 统一解析 LLM JSON。

// parseStringArg extracts a string field from a tool-call arguments JSON blob.
// Returns "" if the field is absent or the JSON is malformed — tools treat a
// missing arg as an error message to the model, never a panic. The model's args
// aren't guaranteed valid JSON, so every parse is best-effort.
func parseStringArg(argsJSON, key string) string {
	if argsJSON == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// parseIntArg extracts an integer field from a tool-call arguments JSON blob.
// Returns -1 (a sentinel that's never a valid chunk index) if absent/malformed.
func parseIntArg(argsJSON, key string) int {
	if argsJSON == "" {
		return -1
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return -1
	}
	// JSON numbers decode to float64 in Go's generic unmarshal.
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return -1
}

// truncate caps a string to n runes, appending an ellipsis if shortened. Used
// to keep tool observations (chunk text can be long) within the model's context
// budget and the trace_json storage.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// formatReviewTime renders a LastReviewed timestamp for the mastery tool output.
// "—" for never-reviewed (new student). Kept short since it's one line per chunk.
func formatReviewTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "未复习"
	}
	return t.Format("2006-01-02 15:04")
}

// extractJSONObject 委托到 jsonx.ExtractJSONObject。围栏剥离 + 截断兜底的实现
// 已下沉到 jsonx 包(详见 jsonx.ExtractJSONObject 文档)。本包内多处调用仍用小写
// 别名以保持零改动;新代码应直接用 jsonx.ParseLLMJSON(它内部已含 extract)。
func extractJSONObject(raw string) string {
	return jsonx.ExtractJSONObject(raw)
}
