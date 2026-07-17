package service

import (
	"errors"
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"

	"gorm.io/gorm"
)

// newEpisodeTxSvc builds a transactional EpisodeService (NewEpisodeServiceWithDB)
// on a fresh in-memory DB with FK enforcement on, mirroring the production
// wiring in main.go. The non-transactional NewEpisodeService constructor does
// not wire *gorm.DB / ChapterRepository, so BulkMoveEpisodes can't run on it.
func newEpisodeTxSvc(t *testing.T) (*gorm.DB, EpisodeService, repository.ChapterRepository) {
	t.Helper()
	db := testutil.NewDB(t)
	// Match production: FK pragma on so any declared CASCADE/RESTRICT fires.
	db.Exec("PRAGMA foreign_keys=ON")
	epRepo := repository.NewEpisodeRepository(db)
	chRepo := repository.NewChapterRepository(db)
	svc := NewEpisodeServiceWithDB(db, epRepo, chRepo, NewStorageProviderResolver(repository.NewStorageSourceRepository(db)))
	return db, svc, chRepo
}

// TestBulkMoveEpisodesCrossCourseRefused: moving an episode into a chapter
// that belongs to a DIFFERENT course must be refused with
// ErrEpisodeMoveCrossCourse (and must not mutate either row).
func TestBulkMoveEpisodesCrossCourseRefused(t *testing.T) {
	db, svc, _ := newEpisodeTxSvc(t)
	subjects := testutil.SeedSubjects(t, db)
	courseRepo := repository.NewCourseRepository(db)

	// Course A + its chapter.
	courseA := &model.Course{Title: "A", SubjectID: subjects["math"].ID}
	if err := courseRepo.Create(courseA); err != nil {
		t.Fatalf("create course A: %v", err)
	}
	chA := &model.Chapter{Title: "Ch A", CourseID: courseA.ID}
	if err := db.Create(chA).Error; err != nil {
		t.Fatalf("create chapter A: %v", err)
	}
	epA := &model.Episode{Title: "Ep A", CourseID: courseA.ID, VideoRelativePath: "/a.mp4", SortOrder: 1}
	if err := db.Create(epA).Error; err != nil {
		t.Fatalf("create ep A: %v", err)
	}

	// Course B + its chapter (the cross-course destination).
	courseB := &model.Course{Title: "B", SubjectID: subjects["english"].ID}
	if err := courseRepo.Create(courseB); err != nil {
		t.Fatalf("create course B: %v", err)
	}
	chB := &model.Chapter{Title: "Ch B", CourseID: courseB.ID}
	if err := db.Create(chB).Error; err != nil {
		t.Fatalf("create chapter B: %v", err)
	}

	err := svc.BulkMoveEpisodes([]uint{epA.ID}, chB.ID)
	if !errors.Is(err, ErrEpisodeMoveCrossCourse) {
		t.Fatalf("expected ErrEpisodeMoveCrossCourse, got %v", err)
	}

	// epA must be unchanged (still in course A, no chapter).
	var got model.Episode
	if err := db.First(&got, epA.ID).Error; err != nil {
		t.Fatalf("reload ep A: %v", err)
	}
	if got.CourseID != courseA.ID {
		t.Errorf("cross-course move should not mutate CourseID: got %d", got.CourseID)
	}
	if got.ChapterID != nil {
		t.Errorf("cross-course move should not set ChapterID: got %v", *got.ChapterID)
	}
}

// TestBulkMoveEpisodesAppendsSortOrder: a valid same-course move appends the
// moved episodes to the END of the destination chapter's existing ordering
// (max+1, max+2, ...), so it never collides with an existing sort_order.
func TestBulkMoveEpisodesAppendsSortOrder(t *testing.T) {
	db, svc, _ := newEpisodeTxSvc(t)
	subjects := testutil.SeedSubjects(t, db)
	courseRepo := repository.NewCourseRepository(db)

	course := &model.Course{Title: "C", SubjectID: subjects["math"].ID}
	if err := courseRepo.Create(course); err != nil {
		t.Fatalf("create course: %v", err)
	}
	ch := &model.Chapter{Title: "Ch", CourseID: course.ID}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create chapter: %v", err)
	}
	// Two episodes already in the chapter at sort_order 5 and 7.
	existing := []model.Episode{
		{Title: "Existing1", CourseID: course.ID, ChapterID: &ch.ID, VideoRelativePath: "/e1.mp4", SortOrder: 5},
		{Title: "Existing2", CourseID: course.ID, ChapterID: &ch.ID, VideoRelativePath: "/e2.mp4", SortOrder: 7},
	}
	for i := range existing {
		if err := db.Create(&existing[i]).Error; err != nil {
			t.Fatalf("create existing ep: %v", err)
		}
	}
	// Two unassigned episodes to move in (array order matters: m1 then m2).
	m1 := &model.Episode{Title: "Move1", CourseID: course.ID, VideoRelativePath: "/m1.mp4", SortOrder: 1}
	m2 := &model.Episode{Title: "Move2", CourseID: course.ID, VideoRelativePath: "/m2.mp4", SortOrder: 2}
	db.Create(m1)
	db.Create(m2)

	if err := svc.BulkMoveEpisodes([]uint{m1.ID, m2.ID}, ch.ID); err != nil {
		t.Fatalf("BulkMoveEpisodes: %v", err)
	}

	// Moved episodes should be at max(5,7)+1=8 and 9, in array order.
	var gotM1, gotM2 model.Episode
	db.First(&gotM1, m1.ID)
	db.First(&gotM2, m2.ID)
	if gotM1.SortOrder != 8 {
		t.Errorf("m1 sort_order: got %d, want 8 (max+1)", gotM1.SortOrder)
	}
	if gotM2.SortOrder != 9 {
		t.Errorf("m2 sort_order: got %d, want 9 (max+2)", gotM2.SortOrder)
	}
	if gotM1.ChapterID == nil || *gotM1.ChapterID != ch.ID {
		t.Errorf("m1 chapter_id: got %v, want %d", gotM1.ChapterID, ch.ID)
	}
}

// TestReorderEpisodesCrossCourseRefused: the batch reorder must reject a set
// whose episodes span more than one course, since sort_order is only
// meaningful within one course.
func TestReorderEpisodesCrossCourseRefused(t *testing.T) {
	db, svc, _ := newEpisodeTxSvc(t)
	subjects := testutil.SeedSubjects(t, db)
	courseRepo := repository.NewCourseRepository(db)

	courseA := &model.Course{Title: "A", SubjectID: subjects["math"].ID}
	courseRepo.Create(courseA)
	courseB := &model.Course{Title: "B", SubjectID: subjects["english"].ID}
	courseRepo.Create(courseB)

	epA := &model.Episode{Title: "A1", CourseID: courseA.ID, VideoRelativePath: "/a.mp4", SortOrder: 1}
	epB := &model.Episode{Title: "B1", CourseID: courseB.ID, VideoRelativePath: "/b.mp4", SortOrder: 1}
	db.Create(epA)
	db.Create(epB)

	err := svc.ReorderEpisodes([]uint{epA.ID, epB.ID})
	if !errors.Is(err, ErrEpisodesDifferentCourses) {
		t.Fatalf("expected ErrEpisodesDifferentCourses, got %v", err)
	}
}

// TestReorderEpisodesMissingLeadingIDOK: a missing leading id in the reorder
// payload (e.g. a client snapshot referencing a just-deleted episode) must NOT
// skew the same-course check — the reference course is taken from the first
// LOADED episode, not the first array slot.
func TestReorderEpisodesMissingLeadingIDOK(t *testing.T) {
	db, svc, _ := newEpisodeTxSvc(t)
	subjects := testutil.SeedSubjects(t, db)
	courseRepo := repository.NewCourseRepository(db)

	course := &model.Course{Title: "C", SubjectID: subjects["math"].ID}
	courseRepo.Create(course)
	ep1 := &model.Episode{Title: "E1", CourseID: course.ID, VideoRelativePath: "/1.mp4", SortOrder: 1}
	ep2 := &model.Episode{Title: "E2", CourseID: course.ID, VideoRelativePath: "/2.mp4", SortOrder: 2}
	db.Create(ep1)
	db.Create(ep2)

	// 99999 doesn't exist; it leads the array. The reorder must still succeed
	// because both LOADED episodes share a course. The existing sort_order
	// logic assigns by ARRAY index (i+1), so ep2 lands at 2 (index 1) — a
	// harmless gap at 1. What matters here is that the missing id doesn't
	// abort the call and the relative order ep2→ep1 is preserved.
	err := svc.ReorderEpisodes([]uint{99999, ep2.ID, ep1.ID})
	if err != nil {
		t.Fatalf("reorder with leading missing id: %v", err)
	}
	var got2, got1 model.Episode
	db.First(&got2, ep2.ID)
	db.First(&got1, ep1.ID)
	if got2.SortOrder >= got1.SortOrder {
		t.Errorf("relative order broken: ep2 sort_order=%d should precede ep1 sort_order=%d", got2.SortOrder, got1.SortOrder)
	}
}
