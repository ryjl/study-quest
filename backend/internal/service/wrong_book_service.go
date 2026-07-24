package service

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// wrong_book_service.go 错题本的业务编排。
//
// 三类操作:
//  1. 列表(GetWrongBook):join 出题面(repository.WrongBookRow)+ 合并 curation 状态
//     (WrongBookItem),组装成客户端视图。题面永远现查,不冗余。
//  2. 重做(RedoWrongBookQuiz / SubmitWrongBookRedo):取一批未掌握错题当一份"重做卷"
//     (复用 QuizViewQuestion 渲染),交卷时用 GradeAnswerV 判分 + 更新 WrongBookItem。
//     重做**不**落 Answer 行、**不**改 quiz-side mastery——错题本重做是轻量巩固,和
//     正式 quiz 交卷隔离(避免重做把 mastery 算两遍、污染答题流水统计)。
//  3. 标记(MarkWrongBookMastered):手动/重做正确后置 mastered。

// WrongBookItemView 是错题本列表的一行:题面 + curation 状态合并。题面字段来自
// repository.WrongBookRow(join 出的),curation 字段来自 WrongBookItem。题面 omit empties,
// 让客户端 JSON 干净。
type WrongBookItemView struct {
	QuestionID  uint   `json:"question_id"`
	Stem        string `json:"stem"`
	Type        string `json:"type"`
	Options     []string `json:"options,omitempty"`
	Explanation string `json:"explanation,omitempty"`
	// 正确答案(列表卡片展开时直接显示,无需进重做流)。复用 redo result 的字段名,
	// 让前端复用 model。由 GetWrongBook 从 row(Scoring/Answer/AnswerText)派生。
	CorrectIndex   *int  `json:"correct_index,omitempty"`   // choice
	CorrectText    string `json:"correct_text,omitempty"`    // fill
	CorrectIndices []int `json:"correct_indices,omitempty"`  // multi_choice
	HasJump     bool   `json:"has_jump"`
	ChunkID     uint   `json:"chunk_id,omitempty"`
	CourseID    uint   `json:"course_id"`
	EpisodeID   uint   `json:"episode_id"`
	SubjectID   uint   `json:"subject_id,omitempty"`
	FirstWrongAt    string `json:"first_wrong_at"`        // RFC3339
	LastAttemptedAt string `json:"last_attempted_at,omitempty"`
	AttemptCount    int    `json:"attempt_count"`
	// CorrectStreak 连续答对次数(重做流累加,达阈值 mastered)。让前端展示"再对 N 次掌握"。
	CorrectStreak   int    `json:"correct_streak"`
	Mastered        bool   `json:"mastered"`
}

// WrongBookRedoResult 是错题本重做交卷的逐题结果(轻量版 AnswerResult,只给对错+正确答案)。
type WrongBookRedoResult struct {
	QuestionID     uint   `json:"question_id"`
	Correct        bool   `json:"correct"`
	Partial        bool   `json:"partial,omitempty"`
	CorrectIndex   *int   `json:"correct_index,omitempty"`
	CorrectText    string `json:"correct_text,omitempty"`
	CorrectIndices []int  `json:"correct_indices,omitempty"`
	Explanation    string `json:"explanation,omitempty"`
}

// GetWrongBook 列错题本。scope=course 时按课程;scope="" 时全局。可按 mastered 过滤。
// nil-safe:wrongBookRepo 为 nil 时返回空(AI 附加层降级),不报错。
func (s *aiService) GetWrongBook(userID uint, courseID uint, mastered *bool) ([]WrongBookItemView, error) {
	if s.wrongBookRepo == nil {
		return []WrongBookItemView{}, nil
	}
	// 题面(不含 mastery 状态)。
	var rows []repository.WrongBookRow
	var err error
	if courseID != 0 {
		rows, err = s.contentRepo.ListWrongAnswersByUserCourse(userID, courseID)
	} else {
		rows, err = s.contentRepo.ListWrongAnswersByUser(userID, 0, 0, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("list wrong answers: %w", err)
	}
	if len(rows) == 0 {
		return []WrongBookItemView{}, nil
	}
	// curation 状态(WrongBookItem)。一次查全部再建 map 合并,避免逐题查。
	// 注意:当传了 mastered 过滤时,items 是过滤后的子集——此时以 items 为驱动
	// (只返回符合 filter 的题),rows 只提供题面 map。没传 filter 时 items 含全部,
	// 行为不变。这样 mastered/course 过滤都正确生效。
	filter := repository.WrongBookFilter{}
	if mastered != nil {
		filter.Mastered = mastered
	}
	if courseID != 0 {
		filter.CourseID = &courseID
	}
	items, err := s.wrongBookRepo.ListByUser(userID, filter)
	if err != nil {
		return nil, fmt.Errorf("list wrong book items: %w", err)
	}
	if len(items) == 0 {
		return []WrongBookItemView{}, nil
	}
	// 题面 rows 建 map(按 question_id),供合并。
	rowByQ := make(map[uint]repository.WrongBookRow, len(rows))
	for _, r := range rows {
		rowByQ[r.QuestionID] = r
	}

	views := make([]WrongBookItemView, 0, len(items))
	for i := range items {
		item := &items[i]
		r, ok := rowByQ[item.QuestionID]
		if !ok {
			continue // 题面已不可得(题被删),跳过——不展示无题面的孤儿错题行
		}
		v := WrongBookItemView{
			QuestionID: r.QuestionID, Stem: r.Stem, Type: r.Type,
			Options: decodeOptions(r.Options), Explanation: r.Explanation,
			HasJump: r.HasJump, ChunkID: r.ChunkID, CourseID: r.CourseID,
			EpisodeID: r.EpisodeID, SubjectID: r.SubjectID,
		}
		// 派生正确答案:把 row 的 Scoring/Answer/AnswerText 拼成 model.Question 喂给现有
		// grading helper(choiceAnswerIndex / fillAcceptable / ParseScoring),复用判分逻辑,
		// 不在客户端解析 JSON。
		v.CorrectIndex, v.CorrectText, v.CorrectIndices = deriveCorrectAnswer(r)
		v.FirstWrongAt = formatRFC3339(item.FirstWrongAt)
		if item.LastAttemptedAt != nil {
			v.LastAttemptedAt = formatRFC3339(*item.LastAttemptedAt)
		}
		v.AttemptCount = item.AttemptCount
		v.CorrectStreak = item.CorrectStreak
		v.Mastered = item.Mastered
		views = append(views, v)
	}
	return views, nil
}

// MarkWrongBookMastered 标记掌握/取消。重做正确或学生手动操作。nil-safe。
func (s *aiService) MarkWrongBookMastered(userID, questionID uint, mastered bool) error {
	if s.wrongBookRepo == nil {
		return nil
	}
	return s.wrongBookRepo.MarkMastered(userID, questionID, mastered)
}

// UnmasteredCount 返回某用户未掌握错题数(全局,courseID=0)。给 tab 角标用——
// 列表默认显示「全部」,但角标只数未掌握的(已掌握的不催复习)。nil-safe 返回 0。
func (s *aiService) UnmasteredCount(userID uint) (int64, error) {
	if s.wrongBookRepo == nil {
		return 0, nil
	}
	no := false
	items, err := s.wrongBookRepo.ListByUser(userID, repository.WrongBookFilter{Mastered: &no})
	if err != nil {
		return 0, err
	}
	return int64(len(items)), nil
}

// ── admin 观测聚合(nil-safe:wrongBookRepo 未注入返回零值/空) ──

func (s *aiService) WrongBookStats() (repository.WrongBookStats, error) {
	if s.wrongBookRepo == nil {
		return repository.WrongBookStats{}, nil
	}
	return s.wrongBookRepo.Stats()
}

func (s *aiService) WrongBookTopFrequent(limit int) ([]repository.FrequentWrongRow, error) {
	if s.wrongBookRepo == nil {
		return []repository.FrequentWrongRow{}, nil
	}
	return s.wrongBookRepo.TopFrequentWrong(limit)
}

func (s *aiService) WrongBookSubjectDistribution() ([]repository.SubjectWrongCount, error) {
	if s.wrongBookRepo == nil {
		return []repository.SubjectWrongCount{}, nil
	}
	return s.wrongBookRepo.DistributionBySubject()
}

// RedoWrongBookQuiz 取一批未掌握的错题当"重做卷"。返回 QuizViewQuestion 列表(不带
// 正确答案,防作弊),客户端复用 quiz 渲染。limit 控制题量(默认 10)。nil-safe。
func (s *aiService) RedoWrongBookQuiz(userID, courseID uint, limit int) ([]QuizViewQuestion, error) {
	if s.wrongBookRepo == nil {
		return []QuizViewQuestion{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	// 取未掌握的错题状态行(取 question_id 列表),再 join 题面。
	unmastered := false
	items, err := s.wrongBookRepo.ListByUser(userID, repository.WrongBookFilter{CourseID: optUint(courseID), Mastered: &unmastered})
	if err != nil {
		return nil, fmt.Errorf("list unmastered: %w", err)
	}
	if len(items) == 0 {
		return []QuizViewQuestion{}, nil
	}
	if len(items) > limit {
		items = items[:limit]
	}
	wantQ := make(map[uint]bool, len(items))
	for _, it := range items {
		wantQ[it.QuestionID] = true
	}
	// 全局 join(按课程或全局),再过滤到本批 question_id。
	var rows []repository.WrongBookRow
	if courseID != 0 {
		rows, _ = s.contentRepo.ListWrongAnswersByUserCourse(userID, courseID)
	} else {
		rows, _ = s.contentRepo.ListWrongAnswersByUser(userID, 0, 0, 0)
	}
	rowByQ := make(map[uint]repository.WrongBookRow, len(rows))
	for _, r := range rows {
		if wantQ[r.QuestionID] {
			rowByQ[r.QuestionID] = r
		}
	}
	out := make([]QuizViewQuestion, 0, len(items))
	for _, it := range items {
		r, ok := rowByQ[it.QuestionID]
		if !ok {
			continue // 题面已不可得(题被删等),跳过
		}
		out = append(out, QuizViewQuestion{
			ID: r.QuestionID, Type: r.Type, Stem: r.Stem,
			Options: decodeOptions(r.Options), HasJump: r.HasJump,
		})
	}
	return out, nil
}

// SubmitWrongBookRedo 是错题本重做交卷。逐题 GradeAnswerV 判分,更新 WrongBookItem
// (对→mastered,错→attempt_count++)。**不**落 Answer 行、**不**改 quiz-side mastery。
// answers 缺的题视为漏答(错)。nil-safe。
func (s *aiService) SubmitWrongBookRedo(userID uint, answers []QuizAnswerInput) ([]WrongBookRedoResult, error) {
	if s.wrongBookRepo == nil {
		return []WrongBookRedoResult{}, nil
	}
	inputByQ := make(map[uint]QuizAnswerInput, len(answers))
	for _, a := range answers {
		inputByQ[a.QuestionID] = a
	}
	out := make([]WrongBookRedoResult, 0, len(answers))
	// 重做只对提交了的题判分;逐题从 wrongBookRepo 取状态 + contentRepo/db 取题面。
	// 题面取不到(题被删)的题跳过判分但仍不报错。
	for _, input := range answers {
		item, err := s.wrongBookRepo.GetItem(userID, input.QuestionID)
		if err != nil || item == nil {
			continue // 不在错题本里(不该发生),跳过
		}
		// 题面:错题本行的 chunk_id 指向知识点,但题面要从 Question 表取。
		// 这里直接按 questionID 查 question(GetQuestions 是按 quiz 的;单独查需 contentRepo)。
		// 用 ListWrongAnswers join 反查不经济——错题本重做频率低,逐题查可接受。
		q, err := s.getQuestionForRedo(item, input.QuestionID)
		if err != nil || q == nil {
			continue
		}
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
		verdict := agent.GradeAnswerV(*q, idx, txt, indices)
		// 全对 → 连对 streak++(达阈值 3 才 mastered,见 IncrementCorrectStreak);
		// 漏选(部分对)/错 → UpsertOnWrong(attempt++ + streak 清零)。
		// 漏选按"错"处理,和交卷 hook / mastery 同口径(2026-07-23 统一判定)。
		if verdict.Correct {
			if _, merr := s.wrongBookRepo.IncrementCorrectStreak(userID, input.QuestionID); merr != nil {
				log.Printf("AI: wrong-book redo increment-streak q%d: %v", input.QuestionID, merr)
			}
		} else {
			if uerr := s.wrongBookRepo.UpsertOnWrong(model.WrongBookItem{
				UserID: userID, QuestionID: input.QuestionID,
				ChunkID: item.ChunkID, CourseID: item.CourseID,
				EpisodeID: item.EpisodeID, SubjectID: item.SubjectID,
			}); uerr != nil {
				log.Printf("AI: wrong-book redo upsert q%d: %v", input.QuestionID, uerr)
			}
		}
		out = append(out, buildRedoResult(*q, verdict))
	}
	return out, nil
}

// getQuestionForRedo 取重做用的题面。错题本重做不经过 quiz,所以要直接查 Question。
// contentRepo 没有单题查询方法,用 db 直接取(service 持有 db)。
func (s *aiService) getQuestionForRedo(item *model.WrongBookItem, questionID uint) (*model.Question, error) {
	var q model.Question
	if err := s.db.First(&q, questionID).Error; err != nil {
		return nil, err
	}
	return &q, nil
}

// buildRedoResult 组装重做交卷的逐题结果(choice/fill/multi 各带正确答案 reveal)。
func buildRedoResult(q model.Question, v agent.Verdict) WrongBookRedoResult {
	res := WrongBookRedoResult{
		QuestionID: q.ID, Correct: v.Correct, Partial: v.Partial,
		Explanation: q.Explanation,
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

// formatRFC3339 把 time.Time 格式化成 RFC3339(UTC)。零值返回空串。
func formatRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// decodeOptions 把 Question.Options(JSON []string)解析成 slice。解析失败返回 nil。
func decodeOptions(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// deriveCorrectAnswer 从 WrongBookRow(带 Scoring/Answer/AnswerText)派生正确答案三件套,
// 复用 redo 交卷的判分 helper(choiceAnswerIndex / fillAcceptable / ParseScoring)。
// 这三个 helper 吃 model.Question,这里用 row 字段拼一个最小 Question 喂进去。
func deriveCorrectAnswer(r repository.WrongBookRow) (*int, string, []int) {
	q := model.Question{Type: r.Type, Scoring: r.Scoring, Answer: r.Answer, AnswerText: r.AnswerText}
	switch r.Type {
	case agent.QuestionFill:
		return nil, joinAcceptable(fillAcceptable(q)), nil
	case agent.QuestionMultiChoice:
		if s := agent.ParseScoring(q); s != nil {
			return nil, "", s.MultiCorrectIndices
		}
		return nil, "", nil
	default: // choice
		i := choiceAnswerIndex(q)
		if i >= 0 {
			return &i, "", nil
		}
		return nil, "", nil
	}
}

// optUint 把 0 转成 nil 指针(WrongBookFilter 用 *uint 表示不过滤)。
func optUint(v uint) *uint {
	if v == 0 {
		return nil
	}
	return &v
}
