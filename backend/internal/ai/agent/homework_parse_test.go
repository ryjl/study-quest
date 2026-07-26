package agent

import (
	"strconv"
	"strings"
	"testing"
)

// homework_parse_test.go 验证 ParseHomeworkGeneration 的逐题校验逻辑。表驱动覆盖:
//   - happy path:8 种题型各一道,全部保留
//   - 各题型 scoring 缺关键字段 → 丢该题,其它保留(choice 缺 correct_index / multi_choice
//     correct_indices 越界 / fill 缺 accept / short_answer 缺 reference / copy_word 缺 content)
//   - 未知 type → 丢
//   - question 引用不存在的 section_seq → 丢
//   - 全废 → 返回 error
//   - JSON 被 MaxTokens 截断(extractJSONObject 救回前 N 题)→ 部分保留
//   - 完全非 JSON 输入 → error
//
// 同时验证:校验通过后 scoring 被规范化为干净 JSON(剔除 LLM 可能多写的冗余字段),
// 体现在 happy path 的断言里。

// buildHomeworkJSON 是测试辅助:用 Go 字符串拼接构造一份作业卷 JSON,可控每道题的内容。
// 不用 encoding/json.Marshal 是为了能精确控制"残题"的字段缺失(测试 malformed 场景)。
// 直接拼裸 JSON 字符串可读性差,但能让测试精确表达"LLM 漏了哪个字段"。
type homeworkSectionBuilder struct {
	secSeq   int
	title    string
	passage  *string // passage_title;nil 则不写 passage 段
	qlines   []string
}

func (b *homeworkSectionBuilder) String() string {
	var sb strings.Builder
	sb.WriteString(`{"seq":`)
	sb.WriteString(strconv.Itoa(b.secSeq))
	sb.WriteString(`,"title":"`)
	sb.WriteString(b.title)
	sb.WriteString(`","passage_title":`)
	if b.passage != nil {
		sb.WriteString(`"`)
		sb.WriteString(*b.passage)
		sb.WriteString(`"`)
	} else {
		sb.WriteString(`null`)
	}
	sb.WriteString(`,"passage_content":null,"questions":[`)
	sb.WriteString(strings.Join(b.qlines, ","))
	sb.WriteString(`]}`)
	return sb.String()
}

// qQuestion 是构造一题 JSON 的辅助。rawJSON 是该题的完整对象字面量(含花括号),让调用方
// 完全控制字段(包括故意缺字段/写字段写错值的 malformed 场景)。
func qQuestion(rawJSON string) string { return rawJSON }

// goodQuestionXxx 是 8 种题型的合法模板(每题 section_seq 填 1,可通过 string replace 改)。
// scoring 故意带一个冗余字段 "_noise":"x",用来验证规范化后会剔除冗余、只保留 schema 字段。
func goodChoice(idx int) string {
	return `{"section_seq":1,"seq":` + strconv.Itoa(idx) + `,"type":"choice","stem":"选择题` + strconv.Itoa(idx) + `","options":["甲","乙","丙","丁"],"scoring":{"correct_index":2,"_noise":"x"},"explanation":"e"}`
}
func goodMultiChoice(idx int) string {
	return `{"section_seq":1,"seq":` + strconv.Itoa(idx) + `,"type":"multi_choice","stem":"多选题` + strconv.Itoa(idx) + `","options":["甲","乙","丙","丁","戊"],"scoring":{"correct_indices":[0,2,3],"partial_credit":true,"_noise":"x"},"explanation":"e"}`
}
func goodFill(idx int) string {
	return `{"section_seq":1,"seq":` + strconv.Itoa(idx) + `,"type":"fill","stem":"填空` + strconv.Itoa(idx) + `:1+1=___","scoring":{"accept":["2","二"],"_noise":"x"},"explanation":"e"}`
}
func goodShortAnswer(idx int) string {
	return `{"section_seq":1,"seq":` + strconv.Itoa(idx) + `,"type":"short_answer","stem":"简答` + strconv.Itoa(idx) + `","scoring":{"reference":"参考答案","_noise":"x"},"explanation":"e"}`
}
func goodCalculation(idx int) string {
	return `{"section_seq":1,"seq":` + strconv.Itoa(idx) + `,"type":"calculation","stem":"计算` + strconv.Itoa(idx) + `:12×3","scoring":{"reference":"36","_noise":"x"},"explanation":"e"}`
}
func goodCopyWord(idx int) string {
	return `{"section_seq":1,"seq":` + strconv.Itoa(idx) + `,"type":"copy_word","stem":"抄写` + strconv.Itoa(idx) + `","scoring":{"content":"生字","times":3,"_noise":"x"},"explanation":"e"}`
}
func goodDictation(idx int) string {
	return `{"section_seq":1,"seq":` + strconv.Itoa(idx) + `,"type":"dictation","stem":"默写` + strconv.Itoa(idx) + `","scoring":{"reference":"床前明月光","_noise":"x"},"explanation":"e"}`
}
func goodTranslation(idx int) string {
	return `{"section_seq":1,"seq":` + strconv.Itoa(idx) + `,"type":"translation","stem":"翻译` + strconv.Itoa(idx) + `","scoring":{"reference":"Hello","_noise":"x"},"explanation":"e"}`
}

// 一个合法 section 模板(seq=1,无 passage),questions 由调用方提供。
func oneSection(qlines []string) string {
	sec := &homeworkSectionBuilder{secSeq: 1, title: "一、混合题", qlines: qlines}
	return `{"sections":[` + sec.String() + `],"questions_count":` + strconv.Itoa(len(qlines)) + `}`
}

// TestParseHomeworkGenerationHappyPath 8 种题型各一道,全部保留,且 scoring 被规范化(剔除 _noise)。
func TestParseHomeworkGenerationHappyPath(t *testing.T) {
	qlines := []string{
		goodChoice(1), goodMultiChoice(2), goodFill(3), goodShortAnswer(4),
		goodCalculation(5), goodCopyWord(6), goodDictation(7), goodTranslation(8),
	}
	raw := oneSection(qlines)
	draft, _, err := ParseHomeworkGeneration(raw, "chinese")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(draft.Questions) != 8 {
		t.Fatalf("expected 8 questions, got %d", len(draft.Questions))
	}
	if len(draft.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(draft.Sections))
	}
	if draft.Sections[0].Seq != 1 {
		t.Errorf("section seq = %d, want 1", draft.Sections[0].Seq)
	}
	// scoring 规范化验证:每题的 scoring 不应含 "_noise"(冗余字段被剔除)。
	for i, q := range draft.Questions {
		if strings.Contains(q.Scoring, "_noise") {
			t.Errorf("question %d (type %s): scoring not normalized, still contains _noise: %s", i, q.Type, q.Scoring)
		}
	}
	// choice 的 options 被保留。
	if len(draft.Questions[0].Options) != 4 {
		t.Errorf("choice options = %d, want 4", len(draft.Questions[0].Options))
	}
	// 非 choice 题型的 options 被清空。
	for i, q := range draft.Questions {
		if q.Type == "choice" || q.Type == "multi_choice" {
			continue
		}
		if len(q.Options) != 0 {
			t.Errorf("question %d (type %s): options should be empty, got %v", i, q.Type, q.Options)
		}
	}
}

// TestParseHomeworkGenerationDropsBadScoring 表驱动:各题型 scoring 缺关键字段 → 丢该题,其它保留。
func TestParseHomeworkGenerationDropsBadScoring(t *testing.T) {
	cases := []struct {
		name     string
		bad      string // 残题 JSON(故意 malformed scoring)
	}{
		{"choice-missing-correct_index", `{"section_seq":1,"seq":1,"type":"choice","stem":"缺答案的选择题","options":["甲","乙"],"scoring":{},"explanation":"e"}`},
		{"choice-correct_index-out-of-range", `{"section_seq":1,"seq":1,"type":"choice","stem":"越界选择题","options":["甲","乙"],"scoring":{"correct_index":5},"explanation":"e"}`},
		{"choice-few-options", `{"section_seq":1,"seq":1,"type":"choice","stem":"选项太少","options":["甲"],"scoring":{"correct_index":0},"explanation":"e"}`},
		{"multi_choice-indices-out-of-range", `{"section_seq":1,"seq":1,"type":"multi_choice","stem":"越界多选","options":["甲","乙","丙"],"scoring":{"correct_indices":[0,9]},"explanation":"e"}`},
		{"multi_choice-too-few-correct", `{"section_seq":1,"seq":1,"type":"multi_choice","stem":"只有一个正确项的假多选","options":["甲","乙","丙"],"scoring":{"correct_indices":[0]},"explanation":"e"}`},
		{"fill-missing-accept", `{"section_seq":1,"seq":1,"type":"fill","stem":"缺答案的填空","scoring":{},"explanation":"e"}`},
		{"short_answer-missing-reference", `{"section_seq":1,"seq":1,"type":"short_answer","stem":"缺答案的简答","scoring":{},"explanation":"e"}`},
		{"calculation-missing-reference", `{"section_seq":1,"seq":1,"type":"calculation","stem":"缺答案的计算","scoring":{},"explanation":"e"}`},
		{"copy_word-missing-content", `{"section_seq":1,"seq":1,"type":"copy_word","stem":"缺内容的抄写","scoring":{"times":3},"explanation":"e"}`},
		{"dictation-missing-reference", `{"section_seq":1,"seq":1,"type":"dictation","stem":"缺答案的默写","scoring":{},"explanation":"e"}`},
		{"translation-missing-reference", `{"section_seq":1,"seq":1,"type":"translation","stem":"缺答案的翻译","scoring":{},"explanation":"e"}`},
		{"unknown-type", `{"section_seq":1,"seq":1,"type":"essay_magic","stem":"未知题型","scoring":{"x":1},"explanation":"e"}`},
		{"empty-stem", `{"section_seq":1,"seq":1,"type":"choice","stem":"   ","options":["甲","乙"],"scoring":{"correct_index":0},"explanation":"e"}`},
		{"missing-scoring-field", `{"section_seq":1,"seq":1,"type":"choice","stem":"完全没 scoring 字段","options":["甲","乙"],"explanation":"e"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 残题 + 一道合法 choice 题。期望残题被丢、合法题保留。
			raw := oneSection([]string{c.bad, goodChoice(2)})
			draft, _, err := ParseHomeworkGeneration(raw, "math")
			if err != nil {
				t.Fatalf("expected no error (one good question survives), got %v", err)
			}
			if len(draft.Questions) != 1 {
				t.Errorf("expected 1 question (bad dropped), got %d: %+v", len(draft.Questions), draft.Questions)
				return
			}
			if draft.Questions[0].Type != "choice" {
				t.Errorf("surviving question type = %s, want choice", draft.Questions[0].Type)
			}
		})
	}
}

// TestParseHomeworkGenerationBadSectionRef question 引用不存在的 section_seq → 丢该题。
func TestParseHomeworkGenerationBadSectionRef(t *testing.T) {
	// section seq=1,但 question section_seq=99(不存在)→ 丢该题。
	badRef := `{"section_seq":99,"seq":1,"type":"choice","stem":"引用不存在的大题","options":["甲","乙"],"scoring":{"correct_index":0},"explanation":"e"}`
	raw := oneSection([]string{badRef, goodChoice(2)})
	draft, _, err := ParseHomeworkGeneration(raw, "math")
	if err != nil {
		t.Fatalf("expected no error (good question survives), got %v", err)
	}
	if len(draft.Questions) != 1 {
		t.Errorf("expected 1 question (bad-ref dropped), got %d", len(draft.Questions))
	}
}

// TestParseHomeworkGenerationAllBad 全废 → 返回 error。
func TestParseHomeworkGenerationAllBad(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "all-questions-malformed",
			raw:  oneSection([]string{`{"type":"choice","stem":"x","options":["a"]}`, `{"type":"fill","stem":"y","scoring":{}}`}),
		},
		{
			name: "empty-questions-array",
			raw:  `{"sections":[{"seq":1,"title":"空","passage_title":null,"passage_content":null,"questions":[]}],"questions_count":0}`,
		},
		{
			name: "non-json-input",
			raw:  "这不是 JSON,只是普通中文文字。",
		},
		{
			name: "empty-string",
			raw:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := ParseHomeworkGeneration(c.raw, "math")
			if err == nil {
				t.Errorf("expected error for %q, got nil", c.name)
			}
		})
	}
}

// TestParseHomeworkGenerationRecoversTruncated JSON 被 MaxTokens 截断,extractJSONObject
// 救回前 N 题,部分保留(参考 tools_test.go 的 TestParseQuizGenerationRecoversTruncatedQuiz)。
func TestParseHomeworkGenerationRecoversTruncated(t *testing.T) {
	// 5 道完整题 + 第 6 道只写了半个 stem(模拟末尾截断)。
	var b strings.Builder
	b.WriteString(`{"sections":[{"seq":1,"title":"一、选择题","passage_title":null,"passage_content":null,"questions":[`)
	for i := 0; i < 5; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(goodChoice(i + 1))
	}
	// 第 6 道截断:写了对象开始但 stem 字符串没闭合。
	b.WriteString(`,{"section_seq":1,"seq":6,"type":"choice","stem":"截断`)
	// 故意在这里截断——没有闭合 stem 字符串、对象、数组、外层对象。
	draft, _, err := ParseHomeworkGeneration(b.String(), "math")
	if err != nil {
		t.Fatalf("expected recovery from truncation, got err: %v", err)
	}
	if len(draft.Questions) != 5 {
		t.Errorf("expected 5 salvaged questions (drop truncated 6th), got %d", len(draft.Questions))
	}
}

// TestParseHomeworkGenerationFencedJSON 模型把 JSON 包在 ```json 代码块里,应能解析。
func TestParseHomeworkGenerationFencedJSON(t *testing.T) {
	inner := oneSection([]string{goodChoice(1)})
	raw := "```json\n" + inner + "\n```"
	draft, _, err := ParseHomeworkGeneration(raw, "math")
	if err != nil {
		t.Fatalf("fenced JSON should parse: %v", err)
	}
	if len(draft.Questions) != 1 {
		t.Errorf("expected 1 question, got %d", len(draft.Questions))
	}
}

// TestParseHomeworkGenerationProseWrapped 模型在 JSON 前后加了散文,应能截出 JSON 部分。
func TestParseHomeworkGenerationProseWrapped(t *testing.T) {
	inner := oneSection([]string{goodChoice(1)})
	raw := "好的,这是作业:\n" + inner + "\n希望对学生有帮助。"
	draft, _, err := ParseHomeworkGeneration(raw, "math")
	if err != nil {
		t.Fatalf("prose-wrapped JSON should parse: %v", err)
	}
	if len(draft.Questions) != 1 {
		t.Errorf("expected 1 question, got %d", len(draft.Questions))
	}
}

// TestParseHomeworkGenerationCopyWordTimesDefault copy_word 缺 times 时默认 3。
func TestParseHomeworkGenerationCopyWordTimesDefault(t *testing.T) {
	noTimes := `{"section_seq":1,"seq":1,"type":"copy_word","stem":"抄写","scoring":{"content":"字"},"explanation":"e"}`
	raw := oneSection([]string{noTimes})
	draft, _, err := ParseHomeworkGeneration(raw, "chinese")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(draft.Questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(draft.Questions))
	}
	// scoring 应含 "times":3(缺省补 3)。
	if !strings.Contains(draft.Questions[0].Scoring, `"times":3`) {
		t.Errorf("expected times default to 3, scoring = %s", draft.Questions[0].Scoring)
	}
}

// TestParseHomeworkGenerationCopyWordContentTooLongDropped v2:copy_word 的 content
// 超 12 字符(整段课文级)→ 丢这道题,避免前端田字格撑爆 A4 卷面行。
func TestParseHomeworkGenerationCopyWordContentTooLongDropped(t *testing.T) {
	// 13 个中文字(超 12 上限)+ 一道合法短 content 题,长的那道应被丢、短的保留。
	longContent := `{"section_seq":1,"seq":1,"type":"copy_word","stem":"抄写","scoring":{"content":"这是一段超长的抄写内容根本不该出现在抄写题里"},"explanation":""}`
	shortContent := `{"section_seq":1,"seq":2,"type":"copy_word","stem":"抄写","scoring":{"content":"天地人"},"explanation":""}`
	raw := oneSection([]string{longContent, shortContent})
	draft, _, err := ParseHomeworkGeneration(raw, "chinese")
	if err != nil {
		t.Fatalf("expected no error (one valid question remains), got %v", err)
	}
	if len(draft.Questions) != 1 {
		t.Fatalf("expected 1 question (long content dropped), got %d", len(draft.Questions))
	}
	if !strings.Contains(draft.Questions[0].Scoring, "天地人") {
		t.Errorf("expected the short-content question to survive, got scoring = %s", draft.Questions[0].Scoring)
	}
}

// TestParseHomeworkGenerationMultiChoiceMinCorrectForHalfDefault multi_choice 缺
// min_correct_for_half 时默认 1。
func TestParseHomeworkGenerationMultiChoiceMinCorrectForHalfDefault(t *testing.T) {
	noMin := `{"section_seq":1,"seq":1,"type":"multi_choice","stem":"多选","options":["甲","乙","丙"],"scoring":{"correct_indices":[0,1]},"explanation":"e"}`
	raw := oneSection([]string{noMin})
	draft, _, err := ParseHomeworkGeneration(raw, "math")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(draft.Questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(draft.Questions))
	}
	if !strings.Contains(draft.Questions[0].Scoring, `"min_correct_for_half":1`) {
		t.Errorf("expected min_correct_for_half default to 1, scoring = %s", draft.Questions[0].Scoring)
	}
}

// TestParseHomeworkGenerationReadingComprehension 阅读理解大题(passage + 多道题)能保留。
func TestParseHomeworkGenerationReadingComprehension(t *testing.T) {
	passage := "森林里住着许多小动物"
	sec := &homeworkSectionBuilder{
		secSeq:  1,
		title:   "一、阅读理解",
		passage: &passage,
		qlines: []string{
			`{"section_seq":1,"seq":1,"type":"choice","stem":"根据上文,森林里住着?","options":["小动物","机器人"],"scoring":{"correct_index":0},"explanation":"e"}`,
			`{"section_seq":1,"seq":2,"type":"short_answer","stem":"这篇短文主要讲什么?","scoring":{"reference":"森林动物"},"explanation":"e"}`,
		},
	}
	raw := `{"sections":[` + sec.String() + `],"questions_count":2}`
	draft, _, err := ParseHomeworkGeneration(raw, "chinese")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(draft.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(draft.Questions))
	}
	if draft.Sections[0].PassageTitle == nil || *draft.Sections[0].PassageTitle != passage {
		t.Errorf("passage_title not preserved: %v", draft.Sections[0].PassageTitle)
	}
}

// TestParseHomeworkGenerationSubjectKeyDoesNotGate 校验 subjectKey 当前不改变校验行为
// (科目配方在 prompt 层约束,代码层对所有科目一视同仁)。保留参数供后续按科目 gating。
// 用 chinese 科目跑数学白名单题型(calculation)依然能通过——证明校验不按科目黑名单丢题。
func TestParseHomeworkGenerationSubjectKeyDoesNotGate(t *testing.T) {
	raw := oneSection([]string{goodCalculation(1)})
	draft, _, err := ParseHomeworkGeneration(raw, "chinese") // chinese 黑名单含 calculation,但代码层不拦
	if err != nil {
		t.Fatalf("expected no error (subjectKey doesn't gate at parse layer), got %v", err)
	}
	if len(draft.Questions) != 1 || draft.Questions[0].Type != "calculation" {
		t.Errorf("calculation should survive regardless of subjectKey, got %+v", draft.Questions)
	}
}

// TestExtractJSONObjectWrapper ExtractJSONObject 是 extractJSONObject 的导出 wrapper,
// 验证两者返回一致(防止后续有人改了 helpers.go 的实现而 wrapper 漏改)。
func TestExtractJSONObjectWrapper(t *testing.T) {
	cases := []string{
		`{"a":1}`,
		"```json\n{\"a\":1}\n```",
		"结果: {\"a\":1} 完毕",
		`{"a":1`,
	}
	for _, c := range cases {
		if got := ExtractJSONObject(c); got != extractJSONObject(c) {
			t.Errorf("ExtractJSONObject(%q) = %q, but extractJSONObject = %q (should match)", c, got, extractJSONObject(c))
		}
	}
}

// TestRepairBareQuotesInJSON 验证 v2 兜底修复:LLM 在 string value 里写的未转义裸
// ASCII 双引号(引语)替换成中文「」(成对交替)。这是 ParseHomeworkGeneration 第一次
// json.Unmarshal 失败时的硬兜底。
func TestRepairBareQuotesInJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "合法 JSON 不改(无裸引号)",
			in:   `{"a":"hello","b":1}`,
			want: `{"a":"hello","b":1}`,
		},
		{
			name: "转义双引号不动(backslash-quote 是合法转义)",
			in:   `{"a":"hello\"world"}`,
			want: `{"a":"hello\"world"}`,
		},
		{
			name: "单个引语:中文间的裸双引号成对替换",
			in:   `{"explanation":"用"对应思想"比较大小"}`,
			want: `{"explanation":"用「对应思想」比较大小"}`,
		},
		{
			name: "多个引语:每个 string 独立配对(toggle 在 string 结束时重置)",
			in:   `{"a":"用"思想"做","b":"讲"算法"快"}`,
			want: `{"a":"用「思想」做","b":"讲「算法」快"}`,
		},
		{
			name: "引号后跟结构字符是真结束(不替换)",
			in:   `{"a":"比较大小","b":1}`,
			want: `{"a":"比较大小","b":1}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RepairBareQuotesInJSON(c.in)
			if got != c.want {
				t.Errorf("RepairBareQuotesInJSON(%q)\n  got  = %q\n  want = %q", c.in, got, c.want)
			}
		})
	}
}

// TestParseHomeworkGeneration_RecoverFromBareQuotes 用真实失败案例(ai_runs id=16)
// 的关键片段验证:LLM 在 explanation 写了裸双引号引语,原 parse 失败,修复后能成功
// parse 出题目。这是 v2 兜底的端到端验证。
func TestParseHomeworkGeneration_RecoverFromBareQuotes(t *testing.T) {
	// 真实失败片段(从 run 16 response_text 提取):一道 choice 题,explanation 里有
	// 裸双引号引语。原 JSON parser 在第一个 " 误判字符串结束。
	raw := `{"sections":[{"seq":1,"title":"一、选择题","passage_title":null,"passage_content":null,"questions":[
		{"section_seq":1,"seq":1,"type":"choice","stem":"23+95 与 87+19 哪个大?","options":["23+95大","87+19大","相等","无法比较"],"scoring":{"correct_index":0},"explanation":"本课强调用"对应思想"比较大小:每个加数都更大,和就更大。"}
	]}],"questions_count":1}`

	// 期望:repair 后能 parse 出 1 道题,explanation 里的裸双引号被「」替换,
	// 且 wasRepaired=true(第一次 parse 失败、靠 repair 救回)。
	draft, wasRepaired, err := ParseHomeworkGeneration(raw, "math")
	if err != nil {
		t.Fatalf("ParseHomeworkGeneration should recover via repair, got err: %v", err)
	}
	if !wasRepaired {
		t.Errorf("expected wasRepaired=true (parse recovered via bare-quote repair)")
	}
	if len(draft.Questions) != 1 {
		t.Fatalf("expected 1 question salvaged, got %d", len(draft.Questions))
	}
	expl := draft.Questions[0].Explanation
	if !strings.Contains(expl, "「对应思想」") {
		t.Errorf("expected explanation to have 「对应思想」 (bare quotes replaced), got %q", expl)
	}
	if strings.Contains(expl, `"对应思想"`) {
		t.Errorf("explanation still has bare ASCII quotes: %q", expl)
	}
}
