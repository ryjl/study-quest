package agent

import (
	"testing"

	"studyquest/backend/internal/model"
)

func TestParseQuizGeneration(t *testing.T) {
	// A well-formed generation: 1 choice + 1 fill + feedback.
	raw := `{
		"questions": [
			{"type":"choice","chunk_index":3,"stem":"3+5=?","options":["6","7","8","9"],"answer":2,"explanation":"3+5=8"},
			{"type":"fill","chunk_index":5,"stem":"1/2+1/3=___","answer_text":["5/6","六分之五"],"explanation":"通分"}
		],
		"student_feedback":"计算基础扎实,通分需巩固"
	}`
	draft, err := parseQuizGeneration(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(draft.Questions))
	}
	if draft.Questions[0].Type != "choice" || draft.Questions[1].Type != "fill" {
		t.Errorf("types wrong: %+v", draft.Questions)
	}
	if draft.AgentFeedback != "计算基础扎实,通分需巩固" {
		t.Errorf("feedback wrong: %q", draft.AgentFeedback)
	}
}

func TestParseQuizGenerationDropsMalformed(t *testing.T) {
	// Choice with out-of-range answer, fill with no answer_text, empty stem — all dropped.
	raw := `{"questions":[
		{"type":"choice","stem":"ok","options":["a","b"],"answer":5},
		{"type":"fill","stem":"empty answers","answer_text":[]},
		{"stem":"   "},
		{"type":"choice","stem":"good","options":["a","b","c","d"],"answer":1}
	]}`
	draft, err := parseQuizGeneration(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Questions) != 1 {
		t.Errorf("expected only the 1 valid question to survive, got %d: %+v", len(draft.Questions), draft.Questions)
	}
}

func TestParseQuizGenerationDefaultsTypeToChoice(t *testing.T) {
	raw := `{"questions":[{"stem":"x","options":["a","b"],"answer":0}]}`
	draft, err := parseQuizGeneration(raw)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Questions[0].Type != "choice" {
		t.Errorf("expected default type choice, got %q", draft.Questions[0].Type)
	}
}

func TestParseQuizGenerationRejectsAllEmpty(t *testing.T) {
	raw := `{"questions":[]}`
	if _, err := parseQuizGeneration(raw); err == nil {
		t.Error("expected error for empty questions")
	}
}

func TestParseQuizGenerationHandlesFencedJSON(t *testing.T) {
	// Model wraps JSON in ```json fences.
	raw := "```json\n{\"questions\":[{\"type\":\"choice\",\"stem\":\"q\",\"options\":[\"a\",\"b\"],\"answer\":0}]}\n```"
	if _, err := parseQuizGeneration(raw); err != nil {
		t.Fatalf("fenced JSON should parse: %v", err)
	}
}

func TestResolveChunkIDs(t *testing.T) {
	chunks := []model.ContentChunk{
		{ID: 100, ChunkIndex: 3},
		{ID: 200, ChunkIndex: 5},
	}
	drafts := []QuestionDraft{
		{ChunkIndex: 3},
		{ChunkIndex: 5},
		{ChunkIndex: 99}, // not found → 0
		{ChunkIndex: 0},  // synthetic → not in map
	}
	m := ResolveChunkIDs(drafts, chunks)
	if m[3] != 100 {
		t.Errorf("chunk 3 → %d, want 100", m[3])
	}
	if m[5] != 200 {
		t.Errorf("chunk 5 → %d, want 200", m[5])
	}
	if m[99] != 0 {
		t.Errorf("missing chunk should map to 0, got %d", m[99])
	}
	if _, ok := m[0]; ok {
		t.Error("synthetic (index 0) should not be in map")
	}
}
