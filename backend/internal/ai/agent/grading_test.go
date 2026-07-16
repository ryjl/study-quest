package agent

import (
	"testing"

	"studyquest/backend/internal/model"
)

func TestGradeChoice(t *testing.T) {
	q := model.Question{Type: QuestionChoice, Answer: 2}
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
	// multiple acceptable answers
	q := model.Question{
		Type:       QuestionFill,
		AnswerText: `["12","十二"]`,
	}
	cases := []struct {
		in   string
		want bool
	}{
		{"12", true},
		{"十二", true},
		{" 12 ", true},      // whitespace
		{"１２", true},      // full-width
		{"12。", true},      // punctuation
		{"11", false},       // wrong (exact, not fuzzy)
		{"", false},         // blank
		{"  ", false},       // whitespace-only
		{"十二三", false},    // wrong CJK
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := GradeFill(q, c.in); got != c.want {
				t.Errorf("GradeFill(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestGradeFillMalformedAnswerText(t *testing.T) {
	q := model.Question{Type: QuestionFill, AnswerText: "not json"}
	if GradeFill(q, "anything") {
		t.Error("malformed answer_text should grade as wrong, not panic")
	}
}

func TestGradeFillEmptyAnswerText(t *testing.T) {
	q := model.Question{Type: QuestionFill, AnswerText: ""}
	if GradeFill(q, "anything") {
		t.Error("empty answer_text should grade as wrong")
	}
}

func TestGradeAnswerDispatch(t *testing.T) {
	choiceQ := model.Question{Type: QuestionChoice, Answer: 1}
	fillQ := model.Question{Type: QuestionFill, AnswerText: `["42"]`}

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
	unknownQ := model.Question{Type: "", Answer: 3}
	if !GradeAnswer(unknownQ, 3, "") {
		t.Error("empty type should default to choice")
	}
}
