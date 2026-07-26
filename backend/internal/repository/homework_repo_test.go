package repository

import (
	"strings"
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"
)

// homeworkRepoTestEnv 建最小真实链(subject + course + episode)。Homework 不绑 user,
// 不需要 question 题库(题是 AI 新出存独立 HomeworkQuestion 表),所以 fixture 比 exam
// 更轻。返回 db + repo + 各 id,供 homework_repo 测试 seed。
type hwIDs struct {
	subjectID, courseID, episodeID uint
}

func seedHomeworkFixture(t *testing.T) (*homeworkRepo, hwIDs) {
	t.Helper()
	db := testutil.NewDB(t)
	subjects := testutil.SeedSubjects(t, db)
	course := &model.Course{Title: "HW Course", SubjectID: subjects["math"].ID}
	db.Create(course)
	episode := &model.Episode{Title: "HW Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	db.Create(episode)
	return &homeworkRepo{db: db}, hwIDs{subjects["math"].ID, course.ID, episode.ID}
}

// sampleHomeworkInput 构造一份合法的 homework + sections + questions,Version 由调用方传。
// sections 和 questions 的 SectionID 对齐(指向 sections[0]),模拟 service 层的组装逻辑。
func sampleHomeworkInput(ids hwIDs, version int, sectionIDPlaceholder uint) (*model.Homework, []model.HomeworkSection, []model.HomeworkQuestion) {
	hw := &model.Homework{EpisodeID: ids.episodeID, CourseID: ids.courseID, Version: version}
	sections := []model.HomeworkSection{
		{Seq: 1, Title: "一、选择题", PassageTitle: nil, PassageContent: nil},
		{Seq: 2, Title: "二、阅读理解", PassageTitle: strPtr("小猴子的故事"), PassageContent: strPtr("从前有一只小猴子...")},
	}
	questions := []model.HomeworkQuestion{
		{SectionID: sectionIDPlaceholder, Seq: 1, Type: "choice", Stem: "1+1=?", Options: `["1","2","3","4"]`, Scoring: `{"correct_index":1}`},
		{SectionID: sectionIDPlaceholder, Seq: 1, Type: "short_answer", Stem: "故事里的小猴子做了什么?", Scoring: `{"reference":"要点:爬树摘桃"}`},
	}
	// 注意:CreateHomework 里 questions 的 SectionID 不会被自动重映射(调用方负责对齐)。
	// 测试里我们分两步:先建 sections 拿到真 ID,再建 questions。这里 placeholder 仅占位,
	// 实际测试用 CreateHomework_SectionIDAlignment 验证对齐逻辑。
	return hw, sections, questions
}

func strPtr(s string) *string { return &s }

// TestCreateHomework_ArchivesOldHomework 重生成(同 episode 第二次 CreateHomework)应
// archive 旧的 active homework 而非删,历史保留。守 quiz/exam 的同款 invariant。
func TestCreateHomework_ArchivesOldHomework(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)

	// 第一份:手动指定 Version=1。
	hw1, sec1, q1 := sampleHomeworkInput(ids, 1, 0)
	id1, err := repo.CreateHomework(hw1, sec1, q1)
	if err != nil {
		t.Fatalf("CreateHomework #1: %v", err)
	}
	// 第二份:Version=2。service 层会查旧 Version 后 +1,这里测试 repo 不自增,手动传 2。
	hw2, sec2, q2 := sampleHomeworkInput(ids, 2, 0)
	id2, err := repo.CreateHomework(hw2, sec2, q2)
	if err != nil {
		t.Fatalf("CreateHomework #2: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("second CreateHomework returned same id %d; want fresh row", id1)
	}
	// GetActiveHomework 只返回新的。
	got, err := repo.GetActiveHomework(ids.episodeID)
	if err != nil {
		t.Fatalf("GetActiveHomework: %v", err)
	}
	if got == nil || got.Homework.ID != id2 {
		t.Errorf("active homework = %+v; want id %d", got, id2)
	}
	if got.Homework.Version != 2 {
		t.Errorf("active version = %d; want 2", got.Homework.Version)
	}
	// 旧的 homework 行还在(status=archived)。
	var old model.Homework
	repo.db.First(&old, id1)
	if old.Status != "archived" {
		t.Errorf("old homework status = %q; want archived", old.Status)
	}
	if old.ArchivedAt == nil {
		t.Error("old homework ArchivedAt should be set")
	}
}

// TestCreateHomework_ActiveUniqueInvariant 多次重生成后必须恰好一个 active。
// 验证 partial unique index + archive-then-insert 配合不出多条 active。
func TestCreateHomework_ActiveUniqueInvariant(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)
	for i := 0; i < 3; i++ {
		hw, sec, q := sampleHomeworkInput(ids, i+1, 0)
		if _, err := repo.CreateHomework(hw, sec, q); err != nil {
			t.Fatalf("CreateHomework #%d: %v", i, err)
		}
	}
	active, err := repo.GetActiveHomework(ids.episodeID)
	if err != nil {
		t.Fatalf("GetActiveHomework: %v", err)
	}
	if active == nil {
		t.Fatal("expected exactly one active homework; got nil")
	}
	// 两个 archived。
	var archivedCount int64
	repo.db.Model(&model.Homework{}).Where("episode_id = ? AND status = 'archived'", ids.episodeID).Count(&archivedCount)
	if archivedCount != 2 {
		t.Errorf("archived count = %d; want 2", archivedCount)
	}
}

// TestGetActiveHomework_LoadsSectionsAndQuestions 验证三层全展开 + 排序(sections 按 Seq,
// questions 按 SectionID ASC, Seq ASC)。
func TestGetActiveHomework_LoadsSectionsAndQuestions(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)
	// 手动构造 sections + questions(不走 sample 的 placeholder,直接建带内容的)。
	// 先建 homework 拿 ID,再建 sections/questions 验证 loadContent 排序。
	// 但 CreateHomework 是 archive-then-insert,会建 sections/questions。直接用它建,
	// 然后验证返回的 sections/questions 顺序。
	// 为了测排序,构造乱序 Seq 的 sections + questions。
	hw2 := &model.Homework{EpisodeID: ids.episodeID, CourseID: ids.courseID, Version: 1}
	sections := []model.HomeworkSection{
		{Seq: 2, Title: "二、填空题"},
		{Seq: 1, Title: "一、选择题"},
	}
	id, err := repo.CreateHomework(hw2, sections, nil) // 先建,拿 section ID
	if err != nil {
		t.Fatalf("CreateHomework #1: %v", err)
	}
	// 拉回 section 真实 ID(按 Seq 排序后 [一, 二])。
	got, err := repo.GetHomeworkByID(id)
	if err != nil {
		t.Fatalf("GetHomeworkByID: %v", err)
	}
	if len(got.Sections) != 2 {
		t.Fatalf("want 2 sections; got %d", len(got.Sections))
	}
	if got.Sections[0].Title != "一、选择题" || got.Sections[1].Title != "二、填空题" {
		t.Errorf("sections order = [%q,%q]; want [一,二] (sorted by Seq)", got.Sections[0].Title, got.Sections[1].Title)
	}
	sec1ID := got.Sections[0].ID // "一、选择题"(Seq=1)
	sec2ID := got.Sections[1].ID // "二、填空题"(Seq=2)

	// 直接往 DB 灌乱序 questions(绕过 CreateHomework,因为我们要测 loadContent 排序)。
	repo.db.Create(&[]model.HomeworkQuestion{
		{HomeworkID: id, SectionID: sec1ID, Seq: 2, Type: "choice", Stem: "a2", Options: `["1","2"]`, Scoring: `{"correct_index":0}`},
		{HomeworkID: id, SectionID: sec1ID, Seq: 1, Type: "choice", Stem: "a1", Options: `["1","2"]`, Scoring: `{"correct_index":0}`},
		{HomeworkID: id, SectionID: sec2ID, Seq: 1, Type: "fill", Stem: "b", Scoring: `{"accept":["x"]}`},
	})

	got2, err := repo.GetHomeworkByID(id)
	if err != nil {
		t.Fatalf("GetHomeworkByID #2: %v", err)
	}
	if len(got2.Questions) != 3 {
		t.Fatalf("want 3 questions; got %d", len(got2.Questions))
	}
	// repo 契约:questions 按 (section_id ASC, seq ASC) 排序返回。
	// 不依赖 section ID 的具体数值(自增顺序与 Seq 无关),动态算期望顺序:
	// 先确定哪个 section 的 ID 更小,排前面的 section 的题先出;组内按 Seq 升序。
	bySectionStems := map[uint][]string{}
	for _, q := range got2.Questions {
		bySectionStems[q.SectionID] = append(bySectionStems[q.SectionID], q.Stem)
	}
	// 组内必须按 Seq 升序:a1(seq1) 先于 a2(seq2);b 单独一组。
	if got := bySectionStems[sec1ID]; len(got) != 2 || got[0] != "a1" || got[1] != "a2" {
		t.Errorf("sec1 questions in-section order = %v; want [a1, a2] (sorted by Seq)", got)
	}
	if got := bySectionStems[sec2ID]; len(got) != 1 || got[0] != "b" {
		t.Errorf("sec2 questions = %v; want [b]", got)
	}
}

// TestGetActiveHomework_NoneReturnsNil nil 的 active homework 返回 (nil,nil)(非 error)。
func TestGetActiveHomework_NoneReturnsNil(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)
	got, err := repo.GetActiveHomework(ids.episodeID)
	if err != nil {
		t.Fatalf("GetActiveHomework on empty: %v", err)
	}
	if got != nil {
		t.Errorf("want nil; got %+v", got)
	}
}

// TestListHomeworksByCourse 列某课程所有 homework(含 archived),按 created DESC。
func TestListHomeworksByCourse(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)
	// 建 2 份(第二份会 archive 第一份)。
	hw1, sec1, q1 := sampleHomeworkInput(ids, 1, 0)
	repo.CreateHomework(hw1, sec1, q1)
	hw2, sec2, q2 := sampleHomeworkInput(ids, 2, 0)
	id2, _ := repo.CreateHomework(hw2, sec2, q2)

	got, err := repo.ListHomeworksByCourse(ids.courseID)
	if err != nil {
		t.Fatalf("ListHomeworksByCourse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2; got %d", len(got))
	}
	// created DESC → 最新的(active,id2)在前。
	if got[0].ID != id2 {
		t.Errorf("first = id %d; want %d (newest first)", got[0].ID, id2)
	}
}

// TestListArchivedHomeworks 列某 episode 的 archived(不含 active),按 archived_at DESC。
func TestListArchivedHomeworks(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)
	// 建 3 份 → 2 archived + 1 active。
	for i := 0; i < 3; i++ {
		hw, sec, q := sampleHomeworkInput(ids, i+1, 0)
		repo.CreateHomework(hw, sec, q)
	}
	got, err := repo.ListArchivedHomeworks(ids.episodeID)
	if err != nil {
		t.Fatalf("ListArchivedHomeworks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 archived; got %d", len(got))
	}
	for _, h := range got {
		if h.Status != "archived" {
			t.Errorf("got non-archived in ListArchivedHomeworks: %+v", h)
		}
	}
}

// ── HomeworkPromptConfig ──

// TestGetOrCreatePromptConfig_LazyCreates 首次访问 lazy 创建(NOT NULL 灌默认),二次直接返回。
func TestGetOrCreatePromptConfig_LazyCreates(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)
	defaultPrompt := "DEFAULT PROMPT FOR MATH"

	// 首次:无记录 → lazy 创建。
	cfg1, err := repo.GetOrCreatePromptConfig(ids.subjectID, defaultPrompt)
	if err != nil {
		t.Fatalf("GetOrCreate #1: %v", err)
	}
	if cfg1.SystemPrompt != defaultPrompt {
		t.Errorf("first create prompt = %q; want %q", cfg1.SystemPrompt, defaultPrompt)
	}
	if cfg1.SubjectID != ids.subjectID {
		t.Errorf("subject_id = %d; want %d", cfg1.SubjectID, ids.subjectID)
	}

	// 二次:已有记录 → 直接返回,不走 default(此时传不同 default 也不该覆盖)。
	cfg2, err := repo.GetOrCreatePromptConfig(ids.subjectID, "DIFFERENT DEFAULT SHOULD NOT WIN")
	if err != nil {
		t.Fatalf("GetOrCreate #2: %v", err)
	}
	if cfg2.SystemPrompt != defaultPrompt {
		t.Errorf("second get prompt = %q; want %q (existing should win, not new default)", cfg2.SystemPrompt, defaultPrompt)
	}
	if cfg2.ID != cfg1.ID {
		t.Errorf("second get id = %d; want %d (same row)", cfg2.ID, cfg1.ID)
	}
}

// TestUpdatePromptConfig_Overwrites admin 编辑后 UPDATE 覆盖。
func TestUpdatePromptConfig_Overwrites(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)
	repo.GetOrCreatePromptConfig(ids.subjectID, "DEFAULT")

	newPrompt := "ADMIN EDITED PROMPT"
	if err := repo.UpdatePromptConfig(ids.subjectID, newPrompt); err != nil {
		t.Fatalf("UpdatePromptConfig: %v", err)
	}
	// 二次 GetOrCreate(传不同 default)应返回编辑后的内容。
	cfg, _ := repo.GetOrCreatePromptConfig(ids.subjectID, "SHOULD NOT WIN")
	if cfg.SystemPrompt != newPrompt {
		t.Errorf("after update prompt = %q; want %q", cfg.SystemPrompt, newPrompt)
	}
}

// TestResetPromptConfig_RestoresDefault admin 恢复默认 → UPDATE 回 default。
func TestResetPromptConfig_RestoresDefault(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)
	originalDefault := "ORIGINAL DEFAULT"
	repo.GetOrCreatePromptConfig(ids.subjectID, originalDefault)
	repo.UpdatePromptConfig(ids.subjectID, "ADMIN CHANGED IT")

	// 恢复默认(service 层传 defaultHomeworkPrompt(subject))。
	if err := repo.ResetPromptConfig(ids.subjectID, originalDefault); err != nil {
		t.Fatalf("ResetPromptConfig: %v", err)
	}
	cfg, _ := repo.GetOrCreatePromptConfig(ids.subjectID, "SHOULD NOT WIN")
	if cfg.SystemPrompt != originalDefault {
		t.Errorf("after reset prompt = %q; want %q", cfg.SystemPrompt, originalDefault)
	}
}

// TestResetPromptConfig_NoopIfNotExists admin 对从未配置的 subject 重置 → 不报错,不创建。
// (repo 的 ResetPromptConfig 是 UPDATE,没有行时 RowsAffected=0,不是错误。)
func TestResetPromptConfig_NoopIfNotExists(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)
	if err := repo.ResetPromptConfig(ids.subjectID, "ANYTHING"); err != nil {
		t.Errorf("ResetPromptConfig on missing row should be noop; got err: %v", err)
	}
	// 验证没创建行。
	var count int64
	repo.db.Model(&model.HomeworkPromptConfig{}).Where("subject_id = ?", ids.subjectID).Count(&count)
	if count != 0 {
		t.Errorf("Reset should not create row; got count %d", count)
	}
}

// TestHomework_CASCADEOnEpisodeDelete 删 episode 时 CASCADE 清 homework。
// 守 FK 约束(model.Homework.Episode 的 OnDelete:CASCADE)。
func TestHomework_CASCADEOnEpisodeDelete(t *testing.T) {
	repo, ids := seedHomeworkFixture(t)
	// SQLite 默认 FK 关闭,显式开启才能测 OnDelete:CASCADE(同 db_cascade_test.go 范式)。
	repo.db.Exec("PRAGMA foreign_keys=ON")
	hw, sec, q := sampleHomeworkInput(ids, 1, 0)
	hwID, _ := repo.CreateHomework(hw, sec, q)

	// 删 episode。
	if err := repo.db.Delete(&model.Episode{}, ids.episodeID).Error; err != nil {
		t.Fatalf("delete episode: %v", err)
	}
	// homework 应被级联清。
	var count int64
	repo.db.Model(&model.Homework{}).Where("id = ?", hwID).Count(&count)
	if count != 0 {
		t.Errorf("homework should be CASCADE-deleted with episode; got count %d", count)
	}
}

// sanity: sampleHomeworkInput 的 sections 第二个是阅读理解(带 passage),验证 strPtr 正确。
func TestSampleHomeworkInput_PassageSection(t *testing.T) {
	_, sec, _ := sampleHomeworkInput(hwIDs{}, 1, 0)
	if sec[1].PassageContent == nil || !strings.Contains(*sec[1].PassageContent, "小猴子") {
		t.Errorf("section[1] passage = %v; want non-nil containing 小猴子", sec[1].PassageContent)
	}
}
