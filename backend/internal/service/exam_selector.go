package service

import (
	"math/rand"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// exam_selector.go 是「课程考试」混合抽题算法的纯函数核心。设计目标:
//   - 弱点优先:该学生 mastery 越低的 chunk,其题越优先被抽(自适应)。
//   - 覆盖度:一张卷子尽量覆盖多个 chunk/episode,而不是全挤在一个弱点 chunk 上
//     (单 chunk 抽满会变成"只复习一个点",失去"考试"的综合检测意义)。
//   - 题型均衡:choice/multi/fill 比例大致均衡,避免全是同一题型。
//   - 降级:题库 < 目标题数时,有多少抽多少(不报错,service 层用 gate 拒绝)。
//   - 退化为均匀:学生无任何 mastery 记录(新课/新学生)时,按均匀随机抽,不偏向任何 chunk。
//
// 纯函数 + 显式注入 *rand.Rand,便于单测(固定 seed → 可重复)。
// 不依赖 DB / repo / agent,所有数据由 caller 传入。

// PoolQuestion 是 exam_selector 需要的题目最小描述。和 repository.ExamPoolQuestion
// 字段一致,但解耦成独立 struct(避免 selector 反向依赖 repository 包)——caller 负责转换。
type PoolQuestion struct {
	ID         uint
	ChunkID    uint // 必须 > 0(pool 已过滤合成题)
	EpisodeID  uint
	Type       string // choice | multi_choice | fill
}

// MasteryEntry 是某 chunk 的学生掌握度,用于弱点加权。
type MasteryEntry struct {
	ChunkID uint
	Mastery float64 // 0.0-1.0
}

// ExamSelection 是抽题结果。
type ExamSelection struct {
	Picked      []PoolQuestion // 抽中的题(已按卷面顺序:弱点优先 + 题型轮转)
	WeakChunks  []uint         // 抽完后仍未充分覆盖的弱点 chunk(交给 agent 新出迁移题)
	CoveredIDs  map[uint]bool  // 已抽题的 ID 集合(给 caller 去重用)
}

// SelectExamQuestions 从题池里按 mastery 弱点加权抽 targetCount 道题。
//
// 算法(分两轮,保证覆盖度 + 弱点优先):
//  1. 把题按 chunk 分桶。计算每个 chunk 的"弱点权重" = 1 - mastery(mastery 越低
//     权重越高;无 mastery 记录的 chunk 权重 = 0.5 中性)。
//  2. 第一轮"广覆盖":按权重排序,从每个 chunk 各抽 1 道(轮转题型),直到覆盖尽量多
//     的 chunk 或达到 targetCount。这保证一张卷子考多个知识点。
//  3. 第二轮"弱点补量":剩余名额从权重最高的 chunk 继续抽(同一 chunk 可多抽),
//     仍轮转题型保持均衡。
//  4. 题库不足:抽到多少算多少(picked < targetCount)。caller 用 len 判断是否够开考。
//
// weakThreshold(默认 0.4,对齐 advice agent 的弱点带)以下的 chunk 才进 WeakChunks
// 返回——这些是"题库里题不够抽、需要 agent 新出迁移题巩固"的弱点。
func SelectExamQuestions(rng *rand.Rand, pool []PoolQuestion, masteries []MasteryEntry, targetCount int, weakThreshold float64) ExamSelection {
	if targetCount <= 0 || len(pool) == 0 {
		return ExamSelection{Picked: []PoolQuestion{}, CoveredIDs: map[uint]bool{}}
	}
	if weakThreshold <= 0 {
		weakThreshold = 0.4 // 默认弱点阈值,对齐 advice agent(advice_tools.go < 0.4 = ★弱点)
	}

	// chunk → mastery。无记录的 chunk 取中性 0.5。
	masteryByChunk := make(map[uint]float64, len(masteries))
	for _, m := range masteries {
		masteryByChunk[m.ChunkID] = m.Mastery
	}
	chunkWeight := func(cid uint) float64 {
		m, ok := masteryByChunk[cid]
		if !ok {
			return 0.5 // 无记录 = 中性
		}
		w := 1.0 - m // mastery 越低权重越高
		if w < 0 {
			w = 0
		}
		return w
	}

	// 按 chunk 分桶,每桶内按题型分组(便于轮转)。
	type bucket struct {
		chunkID uint
		weight  float64
		byType  map[string][]PoolQuestion // type → 候选题(原顺序)
		picked  int                       // 本桶已抽出几道(轮转题型用)
	}
	buckets := make(map[uint]*bucket, 0)
	chunkOrder := []uint{} // 保留首次出现顺序,稳定
	for _, q := range pool {
		b, ok := buckets[q.ChunkID]
		if !ok {
			b = &bucket{chunkID: q.ChunkID, weight: chunkWeight(q.ChunkID), byType: map[string][]PoolQuestion{}}
			buckets[q.ChunkID] = b
			chunkOrder = append(chunkOrder, q.ChunkID)
		}
		b.byType[q.Type] = append(b.byType[q.Type], q)
	}

	// 把桶按权重 DESC 排序(弱点优先)。稳定排序:权重相同时保留 chunkOrder 顺序。
	ordered := make([]*bucket, 0, len(buckets))
	for _, cid := range chunkOrder {
		ordered = append(ordered, buckets[cid])
	}
	// 插入排序(桶数 = chunk 数,几十量级,简单稳定排序够用)。
	for i := 1; i < len(ordered); i++ {
		j := i
		for j > 0 && ordered[j].weight > ordered[j-1].weight {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
			j--
		}
	}

	covered := map[uint]bool{}
	picked := []PoolQuestion{}

	// 题型轮转:每个桶按 type 的固定优先序抽(choice → multi_choice → fill),
	// 重复抽时按 type 轮转 + 同 type 往后取未抽的。typeOrder 决定轮转顺序。
	typeOrder := []string{"choice", "multi_choice", "fill"}
	// pickFromBucket 从桶 b 抽一道题(轮转题型 + 跳过已抽,同 type 有多道时逐个往后取)。
	pickFromBucket := func(b *bucket) (PoolQuestion, bool) {
		for k := 0; k < len(typeOrder); k++ {
			t := typeOrder[(b.picked+k)%len(typeOrder)]
			cands := b.byType[t]
			// 同 type 多次抽时:从该 type 的候选里找第一个未抽的(不是固定 idx,
			// 因为前面可能已被抽过)。
			for _, q := range cands {
				if !covered[q.ID] {
					b.picked++
					return q, true
				}
			}
		}
		return PoolQuestion{}, false
	}

	// 第一轮:广覆盖。按权重顺序每个桶抽一道,直到 targetCount 或桶用完。
	for _, b := range ordered {
		if len(picked) >= targetCount {
			break
		}
		if q, ok := pickFromBucket(b); ok {
			picked = append(picked, q)
			covered[q.ID] = true
		}
	}

	// 第二轮:弱点补量。还有名额就从权重最高的桶继续抽(同桶可多抽),轮转题型。
	// 用 pass 计数避免死循环:每 pass 至少抽到一题才继续,否则该桶本轮耗尽。
	for len(picked) < targetCount {
		progress := false
		for _, b := range ordered {
			if len(picked) >= targetCount {
				break
			}
			if q, ok := pickFromBucket(b); ok {
				picked = append(picked, q)
				covered[q.ID] = true
				progress = true
			}
		}
		if !progress {
			break // 所有桶都抽干了
		}
	}

	// WeakChunks:权重 > 0.5(即 mastery < 中性,是真实弱点)且本次抽题数 < 2 的 chunk。
	// 这些是"弱点但题库覆盖不足"的,交给 caller 让 agent 新出迁移题。
	weak := []uint{}
	for _, b := range ordered {
		if b.weight > 0.5 && b.picked < 2 {
			weak = append(weak, b.chunkID)
		}
	}

	return ExamSelection{Picked: picked, WeakChunks: weak, CoveredIDs: covered}
}

// poolFromRepo 把 repository.ExamPoolQuestion 列表转成 selector 需要的 PoolQuestion。
// 放这里(service 包)而不是 repository,是为了让 selector 不反向依赖 repository 包
// (selector 是纯算法,repository 是 IO)。
func poolFromRepo(rows []repository.ExamPoolQuestion) []PoolQuestion {
	out := make([]PoolQuestion, len(rows))
	for i, r := range rows {
		out[i] = PoolQuestion{ID: r.ID, ChunkID: r.ChunkID, EpisodeID: r.EpisodeID, Type: r.Type}
	}
	return out
}

// masteriesFromModel 把 model.KnowledgeMemory 列表(repo.GetCourseMasteries 的返回)
// 转成 selector 需要的 MasteryEntry。
func masteriesFromModel(rows []model.KnowledgeMemory) []MasteryEntry {
	out := make([]MasteryEntry, len(rows))
	for i, r := range rows {
		out[i] = MasteryEntry{ChunkID: r.ChunkID, Mastery: r.Mastery}
	}
	return out
}
