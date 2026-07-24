package repository

import (
	"testing"

	"studyquest/backend/internal/model"
)

// TestUpsertOnWrong_NewInsert 首次做错:新建行,FirstWrongAt + AttemptCount=1。
func TestUpsertOnWrong_NewInsert(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)

	err := repo.UpsertOnWrong(model.WrongBookItem{
		UserID: 1, QuestionID: 10, ChunkID: 100, CourseID: 200, SubjectID: 5,
	})
	if err != nil {
		t.Fatalf("UpsertOnWrong: %v", err)
	}
	got, err := repo.GetItem(1, 10)
	if err != nil || got == nil {
		t.Fatalf("GetItem after insert: %v %v", got, err)
	}
	if got.AttemptCount != 1 {
		t.Errorf("AttemptCount = %d; want 1 (first wrong)", got.AttemptCount)
	}
	if got.FirstWrongAt.IsZero() {
		t.Error("FirstWrongAt should be set on insert")
	}
	if got.Mastered {
		t.Error("Mastered should be false on insert")
	}
	if got.ChunkID != 100 || got.CourseID != 200 || got.SubjectID != 5 {
		t.Errorf("redundant ids = chunk%d/course%d/subject%d; want 100/200/5", got.ChunkID, got.CourseID, got.SubjectID)
	}
}

// TestUpsertOnWrong_DuplicateIncrementsCount 再次做错同一题:AttemptCount++ + 刷
// LastAttemptedAt,但 FirstWrongAt 保留(首次做错时间不能被覆盖)。
func TestUpsertOnWrong_DuplicateIncrementsCount(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)

	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10})
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10})
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10})

	got, _ := repo.GetItem(1, 10)
	if got.AttemptCount != 3 {
		t.Errorf("AttemptCount = %d; want 3 (three wrongs)", got.AttemptCount)
	}
	if got.FirstWrongAt.IsZero() {
		t.Error("FirstWrongAt lost after repeated upserts")
	}
}

// TestUpsertOnWrong_PerUserQuestionIsolation 不同 (user, question) 互不影响——
// unique index 是 (user_id, question_id),A 做错 q1 不该影响 B 做 q1。
func TestUpsertOnWrong_PerUserQuestionIsolation(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)

	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10})
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 2, QuestionID: 10})
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10})

	u1, _ := repo.GetItem(1, 10)
	u2, _ := repo.GetItem(2, 10)
	if u1.AttemptCount != 2 {
		t.Errorf("user1 q1 AttemptCount = %d; want 2", u1.AttemptCount)
	}
	if u2.AttemptCount != 1 {
		t.Errorf("user2 q1 AttemptCount = %d; want 1 (isolated from user1's 2nd wrong)", u2.AttemptCount)
	}
}

// TestMarkMastered_TrueThenFalse 标记掌握刷 MasteredAt;取消掌握清空。往返。
func TestMarkMastered_TrueThenFalse(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10})

	if err := repo.MarkMastered(1, 10, true); err != nil {
		t.Fatalf("MarkMastered true: %v", err)
	}
	got, _ := repo.GetItem(1, 10)
	if !got.Mastered || got.MasteredAt == nil {
		t.Errorf("after mark true: Mastered=%v MasteredAt=%v; want true + set", got.Mastered, got.MasteredAt)
	}

	if err := repo.MarkMastered(1, 10, false); err != nil {
		t.Fatalf("MarkMastered false: %v", err)
	}
	got, _ = repo.GetItem(1, 10)
	if got.Mastered || got.MasteredAt != nil {
		t.Errorf("after mark false: Mastered=%v MasteredAt=%v; want false + nil", got.Mastered, got.MasteredAt)
	}
}

// TestMarkMastered_NonexistentNoError 标记不存在的行不报错(GORM Updates 0 行无错)。
// 守边界:学生可能对已删题操作,不该 500。
func TestMarkMastered_NonexistentNoError(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)
	if err := repo.MarkMastered(999, 999, true); err != nil {
		t.Errorf("MarkMastered on nonexistent row should not error; got %v", err)
	}
}

// TestGetItem_NonexistentReturnsNilNil 不存在返回 (nil, nil),不返回 error。
// 对齐 GetQuiz 的 not-found 范式。
func TestGetItem_NonexistentReturnsNilNil(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)
	got, err := repo.GetItem(1, 1)
	if err != nil {
		t.Errorf("nonexistent GetItem err = %v; want nil", err)
	}
	if got != nil {
		t.Errorf("nonexistent GetItem = %v; want nil", got)
	}
}

// TestListByUser_Filters 按 course/subject/mastered 过滤的组合。
func TestListByUser_Filters(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)
	course1, course2 := uint(100), uint(200)
	math, chinese := uint(1), uint(2)
	mastered := true
	unmastered := false

	// 灌 4 条:user1 在数学课(已掌握)、语文课(未掌握)、数学课另一题(未掌握)。
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10, CourseID: course1, SubjectID: math})
	repo.MarkMastered(1, 10, true)
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 20, CourseID: course2, SubjectID: chinese})
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 30, CourseID: course1, SubjectID: math})

	// 只查未掌握 → q20 + q30(2 条)。
	got, err := repo.ListByUser(1, WrongBookFilter{Mastered: &unmastered})
	if err != nil {
		t.Fatalf("ListByUser unmastered: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("unmastered filter: got %d; want 2", len(got))
	}
	// 只查已掌握 → q10(1 条)。
	got, _ = repo.ListByUser(1, WrongBookFilter{Mastered: &mastered})
	if len(got) != 1 || got[0].QuestionID != 10 {
		t.Errorf("mastered filter: got %+v; want q10", got)
	}
	// 按 course1 过滤(不分掌握) → q10 + q30(2 条)。
	got, _ = repo.ListByUser(1, WrongBookFilter{CourseID: &course1})
	if len(got) != 2 {
		t.Errorf("course1 filter: got %d; want 2", len(got))
	}
	// 按 subject(语文)过滤 → q20(1 条)。
	got, _ = repo.ListByUser(1, WrongBookFilter{SubjectID: &chinese})
	if len(got) != 1 || got[0].QuestionID != 20 {
		t.Errorf("chinese subject filter: got %+v; want q20", got)
	}
}

// TestListByUser_EmptyReturnsNonNil 空结果非 nil slice。
func TestListByUser_EmptyReturnsNonNil(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)
	got, err := repo.ListByUser(999, WrongBookFilter{})
	if err != nil {
		t.Fatalf("empty ListByUser: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("expected 0; got %d", len(got))
	}
}

// TestIncrementCorrectStreak_ThresholdMastered 连续答对到阈值(3)才 mastered,
// 掌握时 streak 归 0、mastered_at 盖戳。途中(1、2 次)streak 累加但不掌握。
func TestIncrementCorrectStreak_ThresholdMastered(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)
	// 先做错进错题本。
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10, ChunkID: 100, CourseID: 200})

	// 第 1 次 → streak=1,未掌握。
	if mastered, err := repo.IncrementCorrectStreak(1, 10); err != nil || mastered {
		t.Fatalf("1st: err=%v mastered=%v; want nil/false", err, mastered)
	}
	got, _ := repo.GetItem(1, 10)
	if got.CorrectStreak != 1 || got.Mastered {
		t.Errorf("after 1st: streak=%d mastered=%v; want 1/false", got.CorrectStreak, got.Mastered)
	}
	// 第 2 次 → streak=2,未掌握。
	repo.IncrementCorrectStreak(1, 10)
	got, _ = repo.GetItem(1, 10)
	if got.CorrectStreak != 2 || got.Mastered {
		t.Errorf("after 2nd: streak=%d mastered=%v; want 2/false", got.CorrectStreak, got.Mastered)
	}
	// 第 3 次 → 掌握,streak 归 0,mastered_at 盖戳。
	if mastered, err := repo.IncrementCorrectStreak(1, 10); err != nil || !mastered {
		t.Fatalf("3rd: err=%v mastered=%v; want nil/true", err, mastered)
	}
	got, _ = repo.GetItem(1, 10)
	if got.Mastered != true || got.CorrectStreak != 0 || got.MasteredAt == nil {
		t.Errorf("after 3rd: mastered=%v streak=%d masteredAt=%v; want true/0/<set>", got.Mastered, got.CorrectStreak, got.MasteredAt)
	}
}

// TestIncrementCorrectStreak_AlreadyMasteredIdempotent 已掌握的题再答对幂等:不报错、
// 不再触发 mastered(已是 true)、streak 保持 0。
func TestIncrementCorrectStreak_AlreadyMasteredIdempotent(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10})
	// 手动掌握。
	repo.MarkMastered(1, 10, true)
	// 再答对 → 幂等。
	mastered, err := repo.IncrementCorrectStreak(1, 10)
	if err != nil || mastered {
		t.Errorf("already mastered: err=%v mastered=%v; want nil/false", err, mastered)
	}
}

// TestUpsertOnWrong_ClearsStreak 做错(UpsertOnWrong)会清零 CorrectStreak
// (答错打断连对)。
func TestUpsertOnWrong_ClearsStreak(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWrongBookRepository(db)
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10})
	// 连对 2 次(streak=2)。
	repo.IncrementCorrectStreak(1, 10)
	repo.IncrementCorrectStreak(1, 10)
	got, _ := repo.GetItem(1, 10)
	if got.CorrectStreak != 2 {
		t.Fatalf("setup: streak=%d; want 2", got.CorrectStreak)
	}
	// 又做错 → streak 清零。
	repo.UpsertOnWrong(model.WrongBookItem{UserID: 1, QuestionID: 10})
	got, _ = repo.GetItem(1, 10)
	if got.CorrectStreak != 0 {
		t.Errorf("after wrong: streak=%d; want 0 (cleared)", got.CorrectStreak)
	}
}
