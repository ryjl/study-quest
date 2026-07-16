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
// trailing brace — so we walk the string tracking brace depth and stop at the
// first balanced close. Strings inside the JSON (which may contain braces) are
// respected by tracking escape state.
//
// This is more robust than first/last-brace carving: if the model emits
// `{"questions":[...]}\n\n Hope this helps!`, we return exactly the object.
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
	// Walk from start tracking depth, honoring string literals + escapes so a
	// brace inside a string value (e.g. an explanation containing "{}") doesn't
	// corrupt the count.
	depth := 0
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
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1] // first balanced object
			}
		}
	}
	// Unbalanced (truncated output) — return the best-effort tail so the caller
	// gets a clear "unexpected EOF" rather than silently grabbing too much.
	return s[start:]
}
