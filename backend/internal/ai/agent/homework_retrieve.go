package agent

import (
	"context"
	"fmt"
	"strings"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// homework_retrieve.go 是作业卷(Homework)生成的 RAG 检索纯函数。和 quiz 的 search_subtitles
// 工具(tools.go runSearchSubtitles :180-213)平行,但区别在于:
//   - quiz 走 agent 工具调用(模型自己决定何时检索),作业走代码层直接检索(出题编排由
//     service 层控制,先检索 top-K chunk 作为出题素材注入 prompt);
//   - 纯函数:不依赖 agent Toolbox / ToolDeps / MemoryStore,只依赖注入进来的 embedder 和
//     chunks 列表。这样 service 层可以在调用 LLM 前先检索,把素材拼进 user prompt。
//
// 实现复用 ai 包的向量工具(ParseEmbedding / TopK),逻辑镜像 runSearchSubtitles 的检索部分,
// 但去掉了 agent 工具的 observation 文本拼装(那是给模型看的,这里直接返回 chunk 列表)。

// RetrieveTopChunks 在 chunks 中按语义相似度检索和 query 最相关的 top-K 个 chunk。
//
// 流程(参考 tools.go runSearchSubtitles :180-213):
//  1. chunks 为空或 query 为空 → 返回空切片(不报错);
//  2. 收集每个 chunk 的 embedding:调 ai.ParseEmbedding(chunk.Embedding),解析失败的跳过
//     (它不会被当作候选,等价于该 chunk 不可检索);
//  3. embed query:调 emb.Embed(ctx, []string{query});
//  4. 调 ai.TopK(queryVec, chunkVecs, k) 拿到 top-K 的 chunk 索引;
//  5. 返回对应的 []model.ContentChunk(按相似度降序)。
//
// 注意:跳过解析失败的 chunk 时,要保持 chunk 和 vec 的索引对齐——所以这里维护一个独立的
// keptIdx 列表,记录每个成功解析的 vec 对应原 chunks 切片的下标,TopK 返回的是 vec 切片里
// 的下标,通过 keptIdx 映射回原 chunks 切片。
func RetrieveTopChunks(ctx context.Context, emb ai.Embedder, chunks []model.ContentChunk, query string, k int) ([]model.ContentChunk, error) {
	if len(chunks) == 0 || strings.TrimSpace(query) == "" || k <= 0 {
		return nil, nil
	}

	// 收集每个 chunk 的 embedding,跳过解析失败的。keptIdx[i] = 该 vec 对应的 chunks 下标。
	vecs := make([][]float32, 0, len(chunks))
	keptIdx := make([]int, 0, len(chunks))
	for i, ch := range chunks {
		v, err := ai.ParseEmbedding(ch.Embedding)
		if err != nil || len(v) == 0 {
			continue // 不可检索的 chunk,跳过
		}
		vecs = append(vecs, v)
		keptIdx = append(keptIdx, i)
	}
	if len(vecs) == 0 {
		return nil, nil // 所有 chunk 都没有可用 embedding
	}

	// embed query。
	qVecs, err := emb.Embed(ctx, []string{query})
	if err != nil || len(qVecs) == 0 || len(qVecs[0]) == 0 {
		return nil, fmt.Errorf("homework retrieve: embed query: %w", err)
	}
	queryVec := qVecs[0]

	// TopK 拿到 vecs 切片里的 top-K 下标,映射回原 chunks 切片。
	top := ai.TopK(queryVec, vecs, k)
	out := make([]model.ContentChunk, 0, len(top))
	for _, vecIdx := range top {
		if vecIdx < 0 || vecIdx >= len(keptIdx) {
			continue // 防御性:TopK 不该返回越界下标,但防御一下不亏
		}
		originalIdx := keptIdx[vecIdx]
		if originalIdx < 0 || originalIdx >= len(chunks) {
			continue
		}
		out = append(out, chunks[originalIdx])
	}
	return out, nil
}
