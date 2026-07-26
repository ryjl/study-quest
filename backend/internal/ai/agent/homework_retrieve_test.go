package agent

import (
	"context"
	"errors"
	"testing"

	"studyquest/backend/internal/model"
)

// homework_retrieve_test.go 验证 RetrieveTopChunks 的纯函数 RAG 检索行为:
//   - 余弦相似度排序(query 和某个 chunk 最像 → 排第一)
//   - k 截断(返回不超过 k 个)
//   - 空 chunks → 空返回
//   - 空 query → 空返回
//   - 跳过解析失败的 embedding(有垃圾 embedding 的 chunk 不进候选)
//
// 用 fakeHWEmbedder 实现 ai.Embedder 接口(返回固定向量),避免真实 embedding 调用。
// 参考 tools_test.go 的 fakeEmbedder 范式,但那个只实现了 Embed(单方法);RetrieveTopChunks
// 需要 ai.Embedder(含 Dim/Ping/ProviderType),所以这里实现完整接口。

// fakeHWEmbedder 是测试用的 ai.Embedder。Embed 返回固定的 queryVec(忽略输入文本),
// 让测试可以精确控制"query 和哪个 chunk 最像"。其它方法(Dim/Ping/ProviderType)返回
// 占位值——RetrieveTopChunks 不调用它们,但接口要求实现。
type fakeHWEmbedder struct {
	queryVec  []float32
	embedErr  error
	called    int
	lastInput []string
}

func (f *fakeHWEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.called++
	f.lastInput = texts
	if f.embedErr != nil {
		return nil, f.embedErr
	}
	return [][]float32{f.queryVec}, nil
}
func (f *fakeHWEmbedder) Dim() int             { return len(f.queryVec) }
func (f *fakeHWEmbedder) Ping(ctx context.Context) error { return nil }
func (f *fakeHWEmbedder) ProviderType() string  { return "fake_hw_embedder" }

// encEmbedding 把 []float32 编码成 ContentChunk.Embedding 字段期望的 JSON 字符串格式
// (ai.ParseEmbedding 的输入)。和 vector_test.go 的 marshaling 一致。
func encEmbedding(v []float32) string {
	out := "["
	for i, x := range v {
		if i > 0 {
			out += ","
		}
		out += formatF32(x)
	}
	return out + "]"
}

// formatF32 renders a float32 the way encoding/json would, without importing json
// into the test helper (keeps the helper self-contained and visible). Sufficient
// for the small integer-ish test vectors we use (0/1/-1/0.5).
func formatF32(x float32) string {
	// 整数直接转。
	if x == float32(int(x)) {
		return strconvI(int(x))
	}
	// 0.5 这种简单小数手写。
	switch x {
	case 0.5:
		return "0.5"
	case -0.5:
		return "-0.5"
	}
	return "0"
}

// strconvI is strconv.Itoa without the import (test helper self-contained).
func strconvI(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// TestRetrieveTopChunksCosineRanking 余弦相似度排序:query 和 chunk[2] 最像 → 排第一。
func TestRetrieveTopChunksCosineRanking(t *testing.T) {
	// 三个 chunk 的 embedding:
	//   chunk[0] = [1,0,0]  (和 query 正交,相似度 0)
	//   chunk[1] = [0,1,0]  (和 query 正交,相似度 0)
	//   chunk[2] = [1,1,0]  (和 query 同向,相似度最高)
	// query = [1,1,0]
	// 期望:chunk[2] 排第一(chunk_index=2),后面是 chunk[0] 和 chunk[1](都 sim=0,
	// 稳定排序保持原顺序 → chunk[0] 在 chunk[1] 前)。
	chunks := []model.ContentChunk{
		{ID: 10, ChunkIndex: 0, Text: "零号", Embedding: encEmbedding([]float32{1, 0, 0})},
		{ID: 11, ChunkIndex: 1, Text: "壹号", Embedding: encEmbedding([]float32{0, 1, 0})},
		{ID: 12, ChunkIndex: 2, Text: "贰号(最像 query)", Embedding: encEmbedding([]float32{1, 1, 0})},
	}
	emb := &fakeHWEmbedder{queryVec: []float32{1, 1, 0}}

	got, err := RetrieveTopChunks(context.Background(), emb, chunks, "查询", 3)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(got))
	}
	// 最像的 chunk[2](ID=12)应排第一。
	if got[0].ID != 12 {
		t.Errorf("expected top match ID=12 (chunk[2], cosine=1), got ID=%d. order: %+v", got[0].ID, got)
	}
	// embedder 被调一次,传入 query。
	if emb.called != 1 {
		t.Errorf("expected embedder called once, got %d", emb.called)
	}
	if len(emb.lastInput) != 1 || emb.lastInput[0] != "查询" {
		t.Errorf("expected embedder called with [查询], got %v", emb.lastInput)
	}
}

// TestRetrieveTopChunksKTruncate k 截断:返回不超过 k 个。
func TestRetrieveTopChunksKTruncate(t *testing.T) {
	chunks := []model.ContentChunk{
		{ID: 1, ChunkIndex: 0, Embedding: encEmbedding([]float32{1, 0})},
		{ID: 2, ChunkIndex: 1, Embedding: encEmbedding([]float32{1, 0})},
		{ID: 3, ChunkIndex: 2, Embedding: encEmbedding([]float32{1, 0})},
		{ID: 4, ChunkIndex: 3, Embedding: encEmbedding([]float32{1, 0})},
	}
	emb := &fakeHWEmbedder{queryVec: []float32{1, 0}}

	got, err := RetrieveTopChunks(context.Background(), emb, chunks, "q", 2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected k=2 truncation, got %d chunks", len(got))
	}

	// k 大于 chunks 数时返回所有(不超过 chunks 数)。
	gotAll, err := RetrieveTopChunks(context.Background(), emb, chunks, "q", 100)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(gotAll) != 4 {
		t.Errorf("expected all 4 chunks when k>len(chunks), got %d", len(gotAll))
	}
}

// TestRetrieveTopChunksEmptyCases 空 chunks / 空 query / k<=0 → 空返回(不报错、不调 embedder)。
func TestRetrieveTopChunksEmptyCases(t *testing.T) {
	emb := &fakeHWEmbedder{queryVec: []float32{1, 0}}

	cases := []struct {
		name   string
		chunks []model.ContentChunk
		query  string
		k      int
	}{
		{"empty-chunks", nil, "q", 5},
		{"empty-query", []model.ContentChunk{{ID: 1, Embedding: encEmbedding([]float32{1})}}, "", 5},
		{"whitespace-query", []model.ContentChunk{{ID: 1, Embedding: encEmbedding([]float32{1})}}, "   ", 5},
		{"k-zero", []model.ContentChunk{{ID: 1, Embedding: encEmbedding([]float32{1})}}, "q", 0},
		{"k-negative", []model.ContentChunk{{ID: 1, Embedding: encEmbedding([]float32{1})}}, "q", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			emb.called = 0 // reset between subtests
			got, err := RetrieveTopChunks(context.Background(), emb, c.chunks, c.query, c.k)
			if err != nil {
				t.Errorf("expected no error for %s, got %v", c.name, err)
			}
			if len(got) != 0 {
				t.Errorf("expected empty result for %s, got %d chunks", c.name, len(got))
			}
			if emb.called != 0 {
				t.Errorf("expected embedder NOT called for %s (short-circuit), got %d calls", c.name, emb.called)
			}
		})
	}
}

// TestRetrieveTopChunksSkipsBadEmbeddings 跳过解析失败的 embedding:有垃圾 embedding 的 chunk
// 不进候选(即使其它 chunk 合法)。
func TestRetrieveTopChunksSkipsBadEmbeddings(t *testing.T) {
	chunks := []model.ContentChunk{
		{ID: 1, ChunkIndex: 0, Embedding: encEmbedding([]float32{1, 0})}, // 合法
		{ID: 2, ChunkIndex: 1, Embedding: "not-valid-json"},              // 垃圾,跳过
		{ID: 3, ChunkIndex: 2, Embedding: ""},                             // 空,跳过
		{ID: 4, ChunkIndex: 3, Embedding: encEmbedding([]float32{1, 0})}, // 合法
	}
	emb := &fakeHWEmbedder{queryVec: []float32{1, 0}}

	got, err := RetrieveTopChunks(context.Background(), emb, chunks, "q", 5)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// 只有 2 个合法 chunk(ID=1, 4)进候选,即使 k=5 也只返回 2 个。
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks (bad embeddings skipped), got %d", len(got))
	}
	ids := []uint{got[0].ID, got[1].ID}
	for _, badID := range []uint{2, 3} {
		for _, id := range ids {
			if id == badID {
				t.Errorf("bad-embedding chunk ID=%d should not be in results: %v", badID, ids)
			}
		}
	}
}

// TestRetrieveTopChunksAllBadEmbeddings 所有 chunk 的 embedding 都是垃圾 → 空返回(不调 embedder)。
func TestRetrieveTopChunksAllBadEmbeddings(t *testing.T) {
	chunks := []model.ContentChunk{
		{ID: 1, Embedding: "garbage"},
		{ID: 2, Embedding: ""},
	}
	emb := &fakeHWEmbedder{queryVec: []float32{1, 0}}
	got, err := RetrieveTopChunks(context.Background(), emb, chunks, "q", 5)
	if err != nil {
		t.Errorf("expected no error when all embeddings bad, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result when all embeddings bad, got %d", len(got))
	}
	// embedder 不应被调用(短路:没有候选 vec 就没必要 embed query)。
	if emb.called != 0 {
		t.Errorf("expected embedder NOT called (no valid vecs), got %d calls", emb.called)
	}
}

// TestRetrieveTopChunksEmbedError embedder 返回 error → 透传 error。
func TestRetrieveTopChunksEmbedError(t *testing.T) {
	chunks := []model.ContentChunk{
		{ID: 1, Embedding: encEmbedding([]float32{1, 0})},
	}
	embErr := errors.New("simulated embed failure")
	emb := &fakeHWEmbedder{queryVec: []float32{1, 0}, embedErr: embErr}

	_, err := RetrieveTopChunks(context.Background(), emb, chunks, "q", 5)
	if err == nil {
		t.Fatal("expected error when embedder fails, got nil")
	}
	// error 应该 wrap 了原始的 embed error。
	if !contains(err.Error(), "simulated embed failure") {
		t.Errorf("expected error to wrap embed failure, got: %v", err)
	}
}

// ensure errors import is used (fakeHWEmbedder could return errors in extended
// tests later).
var _ = errors.New
