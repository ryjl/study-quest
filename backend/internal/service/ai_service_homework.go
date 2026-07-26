package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// ai_service_homework.go — 课后作业卷(Homework)的 service 层。和 exam_service.go 平行,
// 但作业是 episode 级、不绑 user、AI 单次生成(不走 ReAct)、纯打印。
//
// 入口:
//   - EnqueueHomeworkForCourse:admin 批量触发(整门课),照抄 EnqueueSegmentForCourse 范式
//   - runHomeworkJob:worker 消费 job,照抄 runSummaryJob 的"单次 LLM 调用"范式
//   - GetHomeworkViewByID / ListHomeworksByCourse / HasPendingHomeworkJob:admin 预览/列表/状态
//   - Get/Save/Reset HomeworkPromptConfig:per-subject 完整 system prompt 配置(C 方案)
//
// nil-safe:homeworkRepo 未注入时,所有方法返回 ErrHomeworkNotEnabled,不 panic,其它功能照常。

// ErrHomeworkNotEnabled homeworkRepo 未注入(测试不传或生产装配漏)时的降级错误。
// handler 据此返回 404/503,不阻断其它 AI 功能。
var ErrHomeworkNotEnabled = fmt.Errorf("作业功能未启用")

// ErrHomeworkInsufficientMaterial episode 没有素材(chunks),无法出作业。
var ErrHomeworkInsufficientMaterial = fmt.Errorf("该课时暂无学习素材(字幕未处理),无法生成作业")

// HomeworkView 是给 admin 前端预览/打印的完整作业视图。三层全展开,questions 按 section
// 分组后各自按 seq 排(组装成二维数组,前端直接渲染)。
//
// 扁平 DTO + 显式 snake_case json tag(同 ExamView 范式),**不内嵌 model.Homework**——
// 内嵌会让 JSON 字段名走 Go 默认 PascalCase(model 无 json tag),破坏项目统一的 snake_case
// 契约。这里是跨层契约(Go ↔ TS),必须手维护字段对齐(铁律 #1)。
type HomeworkView struct {
	ID            uint                 `json:"id"`
	EpisodeID     uint                 `json:"episode_id"`
	CourseID      uint                 `json:"course_id"`
	Version       int                  `json:"version"`
	Status        string               `json:"status"`
	AgentMetaJSON string               `json:"agent_meta_json"`
	CreatedAt     time.Time            `json:"created_at"`
	Sections      []HomeworkViewSection `json:"sections"`
}

// HomeworkViewSection 带 passage + 该 section 的题。扁平 DTO + json tag(同 HomeworkView)。
type HomeworkViewSection struct {
	ID             uint                 `json:"id"`
	Seq            int                  `json:"seq"`
	Title          string               `json:"title"`
	PassageTitle   *string              `json:"passage_title"`
	PassageContent *string              `json:"passage_content"`
	Questions      []HomeworkViewQuestion `json:"questions"`
}

// HomeworkViewQuestion 扁平 DTO(对齐 model.HomeworkQuestion,加 snake_case json tag)。
type HomeworkViewQuestion struct {
	ID          uint   `json:"id"`
	Seq         int    `json:"seq"`
	Type        string `json:"type"`
	Stem        string `json:"stem"`
	Options     string `json:"options"`      // JSON []string(choice/multi_choice),前端 JSON.parse
	Scoring     string `json:"scoring"`      // 各题型 JSON,前端 JSON.parse 按题型取字段
	Explanation string `json:"explanation"`
}

// homeworkAgentMeta 记录生成元数据,落 Homework.AgentMetaJSON,供 admin 观测。
type homeworkAgentMeta struct {
	SectionCount   int    `json:"section_count"`
	QuestionCount  int    `json:"question_count"`
	TypeCount      int    `json:"type_count"`
	SubjectKey     string `json:"subject_key"`
	TopChunkCount  int    `json:"top_chunk_count"`
	SelfCheckResult string `json:"self_check_result,omitempty"` // Stage 3 才填
}

// EnqueueHomeworkForCourse 为某课程所有有素材的 episode 批量入队 homework job。
// 照抄 EnqueueSegmentForCourse 范式:ListByCourse → 去重(hasPendingJob)→ 循环入队。
func (s *aiService) EnqueueHomeworkForCourse(courseID uint) (int, error) {
	if s.homeworkRepo == nil {
		return 0, ErrHomeworkNotEnabled
	}
	episodes, err := s.episodeRepo.ListByCourse(courseID)
	if err != nil {
		return 0, err
	}
	if len(episodes) == 0 {
		return 0, nil
	}
	// 去重:已有在途 homework job 的 episode 跳过。这道门对齐 EnqueueSegmentForCourse,
	// 避免 admin 反复点"批量生成"堆出多条 homework job 重复烧 token。
	targetIDs := make([]uint, 0, len(episodes))
	for _, ep := range episodes {
		if !s.hasPendingJob("homework", ep.ID) {
			targetIDs = append(targetIDs, ep.ID)
		}
	}
	if len(targetIDs) == 0 {
		return 0, nil
	}
	// 复用通用 enqueue(它内部会查 episode 解析 courseID,逐条 CreateJob)。
	enqueued, _, err := s.enqueue(targetIDs, "homework", priorityHomework)
	if err != nil {
		return 0, err
	}
	return len(enqueued), nil
}

// HasPendingHomeworkJob 报告某 episode 是否有在途 homework job。
func (s *aiService) HasPendingHomeworkJob(episodeID uint) bool {
	return s.hasPendingJob("homework", episodeID)
}

// runHomeworkJob worker 消费一条 homework job:代码层 RAG → 单次 LLM → 解析 → 持久化。
// 照抄 runSummaryJob 的"单次 LLM 调用"范式(不走 ReAct agent loop,省 token)。
func (s *aiService) runHomeworkJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "AI not configured (no resolver)", nil)
		return
	}
	if s.homeworkRepo == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "homework repo not wired", nil)
		return
	}
	if job.EpisodeID == nil || job.CourseID == nil {
		s.failJob(job, "homework job missing episode_id/course_id")
		return
	}
	episodeID, courseID := *job.EpisodeID, *job.CourseID

	// 1. 拉素材:本课 chunks(主)+ 课程近期 chunks(辅,复习题源,裁剪避免撑爆 prompt)。
	ownChunks, err := s.contentRepo.ListChunks(episodeID, "subtitle")
	if err != nil {
		s.failJob(job, "load own chunks: "+err.Error())
		return
	}
	if len(ownChunks) == 0 {
		s.failJob(job, ErrHomeworkInsufficientMaterial.Error())
		return
	}
	courseChunks, _ := s.contentRepo.ListChunksByCourseForExam(courseID)
	// 裁剪:课程级 chunks 只取其它 episode 的少量做复习素材(总量上限 200 条,约够上下文)。
	reviewChunks := capReviewChunks(courseChunks, episodeID, 200)

	// 2. 课程/科目上下文(用于选科目配方 + prompt 显示科目名)。
	course, _ := s.courseRepo.FindByIDWithSubject(courseID)
	subjectKey := ""
	subjectLabel := ""
	if course != nil && course.Subject.ID != 0 {
		subjectKey = course.Subject.Key
		subjectLabel = course.Subject.Label
	}

	// 3. 取 prompt 配置(无则 lazy 灌默认)。homework 不读 mastery(通用卷不个性化)。
	// subjectID 用 course.Subject.ID(FindByIDWithSubject 已 preload);取不到则用默认 prompt
	// 但不落库(避免给 subjectID=0 建一条脏的 prompt 配置行)。
	systemPrompt := agent.DefaultHomeworkPrompt(subjectKey)
	if course != nil && course.Subject.ID != 0 {
		if cfg, err := s.homeworkRepo.GetOrCreatePromptConfig(course.Subject.ID, systemPrompt); err == nil && cfg.SystemPrompt != "" {
			systemPrompt = cfg.SystemPrompt
		} else if err != nil {
			// GetOrCreate 失败不致命:用默认 prompt 继续(作业仍能生成),记日志。
			log.Printf("homework job %d: GetOrCreatePromptConfig failed (using default): %v", job.ID, err)
		}
	}

	// 4. 代码层 RAG:用本课主题检索 top-K chunks,塞进 prompt(代替 quiz 的 agent 检索)。
	emb, err := s.resolver.ResolveEmbedder()
	retrieved := ownChunks // 默认:无 embedder 时全量本课 chunks 喂进去
	if err == nil && emb != nil {
		topic := episodeTopic(ownChunks) // 从 chunks 抽一个粗略主题做检索 query
		if top, rerr := agent.RetrieveTopChunks(ctx, emb, ownChunks, topic, 8); rerr == nil && len(top) > 0 {
			retrieved = top
		}
	} else if err != nil {
		log.Printf("homework job %d: embedder unavailable (feeding all chunks): %v", job.ID, err)
	}

	// 5. 选 provider。homeworkLLMOverride 是 TEST-ONLY seam(同 polishLLMOverride),
	// 生产为 nil 走 resolver;测试设非 nil 走 fake LLM 驱动完整 RAG→LLM→parse→persist 路径。
	var llm ai.LLMProvider
	modelName := ""
	if s.homeworkLLMOverride != nil {
		llm = s.homeworkLLMOverride
		modelName = "fake-homework-llm"
	} else {
		llm, err = s.resolver.ResolveChatByPurpose("homework")
		if err != nil {
			s.failJob(job, "resolve chat provider: "+err.Error())
			return
		}
		modelName = s.resolver.ChatModelNameByPurpose("homework")
	}

	// 6. 构造 user prompt(本课 top chunks + 课程复习 chunks + 科目信息)。
	userPrompt := buildHomeworkUserPrompt(retrieved, reviewChunks, subjectLabel)

	// 7. 单次 LLM 调用(MaxTokens 给足,避免 quiz 那种 JSON 砍断坑)。
	start := time.Now()
	chatResp, err := llm.Chat(ctx, ai.ChatRequest{
		Model:       modelName,
		Temperature: 0,
		MaxTokens:   10000,
		Messages: []ai.ChatMessage{
			{Role: ai.RoleSystem, Content: systemPrompt},
			{Role: ai.RoleUser, Content: userPrompt},
		},
	})
	if err != nil {
		s.recordHomeworkRunErr(job, systemPrompt, userPrompt, modelName, err, start)
		s.failJob(job, "llm chat: "+err.Error())
		return
	}
	elapsed := time.Since(start)

	// 8. 解析 + 逐题校验(复用 agent.ParseHomeworkGeneration,含截断兜底 + 残题丢弃)。
	draft, err := agent.ParseHomeworkGeneration(chatResp.Content, subjectKey)
	if err != nil {
		// 解析失败:记一条 fail run(LLM 返回了内容但解析/校验全废,admin 需看 ResponseText 诊断)。
		s.recordHomeworkRun(job, systemPrompt, userPrompt, modelName, chatResp, elapsed, 0, "fail", "parse: "+err.Error())
		s.failJob(job, "parse homework: "+err.Error())
		return
	}
	// 解析成功:记 pass run(qCount 是实际落盘题数)。
	s.recordHomeworkRun(job, systemPrompt, userPrompt, modelName, chatResp, elapsed, len(draft.Questions), "pass", "")

	// 9. 持久化:把 draft 翻译成 model(算 Version + 对齐 SectionID)。
	hwID, err := s.persistHomeworkDraft(episodeID, courseID, draft, retrieved, subjectKey)
	if err != nil {
		s.failJob(job, "persist homework: "+err.Error())
		return
	}
	log.Printf("homework job %d: generated homework %d (%d sections, %d questions) for episode %d",
		job.ID, hwID, len(draft.Sections), len(draft.Questions), episodeID)
	s.contentRepo.UpdateJobStatus(job.ID, "done", "", nil)
}

// persistHomeworkDraft 把解析后的 draft 落库:算 Version(查旧 active 卷的 Version+1,
// 无则 1)、建 sections 拿真 ID、把 questions 的 SectionSeq 翻译成 SectionID、算元数据。
func (s *aiService) persistHomeworkDraft(episodeID, courseID uint, draft agent.HomeworkDraft, retrieved []model.ContentChunk, subjectKey string) (uint, error) {
	// 算 Version:查旧 active/archived 的最大 Version + 1(重生成 → 新版本)。
	var maxVersion int
	s.db.Model(&model.Homework{}).Where("episode_id = ?", episodeID).
		Select("COALESCE(MAX(version), 0)").Scan(&maxVersion)
	newVersion := maxVersion + 1

	// Sections:直接用 draft.Sections 建(repo 会赋 ID)。
	sections := make([]model.HomeworkSection, len(draft.Sections))
	for i, ds := range draft.Sections {
		sections[i] = model.HomeworkSection{
			Seq:            ds.Seq,
			Title:          ds.Title,
			PassageTitle:   ds.PassageTitle,
			PassageContent: ds.PassageContent,
		}
	}

	// Questions:先按 Seq 占位建(repo 会填 SectionID = 新建的 section ID),但我们需要
	// 把 draft 的 SectionSeq 翻译成 section slice 的索引。CreateHomework 里 sections 的
	// SectionID 是在事务里建完 section 后才有的,questions 的 SectionID 调用方得在事务外
	// 对齐——但事务外拿不到 section ID。所以这里用一个约定:questions 的 SectionID 填
	// "在 sections slice 里的下标 + 1"是不行的(下标不是 ID)。
	//
	// 正确做法:CreateHomework 在事务里建完 section 后,按 Seq 把 questions 的 SectionID
	// 对齐到对应 section。但当前 repo.CreateHomework 不做这个对齐(它要求调用方对齐)。
	// 我们这里改变策略:不依赖 repo 的批量 insert 对齐,而是分两次:先 CreateHomework 建
	// 卷+sections(questions=nil),拿回 section ID,再单独建 questions。
	hw := &model.Homework{
		EpisodeID: episodeID,
		CourseID:  courseID,
		Version:   newVersion,
	}
	hwID, err := s.homeworkRepo.CreateHomework(hw, sections, nil)
	if err != nil {
		return 0, err
	}
	// 拿回 sections 拿真 ID(按 Seq 排),建 question 的 SectionSeq→ID 映射。
	created, err := s.homeworkRepo.GetHomeworkByID(hwID)
	if err != nil {
		return 0, err
	}
	sectionIDBySeq := make(map[int]uint, len(created.Sections))
	for _, sec := range created.Sections {
		sectionIDBySeq[sec.Seq] = sec.ID
	}
	// 翻译 questions 的 SectionSeq → SectionID,组装 model.HomeworkQuestion。
	questions := make([]model.HomeworkQuestion, 0, len(draft.Questions))
	for _, dq := range draft.Questions {
		secID, ok := sectionIDBySeq[dq.SectionSeq]
		if !ok {
			continue // section 不存在,丢(正常不应发生,parse 已校验)
		}
		opts := ""
		if len(dq.Options) > 0 {
			if b, _ := json.Marshal(dq.Options); b != nil {
				opts = string(b)
			}
		}
		questions = append(questions, model.HomeworkQuestion{
			HomeworkID:  hwID,
			SectionID:   secID,
			Seq:         dq.Seq,
			Type:        dq.Type,
			Stem:        dq.Stem,
			Options:     opts,
			Scoring:     dq.Scoring,
			Explanation: dq.Explanation,
		})
	}
	// 批量建 questions(直接走 db,不走 repo——repo.CreateHomework 是 archive-then-insert
	// 整套,这里只追加 questions)。nil-safe:即使 questions 空,卷子也建好了(极端情况)。
	if len(questions) > 0 {
		if err := s.db.Create(&questions).Error; err != nil {
			return 0, err
		}
	}

	// 写元数据(题型分布等)。
	typeCounts := make(map[string]int)
	for _, q := range questions {
		typeCounts[q.Type]++
	}
	meta := homeworkAgentMeta{
		SectionCount:  len(sections),
		QuestionCount: len(questions),
		TypeCount:     len(typeCounts),
		SubjectKey:    subjectKey,
		TopChunkCount: len(retrieved),
	}
	if metaJSON, _ := json.Marshal(meta); metaJSON != nil {
		s.db.Model(&model.Homework{}).Where("id = ?", hwID).Update("agent_meta_json", string(metaJSON))
	}
	return hwID, nil
}

// capReviewChunks 从课程级 chunks 里挑"非当前 episode"的若干条做复习素材,总量上限。
// 避免把整门课几百条 chunk 全塞进 prompt 撑爆 token。简单策略:遍历取其它 episode 的,
// 满额即停。不按 episode 去重(一节课的复习素材来自多个旧课是正常的)。
func capReviewChunks(all []model.ContentChunk, currentEpisodeID uint, cap int) []model.ContentChunk {
	out := make([]model.ContentChunk, 0, cap)
	for _, ch := range all {
		if ch.EpisodeID == currentEpisodeID {
			continue // 当前课的已在 ownChunks 里,不重复
		}
		out = append(out, ch)
		if len(out) >= cap {
			break
		}
	}
	return out
}

// episodeTopic 从本课 chunks 抽一个粗略主题字符串,作为代码层 RAG 的检索 query。
// 简单策略:取前几条 chunk 的文本拼一段(字幕开头通常是引入/主题)。够用即可,
// 不需要精确——RAG 在这里只是为了让 prompt 里的素材更聚焦,不是关键路径。
func episodeTopic(chunks []model.ContentChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	topic := ""
	for i, ch := range chunks {
		if i >= 3 {
			break
		}
		topic += ch.Text + " "
	}
	return topic
}

// buildHomeworkUserPrompt 构造 user prompt:本课 top chunks(带时间锚)+ 课程复习 chunks
// + 科目信息。参考 summarizer.go 的 buildSummaryUserPrompt 的拼接风格。
func buildHomeworkUserPrompt(ownChunks, reviewChunks []model.ContentChunk, subjectLabel string) string {
	out := "请基于以下素材为这节课出一份课后作业卷。\n\n"
	if subjectLabel != "" {
		out += fmt.Sprintf("【科目】%s\n\n", subjectLabel)
	}
	out += "【本节课内容】\n"
	for i, ch := range ownChunks {
		out += fmt.Sprintf("[%s] %s\n", mmss(ch.StartTime), ch.Text)
		if i >= 15 {
			break // 本课素材上限,避免过长
		}
	}
	if len(reviewChunks) > 0 {
		out += "\n【本课程其它课时的复习素材(可选,用于出复习题)】\n"
		for i, ch := range reviewChunks {
			out += fmt.Sprintf("- %s\n", ch.Text)
			if i >= 20 {
				break
			}
		}
	}
	out += "\n请按系统提示词要求的 JSON 格式输出作业卷(含 sections 分组 + questions)。\n"
	return out
}

// mmss 把秒转成 MM:SS(时间锚,供题干引用)。复制自 summarizer.go 的 mmss(避免跨包依赖)。
func mmss(seconds *int) string {
	if seconds == nil {
		return "00:00"
	}
	m := *seconds / 60
	s := *seconds % 60
	return fmt.Sprintf("%02d:%02d", m, s)
}

// recordHomeworkRun 写一条 homework ai_run(供 admin AIWorkflow 回放)。selfCheckResult
// 传 "pass"(解析成功)/"fail"(LLM 返回但解析全废);Stage 3 接 self-check 后由 check 结果决定。
func (s *aiService) recordHomeworkRun(job *model.AIJob, systemPrompt, userPrompt, modelName string, resp *ai.ChatResponse, elapsed time.Duration, qCount int, selfCheckResult, selfCheckNote string) {
	inputJSON := fmt.Sprintf(`{"question_count":%d}`, qCount)
	s.contentRepo.CreateRun(&model.AIRun{
		JobID:            job.ID,
		Capability:       "homework",
		InputJSON:        inputJSON,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		ModelUsed:        modelName,
		ResponseText:     resp.Content,
		SelfCheckResult:  selfCheckResult,
		SelfCheckNote:    selfCheckNote,
		DurationMs:       int(elapsed.Milliseconds()),
		SystemPromptText: systemPrompt,
		UserPromptText:   userPrompt,
	})
}

// recordHomeworkRunErr 写一条失败的 homework ai_run(LLM 调用本身失败)。
func (s *aiService) recordHomeworkRunErr(job *model.AIJob, systemPrompt, userPrompt, modelName string, callErr error, start time.Time) {
	s.contentRepo.CreateRun(&model.AIRun{
		JobID:            job.ID,
		Capability:       "homework",
		ModelUsed:        modelName,
		ResponseText:     "(call failed) " + callErr.Error(),
		SelfCheckResult:  "fail",
		DurationMs:       int(time.Since(start).Milliseconds()),
		SystemPromptText: systemPrompt,
		UserPromptText:   userPrompt,
	})
}

// GetHomeworkViewByID 取某 homework 完整内容(sections+questions 分组),admin 预览/打印用。
// nil-safe:homeworkRepo 未注入返回 ErrHomeworkNotEnabled。
func (s *aiService) GetHomeworkViewByID(id uint) (*HomeworkView, error) {
	if s.homeworkRepo == nil {
		return nil, ErrHomeworkNotEnabled
	}
	content, err := s.homeworkRepo.GetHomeworkByID(id)
	if err != nil {
		return nil, err
	}
	if content == nil {
		return nil, nil
	}
	return s.assembleHomeworkView(content), nil
}

// ListHomeworksByCourse 列某课程所有 homework(admin 列表)。
func (s *aiService) ListHomeworksByCourse(courseID uint) ([]model.Homework, error) {
	if s.homeworkRepo == nil {
		return nil, ErrHomeworkNotEnabled
	}
	return s.homeworkRepo.ListHomeworksByCourse(courseID)
}

// assembleHomeworkView 把 repo 的扁平返回组装成 HomeworkView(questions 按 section 分组)。
func (s *aiService) assembleHomeworkView(content *repository.HomeworkWithContent) *HomeworkView {
	hw := content.Homework
	view := &HomeworkView{
		ID:            hw.ID,
		EpisodeID:     hw.EpisodeID,
		CourseID:      hw.CourseID,
		Version:       hw.Version,
		Status:        hw.Status,
		AgentMetaJSON: hw.AgentMetaJSON,
		CreatedAt:     hw.CreatedAt,
		Sections:      make([]HomeworkViewSection, len(content.Sections)),
	}
	// 建 section.ID → 该 section 的 questions 列表。
	bySection := make(map[uint][]model.HomeworkQuestion)
	for _, q := range content.Questions {
		bySection[q.SectionID] = append(bySection[q.SectionID], q)
	}
	for i, sec := range content.Sections {
		qs := bySection[sec.ID]
		viewQs := make([]HomeworkViewQuestion, 0, len(qs))
		for _, q := range qs {
			viewQs = append(viewQs, HomeworkViewQuestion{
				ID:          q.ID,
				Seq:         q.Seq,
				Type:        q.Type,
				Stem:        q.Stem,
				Options:     q.Options,
				Scoring:     q.Scoring,
				Explanation: q.Explanation,
			})
		}
		view.Sections[i] = HomeworkViewSection{
			ID:             sec.ID,
			Seq:            sec.Seq,
			Title:          sec.Title,
			PassageTitle:   sec.PassageTitle,
			PassageContent: sec.PassageContent,
			Questions:      viewQs,
		}
	}
	return view
}

// ── HomeworkPromptConfig ──

// GetHomeworkPromptConfig 取某 subject 的 prompt(无则 lazy 创建灌默认)。
// subjectKey 用于算默认 prompt(defaultHomeworkPrompt 按 subjectKey 选配方)。
func (s *aiService) GetHomeworkPromptConfig(subjectID uint, subjectKey string) (model.HomeworkPromptConfig, error) {
	if s.homeworkRepo == nil {
		return model.HomeworkPromptConfig{}, ErrHomeworkNotEnabled
	}
	return s.homeworkRepo.GetOrCreatePromptConfig(subjectID, agent.DefaultHomeworkPrompt(subjectKey))
}

// SaveHomeworkPromptConfig 覆盖某 subject 的 system prompt(admin 编辑)。
func (s *aiService) SaveHomeworkPromptConfig(subjectID uint, subjectKey string, prompt string) error {
	if s.homeworkRepo == nil {
		return ErrHomeworkNotEnabled
	}
	// 先确保行存在(lazy 创建默认),再 UPDATE。避免 admin 对未配置的 subject 直接编辑时
	// UPDATE 到 0 行(那会静默失败,体验差)。
	if _, err := s.homeworkRepo.GetOrCreatePromptConfig(subjectID, agent.DefaultHomeworkPrompt(subjectKey)); err != nil {
		return err
	}
	return s.homeworkRepo.UpdatePromptConfig(subjectID, prompt)
}

// ResetHomeworkPromptConfig 重置回默认(admin 恢复默认)。
func (s *aiService) ResetHomeworkPromptConfig(subjectID uint, subjectKey string) error {
	if s.homeworkRepo == nil {
		return ErrHomeworkNotEnabled
	}
	// 先确保行存在(和 Save 同理),再 UPDATE 回默认。
	if _, err := s.homeworkRepo.GetOrCreatePromptConfig(subjectID, agent.DefaultHomeworkPrompt(subjectKey)); err != nil {
		return err
	}
	return s.homeworkRepo.ResetPromptConfig(subjectID, agent.DefaultHomeworkPrompt(subjectKey))
}
