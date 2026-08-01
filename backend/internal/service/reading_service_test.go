package service

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
	"testing"

	"gorm.io/gorm"
)

func setupReadingServiceDB(t *testing.T) *gorm.DB {
	return testutil.NewDB(t)
}

func seedReadingFixtures(t *testing.T, db *gorm.DB) model.Subject {
	t.Helper()
	s := model.Subject{Key: "chinese", Label: "语文", SortOrder: 1}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	return s
}

// TestReadingBookCanAccessSeriesInheritance is the key access-model test: a
// student with series access (but NO direct book access) should still be able to
// access a book inside that series. This locks in the Course→Episode-style
// inheritance that was the core review fix.
func TestReadingBookCanAccessSeriesInheritance(t *testing.T) {
	db := setupReadingServiceDB(t)
	subj := seedReadingFixtures(t, db)
	seriesRepo := repository.NewReadingSeriesRepository(db)
	bookRepo := repository.NewReadingBookRepository(db)
	bookSvc := NewReadingBookService(bookRepo, NewStorageProviderResolver(repository.NewStorageSourceRepository(db)), seriesRepo)

	series := model.ReadingSeries{Title: "S", SubjectID: subj.ID}
	db.Create(&series)
	db.Create(&model.ReadingSeriesGrade{SeriesID: series.ID, Grade: model.GradeUniversal})
	book := model.ReadingBook{SeriesID: &series.ID, Title: "B", FileRelativePath: "/x.pdf", SubjectID: subj.ID}
	db.Create(&book)
	db.Create(&model.ReadingBookGrade{BookID: book.ID, Grade: model.GradeUniversal})

	const uid uint = 1

	// No access at all → denied.
	ok, err := bookSvc.CanAccess(uid, "student", book.ID)
	if err != nil {
		t.Fatalf("CanAccess (no grant): %v", err)
	}
	if ok {
		t.Fatal("CanAccess (no grant): expected false, got true")
	}

	// Grant series access → book accessible via inheritance.
	seriesRepo.GrantAccess(uid, series.ID)
	ok, err = bookSvc.CanAccess(uid, "student", book.ID)
	if err != nil {
		t.Fatalf("CanAccess (series grant): %v", err)
	}
	if !ok {
		t.Fatal("CanAccess (series grant): expected true (inherited), got false")
	}

	// Admin bypass.
	ok, _ = bookSvc.CanAccess(uid, "admin", book.ID)
	if !ok {
		t.Fatal("CanAccess (admin): expected true")
	}
}

// TestReadingBookCanAccessStandaloneNoInheritance verifies that a standalone
// book (SeriesID=0) does NOT inherit from any series — only direct access works.
func TestReadingBookCanAccessStandaloneNoInheritance(t *testing.T) {
	db := setupReadingServiceDB(t)
	subj := seedReadingFixtures(t, db)
	seriesRepo := repository.NewReadingSeriesRepository(db)
	bookRepo := repository.NewReadingBookRepository(db)
	bookSvc := NewReadingBookService(bookRepo, NewStorageProviderResolver(repository.NewStorageSourceRepository(db)), seriesRepo)

	book := model.ReadingBook{Title: "Standalone", FileRelativePath: "/s.pdf", SubjectID: subj.ID}
	db.Create(&book)
	db.Create(&model.ReadingBookGrade{BookID: book.ID, Grade: model.GradeUniversal})

	// No access → denied.
	ok, _ := bookSvc.CanAccess(1, "student", book.ID)
	if ok {
		t.Fatal("standalone with no access: expected false")
	}

	// Direct access → granted.
	bookRepo.GrantAccess(1, book.ID)
	ok, _ = bookSvc.CanAccess(1, "student", book.ID)
	if !ok {
		t.Fatal("standalone with direct access: expected true")
	}
}

// TestReadingArticleCanAccessSeriesInheritance mirrors the book inheritance
// test for articles.
func TestReadingArticleCanAccessSeriesInheritance(t *testing.T) {
	db := setupReadingServiceDB(t)
	subj := seedReadingFixtures(t, db)
	seriesRepo := repository.NewReadingSeriesRepository(db)
	articleRepo := repository.NewReadingArticleRepository(db)
	articleSvc := NewReadingArticleService(articleRepo, seriesRepo)

	series := model.ReadingSeries{Title: "S", SubjectID: subj.ID}
	db.Create(&series)
	db.Create(&model.ReadingSeriesGrade{SeriesID: series.ID, Grade: model.GradeUniversal})
	article := model.ReadingArticle{SeriesID: &series.ID, Title: "A", SourceURL: "https://example.com", SubjectID: subj.ID}
	db.Create(&article)
	db.Create(&model.ReadingArticleGrade{ArticleID: article.ID, Grade: model.GradeUniversal})

	const uid uint = 1

	// No access → denied.
	ok, _ := articleSvc.CanAccess(uid, "student", article.ID)
	if ok {
		t.Fatal("article with no access: expected false")
	}

	// Series access → inherited.
	seriesRepo.GrantAccess(uid, series.ID)
	ok, _ = articleSvc.CanAccess(uid, "student", article.ID)
	if !ok {
		t.Fatal("article with series access: expected true (inherited)")
	}
}

// TestGetReadingRoomDedup is the key dedup test. A student with:
//   - series access to series 1 (contains book B1)
//   - direct book access to B2 (inside series 2, which the student does NOT
//     have series access to)
//   - direct book access to B3 (standalone, series_id=0)
//
// Expected: Series=[1], Books=[B2, B3] (B1 is inside series 1, not duplicated).
// This was BUG-1 in the review — previously B2 was invisible because
// standaloneOnly=true filtered out series_id≠0 books.
func TestGetReadingRoomDedup(t *testing.T) {
	db := setupReadingServiceDB(t)
	subj := seedReadingFixtures(t, db)
	seriesRepo := repository.NewReadingSeriesRepository(db)
	bookRepo := repository.NewReadingBookRepository(db)
	articleRepo := repository.NewReadingArticleRepository(db)
	seriesSvc := NewReadingSeriesService(seriesRepo, bookRepo, articleRepo)

	// Two series.
	s1 := model.ReadingSeries{Title: "S1", SubjectID: subj.ID, SortOrder: 1}
	s2 := model.ReadingSeries{Title: "S2", SubjectID: subj.ID, SortOrder: 2}
	db.Create(&s1)
	db.Create(&s2)
	db.Create(&model.ReadingSeriesGrade{SeriesID: s1.ID, Grade: model.GradeUniversal})
	db.Create(&model.ReadingSeriesGrade{SeriesID: s2.ID, Grade: model.GradeUniversal})

	// B1 inside S1, B2 inside S2, B3 standalone.
	b1 := model.ReadingBook{SeriesID: &s1.ID, Title: "B1", FileRelativePath: "/1.pdf", SubjectID: subj.ID}
	b2 := model.ReadingBook{SeriesID: &s2.ID, Title: "B2", FileRelativePath: "/2.pdf", SubjectID: subj.ID}
	b3 := model.ReadingBook{Title: "B3", FileRelativePath: "/3.pdf", SubjectID: subj.ID}
	db.Create(&b1)
	db.Create(&b2)
	db.Create(&b3)
	db.Create(&model.ReadingBookGrade{BookID: b1.ID, Grade: model.GradeUniversal})
	db.Create(&model.ReadingBookGrade{BookID: b2.ID, Grade: model.GradeUniversal})
	db.Create(&model.ReadingBookGrade{BookID: b3.ID, Grade: model.GradeUniversal})

	const uid uint = 1
	// Grant: series 1 access (covers B1 via inheritance), direct B2 + B3 access.
	seriesRepo.GrantAccess(uid, s1.ID)
	bookRepo.GrantAccess(uid, b2.ID)
	bookRepo.GrantAccess(uid, b3.ID)

	view, err := seriesSvc.GetReadingRoom(uid, "student", "", 0)
	if err != nil {
		t.Fatalf("GetReadingRoom: %v", err)
	}

	// Series: only S1 (student has series access to it, not S2).
	if len(view.Series) != 1 || view.Series[0].ID != s1.ID {
		t.Fatalf("Series: got %d items, want 1 (S1)", len(view.Series))
	}

	// Books: B2 (series 2 not in visible set → shown standalone) + B3 (truly
	// standalone). B1 must NOT appear (it's inside S1 which is in the series
	// list — reachable via series detail, not duplicated here).
	if len(view.Books) != 2 {
		t.Fatalf("Books: got %d, want 2 (B2 + B3)", len(view.Books))
	}
	bookIDs := map[uint]bool{}
	for _, b := range view.Books {
		bookIDs[b.ID] = true
	}
	if !bookIDs[b2.ID] {
		t.Error("B2 should be in standalone Books (series not in visible set)")
	}
	if !bookIDs[b3.ID] {
		t.Error("B3 should be in standalone Books (truly standalone)")
	}
	if bookIDs[b1.ID] {
		t.Error("B1 should NOT be in standalone Books (covered by series S1)")
	}
}

// TestGetReadingRoomAdmin sees all series + all standalone books.
func TestGetReadingRoomAdmin(t *testing.T) {
	db := setupReadingServiceDB(t)
	subj := seedReadingFixtures(t, db)
	seriesRepo := repository.NewReadingSeriesRepository(db)
	bookRepo := repository.NewReadingBookRepository(db)
	articleRepo := repository.NewReadingArticleRepository(db)
	seriesSvc := NewReadingSeriesService(seriesRepo, bookRepo, articleRepo)

	s1 := model.ReadingSeries{Title: "S1", SubjectID: subj.ID}
	db.Create(&s1)
	db.Create(&model.ReadingSeriesGrade{SeriesID: s1.ID, Grade: model.GradeUniversal})
	b1 := model.ReadingBook{SeriesID: &s1.ID, Title: "B1", FileRelativePath: "/1.pdf", SubjectID: subj.ID}
	b2 := model.ReadingBook{Title: "B2", FileRelativePath: "/2.pdf", SubjectID: subj.ID}
	db.Create(&b1)
	db.Create(&b2)
	db.Create(&model.ReadingBookGrade{BookID: b1.ID, Grade: model.GradeUniversal})
	db.Create(&model.ReadingBookGrade{BookID: b2.ID, Grade: model.GradeUniversal})

	view, err := seriesSvc.GetReadingRoom(1, "admin", "", 0)
	if err != nil {
		t.Fatalf("GetReadingRoom (admin): %v", err)
	}
	if len(view.Series) != 1 {
		t.Fatalf("admin Series: got %d, want 1", len(view.Series))
	}
	// Admin sees only standalone (series_id=0) books — B2, not B1.
	if len(view.Books) != 1 || view.Books[0].ID != b2.ID {
		t.Fatalf("admin Books: got %d items, want 1 (B2 standalone only)", len(view.Books))
	}
}

// TestReadingSeriesCRUD covers the basic create/update/delete cycle with tag
// sync and grade validation — mirrors course_service_test.go.
func TestReadingSeriesCRUD(t *testing.T) {
	db := setupReadingServiceDB(t)
	subj := seedReadingFixtures(t, db)
	seriesRepo := repository.NewReadingSeriesRepository(db)
	bookRepo := repository.NewReadingBookRepository(db)
	articleRepo := repository.NewReadingArticleRepository(db)
	svc := NewReadingSeriesService(seriesRepo, bookRepo, articleRepo)

	// Create.
	created, err := svc.CreateSeries("Test Series", "desc", []model.Grade{model.Grade("3")}, subj.ID, "/cover.png", 0, nil)
	if err != nil {
		t.Fatalf("CreateSeries: %v", err)
	}
	if created.Title != "Test Series" {
		t.Fatalf("Title: got %q", created.Title)
	}
	if created.GradeDisplay() != "3年级" {
		t.Fatalf("Grade: got %q", created.GradeDisplay())
	}

	// Update.
	updated, err := svc.UpdateSeries(created.ID, "Updated", "new desc", []model.Grade{model.GradeUniversal}, subj.ID, "/cover2.png", 5, nil)
	if err != nil {
		t.Fatalf("UpdateSeries: %v", err)
	}
	if updated.Title != "Updated" || updated.SortOrder != 5 {
		t.Fatalf("UpdateSeries: title=%q order=%d", updated.Title, updated.SortOrder)
	}

	// Custom grade tag (grade 是开放 tag 体系,"考研" 合法且原样显示).
	custom, err := svc.CreateSeries("Custom Grade", "", []model.Grade{model.Grade("考研")}, subj.ID, "", 0, nil)
	if err != nil {
		t.Fatalf("custom grade tag '考研' should be accepted: %v", err)
	}
	if custom == nil || custom.GradeDisplay() != "考研" {
		t.Fatalf("custom grade '考研' should display as-is, got: %q", custom.GradeDisplay())
	}

	// Delete.
	if err := svc.DeleteSeries(created.ID); err != nil {
		t.Fatalf("DeleteSeries: %v", err)
	}
	got, _ := svc.GetSeriesByID(created.ID)
	if got != nil {
		t.Fatal("series should be deleted")
	}
}
