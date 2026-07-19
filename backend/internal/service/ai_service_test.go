package service

import (
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// aiServiceTestEnv wires a real aiService (in-memory DB, real repos, no providers)
// for the summary/enqueue-level tests. Mirrors aiServiceQuizTestEnv but kept
// separate so each test file's intent is clear.
func aiServiceTestEnv(t *testing.T) (*aiService, repository.AIContentRepository, repository.EpisodeRepository, repository.CourseRepository) {
	t.Helper()
	db := setupTestDB(t)
	contentRepo := repository.NewAIContentRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	svc := NewAIService(
		db,
		contentRepo,
		episodeRepo,
		courseRepo,
		nil,                             // no provider resolver — enqueue path doesn't need it
		nil,                             // no unlock service
		repository.NewUserRepository(db),
	).(*aiService)
	return svc, contentRepo, episodeRepo, courseRepo
}

// seedEpisodeForSummary creates a course + episode and returns the episode id.
// Helper for EnqueueSummary tests.
func seedEpisodeForSummary(t *testing.T, episodeRepo repository.EpisodeRepository, courseRepo repository.CourseRepository) uint {
	t.Helper()
	course := &model.Course{Title: "Summary Test Course"}
	if err := courseRepo.Create(course); err != nil {
		t.Fatalf("create course: %v", err)
	}
	ep := &model.Episode{Title: "Summary Test Ep", CourseID: course.ID, VideoRelativePath: "/x.mp4", SortOrder: 1}
	if err := episodeRepo.Create(ep); err != nil {
		t.Fatalf("create episode: %v", err)
	}
	return ep.ID
}

// TestEnqueueSummary_DedupPendingJob 验证去重门:某 episode 已有在途 summary job
// (queued/processing)时,再调 EnqueueSummary 应跳过它,不堆第二条 job。
// 这是用户钦点的"连点堆 job"bug 的回归测试。
func TestEnqueueSummary_DedupPendingJob(t *testing.T) {
	svc, contentRepo, episodeRepo, courseRepo := aiServiceTestEnv(t)
	epID := seedEpisodeForSummary(t, episodeRepo, courseRepo)

	// 第一次入队 —— 应成功。
	enqueued, skipped, err := svc.EnqueueSummary([]uint{epID})
	if err != nil {
		t.Fatalf("first EnqueueSummary: %v", err)
	}
	if len(enqueued) != 1 || enqueued[0] != epID {
		t.Fatalf("first call: expected enqueued=[%d], got %v skipped=%v", epID, enqueued, skipped)
	}
	if len(skipped) != 0 {
		t.Fatalf("first call: expected no skips, got %v", skipped)
	}

	// 第二次入队 —— 应被去重门跳过。
	enqueued2, skipped2, err := svc.EnqueueSummary([]uint{epID})
	if err != nil {
		t.Fatalf("second EnqueueSummary: %v", err)
	}
	if len(enqueued2) != 0 {
		t.Errorf("second call: expected enqueued empty (dedup), got %v", enqueued2)
	}
	if reason, ok := skipped2[epID]; !ok {
		t.Errorf("second call: expected episode %d in skipped map, got %v", epID, skipped2)
	} else if reason == "" {
		t.Errorf("second call: skipped reason should not be empty")
	}

	// DB 里应仍只有 1 条 summary job(第一次入的那条)。
	var count int64
	svc.db.Model(&model.AIJob{}).Where("job_type = ? AND episode_id = ?", "summary", epID).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 summary job in DB after double-enqueue, got %d", count)
	}

	// 验证那条 job 真的存在(contentRepo 没说谎)。
	_ = contentRepo // 留着方便断言扩展;上面的 db 直查已够。
}

// TestEnqueueSummary_ForceRegenerateOverwrites 验证"覆盖式重生成"语义:
// 已有 done 状态的 summary job(不在途)时,调 EnqueueSummary 应入队新 job,
// 因为 done 不算"在途",admin 强制重跑覆盖是允许的。
func TestEnqueueSummary_ForceRegenerateOverwrites(t *testing.T) {
	svc, _, episodeRepo, courseRepo := aiServiceTestEnv(t)
	epID := seedEpisodeForSummary(t, episodeRepo, courseRepo)

	// 先插一条 done 的 summary job(模拟历史已完成)。
	epIDCopy, courseIDCopy := epID, uint(1)
	doneJob := &model.AIJob{
		JobType:   "summary",
		EpisodeID: &epIDCopy,
		CourseID:  &courseIDCopy,
		Status:    "done",
		Priority:  prioritySummary,
	}
	if err := svc.contentRepo.CreateJob(doneJob); err != nil {
		t.Fatalf("seed done job: %v", err)
	}

	// 调 EnqueueSummary —— done 不算在途,应入队新 job。
	enqueued, skipped, err := svc.EnqueueSummary([]uint{epID})
	if err != nil {
		t.Fatalf("EnqueueSummary: %v", err)
	}
	if len(enqueued) != 1 || enqueued[0] != epID {
		t.Fatalf("expected enqueued=[%d] (force overwrite), got %v skipped=%v", epID, enqueued, skipped)
	}
	if len(skipped) != 0 {
		t.Errorf("expected no skips, got %v", skipped)
	}

	// DB 里现在应该有 2 条 summary job(1 done + 1 queued)。
	var count int64
	svc.db.Model(&model.AIJob{}).Where("job_type = ? AND episode_id = ?", "summary", epID).Count(&count)
	if count != 2 {
		t.Errorf("expected 2 summary jobs (1 done + 1 new queued), got %d", count)
	}
}

// TestEnqueueSummary_NoResolverStillEnqueues 验证 nil-safe 语义没破:
// AI resolver 为 nil 时,EnqueueSummary 仍入队(handler 这层在 aiService==nil
// 时会 503,但 service 层不应假设 resolver 已配置 —— 入队只是建 job 行,
// 真跑时 worker 检查 resolver)。这条是"AI 附加层"原则的回归保护。
func TestEnqueueSummary_NoResolverStillEnqueues(t *testing.T) {
	// aiServiceTestEnv 默认 resolver=nil,正好覆盖。
	svc, _, episodeRepo, courseRepo := aiServiceTestEnv(t)
	epID := seedEpisodeForSummary(t, episodeRepo, courseRepo)

	if svc.resolver != nil {
		t.Fatalf("test setup invariant: resolver should be nil for this test")
	}

	enqueued, _, err := svc.EnqueueSummary([]uint{epID})
	if err != nil {
		t.Fatalf("EnqueueSummary with nil resolver: %v", err)
	}
	if len(enqueued) != 1 {
		t.Errorf("expected enqueued=[%d] even with nil resolver, got %v", epID, enqueued)
	}

	// job 行确实落库。
	var count int64
	svc.db.Model(&model.AIJob{}).Where("job_type = ? AND episode_id = ?", "summary", epID).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 summary job persisted, got %d", count)
	}
}

// TestEnqueueSummary_NonExistentEpisodeSkipped 验证传入不存在的 episode id
// 时进 skipped 而不是报错(批量入队语义,partial success 正常)。
func TestEnqueueSummary_NonExistentEpisodeSkipped(t *testing.T) {
	svc, _, _, _ := aiServiceTestEnv(t)
	const fakeID uint = 9999

	enqueued, skipped, err := svc.EnqueueSummary([]uint{fakeID})
	if err != nil {
		t.Fatalf("EnqueueSummary: %v", err)
	}
	if len(enqueued) != 0 {
		t.Errorf("expected no enqueues for non-existent episode, got %v", enqueued)
	}
	if reason, ok := skipped[fakeID]; !ok {
		t.Errorf("expected episode %d in skipped, got %v", fakeID, skipped)
	} else if reason == "" {
		t.Errorf("skipped reason should not be empty")
	}
}
