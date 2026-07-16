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
	// Submitted 反映 quiz.SubmittedAt 是否非空(已交卷)。Phase B 改为统一提交后,
	// 客户端据此切到"只读看结果"状态:已交卷就锁定所有题、逐题展示对错。
	// 用专门字段而不是"answered_count==总数",因为单题 submit 兼容端点也会填 answer。
	Submitted bool `json:"submitted"`
}

// QuizViewQuestion is one question as served to the client. Before submit, the
// correct answer is stripped (防作弊). After submit (quiz.SubmittedAt != nil), the
// result fields are filled so a student revisiting a submitted quiz sees their
// verdict + the correct answer + explanation (not just a locked blank question).
type QuizViewQuestion struct {
	ID            uint     `json:"id"`
	Type          string   `json:"type"`
	Stem          string   `json:"stem"`
	Options       []string `json:"options,omitempty"`        // choice only
	ChunkStartTime *int    `json:"chunk_start_time,omitempty"` // seconds, for video-jump; nil if synthetic
	Answered      bool     `json:"answered"`
	// HasJump 告诉前端这题是否可"跳转视频"。仅 has_jump=true 的题才渲染跳转按钮,
	// 避免给综合性题(无单一锚点)放一个点了没用的按钮。老数据(无此字段)为 false。
	HasJump bool `json:"has_jump"`

	// ── 以下字段仅在 quiz 已交卷(submitted)时填充 ──
	// 未交卷时为零值/omitempty,不暴露正确答案。交卷后从这里就能 review 当时的结果,
	// 不必再调 submit(交卷后不能再提交)。
	UserAnswerIndex *int   `json:"user_answer_index,omitempty"` // choice: 学生当时选的索引
	UserAnswerText  string `json:"user_answer_text,omitempty"`  // fill: 学生当时填的
	Correct         bool   `json:"correct,omitempty"`           // 这题对不对(交卷后才有意义)
	CorrectIndex    *int   `json:"correct_index,omitempty"`     // choice: 正确选项索引
	CorrectText     string `json:"correct_text,omitempty"`      // fill: 标准答案
	Explanation     string `json:"explanation,omitempty"`       // 解析
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
	// Resolved display names so the admin list renders titles, not bare ids
	// (the AIUserView page picked the user already, so no user_nickname here).
	EpisodeTitle string `json:"episode_title"`
	CourseTitle  string `json:"course_title"`
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

// --- read-only history view (Phase 3: archived quiz retention) ---
//
// QuizHistoryView is a single ARCHIVED quiz served fully read-only to the
// client history panel. Unlike QuizView (the active quiz, which hides the
// correct answer pre-submit), a history quiz exposes the correct answer for
// every question — the student can't redo it, only review it. It also carries
// the per-question answered/wrong state so the panel can highlight mistakes
// without re-fetching.

// QuizHistoryView is one archived quiz in the client history panel.
type QuizHistoryView struct {
	QuizID        uint                 `json:"quiz_id"`
	EpisodeID     uint                 `json:"episode_id"`
	GeneratedAt   string               `json:"generated_at"` // quiz.CreatedAt (when the set was generated)
	ArchivedAt    string               `json:"archived_at"`  // when it was superseded; drives panel ordering
	QuestionCount int                  `json:"question_count"`
	WrongCount    int                  `json:"wrong_count"` // answers with Correct=false against this quiz
	AgentFeedback string               `json:"agent_feedback,omitempty"`
	Questions     []QuizHistoryQuestion `json:"questions"`
}

// QuizHistoryQuestion is one question in a history quiz, WITH the correct
// answer exposed (read-only review — no submit path).
type QuizHistoryQuestion struct {
	ID             uint     `json:"id"`
	Type           string   `json:"type"`
	Stem           string   `json:"stem"`
	Options        []string `json:"options,omitempty"`
	CorrectIndex   *int     `json:"correct_index,omitempty"`   // choice
	CorrectText    string   `json:"correct_text,omitempty"`    // fill
	Explanation    string   `json:"explanation"`
	ChunkStartTime *int     `json:"chunk_start_time,omitempty"`
	// Wrong is true if the student answered this question wrong at least once
	// (the panel highlights mistakes). False if answered-correct or never
	// answered. Computed from the archived quiz's answer rows.
	Wrong bool `json:"wrong"`
	// UserIndex 是学生当时在这套历史 quiz 里选的选择题答案(0-based 索引)。
	// 从该 quiz 的 answer 行里取最新一条(append-only,取最近一次作答)。nil 表示
	// 这题当时没作答(例如统一提交时漏答)。前端据此高亮"你选了 X"。
	UserIndex *int `json:"user_index,omitempty"`
	// UserText 是学生当时填的填空题答案(填空题原文)。同样取最新一条;空串表示没作答。
	UserText string `json:"user_text,omitempty"`
	// HasJump 透传 question.HasJump,历史 review 里只有 has_jump=true 的题才给跳转按钮。
	HasJump bool `json:"has_jump"`
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
		GetCourseMasteries(userID, courseID uint) ([]model.KnowledgeMemory, error)
		GetSubjectMasteries(userID, subjectID uint) ([]model.KnowledgeMemory, error)
	}
	episodeRepo agentEpisodeLoader
	courseRepo  agentCourseLoader
	// userRepo 只在 advice 路径用(ListUserCourses)。quiz 路径构造时留 nil,
	// ListUserCourses 会回退返回 nil —— quiz agent 永远不会调到那个工具。
	userRepo aiUserCourseLister
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

// ── advice 工具集专用方法(Phase C)──
// 这些方法是 ToolDeps 接口的一部分,quiz 路径用不到它们(quiz agent 不会调用
// advice 工具),但接口要满足才能编译通过。实现走真实的 repo:这样 agentToolDeps
// 既能给 quiz 用(忽略这些方法),也能给 advice 用(真正调用它们)——
// 一个 adapter 服务两个 agent,避免重复。advice job 里会注入携带 userRepo 的版本。
func (d *agentToolDeps) ListUserCourses(userID uint) ([]uint, error) {
	if d.userRepo == nil {
		return nil, nil
	}
	return d.userRepo.GetAccessList(userID)
}
func (d *agentToolDeps) GetCourseMasteries(userID, courseID uint) ([]model.KnowledgeMemory, error) {
	return d.contentRepo.GetCourseMasteries(userID, courseID)
}
func (d *agentToolDeps) GetSubjectMasteries(userID, subjectID uint) ([]model.KnowledgeMemory, error) {
	return d.contentRepo.GetSubjectMasteries(userID, subjectID)
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
		chunkID := chunkIDByIndex[d.ChunkIndex]
		q := model.Question{
			Type:        d.Type,
			ChunkID:     chunkID,
			Stem:        d.Stem,
			Explanation: d.Explanation,
		}
		// has_jump:优先采信 agent 的判断;若 agent 没给(老 prompt 输出)则回退到
		// "有 chunk 锚点就算可跳转"——保证向前兼容,新 prompt 上线前生成的题也能跳。
		// 注意 ChunkIndex>0 但 ResolveChunkIDs 没命中(该 index 不存在)时 chunkID=0,
		// 此时按"无锚点"处理,不会给一个虚假的跳转按钮。
		// (这里用短路 OR 兜底,只填一次,不区分"显式 false"和"缺失"——JSON 的 false
		// 经过解析就是 false,缺失也是 false,二者都走兜底。)
		q.HasJump = d.HasJump || chunkID != 0
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
		// 学生正在客户端轮询等出题(每 3s 一次),quiz 必须排在 segment/summary
		// 前面才能把可见延迟压到最低。priorityQuiz(10)远高于后台作业。
		Priority:  priorityQuiz,
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
	answerByQID := make(map[uint]*model.Answer, len(answers))
	for i := range answers {
		a := &answers[i]
		answeredQIDs[a.QuestionID] = true
		answerByQID[a.QuestionID] = a
	}
	submitted := quiz.SubmittedAt != nil

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
		Submitted:     submitted,
	}
	for _, q := range questions {
		var opts []string
		if q.Options != "" {
			_ = json.Unmarshal([]byte(q.Options), &opts)
		}
		qv := QuizViewQuestion{
			ID:             q.ID,
			Type:           q.Type,
			Stem:           q.Stem,
			Options:        opts,
			ChunkStartTime: startByChunk[q.ChunkID],
			Answered:       answeredQIDs[q.ID],
			HasJump:        q.HasJump,
		}
		// 已交卷:回填该题的作答 + 判定 + 正确答案 + 解析,让学生重进页面能 review。
		// 未交卷时这些字段保持零值(omitempty 不下发),不泄露答案。
		// 注意:Answer.UserAnswer 是 int(choice 的选项索引);填空题的用户原文当前
		// 没持久化(model 限制),填空题只能回填 Correct/CorrectText/Explanation。
		if submitted {
			if a := answerByQID[q.ID]; a != nil {
				if q.Type == "choice" {
					idx := a.UserAnswer
					qv.UserAnswerIndex = &idx
				}
				qv.Correct = a.Correct
			}
			if q.Type == "choice" {
				idx := q.Answer
				qv.CorrectIndex = &idx
			}
			qv.CorrectText = q.AnswerText
			qv.Explanation = q.Explanation
		}
		view.Questions = append(view.Questions, qv)
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

// QuizAnswerInput 是批量提交里一道题的作答。选择题填 AnswerIndex,填空题填
// AnswerText;另一字段留空。与单题 submit 的 request 字段名保持一致,方便前端复用。
type QuizAnswerInput struct {
	QuestionID  uint   `json:"question_id"`
	AnswerIndex *int   `json:"answer_index,omitempty"`
	AnswerText  string `json:"answer_text,omitempty"`
}

// SubmitAllQuizAnswers 是 Phase B 的"统一交卷":一次性判分本 quiz 全部题目,逐题
// 返回结果(correct/正确答案/解析/跳转时间戳),并为每题落 answer 行 + 更新 memory。
// 成功后给 quiz 盖 SubmittedAt,锁定该 quiz(一次提交=一次考试)。
//
// 设计要点:
//   - 复用 SubmitQuizAnswer 的判分 + memory 更新逻辑(抽到 gradeOneAnswer),保证
//     单题与批量两条路径的判分规则完全一致。
//   - 已交卷(SubmittedAt != nil)的 quiz 直接拒绝(409 由 handler 转),防止重复交卷
//     把 memory 算两遍、answer 行翻倍。
//   - 一题没出现在 answers 里(学生漏答):不落 answer 行,返回的 result 里 correct=
//     false 且不带正确答案 reveal——但前端展示结果时仍需要正确答案。所以这里对"漏答"
//     也算出 correct=false 的结果,并 reveal 正确答案(交卷后整张卷子阅完,不再藏答案)。
//   - 题目顺序:按 quiz 的 questions 顺序返回结果(而不是按 input 顺序),前端按题号
//     渲染更自然。input 里没有的题视为漏答。
func (s *aiService) SubmitAllQuizAnswers(userID, episodeID uint, answers []QuizAnswerInput) ([]AnswerResult, error) {
	quiz, err := s.contentRepo.GetQuiz(userID, episodeID)
	if err != nil {
		return nil, err
	}
	if quiz == nil {
		return nil, fmt.Errorf("quiz not found")
	}
	if quiz.SubmittedAt != nil {
		return nil, ErrQuizAlreadySubmitted
	}
	questions, err := s.contentRepo.GetQuestions(quiz.ID)
	if err != nil {
		return nil, err
	}
	// chunk_id → start_time 一次性建好,避免每题 ListChunks 一遍。
	chunks, _ := s.contentRepo.ListChunks(quiz.EpisodeID, "subtitle")
	startByChunk := make(map[uint]*int, len(chunks))
	for _, c := range chunks {
		c := c
		startByChunk[c.ID] = c.StartTime
	}
	// question_id → 用户作答,便于按题目顺序回放。
	inputByQ := make(map[uint]QuizAnswerInput, len(answers))
	for _, a := range answers {
		inputByQ[a.QuestionID] = a
	}

	memory := agent.NewMemoryStore(s.contentRepo)
	ctx := context.Background()
	now := time.Now()

	// 累计判分:落 answer 行 + 更新 memory,逐题产出 AnswerResult。任何一题失败直接
	// 中断并返回错误——交卷是原子的,要么全交要么不交(已落的 answer 行不影响:还没盖
	// SubmittedAt,quiz 仍可重新交卷;memory 更新也是幂等的增量,不会错乱)。
	results := make([]AnswerResult, 0, len(questions))
	for _, q := range questions {
		input, answered := inputByQ[q.ID]
		idx := -1
		txt := ""
		if answered && input.AnswerIndex != nil {
			idx = *input.AnswerIndex
		}
		if answered {
			txt = input.AnswerText
		}
		correct := false
		if answered {
			correct = agent.GradeAnswer(q, idx, txt)
			s.contentRepo.CreateAnswer(&model.Answer{
				QuestionID: q.ID,
				QuizID:     quiz.ID,
				UserID:     userID,
				UserAnswer: idx,
				Correct:    correct,
				AnsweredAt: now,
			})
			// 更新 memory(feedback loop)。合成题(chunkID=0)是 no-op。
			if err := memory.RecordAnswer(ctx, userID, q.ChunkID, quiz.EpisodeID, quiz.CourseID, correct); err != nil {
				log.Printf("AI: update memory for question %d failed: %v", q.ID, err)
				// non-fatal,同单题 submit 的处理:答案已记录,memory 没更新不阻断交卷
			}
		}
		// 交卷后阅卷,无论是否作答都揭示正确答案(学生要看错题解析)。
		res := buildAnswerResult(q, correct, startByChunk[q.ChunkID])
		results = append(results, res)
	}

	// 盖已交卷戳。放最后:前面有任何 error 都不会到这,quiz 仍是未交卷状态。
	if err := s.contentRepo.MarkQuizSubmitted(quiz.ID, now); err != nil {
		// answer/memory 已落,只是没盖戳——返回结果 + 日志。前端可凭 results 渲染,
		// 下次进页面 GetQuizForClient 会发现 SubmittedAt=nil 而显示未交卷,这是个
		// 罕见的不一致,但不会丢数据。这里不回滚已落的 answer(回滚更糟)。
		log.Printf("AI: mark quiz %d submitted failed: %v", quiz.ID, err)
	}

	// Phase C 链式触发:交卷成功后异步入队 episode 级 advice job。理由——学生刚交完
	// 卷,memory 已是最新(本次答题已更新),这时跑 advice 最准;且"复习建议"和"错题
	// 解析"是自然的后续动作。低优先级(priorityAdvice=1),不饿死 quiz;幂等(已有在途
	// advice job 不重复入队)。失败只记日志,绝不阻断交卷主流程——advice 是 nice-to-have。
	if uerr := s.EnqueueAdviceForEpisode(userID, quiz.EpisodeID); uerr != nil {
		log.Printf("AI: chain-enqueue advice for (user %d, episode %d) failed: %v", userID, quiz.EpisodeID, uerr)
	}
	return results, nil
}

// buildAnswerResult 把一道题的判分结果组装成客户端要的 AnswerResult。choice 题
// 给 correct_index,fill 题给 correct_text;有 chunk 锚点的题给 chunk_start_time。
// 抽出来让 SubmitQuizAnswer 和 SubmitAllQuizAnswers 共用同一套 reveal 规则。
func buildAnswerResult(q model.Question, correct bool, chunkStart *int) AnswerResult {
	res := AnswerResult{Correct: correct, Explanation: q.Explanation, ChunkStartTime: chunkStart}
	if q.Type == agent.QuestionFill {
		var accept []string
		_ = json.Unmarshal([]byte(q.AnswerText), &accept)
		res.CorrectText = joinAcceptable(accept)
	} else {
		i := q.Answer
		res.CorrectIndex = &i
	}
	return res
}

// ErrQuizAlreadySubmitted 在对已交卷的 quiz 再次调用 SubmitAllQuizAnswers 时返回。
// handler 把它转成 409,前端据此提示"已交卷,不能重复提交"。
var ErrQuizAlreadySubmitted = fmt.Errorf("quiz already submitted")

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
		// 换题同样走高优先级:学生点了"换题"在等新题,和首次生成一样紧迫。
		Priority:  priorityQuiz,
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
// PascalCase). Episode/course titles are batch-resolved (one lookup per
// distinct id, not per row) so the AIUserView list renders titles not bare ids.
func (s *aiService) ListQuizzesForUser(userID uint) ([]QuizDetailQuiz, error) {
	quizzes, err := s.contentRepo.ListQuizzesForUser(userID)
	if err != nil {
		return nil, err
	}
	names := s.resolveQuizNames(quizzes)
	out := make([]QuizDetailQuiz, 0, len(quizzes))
	for _, q := range quizzes {
		out = append(out, toQuizDTO(q, names))
	}
	return out, nil
}

func toQuizDTO(q model.Quiz, names quizNameCache) QuizDetailQuiz {
	return QuizDetailQuiz{
		ID:            q.ID,
		EpisodeID:     q.EpisodeID,
		UserID:        q.UserID,
		CourseID:      q.CourseID,
		Difficulty:    q.Difficulty,
		AgentFeedback: q.AgentFeedback,
		CreatedAt:     q.CreatedAt.Format("2006-01-02 15:04:05"),
		EpisodeTitle:  names.episodeTitles[q.EpisodeID],
		CourseTitle:   names.courseTitles[q.CourseID],
	}
}

// quizNameCache is the quiz-list analogue of jobNameCache: batch-resolved
// episode/course titles keyed by id, for a set of quizzes. Best-effort (a
// missing id → empty title).
type quizNameCache struct {
	episodeTitles map[uint]string
	courseTitles  map[uint]string
}

// resolveQuizNames loads episode/course titles for a quiz set in one pass per
// distinct id. Same best-effort posture as resolveJobNames.
func (s *aiService) resolveQuizNames(quizzes []model.Quiz) quizNameCache {
	c := quizNameCache{
		episodeTitles: make(map[uint]string, len(quizzes)),
		courseTitles:  make(map[uint]string, len(quizzes)),
	}
	seenEp, seenCourse := map[uint]bool{}, map[uint]bool{}
	for _, q := range quizzes {
		if !seenEp[q.EpisodeID] {
			seenEp[q.EpisodeID] = true
			if ep, err := s.episodeRepo.FindByID(q.EpisodeID); err == nil && ep != nil {
				c.episodeTitles[q.EpisodeID] = ep.Title
			}
		}
		if !seenCourse[q.CourseID] {
			seenCourse[q.CourseID] = true
			if course, err := s.courseRepo.FindByID(q.CourseID); err == nil && course != nil {
				c.courseTitles[q.CourseID] = course.Title
			}
		}
	}
	return c
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
		Quiz:      toQuizDTO(*quiz, s.resolveQuizNames([]model.Quiz{*quiz})),
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

// --- read-only history (Phase 3) ---

// ListQuizHistory assembles the read-only views for every archived quiz of a
// (user, episode). Each view is fully revealed (correct answers shown) since
// the student can only review, not answer. Returns an empty slice (not nil)
// when there's no history, so the client can render "no history yet" cleanly.
//
// The correct-answer/wrong highlighting per question comes from the archived
// quiz's own answer rows (scoped by QuizID snapshot) — this is exactly why
// Answer snapshots QuizID: even though the quiz is now archived, its answers
// still point at it and we can join them without a question table lookup.
func (s *aiService) ListQuizHistory(userID, episodeID uint) ([]QuizHistoryView, error) {
	archived, err := s.contentRepo.ListArchivedQuizzes(userID, episodeID)
	if err != nil {
		return nil, err
	}
	views := make([]QuizHistoryView, 0, len(archived))
	for i := range archived {
		v, err := s.buildQuizHistoryView(&archived[i])
		if err != nil {
			return nil, err
		}
		views = append(views, *v)
	}
	return views, nil
}

// buildQuizHistoryView turns one archived Quiz row into a fully-revealed,
// read-only client view (correct answers + per-question wrong state). Kept
// separate from GetQuizDetail's assembly because the history panel needs a
// different, lighter payload: no mastery/runs, correct answers always shown,
// and a per-question "was this answered wrong?" flag.
func (s *aiService) buildQuizHistoryView(quiz *model.Quiz) (*QuizHistoryView, error) {
	questions, err := s.contentRepo.GetQuestions(quiz.ID)
	if err != nil {
		return nil, err
	}
	// chunk_id → start_time map for the video-jump link, same as the other views.
	chunks, _ := s.contentRepo.ListChunks(quiz.EpisodeID, "subtitle")
	startByChunk := make(map[uint]*int, len(chunks))
	for _, c := range chunks {
		c := c
		startByChunk[c.ID] = c.StartTime
	}
	// Pull this archived quiz's answers (snapshot QuizID scopes them) to flag
	// wrong questions + reveal what the student actually picked. Note
	// ListAnswersForQuiz scopes to the (user, episode) across all quiz generations
	// (it joins quiz_id → episode); for a history quiz that's the right set since
	// each archived quiz is a distinct attempt at the same lesson. We pick the
	// most recent answer per question (append-only: redo adds a new row).
	rawAnswers, _ := s.contentRepo.ListAnswersForQuiz(quiz.ID, quiz.UserID)
	wrongQIDs := make(map[uint]bool, len(rawAnswers))
	wrongCount := 0
	// latestByQ: question_id → 最新一条 answer(按 AnsweredAt 降序取第一条)。
	// ListAnswersForQuiz 已经按 answered_at DESC 排序,所以第一次见某 question_id
	// 的行就是该题最新作答。
	latestByQ := make(map[uint]model.Answer, len(rawAnswers))
	for _, a := range rawAnswers {
		if _, seen := latestByQ[a.QuestionID]; !seen {
			latestByQ[a.QuestionID] = a
		}
		if !a.Correct {
			if !wrongQIDs[a.QuestionID] {
				wrongQIDs[a.QuestionID] = true
				wrongCount++
			}
		}
	}

	out := &QuizHistoryView{
		QuizID:        quiz.ID,
		EpisodeID:     quiz.EpisodeID,
		GeneratedAt:   quiz.CreatedAt.Format("2006-01-02 15:04"),
		QuestionCount: len(questions),
		WrongCount:    wrongCount,
		AgentFeedback: quiz.AgentFeedback,
		Questions:     make([]QuizHistoryQuestion, 0, len(questions)),
	}
	if quiz.ArchivedAt != nil {
		out.ArchivedAt = quiz.ArchivedAt.Format("2006-01-02 15:04")
	}
	for _, q := range questions {
		var opts []string
		if q.Options != "" {
			_ = json.Unmarshal([]byte(q.Options), &opts)
		}
		hq := QuizHistoryQuestion{
			ID:             q.ID,
			Type:           q.Type,
			Stem:           q.Stem,
			Options:        opts,
			Explanation:    q.Explanation,
			ChunkStartTime: startByChunk[q.ChunkID],
			Wrong:          wrongQIDs[q.ID],
			HasJump:        q.HasJump,
		}
		if q.Type == agent.QuestionFill {
			var accept []string
			_ = json.Unmarshal([]byte(q.AnswerText), &accept)
			hq.CorrectText = joinAcceptable(accept)
		} else {
			i := q.Answer
			hq.CorrectIndex = &i
		}
		// 回放学生当时的作答:选择题给索引,填空题给原文。未作答的题两字段都为零值
		// (UserIndex=nil,UserText=""),前端据此显示"未作答"。
		if ans, ok := latestByQ[q.ID]; ok {
			if q.Type == agent.QuestionFill {
				// Answer.UserAnswer 对填空题没有意义(填空判分是文本归一化匹配,
				// 我们没把原文存进 answer 行)。所以历史里填空题只展示对错和正确答案,
				// 不回放学生原文——这是既有存储的限制,不在 Phase B 范围内补。
				hq.UserText = ""
			} else if ans.UserAnswer >= 0 {
				idx := ans.UserAnswer
				hq.UserIndex = &idx
			}
		}
		out.Questions = append(out.Questions, hq)
	}
	return out, nil
}
