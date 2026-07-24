package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"studyquest/backend/internal/model"
)

// TestExam_HTTPFullCycle 走完整 HTTP 链:seed 题库 → status gate → start 组卷 →
// submit 交卷 → 重交拒 409。守 4 个端点 + 交卷锁。
func TestExam_HTTPFullCycle(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "Exam Course", "math", nil)
	episodeID := env.createEpisode(t, courseID, "Exam Ep")
	userID := env.createUser(t, "exam-student", "student")
	// grantAccess + all_open,让 course 对该学生可见(start 的 canAccessCourse gate)。
	env.grantAccess(t, userID, courseID)
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override",
		map[string]any{"strategy": "all_open", "allowed_episode_ids": []uint{}})

	// seed 题库:1 quiz + 4 道 anchor 题(chunkID != 0)。需要真实 chunk。
	chunk := &model.ContentChunk{EpisodeID: episodeID, CourseID: courseID, SourceType: "subtitle", ChunkIndex: 0, Text: "c"}
	env.db.Create(chunk)
	quiz := &model.Quiz{EpisodeID: episodeID, UserID: userID, CourseID: courseID, Status: "active"}
	env.db.Create(quiz)
	for i := 0; i < 4; i++ {
		env.db.Create(&model.Question{
			QuizID: quiz.ID, ChunkID: chunk.ID, Type: "choice",
			Stem: "q", Options: `["a","b"]`, Scoring: `{"correct_index":1}`,
		})
	}

	// 1. status gate:题库够(4 ≥ 3)→ available。
	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/courses/"+itoa(courseID)+"/exam/status", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status: %d %s", resp.Code, resp.Body.String())
	}
	var st struct{ Available bool `json:"available"` }
	json.Unmarshal(resp.Body.Bytes(), &st)
	if !st.Available {
		t.Error("want available (4 questions ≥ minPool 3)")
	}

	// 2. start 组卷。
	resp = env.doAsUser(t, userID, http.MethodPost, "/api/v1/courses/"+itoa(courseID)+"/exam/start", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("start: %d %s", resp.Code, resp.Body.String())
	}
	var view struct {
		ExamID    int      `json:"exam_id"`
		Questions []struct {
			ID int `json:"id"`
		} `json:"questions"`
	}
	json.Unmarshal(resp.Body.Bytes(), &view)
	if view.ExamID == 0 || len(view.Questions) == 0 {
		t.Fatalf("start returned empty exam: %+v", view)
	}

	// 3. submit 交卷:全选索引 1(正确)→ Score 1.0。
	rightIdx := 1
	answers := []map[string]any{}
	for _, q := range view.Questions {
		answers = append(answers, map[string]any{"question_id": q.ID, "answer_index": rightIdx})
	}
	resp = env.doAsUser(t, userID, http.MethodPost, "/api/v1/exams/"+itoa(uint(view.ExamID))+"/submit",
		map[string]any{"answers": answers})
	if resp.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", resp.Code, resp.Body.String())
	}
	var report struct {
		Score float64 `json:"score"`
	}
	json.Unmarshal(resp.Body.Bytes(), &report)
	if report.Score != 1.0 {
		t.Errorf("Score = %v; want 1.0 (all correct)", report.Score)
	}

	// 4. 重交 → 409。
	resp = env.doAsUser(t, userID, http.MethodPost, "/api/v1/exams/"+itoa(uint(view.ExamID))+"/submit",
		map[string]any{"answers": answers})
	if resp.Code != http.StatusConflict {
		t.Errorf("resubmit: %d; want 409", resp.Code)
	}
}

// TestExam_HTTPGateInsufficient 题库不足时 status unavailable + start 409。
func TestExam_HTTPGateInsufficient(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "C", "math", nil)
	episodeID := env.createEpisode(t, courseID, "E")
	userID := env.createUser(t, "u", "student")
	env.grantAccess(t, userID, courseID)
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override",
		map[string]any{"strategy": "all_open", "allowed_episode_ids": []uint{}})
	// 只 2 道题(< minPool 3)。
	chunk := &model.ContentChunk{EpisodeID: episodeID, CourseID: courseID, SourceType: "subtitle", ChunkIndex: 0, Text: "c"}
	env.db.Create(chunk)
	quiz := &model.Quiz{EpisodeID: episodeID, UserID: userID, CourseID: courseID, Status: "active"}
	env.db.Create(quiz)
	for i := 0; i < 2; i++ {
		env.db.Create(&model.Question{QuizID: quiz.ID, ChunkID: chunk.ID, Type: "choice", Stem: "q", Options: `["a","b"]`, Scoring: `{"correct_index":1}`})
	}

	resp := env.doAsUser(t, userID, http.MethodGet, "/api/v1/courses/"+itoa(courseID)+"/exam/status", nil)
	var st struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	json.Unmarshal(resp.Body.Bytes(), &st)
	if st.Available || st.Reason == "" {
		t.Errorf("want unavailable + reason; got %+v", st)
	}

	// start → 409。
	resp = env.doAsUser(t, userID, http.MethodPost, "/api/v1/courses/"+itoa(courseID)+"/exam/start", nil)
	if resp.Code != http.StatusConflict {
		t.Errorf("start with insufficient pool: %d; want 409", resp.Code)
	}
}

// TestExam_AdminStats admin 观测端点:灌考试数据 → GET stats。
func TestExam_AdminStats(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "C", "math", nil)
	episodeID := env.createEpisode(t, courseID, "E")
	userID := env.createUser(t, "u", "student")
	// FK ON:newTestEnvWithAI 开了 PRAGMA foreign_keys=ON,所以 ExamQuestion.QuestionID /
	// ExamAnswer.QuestionID 必须指向真实存在的 questions 行。先 seed 一道 anchor 题。
	chunk := &model.ContentChunk{EpisodeID: episodeID, CourseID: courseID, SourceType: "subtitle", ChunkIndex: 0, Text: "c"}
	env.db.Create(chunk)
	quiz := &model.Quiz{EpisodeID: episodeID, UserID: userID, CourseID: courseID, Status: "active"}
	env.db.Create(quiz)
	q := &model.Question{QuizID: quiz.ID, ChunkID: chunk.ID, Type: "choice", Stem: "q", Options: `["a","b"]`, Scoring: `{"correct_index":1}`}
	env.db.Create(q)
	// 直接 seed 一份已交卷 exam(score 0.5)+ ExamQuestion/Answer 验证 source quality。
	now := time.Now().UTC()
	exam := &model.Exam{UserID: userID, CourseID: courseID, Status: "active", SubmittedAt: &now, Score: 0.5}
	env.db.Create(exam)
	eq1 := &model.ExamQuestion{ExamID: exam.ID, QuestionID: q.ID, ChunkID: chunk.ID, Source: "pool", OrderIdx: 0}
	eq2 := &model.ExamQuestion{ExamID: exam.ID, QuestionID: q.ID, ChunkID: chunk.ID, Source: "generated", OrderIdx: 1}
	env.db.Create(eq1)
	env.db.Create(eq2)
	env.db.Create(&model.ExamAnswer{ExamID: exam.ID, ExamQuestionID: eq1.ID, UserID: userID, QuestionID: q.ID, ChunkID: chunk.ID, Correct: true, AnsweredAt: now})
	env.db.Create(&model.ExamAnswer{ExamID: exam.ID, ExamQuestionID: eq2.ID, UserID: userID, QuestionID: q.ID, ChunkID: chunk.ID, Correct: false, AnsweredAt: now})

	resp := env.do(t, http.MethodGet, "/admin/api/exam/stats", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin stats: %d %s", resp.Code, resp.Body.String())
	}
	var stats struct {
		Total         int64 `json:"total"`
		Submitted     int64 `json:"submitted"`
		AvgScore      float64 `json:"avg_score"`
		SourceQuality []struct {
			Source string  `json:"source"`
			Rate   float64 `json:"rate"`
		} `json:"source_quality"`
	}
	json.Unmarshal(resp.Body.Bytes(), &stats)
	if stats.Submitted != 1 {
		t.Errorf("submitted = %d; want 1", stats.Submitted)
	}
	// source quality:pool 1/1=1.0, generated 0/1=0.0
	srcMap := map[string]float64{}
	for _, s := range stats.SourceQuality {
		srcMap[s.Source] = s.Rate
	}
	if srcMap["pool"] != 1.0 {
		t.Errorf("pool rate = %v; want 1.0", srcMap["pool"])
	}
	if srcMap["generated"] != 0.0 {
		t.Errorf("generated rate = %v; want 0.0", srcMap["generated"])
	}
}

// TestExam_AdminStats_NilAIServiceDegrades AI 未配置时返回零值。
func TestExam_AdminStats_NilAIServiceDegrades(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, "/admin/api/exam/stats", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("nil AI: %d; want 200", resp.Code)
	}
	var stats map[string]any
	json.Unmarshal(resp.Body.Bytes(), &stats)
	if stats["total"] != float64(0) {
		t.Errorf("nil AI total = %v; want 0", stats["total"])
	}
}
