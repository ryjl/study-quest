package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// Tools are the agent's callable actions during the ReAct loop. The model sees
// their specs (name + description + parameter schema) and decides which to call
// to gather context before producing its final answer (the quiz). Each tool is
// backed by a Go function that reads real data (RAG corpus, user memory, lesson
// metadata) and returns a TEXT observation the model reasons over.
//
// This file defines: the tool spec table (what the model sees), the execution
// dispatch (what runs when the model calls one), and the four tools themselves.

// ToolDeps is the read-side data access the tools need. It's a narrow interface
// over the repos so the agent package stays testable with fakes (no GORM).
// Methods that load by id return (nil, nil) when not found — tools translate
// that into an "empty" observation rather than an error, so the agent can reason
// about missing data ("no summary yet") instead of crashing.
//
// 该接口同时服务 quiz 工具集(NewQuizToolbox)和 advice 工具集(NewAdviceToolbox)。
// advice 独有的方法(ListUserCourses / GetCourseMasteries / GetSubjectMasteries)对
// quiz 无意义,quiz 路径的 agentToolDeps adapter 实现它们时返回空(nil/error)即可
// ——quiz agent 永远不会调用 advice 工具,所以这些空实现不会被触发,但接口要满足才能
// 编译通过(见 ai_service_quiz.go 的 agentToolDeps)。
type ToolDeps interface {
	// Chunks for an episode's subtitle track, ordered by index (chronological).
	ListChunks(episodeID uint) ([]model.ContentChunk, error)
	// Episode + its course + subject, for get_episode_info's metadata bundle.
	GetEpisode(episodeID uint) (*model.Episode, error)
	GetCourse(courseID uint) (*model.Course, error)
	// The generated summary (concepts/key_points), if any — a rich starting
	// point for deciding what to quiz on.
	GetSummary(episodeID uint) (*model.AISummary, error)

	// ── advice 工具集专用(Phase C)──
	// ListUserCourses 返回学生当前被授权的所有课程 id(用于 list_user_courses 工具,
	// 让 advice agent 知道学生有几门课、可以建议从哪门开始复习)。走 userRepo.GetAccessList。
	ListUserCourses(userID uint) ([]uint, error)
	// GetCourseMasteries / GetSubjectMasteries 是跨课程/科目聚合的 mastery 读取,
	// advice agent 用它们做"整门课 / 整个科目的弱点分析"。返回 nil/empty 时由工具
	// 翻译成"暂无答题记录"的 observation,agent 据此降级(基于内容给建议)。
	GetCourseMasteries(userID, courseID uint) ([]model.KnowledgeMemory, error)
	GetSubjectMasteries(userID, subjectID uint) ([]model.KnowledgeMemory, error)
}

// embedder is the minimal slice of ai.Embedder the search tool needs.
type embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Toolbox is the registry of tools available to one agent run. It holds the
// execution functions (keyed by tool name) and the specs advertised to the
// model. The agent loop calls Execute to dispatch a model-chosen tool_call.
//
// Every tool is scoped to ONE (episode, user, course) triple, set at
// construction. The model can only pass query/chunk_index style args; the
// episode/user/course are server-enforced — a tool_call can never request
// another student's data or another lesson's chunks.
type Toolbox struct {
	deps      ToolDeps
	memory    *MemoryStore
	embedder  embedder
	episodeID uint
	userID    uint
	courseID  uint
	funcs     map[string]toolFunc
	specs     []ai.Tool
}

// toolFunc executes one tool. The raw arguments JSON from the model is passed
// through; each tool parses its own args (loosely — the model's args aren't
// guaranteed valid). Returns a text observation the model reads next.
type toolFunc func(ctx context.Context, args string) (string, error)

// searchConfig holds knobs for the search tool. Defaults are tuned for subtitle
// chunks: top 5 keeps the model's context window manageable while covering the
// most relevant passages.
type searchConfig struct {
	topK int
}

// NewQuizToolbox builds the toolbox the quizzer agent uses. episodeID/userID/
// courseID scope every tool to the lesson + student being quizzed.
func NewQuizToolbox(deps ToolDeps, memory *MemoryStore, emb embedder, episodeID, userID, courseID uint) *Toolbox {
	tb := &Toolbox{
		deps:      deps,
		memory:    memory,
		embedder:  emb,
		episodeID: episodeID,
		userID:    userID,
		courseID:  courseID,
		funcs:     map[string]toolFunc{},
	}
	cfg := searchConfig{topK: 5}
	tb.register("search_subtitles", searchSubtitlesSpec, func(ctx context.Context, args string) (string, error) {
		return tb.runSearchSubtitles(ctx, tb.episodeID, cfg, args)
	})
	tb.register("get_user_mastery", userMasterySpec, func(ctx context.Context, args string) (string, error) {
		return tb.runGetUserMastery(ctx, tb.userID, tb.episodeID)
	})
	tb.register("get_episode_info", episodeInfoSpec, func(ctx context.Context, args string) (string, error) {
		return tb.runGetEpisodeInfo(ctx, tb.episodeID)
	})
	tb.register("get_related_chunks", relatedChunksSpec, func(ctx context.Context, args string) (string, error) {
		return tb.runGetRelatedChunks(args)
	})
	return tb
}

func (t *Toolbox) register(name string, spec ai.Tool, fn toolFunc) {
	t.funcs[name] = fn
	t.specs = append(t.specs, spec)
}

// Specs returns the tool advertisements sent to the model in the Chat request.
func (t *Toolbox) Specs() []ai.Tool { return t.specs }

// Names returns the registered tool names (for diagnostics / tests).
func (t *Toolbox) Names() []string {
	out := make([]string, 0, len(t.funcs))
	for n := range t.funcs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Execute runs the named tool with the model-supplied args JSON. An unknown
// tool name returns an ERROR STRING (not a Go error) so the model can recover —
// crashing the whole agent run because the model hallucinated a tool name would
// be brittle. The model sees "tool 'x' does not exist" and self-corrects.
func (t *Toolbox) Execute(ctx context.Context, name, args string) (string, error) {
	fn, ok := t.funcs[name]
	if !ok {
		return fmt.Sprintf("错误:工具 '%s' 不存在。可用工具: %s", name, strings.Join(t.Names(), ", ")), nil
	}
	return fn(ctx, args)
}

// ---------------------------------------------------------------------------
// Tool: search_subtitles
// ---------------------------------------------------------------------------

// searchSubtitlesSpec is the tool advertisement for search_subtitles.
var searchSubtitlesSpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "search_subtitles",
		Description: "在当前课时的字幕中按语义检索相关片段(向量相似度)。用于找出讲某个知识点的字幕位置,出题可锚定到具体视频时间点。返回带时间戳的字幕片段。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "要检索的知识点/关键词,中文,如'分数加减法的通分'",
				},
			},
			"required": []string{"query"},
		},
	},
}

func (t *Toolbox) runSearchSubtitles(ctx context.Context, episodeID uint, cfg searchConfig, args string) (string, error) {
	query := parseStringArg(args, "query")
	if query == "" {
		return "错误:缺少 query 参数", nil
	}
	chunks, err := t.deps.ListChunks(episodeID)
	if err != nil {
		return "", fmt.Errorf("search_subtitles: load chunks: %w", err)
	}
	if len(chunks) == 0 {
		return "未找到字幕切片(本课时可能尚未切片)。", nil
	}
	// Embed the query and find the top-K chunks by cosine similarity.
	vecs, err := t.collectEmbeddings(chunks)
	if err != nil {
		return "", fmt.Errorf("search_subtitles: parse embeddings: %w", err)
	}
	qVecs, err := t.embedder.Embed(ctx, []string{query})
	if err != nil || len(qVecs) == 0 {
		return "", fmt.Errorf("search_subtitles: embed query: %w", err)
	}
	top := ai.TopK(qVecs[0], vecs, cfg.topK)
	var b strings.Builder
	fmt.Fprintf(&b, "检索到 %d 条相关字幕片段(按相关度降序):\n\n", len(top))
	for rank, idx := range top {
		ch := chunks[idx]
		ts := "无时间戳"
		if ch.StartTime != nil {
			ts = mmss(*ch.StartTime)
		}
		fmt.Fprintf(&b, "[%d] (时间 %s, 片段#%d) %s\n\n", rank+1, ts, ch.ChunkIndex, truncate(ch.Text, 400))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// collectEmbeddings parses the stored JSON vectors on chunks into a [][]float32
// for TopK. Chunks without an embedding get a zero vector (they'll score 0 and
// never surface as a top match — desired for unretrievable chunks).
func (t *Toolbox) collectEmbeddings(chunks []model.ContentChunk) ([][]float32, error) {
	out := make([][]float32, len(chunks))
	for i, ch := range chunks {
		v, err := ai.ParseEmbedding(ch.Embedding)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Tool: get_user_mastery
// ---------------------------------------------------------------------------

var userMasterySpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_user_mastery",
		Description: "查询当前学生在这节课各知识点的掌握度(mastery 0-1)和答题对错次数。用于发现学生的弱点,出题时优先针对掌握度低的知识点。新学生(无答题记录)返回空。",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

func (t *Toolbox) runGetUserMastery(ctx context.Context, userID, episodeID uint) (string, error) {
	rows, err := t.memory.Masteries(ctx, userID, episodeID)
	if err != nil {
		return "", fmt.Errorf("get_user_mastery: %w", err)
	}
	if len(rows) == 0 {
		return "该学生本课时暂无答题记录(新学生)。请基于课程内容出题,覆盖主要知识点。", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "学生掌握度(弱点优先,mastery 越低越需要加强):\n\n")
	for _, r := range rows {
		verdict := "已掌握"
		if r.Mastery < 0.4 {
			verdict = "★弱点"
		} else if r.Mastery < 0.7 {
			verdict = "一般"
		}
		fmt.Fprintf(&b, "- 知识点片段#%d: mastery=%.2f (%s) | 对%d 错%d | 最近复习:%s\n",
			r.ChunkID, r.Mastery, verdict, r.CorrectCount, r.WrongCount, formatReviewTime(r.LastReviewed))
	}
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// Tool: get_episode_info — RICH metadata bundle (file name, subject, summary...)
// ---------------------------------------------------------------------------

var episodeInfoSpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_episode_info",
		Description: "查询当前课时的元信息:标题、文件名(常含章节信息如'第3讲_分数加减法')、时长、科目、标签、适用年级、术语提示、以及已生成的AI总结(核心概念/要点)。帮你快速锁定这节课讲什么,决定考哪些知识点。",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

func (t *Toolbox) runGetEpisodeInfo(ctx context.Context, episodeID uint) (string, error) {
	_ = ctx
	ep, err := t.deps.GetEpisode(episodeID)
	if err != nil {
		return "", fmt.Errorf("get_episode_info: load episode: %w", err)
	}
	if ep == nil {
		return "未找到课时信息。", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "课时信息:\n")
	fmt.Fprintf(&b, "- 标题: %s\n", ep.Title)
	// File name is high-signal: many netdisk courses name files with the lesson
	// topic ("第3讲_分数加减法.mp4"). Extract the base name from both the import
	// path and the original multi-layer path.
	fmt.Fprintf(&b, "- 文件名: %s\n", filepath.Base(ep.VideoRelativePath))
	if ep.OriginalRelativePath != "" && filepath.Base(ep.OriginalRelativePath) != filepath.Base(ep.VideoRelativePath) {
		fmt.Fprintf(&b, "- 原始文件名: %s\n", filepath.Base(ep.OriginalRelativePath))
	}
	if ep.DurationSeconds != nil {
		fmt.Fprintf(&b, "- 时长: %s\n", mmss(*ep.DurationSeconds))
	}

	// Course context: subject (数学/语文/...), tags, grades, AI hint.
	if course, err := t.deps.GetCourse(ep.CourseID); err == nil && course != nil {
		fmt.Fprintf(&b, "- 课程: %s\n", course.Title)
		if course.Subject.Label != "" {
			fmt.Fprintf(&b, "- 科目: %s\n", course.Subject.Label)
		}
		if len(course.Tags) > 0 {
			labels := make([]string, 0, len(course.Tags))
			for _, tg := range course.Tags {
				labels = append(labels, tg.Label)
			}
			fmt.Fprintf(&b, "- 标签: %s\n", strings.Join(labels, "、"))
		}
		if len(course.Grades) > 0 {
			fmt.Fprintf(&b, "- 适用年级: %s\n", gradesLabel(course.Grades))
		}
		if course.AIHint != "" {
			fmt.Fprintf(&b, "- 术语提示: %s\n", course.AIHint)
		}
	}

	// The generated summary is the single richest signal for "what to quiz": it
	// already distilled the lesson into concepts + key points.
	if sum, err := t.deps.GetSummary(episodeID); err == nil && sum != nil {
		if parsed, perr := parseSummaryForTools(sum.SummaryJSON); perr == nil {
			if parsed.Headline != "" {
				fmt.Fprintf(&b, "\nAI总结:\n- 概括: %s\n", parsed.Headline)
			}
			if len(parsed.Concepts) > 0 {
				fmt.Fprintf(&b, "- 核心概念: %s\n", strings.Join(parsed.Concepts, "、"))
			}
			if len(parsed.KeyPoints) > 0 {
				fmt.Fprintf(&b, "- 要点:\n")
				for _, kp := range parsed.KeyPoints {
					fmt.Fprintf(&b, "  · %s\n", kp)
				}
			}
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// gradesLabel renders a course's grade set to a readable string.
func gradesLabel(grades []model.CourseGrade) string {
	out := make([]string, 0, len(grades))
	for _, g := range grades {
		out = append(out, string(g.Grade))
	}
	return strings.Join(out, "、")
}

// parseSummaryForTools is a lightweight decode of the stored summary JSON (we
// only need concepts/key_points/headline here, not the full SummaryResult). Kept
// local to avoid an import cycle quirk and because the tool tolerates absence.
func parseSummaryForTools(raw string) (struct {
	Headline  string   `json:"headline"`
	KeyPoints []string `json:"key_points"`
	Concepts  []string `json:"concepts"`
}, error) {
	var s struct {
		Headline  string   `json:"headline"`
		KeyPoints []string `json:"key_points"`
		Concepts  []string `json:"concepts"`
	}
	err := json.Unmarshal([]byte(extractJSONObject(raw)), &s)
	return s, err
}

// ---------------------------------------------------------------------------
// Tool: get_related_chunks
// ---------------------------------------------------------------------------

var relatedChunksSpec = ai.Tool{
	Type: "function",
	Function: ai.ToolSpec{
		Name:        "get_related_chunks",
		Description: "按片段编号(chunk_index)读取该字幕片段的完整文本(含时间戳)。当你需要看某个知识点的完整上下文来出更精准的题时调用。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"chunk_index": map[string]any{
					"type":        "integer",
					"description": "字幕片段编号(search_subtitles 返回的 片段#N)",
				},
			},
			"required": []string{"chunk_index"},
		},
	},
}

func (t *Toolbox) runGetRelatedChunks(args string) (string, error) {
	idx := parseIntArg(args, "chunk_index")
	if idx < 0 {
		return "错误:缺少或无效的 chunk_index 参数", nil
	}
	chunks, err := t.deps.ListChunks(t.episodeID)
	if err != nil {
		return "", fmt.Errorf("get_related_chunks: load chunks: %w", err)
	}
	// Find the chunk with the requested index. Not all indices are contiguous
	// (a re-segment could leave gaps historically), so scan rather than index.
	for _, ch := range chunks {
		if ch.ChunkIndex == idx {
			ts := "无时间戳"
			if ch.StartTime != nil {
				ts = mmss(*ch.StartTime)
			}
			return fmt.Sprintf("(片段#%d, 时间 %s)\n%s", ch.ChunkIndex, ts, ch.Text), nil
		}
	}
	return fmt.Sprintf("未找到片段#%d。", idx), nil
}
