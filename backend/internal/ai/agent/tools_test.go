package agent

import (
	"context"
	"errors"
	"testing"

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
	cases := []struct{ in, want string }{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"结果如下: {\"a\":1} 完毕", `{"a":1}`},
		{"no braces here", "no braces here"},
	}
	for _, c := range cases {
		if got := extractJSONObject(c.in); got != c.want {
			t.Errorf("extractJSONObject(%q) = %q, want %q", c.in, got, c.want)
		}
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
