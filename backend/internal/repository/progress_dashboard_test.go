package repository

import (
	"studyquest/backend/internal/testutil"
	"testing"
	"time"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// These tests cover the dashboard aggregates added in progress_repo.go. Each
// is a read-only SQL group/join, so the focus is on edge cases: empty table,
// the since-cutoff for active users, the date window for the trend, and
// leaderboard ordering/ties. Uses a file-backed DB (not :memory:) for the
// tests that set explicit timestamps, so all rows share one clock-free schema
// — same rationale as progress_atomic_test.go.

func setupDashboardTestDB(t *testing.T) *gorm.DB {
	return testutil.NewFileDB(t)
}

// at is a compact helper to build an explicit timestamp for fixture rows, so
// we can place activity on specific days for the trend/since-cutoff tests.
func at(s string) time.Time {
	t, _ := time.Parse("2006-01-02 15:04:05", s)
	return t
}

// seedProgressRow inserts a user_progresses row with explicit fields for the
// aggregate fixtures (watch_seconds, is_completed, updated_at).
func seedProgressRow(t *testing.T, db *gorm.DB, uid, eid uint, watchSec int, completed bool, updated time.Time) {
	t.Helper()
	c := 0
	if completed {
		c = 1
	}
	p := model.UserProgress{
		UserID: uid, EpisodeID: eid,
		WatchSeconds: watchSec, LastPositionSeconds: watchSec,
		IsCompleted: c, UpdatedAt: updated,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("seed progress: %v", err)
	}
}

// seedWatchEvent inserts a watch_events row at an explicit started_at. Used by
// the activity/trend tests now that CountActiveUsersSince and
// RecentDailyWatchSeconds read from watch_events (not user_progresses).
func seedWatchEvent(t *testing.T, db *gorm.DB, uid, eid, cid uint, contentType string, durSec int, started time.Time) {
	t.Helper()
	ev := model.WatchEvent{
		UserID: uid, EpisodeID: eid, CourseID: cid,
		ContentType:     contentType,
		StartedAt:       started,
		EndedAt:         started.Add(time.Duration(durSec) * time.Second),
		DurationSeconds: durSec,
	}
	if err := db.Create(&ev).Error; err != nil {
		t.Fatalf("seed watch event: %v", err)
	}
}

// TestDashboardAggregatesEmpty verifies every aggregate degrades to a safe
// zero / empty slice on a fresh DB with no progress rows.
func TestDashboardAggregatesEmpty(t *testing.T) {
	db := setupDashboardTestDB(t)
	repo := NewProgressRepository(db)

	if got, err := repo.SumTotalWatchSeconds(); err != nil || got != 0 {
		t.Errorf("SumTotalWatchSeconds empty: got %d err %v, want 0", got, err)
	}
	if got, err := repo.CountCompletedEpisodes(); err != nil || got != 0 {
		t.Errorf("CountCompletedEpisodes empty: got %d err %v, want 0", got, err)
	}
	if got, err := repo.CountActiveUsersSince(time.Now().Add(-24 * time.Hour)); err != nil || got != 0 {
		t.Errorf("CountActiveUsersSince empty: got %d err %v, want 0", got, err)
	}
	if got, err := repo.RecentDailyWatchSeconds(7); err != nil {
		t.Errorf("RecentDailyWatchSeconds empty: err %v", err)
	} else if len(got) != 0 {
		t.Errorf("RecentDailyWatchSeconds empty: got %d rows, want 0", len(got))
	}
	if got, err := repo.TopUsersByWatchSeconds(5); err != nil {
		t.Errorf("TopUsersByWatchSeconds empty: err %v", err)
	} else if len(got) != 0 {
		t.Errorf("TopUsersByWatchSeconds empty: got %d rows, want 0", len(got))
	}
	if got, err := repo.TopCoursesByCompletions(5); err != nil {
		t.Errorf("TopCoursesByCompletions empty: err %v", err)
	} else if len(got) != 0 {
		t.Errorf("TopCoursesByCompletions empty: got %d rows, want 0", len(got))
	}
}

// TestSumTotalWatchSeconds adds watch_seconds across all users/episodes.
func TestSumTotalWatchSeconds(t *testing.T) {
	db := setupDashboardTestDB(t)
	repo := NewProgressRepository(db)

	seedProgressRow(t, db, 1, 10, 100, false, at("2026-01-01 00:00:00"))
	seedProgressRow(t, db, 1, 11, 200, false, at("2026-01-01 00:00:00"))
	seedProgressRow(t, db, 2, 10, 50, false, at("2026-01-01 00:00:00"))

	got, err := repo.SumTotalWatchSeconds()
	if err != nil {
		t.Fatalf("SumTotalWatchSeconds: %v", err)
	}
	if got != 350 {
		t.Fatalf("sum = %d, want 350 (100+200+50)", got)
	}
}

// TestCountCompletedEpisodes counts only is_completed=1 rows.
func TestCountCompletedEpisodes(t *testing.T) {
	db := setupDashboardTestDB(t)
	repo := NewProgressRepository(db)

	seedProgressRow(t, db, 1, 10, 100, true, at("2026-01-01 00:00:00"))
	seedProgressRow(t, db, 1, 11, 200, false, at("2026-01-01 00:00:00"))
	seedProgressRow(t, db, 2, 12, 50, true, at("2026-01-01 00:00:00"))

	got, err := repo.CountCompletedEpisodes()
	if err != nil {
		t.Fatalf("CountCompletedEpisodes: %v", err)
	}
	if got != 2 {
		t.Fatalf("completed = %d, want 2", got)
	}
}

// TestCountActiveUsersSince counts DISTINCT users with a watch event at/after
// the cutoff. Events before the cutoff must not count. Reads from watch_events
// (the source of truth for activity), so the fixture seeds events, not
// user_progresses rows.
func TestCountActiveUsersSince(t *testing.T) {
	db := setupDashboardTestDB(t)
	repo := NewProgressRepository(db)

	cutoff := at("2026-01-02 00:00:00")
	// User 1: active after cutoff (counts). Two events but distinct = 1 user.
	seedWatchEvent(t, db, 1, 10, 100, "learning", 10, at("2026-01-03 12:00:00"))
	seedWatchEvent(t, db, 1, 11, 100, "learning", 10, at("2026-01-03 13:00:00"))
	// User 2: active BEFORE cutoff (must not count).
	seedWatchEvent(t, db, 2, 10, 100, "learning", 10, at("2026-01-01 00:00:00"))
	// User 3: exactly at cutoff boundary (>= is inclusive → counts).
	seedWatchEvent(t, db, 3, 10, 100, "learning", 10, cutoff)

	got, err := repo.CountActiveUsersSince(cutoff)
	if err != nil {
		t.Fatalf("CountActiveUsersSince: %v", err)
	}
	if got != 2 {
		t.Fatalf("active users = %d, want 2 (users 1 and 3; user 2 before cutoff)", got)
	}
}

// TestTopUsersByWatchSeconds orders users by total watch_seconds desc and
// respects the limit. Ties are left to SQLite's stable-ish ordering (not
// asserted beyond "both present").
func TestTopUsersByWatchSeconds(t *testing.T) {
	db := setupDashboardTestDB(t)
	repo := NewProgressRepository(db)

	// User A: 300s, User B: 100s, User C: 500s (two rows summing to it).
	seedProgressRow(t, db, 10, 1, 300, false, at("2026-01-01 00:00:00"))
	seedProgressRow(t, db, 20, 1, 100, false, at("2026-01-01 00:00:00"))
	seedProgressRow(t, db, 30, 1, 200, false, at("2026-01-01 00:00:00"))
	seedProgressRow(t, db, 30, 2, 300, false, at("2026-01-01 00:00:00"))

	got, err := repo.TopUsersByWatchSeconds(3)
	if err != nil {
		t.Fatalf("TopUsersByWatchSeconds: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	// Descending: C(500) → A(300) → B(100).
	if got[0].UserID != 30 || got[0].WatchSeconds != 500 {
		t.Errorf("rank 1: got user %d / %d s, want 30 / 500", got[0].UserID, got[0].WatchSeconds)
	}
	if got[1].UserID != 10 || got[1].WatchSeconds != 300 {
		t.Errorf("rank 2: got user %d / %d s, want 10 / 300", got[1].UserID, got[1].WatchSeconds)
	}
	if got[2].UserID != 20 || got[2].WatchSeconds != 100 {
		t.Errorf("rank 3: got user %d / %d s, want 20 / 100", got[2].UserID, got[2].WatchSeconds)
	}

	// limit=2 → only the top 2.
	got2, _ := repo.TopUsersByWatchSeconds(2)
	if len(got2) != 2 || got2[0].UserID != 30 {
		t.Errorf("limit 2: got %+v, want [30, 10]", got2)
	}
}

// TestTopCoursesByCompletions counts completed progress rows grouped by the
// episode's course, descending, with a limit.
func TestTopCoursesByCompletions(t *testing.T) {
	db := setupDashboardTestDB(t)
	repo := NewProgressRepository(db)

	// Two courses, each with episodes.
	c1 := &model.Course{Title: "c1", SubjectID: 1}
	c2 := &model.Course{Title: "c2", SubjectID: 1}
	db.Create(c1)
	db.Create(c2)
	db.Create(&model.CourseGrade{CourseID: c1.ID, Grade: model.Grade("g")})
	db.Create(&model.CourseGrade{CourseID: c2.ID, Grade: model.Grade("g")})
	epC1a := &model.Episode{Title: "e", CourseID: c1.ID, VideoRelativePath: "x"}
	epC1b := &model.Episode{Title: "e", CourseID: c1.ID, VideoRelativePath: "y"}
	epC2 := &model.Episode{Title: "e", CourseID: c2.ID, VideoRelativePath: "z"}
	db.Create(epC1a)
	db.Create(epC1b)
	db.Create(epC2)

	// Course 1: 3 completions (2 users on epC1a, 1 on epC1b). Course 2: 1.
	seedProgressRow(t, db, 1, epC1a.ID, 10, true, at("2026-01-01 00:00:00"))
	seedProgressRow(t, db, 2, epC1a.ID, 10, true, at("2026-01-01 00:00:00"))
	seedProgressRow(t, db, 3, epC1b.ID, 10, true, at("2026-01-01 00:00:00"))
	seedProgressRow(t, db, 1, epC2.ID, 10, true, at("2026-01-01 00:00:00"))
	// An incomplete row on course 1 must NOT count.
	seedProgressRow(t, db, 4, epC1a.ID, 10, false, at("2026-01-01 00:00:00"))

	got, err := repo.TopCoursesByCompletions(5)
	if err != nil {
		t.Fatalf("TopCoursesByCompletions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 courses", len(got))
	}
	if got[0].CourseID != c1.ID || got[0].CompletedEpisodes != 3 {
		t.Errorf("rank 1: got course %d / %d completions, want %d / 3", got[0].CourseID, got[0].CompletedEpisodes, c1.ID)
	}
	if got[1].CourseID != c2.ID || got[1].CompletedEpisodes != 1 {
		t.Errorf("rank 2: got course %d / %d completions, want %d / 1", got[1].CourseID, got[1].CompletedEpisodes, c2.ID)
	}
}
