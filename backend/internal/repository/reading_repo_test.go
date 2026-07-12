package repository

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"
	"testing"

	"gorm.io/gorm"
)

func setupReadingTestDB(t *testing.T) *gorm.DB {
	return testutil.NewDB(t)
}

func seedReadingSubject(t *testing.T, db *gorm.DB) model.Subject {
	t.Helper()
	s := model.Subject{Key: "chinese", Label: "语文", SortOrder: 1}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("seed subject: %v", err)
	}
	return s
}

// TestReadingBookProgressUpsertAtomic verifies the page-memory upsert: two
// sequential writes should leave the last value (not accumulate), and the row
// should exist after the first write. Mirrors progress_atomic_test.go's
// pattern but for the simpler last-writer-wins overwrite (no accumulation).
func TestReadingBookProgressUpsertAtomic(t *testing.T) {
	db := setupReadingTestDB(t)
	repo := NewReadingBookRepository(db)
	const uid, bookID uint = 1, 2

	// First write — page 5.
	prog, err := repo.UpsertProgress(uid, bookID, 5)
	if err != nil {
		t.Fatalf("UpsertProgress(5): %v", err)
	}
	if prog.LastPage != 5 {
		t.Fatalf("after first upsert: LastPage=%d, want 5", prog.LastPage)
	}

	// Second write — page 12. Should overwrite, not add.
	prog, err = repo.UpsertProgress(uid, bookID, 12)
	if err != nil {
		t.Fatalf("UpsertProgress(12): %v", err)
	}
	if prog.LastPage != 12 {
		t.Fatalf("after second upsert: LastPage=%d, want 12", prog.LastPage)
	}

	// Re-read to confirm persistence.
	got, err := repo.GetProgress(uid, bookID)
	if err != nil || got == nil {
		t.Fatalf("GetProgress: %v %v", got, err)
	}
	if got.LastPage != 12 {
		t.Fatalf("GetProgress: LastPage=%d, want 12", got.LastPage)
	}
}

// TestReadingBookProgressNegativeClamp verifies that negative page values are
// clamped to 0 (the repo's input guard).
func TestReadingBookProgressNegativeClamp(t *testing.T) {
	db := setupReadingTestDB(t)
	repo := NewReadingBookRepository(db)

	prog, err := repo.UpsertProgress(1, 2, -5)
	if err != nil {
		t.Fatalf("UpsertProgress(-5): %v", err)
	}
	if prog.LastPage != 0 {
		t.Fatalf("negative clamp: LastPage=%d, want 0", prog.LastPage)
	}
}

// TestReadingBookProgressGetMissing verifies GetProgress returns (nil, nil) for
// a (user, book) pair with no progress row — the caller checks == nil, not error.
func TestReadingBookProgressGetMissing(t *testing.T) {
	db := setupReadingTestDB(t)
	repo := NewReadingBookRepository(db)

	prog, err := repo.GetProgress(999, 999)
	if err != nil {
		t.Fatalf("GetProgress on missing: unexpected error %v", err)
	}
	if prog != nil {
		t.Fatalf("GetProgress on missing: expected nil, got %+v", prog)
	}
}

// TestReadingSeriesAccessThreeStates verifies the access-filter three-state
// pattern that mirrors courseRepo.List: nil = no filter (admin), empty slice =
// deny all, non-empty = filter to those IDs.
func TestReadingSeriesAccessThreeStates(t *testing.T) {
	db := setupReadingTestDB(t)
	subj := seedReadingSubject(t, db)
	repo := NewReadingSeriesRepository(db)

	// Create 3 series.
	for i := 0; i < 3; i++ {
		s := &model.ReadingSeries{Title: "S", SubjectID: subj.ID, SortOrder: i}
		_ = db.Create(s).Error
		_ = db.Create(&model.ReadingSeriesGrade{SeriesID: s.ID, Grade: model.GradeUniversal}).Error
	}

	// Grant access to series 1 and 3 for user 1.
	_ = repo.GrantAccess(1, 1)
	_ = repo.GrantAccess(1, 3)

	// nil (admin) → all 3.
	all, err := repo.List("", 0, nil)
	if err != nil {
		t.Fatalf("List(nil): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List(nil): got %d, want 3", len(all))
	}

	// Empty slice (student with zero grants) → 0.
	none, err := repo.List("", 0, []uint{})
	if err != nil {
		t.Fatalf("List(empty): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("List(empty): got %d, want 0", len(none))
	}

	// [1, 3] (student with grants) → 2.
	some, err := repo.List("", 0, []uint{1, 3})
	if err != nil {
		t.Fatalf("List([1,3]): %v", err)
	}
	if len(some) != 2 {
		t.Fatalf("List([1,3]): got %d, want 2", len(some))
	}
}

// TestReadingSeriesDeleteDetachesChildren verifies that deleting a series
// detaches its child books/articles (SeriesID → 0) rather than destroying them,
// so content survives the series being dissolved.
func TestReadingSeriesDeleteDetachesChildren(t *testing.T) {
	db := setupReadingTestDB(t)
	subj := seedReadingSubject(t, db)
	seriesRepo := NewReadingSeriesRepository(db)
	bookRepo := NewReadingBookRepository(db)

	series := model.ReadingSeries{Title: "Series", SubjectID: subj.ID}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ReadingSeriesGrade{SeriesID: series.ID, Grade: model.GradeUniversal}).Error; err != nil {
		t.Fatal(err)
	}
	book := model.ReadingBook{SeriesID: series.ID, Title: "Book", FileRelativePath: "/x.pdf", SubjectID: subj.ID}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ReadingBookGrade{BookID: book.ID, Grade: model.GradeUniversal}).Error; err != nil {
		t.Fatal(err)
	}

	if err := seriesRepo.Delete(series.ID); err != nil {
		t.Fatalf("Delete series: %v", err)
	}

	// Series is gone.
	got, _ := seriesRepo.FindByID(series.ID)
	if got != nil {
		t.Fatalf("series should be deleted, got %+v", got)
	}
	// Book survives but is now standalone (SeriesID=0).
	reloaded, _ := bookRepo.FindByID(book.ID)
	if reloaded == nil {
		t.Fatal("book should survive series deletion")
	}
	if reloaded.SeriesID != 0 {
		t.Fatalf("book SeriesID after series delete: got %d, want 0", reloaded.SeriesID)
	}
}

// TestReadingBookFindByBasenameAndSize verifies the disaster-recovery lookup
// (basename + file size) used by GetStreamURL when the primary path lookup
// fails. The stored path's basename plus the byte size together identify a book
// even after a storage move renames its directory.
func TestReadingBookFindByBasenameAndSize(t *testing.T) {
	db := setupReadingTestDB(t)
	subj := seedReadingSubject(t, db)
	repo := NewReadingBookRepository(db)

	size := int64(2048)
	book := model.ReadingBook{
		Title: "B", FileRelativePath: "/some/path/lesson01.pdf", FileSize: &size,
		SubjectID: subj.ID,
	}
	if err := db.Create(&book).Error; err != nil {
		t.Fatal(err)
	}

	// Found by basename + size.
	got, err := repo.FindByBasenameAndSize("lesson01.pdf", size)
	if err != nil || got == nil {
		t.Fatalf("FindByBasenameAndSize: %v %v", got, err)
	}
	if got.ID != book.ID {
		t.Fatalf("FindByBasenameAndSize: got ID %d, want %d", got.ID, book.ID)
	}

	// Wrong size → (nil, nil).
	missing, err := repo.FindByBasenameAndSize("lesson01.pdf", 9999)
	if err != nil || missing != nil {
		t.Fatalf("FindByBasenameAndSize(wrong size): expected (nil,nil), got (%+v, %v)", missing, err)
	}

	// Unknown basename → (nil, nil).
	missing, err = repo.FindByBasenameAndSize("nonexistent.pdf", size)
	if err != nil || missing != nil {
		t.Fatalf("FindByBasenameAndSize(missing): expected (nil,nil), got (%+v, %v)", missing, err)
	}
}

// TestReadingAccessBatchLists verifies the batch access-list loader used by
// ListUsers to render per-user access counts without N+1.
func TestReadingAccessBatchLists(t *testing.T) {
	db := setupReadingTestDB(t)
	subj := seedReadingSubject(t, db)
	seriesRepo := NewReadingSeriesRepository(db)
	bookRepo := NewReadingBookRepository(db)

	// Create series + books.
	s1 := model.ReadingSeries{Title: "S1", SubjectID: subj.ID}
	s2 := model.ReadingSeries{Title: "S2", SubjectID: subj.ID}
	db.Create(&s1)
	db.Create(&s2)
	db.Create(&model.ReadingSeriesGrade{SeriesID: s1.ID, Grade: model.GradeUniversal})
	db.Create(&model.ReadingSeriesGrade{SeriesID: s2.ID, Grade: model.GradeUniversal})
	b1 := model.ReadingBook{Title: "B1", FileRelativePath: "/1.pdf", SubjectID: subj.ID}
	b2 := model.ReadingBook{Title: "B2", FileRelativePath: "/2.pdf", SubjectID: subj.ID}
	db.Create(&b1)
	db.Create(&b2)
	db.Create(&model.ReadingBookGrade{BookID: b1.ID, Grade: model.GradeUniversal})
	db.Create(&model.ReadingBookGrade{BookID: b2.ID, Grade: model.GradeUniversal})

	// User 1: series 1 + book 2. User 2: series 2 only.
	seriesRepo.GrantAccess(1, s1.ID)
	bookRepo.GrantAccess(1, b2.ID)
	seriesRepo.GrantAccess(2, s2.ID)

	seriesBatch, err := seriesRepo.BatchAccessLists()
	if err != nil {
		t.Fatalf("series BatchAccessLists: %v", err)
	}
	if len(seriesBatch[1]) != 1 || seriesBatch[1][0] != s1.ID {
		t.Fatalf("user 1 series access: got %v, want [%d]", seriesBatch[1], s1.ID)
	}
	if len(seriesBatch[2]) != 1 || seriesBatch[2][0] != s2.ID {
		t.Fatalf("user 2 series access: got %v, want [%d]", seriesBatch[2], s2.ID)
	}

	bookBatch, err := bookRepo.BatchAccessLists()
	if err != nil {
		t.Fatalf("book BatchAccessLists: %v", err)
	}
	if len(bookBatch[1]) != 1 || bookBatch[1][0] != b2.ID {
		t.Fatalf("user 1 book access: got %v, want [%d]", bookBatch[1], b2.ID)
	}
	if _, ok := bookBatch[2]; ok {
		t.Fatalf("user 2 should have no book access, got %v", bookBatch[2])
	}
}
