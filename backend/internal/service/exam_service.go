package service

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// exam_service.go — 课程考试的业务编排(TODO.md P0)。
//
// StartExam:gate(题库不足返 unavailable)→ SelectExamQuestions 抽题库题 → CreateExam 组卷。
//   混合抽题里"agent 新出迁移题"部分:当前实现是纯题库抽(SelectExamQuestions)。
//   生成的迁移题需要 LLM,走 worker 异步路径更合适(避免开考阻塞几十秒等 agent)。
//   Source 字段已预留('generated'),阶段 2 先落 pool 题;agent 生成题作为后续增强
//   (resolver 非 nil 时可触发,见文末 TODO 注释)。
//
// SubmitExam:抢交卷锁(TryMarkExamSubmitted)→ 逐题 GradeAnswerV 判分 + 写 ExamAnswer +
//   更新 mastery(考试交卷也更新掌握度)→ 算 Score → 返回报告。
//   答案写独立 ExamAnswer 表,不污染 Answer(错题本聚合 / quiz 答题流水)。

// ErrExamInsufficientPool 是 StartExam 的 gate:题库不足(该课程无可抽题)时返回。
// handler 转 409/不可考提示,不报 500。
var ErrExamInsufficientPool = fmt.Errorf("课程题库不足,学完更多课后解锁考试")

// ErrExamAlreadySubmitted 是 SubmitExam 的交卷锁:已交卷的 exam 再交。
var ErrExamAlreadySubmitted = fmt.Errorf("这套考试卷已交卷,不能重复提交")

// ExamView 是开考后返回给客户端的考试卷。题目不带正确答案(防作弊),复用
// QuizViewQuestion 形状让前端复用 quiz 渲染。
type ExamView struct {
	ExamID    uint               `json:"exam_id"`
	CourseID  uint               `json:"course_id"`
	Questions []QuizViewQuestion `json:"questions"`
	Submitted bool               `json:"submitted"`
}

// ExamSubmitResult 是交卷的逐题结果(复用 WrongBookRedoResult 形状)。
type ExamSubmitResult struct {
	QuestionID     uint   `json:"question_id"`
	Correct        bool   `json:"correct"`
	Partial        bool   `json:"partial,omitempty"`
	CorrectIndex   *int   `json:"correct_index,omitempty"`
	CorrectText    string `json:"correct_text,omitempty"`
	CorrectIndices []int  `json:"correct_indices,omitempty"`
	Explanation    string `json:"explanation,omitempty"`
	// Source 让交卷报告区分题库题 vs 新生题(迁移题)。
	Source string `json:"source"`
}

// ExamSubmitReport 是交卷的整体报告。
type ExamSubmitReport struct {
	ExamID  uint              `json:"exam_id"`
	Score   float64           `json:"score"`            // 得分率 0-1
	Results []ExamSubmitResult `json:"results"`
}

// ExamStatus 是 GET /courses/:id/exam/status 的响应:是否可考 + 原因。
type ExamStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"` // unavailable 时的提示
}

// StartExam 为 (user, course) 开考:组卷。返回考试卷(题目不带答案)。
// gate:题库题数 < minPool(默认 3)时返回 ErrExamInsufficientPool(守纯附加层:
// 没学过的课程不该开考,避免凑不出有意义的卷子)。nil-safe:examRepo 为 nil 返回 unavailable。
func (s *aiService) StartExam(userID, courseID uint) (*ExamView, error) {
	if s.examRepo == nil {
		return nil, fmt.Errorf("考试功能未启用")
	}
	const targetCount = 10 // 目标题数
	const minPool = 3      // 题库至少 3 道才开考(否则卷子没意义)

	// 1. 抽题库题(阶段 0 的 SelectExamQuestions)。
	pool, err := s.contentRepo.ListQuestionsByCourseForExam(courseID)
	if err != nil {
		return nil, fmt.Errorf("load question pool: %w", err)
	}
	if len(pool) < minPool {
		return nil, ErrExamInsufficientPool
	}
	masteryRows, _ := s.contentRepo.GetCourseMasteries(userID, courseID)
	selected := SelectExamQuestions(rand.New(rand.NewSource(time.Now().UnixNano())),
		poolFromRepo(pool), masteriesFromModel(masteryRows), targetCount, 0.4)
	if len(selected.Picked) == 0 {
		return nil, ErrExamInsufficientPool
	}

	// 2. 组卷:把抽中的题转成 ExamQuestion(全部 Source=pool;generated 见文首注释)。
	// questionID → ExamPoolQuestion 反查,拿 chunk_id / type 等。
	byID := make(map[uint]repository.ExamPoolQuestion, len(pool))
	for _, p := range pool {
		byID[p.ID] = p
	}
	eqs := make([]model.ExamQuestion, 0, len(selected.Picked))
	for i, pq := range selected.Picked {
		eqs = append(eqs, model.ExamQuestion{
			QuestionID: pq.ID,
			ChunkID:    pq.ChunkID,
			Source:     "pool",
			OrderIdx:   i,
		})
	}

	// 3. CreateExam(archive 旧 active + insert 新的)。
	exam := &model.Exam{UserID: userID, CourseID: courseID}
	examID, err := s.examRepo.CreateExam(exam, eqs)
	if err != nil {
		return nil, fmt.Errorf("create exam: %w", err)
	}

	// 4. 组装客户端视图(题目不带正确答案)。
	savedQs, err := s.examRepo.GetExamQuestions(examID)
	if err != nil {
		return nil, fmt.Errorf("load exam questions: %w", err)
	}
	views := make([]QuizViewQuestion, 0, len(savedQs))
	for _, eq := range savedQs {
		p, ok := byID[eq.QuestionID]
		if !ok {
			continue
		}
		views = append(views, QuizViewQuestion{
			ID: p.ID, Type: p.Type, Stem: p.Stem,
			Options: decodeOptions(p.Options), HasJump: p.HasJump,
		})
	}
	return &ExamView{ExamID: examID, CourseID: courseID, Questions: views}, nil
}

// GetActiveExamView 取某 (user, course) 的 active exam,转成客户端视图。
// 无 active exam 返回 (nil, nil)——handler 据此返回"未开考"。
func (s *aiService) GetActiveExamView(userID, courseID uint) (*ExamView, error) {
	if s.examRepo == nil {
		return nil, nil
	}
	exam, err := s.examRepo.GetActiveExam(userID, courseID)
	if err != nil || exam == nil {
		return nil, err
	}
	eqs, err := s.examRepo.GetExamQuestions(exam.ID)
	if err != nil {
		return nil, err
	}
	// 题面从 questions 表取(ExamQuestion 只存 question_id)。
	// 题面取不到(题被删,见 ExamQuestion 不带 CASCADE 的设计)时塞占位而非跳过——
	// 跳过会让卷子静默少一题、学生无感知;占位明确告诉学生"这题已删除",不漏题。
	views := make([]QuizViewQuestion, 0, len(eqs))
	for _, eq := range eqs {
		var q model.Question
		if err := s.db.First(&q, eq.QuestionID).Error; err != nil {
			views = append(views, QuizViewQuestion{
				ID: eq.QuestionID, Type: "choice",
				Stem: "(本题已删除)", HasJump: false,
			})
			continue
		}
		views = append(views, QuizViewQuestion{
			ID: q.ID, Type: q.Type, Stem: q.Stem,
			Options: decodeOptions(q.Options), HasJump: q.HasJump,
		})
	}
	return &ExamView{
		ExamID: exam.ID, CourseID: courseID, Questions: views,
		Submitted: exam.SubmittedAt != nil,
	}, nil
}

// SubmitExam 交卷。抢交卷锁 → 逐题判分 → 写 ExamAnswer → 更新 mastery → 算 Score → 返回报告。
// 已交卷(SubmittedAt!=nil)返回 ErrExamAlreadySubmitted。
func (s *aiService) SubmitExam(userID, examID uint, answers []QuizAnswerInput) (*ExamSubmitReport, error) {
	if s.examRepo == nil {
		return nil, fmt.Errorf("考试功能未启用")
	}
	exam, err := s.examRepo.GetExamByID(examID)
	if err != nil {
		return nil, fmt.Errorf("load exam: %w", err)
	}
	if exam == nil || exam.UserID != userID {
		return nil, fmt.Errorf("考试卷不存在或无权作答")
	}
	if exam.SubmittedAt != nil {
		return nil, ErrExamAlreadySubmitted
	}
	// 抢交卷锁(条件 UPDATE,消除 TOCTOU)——在落任何 ExamAnswer/mastery 之前。
	now := time.Now().UTC()
	claimed, err := s.examRepo.TryMarkExamSubmitted(examID, now)
	if err != nil {
		return nil, fmt.Errorf("lock exam for submit: %w", err)
	}
	if !claimed {
		return nil, ErrExamAlreadySubmitted
	}

	eqs, err := s.examRepo.GetExamQuestions(examID)
	if err != nil {
		return nil, err
	}
	// question_id → 用户作答。
	inputByQ := make(map[uint]QuizAnswerInput, len(answers))
	for _, a := range answers {
		inputByQ[a.QuestionID] = a
	}

	memory := agent.NewMemoryStore(s.contentRepo)
	results := make([]ExamSubmitResult, 0, len(eqs))
	correctCount := 0
	graded := 0 // 实际判了分的题数(题被删的不计),作得分率分母
	for _, eq := range eqs {
		var q model.Question
		if err := s.db.First(&q, eq.QuestionID).Error; err != nil {
			// 题面被删(ExamQuestion 不带 CASCADE 的设计):判不了分,但要给占位结果
			// (correct=false + 说明),不能 continue——否则 results 数量和卷子题数对不上,
			// 前端按题号渲染会错位。这道题不计入 mastery/ExamAnswer,也不计入得分率分母。
			results = append(results, ExamSubmitResult{
				QuestionID: eq.QuestionID, Correct: false,
				Source: eq.Source, Explanation: "(本题已删除,不计分)",
			})
			continue
		}
		graded++
		input, answered := inputByQ[q.ID]
		verdict := agent.Verdict{}
		if answered {
			idx := -1
			txt := ""
			var indices []int
			if input.AnswerIndex != nil {
				idx = *input.AnswerIndex
			}
			txt = input.AnswerText
			if len(input.AnswerIndices) > 0 {
				indices = input.AnswerIndices
			}
			verdict = agent.GradeAnswerV(q, idx, txt, indices)
			storedText := txt
			if q.Type == agent.QuestionMultiChoice {
				storedText = encodeMultiAnswer(indices)
			}
			if aerr := s.examRepo.CreateExamAnswer(&model.ExamAnswer{
				ExamID: examID, ExamQuestionID: eq.ID, UserID: userID,
				QuestionID: q.ID, ChunkID: eq.ChunkID,
				UserAnswer: idx, UserAnswerText: storedText,
				Correct: verdict.Correct, AnsweredAt: now,
			}); aerr != nil {
				// 不阻断交卷主流程(分数/报告照常返回),但 log 出来——
				// ExamAnswer 落库失败会让 ExamSourceQuality 聚合数据不准,需可见。
				log.Printf("AI: exam submit write answer q%d: %v", q.ID, aerr)
			}
			// 更新 mastery(考试交卷也更新掌握度,让 agent 下次出题反映阶段考试弱点)。
			// 漏选(部分对)按错处理,和 quiz 交卷同口径(统一判定)。
			if merr := memory.RecordAnswer(nil, userID, q.ChunkID, 0, exam.CourseID, verdict.Correct); merr != nil {
				log.Printf("AI: exam submit update memory q%d: %v", q.ID, merr)
			}
		}
		if verdict.Correct {
			correctCount++
		}
		res := buildExamSubmitResult(q, verdict, eq.Source)
		results = append(results, res)
	}

	// 算得分率 + 落库 Score。分母用 graded(实际判了分的题数,题被删的不计),
	// 让被删的题不拉低得分率(学生本来就没得分机会,不该扣分)。全删完则 0 分。
	score := 0.0
	if graded > 0 {
		score = float64(correctCount) / float64(graded)
	}
	s.db.Model(&model.Exam{}).Where("id = ?", examID).Update("score", score)

	return &ExamSubmitReport{ExamID: examID, Score: score, Results: results}, nil
}

// GetExamStatus gate:该课程题库够不够开考。题库 < minPool 返回 unavailable + 提示。
func (s *aiService) GetExamStatus(courseID uint) (ExamStatus, error) {
	if s.examRepo == nil {
		return ExamStatus{Available: false, Reason: "考试功能未启用"}, nil
	}
	pool, err := s.contentRepo.ListQuestionsByCourseForExam(courseID)
	if err != nil {
		return ExamStatus{}, err
	}
	if len(pool) < 3 {
		return ExamStatus{Available: false, Reason: "课程题库不足,学完更多课后解锁考试"}, nil
	}
	return ExamStatus{Available: true}, nil
}

// ── 课程考试 admin 观测(nil-safe:examRepo 未注入返回零值/空) ──

func (s *aiService) ExamStats() (repository.ExamStats, error) {
	if s.examRepo == nil {
		return repository.ExamStats{}, nil
	}
	return s.examRepo.ExamStats()
}

func (s *aiService) ExamSourceQuality() ([]repository.ExamSourceQualityRow, error) {
	if s.examRepo == nil {
		return []repository.ExamSourceQualityRow{}, nil
	}
	return s.examRepo.ExamSourceQuality()
}

// buildExamSubmitResult 组装交卷逐题结果(各题型 reveal 正确答案)。
func buildExamSubmitResult(q model.Question, v agent.Verdict, source string) ExamSubmitResult {
	res := ExamSubmitResult{
		QuestionID: q.ID, Correct: v.Correct, Partial: v.Partial,
		Explanation: q.Explanation, Source: source,
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
		if i >= 0 {
			res.CorrectIndex = &i
		}
	}
	return res
}
