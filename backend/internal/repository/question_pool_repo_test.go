package repository

import (
	"testing"
)

// ─── ListWrongAnswersByUserCourse ───

// TestListWrongAnswersByUserCourse_AggregatesAcrossEpisodesAndGenerations 是
// 错题本的核心数据契约:某用户在某课程下的错题 = 该用户在该课程所有 episode、所有
// quiz generation(active + archived)里做错的题。q1(ep1 active)、q3(ep2 active)、
// q4(ep1 archived)三题都该返回。如果查询漏了 archived quiz 或只查单个 episode,
// 这里会少行——这正是错题本最不能犯的错(学生看不到曾做错的历史题)。
func TestListWrongAnswersByUserCourse_AggregatesAcrossEpisodesAndGenerations(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	rows, err := repo.ListWrongAnswersByUserCourse(ids.userID, ids.mathCourseID)
	if err != nil {
		t.Fatalf("ListWrongAnswersByUserCourse: %v", err)
	}
	got := idsFromRows(rows)
	want := map[uint]struct{}{ids.q1ID: {}, ids.q3ID: {}, ids.q4ID: {}}
	for q := range want {
		if _, ok := got[q]; !ok {
			t.Errorf("missing question %d in wrong-book aggregation; got=%v", q, got)
		}
	}
	for q := range got {
		if _, ok := want[q]; !ok {
			t.Errorf("unexpected question %d in wrong-book aggregation; got=%v", q, got)
		}
	}
}

// TestListWrongAnswersByUserCourse_ExcludesCorrectAnswers 答对的题(q2 合成题)
// 不能进错题本。守 correct=false 过滤。
func TestListWrongAnswersByUserCourse_ExcludesCorrectAnswers(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	rows, _ := repo.ListWrongAnswersByUserCourse(ids.userID, ids.mathCourseID)
	got := idsFromRows(rows)
	if _, found := got[ids.synthQID]; found {
		t.Errorf("correctly-answered question %d should NOT appear in wrong book; got=%v", ids.synthQID, got)
	}
}

// TestListWrongAnswersByUserCourse_ScopedByCourse 语文课的题(mathQID,命名沿用
// fixture,实际是另一门课)不能串进数学课的错题本。守课程隔离。
func TestListWrongAnswersByUserCourse_ScopedByCourse(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	rows, _ := repo.ListWrongAnswersByUserCourse(ids.userID, ids.mathCourseID)
	got := idsFromRows(rows)
	if _, found := got[ids.mathQID]; found {
		t.Errorf("question from another course leaked into math wrong book; got=%v", got)
	}
}

// TestListWrongAnswersByUserCourse_ScopedByUser 别的用户的错题不能串进来。守用户隔离。
func TestListWrongAnswersByUserCourse_ScopedByUser(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	// otherUser 也错 q1,但查 user1 的错题本时,那是 otherUser 的错题,不该出现
	// 重复行或归属错乱。这里用 user1 查,断言 q1 只出现一次(不因 otherUser 也错而翻倍)。
	rows, _ := repo.ListWrongAnswersByUserCourse(ids.userID, ids.mathCourseID)
	q1Count := 0
	for _, r := range rows {
		if r.QuestionID == ids.q1ID {
			q1Count++
		}
	}
	if q1Count != 1 {
		t.Errorf("q1 should appear once for user1 (otherUser's answer must not leak); got %d", q1Count)
	}
}

// TestListWrongAnswersByUserCourse_EmptyReturnsNonNil 空结果返回非 nil slice,
// 客户端能干净渲染"暂无错题"而非 NPE。对齐 TestListArchivedQuizzes_Empty 范式。
func TestListWrongAnswersByUserCourse_EmptyReturnsNonNil(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)
	rows, err := repo.ListWrongAnswersByUserCourse(uint(1), uint(100))
	if err != nil {
		t.Fatalf("empty query: %v", err)
	}
	if rows == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows on empty DB, got %d", len(rows))
	}
}

// TestListWrongAnswersByUserCourse_JoinsStemAndChunk 验证 join 出的题面/chunk
// 上下文正确(前端要展示题目和知识点)。守 join 字段映射。
func TestListWrongAnswersByUserCourse_JoinsStemAndChunk(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	rows, _ := repo.ListWrongAnswersByUserCourse(ids.userID, ids.mathCourseID)
	var q1Row *WrongBookRow
	for i := range rows {
		if rows[i].QuestionID == ids.q1ID {
			q1Row = &rows[i]
			break
		}
	}
	if q1Row == nil {
		t.Fatal("q1 row not found")
	}
	if q1Row.Stem != "q1" {
		t.Errorf("stem = %q; want \"q1\"", q1Row.Stem)
	}
	if q1Row.ChunkID != ids.chunk1ID {
		t.Errorf("chunk_id = %d; want %d", q1Row.ChunkID, ids.chunk1ID)
	}
	if q1Row.EpisodeID != ids.ep1ID {
		t.Errorf("episode_id = %d; want %d", q1Row.EpisodeID, ids.ep1ID)
	}
	if q1Row.SubjectID != ids.subjectID {
		t.Errorf("subject_id = %d; want %d", q1Row.SubjectID, ids.subjectID)
	}
}

// ─── ListWrongAnswersByUser (全局 + 过滤) ───

// TestListWrongAnswersByUser_NoFilter 全局视图:该用户全平台所有错题(目前只在数学
// 课做错,语文课没做题)。守 0 = 不过滤的契约。
func TestListWrongAnswersByUser_NoFilter(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	rows, err := repo.ListWrongAnswersByUser(ids.userID, 0, 0, 0)
	if err != nil {
		t.Fatalf("ListWrongAnswersByUser: %v", err)
	}
	got := idsFromRows(rows)
	// q1, q3, q4 都错;无过滤应全部返回。
	for _, want := range []uint{ids.q1ID, ids.q3ID, ids.q4ID} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing question %d in unfiltered view; got=%v", want, got)
		}
	}
}

// TestListWrongAnswersByUser_FilterByChunk 按 chunk 过滤:只查 chunk1 的错题
// 应只返回 q1 + q4(都锚定 chunk1),不含 q3(chunk2)。
func TestListWrongAnswersByUser_FilterByChunk(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	rows, err := repo.ListWrongAnswersByUser(ids.userID, 0, 0, ids.chunk1ID)
	if err != nil {
		t.Fatalf("ListWrongAnswersByUser by chunk: %v", err)
	}
	got := idsFromRows(rows)
	if _, ok := got[ids.q1ID]; !ok {
		t.Errorf("q1 (chunk1) missing from chunk1-filtered view; got=%v", got)
	}
	if _, ok := got[ids.q3ID]; ok {
		t.Errorf("q3 (chunk2) leaked into chunk1-filtered view; got=%v", got)
	}
}

// TestListWrongAnswersByUser_FilterByCourse 按课程过滤:查语文课错题应返回空
// (该用户只在数学课做错)。
func TestListWrongAnswersByUser_FilterByCourse(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	rows, err := repo.ListWrongAnswersByUser(ids.userID, 0, ids.chineseCourseID, 0)
	if err != nil {
		t.Fatalf("ListWrongAnswersByUser by course: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("chinese course has no wrong answers for this user; got %d rows", len(rows))
	}
}

// ─── ListQuestionsByCourseForExam (考试抽题池) ───

// TestListQuestionsByCourseForExam_ExcludesSyntheticQuestions 合成题(chunkID=0)
// 没有知识点锚点,考试抽题价值低,应被排除。守 chunk_id > 0 过滤。
func TestListQuestionsByCourseForExam_ExcludesSyntheticQuestions(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	rows, err := repo.ListQuestionsByCourseForExam(ids.mathCourseID)
	if err != nil {
		t.Fatalf("ListQuestionsByCourseForExam: %v", err)
	}
	for _, q := range rows {
		if q.ID == ids.synthQID {
			t.Errorf("synthetic question (chunkID=0) should be excluded from exam pool")
		}
		if q.ChunkID == 0 {
			t.Errorf("question %d has chunkID=0; all exam pool questions must be anchored to a chunk", q.ID)
		}
	}
}

// TestListQuestionsByCourseForExam_IncludesAllGenerations 题库是历史积累,archived
// quiz 的题(q4)也算——regenerate 不该让题库缩水。守跨 generation 包含。
func TestListQuestionsByCourseForExam_IncludesAllGenerations(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	rows, _ := repo.ListQuestionsByCourseForExam(ids.mathCourseID)
	got := make(map[uint]struct{}, len(rows))
	for _, q := range rows {
		got[q.ID] = struct{}{}
	}
	// q1(ep1 active), q3(ep2 active), q4(ep1 archived) 都该在池里;q2 合成排除。
	for _, want := range []uint{ids.q1ID, ids.q3ID, ids.q4ID} {
		if _, ok := got[want]; !ok {
			t.Errorf("question %d missing from exam pool (must span all generations); got=%v", want, got)
		}
	}
	if _, ok := got[ids.synthQID]; ok {
		t.Errorf("synthetic q2 leaked into pool")
	}
}

// TestListQuestionsByCourseForExam_ScopedByCourse 语文课的题不能进数学课的考试池。
func TestListQuestionsByCourseForExam_ScopedByCourse(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	rows, _ := repo.ListQuestionsByCourseForExam(ids.mathCourseID)
	for _, q := range rows {
		if q.ID == ids.mathQID {
			t.Errorf("question from another course leaked into math exam pool")
		}
	}
}

// TestListQuestionsByCourseForExam_EmptyReturnsNonNil 课程无题时返回非 nil 空 slice。
func TestListQuestionsByCourseForExam_EmptyReturnsNonNil(t *testing.T) {
	db := setupTestDB(t)
	repo := NewAIContentRepository(db)
	rows, err := repo.ListQuestionsByCourseForExam(uint(999))
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rows == nil {
		t.Fatal("expected non-nil empty slice")
	}
}

// ─── ListChunksByCourseForExam ───

// TestListChunksByCourseForExam_CrossEpisode 取某课程全部 subtitle chunk(跨 episode)。
// 守去掉单 episode 过滤后仍按 course 作用域 + subtitle 源过滤。
func TestListChunksByCourseForExam_CrossEpisode(t *testing.T) {
	db, ids := seedQuestionPool(t)
	repo := NewAIContentRepository(db)

	chunks, err := repo.ListChunksByCourseForExam(ids.mathCourseID)
	if err != nil {
		t.Fatalf("ListChunksByCourseForExam: %v", err)
	}
	got := make(map[uint]struct{}, len(chunks))
	for _, c := range chunks {
		got[c.ID] = struct{}{}
	}
	// chunk1(ep1) + chunk2(ep2) 都该在;语文课的 chunk 不该在。
	if _, ok := got[ids.chunk1ID]; !ok {
		t.Errorf("chunk1 (ep1) missing; got=%v", got)
	}
	if _, ok := got[ids.chunk2ID]; !ok {
		t.Errorf("chunk2 (ep2) missing; got=%v", got)
	}
	for _, c := range chunks {
		if c.CourseID != ids.mathCourseID {
			t.Errorf("chunk %d has course_id %d; want %d (leak from another course)", c.ID, c.CourseID, ids.mathCourseID)
		}
	}
}
