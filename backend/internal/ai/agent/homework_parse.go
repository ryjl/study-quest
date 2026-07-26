package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// homework_parse.go 解析作业卷(Homework)生成的 LLM 返回 JSON。和 quizzer.go 的
// parseQuizGeneration 平行,但:
//   - 产物结构不同(HomeworkDraft:sections + questions,而不是单层 questions 数组);
//   - 支持的题型更多(8 种:choice/multi_choice/fill/short_answer/calculation/copy_word/
//     dictation/translation),每种题型有独立的 scoring schema 校验;
//   - 校验通过后会把 scoring 重新序列化成规范 JSON(剔除 LLM 可能多写的冗余字段)存进
//     HomeworkDraftQuestion.Scoring,保证落库的是干净 JSON。
//
// 设计:逐题校验,残题丢弃,只有全部题都废才返回 error。参考 quizzer.go parseQuizGeneration
// 的逐题校验范式(:258-322)。不依赖任何 service/repo/model 写入——纯函数。

// ExtractJSONObject 是 extractJSONObject(helpers.go)的导出 wrapper,供 service 层(不同包
// 或同包都行)直接调用。helpers.go 里那个是小写未导出,这里加一层导出避免修改 helpers.go。
func ExtractJSONObject(raw string) string {
	return extractJSONObject(raw)
}

// homeworkLLMResponse 是 LLM 输出的 JSON 形状(字段名用下划线,和 prompt 约定一致)。
// 仅用于解码,不是对外产物。解码后逐题校验、规范化 scoring,产出 HomeworkDraft。
type homeworkLLMResponse struct {
	Sections []homeworkLLMSection `json:"sections"`
	// QuestionsCount 是 LLM 自报的总题数,解析层不依赖它(以实际 questions 为准),但解码
	// 进来避免 json.Unmarshal 报 unknown field(默认容忍,这里只是显式忽略)。
	QuestionsCount int `json:"questions_count"`
}

type homeworkLLMSection struct {
	Seq            int                 `json:"seq"`
	Title          string              `json:"title"`
	PassageTitle   *string             `json:"passage_title"`
	PassageContent *string             `json:"passage_content"`
	Questions      []homeworkLLMQuestion `json:"questions"`
}

type homeworkLLMQuestion struct {
	SectionSeq  int      `json:"section_seq"`
	Seq         int      `json:"seq"`
	Type        string   `json:"type"`
	Stem        string   `json:"stem"`
	Options     []string `json:"options"`
	Scoring     json.RawMessage `json:"scoring"`
	Explanation string          `json:"explanation"`
}

// 作业题型常量(和 grading.go 的 Question* 常量语义对齐,但作业多了几种题型,所以这里
// 单独定义,不依赖 grading.go——grading.go 的常量是单课时 quiz 用的 3 种)。
const (
	hwTypeChoice      = "choice"
	hwTypeMultiChoice = "multi_choice"
	hwTypeFill        = "fill"
	hwTypeShortAnswer = "short_answer"
	hwTypeCalculation = "calculation"
	hwTypeCopyWord    = "copy_word"
	hwTypeDictation   = "dictation"
	hwTypeTranslation = "translation"
)

// ParseHomeworkGeneration 解析 LLM 返回的作业卷 JSON,逐题校验,残题丢弃,全废才 error。
// 返回 HomeworkDraft{Sections, Questions}。
//
// 解析流程:
//  1. extractJSONObject 截断兜底(救回被 MaxTokens 砍断的 JSON);
//  2. json.Unmarshal 到 homeworkLLMResponse;
//  3. 逐题按 type 校验 scoring,不合法的题丢弃;
//  4. 校验每题的 section_seq 能在 sections 里找到,否则丢弃;
//  5. 全废返回 error,否则返回 HomeworkDraft。
//
// Scoring 规范化:校验通过后把 scoring 重新 marshal 成规范 JSON(只保留该题型 schema 关心的
// 字段),存进 HomeworkDraftQuestion.Scoring,保证落库的是干净 JSON 而非 LLM 原样(可能
// 多写了冗余字段)。
// ParseHomeworkGeneration 解析 LLM 返回的作业卷 JSON,逐题校验,残题丢弃,全废才 error。
// 返回 (draft, wasRepaired, error):wasRepaired=true 表示第一次 parse 失败、靠引号修复
// 兜底救回(LLM 在 string value 写了裸 ASCII 双引号)。service 层据此在 ai_runs 留痕。
func ParseHomeworkGeneration(raw string, subjectKey string) (HomeworkDraft, bool, error) {
	_ = subjectKey // 当前实现不按科目调整校验规则(科目配方在 prompt 层已约束),保留参数供后续扩展

	trimmed := extractJSONObject(raw)
	var resp homeworkLLMResponse
	if err := json.Unmarshal([]byte(trimmed), &resp); err != nil {
		// v2 兜底:parse 失败常见根因是 LLM 在 string value 里写了未转义的裸 ASCII
		// 双引号(如 "explanation":"用"对应思想"比较"),parser 在第一个 " 误判字符串
		// 结束,后面中文成非法 token(报 "invalid character 'X' after object key:value
		// pair")。prompt 强化(引号转义硬规则)是软约束,LLM 偶尔不听,这里加硬兜底:
		// 用启发式把"明显是引语而非结构"的裸双引号替换成中文「」(成对交替),再 parse 一次。
		// 只在第一次失败时尝试一次,不影响正常路径性能。
		repaired := RepairBareQuotesInJSON(trimmed)
		if repaired == trimmed {
			return HomeworkDraft{}, false, fmt.Errorf("invalid homework JSON: %w", err)
		}
		var resp2 homeworkLLMResponse
		if err2 := json.Unmarshal([]byte(repaired), &resp2); err2 != nil {
			// v2 review MAJOR-4:错误信息不拼 err2(含修复版片段,可能泄露 LLM 原文)。
			// 诊断细节走 ai_runs.response_text(那条本就是 admin 诊断用,设计意图)。
			return HomeworkDraft{}, false, fmt.Errorf("invalid homework JSON (repair attempted but still failed): %w", err)
		}
		resp = resp2
		// 走到这里说明 trimmed 第一次失败 + repair 后成功:标记靠修复救回。
		// 用包级 repair Happened 标志传给 parse 内部不可行(并发),所以直接返回 true,
		// service 层接收 wasRepaired 参数写入 ai_runs.self_check_note。
		return parseHomeworkDraftFromResp(resp, true)
	}
	return parseHomeworkDraftFromResp(resp, false)
}

// parseHomeworkDraftFromResp 把已 unmarshal 的 homeworkLLMResponse 校验 + 组装成 draft。
// 从 ParseHomeworkGeneration 抽出,让"正常路径"和"修复后重试"共用校验逻辑,wasRepaired
// 由调用方传入(走 repair 分支 true,否则 false)。返回 (draft, wasRepaired, error)。
func parseHomeworkDraftFromResp(resp homeworkLLMResponse, wasRepaired bool) (HomeworkDraft, bool, error) {

	// 收集所有合法 section 的 seq 集合,供后续校验 question 的 section_seq 引用。
	// section 本身只校验 seq>0(题号语义),title 空我们宽容(可能 LLM 漏写),不丢 section,
	// 因为丢了 section 会连带丢它下面的题——宁可保留一个没标题的大题。
	validSections := make([]HomeworkDraftSection, 0, len(resp.Sections))
	sectionSeqSet := make(map[int]bool, len(resp.Sections))
	for _, s := range resp.Sections {
		if s.Seq <= 0 {
			continue // seq 非法的 section 丢弃,它下面的题也会因 section_seq 找不到而被丢
		}
		validSections = append(validSections, HomeworkDraftSection{
			Seq:            s.Seq,
			Title:          strings.TrimSpace(s.Title),
			PassageTitle:   s.PassageTitle,
			PassageContent: s.PassageContent,
		})
		sectionSeqSet[s.Seq] = true
	}

	cleaned := make([]HomeworkDraftQuestion, 0, len(resp.Sections)*4)
	for _, sec := range resp.Sections {
		for _, q := range sec.Questions {
			dq, ok := validateHomeworkQuestion(q)
			if !ok {
				continue // 残题丢弃
			}
			// section_seq 必须能在 sections 里找到。这里 q.SectionSeq 应等于所属 sec.Seq,
			// 但 LLM 偶尔写错,用 q.SectionSeq 而非 sec.Seq 做引用一致性校验更严。
			if !sectionSeqSet[q.SectionSeq] {
				continue // 引用了不存在的 section_seq,丢弃
			}
			cleaned = append(cleaned, dq)
		}
	}

	if len(cleaned) == 0 {
		return HomeworkDraft{}, wasRepaired, fmt.Errorf("all questions discarded")
	}

	return HomeworkDraft{
		Sections:  validSections,
		Questions: cleaned,
	}, wasRepaired, nil
}

// validateHomeworkQuestion 校验单道题:返回 (规范化后的题, 是否合法)。不合法返回 (zero, false),
// 调用方据此丢弃残题。校验维度:stem 非空、type 在 8 种已知题型内、按 type 校验 scoring 字段、
// options 数量(choice/multi_choice)。
//
// 校验通过后会把 scoring 重新 marshal 成规范 JSON(只保留该题型 schema 关心的字段)。
func validateHomeworkQuestion(q homeworkLLMQuestion) (HomeworkDraftQuestion, bool) {
	// 1. stem 必须非空。
	if strings.TrimSpace(q.Stem) == "" {
		return HomeworkDraftQuestion{}, false
	}

	// 2. 按 type 分派校验 + 规范化 scoring。
	normalized, ok := normalizeHomeworkScoring(q.Type, q.Options, q.Scoring)
	if !ok {
		return HomeworkDraftQuestion{}, false
	}

	return HomeworkDraftQuestion{
		SectionSeq:  q.SectionSeq,
		Seq:         q.Seq,
		Type:        q.Type,
		Stem:        q.Stem,
		Options:     normalizedOptions(q.Type, q.Options),
		Scoring:     normalized,
		Explanation: q.Explanation,
	}, true
}

// normalizedOptions 返回该题型应该保留的 options。choice/multi_choice 用 options,其它题型
// 一律清空(避免 LLM 在 short_answer 上误填 options 干扰持久化)。
func normalizedOptions(typ string, opts []string) []string {
	if typ == hwTypeChoice || typ == hwTypeMultiChoice {
		if len(opts) == 0 {
			return nil
		}
		out := make([]string, len(opts))
		copy(out, opts)
		return out
	}
	return nil
}

// normalizeHomeworkScoring 按 type 校验 + 规范化 scoring。返回规范 JSON 字符串。
// 不合法返回 ("", false)。
//
// 实现要点:每种 type 把 q.Scoring 反序列化进一个针对性的 struct(只保留该题型关心的字段),
// 校验关键字段,然后重新 marshal——这样存库的 scoring 是干净规范的,不含 LLM 可能多写的
// 冗余字段,grading 层解析时也不会被多余字段干扰。
func normalizeHomeworkScoring(typ string, opts []string, raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false // 所有题型都要求 scoring 非空
	}

	switch typ {
	case hwTypeChoice:
		// choice: options ≥2,scoring.correct_index 必须存在且在 [0, len(options)) 范围内。
		// 用 *int 区分"字段缺失"(nil → 拒绝)和"合法的 0"(第一个选项正确)。
		if len(opts) < 2 {
			return "", false
		}
		var s struct {
			CorrectIndex *int `json:"correct_index"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", false
		}
		if s.CorrectIndex == nil || *s.CorrectIndex < 0 || *s.CorrectIndex >= len(opts) {
			return "", false
		}
		// 规范化输出:重新 marshal 成 {"correct_index": N},只保留 schema 字段。
		norm := struct {
			CorrectIndex int `json:"correct_index"`
		}{CorrectIndex: *s.CorrectIndex}
		out, _ := json.Marshal(norm)
		return string(out), true

	case hwTypeMultiChoice:
		// multi_choice: options ≥2,scoring.correct_indices 数组(≥2,都在范围内)。
		if len(opts) < 2 {
			return "", false
		}
		var s struct {
			CorrectIndices    []int `json:"correct_indices"`
			PartialCredit     bool   `json:"partial_credit"`
			MinCorrectForHalf int    `json:"min_correct_for_half"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", false
		}
		if len(s.CorrectIndices) < 2 || len(s.CorrectIndices) > len(opts) {
			return "", false
		}
		for _, idx := range s.CorrectIndices {
			if idx < 0 || idx >= len(opts) {
				return "", false
			}
		}
		// 缺省 MinCorrectForHalf 视为 1(让 partial_credit 真正生效至少要 ≥1 正确项)。
		if s.MinCorrectForHalf < 1 {
			s.MinCorrectForHalf = 1
		}
		out, _ := json.Marshal(s)
		return string(out), true

	case hwTypeFill:
		// fill: scoring.accept 非空数组(至少 1 个可接受答案)。
		var s struct {
			Accept []string `json:"accept"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", false
		}
		if len(s.Accept) == 0 {
			return "", false
		}
		out, _ := json.Marshal(s)
		return string(out), true

	case hwTypeShortAnswer, hwTypeCalculation, hwTypeDictation, hwTypeTranslation:
		// 这四种题型都只要 scoring.reference 非空。
		var s struct {
			Reference string `json:"reference"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", false
		}
		if strings.TrimSpace(s.Reference) == "" {
			return "", false
		}
		out, _ := json.Marshal(s)
		return string(out), true

	case hwTypeCopyWord:
		// copy_word: scoring.content 非空,times 缺省 3。
		// v2:content 长度上限 12 字符(按 rune,中文安全)。抄写题是生字/单词/短语级别,
		// LLM 偶尔误写整段课文(几十字),前端田字格会渲染 N×times 格撑爆 A4 卷面行,
		// 排版失去整齐感。超长的丢这道题(判残题),不勉强截断(截断后的短语可能无意义)。
		var s struct {
			Content string `json:"content"`
			Times   int    `json:"times"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", false
		}
		if strings.TrimSpace(s.Content) == "" {
			return "", false
		}
		if utf8.RuneCountInString(s.Content) > 12 {
			return "", false // 内容过长(整段课文级),丢弃
		}
		if s.Times < 1 {
			s.Times = 3 // 缺省 3 遍
		}
		out, _ := json.Marshal(s)
		return string(out), true

	default:
		// 未知 type:丢弃。
		return "", false
	}
}

// RepairBareQuotesInJSON 尝试修复 LLM 在 JSON string value 里写的未转义裸 ASCII
// 双引号(引语),把它们替换成中文「」(成对交替)。这是 parse 失败时的兜底修复,
// 只在 ParseHomeworkGeneration 第一次 json.Unmarshal 失败时调用一次。
//
// 公开(Exported)是因为 service 层在 parse 成功后也会调它做"事后比对":如果
// RepairBareQuotesInJSON(content) != content,说明本次 parse 靠修复救回(LLM 写了
// 裸引号),据此在 ai_runs.self_check_note 留痕,让 admin 观测 LLM 多频繁写裸引号。
//
// 核心启发式:用状态机模拟 JSON parser,追踪 inString 状态。当在 string 内部遇到 "
// 时,判断它是"真字符串结束"还是"引语":
//   - 真结束:后面(跳过空白)紧跟的是 JSON 结构字符 , : } ]
//   - 引语:后面紧跟的是其它字符(典型:中文字符,如 "用"对应思想"比较" 里的两个 ")
// 引语 " 成对替换成「」,用一个布尔 toggle 决定当前 " 是开「还是闭」。
//
// 边界处理:
//   - \" (反斜杠转义)是合法的,不动(状态机识别反斜杠转义)。
//   - 完全合法的 JSON(无裸引号)输入,输出和输入完全相同(函数幂等,可重复调用)。
//   - 修复不保证 100% 成功(复杂情况如嵌套引语、引号配对错误),最终仍可能 parse 失败,
//     调用方据此决定是否用修复版。
//
// 字节级扫描而非 rune 级:中文 UTF-8 是 3 字节(0xE0-0xEF 开头),多字节字符的后续
// 字节(0x80-0xBF)不会是 " (0x22),所以字节级扫描中文安全。
func RepairBareQuotesInJSON(s string) string {
	var out strings.Builder
	out.Grow(len(s) + 16) // 少量余量,替换通常不增减太多字节

	inString := false
	escape := false      // 上一个字符是 \(正在转义下一个字符)
	quoteOpen := true    // 引语 " 的开/闭 toggle:true 时下一个引语 " 替换成「,false 替换成」

	for i := 0; i < len(s); i++ {
		c := s[i]

		if escape {
			// 上一字节是 \,当前字节是被转义的(如 \" \\ \n),原样输出,清除 escape。
			out.WriteByte(c)
			escape = false
			continue
		}

		if c == '\\' && inString {
			// string 内的反斜杠:转义下一字节。原样输出 \,下一字节走 escape 分支。
			out.WriteByte(c)
			escape = true
			continue
		}

		if c == '"' {
			if !inString {
				// string 开始。
				inString = true
				out.WriteByte(c)
				continue
			}
			// 在 string 内遇到 "。判断是真结束还是引语。
			if isStringTerminator(s, i+1) {
				// 真结束。
				inString = false
				// 重置引语 toggle(每个 string 独立配对,避免上一个 string 的残留影响下一个)
				quoteOpen = true
				out.WriteByte(c)
				continue
			}
			// 引语裸双引号:替换成「或」。
			if quoteOpen {
				out.WriteString("「")
			} else {
				out.WriteString("」")
			}
			quoteOpen = !quoteOpen
			// 仍在 string 内,inString 保持 true
			continue
		}

		out.WriteByte(c)
	}

	return out.String()
}

// isStringTerminator 判断 string 内的 " 后面(从 s[i:] 开始)是否是合法的字符串结束上下文:
// 即跳过空白后,下一个字节是 JSON 结构字符 , : } ]。若是,这个 " 是真的字符串结束;
// 否则是引语(如 "用"对应思想"比较" 中间的 ")。
func isStringTerminator(s string, i int) bool {
	for ; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ', '\t', '\n', '\r':
			continue // 跳过空白
		case ',', ':', '}', ']':
			return true // 结构字符:说明前面的 " 是真结束
		default:
			return false // 其它字符(典型:中文):说明 " 是引语
		}
	}
	return true // 末尾:也算结束(字符串在文件末闭合)
}
