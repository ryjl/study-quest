package model

import (
	"strings"
	"testing"
)

// TestEffectiveHints_Priority 测试 Effective* 系列的"Course > Subject"两层优先级。
// EffectiveWhisperHint/EffectiveQuizHint 只有两层 fallback(无第三层)。本测试锁定
// 两层行为:
//   - Course 配了 → 用 Course 的(优先)
//   - Course 没配 + Subject 配了 → 用 Subject 的(兜底)
//   - 都没配 → 返回空
//
// 这几个方法是 AI 调用链的起点(prompt 拼装读它们),优先级错了会直接导致 prompt 用错
// 提示词,值得测。
func TestEffectiveHints_Priority(t *testing.T) {
	// 准备带 AIConfig 的 Course 和 Subject。
	courseWith := Course{}
	courseWith.SetAIConfig(AIConfig{
		WhisperHint: "course-whisper", SummaryHint: "course-summary",
		QuizHint: "course-quiz", AdviceHint: "course-advice", TermDict: "course-dict",
	})
	subjectWith := Subject{}
	subjectWith.SetAIConfig(AIConfig{
		WhisperHint: "subject-whisper", SummaryHint: "subject-summary",
		QuizHint: "subject-quiz", AdviceHint: "subject-advice", TermDict: "subject-dict",
	})
	emptyCourse := Course{}
	emptySubject := Subject{}

	// Case 1: Course 配了 → Course 优先,忽略 Subject。
	if got := courseWith.EffectiveWhisperHint(subjectWith); got != "course-whisper" {
		t.Errorf("EffectiveWhisperHint course-set = %q, want course-whisper", got)
	}
	if got := courseWith.EffectiveQuizHint(subjectWith); got != "course-quiz" {
		t.Errorf("EffectiveQuizHint course-set = %q, want course-quiz", got)
	}
	if got := courseWith.EffectiveSummaryHint(subjectWith); got != "course-summary" {
		t.Errorf("EffectiveSummaryHint course-set = %q, want course-summary", got)
	}
	if got := courseWith.EffectiveAdviceHint(subjectWith); got != "course-advice" {
		t.Errorf("EffectiveAdviceHint course-set = %q, want course-advice", got)
	}

	// Case 2: Course 没配 + Subject 配了 → 回退到 Subject。
	if got := emptyCourse.EffectiveWhisperHint(subjectWith); got != "subject-whisper" {
		t.Errorf("EffectiveWhisperHint subject-fallback = %q, want subject-whisper", got)
	}
	if got := emptyCourse.EffectiveQuizHint(subjectWith); got != "subject-quiz" {
		t.Errorf("EffectiveQuizHint subject-fallback = %q, want subject-quiz", got)
	}

	// Case 3: 都没配 → 空串(不再有 AIHint 第三层兜底)。
	if got := emptyCourse.EffectiveWhisperHint(emptySubject); got != "" {
		t.Errorf("EffectiveWhisperHint both-empty = %q, want empty", got)
	}
	if got := emptyCourse.EffectiveQuizHint(emptySubject); got != "" {
		t.Errorf("EffectiveQuizHint both-empty = %q, want empty", got)
	}
}

// TestEffectiveTermDict_Merge 锁定 term_dict 的"合并"语义(不是覆盖)。
// 课程级 term_dict 追加到学科级后面,两者都生效(课程可能有学科通用之外的专有术语)。
func TestEffectiveTermDict_Merge(t *testing.T) {
	courseWith := Course{}
	courseWith.SetAIConfig(AIConfig{TermDict: "course-dict"})
	subjectWith := Subject{}
	subjectWith.SetAIConfig(AIConfig{TermDict: "subject-dict"})
	emptyCourse := Course{}
	emptySubject := Subject{}

	// 两者都有 → 合并(subject 在前,course 追加)。
	got := courseWith.EffectiveTermDict(subjectWith)
	if !strings.Contains(got, "subject-dict") || !strings.Contains(got, "course-dict") {
		t.Errorf("EffectiveTermDict both-set = %q, want merged subject+course", got)
	}
	// 只有 course → 返回 course 的。
	if got := courseWith.EffectiveTermDict(emptySubject); got != "course-dict" {
		t.Errorf("EffectiveTermDict course-only = %q, want course-dict", got)
	}
	// 只有 subject → 返回 subject 的。
	if got := emptyCourse.EffectiveTermDict(subjectWith); got != "subject-dict" {
		t.Errorf("EffectiveTermDict subject-only = %q, want subject-dict", got)
	}
	// 都没 → 空。
	if got := emptyCourse.EffectiveTermDict(emptySubject); got != "" {
		t.Errorf("EffectiveTermDict both-empty = %q, want empty", got)
	}
}
