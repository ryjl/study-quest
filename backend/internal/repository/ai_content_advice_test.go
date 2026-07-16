package repository

import (
	"testing"

	"studyquest/backend/internal/model"
)

// TestGetCourseMasteries 验证跨课程聚合:WHERE user_id AND course_id 取出该学生在
// 该课程下所有 (episode, chunk) 的 mastery 行。两节课的 memory 都应返回。
func TestGetCourseMasteries(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)

	const (
		userID   = uint(1)
		userID2  = uint(2) // 另一个学生,验证不串用户
		courseID = uint(100)
	)
	// 两个 episode 同属 courseID,chunk_id 不同。
	seeds := []model.KnowledgeMemory{
		{UserID: userID, EpisodeID: 10, CourseID: courseID, ChunkID: 1001, Mastery: 0.2},
		{UserID: userID, EpisodeID: 11, CourseID: courseID, ChunkID: 1002, Mastery: 0.8},
		// 另一课程下的同用户 memory:不该返回。
		{UserID: userID, EpisodeID: 12, CourseID: 200, ChunkID: 1003, Mastery: 0.1},
		// 另一用户在同课程下的 memory:不该返回。
		{UserID: userID2, EpisodeID: 10, CourseID: courseID, ChunkID: 1004, Mastery: 0.5},
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed memory %d: %v", i, err)
		}
	}

	got, err := repo.GetCourseMasteries(userID, courseID)
	if err != nil {
		t.Fatalf("GetCourseMasteries: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows; want 2 (only this user + this course)", len(got))
	}
	// 两行的 chunk_id 应是 1001 / 1002。
	chunkIDs := map[uint]bool{}
	for _, r := range got {
		if r.UserID != userID || r.CourseID != courseID {
			t.Errorf("row leaked scope: %+v", r)
		}
		chunkIDs[r.ChunkID] = true
	}
	if !chunkIDs[1001] || !chunkIDs[1002] {
		t.Errorf("missing expected chunk ids; got %v", chunkIDs)
	}
}

// TestGetSubjectMasteries 验证科目级聚合:JOIN courses 取出该 subject 下所有课程的
// mastery。需要先 seed course(带 subject_id)+ memory,验证跨课程但同 subject 的行都返回。
func TestGetSubjectMasteries(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)

	const (
		userID    = uint(1)
		subjectID = uint(7) // 如"数学"
	)
	// 两个课程同属 subjectID。
	courses := []model.Course{
		{ID: 100, Title: "分数", SubjectID: subjectID},
		{ID: 101, Title: "小数", SubjectID: subjectID},
		{ID: 200, Title: "英语阅读", SubjectID: 9}, // 另一科目,不该返回
	}
	for i := range courses {
		// 直接 Create 会触发 GORM 关联处理,这里用 map 避免触发 Subject 关联插入。
		if err := db.Table("courses").Create(&courses[i]).Error; err != nil {
			t.Fatalf("seed course %d: %v", i, err)
		}
	}
	memRows := []model.KnowledgeMemory{
		{UserID: userID, EpisodeID: 10, CourseID: 100, ChunkID: 1, Mastery: 0.2},
		{UserID: userID, EpisodeID: 11, CourseID: 101, ChunkID: 2, Mastery: 0.5},
		// course 200 是另一 subject:不该返回。
		{UserID: userID, EpisodeID: 12, CourseID: 200, ChunkID: 3, Mastery: 0.9},
		// 另一用户在同 subject:不该返回。
		{UserID: 2, EpisodeID: 10, CourseID: 100, ChunkID: 4, Mastery: 0.1},
	}
	for i := range memRows {
		if err := db.Create(&memRows[i]).Error; err != nil {
			t.Fatalf("seed memory %d: %v", i, err)
		}
	}

	got, err := repo.GetSubjectMasteries(userID, subjectID)
	if err != nil {
		t.Fatalf("GetSubjectMasteries: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows; want 2 (only this user + this subject)", len(got))
	}
	chunkIDs := map[uint]bool{}
	for _, r := range got {
		chunkIDs[r.ChunkID] = true
	}
	if !chunkIDs[1] || !chunkIDs[2] {
		t.Errorf("missing expected chunk ids; got %v", chunkIDs)
	}
}

// TestStudyAdvice_GetUpsert 验证 advice 的唯一性 + 替换语义:
//   - GetAdvice 在无记录时返回 nil;
//   - UpsertAdvice 插入新行;
//   - 第二次 Upsert(同 user/scope/scope_id)替换旧行(不是新增)。
func TestStudyAdvice_GetUpsert(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)

	const (
		userID   = uint(1)
		scope    = "episode"
		scopeID  = uint(42)
	)
	// 无记录 → nil。
	got, err := repo.GetAdvice(userID, scope, scopeID)
	if err != nil {
		t.Fatalf("GetAdvice (empty): %v", err)
	}
	if got != nil {
		t.Fatalf("GetAdvice on empty returned non-nil: %+v", got)
	}

	// 第一次插入。
	first := &model.StudyAdvice{
		UserID: userID, Scope: scope, ScopeID: scopeID,
		AdviceText: "第一版建议", ModelUsed: "m1",
	}
	if err := repo.UpsertAdvice(first); err != nil {
		t.Fatalf("UpsertAdvice #1: %v", err)
	}
	got, err = repo.GetAdvice(userID, scope, scopeID)
	if err != nil {
		t.Fatalf("GetAdvice (after #1): %v", err)
	}
	if got == nil || got.AdviceText != "第一版建议" {
		t.Fatalf("after upsert #1, want 第一版建议; got %+v", got)
	}

	// 第二次 Upsert:替换(不是新增第二条)。
	second := &model.StudyAdvice{
		UserID: userID, Scope: scope, ScopeID: scopeID,
		AdviceText: "第二版建议", ModelUsed: "m2",
	}
	if err := repo.UpsertAdvice(second); err != nil {
		t.Fatalf("UpsertAdvice #2: %v", err)
	}
	got, err = repo.GetAdvice(userID, scope, scopeID)
	if err != nil {
		t.Fatalf("GetAdvice (after #2): %v", err)
	}
	if got == nil || got.AdviceText != "第二版建议" {
		t.Fatalf("after upsert #2, want replaced 第二版建议; got %+v", got)
	}
	// 确认仍只有一条行(没有重复 insert)。
	var count int64
	db.Model(&model.StudyAdvice{}).
		Where("user_id = ? AND scope = ? AND scope_id = ?", userID, scope, scopeID).
		Count(&count)
	if count != 1 {
		t.Errorf("after 2 upserts, row count = %d; want 1 (replace not insert)", count)
	}
}

// TestStudyAdvice_ScopeIsolation 验证不同 scope 或 scope_id 互不干扰:
// episode#42 和 course#42 是不同建议(即使 scope_id 都是 42)。
func TestStudyAdvice_ScopeIsolation(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)

	const userID = uint(1)
	if err := repo.UpsertAdvice(&model.StudyAdvice{
		UserID: userID, Scope: "episode", ScopeID: 42, AdviceText: "ep 建议",
	}); err != nil {
		t.Fatalf("UpsertAdvice episode: %v", err)
	}
	if err := repo.UpsertAdvice(&model.StudyAdvice{
		UserID: userID, Scope: "course", ScopeID: 42, AdviceText: "course 建议",
	}); err != nil {
		t.Fatalf("UpsertAdvice course: %v", err)
	}

	ep, _ := repo.GetAdvice(userID, "episode", 42)
	if ep == nil || ep.AdviceText != "ep 建议" {
		t.Errorf("episode advice = %+v; want ep 建议", ep)
	}
	cs, _ := repo.GetAdvice(userID, "course", 42)
	if cs == nil || cs.AdviceText != "course 建议" {
		t.Errorf("course advice = %+v; want course 建议", cs)
	}
}
