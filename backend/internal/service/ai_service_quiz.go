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

// --- 多选题作答存储(Answer 表无多选专用列,复用 UserAnswerText)---
//
// model.Answer 只有一个 int 列(UserAnswer)+ 一个 text 列(UserAnswerText)。choice 用
// UserAnswer,fill 用 UserAnswerText。多选题的"选中多个索引"两者都不够用,但加列要迁移
// schema 且不向后兼容(老进程读新行会炸)。
//
// 决策:多选题把用户选中的索引数组序列化成 JSON 存到 UserAnswerText(如 "[0,2,3]"),
// UserAnswer 存 -1(语义同 fill,标记"int 列无意义")。判分时从 UserAnswerText 解析回 []int。
// 这样不动表结构、不改老 answer 行(choice/fill 的 UserAnswerText 不受影响)。

// encodeMultiAnswer 把用户的多选索引数组序列化成 JSON 文本,存进 Answer.UserAnswerText。
func encodeMultiAnswer(indices []int) string {
	b, err := json.Marshal(indices)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// decodeMultiAnswer 从 Answer.UserAnswerText 解析多选索引数组。空串/格式错 → nil。
// 用来回填 history / view 里的 user_answer_indices。
func decodeMultiAnswer(stored string) []int {
	if stored == "" {
		return nil
	}
	var idx []int
	if err := json.Unmarshal([]byte(stored), &idx); err != nil {
		return nil
	}
	return idx
}

// --- client-facing view types ---

// QuizView is the client-safe quiz payload. Questions omit the correct answer
// (revealed post-submit); each carries a chunk_start_time for video-jump. The
// agent_feedback (LLM's study advice) is included so the student sees it.
type QuizView struct {
	QuizID        uint               `json:"quiz_id"`
	EpisodeID     uint               `json:"episode_id"`
	Difficulty    string             `json:"difficulty"`
	AgentFeedback string             `json:"agent_feedback,omitempty"`
	Questions     []QuizViewQuestion `json:"questions"`
	AnsweredCount int                `json:"answered_count"`
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
	ID             uint     `json:"id"`
	Type           string   `json:"type"`
	Stem           string   `json:"stem"`
	Options        []string `json:"options,omitempty"`          // choice only
	ChunkStartTime *int     `json:"chunk_start_time,omitempty"` // seconds, for video-jump; nil if synthetic
	Answered       bool     `json:"answered"`
	// HasJump 告诉前端这题是否可"跳转视频"。仅 has_jump=true 的题才渲染跳转按钮,
	// 避免给综合性题(无单一锚点)放一个点了没用的按钮。老数据(无此字段)为 false。
	HasJump bool `json:"has_jump"`

	// ── 以下字段仅在 quiz 已交卷(submitted)时填充 ──
	// 未交卷时为零值/omitempty,不暴露正确答案。交卷后从这里就能 review 当时的结果,
	// 不必再调 submit(交卷后不能再提交)。
	UserAnswerIndex *int `json:"user_answer_index,omitempty"` // choice: 学生当时选的索引
	// UserAnswerText 是填空题学生当时填的原文(交卷后回填,用于"你填的 X"回放)。
	// 之前因 Answer 表只有 int 列存不下,现在补上 Answer.UserAnswerText 后可回放。
	// 多选题也复用这一列:存用户选中索引的 JSON 数组(回放时解析成 user_answer_indices)。
	UserAnswerText string `json:"user_answer_text,omitempty"` // fill: 学生当时填的
	// UserAnswerIndices 是多选题学生当时选中的索引数组(交卷后回填)。从 Answer.UserAnswerText
	// 解析(JSON []int);解析失败/没存时为 nil。
	UserAnswerIndices []int `json:"user_answer_indices,omitempty"` // multi_choice: 学生当时选的
	Correct           bool  `json:"correct,omitempty"`             // 这题对不对(交卷后才有意义)
	// Partial 仅多选题部分对时为 true(漏选但没多选错项)。单选/填空恒 false(omitempty 不下发)。
	Partial bool `json:"partial,omitempty"`
	// MissedCount/ExtraCount 仅多选题部分对/错时下发,供前端展示"漏选 X / 多选 Y"。
	MissedCount  int    `json:"missed_count,omitempty"`
	ExtraCount   int    `json:"extra_count,omitempty"`
	CorrectIndex *int   `json:"correct_index,omitempty"` // choice: 正确选项索引
	CorrectText  string `json:"correct_text,omitempty"`  // fill: 标准答案
	// CorrectIndices 是多选题的正确选项索引数组(交卷后回填)。choice 题为 nil。
	CorrectIndices []int  `json:"correct_indices,omitempty"` // multi_choice: 正确索引
	Explanation    string `json:"explanation,omitempty"`     // 解析
}

// AnswerResult is the response to a submit. Reveals correctness, the correct
// answer, the explanation, and the jump-to-video time.
type AnswerResult struct {
	// QuestionID 让前端按 id 映射结果到题目,而不是依赖返回顺序与题序一致
	// (位置映射是脆弱契约——并发删题或 DB 排序漂移会导致错位)。
	QuestionID uint `json:"question_id"`
	Correct    bool `json:"correct"`
	// Partial 仅多选题部分对时 true(漏选但没多选错项)。单选/填空恒 false(omitempty)。
	Partial bool `json:"partial,omitempty"`
	// MissedCount/ExtraCount 仅多选题下发,供前端展示"漏选 X / 多选 Y"。
	MissedCount  int  `json:"missed_count,omitempty"`
	ExtraCount   int  `json:"extra_count,omitempty"`
	CorrectIndex *int `json:"correct_index,omitempty"` // choice: the right option index
	// CorrectIndices 多选题的正确索引数组(交卷后揭示)。choice 题为 nil。
	CorrectIndices []int  `json:"correct_indices,omitempty"` // multi_choice: 正确索引
	CorrectText    string `json:"correct_text,omitempty"`    // fill: the canonical answer(s)
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
	Quiz      QuizDetailQuiz       `json:"quiz"`
	Questions []QuizDetailQuestion `json:"questions"`
	Answers   []QuizDetailAnswer   `json:"answers"`
	Masteries []QuizDetailMastery  `json:"masteries"`
	Runs      []model.AIRun        `json:"runs"` // the ai_runs that generated this quiz (trace lives here). AIRun already has JSON tags? — no, but it's read directly by the existing AIWorkflow page which already tolerates PascalCase via the AiRun TS type mapping. Kept as-is for consistency with that page.
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
	ID         uint   `json:"id"`
	Type       string `json:"type"`
	ChunkID    uint   `json:"chunk_id"`
	Stem       string `json:"stem"`
	Options    string `json:"options"`     // JSON []string (choice/multi_choice)
	Answer     int    `json:"answer"`      // DEPRECATED choice: 0-based index (multi_choice 恒 0,看 correct_indices)
	AnswerText string `json:"answer_text"` // DEPRECATED fill: JSON []string (multi_choice 空,看 scoring)
	// CorrectIndices 多选题的正确选项索引数组(choice/fill 为 nil)。admin 核对多选题答案用。
	CorrectIndices []int `json:"correct_indices,omitempty"` // multi_choice: 正确索引
	PartialCredit  bool  `json:"partial_credit,omitempty"`  // multi_choice: 是否允许部分对
	// Scoring 透传原始判分元数据 JSON,供 admin 排查判分问题(按 type 解析)。
	Scoring        string `json:"scoring,omitempty"`
	Explanation    string `json:"explanation"`
	ChunkStartTime *int   `json:"chunk_start_time,omitempty"`
}

// QuizDetailAnswer is one student-answer row (append-only history).
type QuizDetailAnswer struct {
	ID             int    `json:"id"`
	QuestionID     uint   `json:"question_id"`
	UserID         uint   `json:"user_id"`
	UserAnswer     int    `json:"user_answer"`      // choice: 0-based index; fill: -1
	UserAnswerText string `json:"user_answer_text"` // fill: 学生原文(choice 题为空)
	Correct        bool   `json:"correct"`
	AnsweredAt     string `json:"answered_at"`
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
	QuizID        uint                  `json:"quiz_id"`
	EpisodeID     uint                  `json:"episode_id"`
	GeneratedAt   string                `json:"generated_at"` // quiz.CreatedAt (when the set was generated)
	ArchivedAt    string                `json:"archived_at"`  // when it was superseded; drives panel ordering
	QuestionCount int                   `json:"question_count"`
	WrongCount    int                   `json:"wrong_count"` // answers with Correct=false against this quiz
	AgentFeedback string                `json:"agent_feedback,omitempty"`
	Questions     []QuizHistoryQuestion `json:"questions"`
}

// QuizHistoryQuestion is one question in a history quiz, WITH the correct
// answer exposed (read-only review — no submit path).
type QuizHistoryQuestion struct {
	ID           uint     `json:"id"`
	Type         string   `json:"type"`
	Stem         string   `json:"stem"`
	Options      []string `json:"options,omitempty"`
	CorrectIndex *int     `json:"correct_index,omitempty"` // choice
	// CorrectIndices 多选题的正确索引数组(历史 review 也揭示)。choice 题为 nil。
	CorrectIndices []int  `json:"correct_indices,omitempty"` // multi_choice
	CorrectText    string `json:"correct_text,omitempty"`    // fill
	Explanation    string `json:"explanation"`
	ChunkStartTime *int   `json:"chunk_start_time,omitempty"`
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
	// UserIndices 是多选题学生当时选中的索引数组(历史 review 回放)。从 Answer.UserAnswerText
	// 解析的 JSON []int。multi_choice 题专用;nil 表示没作答。
	UserIndices []int `json:"user_indices,omitempty"` // multi_choice
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
	// ListByCourse 给 course summary agent 用(Phase D):遍历课程下所有 episode。
	// repository.EpisodeRepository 已实现该方法,advice/quiz 路径的 episodeRepo 满足。
	ListByCourse(courseID uint) ([]model.Episode, error)
}
type agentCourseLoader interface {
	FindByID(id uint) (*model.Course, error)
	// FindByIDWithSubject 只读路径用(Preload Subject),供 Effective*Hint 学科级回退。
	FindByIDWithSubject(id uint) (*model.Course, error)
}

func (d *agentToolDeps) ListChunks(episodeID uint) ([]model.ContentChunk, error) {
	return d.contentRepo.ListChunks(episodeID, "subtitle")
}
func (d *agentToolDeps) GetEpisode(episodeID uint) (*model.Episode, error) {
	return d.episodeRepo.FindByID(episodeID)
}
func (d *agentToolDeps) GetCourse(courseID uint) (*model.Course, error) {
	// 用 FindByIDWithSubject(带 Subject 预加载):quiz agent 的 get_episode_info 工具
	// 需要 course.Subject 做 EffectiveQuizHint/EffectiveTermDict 学科级回退。只读路径,
	// Preload 安全(不经过 UpdateCourse 的 Save)。
	return d.courseRepo.FindByIDWithSubject(courseID)
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

// ── course summary 工具集专用方法(Phase D)──
// ListCourseEpisodes 委托 episodeRepo.ListByCourse。course summary agent 用它遍历整门课。
// repository.EpisodeRepository 已实现 ListByCourse,所以 episodeRepo 直接满足。
func (d *agentToolDeps) ListCourseEpisodes(courseID uint) ([]model.Episode, error) {
	return d.episodeRepo.ListByCourse(courseID)
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
	if job.EpisodeID == nil || job.CourseID == nil {
		s.failJob(job, "quiz job missing episode_id/course_id")
		return
	}
	userID := *job.UserID
	episodeID, courseID := *job.EpisodeID, *job.CourseID

	// Both providers are needed: chat for generation, embedding for search.
	// Quiz generation is the highest-stakes task (homophone-bad questions confuse
	// students), so it gets the "quiz" purpose tag — admin can point this at the
	// strongest model they have. The self-check (checkAgent below) reuses the
	// same provider; if it deserves a separate tag later, thread "quiz_check".
	llm, err := s.resolver.ResolveChatByPurpose("quiz")
	if err != nil {
		s.failJob(job, "resolve chat provider: "+err.Error())
		return
	}
	emb, err := s.resolver.ResolveEmbedder()
	if err != nil {
		s.failJob(job, "resolve embedding provider: "+err.Error())
		return
	}
	modelName := s.resolver.ChatModelNameByPurpose("quiz")

	// Episode + course context for the prompt + chunk-id resolution.
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil || ep == nil {
		s.failJob(job, "load episode: "+err.Error())
		return
	}
	course, _ := s.courseRepo.FindByID(courseID)
	subject := ""
	if course != nil {
		subject = course.Subject.Label
	}

	// Build the agent graph: deps adapter → memory → toolbox → agents → quizzer.
	deps := &agentToolDeps{contentRepo: s.contentRepo, episodeRepo: s.episodeRepo, courseRepo: s.courseRepo}
	memory := agent.NewMemoryStore(s.contentRepo) // contentRepo implements agent.MemoryRepo
	toolbox := agent.NewQuizToolbox(deps, memory, emb, episodeID, userID, courseID)
	// MaxTokens is generous on the generation turn: the final answer is a
	// multi-question quiz JSON with per-question explanations. Round 3 raised the
	// question count to 8-12 and demands stronger distractors + reasoning-based
	// stems, which pushes output to ~3000-5000 tokens. Without an explicit cap the
	// relay/model default can be small (we saw ~1197-token truncation), cutting the
	// JSON mid-generation and breaking parsing. 6000 leaves comfortable headroom.
	genAgent := agent.NewAgent(llm, modelName, toolbox, agent.AgentOpts{MaxSteps: 6, MaxTokens: 6000})
	checkAgent := agent.NewAgent(llm, modelName, nil, agent.AgentOpts{MaxSteps: 1, MaxTokens: 800}) // self-check: short verdict
	quizzer := agent.NewQuizzer(genAgent, checkAgent, memory, deps, llm, modelName)

	start := time.Now()
	res, err := quizzer.Generate(ctx, agent.QuizzerRequest{
		EpisodeID:    episodeID,
		CourseID:     courseID,
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
	chunks, _ := s.contentRepo.ListChunks(episodeID, "subtitle")
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
		// 判分元数据统一走 Scoring(JSON,按 type 解析),grading 优先读 Scoring。
		// choice/fill 同时写老字段(Answer/AnswerText)兼容老代码路径(老 query / 单题
		// submit / 未升级的判分点),多选题没有老字段可写只能进 Scoring。
		switch d.Type {
		case agent.QuestionMultiChoice:
			opts, _ := json.Marshal(d.Options)
			q.Options = string(opts)
			// min_correct_for_half 缺省 1(parseQuizGeneration 已保证 ≥1,这里兜底防 0)。
			minForHalf := d.MinCorrectForHalf
			if minForHalf < 1 {
				minForHalf = 1
			}
			scoring, _ := json.Marshal(map[string]any{
				"correct_indices":      d.CorrectIndices,
				"partial_credit":       d.PartialCredit,
				"min_correct_for_half": minForHalf,
			})
			q.Scoring = string(scoring)
		case agent.QuestionChoice:
			opts, _ := json.Marshal(d.Options)
			q.Options = string(opts)
			q.Answer = d.Answer
			scoring, _ := json.Marshal(map[string]any{"correct_index": d.Answer})
			q.Scoring = string(scoring)
		default: // fill
			at, _ := json.Marshal(d.AnswerText)
			q.AnswerText = string(at)
			scoring, _ := json.Marshal(map[string]any{"accept": d.AnswerText})
			q.Scoring = string(scoring)
		}
		questions = append(questions, q)
	}

	quiz := &model.Quiz{
		EpisodeID:     episodeID,
		UserID:        userID,
		CourseID:      courseID,
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
	if err := s.contentRepo.CreateRun(&model.AIRun{
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
		// 记下这次发给 LLM 的完整 system+user prompt,供 admin "查看回放"
		// 还原本次 prompt(原来只存精简 InputJSON 快照,调 prompt 是盲调)。
		SystemPromptText: res.SystemPrompt,
		UserPromptText:   res.UserPrompt,
	}); err != nil {
		log.Printf("AI: recordQuizRun failed for job %d: %v", jobID, err)
	}
}

// truncateForRun builds a compact response_text snapshot of the generated quiz
// for the run record (the full questions live on the quiz rows; this is just a
// human-readable preview for the admin list view).
func truncateForRun(qs []agent.QuestionDraft, feedback string) string {
	preview, _ := json.Marshal(map[string]any{
		"question_count": len(qs),
		"agent_feedback": feedback,
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
	courseID := ep.CourseID
	job := &model.AIJob{
		JobType:   "quiz",
		EpisodeID: &episodeID,
		CourseID:  &courseID,
		UserID:    &userID,
		Status:    "queued",
		// 学生正在客户端轮询等出题(每 3s 一次),quiz 必须排在 segment/summary
		// 前面才能把可见延迟压到最低。priorityQuiz(10)远高于后台作业。
		Priority: priorityQuiz,
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
		// 三种题型分别回填:choice→UserAnswerIndex/CorrectIndex,fill→UserAnswerText/
		// CorrectText,multi_choice→UserAnswerIndices/CorrectIndices/Partial(+Missed/Extra)。
		// 注意 multi_choice 的 Partial 不在 Answer 表(只存了 Correct=是否全对),这里按
		// UserAnswerIndices 重算 GradeMultiChoice 得到 partial/missed/extra。
		if submitted {
			if a := answerByQID[q.ID]; a != nil {
				switch q.Type {
				case agent.QuestionMultiChoice:
					userIdx := decodeMultiAnswer(a.UserAnswerText)
					qv.UserAnswerIndices = userIdx
					mv := agent.GradeMultiChoice(q, userIdx)
					qv.Partial = mv.Partial
					qv.MissedCount = mv.MissedCount
					qv.ExtraCount = mv.ExtraCount
					qv.Correct = mv.Correct
				case agent.QuestionFill:
					qv.UserAnswerText = a.UserAnswerText
					qv.Correct = a.Correct
				default: // choice
					idx := a.UserAnswer
					qv.UserAnswerIndex = &idx
					qv.Correct = a.Correct
				}
			}
			// 揭示正确答案(三题型分别走各自字段)。
			switch q.Type {
			case agent.QuestionMultiChoice:
				if s := agent.ParseScoring(q); s != nil {
					qv.CorrectIndices = s.MultiCorrectIndices
				}
			case agent.QuestionFill:
				qv.CorrectText = joinAcceptable(fillAcceptable(q))
			default: // choice
				idx := choiceAnswerIndex(q)
				qv.CorrectIndex = &idx
			}
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
	// QuizID + the memory state persist). UserAnswerText 持久化填空题原文,
	// 让交卷后 / 历史 review 能回放"你当时填的什么"(choice 题留空)。
	s.contentRepo.CreateAnswer(&model.Answer{
		QuestionID:     questionID,
		QuizID:         quiz.ID,
		UserID:         userID,
		UserAnswer:     idx,
		UserAnswerText: txt,
		Correct:        correct,
		AnsweredAt:     time.Now().UTC(),
	})

	// Update memory (feedback loop). No-op for synthetic questions (chunkID=0).
	memory := agent.NewMemoryStore(s.contentRepo)
	if err := memory.RecordAnswer(context.Background(), userID, q.ChunkID, quiz.EpisodeID, quiz.CourseID, correct); err != nil {
		log.Printf("AI: update memory for question %d failed: %v", questionID, err)
		// non-fatal — the answer is recorded; memory just didn't update
	}

	// Build the result, revealing the correct answer + jump time.
	// 三题型分别揭示:choice→CorrectIndex,fill→CorrectText,multi_choice→CorrectIndices。
	// 注意:这条单题 submit 路径当前前端不走(前端用 submit-all),且签名不支持 multi_choice
	// 作答(没 answerIndices 参数)。但 reveal 逻辑仍要按题型分派,避免万一调用方传
	// multi_choice 题时把 q.Answer=0 当正确答案下发。
	res := &AnswerResult{QuestionID: q.ID, Correct: correct, Explanation: q.Explanation}
	switch q.Type {
	case agent.QuestionMultiChoice:
		if s := agent.ParseScoring(*q); s != nil {
			res.CorrectIndices = s.MultiCorrectIndices
		}
	case agent.QuestionFill:
		res.CorrectText = joinAcceptable(fillAcceptable(*q))
	default: // choice
		i := choiceAnswerIndex(*q)
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
// AnswerText,多选题填 AnswerIndices;其余字段留空。与单题 submit 的 request 字段名
// 保持一致,方便前端复用。
type QuizAnswerInput struct {
	QuestionID  uint   `json:"question_id"`
	AnswerIndex *int   `json:"answer_index,omitempty"`
	AnswerText  string `json:"answer_text,omitempty"`
	// AnswerIndices 是多选题学生选中的选项索引数组(无序)。仅 multi_choice 题有意义。
	AnswerIndices []int `json:"answer_indices,omitempty"`
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
	// 并发安全:在落任何 answer/memory 之前,用条件 UPDATE 抢占 submitted_at。
	// 两个并发 submit-all 都可能过上面的非事务 nil 检查,但只有一个能抢到这把锁——
	// SQLite 的 UPDATE 自动行锁,败者 RowsAffected=0,直接拒绝,不会落重复 Answer 行
	// 或重复扣 mastery(消除 TOCTOU)。抢到后即使后续步骤失败,quiz 也已锁定(交卷态),
	// 符合"一次提交=一次考试"的语义。
	now := time.Now().UTC()
	claimed, err := s.contentRepo.TryMarkQuizSubmitted(quiz.ID, now)
	if err != nil {
		return nil, fmt.Errorf("lock quiz for submit: %w", err)
	}
	if !claimed {
		// 别的请求抢先盖了戳——本请求是并发重复交卷,拒绝。
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
	// 预取 course 的 subjectID,给错题本 hook 用(错题本冗余 subject_id 供按科目过滤)。
	// best-effort:取不到则 subjectID=0,错题本行 subject_id=0(可接受——主流程不依赖它)。
	var subjectID uint
	if c, cerr := s.courseRepo.FindByID(quiz.CourseID); cerr == nil && c != nil {
		subjectID = c.SubjectID
	}
	// question_id → 用户作答,便于按题目顺序回放。
	inputByQ := make(map[uint]QuizAnswerInput, len(answers))
	for _, a := range answers {
		inputByQ[a.QuestionID] = a
	}

	memory := agent.NewMemoryStore(s.contentRepo)
	ctx := context.Background()
	// now 复用前面 TryMarkQuizSubmitted 盖戳的同一时间戳——整个交卷动作(盖戳/落 answer/
	// 更新 memory)用统一时间,语义更一致。

	// 累计判分:落 answer 行 + 更新 memory,逐题产出 AnswerResult。任何一题失败直接
	// 中断并返回错误。注意:submitted_at 已在前面 TryMarkQuizSubmitted 抢占盖戳,所以
	// 这里即使中途失败,quiz 也已锁定(交卷态),不会出现"落了一半 answer 但 quiz 未交卷"
	// 的窗口——这正是把抢占提前的收益。
	results := make([]AnswerResult, 0, len(questions))
	for _, q := range questions {
		input, answered := inputByQ[q.ID]
		// 解析用户作答:int / 文本 / 索引数组(多选)。choice 用 idx,fill 用 txt,
		// multi 用 indices。三者来源不同,落库时按 type 选其一。
		idx := -1
		txt := ""
		var indices []int
		if answered && input.AnswerIndex != nil {
			idx = *input.AnswerIndex
		}
		if answered {
			txt = input.AnswerText
		}
		if answered && len(input.AnswerIndices) > 0 {
			indices = input.AnswerIndices
		}
		verdict := agent.Verdict{}
		if answered {
			verdict = agent.GradeAnswerV(q, idx, txt, indices)
			// 落 answer 行。多选题把索引数组 JSON 存进 UserAnswerText(choice/fill 不变),
			// 见文件头 encodeMultiAnswer 的设计说明。
			storedText := txt
			if q.Type == agent.QuestionMultiChoice {
				storedText = encodeMultiAnswer(indices)
			}
			s.contentRepo.CreateAnswer(&model.Answer{
				QuestionID:     q.ID,
				QuizID:         quiz.ID,
				UserID:         userID,
				UserAnswer:     idx,
				UserAnswerText: storedText,
				Correct:        verdict.Correct,
				AnsweredAt:     now,
			})
			// 更新 memory(feedback loop)。合成题(chunkID=0)是 no-op。
			// memory 更新的口径:全对(correct=true)+0.1(增 mastery),否则 -0.2(扣)。
			// 多选题部分对(漏选但没多选错项)按"错"处理:漏一个正确项就是没完全掌握,
			// 该扣 mastery 才能让弱点浮现给 advice/考试抽题。这是 2026-07-23 改的一致口径:
			// mastery / 错题本 / 显示对错 三处对"漏选"用同一判定(漏选=错),避免同一行为
			// 在不同地方判得相反(旧版给部分对传 true 不扣分,导致漏选既算错进错题本又不算
			// 错不扣 mastery,自相矛盾)。verdict.Partial 字段仍保留用于 UI 展示"漏选X/多选Y"
			// 的明细,但它不再改变 mastery/错题本的判定。
			if err := memory.RecordAnswer(ctx, userID, q.ChunkID, quiz.EpisodeID, quiz.CourseID, verdict.Correct); err != nil {
				log.Printf("AI: update memory for question %d failed: %v", q.ID, err)
				// non-fatal,同单题 submit 的处理:答案已记录,memory 没更新不阻断交卷
			}
			// 错题本 hook:做错的题(!correct)upsert 进 WrongBookItem,让学生能在错题本
			// 里复习。nil-safe(wrongBookRepo 在老测试里可能为 nil,生产已注入)。
			// best-effort:失败只记日志,不阻断交卷主流程——错题本是 nice-to-have 附加。
			// 漏选(multi_choice 部分对)按"错"处理,和 mastery 同口径(见上注释)。
			if s.wrongBookRepo != nil && !verdict.Correct {
				if werr := s.wrongBookRepo.UpsertOnWrong(model.WrongBookItem{
					UserID: userID, QuestionID: q.ID, ChunkID: q.ChunkID,
					CourseID: quiz.CourseID, EpisodeID: quiz.EpisodeID, SubjectID: subjectID,
				}); werr != nil {
					log.Printf("AI: wrong-book upsert for question %d failed: %v", q.ID, werr)
				}
			}
		}
		// 交卷后阅卷,无论是否作答都揭示正确答案(学生要看错题解析)。
		res := buildAnswerResult(q, verdict, startByChunk[q.ChunkID])
		results = append(results, res)
	}

	// 注:submitted_at 已在函数开头 TryMarkQuizSubmitted 抢占盖戳,这里不再重复盖。
	// 旧实现把盖戳放最后(前面 error 时 quiz 保持未交卷),但那留下了 TOCTOU 窗口;
	// 现在盖戳提前到所有副作用之前,quiz 一开始就锁定,更安全。

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
// 给 correct_index,fill 题给 correct_text,multi_choice 题给 correct_indices + 部分对信息;
// 有 chunk 锚点的题给 chunk_start_time。抽出来让 SubmitQuizAnswer 和
// SubmitAllQuizAnswers 共用同一套 reveal 规则。
func buildAnswerResult(q model.Question, v agent.Verdict, chunkStart *int) AnswerResult {
	res := AnswerResult{
		QuestionID:     q.ID,
		Correct:        v.Correct,
		Partial:        v.Partial,
		Explanation:    q.Explanation,
		ChunkStartTime: chunkStart,
	}
	if v.Multi != nil {
		// 多选题:带出漏选/多选计数 + 正确索引数组。
		res.MissedCount = v.Multi.MissedCount
		res.ExtraCount = v.Multi.ExtraCount
	}
	switch q.Type {
	case agent.QuestionFill:
		res.CorrectText = joinAcceptable(fillAcceptable(q))
	case agent.QuestionMultiChoice:
		if s := agent.ParseScoring(q); s != nil {
			res.CorrectIndices = s.MultiCorrectIndices
		}
	default: // choice
		i := choiceAnswerIndex(q)
		res.CorrectIndex = &i
	}
	return res
}

// fillAcceptable resolves the fill acceptable answers (Scoring.accept first,
// then legacy AnswerText JSON).
func fillAcceptable(q model.Question) []string {
	if s := agent.ParseScoring(q); s != nil && len(s.FillAccept) > 0 {
		return s.FillAccept
	}
	var accept []string
	_ = json.Unmarshal([]byte(q.AnswerText), &accept)
	return accept
}

// choiceAnswerIndex resolves the choice correct index (Scoring.correct_index
// first, then legacy Answer column).
func choiceAnswerIndex(q model.Question) int {
	if s := agent.ParseScoring(q); s != nil && s.ChoiceIndex != nil {
		return *s.ChoiceIndex
	}
	return q.Answer
}

// ErrQuizAlreadySubmitted 在对已交卷的 quiz 再次调用 SubmitAllQuizAnswers 时返回。
// handler 把它转成 409,前端据此提示"已交卷,不能重复提交"。
var ErrQuizAlreadySubmitted = fmt.Errorf("quiz already submitted")

// RegenerateQuiz drops the user's current quiz and re-enqueues generation. The
// agent will read the user's current memory (updated by prior answers) and
// produce a fresh adaptive set. Returns "generating" so the client polls.
//
// quizPrerequisitesMet 这道门对客户端很重要(没 chunks 时直接 unavailable,避免白等);
// admin 端用 RegenerateQuizForUser,走同一道门 —— 如果一个 episode 没 chunks,
// admin 重出题也会失败,这时 admin 应该去检查字幕/segment 是否到位,而不是被骗说"重出成功"。
func (s *aiService) RegenerateQuiz(userID, episodeID uint) (string, error) {
	return s.regenerateQuiz(userID, episodeID)
}

// RegenerateQuizForUser 是 admin 端的"给某学生重出题"入口,和客户端 RegenerateQuiz
// 走同一套实现(抽公共函数)。两者语义对齐:同样的 prerequisites check(没 chunks 时
// unavailable)、同样的在途去重、同样优先级。差异只在调用方权限(handler 端处理)。
func (s *aiService) RegenerateQuizForUser(userID, episodeID uint) (string, error) {
	return s.regenerateQuiz(userID, episodeID)
}

// regenerateQuiz 是 RegenerateQuiz / RegenerateQuizForUser 的共享实现。
// 抽出来避免两处重复,确保 admin 端重出题和学生自助换题走同一套规则。
func (s *aiService) regenerateQuiz(userID, episodeID uint) (string, error) {
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
	courseID := ep.CourseID
	job := &model.AIJob{
		JobType:   "quiz",
		EpisodeID: &episodeID,
		CourseID:  &courseID,
		UserID:    &userID,
		Status:    "queued",
		// 换题同样走高优先级:学生点了"换题"在等新题,和首次生成一样紧迫。
		Priority: priorityQuiz,
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
		dq := QuizDetailQuestion{
			ID:             q.ID,
			Type:           q.Type,
			ChunkID:        q.ChunkID,
			Stem:           q.Stem,
			Options:        q.Options,
			Answer:         q.Answer,
			AnswerText:     q.AnswerText,
			Scoring:        q.Scoring,
			Explanation:    q.Explanation,
			ChunkStartTime: startByChunk[q.ChunkID],
		}
		// 多选题从 Scoring 解析正确索引 + partial_credit,让 admin 能核对答案。
		if q.Type == agent.QuestionMultiChoice {
			if s := agent.ParseScoring(q); s != nil {
				dq.CorrectIndices = s.MultiCorrectIndices
				dq.PartialCredit = s.MultiPartialCredit
			}
		}
		detailQuestions = append(detailQuestions, dq)
	}
	rawAnswers, _ := s.contentRepo.ListAnswersForQuiz(quizID, quiz.UserID)
	detailAnswers := make([]QuizDetailAnswer, 0, len(rawAnswers))
	for _, a := range rawAnswers {
		detailAnswers = append(detailAnswers, QuizDetailAnswer{
			ID:             int(a.ID),
			QuestionID:     a.QuestionID,
			UserID:         a.UserID,
			UserAnswer:     a.UserAnswer,
			UserAnswerText: a.UserAnswerText,
			Correct:        a.Correct,
			AnsweredAt:     a.AnsweredAt.Format("2006-01-02 15:04:05"),
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
		// 揭示正确答案(三题型分别走各自字段)。历史 review 是只读,所有答案都揭示。
		switch q.Type {
		case agent.QuestionMultiChoice:
			if s := agent.ParseScoring(q); s != nil {
				hq.CorrectIndices = s.MultiCorrectIndices
			}
		case agent.QuestionFill:
			hq.CorrectText = joinAcceptable(fillAcceptable(q))
		default: // choice
			i := choiceAnswerIndex(q)
			hq.CorrectIndex = &i
		}
		// 回放学生当时的作答:选择题给索引,填空题给原文,多选题给索引数组。
		// 未作答的题相应字段为零值(UserIndex=nil/UserText=""/UserIndices=nil),前端据此显示"未作答"。
		if ans, ok := latestByQ[q.ID]; ok {
			switch q.Type {
			case agent.QuestionMultiChoice:
				hq.UserIndices = decodeMultiAnswer(ans.UserAnswerText)
			case agent.QuestionFill:
				hq.UserText = ans.UserAnswerText
			default: // choice
				if ans.UserAnswer >= 0 {
					idx := ans.UserAnswer
					hq.UserIndex = &idx
				}
			}
		}
		out.Questions = append(out.Questions, hq)
	}
	return out, nil
}
