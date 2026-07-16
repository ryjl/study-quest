package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// quizzer.go orchestrates quiz generation: it runs the ReAct agent loop with
// the quiz tools, parses the model's structured answer (choice + fill questions
// + student feedback), and runs a self-check pass to catch bad questions before
// they reach the student.
//
// The flow (mirrors docs/handoff-ai-step3.md §出题决策流):
//   1. Gather context: the agent loop calls tools (episode info, search,
//      mastery) as it sees fit — we don't force a fixed sequence, that's the
//      point of ReAct. We DO pre-seed the prompt with mastery + episode meta so
//      the model isn't blind on turn 1.
//   2. Parse the final answer into []QuestionDraft + AgentFeedback.
//   3. Self-check: a second, tool-free LLM call validates the questions. On
//      fail, regenerate ONCE (bounded — no infinite loop).
//   4. Return QuizDraft{Questions, Feedback, SelfCheck result} + a trace.
//
// All of this is recorded to ai_runs by the caller (the service worker): one
// run for the main agent loop (with the full trace), one for the self-check.

// QuizzerRequest carries the inputs to one quiz generation.
type QuizzerRequest struct {
	EpisodeID    uint
	CourseID     uint
	UserID       uint
	EpisodeTitle string
	Subject      string // e.g. "数学" — gates whether fill questions are sensible
	FileName     string // episode file name, often topic-bearing
}

// QuestionDraft is the parsed form of one question from the model's JSON, before
// it's persisted as a model.Question. Normalized so the repo layer can't
// receive a half-formed question.
type QuestionDraft struct {
	Type        string   `json:"type"`         // "choice" | "fill"; defaults to choice
	ChunkIndex  int      `json:"chunk_index"`  // resolved to a ChunkID at persist time; 0/absent = synthetic
	Stem        string   `json:"stem"`         // required
	Options     []string `json:"options"`      // choice only
	Answer      int      `json:"answer"`       // choice: 0-based index
	AnswerText  []string `json:"answer_text"`  // fill: acceptable answers
	Explanation string   `json:"explanation"`  // shown after answering
}

// QuizDraft is the full result of one generation: the questions + the LLM's
// analysis of the student (agent_feedback) + the self-check outcome.
type QuizDraft struct {
	Questions       []QuestionDraft
	AgentFeedback   string
	SelfCheckResult string // pass | fail | skipped
	SelfCheckNote   string
}

// quizGenerationResponse is the JSON the agent is asked to emit.
type quizGenerationResponse struct {
	Questions       []QuestionDraft `json:"questions"`
	StudentFeedback string          `json:"student_feedback"`
}

// Quizzer generates one adaptive quiz for a student using the ReAct agent loop.
type Quizzer struct {
	agent     *Agent            // the ReAct engine (with quiz tools)
	selfCheck *Agent            // tool-free judge (same LLM, no tools)
	memory    *MemoryStore      // to pre-seed the prompt with weak points
	deps      ToolDeps          // for mastery summary + episode title
	llm       ai.LLMProvider    // raw access for the self-check pass
	model     string            // model name
}

// NewQuizzer builds a Quizzer. agentLoop must have the quiz Toolbox wired;
// selfCheckLoop should have a nil/empty toolbox (it judges without tools).
func NewQuizzer(agentLoop, selfCheckLoop *Agent, memory *MemoryStore, deps ToolDeps, llm ai.LLMProvider, modelName string) *Quizzer {
	return &Quizzer{agent: agentLoop, selfCheck: selfCheckLoop, memory: memory, deps: deps, llm: llm, model: modelName}
}

// QuizResult is what Generate returns to the service layer: the parsed draft
// plus the agent trace (for ai_runs) and aggregated usage (cost tracking).
type QuizResult struct {
	Draft        QuizDraft
	Trace        []TraceStep
	Usage        ai.Usage // summed across generation + any self-check calls
	Turns        int
	RawFinalText string // the unparsed final answer when parsing failed (diagnostics); empty on success
}

// Generate runs the full quiz-generation flow for one (user, episode).
func (q *Quizzer) Generate(ctx context.Context, req QuizzerRequest) (*QuizResult, error) {
	// ── Pre-seed: gather the student's mastery summary for the prompt ──
	// The model can (and should) call get_user_mastery itself, but giving it the
	// headline weaknesses up front saves a round-trip and frames its thinking.
	masterySummary := q.buildMasterySummary(ctx, req.UserID, req.EpisodeID)

	// ── 1. Agent loop (with tools) ──
	userPrompt := buildQuizUserPrompt(req, masterySummary)
	agentRes, err := q.agent.Run(ctx, QuizzerSystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("quizzer: agent loop: %w", err)
	}
	trace := agentRes.Trace
	usage := agentRes.Usage

	// ── 2. Parse the final answer ──
	draft, err := parseQuizGeneration(agentRes.FinalText)
	if err != nil {
		// Capture the raw final text on the result so the caller can log it for
		// debugging (a parse failure usually means the model truncated or wrapped
		// the JSON — seeing the tail is essential to diagnose).
		return &QuizResult{Trace: trace, Usage: usage, Turns: agentRes.Turns, RawFinalText: agentRes.FinalText},
			fmt.Errorf("quizzer: parse generation: %w", err)
	}

	// ── 3. Self-check (tool-free) ──
	selfCheck, scUsage, scErr := q.runSelfCheck(ctx, req, draft)
	usage.PromptTokens += scUsage.PromptTokens
	usage.CompletionTokens += scUsage.CompletionTokens
	if scErr != nil {
		// Self-check failed to run (network) — record as skipped, keep the draft.
		// Surfacing the questions is better than dropping them; the admin sees
		// self_check=skipped and can investigate.
		draft.SelfCheckResult = "skipped"
		draft.SelfCheckNote = "self-check call failed: " + scErr.Error()
	} else {
		draft.SelfCheckResult = selfCheck.Result
		draft.SelfCheckNote = selfCheck.Note
		// On fail, regenerate ONCE (bounded — no infinite regen loop).
		if selfCheck.Result == "fail" {
			regen, regenErr := q.regenerate(ctx, req, masterySummary, selfCheck.Note)
			if regenErr == nil {
				// Append the regeneration's trace + usage, prefer the new draft.
				trace = append(trace, regen.Trace...)
				usage.PromptTokens += regen.Usage.PromptTokens
				usage.CompletionTokens += regen.Usage.CompletionTokens
				draft.Questions = regen.Draft.Questions
				draft.AgentFeedback = regen.Draft.AgentFeedback
				// Honest status: we regenerated but did NOT re-run self-check on
				// the new draft (one retry, no second check pass). Label it so the
				// admin can see "first attempt failed, regenerated" rather than a
				// misleading "pass". A future iteration could re-check the regen.
				draft.SelfCheckResult = "regenerated"
				draft.SelfCheckNote = "首次审核未通过,已根据反馈重新生成(未对重新生成的题目二次审核): " + selfCheck.Note
			}
			// If regeneration also failed, keep the original draft with fail status.
		}
	}

	return &QuizResult{Draft: draft, Trace: trace, Usage: usage, Turns: agentRes.Turns}, nil
}

// buildMasterySummary produces a short text describing the student's weak points
// for the prompt. Empty for a new student (no answer history yet).
func (q *Quizzer) buildMasterySummary(ctx context.Context, userID, episodeID uint) string {
	rows, err := q.memory.Masteries(ctx, userID, episodeID)
	if err != nil || len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range rows {
		// Only surface weaknesses worth targeting; mastery >= 0.8 is solid.
		if r.Mastery >= 0.8 {
			continue
		}
		fmt.Fprintf(&b, "- 知识点片段#%d: mastery=%.2f(对%d 错%d)\n", r.ChunkID, r.Mastery, r.CorrectCount, r.WrongCount)
	}
	return strings.TrimRight(b.String(), "\n")
}

// runSelfCheck asks the tool-free judge to validate the draft. Returns the
// pass/fail + note. On LLM error, returns a non-nil error so Generate can mark
// self_check=skipped (rather than dropping the quiz).
func (q *Quizzer) runSelfCheck(ctx context.Context, req QuizzerRequest, draft QuizDraft) (selfCheckOutcome, ai.Usage, error) {
	outcome := selfCheckOutcome{Result: "skipped"}
	// Build a compact representation of the questions for the judge.
	questionsJSON, _ := json.Marshal(draft.Questions)
	userPrompt := fmt.Sprintf("课时: %s (%s)\n\n待审核题目:\n%s\n\n请逐题检查质量。", req.EpisodeTitle, req.FileName, string(questionsJSON))

	res, err := q.selfCheck.Run(ctx, QuizSelfCheckPrompt, userPrompt)
	if err != nil {
		return outcome, ai.Usage{}, err
	}
	parsed := struct {
		Pass bool   `json:"pass"`
		Note string `json:"note"`
	}{}
	if err := json.Unmarshal([]byte(extractJSONObject(res.FinalText)), &parsed); err != nil {
		// Couldn't parse the verdict — treat as skipped (judge spoke gibberish).
		outcome.Result = "skipped"
		outcome.Note = "self-check response unparseable"
		return outcome, res.Usage, nil
	}
	if parsed.Pass {
		outcome.Result = "pass"
	} else {
		outcome.Result = "fail"
	}
	outcome.Note = parsed.Note
	return outcome, res.Usage, nil
}

type selfCheckOutcome struct {
	Result string // pass | fail | skipped
	Note   string
}

// regenerate runs the agent loop a second time after a self-check failure,
// folding the checker's note into the prompt so the model corrects the noted
// problems. Bounded to ONE retry (Generate calls this at most once).
func (q *Quizzer) regenerate(ctx context.Context, req QuizzerRequest, masterySummary, checkNote string) (*QuizResult, error) {
	userPrompt := buildQuizUserPrompt(req, masterySummary)
	userPrompt += "\n\n⚠️ 上一版题目审核未通过,请修正以下问题后重新出题:\n" + checkNote + "\n"
	res, err := q.agent.Run(ctx, QuizzerSystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	draft, err := parseQuizGeneration(res.FinalText)
	if err != nil {
		return nil, err
	}
	return &QuizResult{Draft: draft, Trace: res.Trace, Usage: res.Usage, Turns: res.Turns}, nil
}

// parseQuizGeneration decodes the agent's final JSON into questions + feedback.
// Defensive: defaults type to choice, drops questions missing a stem, and
// ensures choice questions have a sane answer index. The model isn't perfectly
// reliable, so we'd rather drop a malformed question than persist garbage.
func parseQuizGeneration(raw string) (QuizDraft, error) {
	var resp quizGenerationResponse
	if err := json.Unmarshal([]byte(extractJSONObject(raw)), &resp); err != nil {
		return QuizDraft{}, fmt.Errorf("invalid quiz JSON: %w", err)
	}
	cleaned := make([]QuestionDraft, 0, len(resp.Questions))
	for _, qd := range resp.Questions {
		if strings.TrimSpace(qd.Stem) == "" {
			continue // drop empty
		}
		if qd.Type == "" {
			qd.Type = QuestionChoice
		}
		// Validate choice questions: need options + in-range answer.
		if qd.Type == QuestionChoice {
			if len(qd.Options) < 2 {
				continue // unusable choice question
			}
			if qd.Answer < 0 || qd.Answer >= len(qd.Options) {
				continue // bad answer index
			}
		}
		// Validate fill questions: need at least one acceptable answer.
		if qd.Type == QuestionFill {
			if len(qd.AnswerText) == 0 {
				continue // unusable fill question
			}
		}
		cleaned = append(cleaned, qd)
	}
	if len(cleaned) == 0 {
		return QuizDraft{}, fmt.Errorf("no valid questions in generation")
	}
	return QuizDraft{
		Questions:     cleaned,
		AgentFeedback: strings.TrimSpace(resp.StudentFeedback),
	}, nil
}

// ResolveChunkIDs maps each question's ChunkIndex (the model-friendly handle)
// to a real ContentChunk.ID for persistence (Question.ChunkID). Questions whose
// ChunkIndex doesn't match any chunk (or are synthetic, index 0) get ChunkID=0.
// Returns the drafts with ChunkIndex preserved for the service layer's use.
func ResolveChunkIDs(drafts []QuestionDraft, chunks []model.ContentChunk) map[int]uint {
	byIndex := make(map[int]uint, len(chunks))
	for _, c := range chunks {
		byIndex[c.ChunkIndex] = c.ID
	}
	out := make(map[int]uint, len(drafts))
	for _, d := range drafts {
		if d.ChunkIndex > 0 {
			out[d.ChunkIndex] = byIndex[d.ChunkIndex] // 0 if not found
		}
	}
	return out
}
