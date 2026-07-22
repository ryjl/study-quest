package service

import (
	"testing"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// TestOnSubtitleCompleted_EnqueuesPolishForWhisper is the integration test that reproduces the production
// OnSubtitleCompleted → polish-enqueue path with a REAL resolver (not nil) seeded with an enabled
// provider that has "polish" in its tags. This is the exact wiring main.go uses.
//
// If this test FAILS (no polish job created), the bug is in the code path itself — not a runtime
// config issue. If it PASSES, the code is correct and the "no polish job" symptom is a stale
// binary / wrong provider config at runtime.
func TestOnSubtitleCompleted_EnqueuesPolishForWhisper(t *testing.T) {
	db := testutil.NewFileDB(t)

	// Seed an enabled AIProvider with "polish" in tags — mirrors the production row
	// (id 1, gpt-5.6-luna, tags include "polish").
	provider := &model.AIProvider{
		Capability:    "chat",
		Name:        "test-chat",
		ProviderType: "openai_compat",
		BaseURL:     "http://localhost:0", // unreachable, but we never call it (OnSubtitleCompleted only enqueues)
		ModelName:   "test-model",
		Tags:       `["polish","summary"]`,
		IsEnabled:  true,
	}
	if err := db.Create(provider).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}

	// Build the SAME wiring main.go uses: real resolver from the provider repo, real aiService.
	aiProviderRepo := repository.NewAIProviderRepository(db)
	resolver := ai.NewProviderResolver(aiProviderRepo, "")
	contentRepo := repository.NewAIContentRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	svc := NewAIService(
		db, contentRepo, episodeRepo, courseRepo,
		resolver, nil, repository.NewUserRepository(db),
		repository.NewGlossaryRepository(db),
		repository.NewSubjectRepository(db),
		repository.NewAIPolishChunkRepository(db), // real repo — OnSubtitleCompleted may chain polish
		nil,                                        // no logRepo — structured-log writes not asserted
	).(*aiService)
	t.Cleanup(svc.Stop)

	// Verify the resolver actually resolves (proves resolver != nil is not enough — it must BUILD).
	if _, err := resolver.ResolveChatByPurpose("polish"); err != nil {
		t.Fatalf("resolver.ResolveChatByPurpose(polish) failed: %v — this is the root cause if shouldPolish is false in prod", err)
	}

	// Seed a course with AI ON + an episode + a whisper subtitle.
	course := &model.Course{
		Title: "polish-test", SubjectID: 1,
		ContentType: model.ContentLearning,
		AISummaryEnabled: true, AIQuizEnabled: true,
	}
	if err := courseRepo.Create(course); err != nil {
		t.Fatalf("create course: %v", err)
	}
	ep := &model.Episode{
		Title: "polish-ep", CourseID: course.ID,
		VideoRelativePath: "/x.mp4", SortOrder: 1,
	}
	if err := episodeRepo.Create(ep); err != nil {
		t.Fatalf("create episode: %v", err)
	}
	// Whisper subtitle (the OnSubtitleCompleted entry point only polishes whisper source).
	if err := episodeRepo.SaveSubtitle(&model.Subtitle{
		EpisodeID: ep.ID, Language: "zh-CN", Label: "中文",
		VttContent: "WEBVTT\n\n1\n00:00:00.000 --> 00:00:01.000\n考算\n",
		RawVttContent: "WEBVTT\n\n1\n00:00:00.000 --> 00:00:01.000\n考算\n",
		Source: "whisper", IsPrimary: true,
	}); err != nil {
		t.Fatalf("seed subtitle: %v", err)
	}

	// The moment of truth: trigger the hook.
	svc.OnSubtitleCompleted(ep.ID)

	// Assert a polish job was created.
	var n int64
	svc.db.Model(&model.AIJob{}).
		Where("job_type = ? AND episode_id = ?", "polish", ep.ID).Count(&n)
	if n != 1 {
		t.Errorf("expected 1 polis job after OnSubtitleCompleted, got %d — shouldPolish was false (see log above)", n)
	}
}
