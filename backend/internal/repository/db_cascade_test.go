package repository

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"
	"testing"

	"gorm.io/gorm"
)

func setupCascadeTestDB(t *testing.T) *gorm.DB {
	db := testutil.NewDB(t)
	// Enable foreign key constraints in SQLite for this connection.
	if err := db.Exec("PRAGMA foreign_keys=ON").Error; err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	return db
}

func TestDBCascadeAndNullableFields(t *testing.T) {
	db := setupCascadeTestDB(t)
	subjects := testutil.SeedSubjects(t, db)

	t.Run("SubjectRestrictConstraint", func(t *testing.T) {
		subj := subjects["math"]
		// Create a course that references the subject.
		c := &model.Course{
			Title:     "Restrict Math Course",
			SubjectID: subj.ID,
		}
		if err := db.Create(c).Error; err != nil {
			t.Fatalf("failed to create course: %v", err)
		}

		// Trying to delete the subject should fail due to RESTRICT.
		err := db.Delete(&subj).Error
		if err == nil {
			t.Fatal("expected FOREIGN KEY constraint failed error on deleting subject in use, got nil")
		}
	})

	t.Run("CourseCascadeDelete", func(t *testing.T) {
		subj := subjects["chinese"]
		c := &model.Course{
			Title:     "Cascade Course",
			SubjectID: subj.ID,
		}
		if err := db.Create(c).Error; err != nil {
			t.Fatalf("failed to create course: %v", err)
		}

		ch := &model.Chapter{
			Title:    "Chapter 1",
			CourseID: c.ID,
		}
		if err := db.Create(ch).Error; err != nil {
			t.Fatalf("failed to create chapter: %v", err)
		}

		ep := &model.Episode{
			Title:             "Episode 1",
			CourseID:          c.ID,
			VideoRelativePath: "/x.mp4",
		}
		if err := db.Create(ep).Error; err != nil {
			t.Fatalf("failed to create episode: %v", err)
		}

		// Delete course.
		if err := db.Delete(c).Error; err != nil {
			t.Fatalf("failed to delete course: %v", err)
		}

		// Verify that chapter and episode are cascaded away (deleted).
		var chCount int64
		db.Model(&model.Chapter{}).Where("id = ?", ch.ID).Count(&chCount)
		if chCount != 0 {
			t.Errorf("chapter %d was not deleted on course delete", ch.ID)
		}

		var epCount int64
		db.Model(&model.Episode{}).Where("id = ?", ep.ID).Count(&epCount)
		if epCount != 0 {
			t.Errorf("episode %d was not deleted on course delete", ep.ID)
		}
	})

	t.Run("ChapterSetNullOnDelete", func(t *testing.T) {
		subj := subjects["english"]
		c := &model.Course{
			Title:     "Set Null Course",
			SubjectID: subj.ID,
		}
		if err := db.Create(c).Error; err != nil {
			t.Fatalf("failed to create course: %v", err)
		}

		ch := &model.Chapter{
			Title:    "Chapter A",
			CourseID: c.ID,
		}
		if err := db.Create(ch).Error; err != nil {
			t.Fatalf("failed to create chapter: %v", err)
		}

		// Episode with ChapterID pointing to the chapter.
		ep := &model.Episode{
			Title:             "Episode A",
			CourseID:          c.ID,
			ChapterID:         &ch.ID,
			VideoRelativePath: "/y.mp4",
		}
		if err := db.Create(ep).Error; err != nil {
			t.Fatalf("failed to create episode: %v", err)
		}

		// Delete the chapter.
		if err := db.Delete(ch).Error; err != nil {
			t.Fatalf("failed to delete chapter: %v", err)
		}

		// Verify episode still exists, but its ChapterID is now NULL.
		var gotEp model.Episode
		if err := db.First(&gotEp, ep.ID).Error; err != nil {
			t.Fatalf("failed to find episode: %v", err)
		}
		if gotEp.ChapterID != nil {
			t.Errorf("expected episode %d's ChapterID to be nil after chapter delete, got %d", gotEp.ID, *gotEp.ChapterID)
		}
	})

	t.Run("ReadingSeriesSetNullOnDelete", func(t *testing.T) {
		subj := subjects["chinese"]
		series := &model.ReadingSeries{
			Title:     "My Series",
			SubjectID: subj.ID,
		}
		if err := db.Create(series).Error; err != nil {
			t.Fatalf("failed to create reading series: %v", err)
		}

		book := &model.ReadingBook{
			Title:            "My Book",
			SubjectID:        subj.ID,
			SeriesID:         &series.ID,
			FileRelativePath: "/b.pdf",
		}
		if err := db.Create(book).Error; err != nil {
			t.Fatalf("failed to create reading book: %v", err)
		}

		article := &model.ReadingArticle{
			Title:     "My Article",
			SubjectID: subj.ID,
			SeriesID:  &series.ID,
			SourceURL: "http://x.com",
		}
		if err := db.Create(article).Error; err != nil {
			t.Fatalf("failed to create reading article: %v", err)
		}

		// Delete reading series.
		if err := db.Delete(series).Error; err != nil {
			t.Fatalf("failed to delete reading series: %v", err)
		}

		// Verify book and article still exist, but SeriesID is NULL.
		var gotBook model.ReadingBook
		if err := db.First(&gotBook, book.ID).Error; err != nil {
			t.Fatalf("failed to find book: %v", err)
		}
		if gotBook.SeriesID != nil {
			t.Errorf("expected book %d SeriesID to be nil, got %d", gotBook.ID, *gotBook.SeriesID)
		}

		var gotArticle model.ReadingArticle
		if err := db.First(&gotArticle, article.ID).Error; err != nil {
			t.Fatalf("failed to find article: %v", err)
		}
		if gotArticle.SeriesID != nil {
			t.Errorf("expected article %d SeriesID to be nil, got %d", gotArticle.ID, *gotArticle.SeriesID)
		}
	})

	t.Run("UserCascadeDelete", func(t *testing.T) {
		// Create a user.
		u := &model.User{
			Nickname: "Test Student",
			PinHash:  "xxxx",
			Role:     "student",
		}
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("failed to create user: %v", err)
		}

		// Create a course/episode to map progress.
		subj := subjects["math"]
		c := &model.Course{Title: "Math Course", SubjectID: subj.ID}
		db.Create(c)
		ep := &model.Episode{Title: "Math Lesson 1", CourseID: c.ID, VideoRelativePath: "/math1.mp4"}
		db.Create(ep)

		// Create UserProgress.
		up := &model.UserProgress{
			UserID:   u.ID,
			EpisodeID: ep.ID,
		}
		if err := db.Create(up).Error; err != nil {
			t.Fatalf("failed to create user progress: %v", err)
		}

		// Create PointsLedger.
		ledger := &model.PointsLedger{
			UserID:       u.ID,
			ChangeAmount: 10,
			ReasonType:   "test",
		}
		if err := db.Create(ledger).Error; err != nil {
			t.Fatalf("failed to create points ledger: %v", err)
		}

		// Create UserBadge.
		b := &model.Badge{Code: "test_badge", Title: "Test Badge", Description: "desc"}
		db.Create(b)
		ub := &model.UserBadge{
			UserID:  u.ID,
			BadgeID: b.ID,
		}
		if err := db.Create(ub).Error; err != nil {
			t.Fatalf("failed to create user badge: %v", err)
		}

		// Create ReadingBookProgress.
		rb := &model.ReadingBook{Title: "Test Book", SubjectID: subj.ID, FileRelativePath: "/tb.pdf"}
		db.Create(rb)
		rbp := &model.ReadingBookProgress{
			UserID:   u.ID,
			BookID:   rb.ID,
			LastPage: 3,
		}
		if err := db.Create(rbp).Error; err != nil {
			t.Fatalf("failed to create reading book progress: %v", err)
		}

		// Delete user.
		if err := db.Delete(u).Error; err != nil {
			t.Fatalf("failed to delete user: %v", err)
		}

		// Verify cascaded items are deleted.
		var upCount int64
		db.Model(&model.UserProgress{}).Where("user_id = ?", u.ID).Count(&upCount)
		if upCount != 0 {
			t.Error("UserProgress was not cascaded away on user delete")
		}

		var ledgerCount int64
		db.Model(&model.PointsLedger{}).Where("user_id = ?", u.ID).Count(&ledgerCount)
		if ledgerCount != 0 {
			t.Error("PointsLedger was not cascaded away on user delete")
		}

		var ubCount int64
		db.Model(&model.UserBadge{}).Where("user_id = ?", u.ID).Count(&ubCount)
		if ubCount != 0 {
			t.Error("UserBadge was not cascaded away on user delete")
		}

		var rbpCount int64
		db.Model(&model.ReadingBookProgress{}).Where("user_id = ?", u.ID).Count(&rbpCount)
		if rbpCount != 0 {
			t.Error("ReadingBookProgress was not cascaded away on user delete")
		}
	})

	t.Run("NullableFieldsRoundTrip", func(t *testing.T) {
		subj := subjects["chinese"]
		c := &model.Course{Title: "Round Trip Course", SubjectID: subj.ID}
		db.Create(c)

		// 1. Episode with nil ChapterID.
		ep1 := &model.Episode{
			Title:             "Nil Chapter Episode",
			CourseID:          c.ID,
			ChapterID:         nil,
			VideoRelativePath: "/nil.mp4",
		}
		if err := db.Create(ep1).Error; err != nil {
			t.Fatalf("failed to create ep1: %v", err)
		}

		// Verify ep1 ChapterID is nil in DB.
		var gotEp1 model.Episode
		if err := db.First(&gotEp1, ep1.ID).Error; err != nil {
			t.Fatalf("failed to query ep1: %v", err)
		}
		if gotEp1.ChapterID != nil {
			t.Errorf("expected ChapterID to be nil, got %d", *gotEp1.ChapterID)
		}

		// 2. Episode with non-nil ChapterID.
		ch := &model.Chapter{Title: "Some Chapter", CourseID: c.ID}
		db.Create(ch)

		ep2 := &model.Episode{
			Title:             "Valued Chapter Episode",
			CourseID:          c.ID,
			ChapterID:         &ch.ID,
			VideoRelativePath: "/val.mp4",
		}
		if err := db.Create(ep2).Error; err != nil {
			t.Fatalf("failed to create ep2: %v", err)
		}

		// Verify ep2 ChapterID matches ch.ID in DB.
		var gotEp2 model.Episode
		if err := db.First(&gotEp2, ep2.ID).Error; err != nil {
			t.Fatalf("failed to query ep2: %v", err)
		}
		if gotEp2.ChapterID == nil {
			t.Fatal("expected ChapterID to be non-nil, got nil")
		}
		if *gotEp2.ChapterID != ch.ID {
			t.Errorf("expected ChapterID to be %d, got %d", ch.ID, *gotEp2.ChapterID)
		}
	})
}
