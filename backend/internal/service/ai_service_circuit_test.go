package service

import (
	"testing"

	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
)

// ai_service_circuit_test.go 验证「连续失败熔断」机制(见 ai_service.go 的
// maxConsecutiveFailures + consecutiveFailures / consecutiveQuizFailures /
// consecutiveAdviceFailures)。背景:episode 31 quiz 客户端轮询 + 失败重入队 = 9 次
// 失败烧 50 万 token。修复=同一任务连续失败 ≥3 次后,lazy 入队返回 cooling 拒绝再试。

// seedJob 建一条 ai_jobs 行,status 控制(测试只关心 status + 排序)。
func seedJob(t *testing.T, svc *aiService, jobType, status string, episodeID, userID uint) {
	t.Helper()
	epID, courseID, uid := episodeID, uint(1), userID
	if userID == 0 {
		uid = 1
	}
	j := &model.AIJob{
		JobType: jobType, EpisodeID: &epID, CourseID: &courseID, UserID: &uid,
		Status: status, Priority: 1,
	}
	if err := svc.contentRepo.CreateJob(j); err != nil {
		t.Fatalf("seed %s job: %v", jobType, err)
	}
}

// TestConsecutiveFailuresCountsTrailingFailed 验证「连击」语义:只数尾部连续 failed,
// 遇到 done/queued/processing 就停。历史失败不抵消最近的成功。
func TestConsecutiveFailuresCountsTrailingFailed(t *testing.T) {
	svc, _, epRepo, _ := aiServiceTestEnv(t)
	ep := &model.Episode{Title: "ep", CourseID: 1, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := epRepo.Create(ep); err != nil {
		t.Fatal(err)
	}
	// 顺序:failed, failed, done, failed —— 最新一条是 failed,往前数到 done 中断 = 1。
	seedJob(t, svc, "summary", "failed", ep.ID, 0)
	seedJob(t, svc, "summary", "failed", ep.ID, 0)
	seedJob(t, svc, "summary", "done", ep.ID, 0)
	seedJob(t, svc, "summary", "failed", ep.ID, 0)
	if got := svc.consecutiveFailures("summary", ep.ID); got != 1 {
		t.Errorf("trailing failed after done: got %d, want 1", got)
	}
}

// TestConsecutiveFailuresStopsAtThreshold 验证只查最近 N 条(maxConsecutiveFailures=3):
// 即使历史上有 5 条连续 failed,函数也只看最近 3 条,返回 3(到阈值即熔断)。
func TestConsecutiveFailuresStopsAtThreshold(t *testing.T) {
	svc, _, epRepo, _ := aiServiceTestEnv(t)
	ep := &model.Episode{Title: "ep", CourseID: 1, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := epRepo.Create(ep); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		seedJob(t, svc, "summary", "failed", ep.ID, 0)
	}
	if got := svc.consecutiveFailures("summary", ep.ID); got != maxConsecutiveFailures {
		t.Errorf("5 trailing failed: got %d, want %d (capped at threshold)", got, maxConsecutiveFailures)
	}
}

// TestConsecutiveFailuresResetsAfterNonFailed 验证 admin RetryJob 的「天然重置」:
// RetryJob 把最新一条 failed 改成 queued,consecutiveFailures 第一步就遇到非 failed
// 中断,返回 0 —— 熔断自动解除,无需改 RetryJob 的 SQL。
func TestConsecutiveFailuresResetsAfterNonFailed(t *testing.T) {
	svc, _, epRepo, _ := aiServiceTestEnv(t)
	ep := &model.Episode{Title: "ep", CourseID: 1, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := epRepo.Create(ep); err != nil {
		t.Fatal(err)
	}
	// 3 条 failed = 已熔断。
	for i := 0; i < 3; i++ {
		seedJob(t, svc, "summary", "failed", ep.ID, 0)
	}
	if got := svc.consecutiveFailures("summary", ep.ID); got != 3 {
		t.Fatalf("pre-retry: got %d, want 3", got)
	}
	// admin 把最新一条 failed 改成 queued(模拟 RetryJob)。
	var latest model.AIJob
	if err := svc.db.Where("job_type = ? AND episode_id = ? AND status = ?", "summary", ep.ID, "failed").
		Order("created_at DESC").First(&latest).Error; err != nil {
		t.Fatalf("load latest failed job: %v", err)
	}
	if err := svc.db.Model(&model.AIJob{}).Where("id = ?", latest.ID).
		Update("status", "queued").Error; err != nil {
		t.Fatalf("simulate retry: %v", err)
	}
	// 现在最新一条是 queued,连击中断 → 0,熔断解除。
	if got := svc.consecutiveFailures("summary", ep.ID); got != 0 {
		t.Errorf("after retry: got %d, want 0 (retry resets the streak)", got)
	}
}

// TestGetOrEnqueueQuizCoolingAfterRepeatedFailures 端到端验证 quiz 熔断:
// seed 3 条同 (user,episode) 的 failed quiz job 后,GetOrEnqueueQuiz 应返回 cooling
// 而不是入队新 job。对应用户要的「3 次失败永久封」。
func TestGetOrEnqueueQuizCoolingAfterRepeatedFailures(t *testing.T) {
	svc, _, epRepo, _ := aiServiceTestEnv(t)
	course := &model.Course{Title: "c"}
	if err := svc.courseRepo.Create(course); err != nil {
		t.Fatal(err)
	}
	ep := &model.Episode{Title: "ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := epRepo.Create(ep); err != nil {
		t.Fatal(err)
	}
	user := &model.User{Nickname: "u", PinHash: "x", Role: "student"}
	if err := svc.db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	// 2 条 failed:还不到阈值,应正常入队(返回 generating 或 unavailable,不是 cooling)。
	seedJob(t, svc, "quiz", "failed", ep.ID, user.ID)
	seedJob(t, svc, "quiz", "failed", ep.ID, user.ID)
	status, _, err := svc.GetOrEnqueueQuiz(user.ID, ep.ID)
	if err != nil {
		t.Fatalf("GetOrEnqueueQuiz with 2 failures: %v", err)
	}
	if status == quizStatusCooling {
		t.Errorf("2 failures should NOT trip circuit yet (threshold=3), got cooling")
	}
	// 第 3 条 failed:达到阈值,应熔断。
	seedJob(t, svc, "quiz", "failed", ep.ID, user.ID)
	status, _, err = svc.GetOrEnqueueQuiz(user.ID, ep.ID)
	if err != nil {
		t.Fatalf("GetOrEnqueueQuiz with 3 failures: %v", err)
	}
	if status != quizStatusCooling {
		t.Errorf("3 failures should trip circuit, got %q (want cooling)", status)
	}
}

// TestGetOrEnqueueQuizCoolingIsPerUser 验证熔断是 per-user 的:A 学生 3 次失败
// 不应影响 B 学生(A 失败熔断时 B 仍能正常入队)。这是 consecutiveQuizFailures 按
// (user_id, episode_id) 三元组计数的关键意义。
func TestGetOrEnqueueQuizCoolingIsPerUser(t *testing.T) {
	svc, _, epRepo, _ := aiServiceTestEnv(t)
	course := &model.Course{Title: "c"}
	if err := svc.courseRepo.Create(course); err != nil {
		t.Fatal(err)
	}
	ep := &model.Episode{Title: "ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := epRepo.Create(ep); err != nil {
		t.Fatal(err)
	}
	userA := &model.User{Nickname: "A", PinHash: "x", Role: "student"}
	userB := &model.User{Nickname: "B", PinHash: "x", Role: "student"}
	if err := svc.db.Create(userA).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.db.Create(userB).Error; err != nil {
		t.Fatal(err)
	}
	// userA 失败 3 次 → 熔断。
	for i := 0; i < 3; i++ {
		seedJob(t, svc, "quiz", "failed", ep.ID, userA.ID)
	}
	statusA, _, _ := svc.GetOrEnqueueQuiz(userA.ID, ep.ID)
	if statusA != quizStatusCooling {
		t.Errorf("userA (3 failures): got %q, want cooling", statusA)
	}
	// userB 从未失败,应正常(不熔断)。
	statusB, _, _ := svc.GetOrEnqueueQuiz(userB.ID, ep.ID)
	if statusB == quizStatusCooling {
		t.Errorf("userB (0 failures): should not be affected by userA's failures, got cooling")
	}
}

// TestConsecutiveAdviceFailuresMatchesScope 验证 advice 熔断按 scope 精确匹配:
// 同一 user 的 episode 级失败不应熔断 course 级(advice 的 scope_id 存在 PayloadJSON,
// 不能用 SQL 直接 WHERE,consecutiveAdviceFailures 走 Go 层解码匹配)。
func TestConsecutiveAdviceFailuresMatchesScope(t *testing.T) {
	svc, _, epRepo, _ := aiServiceTestEnv(t)
	course := &model.Course{Title: "c"}
	if err := svc.courseRepo.Create(course); err != nil {
		t.Fatal(err)
	}
	ep := &model.Episode{Title: "ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := epRepo.Create(ep); err != nil {
		t.Fatal(err)
	}
	user := &model.User{Nickname: "u", PinHash: "x", Role: "student"}
	if err := svc.db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	// seed 3 条 episode 级 failed advice job(PayloadJSON 带 scope=episode)。
	for i := 0; i < 3; i++ {
		epIDCopy, courseIDCopy, uidCopy := ep.ID, course.ID, user.ID
		payload := `{"scope":"episode","scope_id":` + uintToString(ep.ID) + `,"subject_id":0}`
		j := &model.AIJob{
			JobType: "advice", EpisodeID: &epIDCopy, CourseID: &courseIDCopy, UserID: &uidCopy,
			Status: "failed", Priority: priorityAdvice, PayloadJSON: payload,
		}
		if err := svc.contentRepo.CreateJob(j); err != nil {
			t.Fatalf("seed advice job: %v", err)
		}
	}
	// episode 级:3 次失败 → 熔断。
	if got := svc.consecutiveAdviceFailures(user.ID, agent.ScopeEpisode, ep.ID); got != 3 {
		t.Errorf("episode scope: got %d, want 3", got)
	}
	// course 级:同 user 但不同 scope,不应被 episode 级失败影响 → 0。
	if got := svc.consecutiveAdviceFailures(user.ID, agent.ScopeCourse, course.ID); got != 0 {
		t.Errorf("course scope (different from failed episode scope): got %d, want 0", got)
	}
}
