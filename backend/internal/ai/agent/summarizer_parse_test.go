package agent

import (
	"strings"
	"testing"
)

// TestParseSummaryJSONRecoversTruncated 验证 parseSummaryJSON 在模型输出被
// MaxTokens 砍断时(生产 ep123 的真实故障:JSON 在多字节汉字中间断裂),能靠
// extractJSONObject 的截断兜底救回前面的字段,而不是整个解析失败。
//
// 这是 2026-07-29 加固的核心:之前 parseSummaryJSON 用手写的 first/last-brace
// carving,碰到截断的 JSON 直接 Unmarshal 报 "invalid character 'å' after array
// element"(被砍断的汉字首字节),整个 summary job 失败。改用 extractJSONObject 后,
// 它会闭合未完结的字符串和括号,让 Unmarshal 能解出前面完整的 sections。
func TestParseSummaryJSONRecoversTruncated(t *testing.T) {
	// 模拟生产 ep123 的截断:points 数组第二项写到一半被砍断(末尾是半个汉字)。
	// 注意末尾的 "her 是未闭合的字符串字面量——extractJSONObject 要先闭合它。
	truncated := `{"headline":"学习物主代词", "sections": [{"title": "物主代词", "points": ["**my** = 我的", "**her`
	got, err := parseSummaryJSON(truncated)
	if err != nil {
		t.Fatalf("parseSummaryJSON on truncated input should recover, got error: %v", err)
	}
	// headline 在截断点之前,必须被救回。
	if got.Headline != "学习物主代词" {
		t.Errorf("Headline not recovered: got %q, want %q", got.Headline, "学习物主代词")
	}
	// 第一节标题也被救回。
	if len(got.Sections) != 1 || got.Sections[0].Title != "物主代词" {
		t.Errorf("first section not recovered: got %+v", got.Sections)
	}
	// 第一项要点被救回,第二项被截断的丢失(可接受——救回前 N 项比全废强)。
	if len(got.Sections[0].Points) < 1 || !strings.Contains(got.Sections[0].Points[0], "我的") {
		t.Errorf("first point not recovered: got %+v", got.Sections[0].Points)
	}
}

// TestParseSummaryJSONStripsFences 验证 ```json 围栏被正确剥离。
func TestParseSummaryJSONStripsFences(t *testing.T) {
	fenced := "```json\n{\"headline\":\"标题\",\"sections\":[]}\n```"
	got, err := parseSummaryJSON(fenced)
	if err != nil {
		t.Fatalf("parseSummaryJSON on fenced input failed: %v", err)
	}
	if got.Headline != "标题" {
		t.Errorf("Headline: got %q, want %q", got.Headline, "标题")
	}
}

// TestParseSummaryJSONNormalizesNilSlices 验证所有切片字段非 nil(前端 .map 会炸)。
func TestParseSummaryJSONNormalizesNilSlices(t *testing.T) {
	// 模型可能省略部分数组字段。
	minimal := `{"headline":"只有标题"}`
	got, err := parseSummaryJSON(minimal)
	if err != nil {
		t.Fatalf("parseSummaryJSON failed: %v", err)
	}
	for name, sl := range map[string]interface{}{
		"KeyPoints":     got.KeyPoints,
		"Concepts":      got.Concepts,
		"Sections":      got.Sections,
		"Methods":       got.Methods,
		"CommonMistakes": got.CommonMistakes,
		"PreAdventure":  got.PreAdventure,
	} {
		if sl == nil {
			t.Errorf("%s is nil after parse; must be non-nil empty slice for frontend .map", name)
		}
	}
}

// TestParseSummaryJSONRepairsBareQuotes 验证 parseSummaryJSON 在 LLM 于 JSON
// string value 里写未转义裸双引号时(生产 ep2 真实故障:points 里写"象棋级别"毕业""),
// 靠 RepairBareQuotesInJSON 兜底救回,而不是整个 summary 失败。
//
// 这和 homework 是同源故障——LLM 在 JSON 字符串里用裸 ASCII 双引号表达引语,
// parser 在第一个裸引号误判字符串结束,后面中文成非法 token。prompt 加了引号转义
// 硬规则(软约束,模型偶尔不听),这里复用 homework 的修复做硬兜底。
func TestParseSummaryJSONRepairsBareQuotes(t *testing.T) {
	// 模拟 ep2 的真实失败:points 里 "象棋级别"毕业"" 的裸双引号让 JSON 断裂。
	bareQuote := `{
  "headline": "象棋升级赛与残局复盘",
  "sections": [
    {"title": "升级赛", "points": ["升级赛考到棋协大师即算象棋级别"毕业",之后参加全国赛不受门槛限制。"]}
  ],
  "takeaway": "下棋要有计划"
}`
	got, err := parseSummaryJSON(bareQuote)
	if err != nil {
		t.Fatalf("parseSummaryJSON on bare-quote input should recover via RepairBareQuotesInJSON, got: %v", err)
	}
	if got.Headline != "象棋升级赛与残局复盘" {
		t.Errorf("Headline: got %q, want 象棋升级赛与残局复盘", got.Headline)
	}
	// 那条带裸引号的 point 必须被救回(引号被修复成中文引号或转义)。
	if len(got.Sections) != 1 || len(got.Sections[0].Points) != 1 {
		t.Fatalf("section/point not recovered: %+v", got.Sections)
	}
	if !strings.Contains(got.Sections[0].Points[0], "毕业") {
		t.Errorf("bare-quote point content lost: %q", got.Sections[0].Points[0])
	}
	// 修复后不该再有裸双引号嵌在值中间(RepairBareQuotesInJSON 会替换成「」)。
	if strings.Contains(got.Sections[0].Points[0], `"毕业"`) {
		t.Errorf("bare quotes not repaired: %q", got.Sections[0].Points[0])
	}
}
