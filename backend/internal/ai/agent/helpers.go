package agent

import (
	"encoding/json"
	"strings"
	"time"
)

// Small shared helpers for the agent package: loose JSON argument parsing for
// tools, text truncation, time formatting, and robust JSON-object extraction
// (the model often wraps JSON in prose or ``` fences).

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

// extractJSONObject carves the first BALANCED JSON object out of a model
// response. Relays sometimes wrap structured output in ```json fences or add
// stray prose/trailing content ("Here is the quiz: {...}. Good luck!"). A naive
// "first { to last }" carve breaks when the model appends a second object or a
// trailing brace — so we walk the string tracking an open-bracket stack and
// stop at the first balanced close. Strings inside the JSON (which may contain
// braces) are respected by tracking escape state.
//
// This is more robust than first/last-brace carving: if the model emits
// `{"questions":[...]}\n\n Hope this helps!`, we return exactly the object.
//
// 截断兜底(truncation recovery):如果走到字符串末尾仍未平衡(说明输出被中途砍断——
// 典型是 max_tokens 上限落在了多字节 UTF-8 字符中间,表现为
// "invalid character 'é' after object key:value pair"),不直接返回残缺 JSON 让整次
// 解析失败,而是尽力补全:先闭合未完结的字符串字面量,再按未闭合的开符号栈逆序补对应的
// 闭符号(} 配 {,] 配 [)。补全后的 JSON 能被 Unmarshal 解析,parseQuizGeneration 的
// 逐题校验会丢弃最后一道写了一半的残题,从而救回前面 N-1 道完整题——比整次 run 失败
// 白烧几万 token 强得多。这是 extractJSONObject 的最后一道保险:首选仍是靠足够的
// MaxTokens 让输出不被砍断(见 ai_service_quiz.go 的 MaxTokens 注释)。
func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	// Strip ```json ... ``` fences if present (common with some models).
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	if strings.HasSuffix(s, "```") {
		s = s[:len(s)-len("```")]
	}
	s = strings.TrimSpace(s)

	start := strings.Index(s, "{")
	if start < 0 {
		return s // no object at all — let the caller's Unmarshal report it
	}
	// Walk from start tracking an open-bracket STACK of { and [ (tracking both,
	// not just a { depth counter, so a truncated array like ["A","B can be
	// closed correctly in the fallback below). String literals + escapes are
	// honored so a brace inside a string value (e.g. an explanation containing
	// "{}") doesn't corrupt the stack.
	var openStack []byte
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{', '[':
			openStack = append(openStack, c)
		case '}', ']':
			if len(openStack) > 0 {
				want := byte('{')
				if c == ']' {
					want = '['
				}
				if openStack[len(openStack)-1] == want {
					openStack = openStack[:len(openStack)-1]
					if len(openStack) == 0 {
						return s[start : i+1] // first balanced object
					}
				}
			}
		}
	}
	// Truncated (openStack non-empty at end of string) — recover by closing
	// what's there. Terminate an open string literal first, then emit matching
	// closers in reverse open order. Worst case the result still doesn't parse
	// (we never make it worse than the raw tail); best case we salvage the N-1
	// complete questions and only lose the half-written trailing one.
	out := s[start:]
	if inString {
		out += "\""
	}
	for i := len(openStack) - 1; i >= 0; i-- {
		if openStack[i] == '{' {
			out += "}"
		} else {
			out += "]"
		}
	}
	return out
}
