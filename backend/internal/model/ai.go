package model

import (
	"encoding/json"
	"strings"
	"time"
)

// Code split from models.go for navigability. See models.go for the
// package overview.

// AISummaryEnabled/AIQuizEnabled on, the system behaves exactly as before and
// these tables sit empty.
//
// Capability constants for AIProvider.Capability.
const (
	AICapabilityChat      = "chat"      // LLM chat completion
	AICapabilityEmbedding = "embedding" // text → vector
	AICapabilityRerank    = "rerank"    // (reserved, not wired in MVP)
)

// AIProvider is one row of admin-configured provider credentials for one
// capability. Modeled on StorageSource (multi-row, admin CRUD, test-connection
// button). The three capabilities are independent: you can run chat against a
// remote relay while embedding runs locally, and swap either without touching
// the other. Credentials are stored plaintext (same posture as StorageSource;
// at-rest encryption is a separate cross-cutting PR).
type AIProvider struct {
	ID           uint   `gorm:"primaryKey;autoIncrement"`
	Capability   string `gorm:"size:20;not null;index"` // chat | embedding | rerank
	Name         string `gorm:"size:100;not null"`      // display name, e.g. "主聊天模型"
	ProviderType string `gorm:"size:30;not null"`       // openai_compat | onnx_local | ...
	BaseURL      string `gorm:"size:1024"`              // chat relay base (no /v1); empty for onnx_local
	APIKey       string `gorm:"size:1024"`              // bearer token; empty for onnx_local
	ModelName    string `gorm:"size:255;not null"`      // model id (chat) or model dir (onnx)
	ExtraJSON    string `gorm:"type:text"`              // capability-specific knobs (temperature, dim, seqLen...)
	// Tags is a JSON array of purpose tags, e.g. ["polish","quiz-check"]. The
	// resolver uses it to route tasks to specific providers: a provider tagged
	// "polish" is preferred for the polish job, "quiz" for quiz generation, etc.
	// Empty/missing tags = general-purpose (the historical default) — every task
	// that finds no purpose-tagged provider falls back to this one. See
	// ProviderResolver.ResolveChatByPurpose. Stored as raw JSON text rather than
	// a real array because SQLite + GORM have no first-class JSON column type and
	// we filter it in Go (provider count is tiny — single-digit — so a linear
	// scan per resolve is free).
	Tags      string `gorm:"size:256"`
	IsEnabled bool   `gorm:"default:false"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableName pins the table name to `ai_providers`. Without this, GORM's default
// snake-casing turns `AIProvider` into `a_i_providers` (each uppercase letter
// gets a preceding underscore, then leading/trailing underscores are trimmed →
// `a_iproviders`), which is how the original misnamed table was born. The name
// is pinned explicitly so current and future deployments all use `ai_providers`
// and future-proof against any GORM naming-convention change.
//
// 历史背景:早期部署曾用过误名 `a_iproviders`,后来做过一次数据清零重整,
// 现网 DB 已全部统一为 `ai_providers`,不再需要从老表名迁移。(此前注释提到的
// migrateAIProvidersTableName 迁移函数已废弃删除,不再调用。)
func (AIProvider) TableName() string { return "ai_providers" }

// ParseTags parses the provider's Tags JSON array into a slice. Returns nil for
// empty/malformed tags (the caller treats nil as "general-purpose, matches no
// specific purpose"). Centralized here so the resolver and the admin DTO both
// parse the same way.
func (p AIProvider) ParseTags() []string {
	s := strings.TrimSpace(p.Tags)
	if s == "" {
		return nil
	}
	// Tolerate both `["a","b"]` and the degraded `a,b` form an admin might type
	// by hand — we never want a typo here to brick the resolver.
	if strings.HasPrefix(s, "[") {
		var out []string
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil
		}
		return out
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// HasTag reports whether the provider's Tags contain the given purpose. Convenience
// wrapper over ParseTags for the resolver's linear scan. Empty purpose always
// matches (the "general-purpose" caller asking for the default).
func (p AIProvider) HasTag(purpose string) bool {
	if purpose == "" {
		return true
	}
	for _, t := range p.ParseTags() {
		if t == purpose {
			return true
		}
	}
	return false
}

// AIJob is one asynchronous AI generation task (segment/summary/quiz), modeled
// on SubtitleJob's queue/claim/complete pattern. Generated offline so the
// client reads already-produced content with zero latency (the user never waits
// for a 20s LLM call). Status values mirror SubtitleJob: queued|processing|
// done|failed|skipped.
type AIJob struct {
	ID          uint       `gorm:"primaryKey;autoIncrement"`
	JobType     string     `gorm:"size:20;not null;index"` // segment | summary | quiz | advice | course_summary | user_report
	// EpisodeID/CourseID 是 *uint(可空):subject 级 advice job 不属于任何 episode/
	// course,enqueueAdviceJob 对它们写 nil。以前是 `uint not null` 但代码塞 0,
	// 形式上的约束没保护——0 是合法整数值,SQLite 接受,但语义撒谎(指向不存在的
	// episode)。改成显式 nil 让"无对应实体"的语义诚实表达。读处用 ptrVal() deref。
	EpisodeID   *uint      `gorm:"index"`
	CourseID    *uint      `gorm:"index"`
	UserID      *uint      `gorm:"index"` // nullable: segment/summary leave it NULL; quiz jobs bind to a specific user (per-user adaptive generation)
	Status      string     `gorm:"size:20;not null;default:'queued';index"`
	Priority    int        `gorm:"default:0"`
	Attempt     int        `gorm:"default:0"`
	ClaimedAt   *time.Time `gorm:"index"`
	CompletedAt *time.Time
	Error       string `gorm:"type:text"`
	Progress    *float64
	// PayloadJSON 存 job 类型特定的参数(Phase C advice 用:scope/scope_id/subject_id,
	// 因为 AIJob 表是 episode-centric 的,subject 级 advice 没有专门列)。quiz/segment/
	// summary job 留空。JSON 文本,宽松解析(buildAdviceRequest 容忍缺字段)。
	PayloadJSON string `gorm:"type:text"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PtrVal 把可空的 *uint 安全 deref 成 uint,nil 返回 0。供所有读 AIJob.EpisodeID /
// CourseID(以及未来其它可空 uint 列)的调用点使用,集中一处避免散落 nil 判断。
//
// 背景见 AIJob 字段注释:EpisodeID/CourseID 改成 *uint 是为了修"形式 not null 但代码
// 塞 0"的 bug —— subject 级 advice job 现在写 nil 而不是 0,语义诚实。所有读处用 PtrVal
// 把 nil 转回 0 保持向后兼容(uint 0 在老逻辑里一直表示"无对应实体")。
//
// 放在 model 包(不是 service)因为 handler/admin_ai.go 也要 deref(转 aiJobDTO 时),
// 放 model 让两个包都能用,不破坏包依赖方向。
func PtrVal(p *uint) uint {
	if p != nil {
		return *p
	}
	return 0
}

// ContentChunk is one retrievable unit of the RAG corpus, source-agnostic so
// subtitle segments AND attachment extracts (PDF/workbook text, future) live in
// one table and are retrievable together. For subtitle chunks, StartTime/
// EndTime let quiz/chat answers link back to an exact video timestamp ("this
// concept is explained at 12:38") — the knowledge-point → video-jump feature.
// Embedding is the JSON-serialized float32 vector from the Embedder; cosine
// similarity is computed in Go (brute force) since per-episode chunk counts are
// small (hundreds). StartTime/EndTime are NULL for attachment chunks.
type ContentChunk struct {
	ID         uint    `gorm:"primaryKey;autoIncrement"`
	EpisodeID  uint    `gorm:"index:idx_chunk_ep_src;not null"`
	CourseID   uint    `gorm:"index;not null"`
	SourceType string  `gorm:"size:20;not null;index:idx_chunk_ep_src;default:'subtitle'"` // subtitle | attachment
	SourceRef  string  `gorm:"size:255"`                                                 // subtitle_id or attachment identifier
	ChunkIndex int     `gorm:"not null"`
	StartTime  *int    // seconds; NULL for attachment
	EndTime    *int    // seconds; NULL for attachment
	Text       string  `gorm:"type:text;not null"`
	Embedding  string  `gorm:"type:text"` // JSON []float32, length = embedder Dim
	CreatedAt  time.Time
	// FK 关系(单向,AI 附加层):删 episode/course 时 DB 自动 CASCADE 清本表。
	// 关系字段只在 AI 侧声明,core 结构体(Episode/Course)零感知,保证 AI 关闭时
	// core 包运行不查询任何 AI 表。
	Episode Episode `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE" json:"-"`
	Course  Course  `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE" json:"-"`
}

// AISummary is the agent-generated summary for one episode (one row per
// episode). SummaryJSON is structured (key points, key concepts) so the client
// can render it richly. Generated by the summarizer capability via an AIJob.
type AISummary struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	EpisodeID  uint   `gorm:"uniqueIndex;not null"`
	CourseID   uint   `gorm:"index;not null"`
	SummaryJSON string `gorm:"type:text;not null"`
	ModelUsed  string `gorm:"size:255"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// FK 关系(AI 附加层,单向):删 episode/course 时 DB CASCADE 清本表。
	Episode Episode `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE" json:"-"`
	Course  Course  `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE" json:"-"`
}

// KnowledgeMemory is the per-user learning state for one knowledge-point chunk
// — the heart of the feedback loop. mastery (0.0–1.0) is updated on each answer
// (correct +0.1 / wrong −0.2, clamped; a decay curve is a planned later step)
// and READ by the quiz agent on the next generation, so it adapts to the
// student's weak points. This is what makes the system an agent (state-driven,
// self-adapting) rather than a stateless quiz generator.
type KnowledgeMemory struct {
	ID           uint       `gorm:"primaryKey;autoIncrement"`
	UserID       uint       `gorm:"uniqueIndex:idx_mem_user_chunk;not null"`
	EpisodeID    uint       `gorm:"index;not null"`
	CourseID     uint       `gorm:"index;not null"`
	ChunkID      uint       `gorm:"uniqueIndex:idx_mem_user_chunk;index;not null"` // the knowledge-point chunk
	Mastery      float64    `gorm:"default:0"`                                      // 0.0–1.0
	CorrectCount int        `gorm:"default:0"`
	WrongCount   int        `gorm:"default:0"`
	LastReviewed *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// FK 关系(AI 附加层,单向):删 user/episode/course/chunk 时 DB CASCADE 清本表。
	User    User          `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	Episode Episode       `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE" json:"-"`
	Course  Course        `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE" json:"-"`
	Chunk   ContentChunk  `gorm:"foreignKey:ChunkID;constraint:OnDelete:CASCADE" json:"-"`
}

// Quiz is one generated quiz set for a (user, episode). Questions belong to it.
// Generated by the quizzer capability (which runs the agent loop with tool
// calling) via an AIJob, then served read-only to the client.
//
// One ACTIVE row per (user, episode): a student has a SINGLE current quiz per
// lesson. "重做" (redo) answers the same set again (Answer rows accumulate,
// memory updates); "换题" (regenerate) ARCHIVES the current row + its questions
// (Status active→archived, ArchivedAt set) and inserts a fresh active one, so
// the student's past attempts stay readable in the history panel instead of
// being wiped. The single-active invariant is enforced by a partial unique
// index (see AutoMigrate: WHERE status='active'); GORM can't express partial
// indexes, so the model itself carries no unique tag — the index is created in
// raw SQL after AutoMigrate.
type Quiz struct {
	ID            uint       `gorm:"primaryKey;autoIncrement"`
	EpisodeID     uint       `gorm:"index;not null"`
	UserID        uint       `gorm:"index;not null"`
	CourseID      uint       `gorm:"index;not null"`
	Difficulty    string     `gorm:"size:20;default:'adaptive'"` // adaptive = agent decides from memory
	AgentFeedback string     `gorm:"type:text"`                   // LLM's analysis of this student's weak points + study advice, a byproduct of generation (the agent already read memory to pick questions). Shown to the student on the AI study page and to the admin in the user view — both a learning aid and an observability signal.
	// Status is 'active' (the one current quiz the student plays) or 'archived'
	// (a superseded prior generation, kept read-only for history). Default
	// 'active' also back-fills pre-existing rows on migration, so the install's
	// current quizzes all become the active one — matching prior behavior where
	// there was exactly one row per (user, episode).
	Status     string     `gorm:"size:16;default:'active'"`
	ArchivedAt *time.Time // set when Status flips to 'archived'; nil while active
	// SubmittedAt 标记"已交卷"时间。Phase B 改成统一提交(一次提交 = 一次考试):
	// 学生点"提交全部"后,该 quiz 被锁定,不可再改答案。nil = 尚未交卷(仍可作答)。
	// 用专门字段而不是"是否存在 answer"来判断,因为单题 submit 端点(兼容保留)也会
	// 产生 answer 行,后者不能误判为已交卷。
	SubmittedAt *time.Time
	CreatedAt  time.Time
	// FK 关系(AI 附加层,单向):删 user/episode/course 时 DB CASCADE 清本表。
	// Quiz 是 Question/Answer 的父表(各自有 FK 指回 Quiz),CASCADE 会级联到它们。
	Episode Episode `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE" json:"-"`
	Course  Course  `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE" json:"-"`
	User    User    `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}

// Question is one question in a Quiz. ChunkID links it to the knowledge-point
// chunk it tests, which (via ContentChunk.StartTime) gives the "jump to video"
// timestamp.
//
// Two question types, selected by the agent based on the knowledge point:
//   - choice: Options is a JSON []string of options; Answer is the 0-based
//     correct index. Used for discrimination/understanding questions.
//   - fill:   a fill-in-the-blank with a single canonical answer. Options is
//     empty; AnswerText is a JSON []string of ACCEPTABLE answers (multiple
//     equivalent forms, e.g. ["12","十二"]) graded by normalization. Used ONLY
//     for knowledge points with a unique answer (typical: math computation,
//     factual recall). The prompt forbids fill questions when the answer is
//     subjective or ambiguous — a fill question whose "correct" answer is a
//     matter of opinion can't be graded.
type Question struct {
	ID          uint   `gorm:"primaryKey;autoIncrement"`
	QuizID      uint   `gorm:"index;not null"`
	ChunkID     uint   `gorm:"index"` // nullable: a question may be synthetic, not tied to one chunk
	// Type 扩展为 choice | multi_choice | fill（未来可加 judge | order | short_answer）。
	// 每种题型的判分元数据走 Scoring 列（按 type 解析 JSON），让加新题型不必再改表结构。
	Type        string `gorm:"size:20;default:'choice'"` // choice | multi_choice | fill
	Stem        string `gorm:"type:text;not null"`
	Options     string `gorm:"type:text"` // JSON []string (choice/multi_choice)
	// Scoring 承载各题型的判分元数据(JSON),按 type 解析。前向兼容设计:加新题型只扩
	// Scoring 的 schema,不必改表。示例:
	//   choice:       {"correct_index":2}
	//   multi_choice: {"correct_indices":[0,2,3],"partial_credit":true,"min_correct_for_half":1}
	//   fill:         {"accept":["12","十二"]}
	//   judge(未来):  {"correct":true}
	//   short_answer(未来): {"rubric":"...","keywords":[...]}
	// 历史:曾经有 Answer(int)/AnswerText(string) 两列承载 choice/fill 判分,2026-07-27
	// 清数据重部署时连同 DB 列一起删除——Scoring 列成为唯一判分元数据来源。
	Scoring     string `gorm:"type:text"`
	Explanation string `gorm:"type:text"`
	// HasJump 标记该题是否对应一个明确的视频片段(anchor chunk)。agent 出题时
	// 判断:能锚定到具体知识点 chunk 的题 has_jump=true(答错可跳视频复习);
	// 贯穿全文/综合性的题 has_jump=false(没有单一跳转锚点)。默认 false 兼容老数据
	// (Phase B 之前生成的题没有此字段,视为不可跳转)。
	HasJump bool `gorm:"default:false"`
	CreatedAt   time.Time
	// FK 关系(AI 附加层,单向):删 Quiz 时 DB CASCADE 清本表(以前最大孤儿源)。
	// Chunk 关系不加:ChunkID 可空(合成题),且 ContentChunk 删时 Question 不应被级联
	// 清(quiz 可能仍想引用它),让 Question 跟随 Quiz 生命周期走。
	Quiz Quiz `gorm:"foreignKey:QuizID;constraint:OnDelete:CASCADE" json:"-"`
}

// Answer records one user answer to one Question (append-only). Written on
// submit, then used to update KnowledgeMemory (the feedback loop).
//
// QuizID is a DENORMALIZED snapshot of the question's quiz at answer time. It
// lets us list a user's answer history for an episode WITHOUT joining questions
// — which matters because regenerate (换题) DELETES old questions, breaking a
// question-join. With QuizID we can still show past attempts after a regen by
// scoping to the (user, episode)'s quiz lineage. (There's at most one quiz row
// per (user, episode) at a time, but the quiz row gets a new ID on each regen;
// the old QuizID values on historical answers point to deleted quiz rows, which
// is fine — we group by the current quiz's episode instead.)
//
// Two answer shapes coexist by question type:
//   - choice: UserAnswer holds the 0-based option index; UserAnswerText is "".
//   - fill:   UserAnswerText holds the student's free-text answer verbatim
//     (previously discarded after grading — the answer could only be shown as
//     correct/wrong, never "你当时填的 X"). UserAnswer is -1 (meaningless for
//     fill). Grading still uses NormalizeText matching against
//     Question.AnswerText; this column is purely for回放 in submitted review +
//     history, so the student can see what they typed.
type Answer struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	QuestionID uint      `gorm:"index;not null"`
	// 复合索引 (quiz_id, user_id) 服务 ListAnswersForQuiz(quizID, userID)——交卷/历史
	// review 的主查询模式,复合索引比两个单列索引快(一次索引定位 vs 索引交集)。
	// 保留单列 index 兜底其它按 user_id 的查询(如错题本聚合)。
	QuizID     uint      `gorm:"index;index:idx_answer_quiz_user,priority:1"`
	UserID     uint      `gorm:"index;not null;index:idx_answer_quiz_user,priority:2"`
	UserAnswer int       // choice: 0-based index the user picked; fill: -1 (meaningless)
	// UserAnswerText 是填空题学生的原文(choice 题为空)。交卷后 / 历史 review 里回放
	// "你当时填的什么"用这个字段;判分仍走 Question.AnswerText 的归一化匹配,不依赖它。
	UserAnswerText string `gorm:"type:text"`
	Correct        bool
	AnsweredAt     time.Time
	// FK 关系(AI 附加层,单向):删 Question/Quiz/User 时 DB CASCADE 清本表。
	Question Question `gorm:"foreignKey:QuestionID;constraint:OnDelete:CASCADE" json:"-"`
	Quiz     Quiz     `gorm:"foreignKey:QuizID;constraint:OnDelete:CASCADE" json:"-"`
	User     User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}

// AIRun records ONE LLM call's decision trace — input snapshot, the raw model
// response, token usage, self-check outcome. Written for every agent step so
// the admin AI Workflow page can REPLAY how the agent reasoned: what chunks it
// retrieved, what memory weaknesses it saw, what it answered, whether its
// self-check passed. This is both the observability layer (debug bad output)
// and the learning material (see agent decision flow in action).
type AIRun struct {
	ID              uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	JobID           uint   `gorm:"index" json:"job_id"`               // 0 for ad-hoc (e.g. chat) runs not tied to a job
	Capability      string `gorm:"size:20;not null;index" json:"capability"` // summary | quiz | chat
	InputJSON       string `gorm:"type:text" json:"input_json"`      // snapshot: retrieved chunks, memory weaknesses
	PromptTokens    int    `json:"prompt_tokens"`
	CompletionTokens int   `json:"completion_tokens"`
	ModelUsed       string `gorm:"size:255" json:"model_used"`
	ResponseText    string `gorm:"type:text" json:"response_text"` // the raw model output
	// TraceJSON records the agent's ReAct step-by-step reasoning for quiz runs:
	// a JSON array of {step, thought, action:{tool, args}, observation}, one
	// entry per loop iteration. Empty for single-shot capabilities (summary) —
	// those have no loop. This is the observability centerpiece: the admin
	// "思考时间线" view replays exactly which tools the agent called, with what
	// arguments, and what each returned, so a bad quiz can be traced to the
	// retrieval/memory step that misled it. Observations are truncated per step
	// (tool output can be large) to keep the field scannable.
	TraceJSON       string `gorm:"type:text" json:"trace_json"`
	SelfCheckResult string `gorm:"size:20;default:'skipped'" json:"self_check_result"` // pass | fail | skipped
	SelfCheckNote   string `gorm:"type:text" json:"self_check_note"`
	DurationMs      int    `json:"duration_ms"`
	// SystemPromptText/UserPromptText 记录最终发给 LLM 的完整 prompt(可观测性)。
	// system prompt 是代码常量(此处冗余存一份供 admin 查看);user prompt 是拼装结果。
	// 让 admin 在 AI Workflow 页"查看回放"能看到这次到底发了什么给 LLM,告别盲调。
	SystemPromptText string `gorm:"column:system_prompt_text;type:text"`
	UserPromptText   string `gorm:"column:user_prompt_text;type:text"`
	CreatedAt        time.Time `json:"created_at"`
	// FK 关系(AI 附加层,单向):删 AIJob 时 DB CASCADE 清本表(以前孤儿源,因为
	// job_id 无 FK,删 AIJob 后 AIRun.job_id 悬空)。JobID=0 的 ad-hoc run(chat,
	// 未来)没有对应 job,FK 约束对它们是 RESTRICT —— 但目前所有 AIRun 都挂在 job 上,
	// 这条约束现在加是安全的。等 chat 上线时若需要 ad-hoc run,届时单独处理。
	Job AIJob `gorm:"foreignKey:JobID;constraint:OnDelete:CASCADE" json:"-"`
}

// ChatSession / ChatMessage hold the multi-turn chat (Phase D capability) so a
// user can discuss a lesson with the agent. Tables are created in this phase so
// the schema is stable, but the chat capability itself is implemented later.
type ChatSession struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	UserID     uint      `gorm:"index;not null"`
	EpisodeID  uint      `gorm:"index;not null"`
	CreatedAt  time.Time
}

type ChatMessage struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	SessionID  uint      `gorm:"index;not null"`
	Role       string    `gorm:"size:20;not null"` // user | assistant
	Content    string    `gorm:"type:text;not null"`
	ChunkRefs  string    `gorm:"type:text"`        // JSON [{text,start_time,end_time}] for video-jump links
	CreatedAt  time.Time
}

// StudyAdvice 是 Phase C 的 agent 驱动学习建议产物。和 quiz 不同,advice 的产出是
// 自然语言文本(不是结构化 JSON),由 advice agent 跑 ReAct loop 跨课程查 mastery 后
// 生成。按 (user, scope, scope_id) 唯一存储:
//   - scope="episode", scope_id=episode_id:某节课交卷后的复习建议
//   - scope="course",  scope_id=course_id:某门课的整体弱点分析
//   - scope="subject", scope_id=subject_id:某科目(跨多门课)的弱点分析
//
// 重新生成替换旧记录(同 quiz 的 Upsert 语义,但 advice 不保留历史——建议是"当前
// 快照",过期了就覆盖)。MasterySnapshotJSON 存当时 mastery 的 JSON 快照,供后续对比
// "上次建议后学生进步了多少"(Phase D 可用,Phase C 先存下来)。
type StudyAdvice struct {
	ID                  uint      `gorm:"primaryKey;autoIncrement" json:"-"`
	UserID              uint      `gorm:"uniqueIndex:idx_advice_user_scope;not null" json:"-"`
	Scope               string    `gorm:"size:16;uniqueIndex:idx_advice_user_scope;not null" json:"scope"`        // episode | course | subject
	ScopeID             uint      `gorm:"uniqueIndex:idx_advice_user_scope;not null" json:"scope_id"`             // episode_id / course_id / subject_id
	AdviceText          string    `gorm:"type:text;not null" json:"advice_text"`                                   // 自然语言建议(agent 的 FinalText)
	MasterySnapshotJSON string    `gorm:"type:text" json:"-"`                                                      // 生成时 mastery 快照,内部用,不下发客户端
	ModelUsed           string    `gorm:"size:255" json:"model_used,omitempty"`
	GeneratedAt         time.Time `gorm:"not null" json:"generated_at"`
	CreatedAt           time.Time `json:"-"`
	UpdatedAt           time.Time `json:"-"`
}

// AICourseSummary 是 Phase D 的课程级总结产物(course-unique)。和 StudyAdvice 的关键
// 差异:它按 course 唯一存储(不含 user_id),是**纯内容总结**——课程整体脉络 + 学习路径
// 建议,与具体学生无关。这样所有学生共享同一条总结,admin 生成一次即可,不必按 user
// 重复跑(不同学生的"针对建议"走 advice,那是 per-user 的,不在这里)。
//
// 重新生成替换旧记录(同 AISummary.UpsertSummary / StudyAdvice.UpsertAdvice 语义)。
// SummaryText 存 agent 的自然语言 FinalText(整体脉络 + 学习路径);课程总结是给所有
// 学生看的导览,不是结构化题库,所以用纯文本而不是 JSON。
type AICourseSummary struct {
	ID          uint      `gorm:"primaryKey;autoIncrement"`
	CourseID    uint      `gorm:"uniqueIndex;not null"`
	SummaryText string    `gorm:"type:text;not null"` // agent 的自然语言 FinalText(整体脉络 + 学习路径)
	ModelUsed   string    `gorm:"size:255"`
	GeneratedAt time.Time `gorm:"not null"`
	// EpisodeCountAtGen 是生成时该课程"已有 AI summary"的课时数快照。读时跟当前
	// CountEpisodesWithSummaryByCourse 对比,差值 > 0 = 字幕逐节补全后内容已变旧,
	// 给 admin/学生端展示"建议刷新"提示。课程总览只 admin 手动触发重生成(不自动
	// 跟着每集字幕补全跑——避免烧 API),这个快照让"陈旧"状态可观测。
	EpisodeCountAtGen int `gorm:"default:0"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
	// FK 关系(AI 附加层,单向):删 course 时 DB CASCADE 清本表。
	Course Course `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE" json:"-"`
}

// UserStudyReport 是 Phase E 的产物:admin 视角下"某学生跨课程学习情况"的 agent 报告。
// 和 StudyAdvice 的差异——advice 是给学生本人看的"复习建议"(episode/course/subject 级),
// UserStudyReport 是给 admin 看的"这个学生整体学得怎么样"的跨课程画像报告。每用户一份
// 最新报告(unique on user_id),重新生成替换。Agent 走和 advice 同一套 ReAct loop,但
// 工具集是 user_study 专用(list_user_courses / get_course_mastery / get_course_summary /
// get_user_advice),agent 自己遍历该学生所有课程交叉分析。
type UserStudyReport struct {
	ID          uint      `gorm:"primaryKey;autoIncrement"`
	UserID      uint      `gorm:"uniqueIndex;not null"` // 每用户一份最新报告(unique on user_id,重新生成替换)
	ReportText  string    `gorm:"type:text;not null"`   // 自然语言报告(agent 的 FinalText)
	ModelUsed   string    `gorm:"size:255"`
	GeneratedAt time.Time `gorm:"not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// FK 关系(AI 附加层,单向):删 user 时 DB CASCADE 清本表(以前 user_repo.Delete
	// 完全不动 AI 数据,是孤儿数据主源)。
	User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}

// GlossaryCandidate is a term-correction rule the polish pipeline mined while
// fixing homophones in a subtitle (see docs/subtitle-system-overhaul.md §三).
// Each polish run asks the LLM to surface, alongside the per-cue fixes, the
// reusable patterns it spotted — e.g. "车 is consistently mis-transcribed as
// 军/局 in this xiangqi course". These land here as pending candidates for the
// admin to review in the AI Console (PR2.5).
//
// Accepted candidates are appended to Course.AIConfigJSON.TermDict and become
// input to future polish runs, so the system converges: early episodes mine a
// lot, later ones almost nothing (the dict already covers the domain).
//
// Dedup key is (CourseID, Original, Corrected) — the same rule mined across
// multiple episodes of one course accumulates EvidenceCount instead of
// creating duplicate rows. Status guards against re-surfacing admin decisions:
// once accepted/rejected, future polish runs won't disturb the row (the upsert
// is a no-op on non-pending rows).
type GlossaryCandidate struct {
	ID             uint       `gorm:"primaryKey;autoIncrement"`
	CourseID       uint       `gorm:"index:idx_course_orig_corr;not null"`
	Original       string     `gorm:"size:64;index:idx_course_orig_corr;not null"`
	Corrected      string     `gorm:"size:64;index:idx_course_orig_corr;not null"`
	Context        string     `gorm:"size:256"`                       // free-form, e.g. "象棋术语,指棋子"
	Confidence     float64                                        // LLM-reported, [0,1]; only >= 0.7 mined
	EvidenceCount  int        `gorm:"default:0"`                     // total observations across episodes
	EvidenceSample string     `gorm:"type:text"`                     // JSON array of <=5 sample cue texts
	Status         string     `gorm:"size:16;default:'pending';index"` // pending | accepted | rejected
	AcceptedAt     *time.Time                                     // set when admin accepts
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AIPolishChunk is one chunk's persisted state within a polish job — the
// checkpoint/resume (断点续润) substrate. A polish job splits its VttContent
// into deterministic 150-cue chunks (step=147, so chunk boundaries are stable
// for a given input); each chunk that completes is written here. When the job
// is retried after a partial failure, runPolishJob reads back the done chunks
// and feeds them to polish.Polish as PriorOutcomes — so only the FAILED chunks
// re-call the LLM, instead of re-burning the whole episode's tokens.
//
// Lifecycle mirrors the parent job:
//   - seeded as Status="queued" when the job starts (SeedChunksForJob, idempotent)
//   - flipped to "done" / "failed" as each chunk finishes (MarkChunkDone/Failed,
//     driven by polish.Polish's OnChunkDone callback)
//   - cleared only by deleting the parent job (FK OnDelete:CASCADE). A successful
//     job keeps its chunk rows as a token-spend record + the resume anchor for
//     a future re-polish retry.
//
// PolishedChunkJSON stores the chunk's {changes,glossary} so a resume can rebuild
// the final VTT without re-calling the LLM. It's the serialized form of the
// in-memory chunkOutcome (CueChange + GlossaryCandidate slices).
type AIPolishChunk struct {
	ID                 uint   `gorm:"primaryKey;autoIncrement"`
	JobID              uint   `gorm:"uniqueIndex:idx_polish_chunk_job_idx;not null"`
	ChunkIndex         int    `gorm:"uniqueIndex:idx_polish_chunk_job_idx;not null"` // 0-based, matches polish chunk layout
	ChunkFirstGlobalIdx int   // first global cue idx in this chunk (audit / debug)
	ChunkLastGlobalIdx  int   // last global cue idx in this chunk (inclusive)
	Status             string `gorm:"size:16;default:'queued';index"` // queued | done | failed
	PromptTokens       int    // accumulated across this chunk's attempts (incl. validation-rejected)
	CompletionTokens   int
	Retries            int    // retry attempts spent before this chunk settled
	HighEditDistanceCount int // changes in this chunk flagged suspicious (informational)
	ChangedCues        int    // cues this chunk actually modified
	FirstErr           string `gorm:"size:256"` // on failed: the last retry's error (truncated)
	PolishedChunkJSON  string `gorm:"type:text"` // on done: {"changes":[...],"glossary":[...]} for resume
	CreatedAt          time.Time
	UpdatedAt          time.Time
	// NOTE: deliberately NO FK relation field (Job AIJob). Adding one alongside
	// the uniqueIndex on JobID made GORM's AutoMigrate silently skip creating
	// this table (the FK-constraint path + a custom unique index on the FK
	// column is a combination GORM's SQLite migrator bails on without error).
	// GlossaryCandidate follows the same no-relation-field pattern and migrates
	// fine. CASCADE cleanup on job delete isn't a real concern here (polish jobs
	// are essentially never deleted), and the uniqueIndex on (JobID, ChunkIndex)
	// — the thing SeedChunksForJob's ON CONFLICT depends on — now creates
	// cleanly because the table creates cleanly.
}

// LogEntry is one row in the lightweight structured log layer (TODO.md P1).
// Unlike AIRun (one per LLM call's full trace), LogEntry captures operational
// EVENTS across the AI/subtitle worker: job failed, reaper reset a stale job,
// provider resolve error, worker panic recovered. The point is admin
// observability WITHOUT SSH-ing in to read stderr — these land in the DB and
// surface on the /admin/logs page.
//
// Intentionally a thin wrapper, not a full logging framework (TODO.md: 不引第三
// 方 log 库). The 5 write sites (failJob / reaper / polishStats / provider
// resolve / worker panic) are the high-signal events; the other ~80 log.Printf
// calls in the codebase are NOT bulk-migrated (渐进迁移). FieldsJSON carries
// structured context (token counts, chunk ids, etc.) as a JSON blob.
type LogEntry struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Level      string    `gorm:"size:16;not null;index" json:"level"`           // info | warn | error
	Source     string    `gorm:"size:32;not null;index" json:"source"`         // ai_worker | reaper | polish | provider | segment ...
	Message    string    `gorm:"type:text;not null" json:"message"`
	FieldsJSON string    `gorm:"type:text" json:"fields_json"`                 // optional structured context (JSON object)
	JobID      *uint     `gorm:"index" json:"job_id,omitempty"`                // optional: the job this event concerns
	EpisodeID  *uint     `gorm:"index" json:"episode_id,omitempty"`            // optional: resolved at enrich time too
	CourseID   *uint     `json:"course_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// AIConfig is the parsed form of Course.AIConfigJSON. Add new fields here to
// extend the course's AI configuration WITHOUT a DB migration — the whole
// config is stored as one JSON blob (forward-compatible, same pattern as
// Question.Scoring). Each field maps to a real config knob consumed by some AI
// capability (Whisper, summary, quiz, advice).
type AIConfig struct {
	// WhisperHint 喂 Whisper 字幕转录的 initial_prompt(术语列表、口音、学科风格句)。
	// transcriber.py 会截断到 240 字。
	WhisperHint string `json:"whisper_hint,omitempty"`
	// SummaryHint 喂 summary agent:总结的风格/侧重点(如"侧重开局原理""多举例题")。
	SummaryHint string `json:"summary_hint,omitempty"`
	// QuizHint 喂 quiz agent:题型偏好/难度/出题指引(只留出题相关,术语移到 TermDict)。
	QuizHint string `json:"quiz_hint,omitempty"`
	// AdviceHint 喂 advice agent:建议的风格/侧重点(如"象棋重实战练习""数学重计算巩固")。
	AdviceHint string `json:"advice_hint,omitempty"`
	// TermDict 横切给 summary/quiz/advice 三个 agent:术语纠错字典(车→居、通分→同分)。
	// 输出时按此纠正字幕同音错字。横切属性:课程级 + 学科级合并(课程可能有学科通用之外的术语)。
	TermDict string `json:"term_dict,omitempty"`
	// Future knobs go here, e.g.:
	//   DifficultyBias  string  `json:"difficulty_bias,omitempty"`  // easy|medium|hard
	//   QuestionTypeMix map[string]float64 `json:"question_type_mix,omitempty"`
	//   Language        string  `json:"language,omitempty"`
	// Adding any of them is a code-only change — no ALTER TABLE.
}

// AIConfig parses Course.AIConfigJSON into an AIConfig. Returns the zero value
// on parse error or empty storage (callers treat zero-value fields as "unset").
// Safe to call on hot paths; the JSON is small (a few hundred bytes typical).
func (c Course) AIConfig() AIConfig {
	if strings.TrimSpace(c.AIConfigJSON) == "" {
		return AIConfig{}
	}
	var cfg AIConfig
	if err := json.Unmarshal([]byte(c.AIConfigJSON), &cfg); err != nil {
		return AIConfig{} // malformed → treat as unset; admin can re-save to fix
	}
	return cfg
}

// SetAIConfig serializes cfg into AIConfigJSON. Used by the service layer when
// saving a course (admin form writes the two textareas → service builds an
// AIConfig → calls this). Kept on the struct so the encoding rule lives next
// to the field, not in the service.
func (c *Course) SetAIConfig(cfg AIConfig) {
	if cfg == (AIConfig{}) {
		c.AIConfigJSON = "" // empty config → empty storage (not "{}")
		return
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return // unreachable for this struct (only strings)
	}
	c.AIConfigJSON = string(out)
}

// AIConfig parses Subject.AIConfigJSON into an AIConfig. Returns the zero value
// on parse error or empty storage (callers treat zero-value fields as "unset").
// Mirrors Course.AIConfig() — same forward-compatible JSON-blob pattern, so a
// custom subject can carry the same default prompts (whisper/summary/quiz/advice/
// term-dict) that its courses fall back to when they don't override a field.
func (s Subject) AIConfig() AIConfig {
	if strings.TrimSpace(s.AIConfigJSON) == "" {
		return AIConfig{}
	}
	var cfg AIConfig
	if err := json.Unmarshal([]byte(s.AIConfigJSON), &cfg); err != nil {
		return AIConfig{} // malformed → treat as unset; admin can re-save to fix
	}
	return cfg
}

// SetAIConfig serializes cfg into Subject.AIConfigJSON. Used by the service
// layer when saving a subject (admin form → service builds an AIConfig → calls
// this). Kept on the struct so the encoding rule lives next to the field.
func (s *Subject) SetAIConfig(cfg AIConfig) {
	if cfg == (AIConfig{}) {
		s.AIConfigJSON = "" // empty config → empty storage (not "{}")
		return
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return // unreachable for this struct (only strings)
	}
	s.AIConfigJSON = string(out)
}

// EffectiveWhisperHint: Course.AIConfig().WhisperHint 优先 → Subject.AIConfig().WhisperHint。
// 历史:曾有三层 fallback 到 deprecated AIHint 列,2026-07-27 清数据重部署时 AIHint 列
// 一并删除,此方法不再有第三层兜底(新库所有 course 的 whisper_hint 都在 AIConfigJSON 里)。
func (c Course) EffectiveWhisperHint(subject Subject) string {
	if h := strings.TrimSpace(c.AIConfig().WhisperHint); h != "" {
		return h
	}
	return strings.TrimSpace(subject.AIConfig().WhisperHint)
}

// EffectiveSummaryHint: Course > Subject > ""
func (c Course) EffectiveSummaryHint(subject Subject) string {
	if h := strings.TrimSpace(c.AIConfig().SummaryHint); h != "" {
		return h
	}
	return strings.TrimSpace(subject.AIConfig().SummaryHint)
}

// EffectiveQuizHint: Course > Subject(曾有三层 fallback 到 AIHint,已删)
func (c Course) EffectiveQuizHint(subject Subject) string {
	if h := strings.TrimSpace(c.AIConfig().QuizHint); h != "" {
		return h
	}
	return strings.TrimSpace(subject.AIConfig().QuizHint)
}

// EffectiveAdviceHint: Course > Subject > ""
func (c Course) EffectiveAdviceHint(subject Subject) string {
	if h := strings.TrimSpace(c.AIConfig().AdviceHint); h != "" {
		return h
	}
	return strings.TrimSpace(subject.AIConfig().AdviceHint)
}

// EffectiveTermDict: Course + Subject 合并(课程级追加到学科级后面),不是覆盖。
// 课程可能有学科通用之外的专有术语,合并让两者都生效。
func (c Course) EffectiveTermDict(subject Subject) string {
	subjDict := strings.TrimSpace(subject.AIConfig().TermDict)
	courseDict := strings.TrimSpace(c.AIConfig().TermDict)
	if courseDict == "" {
		return subjDict
	}
	if subjDict == "" {
		return courseDict
	}
	return subjDict + "\n" + courseDict
}
