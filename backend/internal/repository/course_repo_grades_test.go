package repository

import (
	"studyquest/backend/internal/model"
	"testing"
)

// TestListDistinctGrades 覆盖 CourseRepository.ListDistinctGrades:返回所有课程
// 用过的 distinct grade key,用于填充 admin/Flutter 的 grade 过滤栏(预设 + 自定义)。
// 验证:多课程共用同一 grade 时去重;空 DB 返回空切片;结果按字母序排。
func TestListDistinctGrades(t *testing.T) {
	db := setupTestDB(t)
	courseRepo := NewCourseRepository(db)

	// 一个 subject 让 FK 满足。
	subj := &model.Subject{Key: "math", Label: "数学", SortOrder: 1, Category: string(model.SubjectCategoryAcademic)}
	if err := db.Create(subj).Error; err != nil {
		t.Fatalf("create subject: %v", err)
	}

	// 课程 1: grades = ["primary", "junior"]
	// 课程 2: grades = ["primary", "custom_tag"]  (custom_tag 是 admin 自定义)
	// 课程 3: grades = []  (无 grade)
	c1 := &model.Course{Title: "c1", SubjectID: subj.ID, ContentType: model.ContentLearning}
	c2 := &model.Course{Title: "c2", SubjectID: subj.ID, ContentType: model.ContentLearning}
	c3 := &model.Course{Title: "c3", SubjectID: subj.ID, ContentType: model.ContentLearning}
	for _, c := range []*model.Course{c1, c2, c3} {
		if err := db.Create(c).Error; err != nil {
			t.Fatalf("create course: %v", err)
		}
	}
	if err := courseRepo.SetGrades(c1.ID, []model.Grade{model.GradePrimary, model.GradeJunior}); err != nil {
		t.Fatalf("set grades c1: %v", err)
	}
	if err := courseRepo.SetGrades(c2.ID, []model.Grade{model.GradePrimary, model.Grade("custom_tag")}); err != nil {
		t.Fatalf("set grades c2: %v", err)
	}
	// c3 故意不设 grade(SetGrades 传空会清空,这里直接不调)。

	// Act.
	got, err := courseRepo.ListDistinctGrades()
	if err != nil {
		t.Fatalf("ListDistinctGrades: %v", err)
	}

	// Assert: ["custom_tag", "junior", "primary"](字母序)。primary 跨两个课程只
	// 出现一次(去重)。
	want := []model.Grade{model.Grade("custom_tag"), model.GradeJunior, model.GradePrimary}
	if len(got) != len(want) {
		t.Fatalf("distinct grades count = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i, g := range got {
		if g != want[i] {
			t.Errorf("distinct grades[%d] = %q, want %q (full: %v)", i, g, want[i], got)
		}
	}
}

// TestListDistinctGrades_EmptyDB 验证空 DB 不崩,返回空切片。
func TestListDistinctGrades_EmptyDB(t *testing.T) {
	db := setupTestDB(t)
	courseRepo := NewCourseRepository(db)

	got, err := courseRepo.ListDistinctGrades()
	if err != nil {
		t.Fatalf("ListDistinctGrades on empty DB: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice on empty DB, got %v", got)
	}
}
