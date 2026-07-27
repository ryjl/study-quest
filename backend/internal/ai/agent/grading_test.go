package agent

import (
	"testing"

	"studyquest/backend/internal/model"
)

func TestGradeChoice(t *testing.T) {
	q := model.Question{Type: QuestionChoice, Scoring: `{"correct_index":2}`}
	if !GradeChoice(q, 2) {
		t.Error("correct index should pass")
	}
	if GradeChoice(q, 0) {
		t.Error("wrong index should fail")
	}
	if GradeChoice(q, 99) {
		t.Error("out-of-range should fail")
	}
	if GradeChoice(q, -1) {
		t.Error("negative should fail")
	}
}

func TestGradeFill(t *testing.T) {
	// multiple acceptable answers (Scoring.accept)
	q := model.Question{
		Type:    QuestionFill,
		Scoring: `{"accept":["12","十二"]}`,
	}
	cases := []struct {
		in   string
		want bool
	}{
		{"12", true},
		{"十二", true},
		{" 12 ", true},  // whitespace
		{"１２", true},  // full-width
		{"12。", true},  // punctuation
		{"11", false},   // wrong (exact, not fuzzy)
		{"", false},     // blank
		{"  ", false},   // whitespace-only
		{"十二三", false}, // wrong CJK
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := GradeFill(q, c.in); got != c.want {
				t.Errorf("GradeFill(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestGradeFillMalformedScoring:Scoring 是损坏 JSON 时,GradeFill 应判错不 panic
// (以前测 AnswerText 损坏,2026-07-27 删 AnswerText 列后改成测 Scoring 损坏)。
func TestGradeFillMalformedScoring(t *testing.T) {
	q := model.Question{Type: QuestionFill, Scoring: "not json"}
	if GradeFill(q, "anything") {
		t.Error("malformed scoring should grade as wrong, not panic")
	}
}

// TestGradeFillEmptyScoring:Scoring 为空时,没有可接受答案,GradeFill 应判错。
func TestGradeFillEmptyScoring(t *testing.T) {
	q := model.Question{Type: QuestionFill, Scoring: ""}
	if GradeFill(q, "anything") {
		t.Error("empty scoring should grade as wrong")
	}
}

func TestGradeAnswerDispatch(t *testing.T) {
	choiceQ := model.Question{Type: QuestionChoice, Scoring: `{"correct_index":1}`}
	fillQ := model.Question{Type: QuestionFill, Scoring: `{"accept":["42"]}`}

	if !GradeAnswer(choiceQ, 1, "ignored") {
		t.Error("choice should grade by index")
	}
	if !GradeAnswer(fillQ, 0, "42") {
		t.Error("fill should grade by text")
	}
	if GradeAnswer(fillQ, 0, "wrong") {
		t.Error("fill wrong text should fail")
	}

	// empty/unknown type defaults to choice grading
	unknownQ := model.Question{Type: "", Scoring: `{"correct_index":3}`}
	if !GradeAnswer(unknownQ, 3, "") {
		t.Error("empty type should default to choice")
	}
}

// TestChoiceCorrectIndex_NoScoring 锁定"无 Scoring 的 choice 题返回 0"这个行为。
// 2026-07-27 删 Answer 列后,choiceCorrectIndex 不再回退老字段,而是返回 0(第一项)。
// 这依赖于"所有 question 都有 Scoring"的不变式(清数据重部署后,runQuizJob 总写 Scoring)。
// 如果未来有新代码路径创建无 Scoring 的 choice 题,这里会静默判"选 A 对"——本测试
// 显式记录该行为,提醒未来维护者这个不变式。
func TestChoiceCorrectIndex_NoScoring(t *testing.T) {
	// 无 Scoring → 返回 0(不 panic、不报错)。这是有意为之的防御性默认,
	// 上游应保证所有题都有 Scoring(否则会误判)。
	q := model.Question{Type: QuestionChoice, Scoring: ""}
	if got := choiceCorrectIndex(q); got != 0 {
		t.Errorf("choiceCorrectIndex with empty Scoring = %d, want 0 (defensive default)", got)
	}
	// 损坏 Scoring → ParseScoring 返回 nil → 同样返回 0。
	q2 := model.Question{Type: QuestionChoice, Scoring: "not json"}
	if got := choiceCorrectIndex(q2); got != 0 {
		t.Errorf("choiceCorrectIndex with malformed Scoring = %d, want 0", got)
	}
	// 正常 Scoring → 返回 correct_index。
	q3 := model.Question{Type: QuestionChoice, Scoring: `{"correct_index":2}`}
	if got := choiceCorrectIndex(q3); got != 2 {
		t.Errorf("choiceCorrectIndex with valid Scoring = %d, want 2", got)
	}
}

// TestFillAcceptableAnswers_NoScoring 锁定"无 Scoring 的 fill 题返回 nil"。
// 这是安全方向(没可接受答案 → 判错),不像 choice 返回 0 有误判风险。
func TestFillAcceptableAnswers_NoScoring(t *testing.T) {
	q := model.Question{Type: QuestionFill, Scoring: ""}
	if got := fillAcceptableAnswers(q); got != nil {
		t.Errorf("fillAcceptableAnswers with empty Scoring = %v, want nil", got)
	}
	q2 := model.Question{Type: QuestionFill, Scoring: "not json"}
	if got := fillAcceptableAnswers(q2); got != nil {
		t.Errorf("fillAcceptableAnswers with malformed Scoring = %v, want nil", got)
	}
}

// TestGradeMultiChoice 覆盖多选题判分的全路径。这块原本完全没测试——清债改动
// (删 Answer 回退)虽然没直接动多选题逻辑,但多选题判分是判分系统的核心,补测试防回归。
func TestGradeMultiChoice(t *testing.T) {
	// 3 个正确项 [0,2,4],允许部分对,选对 ≥2 个(且无多选)给半分。
	q := model.Question{
		Type: QuestionMultiChoice,
		Scoring: `{"correct_indices":[0,2,4],"partial_credit":true,"min_correct_for_half":2}`,
	}
	cases := []struct {
		name    string
		picks   []int
		correct bool
		score   float64
	}{
		{"全对", []int{0, 2, 4}, true, 1.0},
		{"全对(无序)", []int{4, 0, 2}, true, 1.0},
		{"部分对-够半分(选2对、无多选)", []int{0, 2}, false, 0.5},
		{"部分对-不够半分(只选1对)", []int{0}, false, 0},
		{"部分对但有多选错项(不给半分)", []int{0, 2, 1}, false, 0},
		{"全错", []int{1, 3}, false, 0},
		{"空选", []int{}, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := GradeMultiChoice(q, c.picks)
			if v.Correct != c.correct {
				t.Errorf("Correct = %v, want %v", v.Correct, c.correct)
			}
			if v.Score != c.score {
				t.Errorf("Score = %v, want %v", v.Score, c.score)
			}
		})
	}
}

// TestGradeMultiChoice_NoScoring:无 Scoring 的多选题判全错(安全方向)。
func TestGradeMultiChoice_NoScoring(t *testing.T) {
	q := model.Question{Type: QuestionMultiChoice, Scoring: ""}
	v := GradeMultiChoice(q, []int{0, 1})
	if v.Correct {
		t.Error("multi_choice with empty Scoring should grade as wrong")
	}
	if v.Score != 0 {
		t.Errorf("Score = %v, want 0", v.Score)
	}
}
