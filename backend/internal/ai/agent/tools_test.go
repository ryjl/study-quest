package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"studyquest/backend/internal/model"
)

// --- fakes for tool tests ---

type fakeToolDeps struct {
	chunks   []model.ContentChunk
	episode  *model.Episode
	course   *model.Course
	summary  *model.AISummary
	chunkErr error
	// advice 工具集专用字段(Phase C)。零值时工具返回空 observation,quiz 测试用不到。
	courseMasteries  []model.KnowledgeMemory
	subjectMasteries []model.KnowledgeMemory
	userCourses      []uint
	// course summary 工具集专用字段(Phase D)。零值时工具返回"暂无 episode"提示。
	// courseEpisodes 是按 courseID 查 episode 列表的返回值(course summary agent 遍历用);
	// episodesByID 让 get_episode_summary 工具按 episode_id 参数返回不同的 episode + summary。
	courseEpisodes []model.Episode
	episodesByID   map[uint]*model.Episode
	summariesByID  map[uint]*model.AISummary
}

func (f *fakeToolDeps) ListChunks(episodeID uint) ([]model.ContentChunk, error) {
	return f.chunks, f.chunkErr
}
func (f *fakeToolDeps) GetEpisode(episodeID uint) (*model.Episode, error) {
	// 优先按 id map 返回(course summary 工具按 episode_id 参数查不同 episode);
	// 没配置 map 时回退到单值字段(quiz/advice 测试的默认行为)。
	if f.episodesByID != nil {
		if ep, ok := f.episodesByID[episodeID]; ok {
			return ep, nil
		}
		return nil, nil
	}
	return f.episode, nil
}
func (f *fakeToolDeps) GetCourse(courseID uint) (*model.Course, error) {
	return f.course, nil
}
func (f *fakeToolDeps) GetSummary(episodeID uint) (*model.AISummary, error) {
	// 同 GetEpisode:优先按 id map 返回(course summary 测试需要不同 episode 不同 summary)。
	if f.summariesByID != nil {
		if s, ok := f.summariesByID[episodeID]; ok {
			return s, nil
		}
		return nil, nil
	}
	return f.summary, nil
}
// advice 工具集方法:满足 ToolDeps 接口(quiz 测试不会调用,返回 fake 字段即可)。
func (f *fakeToolDeps) ListUserCourses(userID uint) ([]uint, error) {
	return f.userCourses, nil
}
func (f *fakeToolDeps) GetCourseMasteries(userID, courseID uint) ([]model.KnowledgeMemory, error) {
	return f.courseMasteries, nil
}
func (f *fakeToolDeps) GetSubjectMasteries(userID, subjectID uint) ([]model.KnowledgeMemory, error) {
	return f.subjectMasteries, nil
}
// course summary 工具集方法(Phase D):满足 ToolDeps 接口。返回 fake 字段,quiz/advice
// 测试用不到(它们不会调到这个方法)。
func (f *fakeToolDeps) ListCourseEpisodes(courseID uint) ([]model.Episode, error) {
	return f.courseEpisodes, nil
}

type fakeEmbedder struct{ vecs [][]float32 }

func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return f.vecs, nil
}

func ptrInt(v int) *int { return &v }

// --- tests ---

func TestToolboxUnknownToolReturnsErrorString(t *testing.T) {
	tb := NewQuizToolbox(&fakeToolDeps{}, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{}, 1, 1, 1)
	out, err := tb.Execute(context.Background(), "does_not_exist", "{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" || !contains(out, "不存在") {
		t.Errorf("expected error-string observation, got %q", out)
	}
}

func TestToolboxSpecsCount(t *testing.T) {
	tb := NewQuizToolbox(&fakeToolDeps{}, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{}, 1, 1, 1)
	if len(tb.Specs()) != 4 {
		t.Errorf("expected 4 tool specs, got %d", len(tb.Specs()))
	}
	names := tb.Names()
	for _, want := range []string{"get_episode_info", "get_related_chunks", "get_user_mastery", "search_subtitles"} {
		found := false
		for _, got := range names {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing tool %q in %v", want, names)
		}
	}
}

func TestGetEpisodeInfoRichMetadata(t *testing.T) {
	deps := &fakeToolDeps{
		episode: &model.Episode{
			Title:             "分数加减法",
			VideoRelativePath: "math/grade5/第3讲_分数加减法.mp4",
			DurationSeconds:   ptrInt(1860),
			CourseID:          10,
		},
		course: &model.Course{
			Title:   "五年级数学",
			Subject: model.Subject{Label: "数学"},
			Tags:    []model.Tag{{Label: "必修"}, {Label: "思维训练"}},
		},
		summary: &model.AISummary{SummaryJSON: `{"headline":"分数加减法","concepts":["通分","公分母"],"key_points":["异分母先通分"]}`},
	}
	deps.course.SetAIConfig(model.AIConfig{QuizHint: "通分、约分术语"})
	tb := NewQuizToolbox(deps, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{}, 1, 1, 10)
	out, err := tb.Execute(context.Background(), "get_episode_info", "{}")
	if err != nil {
		t.Fatal(err)
	}
	// file name extracted from path
	checks := []string{"分数加减法", "第3讲_分数加减法.mp4", "数学", "必修", "通分", "公分母", "异分母先通分", "五年级数学"}
	for _, c := range checks {
		if !contains(out, c) {
			t.Errorf("get_episode_info output missing %q\noutput:\n%s", c, out)
		}
	}
}

func TestSearchSubtitlesTopK(t *testing.T) {
	startA, startB := 0, 120
	deps := &fakeToolDeps{
		chunks: []model.ContentChunk{
			{ChunkIndex: 0, StartTime: &startA, Text: "今天讲通分", Embedding: `[0,1]`},
			{ChunkIndex: 1, StartTime: &startB, Text: "公分母的求法", Embedding: `[1,0]`},
		},
	}
	emb := &fakeEmbedder{vecs: [][]float32{{1, 0}}} // query matches chunk 1 best
	tb := NewQuizToolbox(deps, NewMemoryStore(&fakeMemoryRepo{}), emb, 1, 1, 1)
	out, err := tb.Execute(context.Background(), "search_subtitles", `{"query":"公分母"}`)
	if err != nil {
		t.Fatal(err)
	}
	// chunk 1 (sim=1) should rank first, chunk 0 (sim=0) second
	if !contains(out, "公分母的求法") {
		t.Errorf("expected best match '公分母的求法' in output:\n%s", out)
	}
}

func TestSearchSubtitlesMissingQuery(t *testing.T) {
	tb := NewQuizToolbox(&fakeToolDeps{}, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{}, 1, 1, 1)
	out, err := tb.Execute(context.Background(), "search_subtitles", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(out, "缺少 query") {
		t.Errorf("expected missing-query error, got %q", out)
	}
}

func TestGetUserMasteryEmptyForNewStudent(t *testing.T) {
	tb := NewQuizToolbox(&fakeToolDeps{}, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{}, 1, 1, 1)
	out, err := tb.Execute(context.Background(), "get_user_mastery", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(out, "无答题记录") {
		t.Errorf("expected new-student message, got %q", out)
	}
}

func TestGetUserMasteryListsWeaknesses(t *testing.T) {
	repo := &fakeMemoryRepo{rows: []model.KnowledgeMemory{
		{ChunkID: 5, Mastery: 0.2, CorrectCount: 1, WrongCount: 4},
		{ChunkID: 6, Mastery: 0.9, CorrectCount: 8, WrongCount: 0},
	}}
	tb := NewQuizToolbox(&fakeToolDeps{}, NewMemoryStore(repo), &fakeEmbedder{}, 1, 1, 1)
	out, err := tb.Execute(context.Background(), "get_user_mastery", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(out, "弱点") {
		t.Errorf("expected weakness marker for mastery 0.2:\n%s", out)
	}
}

func TestGetRelatedChunks(t *testing.T) {
	start := 300
	deps := &fakeToolDeps{
		chunks: []model.ContentChunk{
			{ChunkIndex: 3, StartTime: &start, Text: "通分的完整内容"},
		},
	}
	tb := NewQuizToolbox(deps, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{}, 1, 1, 1)
	out, err := tb.Execute(context.Background(), "get_related_chunks", `{"chunk_index":3}`)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(out, "通分的完整内容") {
		t.Errorf("expected chunk text, got %q", out)
	}
}

func TestGetRelatedChunksNotFound(t *testing.T) {
	deps := &fakeToolDeps{chunks: []model.ContentChunk{{ChunkIndex: 1}}}
	tb := NewQuizToolbox(deps, NewMemoryStore(&fakeMemoryRepo{}), &fakeEmbedder{}, 1, 1, 1)
	out, _ := tb.Execute(context.Background(), "get_related_chunks", `{"chunk_index":99}`)
	if !contains(out, "未找到") {
		t.Errorf("expected not-found message, got %q", out)
	}
}

func TestExtractJSONObject(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"balanced", `{"a":1}`, `{"a":1}`},
		{"fenced", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"prose-wrapped", "结果如下: {\"a\":1} 完毕", `{"a":1}`},
		{"no-braces", "no braces here", "no braces here"},
		// 截断兜底:走到末尾仍未平衡时,补全缺失的闭符号。这是 max_tokens 砍断输出
		// 的真实场景(截断点常落在中文 UTF-8 多字节字符中间,报 invalid character 'é')。
		// 补全后能被 json.Unmarshal 解析,救回前面已写完整的字段/题目。
		{"truncated-object", `{"a":1`, `{"a":1}`},
		{"truncated-nested", `{"a":{"b":1`, `{"a":{"b":1}}`},
		{"truncated-array", `{"q":["x","y"`, `{"q":["x","y"]}`},
		{"truncated-string-value", `{"a":"hel`, `{"a":"hel"}`},
	}
	for _, c := range cases {
		if got := extractJSONObject(c.in); got != c.want {
			t.Errorf("extractJSONObject(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestExtractJSONObjectTruncatedUTF8MidChar v2 UTF-8 字节截断盲区补测。
// MaxTokens 截断点可能落在一个 3 字节中文汉字的中间(切了 1-2 字节),s[start:] 起点带
// 半个 UTF-8 序列会让后续 json.Unmarshal 报 "invalid UTF-8" 而非真正的语法错误。
// ToValidUTF8 把残缺字节剔除,保证喂给 Unmarshal 的是合法 UTF-8。
//
// 场景:LLM 正在写 {"a":"老师讲到中... 字符串值没闭合就被砍断,且砍断点恰好落在
// 「中」字(\xe4\xb8\xad)的第 2 字节后(留了 \xe4\xb8 两个残缺字节)。
// 期望:残缺字节剔除 + 字符串补闭合 + object 补闭合 → {"a":"老师讲到"}
func TestExtractJSONObjectTruncatedUTF8MidChar(t *testing.T) {
	// 构造输入:合法前缀 + 残缺 UTF-8 尾字节(「中」的前两字节 \xe4\xb8,第三字节被砍)。
	input := "{\"a\":\"老师讲到" + string([]byte{0xe4, 0xb8})
	recovered := extractJSONObject(input)

	// 关键断言 1:recovered 是合法 UTF-8(没有残缺字节)。
	if !utf8.ValidString(recovered) {
		t.Errorf("recovered is not valid UTF-8: % x", []byte(recovered))
	}
	// 关键断言 2:recovered 能被 json.Unmarshal 解析(不报 invalid UTF-8)。
	var v struct {
		A string `json:"a"`
	}
	if err := json.Unmarshal([]byte(recovered), &v); err != nil {
		t.Fatalf("recovered JSON unparseable: %v\nrecovered=%q", err, recovered)
	}
	// 关键断言 3:前缀的合法中文保留,残缺字节被剔除而非留作乱码。
	if !strings.Contains(v.A, "老师讲到") {
		t.Errorf("expected 老师讲到 in salvaged value, got %q (recovered=%s)", v.A, recovered)
	}
}

// TestExtractJSONObjectTruncationRecoverable 验证截断兜底产出的 JSON 能被标准库
// json.Unmarshal 解析——即救回来的不是「仍是半截」,而是结构上完整的数据。这是
// parseQuizGeneration 在被截断的输出上能救回前面题目的前提。
func TestExtractJSONObjectTruncationRecoverable(t *testing.T) {
	// 模拟被 max_tokens 砍断的 quiz 输出:第一道完整、第二道只写了半个 stem。
	truncated := `{"questions":[{"stem":"Q1","options":["A"],"answer":0},{"stem":"Q2`
	recovered := extractJSONObject(truncated)
	var v struct {
		Questions []struct {
			Stem string `json:"stem"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(recovered), &v); err != nil {
		t.Fatalf("recovered JSON still unparseable: %v\nrecovered=%s", err, recovered)
	}
	if len(v.Questions) < 1 || v.Questions[0].Stem != "Q1" {
		t.Errorf("expected to salvage Q1, got %+v (recovered=%s)", v.Questions, recovered)
	}
}

// TestParseQuizGenerationRecoversTruncatedQuiz 验证 parseQuizGeneration 在被截断的
// quiz 输出上能救回前面完整的题目、丢弃最后一道写了一半的残题——而不是整次失败。
// 对应 episode 31 失败场景:JSON 在 4000 tokens 附近被砍断在 UTF-8 多字节字符中间。
func TestParseQuizGenerationRecoversTruncatedQuiz(t *testing.T) {
	// 8 道完整题 + 第 9 道只写了字段名没写值(模拟末尾截断)。
	// extractJSONObject 会补全括号,json.Unmarshal 解析后第 9 道 stem 为空,
	// parseQuizGeneration 的「drop empty stem」逻辑会丢弃它,保留前 8 道。
	var b strings.Builder
	b.WriteString(`{"questions":[`)
	for i := 0; i < 8; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"type":"choice","stem":"题`)
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(`","options":["A","B","C","D"],"answer":0,"explanation":"e"}`)
	}
	// 第 9 道:写了对象开始但字段值被截断。
	b.WriteString(`,{"type":"choice","stem":"题`)
	// 故意在这里截断——没有闭合 stem 字符串、对象、数组、外层对象。
	draft, err := parseQuizGeneration(b.String())
	if err != nil {
		t.Fatalf("parseQuizGeneration should recover from truncation, got err: %v", err)
	}
	if len(draft.Questions) != 8 {
		t.Errorf("expected 8 salvaged questions (drop the truncated 9th), got %d", len(draft.Questions))
	}
}

// contains is a tiny substring helper to avoid pulling strings.Contains into
// every test signature. (Test-only; not exported.)
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ensure errors import is used (fakeEmbedder could return errors in extended
// tests later).
var _ = errors.New
