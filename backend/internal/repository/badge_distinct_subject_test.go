package repository

import (
	"studyquest/backend/internal/model"
	"testing"
)

// TestGetDistinctSubjectCompletedCount covers the distinct_subject_count rule
// aggregate: it counts how many DISTINCT subjects a user has at least one
// completed episode in, by joining user_progresses → episodes → courses.
//
// Schema notes for the fixture:
//   - courses.subject_id → subjects.id (seeded by setupTestDB's sibling helper
//     via a direct insert here to keep this file standalone)
//   - episodes.course_id → courses.id
//   - user_progresses.episode_id → episodes.id, is_completed ∈ {0,1}
func TestGetDistinctSubjectCompletedCount(t *testing.T) {
	db := setupTestDB(t)
	badgeRepo := NewBadgeRepository(db)

	// Three subjects.
	sMath := &model.Subject{Key: "math", Label: "数学", SortOrder: 1}
	sEng := &model.Subject{Key: "english", Label: "英语", SortOrder: 2}
	sChi := &model.Subject{Key: "chinese", Label: "语文", SortOrder: 3}
	for _, s := range []*model.Subject{sMath, sEng, sChi} {
		if err := db.Create(s).Error; err != nil {
			t.Fatalf("create subject: %v", err)
		}
	}

	// A course per subject, each with an episode.
	mkCourseEpisode := func(subjectID uint) (courseID, episodeID uint) {
		c := &model.Course{Title: "c", SubjectID: subjectID}
		if err := db.Create(c).Error; err != nil {
			t.Fatalf("create course: %v", err)
		}
		if err := db.Create(&model.CourseGrade{CourseID: c.ID, Grade: model.Grade("g1")}).Error; err != nil {
			t.Fatalf("create course grade: %v", err)
		}
		ep := &model.Episode{Title: "e", CourseID: c.ID, VideoRelativePath: "x.mp4"}
		if err := db.Create(ep).Error; err != nil {
			t.Fatalf("create episode: %v", err)
		}
		return c.ID, ep.ID
	}
	_, mathEp := mkCourseEpisode(sMath.ID)
	_, engEp := mkCourseEpisode(sEng.ID)
	_, chiEp := mkCourseEpisode(sChi.ID)

	// User completes episodes in math + english (2 distinct subjects). Chinese
	// episode is watched but NOT completed → must not count.
	const uid uint = 42
	completed := func(eid uint) *model.UserProgress {
		return &model.UserProgress{UserID: uid, EpisodeID: eid, IsCompleted: 1, WatchSeconds: 10}
	}
	incomplete := func(eid uint) *model.UserProgress {
		return &model.UserProgress{UserID: uid, EpisodeID: eid, IsCompleted: 0, WatchSeconds: 5}
	}
	for _, p := range []*model.UserProgress{completed(mathEp), completed(engEp), incomplete(chiEp)} {
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("create progress: %v", err)
		}
	}

	got, err := badgeRepo.GetDistinctSubjectCompletedCount(uid)
	if err != nil {
		t.Fatalf("GetDistinctSubjectCompletedCount: %v", err)
	}
	if got != 2 {
		t.Fatalf("distinct completed subjects = %d, want 2 (math+english; chinese incomplete)", got)
	}

	// Completing the chinese episode bumps the distinct count to 3.
	db.Model(&model.UserProgress{}).Where("user_id = ? AND episode_id = ?", uid, chiEp).
		Update("is_completed", 1)
	got, err = badgeRepo.GetDistinctSubjectCompletedCount(uid)
	if err != nil {
		t.Fatalf("GetDistinctSubjectCompletedCount (after): %v", err)
	}
	if got != 3 {
		t.Fatalf("distinct completed subjects = %d, want 3 after completing chinese", got)
	}
}
