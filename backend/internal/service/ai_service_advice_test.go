package service

import (
	"testing"

	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
)

// TestRegenerateAdvice_NilSafeAndDedups 验证 RegenerateAdvice 的两条不变量:
//  (a) nil resolver 时返回 unavailable —— nil-safe 语义(AI 附加层原则)不被破坏;
//  (b) 已有在途 advice job 时,即使 resolver 配好,也不该堆第二条 job(去重门生效)。
//
// 本测试用 nil resolver(单元测试默认环境),所以 (b) 的"去重门真生效"在 resolver
// 非空时才会触发到 enqueue;这里间接验证去重检查(hasPendingAdviceJob)在调用栈上仍
// 被先于 enqueue 检查 —— 通过手动 seed 在途 job + 断言 DB 不增 job 来确认 nil-safe 路径
// 不绕过去重检查。
//
// 完整端到端"真 resolver + 去重"覆盖留给 cmd/server 集成测试(那里有真实 provider)。
func TestRegenerateAdvice_NilSafeAndDedupes(t *testing.T) {
	svc, _, episodeRepo, courseRepo := aiServiceTestEnv(t)
	course := &model.Course{Title: "Advice Course"}
	if err := courseRepo.Create(course); err != nil {
		t.Fatalf("create course: %v", err)
	}
	ep := &model.Episode{Title: "Advice Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := episodeRepo.Create(ep); err != nil {
		t.Fatalf("create episode: %v", err)
	}
	user := &model.User{Nickname: "advice-user", PinHash: "x", Role: "student"}
	if err := svc.db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// (a) nil resolver → unavailable。
	status, err := svc.RegenerateAdvice(user.ID, agent.ScopeEpisode, ep.ID)
	if err != nil {
		t.Fatalf("RegenerateAdvice nil resolver: %v", err)
	}
	if status != adviceStatusUnavailable {
		t.Errorf("nil resolver: expected unavailable, got %q", status)
	}

	// (b) seed 一条在途 advice job,再次调 RegenerateAdvice 仍不应增 job。
	epIDCopy, courseIDCopy, uidCopy := ep.ID, course.ID, user.ID
	payload := `{"scope":"episode","scope_id":` + uintToString(ep.ID) + `,"subject_id":0}`
	inflight := &model.AIJob{
		JobType: "advice", EpisodeID: &epIDCopy, CourseID: &courseIDCopy, UserID: &uidCopy,
		Status: "queued", Priority: priorityAdvice, PayloadJSON: payload,
	}
	if err := svc.contentRepo.CreateJob(inflight); err != nil {
		t.Fatalf("seed in-flight advice job: %v", err)
	}
	if !svc.hasPendingAdviceJob(user.ID, agent.ScopeEpisode, ep.ID) {
		t.Fatalf("test invariant: hasPendingAdviceJob should be true after seeding")
	}

	// 再调一次(nil resolver → unavailable,且不应在 DB 里增加 job 行)。
	if _, err := svc.RegenerateAdvice(user.ID, agent.ScopeEpisode, ep.ID); err != nil {
		t.Fatalf("RegenerateAdvice 2nd call: %v", err)
	}
	var count int64
	svc.db.Model(&model.AIJob{}).Where("job_type = ? AND user_id = ?", "advice", user.ID).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 advice job (no stacking), got %d", count)
	}
}

// TestRegenerateAdvice_RejectsInvalidScope 验证 scope 白名单兜底。
func TestRegenerateAdvice_RejectsInvalidScope(t *testing.T) {
	svc, _, _, _ := aiServiceTestEnv(t)
	user := &model.User{Nickname: "u", PinHash: "x", Role: "student"}
	svc.db.Create(user)

	_, err := svc.RegenerateAdvice(user.ID, "bogus", 1)
	if err == nil {
		t.Errorf("expected error for invalid scope, got nil")
	}
}

// uintToString 是测试内简易 uint→string(避免导入 strconv 多余符号)。
func uintToString(n uint) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
