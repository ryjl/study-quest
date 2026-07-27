package agent

import (
	"encoding/json"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// Grading for quiz answers. Choice questions are graded by index comparison
// (exact). Fill-in-the-blank questions are graded by text normalization against
// a set of acceptable answers — deliberately EXACT (not fuzzy), because fill
// questions are restricted to knowledge points with a unique answer (math
// results, factual recall). Fuzzy matching would silently accept "11" ≈ "12".

// QuestionType constants mirror the Type field on model.Question.
const (
	QuestionChoice      = "choice"
	QuestionMultiChoice = "multi_choice"
	QuestionFill        = "fill"
)

// GradeChoice returns whether the user's selected option index is the correct
// one. Out-of-range indices are simply wrong (defensive — the client should
// never send one, but grading must never panic).
func GradeChoice(q model.Question, userIndex int) bool {
	if q.Type != "" && q.Type != QuestionChoice {
		return false // not a choice question
	}
	idx := choiceCorrectIndex(q)
	return userIndex == idx && userIndex >= 0
}

// choiceCorrectIndex resolves the correct option index for a single-choice
// question from Scoring.correct_index. No fallback — Scoring is the single
// source of truth since the deprecated Answer column was removed (2026-07-27).
// Returns 0 (first option) when Scoring is missing; callers treat a missing
// correct index as "ungradable" at higher layers (defensive).
func choiceCorrectIndex(q model.Question) int {
	if s := ParseScoring(q); s != nil && s.ChoiceIndex != nil {
		return *s.ChoiceIndex
	}
	return 0
}

// GradeFill returns whether the user's free-text answer matches one of the
// question's acceptable answers, after normalization. The acceptable answers
// come from Scoring.accept (a JSON []string of equivalent forms, e.g.
// ["12","十二"]). An empty user answer is always wrong.
//
// Normalization (ai.NormalizeText) lowercases, folds full-width → half-width,
// and strips whitespace/punctuation. So " 12，" == "12" == "１２". But "11" !=
// "12" (exact, as required for math).
func GradeFill(q model.Question, userText string) bool {
	if q.Type != QuestionFill {
		return false
	}
	normalized := ai.NormalizeText(userText)
	if normalized == "" {
		return false
	}
	accept := fillAcceptableAnswers(q)
	if len(accept) == 0 {
		return false
	}
	for _, a := range accept {
		if ai.NormalizeText(a) == normalized {
			return true
		}
	}
	return false
}

// fillAcceptableAnswers resolves the acceptable fill answers from Scoring.accept.
// No fallback — Scoring is the single source of truth since the deprecated
// AnswerText column was removed (2026-07-27).
func fillAcceptableAnswers(q model.Question) []string {
	if s := ParseScoring(q); s != nil && len(s.FillAccept) > 0 {
		return s.FillAccept
	}
	return nil
}

// GradeAnswer dispatches on question type. answerIndex is used for choice,
// answerText for fill. The unused argument is ignored.
func GradeAnswer(q model.Question, answerIndex int, answerText string) bool {
	// multi_choice has no single-index shape; if a caller routes one here it's
	// treated as wrong (use GradeAnswerV with answerIndices for multi).
	if q.Type == QuestionMultiChoice {
		return false
	}
	if q.Type == QuestionFill {
		return GradeFill(q, answerText)
	}
	return GradeChoice(q, answerIndex) // default to choice for "" / unknown
}

// --- multi-choice + unified verdict API (Phase: multi_choice 题型) ---
//
// 新增的判分接口把"题目 → 判分结果"统一成 Verdict,让 service 层不必再按题型分派。
// 旧的 GradeChoice/GradeFill/GradeAnswer 保留(有测试和外部调用依赖),它们语义不变。
// 题型的判分元数据迁移到了 Question.Scoring(JSON, 按 type 解析),grading 优先读 Scoring,
// 空则回退老字段(Answer/AnswerText),保证老数据/老 prompt 生成的题不炸。

// MultiChoiceVerdict 是多选题的细化判分。MissedCount/ExtraCount 供前端展示
// "漏选 X / 多选 Y",让部分对的学生知道自己差在哪。
type MultiChoiceVerdict struct {
	Correct     bool    // 全对
	Partial     bool    // 部分对(partial_credit 开启且达到 min_correct_for_half 且无多选错项)
	Score       float64 // 全对 1.0 / 部分对 0.5 / 错 0
	MissedCount int     // 漏选数(应该选但没选)
	ExtraCount  int     // 多选数(不该选却选了)
}

// Verdict 是统一判分返回:choice/fill 走 Correct/Score,Multi 非 nil 时是多选题。
// Partial 对 choice/fill 永远 false(单选/填空没有"部分对")。
type Verdict struct {
	Correct bool
	Partial bool
	Score   float64
	Multi   *MultiChoiceVerdict // 非 nil 表示这是多选题判分(MissedCount/ExtraCount 在这里)
}

// GradeMultiChoice 判分一道多选题。userIndices 是学生选中的选项索引集合(无序、可空)。
//
// 判分规则:
//   - 全对(userIndices 完全匹配 correct_indices,既不漏也不多)→ Correct=true, Score=1。
//   - 部分对(partial_credit=true 且 选中的正确项数 ≥ min_correct_for_half 且 ExtraCount=0):
//     给半分 Score=0.5。注意"有多选错项"不算部分对——多选了错项说明学生在瞎蒙,算错。
//   - 其他 → Correct=false, Score=0。
//
// 兼容:Scoring 空时返回全错(没法判 correct_indices)。MissedCount/ExtraCount 仍会算出,
// 但因为没拿到 correct_indices,两者都是 len(userIndices)(全算多选),前端看到的就是 0 分。
func GradeMultiChoice(q model.Question, userIndices []int) MultiChoiceVerdict {
	v := MultiChoiceVerdict{}
	s := ParseScoring(q)
	correct := []int{}
	partialCredit := false
	minForHalf := 1
	if s != nil {
		correct = s.MultiCorrectIndices
		partialCredit = s.MultiPartialCredit
		minForHalf = s.MultiMinCorrectForHalf
	}
	if minForHalf < 1 {
		minForHalf = 1 // 缺省视为 1,防 LLM 给 0/负数导致"零正确项也算部分对"
	}
	// 去重 userIndices(防前端误传重复索引),并校验范围(负数/超界算越界,忽略)。
	picked := make(map[int]bool, len(userIndices))
	for _, idx := range userIndices {
		if idx >= 0 {
			picked[idx] = true
		}
	}
	correctSet := make(map[int]bool, len(correct))
	for _, idx := range correct {
		correctSet[idx] = true
	}
	// 选中的正确项数 / 漏选数 / 多选数。
	hit := 0
	for _, idx := range correct {
		if picked[idx] {
			hit++
		} else {
			v.MissedCount++
		}
	}
	for idx := range picked {
		if !correctSet[idx] {
			v.ExtraCount++
		}
	}
	// Scoring 空 → 没拿到 correct_indices,直接判全错(没法判)。
	if s == nil || len(correct) == 0 {
		v.Score = 0
		return v
	}
	// 全对:既不漏也不多。
	if v.MissedCount == 0 && v.ExtraCount == 0 {
		v.Correct = true
		v.Score = 1
		return v
	}
	// 部分对:开了 partial_credit + 选中正确项达标 + 没有多选错项。
	if partialCredit && hit >= minForHalf && v.ExtraCount == 0 {
		v.Partial = true
		v.Score = 0.5
		return v
	}
	// 其他(漏选过多 / 多选了错项 / 全错)。
	v.Score = 0
	return v
}

// GradeAnswerV 是 GradeAnswer 的统一版,按 type 分派并返回 Verdict。
//   - choice: Correct = (answerIndex 命中), Partial=false, Score=1/0。
//   - fill:   Correct = (answerText 命中), Partial=false, Score=1/0。
//   - multi_choice: 走 GradeMultiChoice,Multi 非 nil,Partial 可能 true,Score=1/0.5/0。
//
// answerIndices 仅对 multi_choice 有意义;其余题型忽略它。
func GradeAnswerV(q model.Question, answerIndex int, answerText string, answerIndices []int) Verdict {
	switch q.Type {
	case QuestionMultiChoice:
		mv := GradeMultiChoice(q, answerIndices)
		return Verdict{Correct: mv.Correct, Partial: mv.Partial, Score: mv.Score, Multi: &mv}
	case QuestionFill:
		ok := GradeFill(q, answerText)
		score := 0.0
		if ok {
			score = 1.0
		}
		return Verdict{Correct: ok, Partial: false, Score: score}
	default: // choice / "" / unknown → choice 语义
		ok := GradeChoice(q, answerIndex)
		score := 0.0
		if ok {
			score = 1.0
		}
		return Verdict{Correct: ok, Partial: false, Score: score}
	}
}

// --- Scoring 解析 ---
//
// Question.Scoring 是按 type 分发的判分元数据 JSON。ParseScoring 把它解析成一个
// ParsedScoring,各题型字段共用一个 struct(取用方按 type 挑自己关心的字段)。
// service 层(持久化/回填)也复用这个函数,避免每个调用方各自 Unmarshal。

// ParsedScoring 是 Scoring JSON 的解析结果。ChoiceIndex/FillAccept/Multi* 各题型
// 用各自那一组;其余为零值。
//
//   - choice:       {"correct_index":2}                       → ChoiceIndex
//   - multi_choice: {"correct_indices":[0,2,3],"partial_credit":true,"min_correct_for_half":1}
//   - fill:         {"accept":["12","十二"]}                   → FillAccept
type ParsedScoring struct {
	ChoiceIndex           *int    `json:"correct_index,omitempty"`
	FillAccept            []string `json:"accept,omitempty"`
	MultiCorrectIndices   []int   `json:"correct_indices,omitempty"`
	MultiPartialCredit    bool    `json:"partial_credit"`
	MultiMinCorrectForHalf int    `json:"min_correct_for_half,omitempty"`
}

// ParseScoring 解析 q.Scoring。空/格式错时返回 nil(调用方按"无 Scoring"回退老字段)。
func ParseScoring(q model.Question) *ParsedScoring {
	if q.Scoring == "" {
		return nil
	}
	var s ParsedScoring
	if err := json.Unmarshal([]byte(q.Scoring), &s); err != nil {
		return nil
	}
	return &s
}
