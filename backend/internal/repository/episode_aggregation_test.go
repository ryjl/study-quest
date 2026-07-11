package repository

import (
	"studyquest/backend/internal/testutil"
	"studyquest/backend/internal/model"
	"testing"
)

func int64p(v int64) *int64 { return &v }
func intp(v int) *int       { return &v }

func TestEpisodeAggregations(t *testing.T) {
	db := setupTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	courseRepo := NewCourseRepository(db)
	episodeRepo := NewEpisodeRepository(db)
	chapterRepo := NewChapterRepository(db)

	// Two courses across two subjects.
	c1 := &model.Course{Title: "数学课", Grade: "3", SubjectID: subjects["math"].ID}
	c2 := &model.Course{Title: "语文课", Grade: "3", SubjectID: subjects["chinese"].ID}
	if err := courseRepo.Create(c1); err != nil {
		t.Fatal(err)
	}
	if err := courseRepo.Create(c2); err != nil {
		t.Fatal(err)
	}
	ch1 := &model.Chapter{CourseID: c1.ID, Title: "第一章", SortOrder: 1}
	if err := chapterRepo.Create(ch1); err != nil {
		t.Fatal(err)
	}

	// 6 episodes: 4 with durations (2 math + 2 chinese), 2 without (probe pending).
	eps := []*model.Episode{
		{CourseID: c1.ID, ChapterID: ch1.ID, SortOrder: 1, Title: "m1", VideoRelativePath: "/m1.mp4", DurationSeconds: intp(600), FileSize: int64p(100)},
		{CourseID: c1.ID, ChapterID: ch1.ID, SortOrder: 2, Title: "m2", VideoRelativePath: "/m2.mp4", DurationSeconds: intp(1200), FileSize: int64p(200)},
		{CourseID: c1.ID, ChapterID: 0, SortOrder: 3, Title: "m3", VideoRelativePath: "/m3.mp4"}, // no duration
		{CourseID: c2.ID, ChapterID: 0, SortOrder: 1, Title: "c1", VideoRelativePath: "/c1.mp4", DurationSeconds: intp(300), FileSize: int64p(50)},
		{CourseID: c2.ID, ChapterID: 0, SortOrder: 2, Title: "c2", VideoRelativePath: "/c2.mp4", DurationSeconds: intp(900), FileSize: int64p(80)},
		{CourseID: c2.ID, ChapterID: 0, SortOrder: 3, Title: "c3", VideoRelativePath: "/c3.mp4"}, // no duration
	}
	for _, e := range eps {
		if err := episodeRepo.Create(e); err != nil {
			t.Fatalf("create episode: %v", err)
		}
	}

	t.Run("CountAll", func(t *testing.T) {
		got, err := episodeRepo.CountAll()
		if err != nil {
			t.Fatal(err)
		}
		if got != 6 {
			t.Errorf("CountAll = %d, want 6", got)
		}
	})

	t.Run("CountByNullDuration", func(t *testing.T) {
		got, err := episodeRepo.CountByNullDuration()
		if err != nil {
			t.Fatal(err)
		}
		// The predicate matches "needs probe": duration OR cover missing. All 6
		// episodes here have no cover_url, so all 6 count.
		if got != 6 {
			t.Errorf("CountByNullDuration = %d, want 6 (all lack cover_url)", got)
		}
	})

	t.Run("SumTotalDurationSeconds", func(t *testing.T) {
		got, err := episodeRepo.SumTotalDurationSeconds()
		if err != nil {
			t.Fatal(err)
		}
		// 600 + 1200 + 300 + 900 = 3000
		if got != 3000 {
			t.Errorf("SumTotalDurationSeconds = %d, want 3000", got)
		}
	})

	t.Run("CountBySubject", func(t *testing.T) {
		got, err := episodeRepo.CountBySubject()
		if err != nil {
			t.Fatal(err)
		}
		if got["math"] != 3 || got["chinese"] != 3 {
			t.Errorf("CountBySubject = %+v, want math=3 chinese=3", got)
		}
	})

	t.Run("CountByCourse", func(t *testing.T) {
		got, err := episodeRepo.CountByCourse(c1.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != 3 {
			t.Errorf("CountByCourse(c1) = %d, want 3", got)
		}
	})

	t.Run("SumDurationByCourse", func(t *testing.T) {
		got, err := episodeRepo.SumDurationByCourse(c1.ID)
		if err != nil {
			t.Fatal(err)
		}
		// Only m1 + m2 have durations on c1: 600 + 1200 = 1800
		if got != 1800 {
			t.Errorf("SumDurationByCourse(c1) = %d, want 1800", got)
		}
	})

	t.Run("RecentDailyCount", func(t *testing.T) {
		got, err := episodeRepo.RecentDailyCount(7)
		if err != nil {
			t.Fatal(err)
		}
		// All 6 were just created today, so we expect at least one row summing to 6.
		var total int
		for _, r := range got {
			total += r.Count
		}
		if total != 6 {
			t.Errorf("RecentDailyCount total = %d, want 6 (rows=%d)", total, len(got))
		}
	})
}

func TestChapterCountByCourse(t *testing.T) {
	db := setupTestDB(t)
	subjects := testutil.SeedSubjects(t, db)
	courseRepo := NewCourseRepository(db)
	chapterRepo := NewChapterRepository(db)

	c := &model.Course{Title: "课", Grade: "1", SubjectID: subjects["math"].ID}
	if err := courseRepo.Create(c); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := chapterRepo.Create(&model.Chapter{CourseID: c.ID, Title: "ch", SortOrder: i}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := chapterRepo.CountByCourse(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Errorf("CountByCourse = %d, want 3", got)
	}

	// A non-existent course should report 0, not error.
	zero, err := chapterRepo.CountByCourse(99999)
	if err != nil {
		t.Fatal(err)
	}
	if zero != 0 {
		t.Errorf("CountByCourse(unknown) = %d, want 0", zero)
	}
}
