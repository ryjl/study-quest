package agent

import (
	"context"
	"errors"
	"testing"

	"studyquest/backend/internal/model"
)

// fakeMemoryRepo is a minimal in-memory MemoryRepo for testing MemoryStore.
type fakeMemoryRepo struct {
	rows             []model.KnowledgeMemory
	courseRows       []model.KnowledgeMemory // 返回给 GetCourseMasteries
	subjectRows      []model.KnowledgeMemory // 返回给 GetSubjectMasteries
	upserts          []upsertCall            // recorded calls to UpsertMemoryOnAnswer
	upsertFn         func(userID, chunkID, episodeID, courseID uint, correct bool) error
}

type upsertCall struct {
	userID, chunkID, episodeID, courseID uint
	correct                              bool
}

func (f *fakeMemoryRepo) GetMasteries(userID, episodeID uint) ([]model.KnowledgeMemory, error) {
	return f.rows, nil
}
func (f *fakeMemoryRepo) UpsertMemoryOnAnswer(userID, chunkID, episodeID, courseID uint, correct bool) error {
	f.upserts = append(f.upserts, upsertCall{userID, chunkID, episodeID, courseID, correct})
	if f.upsertFn != nil {
		return f.upsertFn(userID, chunkID, episodeID, courseID, correct)
	}
	return nil
}
// 跨课程聚合方法(Phase C advice 用)。未配置字段时返回 nil,测试可在构造时塞入。
func (f *fakeMemoryRepo) GetCourseMasteries(userID, courseID uint) ([]model.KnowledgeMemory, error) {
	if f.courseRows != nil {
		return f.courseRows, nil
	}
	return nil, nil
}
func (f *fakeMemoryRepo) GetSubjectMasteries(userID, subjectID uint) ([]model.KnowledgeMemory, error) {
	if f.subjectRows != nil {
		return f.subjectRows, nil
	}
	return nil, nil
}

func TestApplyMastery(t *testing.T) {
	tests := []struct {
		name    string
		current float64
		correct bool
		want    float64
		field   string
	}{
		{"correct nudges up", 0.5, true, 0.6, "correct_count"},
		{"wrong nudges down", 0.5, false, 0.3, "wrong_count"},
		{"correct clamps at 1", 0.95, true, 1.0, "correct_count"},
		{"wrong clamps at 0", 0.1, false, 0.0, "wrong_count"},
		{"zero stays zero on wrong", 0.0, false, 0.0, "wrong_count"},
		{"one stays one on correct", 1.0, true, 1.0, "correct_count"},
		// asymmetric: wrong costs more than correct gains
		{"wrong from 0.5 → 0.3", 0.5, false, 0.3, "wrong_count"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, field := ApplyMastery(tc.current, tc.correct)
			if got != tc.want || field != tc.field {
				t.Errorf("ApplyMastery(%v,%v) = (%v,%q), want (%v,%q)", tc.current, tc.correct, got, field, tc.want, tc.field)
			}
		})
	}
}

func TestMasteriesOrderedWorstFirst(t *testing.T) {
	repo := &fakeMemoryRepo{rows: []model.KnowledgeMemory{
		{ChunkID: 1, Mastery: 0.8},
		{ChunkID: 2, Mastery: 0.2},
		{ChunkID: 3, Mastery: 0.5},
	}}
	store := NewMemoryStore(repo)
	got, err := store.Masteries(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ChunkID != 2 || got[1].ChunkID != 3 || got[2].ChunkID != 1 {
		t.Errorf("expected worst-first [2,3,1], got %+v", got)
	}
}

// TestCourseMasteriesOrderedWorstFirst 验证跨课程聚合也按 mastery ASC 排序(弱点优先)。
// advice agent 的 prompt 依赖这个顺序——最该加强的知识点要列在最前。
func TestCourseMasteriesOrderedWorstFirst(t *testing.T) {
	repo := &fakeMemoryRepo{courseRows: []model.KnowledgeMemory{
		{ChunkID: 1, Mastery: 0.9},
		{ChunkID: 2, Mastery: 0.1},
		{ChunkID: 3, Mastery: 0.5},
	}}
	store := NewMemoryStore(repo)
	got, err := store.CourseMasteries(context.Background(), 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ChunkID != 2 || got[1].ChunkID != 3 || got[2].ChunkID != 1 {
		t.Errorf("expected worst-first [2,3,1], got %+v", got)
	}
}

// TestSubjectMasteriesOrderedWorstFirst 科目级聚合同样按 mastery ASC 排序。
func TestSubjectMasteriesOrderedWorstFirst(t *testing.T) {
	repo := &fakeMemoryRepo{subjectRows: []model.KnowledgeMemory{
		{ChunkID: 1, Mastery: 0.7},
		{ChunkID: 2, Mastery: 0.3},
	}}
	store := NewMemoryStore(repo)
	got, err := store.SubjectMasteries(context.Background(), 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ChunkID != 2 || got[1].ChunkID != 1 {
		t.Errorf("expected worst-first [2,1], got %+v", got)
	}
}

func TestRecordAnswerSkipsSyntheticQuestion(t *testing.T) {
	repo := &fakeMemoryRepo{}
	store := NewMemoryStore(repo)
	// chunkID == 0 = synthetic question → no-op
	if err := store.RecordAnswer(context.Background(), 1, 0, 10, 20, true); err != nil {
		t.Fatal(err)
	}
	if len(repo.upserts) != 0 {
		t.Errorf("synthetic question should not upsert, got %d calls", len(repo.upserts))
	}
}

func TestRecordAnswerCallsRepo(t *testing.T) {
	repo := &fakeMemoryRepo{}
	store := NewMemoryStore(repo)
	if err := store.RecordAnswer(context.Background(), 7, 42, 10, 20, true); err != nil {
		t.Fatal(err)
	}
	if len(repo.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(repo.upserts))
	}
	c := repo.upserts[0]
	if c.userID != 7 || c.chunkID != 42 || c.episodeID != 10 || c.courseID != 20 || !c.correct {
		t.Errorf("unexpected call %+v", c)
	}
}

func TestRecordAnswerPropagatesError(t *testing.T) {
	repo := &fakeMemoryRepo{upsertFn: func(uint, uint, uint, uint, bool) error {
		return errors.New("db down")
	}}
	store := NewMemoryStore(repo)
	if err := store.RecordAnswer(context.Background(), 1, 1, 1, 1, true); err == nil {
		t.Error("expected error to propagate")
	}
}
