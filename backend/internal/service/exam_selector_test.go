package service

import (
	"math/rand"
	"testing"
)

// fixedRng 返回固定 seed 的 rand,让抽题算法可重复(否则单测无法断言确切结果)。
func fixedRng() *rand.Rand {
	return rand.New(rand.NewSource(42))
}

// makePool 构造一个题池 helper:id 从 base 起按 type 给的顺序造,全部挂同一 chunk。
func makePool(base uint, chunkID uint, types []string) []PoolQuestion {
	out := make([]PoolQuestion, len(types))
	for i, t := range types {
		out[i] = PoolQuestion{ID: base + uint(i), ChunkID: chunkID, Type: t}
	}
	return out
}

// TestSelectExam_WeaknessPriority 弱点优先:两个 chunk 各有题,mastery 低的 chunk
// 的题应先被抽(在 targetCount 只够覆盖一个 chunk 时,抽弱点的那个)。守权重方向。
func TestSelectExam_WeaknessPriority(t *testing.T) {
	// chunk1 mastery 0.8(已掌握,权重 0.2),chunk2 mastery 0.1(弱点,权重 0.9)。
	pool := append(makePool(1, 100, []string{"choice"}),
		makePool(10, 200, []string{"choice"})...)
	masteries := []MasteryEntry{{100, 0.8}, {200, 0.1}}

	// 只要 1 道题:必须抽 chunk2(弱点),不是 chunk1。
	sel := SelectExamQuestions(fixedRng(), pool, masteries, 1, 0.4)
	if len(sel.Picked) != 1 {
		t.Fatalf("want 1 picked; got %d", len(sel.Picked))
	}
	if sel.Picked[0].ChunkID != 200 {
		t.Errorf("weakness priority broken: picked chunk %d; want 200 (mastery 0.1)", sel.Picked[0].ChunkID)
	}
}

// TestSelectExam_CoverageMultipleChunks 覆盖度:targetCount >= chunk 数时,第一轮
// 广覆盖应让每个 chunk 至少抽 1 道,而不是全挤在权重最高的 chunk。守综合检测意义。
func TestSelectExam_CoverageMultipleChunks(t *testing.T) {
	// 3 个 chunk,各 3 道 choice。要 3 道题 → 每个 chunk 各 1 道。
	pool := []PoolQuestion{}
	for _, cid := range []uint{100, 200, 300} {
		pool = append(pool, makePool(cid*10, cid, []string{"choice", "choice", "choice"})...)
	}
	// mastery:chunk100 最弱(0.1),chunk300 最好(0.9)。第一轮按权重序抽,各 1 道。
	masteries := []MasteryEntry{{100, 0.1}, {200, 0.5}, {300, 0.9}}

	sel := SelectExamQuestions(fixedRng(), pool, masteries, 3, 0.4)
	if len(sel.Picked) != 3 {
		t.Fatalf("want 3 picked; got %d", len(sel.Picked))
	}
	seenChunks := map[uint]int{}
	for _, q := range sel.Picked {
		seenChunks[q.ChunkID]++
	}
	for _, cid := range []uint{100, 200, 300} {
		if seenChunks[cid] != 1 {
			t.Errorf("coverage broken: chunk %d picked %d times; want 1 (first round should spread across chunks)", cid, seenChunks[cid])
		}
	}
}

// TestSelectExam_TypeBalance 题型均衡:同一 chunk 有 choice+fill+multi 时,
// 轮转应让抽出的题尽量覆盖不同题型,而不是全抽同一种。
func TestSelectExam_TypeBalance(t *testing.T) {
	// 单 chunk,各题型 3 道。要 3 道 → 应各抽 1 道(choice/fill/multi 各一)。
	pool := makePool(1, 100, []string{"choice", "multi_choice", "fill", "choice", "multi_choice", "fill"})
	sel := SelectExamQuestions(fixedRng(), pool, nil, 3, 0.4)
	if len(sel.Picked) != 3 {
		t.Fatalf("want 3 picked; got %d", len(sel.Picked))
	}
	seenTypes := map[string]int{}
	for _, q := range sel.Picked {
		seenTypes[q.Type]++
	}
	for _, ty := range []string{"choice", "multi_choice", "fill"} {
		if seenTypes[ty] != 1 {
			t.Errorf("type balance broken: %s appears %d times; want 1 (round-robin should rotate types)", ty, seenTypes[ty])
		}
	}
}

// TestSelectExam_PoolSmallerThanTarget 降级:题库只有 2 道,要 5 道 → 只能抽 2 道,
// 不报错。service 层用 len 判断是否够开考(gate)。守"有多少抽多少"契约。
func TestSelectExam_PoolSmallerThanTarget(t *testing.T) {
	pool := makePool(1, 100, []string{"choice", "fill"})
	sel := SelectExamQuestions(fixedRng(), pool, nil, 5, 0.4)
	if len(sel.Picked) != 2 {
		t.Errorf("degradation broken: want 2 (pool size); got %d", len(sel.Picked))
	}
}

// TestSelectExam_NoMasteriesDegradesToNeutral 无 mastery 记录(新学生/新课)时,
// 所有 chunk 权重 = 0.5 中性,不偏向任何 chunk。仍能正常抽题(按 chunk 出现顺序)。
// 守"无数据不崩"契约。
func TestSelectExam_NoMasteriesDegradesToNeutral(t *testing.T) {
	pool := append(makePool(1, 100, []string{"choice"}),
		makePool(10, 200, []string{"choice"})...)
	sel := SelectExamQuestions(fixedRng(), pool, nil, 2, 0.4)
	if len(sel.Picked) != 2 {
		t.Fatalf("want 2 picked with no masteries; got %d", len(sel.Picked))
	}
	// 无 mastery 时 WeakChunks 应为空(weight 0.5 不 > 0.5)。
	if len(sel.WeakChunks) != 0 {
		t.Errorf("no-mastery case should yield no WeakChunks (weight 0.5 not > 0.5); got %v", sel.WeakChunks)
	}
}

// TestSelectExam_WeakChunksReported 弱点 chunk 题库覆盖不足时,picked 不足 2 道
// 的弱点 chunk 进 WeakChunks,交给 caller 让 agent 新出迁移题。
func TestSelectExam_WeakChunksReported(t *testing.T) {
	// chunk100 弱点(0.1)但只有 1 道题;chunk200 弱点(0.2)有 3 道题。
	// 要 4 道题:chunk100 抽满(1道),chunk200 抽 3 道。chunk100 picked=1 < 2 → 进 WeakChunks。
	pool := append(makePool(1, 100, []string{"choice"}),
		makePool(10, 200, []string{"choice", "fill", "multi_choice"})...)
	masteries := []MasteryEntry{{100, 0.1}, {200, 0.2}}
	sel := SelectExamQuestions(fixedRng(), pool, masteries, 4, 0.4)
	if len(sel.Picked) != 4 {
		t.Fatalf("want 4 picked; got %d", len(sel.Picked))
	}
	found := false
	for _, c := range sel.WeakChunks {
		if c == 100 {
			found = true
		}
	}
	if !found {
		t.Errorf("chunk 100 (weak, under-covered) should be in WeakChunks; got %v", sel.WeakChunks)
	}
}

// TestSelectExam_EmptyPool 题池空时返回空结果,不 panic。守边界。
func TestSelectExam_EmptyPool(t *testing.T) {
	sel := SelectExamQuestions(fixedRng(), nil, nil, 5, 0.4)
	if len(sel.Picked) != 0 {
		t.Errorf("empty pool should yield 0 picked; got %d", len(sel.Picked))
	}
	if sel.CoveredIDs == nil {
		t.Error("CoveredIDs should be non-nil even when empty")
	}
}

// TestSelectExam_ZeroTarget targetCount=0 或负数时返回空,不 panic。守边界。
func TestSelectExam_ZeroTarget(t *testing.T) {
	pool := makePool(1, 100, []string{"choice"})
	sel := SelectExamQuestions(fixedRng(), pool, nil, 0, 0.4)
	if len(sel.Picked) != 0 {
		t.Errorf("targetCount=0 should yield 0 picked; got %d", len(sel.Picked))
	}
}

// TestSelectExam_DefaultWeakThreshold weakThreshold 传 0 或负数时用默认 0.4。
// 守默认值契约(对齐 advice agent 弱点带)。
func TestSelectExam_DefaultWeakThreshold(t *testing.T) {
	// 不直接断言阈值(它是内部权重计算),而是验证传 0 不崩 + 行为与合理阈值一致。
	pool := makePool(1, 100, []string{"choice"})
	masteries := []MasteryEntry{{100, 0.5}}
	sel := SelectExamQuestions(fixedRng(), pool, masteries, 1, 0)
	if len(sel.Picked) != 1 {
		t.Errorf("default threshold case should still pick 1; got %d", len(sel.Picked))
	}
}

// TestSelectExam_NoDuplicates 同一道题不会被抽两次(CoveredIDs 去重)。
// 即使某 chunk 题少、第二轮补量时也不会重复。
func TestSelectExam_NoDuplicates(t *testing.T) {
	// 单 chunk 2 道题,要 2 道。两轮都从同一 chunk 抽,但不能重复。
	pool := makePool(1, 100, []string{"choice", "fill"})
	sel := SelectExamQuestions(fixedRng(), pool, nil, 2, 0.4)
	if len(sel.Picked) != 2 {
		t.Fatalf("want 2; got %d", len(sel.Picked))
	}
	if sel.Picked[0].ID == sel.Picked[1].ID {
		t.Errorf("duplicate question %d picked twice", sel.Picked[0].ID)
	}
}

// TestSelectExam_SecondRoundFillsFromWeakest 第二轮补量从权重最高的 chunk 继续
// 抽(同 chunk 可多抽),而不是均匀分散。守"弱点优先"在补量阶段仍生效。
func TestSelectExam_SecondRoundFillsFromWeakest(t *testing.T) {
	// chunk100 弱(0.1)有 4 道题;chunk200 强(0.9)有 4 道。要 4 道:
	// 第一轮各抽 1(广覆盖);第二轮还有 2 个名额,从最弱 chunk100 继续抽。
	pool := append(makePool(1, 100, []string{"choice", "fill", "multi_choice", "choice"}),
		makePool(100, 200, []string{"choice", "fill", "multi_choice", "choice"})...)
	masteries := []MasteryEntry{{100, 0.1}, {200, 0.9}}
	sel := SelectExamQuestions(fixedRng(), pool, masteries, 4, 0.4)
	if len(sel.Picked) != 4 {
		t.Fatalf("want 4; got %d", len(sel.Picked))
	}
	// 弱点 chunk100 应至少 2 道(第一轮 1 + 第二轮补),强 chunk200 最多 2 道(第一轮 1)。
	weakCount, strongCount := 0, 0
	for _, q := range sel.Picked {
		if q.ChunkID == 100 {
			weakCount++
		} else if q.ChunkID == 200 {
			strongCount++
		}
	}
	if weakCount < 2 {
		t.Errorf("weakest chunk should get extra questions in round 2; weakCount=%d (want >=2)", weakCount)
	}
	if strongCount > 2 {
		t.Errorf("strong chunk should not exceed first-round coverage; strongCount=%d", strongCount)
	}
}
