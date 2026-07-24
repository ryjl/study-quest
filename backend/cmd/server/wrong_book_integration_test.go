package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"studyquest/backend/internal/model"
)

// TestWrongBook_HTTPFullCycle 走完整的 HTTP 链:seed quiz → submit-all 产生错题 →
// GET 错题本 → GET redo 卷 → POST redo submit → POST master。这是错题本端点的端到端
// 覆盖,需要 newTestEnvWithAI(注入真 aiService)。守 5 个端点的状态码 + body + 隔离性。
func TestWrongBook_HTTPFullCycle(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop) // release AI worker goroutine

	courseID := env.createCourse(t, "WB Course", "math", nil)
	episodeID := env.createEpisode(t, courseID, "WB Ep")
	userID := env.createUser(t, "wb-student", "student")
	// grantAccess + all_open override,让 episode 对该学生可见(submit-all 的
	// canAccessEpisode gate 需要)。grantAccess 给课程访问权;override 解除 drip 锁。
	env.grantAccess(t, userID, courseID)
	env.do(t, http.MethodPut, "/admin/api/users/"+itoa(userID)+"/courses/"+itoa(courseID)+"/unlock-override",
		map[string]any{"strategy": "all_open", "allowed_episode_ids": []uint{}})

	// 直接用 db seed 一个 active quiz + 2 道题(绕过 AI 生成,生成需要 provider)。
	// 两题都答错 → 都进错题本。
	quiz := &model.Quiz{EpisodeID: episodeID, UserID: userID, CourseID: courseID, Status: "active"}
	if err := env.db.Create(quiz).Error; err != nil {
		t.Fatalf("seed quiz: %v", err)
	}
	q1 := &model.Question{QuizID: quiz.ID, ChunkID: 0, Type: "choice", Stem: "q1", Options: `["a","b"]`, Scoring: `{"correct_index":1}`}
	q2 := &model.Question{QuizID: quiz.ID, ChunkID: 0, Type: "choice", Stem: "q2", Options: `["a","b"]`, Scoring: `{"correct_index":1}`}
	env.db.Create(q1)
	env.db.Create(q2)

	// 交卷:两题都选索引 0(正确是 1),都错。
	wrongIdx := 0
	resp := env.doAsUser(t, userID, http.MethodPost, "/api/v1/episodes/"+itoa(episodeID)+"/ai-quiz/submit-all",
		map[string]any{"answers": []map[string]any{
			{"question_id": q1.ID, "answer_index": wrongIdx},
			{"question_id": q2.ID, "answer_index": wrongIdx},
		}})
	if resp.Code != http.StatusOK {
		t.Fatalf("submit-all: status %d body %s", resp.Code, resp.Body.String())
	}

	// GET 错题本:应有 2 条。
	resp = env.doAsUser(t, userID, http.MethodGet, "/api/v1/wrong-book", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET wrong-book: %d %s", resp.Code, resp.Body.String())
	}
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(resp.Body.Bytes(), &listResp)
	if len(listResp.Items) != 2 {
		t.Errorf("wrong-book items = %d; want 2 (both questions wrong)", len(listResp.Items))
	}

	// GET redo 卷:2 道未掌握题都应在。
	resp = env.doAsUser(t, userID, http.MethodGet, "/api/v1/wrong-book/redo?limit=10", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET redo: %d %s", resp.Code, resp.Body.String())
	}
	var redoResp struct {
		Questions []map[string]any `json:"questions"`
	}
	json.Unmarshal(resp.Body.Bytes(), &redoResp)
	if len(redoResp.Questions) != 2 {
		t.Errorf("redo questions = %d; want 2", len(redoResp.Questions))
	}

	// redo submit:q1 答对(索引 1),q2 仍答错(索引 0)。
	rightIdx := 1
	resp = env.doAsUser(t, userID, http.MethodPost, "/api/v1/wrong-book/redo/submit",
		map[string]any{"answers": []map[string]any{
			{"question_id": q1.ID, "answer_index": rightIdx},
			{"question_id": q2.ID, "answer_index": wrongIdx},
		}})
	if resp.Code != http.StatusOK {
		t.Fatalf("redo submit: %d %s", resp.Code, resp.Body.String())
	}

	// 标记 q1 掌握(手动,虽然 redo 已标)——验证 master 端点。
	resp = env.doAsUser(t, userID, http.MethodPost, "/api/v1/wrong-book/"+itoa(q1.ID)+"/master", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("master: %d %s", resp.Code, resp.Body.String())
	}

	// 错题本按 mastered=true 过滤:应只剩 q1。
	resp = env.doAsUser(t, userID, http.MethodGet, "/api/v1/wrong-book?mastered=true", nil)
	json.Unmarshal(resp.Body.Bytes(), &listResp)
	if len(listResp.Items) != 1 {
		t.Errorf("mastered filter items = %d; want 1 (q1)", len(listResp.Items))
	}
}

// TestWrongBook_HTTPUnmaster 取消掌握端点。
func TestWrongBook_HTTPUnmaster(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "C", "math", nil)
	episodeID := env.createEpisode(t, courseID, "E")
	userID := env.createUser(t, "u", "student")
	quiz := &model.Quiz{EpisodeID: episodeID, UserID: userID, CourseID: courseID, Status: "active"}
	env.db.Create(quiz)
	q := &model.Question{QuizID: quiz.ID, Type: "choice", Stem: "q", Options: `["a","b"]`, Scoring: `{"correct_index":1}`}
	env.db.Create(q)

	// 先 master 再 unmaster,验证 unmaster 端点 200 + 不报错。
	env.doAsUser(t, userID, http.MethodPost, "/api/v1/wrong-book/"+itoa(q.ID)+"/master", nil)
	resp := env.doAsUser(t, userID, http.MethodPost, "/api/v1/wrong-book/"+itoa(q.ID)+"/unmaster", nil)
	if resp.Code != http.StatusOK {
		t.Errorf("unmaster: %d %s", resp.Code, resp.Body.String())
	}
}

// TestWrongBook_HTTPUserIsolation 学生 A 的错题本看不到学生 B 的错题。
func TestWrongBook_HTTPUserIsolation(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "C", "math", nil)
	userA := env.createUser(t, "A", "student")
	userB := env.createUser(t, "B", "student")

	// A 做错一题(直接 seed wrong-book item,跳过 submit 简化)。
	env.db.Create(&model.WrongBookItem{
		UserID: userA, QuestionID: 100, CourseID: courseID,
		FirstWrongAt: time.Now().UTC(), AttemptCount: 1,
	})

	// B 查错题本应是空。
	resp := env.doAsUser(t, userB, http.MethodGet, "/api/v1/wrong-book", nil)
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(resp.Body.Bytes(), &listResp)
	if len(listResp.Items) != 0 {
		t.Errorf("userB sees userA's wrong items: %d (must be 0)", len(listResp.Items))
	}
}

// TestWrongBook_AdminStats 走 admin 观测端点:灌错题数据 → GET stats → 验证聚合。
// 守 stats handler 的部分失败容忍(每聚合独立降级)+ 正确性。
func TestWrongBook_AdminStats(t *testing.T) {
	env := newTestEnvWithAI(t)
	t.Cleanup(env.aiStop)
	courseID := env.createCourse(t, "C", "math", nil)
	episodeID := env.createEpisode(t, courseID, "E")
	userA := env.createUser(t, "A", "student")
	userB := env.createUser(t, "B", "student")
	// 取 subject id(math)给 wrong_book_items 的 subject_id 冗余。
	var mathSubject model.Subject
	env.db.Where("key = ?", "math").First(&mathSubject)
	// 建真实 quiz + 2 道题(FK ON,WrongBookItem 的 QuestionID 必须指向真实 question)。
	quiz := &model.Quiz{EpisodeID: episodeID, UserID: userA, CourseID: courseID, Status: "active"}
	env.db.Create(quiz)
	q1 := &model.Question{QuizID: quiz.ID, Type: "choice", Stem: "高频题", Options: `["a","b"]`, Scoring: `{"correct_index":1}`}
	q2 := &model.Question{QuizID: quiz.ID, Type: "choice", Stem: "普通题", Options: `["a","b"]`, Scoring: `{"correct_index":1}`}
	env.db.Create(q1)
	env.db.Create(q2)

	// 灌错题:q1 两个学生都错(高频),q2 只 A 错。
	env.db.Create(&model.WrongBookItem{UserID: userA, QuestionID: q1.ID, CourseID: courseID, SubjectID: mathSubject.ID, FirstWrongAt: time.Now().UTC(), AttemptCount: 2})
	env.db.Create(&model.WrongBookItem{UserID: userB, QuestionID: q1.ID, CourseID: courseID, SubjectID: mathSubject.ID, FirstWrongAt: time.Now().UTC(), AttemptCount: 1})
	env.db.Create(&model.WrongBookItem{UserID: userA, QuestionID: q2.ID, CourseID: courseID, SubjectID: mathSubject.ID, FirstWrongAt: time.Now().UTC(), AttemptCount: 1})
	// 标记 q2 掌握(测转化率)。
	env.db.Model(&model.WrongBookItem{}).Where("user_id = ? AND question_id = ?", userA, q2.ID).Update("mastered", true)

	resp := env.do(t, http.MethodGet, "/admin/api/wrong-book/stats", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin stats: %d %s", resp.Code, resp.Body.String())
	}
	var stats struct {
		Total       int64 `json:"total"`
		Unmastered  int64 `json:"unmastered"`
		ThisWeek    int64 `json:"this_week"`
		MasterRate  float64 `json:"master_rate"`
		TopFrequent []map[string]any `json:"top_frequent"`
		BySubject   []map[string]any `json:"by_subject"`
	}
	json.Unmarshal(resp.Body.Bytes(), &stats)

	if stats.Total != 3 {
		t.Errorf("total = %d; want 3", stats.Total)
	}
	if stats.Unmastered != 2 { // q1(A)+q1(B) 未掌握,q2(A) 已掌握
		t.Errorf("unmastered = %d; want 2", stats.Unmastered)
	}
	if stats.ThisWeek != 3 {
		t.Errorf("this_week = %d; want 3 (all seeded today)", stats.ThisWeek)
	}
	// 高频榜:q1 应排第一(2 个学生错)。
	if len(stats.TopFrequent) == 0 {
		t.Fatal("top_frequent empty")
	}
	if stats.TopFrequent[0]["question_id"] != float64(q1.ID) {
		t.Errorf("top frequent[0] = %v; want question %d (2 students)", stats.TopFrequent[0], q1.ID)
	}
	// 科目分布:math 应有 3 条。
	if len(stats.BySubject) == 0 || stats.BySubject[0]["count"] != float64(3) {
		t.Errorf("by_subject = %+v; want math count 3", stats.BySubject)
	}
}

// TestWrongBook_AdminStats_NilAIServiceDegrades AI 未配置时 admin stats 返回零值不 500。
// 用默认 newTestEnv(aiService nil)。守降级。
func TestWrongBook_AdminStats_NilAIServiceDegrades(t *testing.T) {
	env := newTestEnv(t) // AI-free
	resp := env.do(t, http.MethodGet, "/admin/api/wrong-book/stats", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("nil AI stats should be 200; got %d", resp.Code)
	}
	var stats map[string]any
	json.Unmarshal(resp.Body.Bytes(), &stats)
	if stats["total"] != float64(0) {
		t.Errorf("nil AI stats total = %v; want 0", stats["total"])
	}
}
