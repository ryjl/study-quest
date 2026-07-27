package service

import (
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// TestAcknowledgeJob 是 AcknowledgeJob(failed→skipped)的聚焦单测。这个端点存在的
// 理由:episode 无字幕导致 summary/quiz job 永久 failed,retry 无意义(字幕不会凭空
// 出现),但留在 failed 列表会淹没真实的新失败。acknowledge 让 admin 把它清出失败列表。
//
// 覆盖三条路径:
//  1. 非 failed 状态(queued)→ ErrJobNotFailed(409)。
//  2. failed 状态 → 成功翻成 skipped,detail 带 "admin acknowledged:" 前缀 + 原 error。
//  3. 不存在的 job id → ErrJobNotFound。
func TestAcknowledgeJob(t *testing.T) {
	svc, contentRepo, _, _ := aiServiceTestEnv(t)

	// 建一门课 + 课时(满足 AIJob 的 not-null 外键语义)。
	course := &model.Course{Title: "ack-test"}
	if err := svc.db.Create(course).Error; err != nil {
		t.Fatalf("create course: %v", err)
	}
	ep := &model.Episode{Title: "ep1", CourseID: course.ID}
	if err := svc.db.Create(ep).Error; err != nil {
		t.Fatalf("create episode: %v", err)
	}

	// Case 1: queued job → ErrJobNotFailed。acknowledge 只对 failed 有意义。
	queuedJob := &model.AIJob{JobType: "summary", EpisodeID: &ep.ID, CourseID: &course.ID, Status: "queued"}
	if err := svc.db.Create(queuedJob).Error; err != nil {
		t.Fatalf("create queued job: %v", err)
	}
	if err := svc.AcknowledgeJob(queuedJob.ID); err != repository.ErrJobNotFailed {
		t.Fatalf("AcknowledgeJob on queued job: want ErrJobNotFailed, got %v", err)
	}

	// Case 2: failed job → skipped,detail 保留原 error。
	failedJob := &model.AIJob{
		JobType: "summary", EpisodeID: &ep.ID, CourseID: &course.ID,
		Status: "failed", Error: "no subtitle for this episode",
	}
	if err := svc.db.Create(failedJob).Error; err != nil {
		t.Fatalf("create failed job: %v", err)
	}
	if err := svc.AcknowledgeJob(failedJob.ID); err != nil {
		t.Fatalf("AcknowledgeJob on failed job: %v", err)
	}
	after, err := contentRepo.GetJob(failedJob.ID)
	if err != nil {
		t.Fatalf("GetJob after acknowledge: %v", err)
	}
	if after.Status != "skipped" {
		t.Errorf("status after acknowledge: want skipped, got %s", after.Status)
	}
	// detail 应带 "admin acknowledged:" 前缀 + 原错误,让 admin 在历史里仍能看到为什么忽略。
	wantDetail := "admin acknowledged: no subtitle for this episode"
	if after.Error != wantDetail {
		t.Errorf("detail after acknowledge: want %q, got %q", wantDetail, after.Error)
	}

	// Case 3: 不存在的 job id → ErrJobNotFound。
	if err := svc.AcknowledgeJob(99999); err != repository.ErrJobNotFound {
		t.Fatalf("AcknowledgeJob on missing job: want ErrJobNotFound, got %v", err)
	}
}
