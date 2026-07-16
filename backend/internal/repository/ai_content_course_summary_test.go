package repository

import (
	"testing"

	"studyquest/backend/internal/model"
)

// ai_content_course_summary_test.go: Phase D 课程级总结 repo 单测。覆盖:
//   - GetCourseSummary 无记录返回 nil;
//   - UpsertCourseSummary 插入;
//   - 第二次 Upsert(同 course_id)替换(不是新增);
//   - 不同 course_id 互不干扰(course-unique 隔离)。
//
// 仿 ai_content_advice_test.go 的 TestStudyAdvice_GetUpsert / TestStudyAdvice_ScopeIsolation
// 范式,但 course-unique(无 user 维度,unique on course_id)。

// TestCourseSummary_GetUpsert 验证课程总结的唯一性 + 替换语义:
//   - GetCourseSummary 在无记录时返回 nil;
//   - UpsertCourseSummary 插入新行;
//   - 第二次 Upsert(同 course_id)替换旧行(不是新增)。
func TestCourseSummary_GetUpsert(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)

	const courseID = uint(42)
	// 无记录 → nil。
	got, err := repo.GetCourseSummary(courseID)
	if err != nil {
		t.Fatalf("GetCourseSummary (empty): %v", err)
	}
	if got != nil {
		t.Fatalf("GetCourseSummary on empty returned non-nil: %+v", got)
	}

	// 第一次插入。
	first := &model.AICourseSummary{
		CourseID: courseID, SummaryText: "第一版课程总结", ModelUsed: "m1",
	}
	if err := repo.UpsertCourseSummary(first); err != nil {
		t.Fatalf("UpsertCourseSummary #1: %v", err)
	}
	got, err = repo.GetCourseSummary(courseID)
	if err != nil {
		t.Fatalf("GetCourseSummary (after #1): %v", err)
	}
	if got == nil || got.SummaryText != "第一版课程总结" {
		t.Fatalf("after upsert #1, want 第一版课程总结; got %+v", got)
	}

	// 第二次 Upsert:替换(不是新增第二条)。
	second := &model.AICourseSummary{
		CourseID: courseID, SummaryText: "第二版课程总结", ModelUsed: "m2",
	}
	if err := repo.UpsertCourseSummary(second); err != nil {
		t.Fatalf("UpsertCourseSummary #2: %v", err)
	}
	got, err = repo.GetCourseSummary(courseID)
	if err != nil {
		t.Fatalf("GetCourseSummary (after #2): %v", err)
	}
	if got == nil || got.SummaryText != "第二版课程总结" {
		t.Fatalf("after upsert #2, want replaced 第二版课程总结; got %+v", got)
	}
	// 确认仍只有一条行(没有重复 insert)。
	var count int64
	db.Model(&model.AICourseSummary{}).Where("course_id = ?", courseID).Count(&count)
	if count != 1 {
		t.Errorf("after 2 upserts, row count = %d; want 1 (replace not insert)", count)
	}
}

// TestCourseSummary_CourseIsolation 验证不同 course_id 的课程总结互不干扰。
func TestCourseSummary_CourseIsolation(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)

	if err := repo.UpsertCourseSummary(&model.AICourseSummary{
		CourseID: 100, SummaryText: "课程 100 的总结",
	}); err != nil {
		t.Fatalf("UpsertCourseSummary 100: %v", err)
	}
	if err := repo.UpsertCourseSummary(&model.AICourseSummary{
		CourseID: 200, SummaryText: "课程 200 的总结",
	}); err != nil {
		t.Fatalf("UpsertCourseSummary 200: %v", err)
	}

	c100, _ := repo.GetCourseSummary(100)
	if c100 == nil || c100.SummaryText != "课程 100 的总结" {
		t.Errorf("course 100 summary = %+v; want 课程 100 的总结", c100)
	}
	c200, _ := repo.GetCourseSummary(200)
	if c200 == nil || c200.SummaryText != "课程 200 的总结" {
		t.Errorf("course 200 summary = %+v; want 课程 200 的总结", c200)
	}
}
