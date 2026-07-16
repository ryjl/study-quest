package agent

import (
	"context"
	"strings"
	"testing"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// course_summary_test.go: course summary agent 的单元测试。覆盖:
//   - NewCourseSummaryToolbox 注册的 2 个工具(Names/Specs);
//   - get_course_episodes 列出 episode(按顺序);
//   - get_episode_summary 按 episode_id 参数查(summary 版,带 episode_id 校验);
//   - get_episode_summary 拒绝别课程的 episode(scope 安全);
//   - CourseSummaryAgent.Generate 端到端(mock LLM 脚本化,验证 pre-seed + FinalText 直传);
//   - Generate 空 FinalText 报错(job 标 failed 的触发条件)。
//
// 复用 agent_test.go 的 mockLLM、toolCall 辅助;tools_test.go 的 fakeToolDeps(已扩展
// course summary 字段:courseEpisodes / episodesByID / summariesByID)。

// TestCourseSummaryToolboxRegistersAllTools 验证 NewCourseSummaryToolbox 注册了 2 个工具。
// 工具集是 course summary agent 的能力边界——少注册一个,agent 就缺一类查询能力。
func TestCourseSummaryToolboxRegistersAllTools(t *testing.T) {
	tb := NewCourseSummaryToolbox(&fakeToolDeps{}, 100)
	want := map[string]bool{
		"get_course_episodes": false,
		"get_episode_summary": false,
	}
	for _, n := range tb.Names() {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("course summary toolbox missing tool %q; registered: %v", name, tb.Names())
		}
	}
	if len(tb.Specs()) != len(want) {
		t.Errorf("course summary toolbox specs count = %d; want %d", len(tb.Specs()), len(want))
	}
}

// TestCourseSummaryGetCourseEpisodes 验证 get_course_episodes 按 deps.ListCourseEpisodes 返回
// 的顺序列出 episode(id + 标题)。pre-seed 已喂了列表,但 agent 想确认结构时仍可调本工具。
func TestCourseSummaryGetCourseEpisodes(t *testing.T) {
	deps := &fakeToolDeps{
		courseEpisodes: []model.Episode{
			{ID: 10, CourseID: 100, Title: "通分"},
			{ID: 11, CourseID: 100, Title: "异分母加减"},
		},
	}
	tb := NewCourseSummaryToolbox(deps, 100)
	out, err := tb.Execute(context.Background(), "get_course_episodes", "{}")
	if err != nil {
		t.Fatalf("get_course_episodes: %v", err)
	}
	if !strings.Contains(out, "通分") || !strings.Contains(out, "异分母加减") {
		t.Errorf("episodes missing titles; got:\n%s", out)
	}
	if !strings.Contains(out, "共 2 节") {
		t.Errorf("episodes count missing; got:\n%s", out)
	}
}

// TestCourseSummaryGetCourseEpisodesEmpty 无 episode 时返回降级提示,不报错。
func TestCourseSummaryGetCourseEpisodesEmpty(t *testing.T) {
	tb := NewCourseSummaryToolbox(&fakeToolDeps{}, 100)
	out, err := tb.Execute(context.Background(), "get_course_episodes", "{}")
	if err != nil {
		t.Fatalf("get_course_episodes: %v", err)
	}
	if !strings.Contains(out, "暂无 episode") {
		t.Errorf("empty episodes should hint; got:\n%s", out)
	}
}

// TestCourseSummaryGetEpisodeSummary 验证 get_episode_summary 按 episode_id 参数查 summary
// (course summary 版的关键差异:接受 episode_id 参数,不像 advice 版绑死 toolbox scope)。
// 验证:返回了 concepts / key_points / headline;按参数查到了正确的 episode。
func TestCourseSummaryGetEpisodeSummary(t *testing.T) {
	deps := &fakeToolDeps{
		episodesByID: map[uint]*model.Episode{
			10: {ID: 10, CourseID: 100, Title: "通分"},
		},
		summariesByID: map[uint]*model.AISummary{
			10: {SummaryJSON: `{"headline":"讲通分","concepts":["通分","公分母"],"key_points":["找最小公倍数"]}`},
		},
	}
	tb := NewCourseSummaryToolbox(deps, 100)
	out, err := tb.Execute(context.Background(), "get_episode_summary", `{"episode_id":10}`)
	if err != nil {
		t.Fatalf("get_episode_summary: %v", err)
	}
	for _, want := range []string{"通分", "公分母", "找最小公倍数", "讲通分"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q; got:\n%s", want, out)
		}
	}
}

// TestCourseSummaryGetEpisodeSummaryRejectsForeignCourse 验证 scope 安全:agent 传一个不属于
// 本课程的 episode_id 时,工具拒绝并返回错误 observation(不越权查别课程的 summary)。
// 这是工具集 server-enforced scope 的核心——和 quiz/advice 同一套安全模型。
func TestCourseSummaryGetEpisodeSummaryRejectsForeignCourse(t *testing.T) {
	deps := &fakeToolDeps{
		episodesByID: map[uint]*model.Episode{
			// episode 20 属于 course 200(不是 toolbox scope 的 100)。
			20: {ID: 20, CourseID: 200, Title: "别课程的课"},
		},
		summariesByID: map[uint]*model.AISummary{
			20: {SummaryJSON: `{"headline":"不该被看到"}`},
		},
	}
	tb := NewCourseSummaryToolbox(deps, 100)
	out, err := tb.Execute(context.Background(), "get_episode_summary", `{"episode_id":20}`)
	if err != nil {
		t.Fatalf("get_episode_summary: %v", err)
	}
	if !strings.Contains(out, "不属于本课程") {
		t.Errorf("foreign-course episode should be rejected; got:\n%s", out)
	}
	if strings.Contains(out, "不该被看到") {
		t.Errorf("foreign-course summary leaked; got:\n%s", out)
	}
}

// TestCourseSummaryGetEpisodeSummaryMissingID 缺 episode_id 参数时返回错误 observation。
func TestCourseSummaryGetEpisodeSummaryMissingID(t *testing.T) {
	tb := NewCourseSummaryToolbox(&fakeToolDeps{}, 100)
	out, err := tb.Execute(context.Background(), "get_episode_summary", "{}")
	if err != nil {
		t.Fatalf("get_episode_summary: %v", err)
	}
	if !strings.Contains(out, "缺少或无效的 episode_id") {
		t.Errorf("missing episode_id should error; got:\n%s", out)
	}
}

// ── CourseSummaryAgent.Generate 端到端(mock LLM)──

// TestCourseSummaryAgentGenerateFinalTextDirect 验证 agent 的 FinalText(自然语言)直接作为
// SummaryText 返回(不解析 JSON)。mockLLM 立即给最终答案(不调工具),agent 应返回该文本。
// 同时验证 pre-seed 把 episode 列表 + headline 塞进了 prompt(deps 提供 courseEpisodes +
// summariesByID,pre-seed 不报错即可)。
func TestCourseSummaryAgentGenerateFinalTextDirect(t *testing.T) {
	deps := &fakeToolDeps{
		courseEpisodes: []model.Episode{
			{ID: 10, CourseID: 100, Title: "通分"},
			{ID: 11, CourseID: 100, Title: "异分母加减"},
		},
		summariesByID: map[uint]*model.AISummary{
			10: {SummaryJSON: `{"headline":"讲通分的方法"}`},
			11: {SummaryJSON: `{"headline":"用通分做异分母加减"}`},
		},
	}
	// mockLLM:第一轮就给最终答案(自然语言课程总结,非 JSON)。
	llm := &mockLLM{responses: []*ai.ChatResponse{
		{Content: "这门课围绕分数运算展开。第 1 节引入通分,第 2 节用它处理异分母加减……", FinishReason: "stop"},
	}}
	// 用空 toolbox(不注册工具)验证 Generate 把 FinalText 透传;pre-seed 仍由 deps 提供。
	emptyTB := &Toolbox{funcs: map[string]toolFunc{}}
	gen := NewAgent(llm, "mock-model", emptyTB, AgentOpts{MaxSteps: 3, MaxTokens: 500})
	summarizer := NewCourseSummaryAgent(gen, deps)

	res, err := summarizer.Generate(context.Background(), CourseSummaryRequest{
		CourseID: 100, CourseTitle: "分数运算", Subject: "数学",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.SummaryText, "分数运算") {
		t.Errorf("SummaryText should pass through FinalText; got %q", res.SummaryText)
	}
}

// TestCourseSummaryAgentGenerateEmptyFinalTextErrors agent 给空 FinalText 时 Generate 应返回错误
// (而不是存一条空总结误导学生)。这是 job 标 failed 的触发条件。
func TestCourseSummaryAgentGenerateEmptyFinalTextErrors(t *testing.T) {
	deps := &fakeToolDeps{}
	llm := &mockLLM{responses: []*ai.ChatResponse{
		{Content: "", FinishReason: "stop"},
	}}
	emptyTB := &Toolbox{funcs: map[string]toolFunc{}}
	gen := NewAgent(llm, "mock-model", emptyTB, AgentOpts{MaxSteps: 3, MaxTokens: 500})
	summarizer := NewCourseSummaryAgent(gen, deps)

	_, err := summarizer.Generate(context.Background(), CourseSummaryRequest{
		CourseID: 100, CourseTitle: "测试课程",
	})
	if err == nil {
		t.Fatal("Generate with empty FinalText should error")
	}
	if !strings.Contains(err.Error(), "empty final answer") {
		t.Errorf("error should mention empty final answer; got %v", err)
	}
}

// TestCourseSummaryAgentGenerateCallsToolThenAnswers 验证 course summary agent 走完整的 ReAct
// loop:调一次工具(get_episode_summary)拿到 observation,再给最终答案。证明复用的 ReAct loop
// 在 course summary 场景下也工作(不是只能单次调用)。
func TestCourseSummaryAgentGenerateCallsToolThenAnswers(t *testing.T) {
	deps := &fakeToolDeps{
		courseEpisodes: []model.Episode{
			{ID: 10, CourseID: 100, Title: "通分"},
		},
		episodesByID: map[uint]*model.Episode{
			10: {ID: 10, CourseID: 100, Title: "通分"},
		},
		summariesByID: map[uint]*model.AISummary{
			10: {SummaryJSON: `{"headline":"通分的方法","concepts":["通分","公分母"]}`},
		},
	}
	// 用真实的 course summary toolbox(注册了工具),让模型能调 get_episode_summary。
	tb := NewCourseSummaryToolbox(deps, 100)
	llm := &mockLLM{responses: []*ai.ChatResponse{
		// 第一轮:模型要求调 get_episode_summary(传 episode_id=10)。
		{
			FinishReason: "tool_calls",
			ToolCalls:    []ai.ToolCall{toolCall("call-1", "get_episode_summary", `{"episode_id":10}`)},
		},
		// 第二轮:模型拿到 observation 后给最终课程总结。
		{Content: "这门课的通分是核心基础,建议先掌握公分母的求法……", FinishReason: "stop"},
	}}
	gen := NewAgent(llm, "mock-model", tb, AgentOpts{MaxSteps: 5, MaxTokens: 500})
	summarizer := NewCourseSummaryAgent(gen, deps)

	res, err := summarizer.Generate(context.Background(), CourseSummaryRequest{
		CourseID: 100, CourseTitle: "分数",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.SummaryText, "通分") {
		t.Errorf("SummaryText should reflect episode summary observation; got %q", res.SummaryText)
	}
	if len(res.Trace) < 2 {
		t.Errorf("trace should have >=2 steps (tool call + final); got %d", len(res.Trace))
	}
	if llm.calls != 2 {
		t.Errorf("LLM should be called twice (tool + answer); got %d calls", llm.calls)
	}
}

// TestParseHeadlineOnly 验证从 summary JSON 只取 headline 字段(宽松解析,容忍 prose/fences)。
func TestParseHeadlineOnly(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain json", `{"headline":"讲通分的方法","concepts":["x"]}`, "讲通分的方法"},
		{"fenced json", "```json\n{\"headline\":\"fenced\"}\n```", "fenced"},
		{"prose-wrapped", `好的,这是总结:{"headline":"wrapped"} 希望有帮助`, "wrapped"},
		{"empty", "", ""},
		{"missing field", `{"concepts":["x"]}`, ""},
		{"malformed", `not json at all`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseHeadlineOnly(tc.raw)
			if got != tc.want {
				t.Errorf("parseHeadlineOnly(%q) = %q; want %q", tc.raw, got, tc.want)
			}
		})
	}
}
