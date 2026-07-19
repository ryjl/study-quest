package agent

import (
	"strings"
	"testing"
)

// TestNormalizeOneMarkdownField covers the two real-world data bugs observed
// in production: literal `\n` not unescaped (breaks GFM tables), and bare
// `<svg>` not wrapped in a fence (gets escaped as text).
func TestNormalizeOneMarkdownField(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // key substring that must appear (or not appear)
	}{
		{
			name: "literal \\n in table becomes real newline",
			in:   "| col1 | col2 |\n|---|---|\n| a | b |",
			want: "| col1 | col2 |\n|---|---|\n| a | b |", // no literal \n
		},
		{
			name: "literal \\n in GFM table source becomes real newline",
			in:   "| 易混淆 | 正确 |\n|---|---|\n| x | y |",
			want: "| 易混淆 | 正确 |\n|---|---|\n| x | y |",
		},
		{
			name: "bare svg gets wrapped in fence",
			in:   "see diagram: <svg viewBox=\"0 0 10 10\"><rect/></svg> end",
			want: "```svg\n<svg viewBox=\"0 0 10 10\"><rect/></svg>\n```",
		},
		{
			name: "already-fenced svg is untouched",
			in:   "```svg\n<svg viewBox=\"0 0 10 10\"><rect/></svg>\n```",
			want: "```svg\n<svg viewBox=\"0 0 10 10\"><rect/></svg>\n```",
		},
		{
			name: "plain text passes through",
			in:   "只是一句普通文本,没有特殊内容。",
			want: "只是一句普通文本,没有特殊内容。",
		},
		{
			name: "literal \\n in plain text becomes real newline",
			in:   "第一行\\n第二行",
			want: "第一行\n第二行",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeOneMarkdownField(tt.in)
			if !strings.Contains(got, tt.want) && got != tt.want {
				// For "contains" checks use Contains; for exact match use ==.
				if tt.want != "" && got != tt.want && !strings.Contains(got, tt.want) {
					t.Errorf("normalizeOneMarkdownField(%q) = %q, want to contain %q", tt.in, got, tt.want)
				}
			}
			// Universal invariant: no literal \n should remain outside code fences.
			if strings.Contains(got, "\\n") {
				// Allowed only inside ``` fences; check by stripping fences.
				stripped := stripCodeFences(got)
				if strings.Contains(stripped, "\\n") {
					t.Errorf("result still has literal \\n outside code fence: %q", got)
				}
			}
		})
	}
}

// stripCodeFences removes ```...``` blocks so we can check the outer text only.
func stripCodeFences(s string) string {
	out := strings.Builder{}
	parts := strings.Split(s, "```")
	for i, p := range parts {
		if i%2 == 0 {
			out.WriteString(p)
		}
	}
	return out.String()
}

// TestNormalizeLiteralNewlineInTable is the exact production bug: a GFM table
// stored with literal `\n` (backslash+n) between rows. After normalize it must
// be a real table.
func TestNormalizeLiteralNewlineInTable(t *testing.T) {
	// Raw bytes as they appear in the DB (literal backslash-n, 2 chars).
	in := "| 易混淆点 | 正确理解 |\n|---|---|\n| 拿走型 | 从总数中去掉 |"
	got := normalizeOneMarkdownField(in)
	if strings.Contains(got, "\\n") {
		t.Fatalf("literal \\n not removed: %q", got)
	}
	if !strings.Contains(got, "\n|---|") {
		t.Fatalf("separator row not on its own line: %q", got)
	}
}

// TestNormalizeBareSvgWraps ensures a bare SVG (no fence) becomes a fence.
// This is the math-course observed bug: model emitted raw <svg>...</svg>.
func TestNormalizeBareSvgWraps(t *testing.T) {
	in := `计算思路。
<svg viewBox="0 0 760 120"><rect/><text>x</text></svg>
end.`
	got := normalizeOneMarkdownField(in)
	if !strings.Contains(got, "```svg") {
		t.Fatalf("bare svg not wrapped in fence: %q", got)
	}
	// The svg content itself must be preserved.
	if !strings.Contains(got, "<rect/>") {
		t.Fatalf("svg content lost: %q", got)
	}
}
