package repository

import (
	"studyquest/backend/internal/model"
	"testing"
)

// These tests cover the tag course-count helpers used by the delete-confirm
// prompt: CountCoursesByTag (single-tag blast radius) and BatchCourseCountsByTag
// (the N+1-free list variant). The join table is course_tags (many2many on
// Course.Tags), so creating a Course with Tags populates the join rows.

// TestCountCoursesByTag verifies the single-tag count across used/unused tags.
func TestCountCoursesByTag(t *testing.T) {
	db := setupTestDB(t)
	tagRepo := NewTagRepository(db)

	tagA := &model.Tag{Key: "a", Label: "A", Color: "#fff"}
	tagB := &model.Tag{Key: "b", Label: "B", Color: "#fff"}
	if err := tagRepo.Create(tagA); err != nil {
		t.Fatal(err)
	}
	if err := tagRepo.Create(tagB); err != nil {
		t.Fatal(err)
	}

	// Create 3 courses using tagA (via the many2many join), 0 using tagB.
	for i := 0; i < 3; i++ {
		if err := db.Create(&model.Course{Title: "c", SubjectID: 1, Grade: "g1", Tags: []model.Tag{*tagA}}).Error; err != nil {
			t.Fatalf("create course %d: %v", i, err)
		}
	}

	if got, err := tagRepo.CountCoursesByTag(tagA.ID); err != nil || got != 3 {
		t.Fatalf("CountCoursesByTag(A): got %d err %v, want 3", got, err)
	}
	if got, err := tagRepo.CountCoursesByTag(tagB.ID); err != nil || got != 0 {
		t.Fatalf("CountCoursesByTag(B): got %d err %v, want 0", got, err)
	}
}

// TestBatchCourseCountsByTag verifies the grouped map variant returns counts
// for every tag that has ≥1 course, and omits (treats as 0) unused tags.
func TestBatchCourseCountsByTag(t *testing.T) {
	db := setupTestDB(t)
	tagRepo := NewTagRepository(db)

	tagA := &model.Tag{Key: "a", Label: "A", Color: "#fff"}
	tagB := &model.Tag{Key: "b", Label: "B", Color: "#fff"}
	tagC := &model.Tag{Key: "c", Label: "C", Color: "#fff"} // unused
	for _, tg := range []*model.Tag{tagA, tagB, tagC} {
		if err := tagRepo.Create(tg); err != nil {
			t.Fatal(err)
		}
	}

	// 2 courses with A, 1 with B, none with C.
	for i := 0; i < 2; i++ {
		if err := db.Create(&model.Course{Title: "c", SubjectID: 1, Grade: "g1", Tags: []model.Tag{*tagA}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.Course{Title: "c", SubjectID: 1, Grade: "g1", Tags: []model.Tag{*tagB}}).Error; err != nil {
		t.Fatal(err)
	}

	counts, err := tagRepo.BatchCourseCountsByTag()
	if err != nil {
		t.Fatalf("BatchCourseCountsByTag: %v", err)
	}
	if counts[tagA.ID] != 2 {
		t.Errorf("tagA count = %d, want 2", counts[tagA.ID])
	}
	if counts[tagB.ID] != 1 {
		t.Errorf("tagB count = %d, want 1", counts[tagB.ID])
	}
	if _, present := counts[tagC.ID]; present {
		t.Errorf("unused tagC should be absent from the map, got %d", counts[tagC.ID])
	}
}
