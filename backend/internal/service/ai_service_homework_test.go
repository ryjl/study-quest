package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// ai_service_homework_test.go — 课后作业卷 service 层测试。覆盖 plan §二 的必测项:
//   - EnqueueHomeworkForCourse:去重、批量入队、缺素材跳过
//   - runHomeworkJob:fake LLM 返回 fixture JSON → Homework/Section/Question 持久化 + AIRun 落库
//   - nil-safe:homeworkRepo 未注入返回 ErrHomeworkNotEnabled
//   - 重生成:已有 active 卷时再生成 → 旧卷 archived、新卷 Version 自增
//   - 失败路径:LLM 返回非 JSON / 空 → failJob 不崩
//
// fake LLM 范式照搬 ai_service_polish_test.go 的 fakePolishLLM(同样的 TEST-ONLY seam 思路,
// 但 homework 走 homeworkLLMOverride seam)。

// fakeHomeworkLLM 同 fakePolishLLM:脚本化 Chat 响应,每次调用 pop 一条。
type fakeHomeworkLLM struct {
	mu        sync.Mutex
	responses []fakeHomeworkResp
	calls     int
}

type fakeHomeworkResp struct {
	content string
	err     error
}

func (m *fakeHomeworkLLM) Chat(_ context.Context, _ ai.ChatRequest) (*ai.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.calls
	m.calls++
	if idx >= len(m.responses) {
		return nil, errors.New("fakeHomeworkLLM: no more scripted responses")
	}
	r := m.responses[idx]
	if r.err != nil {
		return nil, r.err
	}
	return &ai.ChatResponse{Content: r.content, FinishReason: "stop"}, nil
}
func (m *fakeHomeworkLLM) Ping(_ context.Context) error { return nil }
func (m *fakeHomeworkLLM) ProviderType() string         { return "fake-homework" }

// hwServiceIDs 测试 fixture 的关键 id。
type hwServiceIDs struct {
	subjectID, courseID, episodeID uint
}

// seedHomeworkServiceFixture 建最小链:subject(math)+ course + episode + 该 episode 的
// content chunks(作业素材)。返回 service(*aiService)+ repo + ids。
// 用 NewFileDB(非 :memory:):测试期间 worker goroutine 在并发跑,会用到连接池的其它连接,
// :memory: 每连接私有会导致 worker 看不到表(同 polishE2EEnv 的范式)。
func seedHomeworkServiceFixture(t *testing.T) (*aiService, repository.HomeworkRepository, hwServiceIDs) {
	t.Helper()
	db := testutil.NewFileDB(t)
	subjects := testutil.SeedSubjects(t, db)
	course := &model.Course{Title: "HW Svc Course", SubjectID: subjects["math"].ID}
	db.Create(course)
	episode := &model.Episode{Title: "HW Svc Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	db.Create(episode)
	// 灌几条 chunks(作业素材)。homework 需要本课 chunks 非空,否则 ErrHomeworkInsufficientMaterial。
	contentRepo := repository.NewAIContentRepository(db)
	for i := 0; i < 3; i++ {
		contentRepo.ReplaceChunksForEpisode(episode.ID, course.ID, "subtitle", []model.ContentChunk{
			{EpisodeID: episode.ID, CourseID: course.ID, SourceType: "subtitle", ChunkIndex: i, Text: "本课讲了分数加减法 " + string(rune('a'+i))},
		})
	}
	homeworkRepo := repository.NewHomeworkRepository(db)
	// 非 nil 的 resolver,带真实(空)provider repo(同 onsubtitle 测试范式):runHomeworkJob
	// 的 nil-resolver guard 能过;ResolveEmbedder 返回 ErrNoProvider(不 panic,因为 providerRepo
	// 非 nil),代码层 RAG 降级用全量 chunks;ResolveChatByPurpose 同样返回 err,但
	// homeworkLLMOverride seam 短路在 resolver 调用前。测试必须设 svc.homeworkLLMOverride。
	resolver := ai.NewProviderResolver(repository.NewAIProviderRepository(db), "")
	svc := NewAIService(db, contentRepo, repository.NewEpisodeRepository(db), repository.NewCourseRepository(db),
		resolver, nil, nil, nil, nil, nil, nil, nil, nil, homeworkRepo).(*aiService)
	// 立即停掉 worker goroutine:这些测试手动驱动 runHomeworkJob,不让后台 worker 抢着
	// claim 同一条 job 造成竞态(polish E2E 用 file db 能跑通,但 homework 测试有多次
	// runHomeworkJob 调用,worker 的 3s tick 会撞上,索性停掉)。
	svc.Stop()
	return svc, homeworkRepo, hwServiceIDs{subjects["math"].ID, course.ID, episode.ID}
}

// validHomeworkJSON 一份合法的 LLM 返回:2 个 section(选择 + 问答),3 道题。
// 严格按 ParseHomeworkGeneration 要求的 schema:questions 嵌套在 section.questions 下,
// 每题的 section_seq 必须匹配所属 section 的 seq。
func validHomeworkJSON() string {
	return `{
  "sections": [
    {"seq": 1, "title": "一、选择题", "passage_title": null, "passage_content": null, "questions": [
      {"section_seq": 1, "seq": 1, "type": "choice", "stem": "1/2 + 1/2 = ?", "options": ["1/2","1","2","0"], "scoring": {"correct_index": 1}, "explanation": "同分母相加"},
      {"section_seq": 1, "seq": 2, "type": "choice", "stem": "1/4 + 1/4 = ?", "options": ["1/2","1/4","1/8","2/4"], "scoring": {"correct_index": 0}, "explanation": ""}
    ]},
    {"seq": 2, "title": "二、简答题", "passage_title": null, "passage_content": null, "questions": [
      {"section_seq": 2, "seq": 1, "type": "short_answer", "stem": "请说明异分母分数相加的步骤", "scoring": {"reference": "通分后相加"}, "explanation": ""}
    ]}
  ],
  "questions_count": 3
}`
}

// enqueueHomeworkJob 建一条 homework job 并直接调 runHomeworkJob(绕过 worker 轮询,
// 测试驱动单 job,同 polish 测试的范式)。Status 设 "processing":UpdateJobStatus 的终态
// 写入带 WHERE status='processing' 守卫(见 ai_job_repo.go:45),所以测试手动驱动的 job
// 必须 seed 成 processing,failJob/done 的 UPDATE 才能落盘。
func enqueueHomeworkJob(svc *aiService, ids hwServiceIDs) *model.AIJob {
	epID := ids.episodeID
	cID := ids.courseID
	job := &model.AIJob{
		JobType:   "homework",
		EpisodeID: &epID,
		CourseID:  &cID,
		Status:    "processing",
		Priority:  priorityHomework,
	}
	svc.contentRepo.CreateJob(job)
	return job
}

// TestRunHomeworkJob_PersistsHomeworkSectionsQuestions happy path:fake LLM 返回合法 JSON →
// 卷子建好,3 题(2 section)持久化,AIRun 落库(Capability=homework, SelfCheckResult=pass)。
func TestRunHomeworkJob_PersistsHomeworkSectionsQuestions(t *testing.T) {
	svc, hwRepo, ids := seedHomeworkServiceFixture(t)
	svc.homeworkLLMOverride = &fakeHomeworkLLM{responses: []fakeHomeworkResp{{content: validHomeworkJSON()}}}
	job := enqueueHomeworkJob(svc, ids)

	svc.runHomeworkJob(job)

	// job 状态 done。
	got, _ := svc.contentRepo.GetJob(job.ID)
	if got.Status != "done" {
		t.Fatalf("job status = %q; want done", got.Status)
	}
	// homework 建好。
	content, err := hwRepo.GetActiveHomework(ids.episodeID)
	if err != nil || content == nil {
		t.Fatalf("GetActiveHomework: %v / %v", content, err)
	}
	if len(content.Sections) != 2 {
		t.Errorf("sections = %d; want 2", len(content.Sections))
	}
	if len(content.Questions) != 3 {
		t.Errorf("questions = %d; want 3", len(content.Questions))
	}
	if content.Homework.Version != 1 {
		t.Errorf("version = %d; want 1", content.Homework.Version)
	}
	// AIRun 落库。
	runs, _ := svc.contentRepo.ListRunsForJob(job.ID)
	if len(runs) != 1 {
		t.Fatalf("runs = %d; want 1", len(runs))
	}
	if runs[0].Capability != "homework" {
		t.Errorf("run capability = %q; want homework", runs[0].Capability)
	}
	if runs[0].SelfCheckResult != "pass" {
		t.Errorf("run self_check = %q; want pass", runs[0].SelfCheckResult)
	}
	if runs[0].SystemPromptText == "" {
		t.Error("run system_prompt_text should be recorded for admin replay")
	}
}

// TestRunHomeworkJob_RegeneratesArchivesOld 重生成:第二次 runHomeworkJob → 旧卷 archived、
// 新卷 active 且 Version=2。
func TestRunHomeworkJob_RegeneratesArchivesOld(t *testing.T) {
	svc, hwRepo, ids := seedHomeworkServiceFixture(t)
	svc.homeworkLLMOverride = &fakeHomeworkLLM{responses: []fakeHomeworkResp{
		{content: validHomeworkJSON()},
		{content: validHomeworkJSON()}, // 第二次生成用同一份 fixture
	}}
	// 第一次。
	job1 := enqueueHomeworkJob(svc, ids)
	svc.runHomeworkJob(job1)
	// 第二次(同 episode,应触发重生成 → archive 旧卷)。
	job2 := enqueueHomeworkJob(svc, ids)
	svc.runHomeworkJob(job2)

	// active 是新卷,Version=2。
	content, _ := hwRepo.GetActiveHomework(ids.episodeID)
	if content == nil {
		t.Fatal("expected active homework after regen")
	}
	if content.Homework.Version != 2 {
		t.Errorf("active version = %d; want 2", content.Homework.Version)
	}
	// 旧卷 archived。
	archived, _ := hwRepo.ListArchivedHomeworks(ids.episodeID)
	if len(archived) != 1 {
		t.Errorf("archived count = %d; want 1", len(archived))
	}
	if archived[0].Version != 1 {
		t.Errorf("archived version = %d; want 1", archived[0].Version)
	}
}

// TestRunHomeworkJob_InvalidJSONFailsJob LLM 返回非 JSON → failJob(job=failed),不崩,写 fail run。
func TestRunHomeworkJob_InvalidJSONFailsJob(t *testing.T) {
	svc, hwRepo, ids := seedHomeworkServiceFixture(t)
	svc.homeworkLLMOverride = &fakeHomeworkLLM{responses: []fakeHomeworkResp{
		{content: "这不是 JSON,我只是随便说说"},
	}}
	job := enqueueHomeworkJob(svc, ids)

	svc.runHomeworkJob(job)

	got, _ := svc.contentRepo.GetJob(job.ID)
	if got == nil {
		t.Fatal("job disappeared after run")
	}
	if got.Status != "failed" {
		t.Errorf("job status = %q; want failed (invalid JSON)", got.Status)
	}
	if !strings.Contains(got.Error, "parse homework") {
		t.Errorf("job error = %q; want contains 'parse homework'", got.Error)
	}
	// 没建卷。
	content, _ := hwRepo.GetActiveHomework(ids.episodeID)
	if content != nil {
		t.Errorf("expected no homework on parse failure; got %+v", content)
	}
	// fail run 落库(供 admin 看 LLM 返回了啥)。
	runs, _ := svc.contentRepo.ListRunsForJob(job.ID)
	if len(runs) != 1 || runs[0].SelfCheckResult != "fail" {
		t.Errorf("expected 1 fail run; got %+v", runs)
	}
}

// TestRunHomeworkJob_LLMCallError LLM 调用本身失败(网络错)→ failJob,不崩。
func TestRunHomeworkJob_LLMCallError(t *testing.T) {
	svc, _, ids := seedHomeworkServiceFixture(t)
	svc.homeworkLLMOverride = &fakeHomeworkLLM{responses: []fakeHomeworkResp{
		{err: errors.New("relay 502")},
	}}
	job := enqueueHomeworkJob(svc, ids)

	svc.runHomeworkJob(job)

	got, _ := svc.contentRepo.GetJob(job.ID)
	if got == nil {
		t.Fatal("job disappeared after run")
	}
	if got.Status != "failed" {
		t.Errorf("job status = %q; want failed (llm call error)", got.Status)
	}
	if !strings.Contains(got.Error, "llm chat") {
		t.Errorf("job error = %q; want contains 'llm chat'", got.Error)
	}
}

// TestRunHomeworkJob_NoChunksFails 无素材(chunks 空)→ failJob,带 ErrHomeworkInsufficientMaterial。
func TestRunHomeworkJob_NoChunksFails(t *testing.T) {
	// 建一个无 chunks 的 episode。同样用 NewFileDB(worker goroutine 并发)。
	db := testutil.NewFileDB(t)
	subjects := testutil.SeedSubjects(t, db)
	course := &model.Course{Title: "Empty", SubjectID: subjects["math"].ID}
	db.Create(course)
	episode := &model.Episode{Title: "Empty Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	db.Create(episode)
	contentRepo := repository.NewAIContentRepository(db)
	homeworkRepo := repository.NewHomeworkRepository(db)
	// 非 nil resolver 带真实空 provider repo(同主 fixture):过 nil-resolver guard,走到
	// no-chunks 检查。homeworkLLMOverride 设了但不该被触达(无 chunks 前就 fail)。
	resolver := ai.NewProviderResolver(repository.NewAIProviderRepository(db), "")
	svc := NewAIService(db, contentRepo, repository.NewEpisodeRepository(db), repository.NewCourseRepository(db),
		resolver, nil, nil, nil, nil, nil, nil, nil, nil, homeworkRepo).(*aiService)
	svc.Stop() // 停 worker,手动驱动 runHomeworkJob(同主 fixture)
	svc.homeworkLLMOverride = &fakeHomeworkLLM{responses: []fakeHomeworkResp{{content: validHomeworkJSON()}}}
	epID := episode.ID
	cID := course.ID
	job := &model.AIJob{JobType: "homework", EpisodeID: &epID, CourseID: &cID, Status: "processing"}
	svc.contentRepo.CreateJob(job)

	svc.runHomeworkJob(job)

	got, _ := svc.contentRepo.GetJob(job.ID)
	if got.Status != "failed" {
		t.Errorf("job status = %q; want failed (no chunks)", got.Status)
	}
	if !strings.Contains(got.Error, "素材") {
		t.Errorf("job error = %q; want mentions 素材", got.Error)
	}
}

// TestEnqueueHomeworkForCourse_Dedupes 批量入队 + 去重(已有在途 job 的 episode 跳过)。
func TestEnqueueHomeworkForCourse_Dedupes(t *testing.T) {
	svc, _, ids := seedHomeworkServiceFixture(t)
	// 第一次:入队 1 条。
	n1, err := svc.EnqueueHomeworkForCourse(ids.courseID)
	if err != nil {
		t.Fatalf("Enqueue #1: %v", err)
	}
	if n1 != 1 {
		t.Errorf("enqueued #1 = %d; want 1", n1)
	}
	// 第二次:同 course,已有在途 job → 0。
	n2, err := svc.EnqueueHomeworkForCourse(ids.courseID)
	if err != nil {
		t.Fatalf("Enqueue #2: %v", err)
	}
	if n2 != 0 {
		t.Errorf("enqueued #2 = %d; want 0 (dedup)", n2)
	}
	// 清掉在途 job(模拟它跑完),再入队应能再入 1 条。
	svc.db.Model(&model.AIJob{}).Where("job_type = 'homework'").Update("status", "done")
	n3, _ := svc.EnqueueHomeworkForCourse(ids.courseID)
	if n3 != 1 {
		t.Errorf("enqueued #3 = %d; want 1 (after prior done)", n3)
	}
}

// TestHomeworkNilSafe homeworkRepo 未注入 → 所有方法返回 ErrHomeworkNotEnabled,不 panic。
func TestHomeworkNilSafe(t *testing.T) {
	db := testutil.NewFileDB(t)
	contentRepo := repository.NewAIContentRepository(db)
	// homeworkRepo = nil(最后一个参数)。
	svc := NewAIService(db, contentRepo, repository.NewEpisodeRepository(db), repository.NewCourseRepository(db),
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).(*aiService)
	svc.Stop() // 停 worker,这些 nil-safe 方法不跑 job,worker 纯属噪音

	if _, err := svc.EnqueueHomeworkForCourse(1); !errors.Is(err, ErrHomeworkNotEnabled) {
		t.Errorf("Enqueue nil: err = %v; want ErrHomeworkNotEnabled", err)
	}
	if _, err := svc.GetHomeworkViewByID(1); !errors.Is(err, ErrHomeworkNotEnabled) {
		t.Errorf("GetByID nil: err = %v; want ErrHomeworkNotEnabled", err)
	}
	if _, err := svc.ListHomeworksByCourse(1); !errors.Is(err, ErrHomeworkNotEnabled) {
		t.Errorf("List nil: err = %v; want ErrHomeworkNotEnabled", err)
	}
	if _, err := svc.GetHomeworkPromptConfig(1, "math"); !errors.Is(err, ErrHomeworkNotEnabled) {
		t.Errorf("GetPrompt nil: err = %v; want ErrHomeworkNotEnabled", err)
	}
}

// TestHomeworkPromptConfig_Lifecycle Get → Save → Get(改了)→ Reset → Get(回默认)。
func TestHomeworkPromptConfig_Lifecycle(t *testing.T) {
	svc, _, ids := seedHomeworkServiceFixture(t)

	// Get 首次:lazy 创建灌默认(math 配方)。
	cfg, err := svc.GetHomeworkPromptConfig(ids.subjectID, "math")
	if err != nil {
		t.Fatalf("Get #1: %v", err)
	}
	if cfg.SystemPrompt == "" {
		t.Fatal("default prompt should be non-empty")
	}
	if !strings.Contains(cfg.SystemPrompt, "calculation") {
		t.Error("math default prompt should mention calculation (math recipe)")
	}

	// Save:改成 english 配方内容。
	newPrompt := "ADMIN EDITED: 只出选择题"
	if err := svc.SaveHomeworkPromptConfig(ids.subjectID, "math", newPrompt); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg2, _ := svc.GetHomeworkPromptConfig(ids.subjectID, "math")
	if cfg2.SystemPrompt != newPrompt {
		t.Errorf("after save = %q; want %q", cfg2.SystemPrompt, newPrompt)
	}

	// Reset:回 math 默认。
	if err := svc.ResetHomeworkPromptConfig(ids.subjectID, "math"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	cfg3, _ := svc.GetHomeworkPromptConfig(ids.subjectID, "math")
	if cfg3.SystemPrompt != cfg.SystemPrompt {
		t.Errorf("after reset = %q; want original default %q", cfg3.SystemPrompt, cfg.SystemPrompt)
	}
}

// TestGetHomeworkViewByID_AssemblesSections GetHomeworkViewByID 把 questions 按 section 分组。
func TestGetHomeworkViewByID_AssemblesSections(t *testing.T) {
	svc, hwRepo, ids := seedHomeworkServiceFixture(t)
	svc.homeworkLLMOverride = &fakeHomeworkLLM{responses: []fakeHomeworkResp{{content: validHomeworkJSON()}}}
	job := enqueueHomeworkJob(svc, ids)
	svc.runHomeworkJob(job)

	content, _ := hwRepo.GetActiveHomework(ids.episodeID)
	view, err := svc.GetHomeworkViewByID(content.Homework.ID)
	if err != nil || view == nil {
		t.Fatalf("GetHomeworkViewByID: %v / %v", view, err)
	}
	if len(view.Sections) != 2 {
		t.Fatalf("view sections = %d; want 2", len(view.Sections))
	}
	// section 1(选择)应 2 题,section 2(简答)应 1 题。
	if len(view.Sections[0].Questions) != 2 {
		t.Errorf("section 0 questions = %d; want 2", len(view.Sections[0].Questions))
	}
	if len(view.Sections[1].Questions) != 1 {
		t.Errorf("section 1 questions = %d; want 1", len(view.Sections[1].Questions))
	}
}
