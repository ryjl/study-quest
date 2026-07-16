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
	QuestionChoice = "choice"
	QuestionFill   = "fill"
)

// GradeChoice returns whether the user's selected option index is the correct
// one. Out-of-range indices are simply wrong (defensive — the client should
// never send one, but grading must never panic).
func GradeChoice(q model.Question, userIndex int) bool {
	if q.Type != "" && q.Type != QuestionChoice {
		return false // not a choice question
	}
	return userIndex == q.Answer && userIndex >= 0
}

// GradeFill returns whether the user's free-text answer matches one of the
// question's acceptable answers, after normalization. The acceptable answers
// are stored on q.AnswerText as a JSON []string (multiple equivalent forms, e.g.
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
	accept, err := parseAcceptableAnswers(q.AnswerText)
	if err != nil || len(accept) == 0 {
		return false
	}
	for _, a := range accept {
		if ai.NormalizeText(a) == normalized {
			return true
		}
	}
	return false
}

// GradeAnswer dispatches on question type. answerIndex is used for choice,
// answerText for fill. The unused argument is ignored.
func GradeAnswer(q model.Question, answerIndex int, answerText string) bool {
	if q.Type == QuestionFill {
		return GradeFill(q, answerText)
	}
	return GradeChoice(q, answerIndex) // default to choice for "" / unknown
}

// parseAcceptableAnswers decodes the JSON []string on Question.AnswerText. Empty
// input → empty slice (no acceptable answers, treated as ungradeable → wrong).
func parseAcceptableAnswers(answerTextJSON string) ([]string, error) {
	if answerTextJSON == "" {
		return nil, nil
	}
	var accept []string
	if err := json.Unmarshal([]byte(answerTextJSON), &accept); err != nil {
		return nil, err
	}
	return accept, nil
}
