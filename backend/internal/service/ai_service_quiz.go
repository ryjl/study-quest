package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
)

// ai_service_quiz.go holds the Phase C quiz orchestration: the worker job that
// runs the agent, the client-facing lazy-generation + answering flow, and the
// admin observability reads. Split from ai_service.go so each capability's
// wiring stays scannable.
//
// The agent package owns the decision logic (the ReAct loop, tools, grading);
// this file is the GLUE: it resolves providers, builds the agent, persists
// results, and enforces the access/AI-enabled gates. It never makes a decision
// the agent should make.

// --- client-facing view types ---

// QuizView is the client-safe quiz payload. Questions omit the correct answer
// (revealed post-submit); each carries a chunk_start_time for video-jump. The
// agent_feedback (LLM's study advice) is included so the student sees it.
type QuizView struct {
	QuizID        uint                `json:"quiz_id"`
	EpisodeID     uint                `json:"episode_id"`
	Difficulty    string              `json:"difficulty"`
	AgentFeedback string              `json:"agent_feedback,omitempty"`
	Questions     []QuizViewQuestion  `json:"questions"`
	AnsweredCount int                 `json:"answered_count"`
}

// QuizViewQuestion is one question as served to the client. No answer field —
// the correct index/text is stripped here and only returned via submit.
type QuizViewQuestion struct {
	ID            uint     `json:"id"`
	Type          string   `json:"type"`
	Stem          string   `json:"stem"`
	Options       []string `json:"options,omitempty"`        // choice only
	ChunkStartTime *int    `json:"chunk_start_time,omitempty"` // seconds, for video-jump; nil if synthetic
	Answered      bool     `json:"answered"`
}

// AnswerResult is the response to a submit. Reveals correctness, the correct
// answer, the explanation, and the jump-to-video time.
type AnswerResult struct {
	Correct        bool   `json:"correct"`
	CorrectIndex   *int   `json:"correct_index,omitempty"`    // choice: the right option index
	CorrectText    string `json:"correct_text,omitempty"`     // fill: the canonical answer(s)
	Explanation    string `json:"explanation"`
	ChunkStartTime *int   `json:"chunk_start_time,omitempty"` // seconds, for "[跳转 12:38]"
}

// QuizDetail is the admin observability view of one quiz: the questions WITH
// answers (admin sees everything), the student's answer history, their memory,
// and the agent's feedback. This is the per-user drill-down on the AI user view.
//
// All fields carry explicit snake_case JSON tags: the underlying model.* types
// marshal PascalCase (GORM default), but the admin SPA expects snake_case
// (matching every other admin endpoint). The admin_ai handler wraps these into
// the final JSON, so the contract is stable regardless of model tag changes.
type QuizDetail struct {
	Quiz      QuizDetailQuiz      `json:"quiz"`
	Questions []QuizDetailQuestion `json:"questions"`
	Answers   []QuizDetailAnswer  `json:"answers"`
	Masteries []QuizDetailMastery `json:"masteries"`
	Runs      []model.AIRun       `json:"runs"` // the ai_runs that generated this quiz (trace lives here). AIRun already has JSON tags? — no, but it's read directly by the existing AIWorkflow page which already tolerates PascalCase via the AiRun TS type mapping. Kept as-is for consistency with that page.
}

// QuizDetailQuiz is the quiz row in admin-snake_case.
type QuizDetailQuiz struct {
	ID            uint   `json:"id"`
	EpisodeID     uint   `json:"episode_id"`
	UserID        uint   `json:"user_id"`
	CourseID      uint   `json:"course_id"`
	Difficulty    string `json:"difficulty"`
	AgentFeedback string `json:"agent_feedback"`
	CreatedAt     string `json:"created_at"`
}

// QuizDetailQuestion is a question with its answer exposed (admin view only) +
// the joined video-jump time.
type QuizDetailQuestion struct {
	ID             uint   `json:"id"`
	Type           string `json:"type"`
	ChunkID        uint   `json:"chunk_id"`
	Stem           string `json:"stem"`
	Options        string `json:"options"`         // JSON []string (choice)
	Answer         int    `json:"answer"`          // choice: 0-based index
	AnswerText     string `json:"answer_text"`     // fill: JSON []string
	Explanation    string `json:"explanation"`
	ChunkStartTime *int   `json:"chunk_start_time,omitempty"`
}

// QuizDetailAnswer is one student-answer row (append-only history).
type QuizDetailAnswer struct {
	ID         int      `json:"id"`
	QuestionID uint     `json:"question_id"`
	UserID     uint     `json:"user_id"`
	UserAnswer int      `json:"user_answer"`
	Correct    bool     `json:"correct"`
	AnsweredAt string   `json:"answered_at"`
}

// QuizDetailMastery is one per-chunk memory row.
type QuizDetailMastery struct {
	ChunkID      uint    `json:"chunk_id"`
	Mastery      float64 `json:"mastery"`
	CorrectCount int     `json:"correct_count"`
	WrongCount   int     `json:"wrong_count"`
}

// --- status constants for GetOrEnqueueQuiz ---

const (
	quizStatusReady       = "ready"
	quizStatusGenerating  = "generating"
	quizStatusUnavailable = "unavailable"
)

// --- ToolDeps adapter ---
//
// The agent's ToolDeps interface has a no-sourceType ListChunks; the repo's has
// a sourceType param. This adapter bridges them, scoping to subtitle chunks
// (the only source today). It also implements agent.MemoryRepo (the repo's
// mastery methods match). Keeping the adapter here (not in the agent package)
// means the agent never imports the repo — it stays independently testable.

type agentToolDeps struct {
	contentRepo interface {
		ListChunks(episodeID uint, sourceType string) ([]model.ContentChunk, error)
		GetSummary(episodeID uint) (*model.AISummary, error)
	}
	episodeRepo agentEpisodeLoader
	courseRepo  agentCourseLoader
}

type agentEpisodeLoader interface {
	FindByID(id uint) (*model.Episode, error)
}
type agentCourseLoader interface {
	FindByID(id uint) (*model.Course, error)
}

func (d *agentToolDeps) ListChunks(episodeID uint) ([]model.ContentChunk, error) {
	return d.contentRepo.ListChunks(episodeID, "subtitle")
}
func (d *agentToolDeps) GetEpisode(episodeID uint) (*model.Episode, error) {
	return d.episodeRepo.FindByID(episodeID)
}
func (d *agentToolDeps) GetCourse(courseID uint) (*model.Course, error) {
	return d.courseRepo.FindByID(courseID)
}
func (d *agentToolDeps) GetSummary(episodeID uint) (*model.AISummary, error) {
	return d.contentRepo.GetSummary(episodeID)
}

// Compile-time: agentToolDeps satisfies the agent.ToolDeps interface.
var _ agent.ToolDeps = (*agentToolDeps)(nil)

// --- the worker job: runs the agent and persists the quiz ---

// runQuizJob is the generation path. Triggered lazily by GetOrEnqueueQuiz (or,
// in future, an admin bulk prewarm). It:
//  1. Resolves chat + embedding providers (both required: chat for generation,
//     embedding for the search tool).
//  2. Builds the agent toolbox + memory store + quizzer.
//  3. Runs Generate (ReAct loop + self-check).
//  4. Persists the quiz + questions (CreateQuiz replaces any existing).
//  5. Records ai_runs (the main loop WITH trace_json + the self-check).
//
// job.UserID MUST be set (quiz jobs are per-user). A quiz job without a user is
// a bug — skipped, not crashed.
func (s *aiService) runQuizJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "AI not configured (no resolver)", nil)
		return
	}
	if job.UserID == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "quiz job missing user_id", nil)
		return
	}
	userID := *job.UserID

	// Both providers are needed: chat for generation, embedding for search.
	llm, err := s.resolver.ResolveChat()
	if err != nil {
		s.failJob(job, "resolve chat provider: "+err.Error())
		return
	}
	emb, err := s.resolver.ResolveEmbedder()
	if err != nil {
		s.failJob(job, "resolve embedding provider: "+err.Error())
		return
	}
	modelName := s.resolver.ChatModelName()

	// Episode + course context for the prompt + chunk-id resolution.
	ep, err := s.episodeRepo.FindByID(job.EpisodeID)
	if err != nil || ep == nil {
		s.failJob(job, "load episode: "+err.Error())
		return
	}
	course, _ := s.courseRepo.FindByID(job.CourseID)
	subject := ""
	if course != nil {
		subject = course.Subject.Label
	}

	// Build the agent graph: deps adapter → memory → toolbox → agents → quizzer.
	deps := &agentToolDeps{contentRepo: s.contentRepo, episodeRepo: s.episodeRepo, courseRepo: s.courseRepo}
	memory := agent.NewMemoryStore(s.contentRepo) // contentRepo implements agent.MemoryRepo
	toolbox := agent.NewQuizToolbox(deps, memory, emb, job.EpisodeID, userID, job.CourseID)
	// MaxTokens is generous on the generation turn: the final answer is a
	// multi-question quiz JSON with per-question explanations, which runs
	// 1500-2500 tokens. Without an explicit cap the relay/model default can be
	// small (we saw ~1197-token truncation), cutting the JSON mid-generation and
	// breaking parsing. 4000 leaves comfortable headroom.
	genAgent := agent.NewAgent(llm, modelName, toolbox, agent.AgentOpts{MaxSteps: 6, MaxTokens: 4000})
	checkAgent := agent.NewAgent(llm, modelName, nil, agent.AgentOpts{MaxSteps: 1, MaxTokens: 800}) // self-check: short verdict
	quizzer := agent.NewQuizzer(genAgent, checkAgent, memory, deps, llm, modelName)

	start := time.Now()
	res, err := quizzer.Generate(ctx, agent.QuizzerRequest{
		EpisodeID:    job.EpisodeID,
		CourseID:     job.CourseID,
		UserID:       userID,
		EpisodeTitle: ep.Title,
		Subject:      subject,
		FileName:     filepath.Base(ep.VideoRelativePath),
	})
	elapsed := time.Since(start)

	if err != nil {
		// Still record what we tried: the partial trace is valuable for debugging
		// a generation failure (did the agent loop die? did parsing fail?).
		if res != nil {
			note := err.Error()
			// If parsing failed, append the raw final text tail so the admin can
			// see WHY (truncation? extra content? fence wrapping?).
			if res.RawFinalText != "" {
				tail := res.RawFinalText
				if len(tail) > 800 {
					tail = "..." + tail[len(tail)-800:]
				}
				note = fmt.Sprintf("%s | raw tail: %s", note, tail)
			}
			s.recordQuizRun(job.ID, modelName, res, elapsed, "fail", note)
		}
		s.failJob(job, "quiz generation: "+err.Error())
		return
	}

	// Resolve chunk_index → chunk_id for persistence + attach start times.
	chunks, _ := s.contentRepo.ListChunks(job.EpisodeID, "subtitle")
	chunkIDByIndex := agent.ResolveChunkIDs(res.Draft.Questions, chunks)
	questions := make([]model.Question, 0, len(res.Draft.Questions))
	for _, d := range res.Draft.Questions {
		q := model.Question{
			Type:        d.Type,
			ChunkID:     chunkIDByIndex[d.ChunkIndex],
			Stem:        d.Stem,
			Explanation: d.Explanation,
		}
		if d.Type == agent.QuestionChoice {
			opts, _ := json.Marshal(d.Options)
			q.Options = string(opts)
			q.Answer = d.Answer
		} else {
			at, _ := json.Marshal(d.AnswerText)
			q.AnswerText = string(at)
		}
		questions = append(questions, q)
	}

	quiz := &model.Quiz{
		EpisodeID:     job.EpisodeID,
		UserID:        userID,
		CourseID:      job.CourseID,
		Difficulty:    "adaptive",
		AgentFeedback: res.Draft.AgentFeedback,
	}
	if _, err := s.contentRepo.CreateQuiz(quiz, questions); err != nil {
		s.recordQuizRun(job.ID, modelName, res, elapsed, "fail", "persist: "+err.Error())
		s.failJob(job, "persist quiz: "+err.Error())
		return
	}

	// Record the ai_runs: one for the main generation (with trace), one for
	// self-check. These are what the admin replays.
	s.recordQuizRun(job.ID, modelName, res, elapsed, res.Draft.SelfCheckResult, res.Draft.SelfCheckNote)
	s.contentRepo.UpdateJobStatus(job.ID, "done", "", nil)
}

// recordQuizRun writes the ai_run for a quiz generation: the full trace_json
// (the agent's step-by-step reasoning) + aggregated usage + self-check verdict.
// This single run carries the observability payload for the whole generation.
func (s *aiService) recordQuizRun(jobID uint, modelName string, res *agent.QuizResult, elapsed time.Duration, selfCheck, note string) {
	input := fmt.Sprintf(`{"job_id":%d,"turns":%d,"steps":%d}`, jobID, res.Turns, len(res.Trace))
	s.contentRepo.CreateRun(&model.AIRun{
		JobID:            jobID,
		Capability:       "quiz",
		InputJSON:        input,
		PromptTokens:     res.Usage.PromptTokens,
		CompletionTokens: res.Usage.CompletionTokens,
		ModelUsed:        modelName,
		ResponseText:     truncateForRun(res.Draft.Questions, res.Draft.AgentFeedback),
		TraceJSON:        agent.TraceJSON(res.Trace),
		SelfCheckResult:  selfCheck,
		SelfCheckNote:    note,
		DurationMs:       int(elapsed.Milliseconds()),
	})
}

// truncateForRun builds a compact response_text snapshot of the generated quiz
// for the run record (the full questions live on the quiz rows; this is just a
// human-readable preview for the admin list view).
func truncateForRun(qs []agent.QuestionDraft, feedback string) string {
	preview, _ := json.Marshal(map[string]any{
		"question_count":  len(qs),
		"agent_feedback":  feedback,
	})
	return string(preview)
}

// --- client flow: lazy generation ---

// GetOrEnqueueQuiz implements lazy generation. The client GETs the quiz:
//   - exists → "ready" + the quiz
//   - missing, and AI/quiz enabled + chunks exist → enqueue a per-user quiz
//     job, return "generating" (client polls)
//   - AI off / no chunks → "unavailable" (client shows nothing / "AI 未就绪")
//
// Access is NOT re-checked here — the handler does that (IsEpisodeVisible) before
// calling. This method trusts the caller has gated access; keeping the check in
// one place (the handler) avoids divergent policies.
func (s *aiService) GetOrEnqueueQuiz(userID, episodeID uint) (string, *model.Quiz, error) {
	// A pending generation (queued/processing) takes priority over an existing
	// quiz: it means a regenerate is in flight, and the current quiz row is
	// about to be replaced. Returning "ready" with the soon-to-be-stale quiz
	// would let the client render questions that vanish on refresh. So check the
	// job FIRST.
	if s.hasPendingQuizJob(userID, episodeID) {
		return quizStatusGenerating, nil, nil
	}
	quiz, err := s.contentRepo.GetQuiz(userID, episodeID)
	if err != nil {
		return quizStatusUnavailable, nil, err
	}
	if quiz != nil {
		return quizStatusReady, quiz, nil
	}
	// No quiz yet. Check prerequisites before enqueuing (cheap gates).
	if !s.quizPrerequisitesMet(episodeID) {
		return quizStatusUnavailable, nil, nil
	}
	// Enqueue a per-user quiz job. ClaimNextQueuedJob picks it up next poll.
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil || ep == nil {
		return quizStatusUnavailable, nil, nil
	}
	job := &model.AIJob{
		JobType:   "quiz",
		EpisodeID: episodeID,
		CourseID:  ep.CourseID,
		UserID:    &userID,
		Status:    "queued",
	}
	if err := s.contentRepo.CreateJob(job); err != nil {
		return quizStatusUnavailable, nil, err
	}
	return quizStatusGenerating, nil, nil
}

// hasPendingQuizJob reports whether a queued/processing quiz job exists for the
// (user, episode). Used to suppress duplicate enqueues during client polling —
// generation takes ~30s (ReAct loop + self-check), and the client polls every
// 3s, so without this check we'd stack up ~10 redundant jobs per generation.
// A done/failed/skipped job does NOT count (those are finished; a new request
// means the user wants another attempt, e.g. after a prior failure).
func (s *aiService) hasPendingQuizJob(userID, episodeID uint) bool {
	var count int64
	s.db.Model(&model.AIJob{}).
		Where("job_type = ? AND user_id = ? AND episode_id = ? AND status IN ?", "quiz", userID, episodeID, []string{"queued", "processing"}).
		Count(&count)
	return count > 0
}

// quizPrerequisitesMet returns false when quiz generation can't succeed, so we
// don't enqueue a job that's doomed to fail (and waste a worker cycle + show the
// user a perpetual "generating"). Requires: AI resolver configured, course has
// AIQuizEnabled on, and subtitle chunks exist (the agent needs source material).
func (s *aiService) quizPrerequisitesMet(episodeID uint) bool {
	if s.resolver == nil {
		return false
	}
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil || ep == nil {
		return false
	}
	course, err := s.courseRepo.FindByID(ep.CourseID)
	if err != nil || course == nil || !course.AIQuizEnabled {
		return false
	}
	// Chunks are the agent's source material. No chunks → can't quiz meaningfully.
	has, err := s.contentRepo.HasChunks(episodeID, "subtitle")
	if err != nil || !has {
		return false
	}
	return true
}

// GetQuizForClient builds the client-safe view: questions without answers, with
// per-question answered state (from the latest answer row) + chunk start times.
// Returns (nil, nil) when no quiz exists (the client then sees "generating" or
// "unavailable" from GetOrEnqueueQuiz).
func (s *aiService) GetQuizForClient(userID, episodeID uint) (*QuizView, error) {
	quiz, err := s.contentRepo.GetQuiz(userID, episodeID)
	if err != nil {
		return nil, err
	}
	if quiz == nil {
		return nil, nil
	}
	questions, err := s.contentRepo.GetQuestions(quiz.ID)
	if err != nil {
		return nil, err
	}
	answers, err := s.contentRepo.ListAnswersForQuiz(quiz.ID, userID)
	if err != nil {
		return nil, err
	}
	answeredQIDs := make(map[uint]bool, len(answers))
	for _, a := range answers {
		answeredQIDs[a.QuestionID] = true
	}

	// Build a chunk_id → start_time map for video-jump.
	chunks, _ := s.contentRepo.ListChunks(episodeID, "subtitle")
	startByChunk := make(map[uint]*int, len(chunks))
	for _, c := range chunks {
		c := c
		startByChunk[c.ID] = c.StartTime
	}

	view := &QuizView{
		QuizID:        quiz.ID,
		EpisodeID:     quiz.EpisodeID,
		Difficulty:    quiz.Difficulty,
		AgentFeedback: quiz.AgentFeedback,
		Questions:     make([]QuizViewQuestion, 0, len(questions)),
	}
	for _, q := range questions {
		var opts []string
		if q.Options != "" {
			_ = json.Unmarshal([]byte(q.Options), &opts)
		}
		view.Questions = append(view.Questions, QuizViewQuestion{
			ID:             q.ID,
			Type:           q.Type,
			Stem:           q.Stem,
			Options:        opts,
			ChunkStartTime: startByChunk[q.ChunkID],
			Answered:       answeredQIDs[q.ID],
		})
		if answeredQIDs[q.ID] {
			view.AnsweredCount++
		}
	}
	return view, nil
}

// SubmitQuizAnswer grades one answer, records it, updates memory, and returns
// the verdict. Exactly one of answerIndex/answerText is meaningful per type:
// choice uses answerIndex, fill uses answerText. The unused arg is ignored.
func (s *aiService) SubmitQuizAnswer(userID, questionID uint, answerIndex *int, answerText *string) (*AnswerResult, error) {
	// Load the question (via a direct read — repo doesn't have GetQuestion, but
	// we can find it by querying). Use the content repo's GetQuestions path via
	// a minimal lookup. For simplicity and to avoid a new repo method, fetch the
	// question row directly through a scoped helper.
	q, err := s.getQuestion(questionID)
	if err != nil || q == nil {
		return nil, fmt.Errorf("question not found")
	}
	quiz, err := s.contentRepo.GetQuizByID(q.QuizID)
	if err != nil || quiz == nil || quiz.UserID != userID {
		return nil, fmt.Errorf("quiz not found for this user")
	}

	// Grade by type.
	idx := -1
	if answerIndex != nil {
		idx = *answerIndex
	}
	txt := ""
	if answerText != nil {
		txt = *answerText
	}
	correct := agent.GradeAnswer(*q, idx, txt)

	// Record the answer (append-only). QuizID is snapshotted so the answer
	// survives a future regenerate (换题 deletes the question but the answer's
	// QuizID + the memory state persist).
	s.contentRepo.CreateAnswer(&model.Answer{
		QuestionID: questionID,
		QuizID:     quiz.ID,
		UserID:     userID,
		UserAnswer: idx,
		Correct:    correct,
		AnsweredAt: time.Now(),
	})

	// Update memory (feedback loop). No-op for synthetic questions (chunkID=0).
	memory := agent.NewMemoryStore(s.contentRepo)
	if err := memory.RecordAnswer(context.Background(), userID, q.ChunkID, quiz.EpisodeID, quiz.CourseID, correct); err != nil {
		log.Printf("AI: update memory for question %d failed: %v", questionID, err)
		// non-fatal — the answer is recorded; memory just didn't update
	}

	// Build the result, revealing the correct answer + jump time.
	res := &AnswerResult{Correct: correct, Explanation: q.Explanation}
	if q.Type == agent.QuestionFill {
		var accept []string
		_ = json.Unmarshal([]byte(q.AnswerText), &accept)
		res.CorrectText = joinAcceptable(accept)
	} else {
		i := q.Answer
		res.CorrectIndex = &i
	}
	// Jump-to-video time from the linked chunk.
	if q.ChunkID != 0 {
		chunks, _ := s.contentRepo.ListChunks(quiz.EpisodeID, "subtitle")
		for _, c := range chunks {
			if c.ID == q.ChunkID {
				res.ChunkStartTime = c.StartTime
				break
			}
		}
	}
	return res, nil
}

// joinAcceptable renders the fill answer's acceptable forms for display.
func joinAcceptable(accept []string) string {
	out := ""
	for i, a := range accept {
		if i > 0 {
			out += " / "
		}
		out += a
	}
	return out
}

// RegenerateQuiz drops the user's current quiz and re-enqueues generation. The
// agent will read the user's current memory (updated by prior answers) and
// produce a fresh adaptive set. Returns "generating" so the client polls.
func (s *aiService) RegenerateQuiz(userID, episodeID uint) (string, error) {
	if !s.quizPrerequisitesMet(episodeID) {
		return quizStatusUnavailable, nil
	}
	// If a generation is already in flight, don't stack another — let it finish.
	// (CreateQuiz replaces atomically, but two concurrent generations waste LLM
	// tokens and could interleave confusingly in the trace view.)
	if s.hasPendingQuizJob(userID, episodeID) {
		return quizStatusGenerating, nil
	}
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil || ep == nil {
		return quizStatusUnavailable, nil
	}
	job := &model.AIJob{
		JobType:   "quiz",
		EpisodeID: episodeID,
		CourseID:  ep.CourseID,
		UserID:    &userID,
		Status:    "queued",
	}
	if err := s.contentRepo.CreateJob(job); err != nil {
		return quizStatusUnavailable, err
	}
	return quizStatusGenerating, nil
}

// getQuestion loads one question row by ID. A direct db read (rather than a new
// repo method) since this is the only single-question lookup — the quiz views
// load by quiz_id via GetQuestions.
func (s *aiService) getQuestion(id uint) (*model.Question, error) {
	var q model.Question
	if err := s.db.First(&q, id).Error; err != nil {
		return nil, err
	}
	return &q, nil
}

// --- admin observability reads ---

// ListQuizzesForUser returns a user's quizzes as admin-snake_case DTOs (the
// admin SPA's AiQuizRow type expects snake_case; raw model.Quiz marshals
// PascalCase).
func (s *aiService) ListQuizzesForUser(userID uint) ([]QuizDetailQuiz, error) {
	quizzes, err := s.contentRepo.ListQuizzesForUser(userID)
	if err != nil {
		return nil, err
	}
	out := make([]QuizDetailQuiz, 0, len(quizzes))
	for _, q := range quizzes {
		out = append(out, toQuizDTO(q))
	}
	return out, nil
}

func toQuizDTO(q model.Quiz) QuizDetailQuiz {
	return QuizDetailQuiz{
		ID:            q.ID,
		EpisodeID:     q.EpisodeID,
		UserID:        q.UserID,
		CourseID:      q.CourseID,
		Difficulty:    q.Difficulty,
		AgentFeedback: q.AgentFeedback,
		CreatedAt:     q.CreatedAt.Format("2006-01-02 15:04:05"),
	}
}

// GetQuizDetail assembles the full per-quiz admin view: questions (with answers
// + chunk start times), the student's answer history, their mastery, the agent
// feedback, and the ai_runs that produced it (trace lives on the runs).
func (s *aiService) GetQuizDetail(quizID uint) (*QuizDetail, error) {
	quiz, err := s.contentRepo.GetQuizByID(quizID)
	if err != nil {
		return nil, err
	}
	if quiz == nil {
		return nil, nil
	}
	questions, err := s.contentRepo.GetQuestions(quizID)
	if err != nil {
		return nil, err
	}
	chunks, _ := s.contentRepo.ListChunks(quiz.EpisodeID, "subtitle")
	startByChunk := make(map[uint]*int, len(chunks))
	for _, c := range chunks {
		c := c
		startByChunk[c.ID] = c.StartTime
	}
	detailQuestions := make([]QuizDetailQuestion, 0, len(questions))
	for _, q := range questions {
		detailQuestions = append(detailQuestions, QuizDetailQuestion{
			ID:             q.ID,
			Type:           q.Type,
			ChunkID:        q.ChunkID,
			Stem:           q.Stem,
			Options:        q.Options,
			Answer:         q.Answer,
			AnswerText:     q.AnswerText,
			Explanation:    q.Explanation,
			ChunkStartTime: startByChunk[q.ChunkID],
		})
	}
	rawAnswers, _ := s.contentRepo.ListAnswersForQuiz(quizID, quiz.UserID)
	detailAnswers := make([]QuizDetailAnswer, 0, len(rawAnswers))
	for _, a := range rawAnswers {
		detailAnswers = append(detailAnswers, QuizDetailAnswer{
			ID:         int(a.ID),
			QuestionID: a.QuestionID,
			UserID:     a.UserID,
			UserAnswer: a.UserAnswer,
			Correct:    a.Correct,
			AnsweredAt: a.AnsweredAt.Format("2006-01-02 15:04:05"),
		})
	}
	rawMasteries, _ := s.contentRepo.GetMasteries(quiz.UserID, quiz.EpisodeID)
	detailMasteries := make([]QuizDetailMastery, 0, len(rawMasteries))
	for _, m := range rawMasteries {
		detailMasteries = append(detailMasteries, QuizDetailMastery{
			ChunkID:      m.ChunkID,
			Mastery:      m.Mastery,
			CorrectCount: m.CorrectCount,
			WrongCount:   m.WrongCount,
		})
	}
	runs := s.findQuizRuns(quiz)
	return &QuizDetail{
		Quiz:      toQuizDTO(*quiz),
		Questions: detailQuestions,
		Answers:   detailAnswers,
		Masteries: detailMasteries,
		Runs:      runs,
	}, nil
}

// findQuizRuns locates the ai_runs for the job that generated this quiz. We
// match the most recent quiz job for the same (episode, user). Best-effort: if
// no runs are found (e.g. generated before tracing existed), returns empty.
func (s *aiService) findQuizRuns(quiz *model.Quiz) []model.AIRun {
	var job model.AIJob
	// Most recent quiz job for this episode+user.
	err := s.db.Where("job_type = ? AND episode_id = ? AND user_id = ?", "quiz", quiz.EpisodeID, quiz.UserID).
		Order("created_at DESC").First(&job).Error
	if err != nil {
		return nil
	}
	runs, err := s.contentRepo.ListRunsForJob(job.ID)
	if err != nil {
		return nil
	}
	return runs
}
