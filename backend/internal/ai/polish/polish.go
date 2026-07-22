// Package polish is the subtitle-homophone-correction PoC for PR2 of the
// subtitle-system overhaul.
//
// It takes a VTT subtitle string + a course TermDict + a subject, runs the
// subtitles through an LLM in chunks (150 cues each, 3-cue overlap, 3-way
// concurrency) and returns ONLY a diff of what to change — never a full
// rewrite. Timestamps are physically impossible for the model to corrupt
// because they are never sent in the prompt: only {id, text} pairs go in,
// and the model returns {id, text} pairs back. The backend re-applies each
// change to the corresponding cue by id, preserving start/end ms exactly.
//
// This is a PoC package — the production wiring (DB persistence, source-based
// gating, glossary_candidates table) lives elsewhere and is implemented in
// a later PR. Here we focus on validating the LLM-prompt + chunking +
// validation + reassembly pipeline against real data.
package polish

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/subtitle"
)

// --- public types --------------------------------------------------------

// PolishRequest is the input to Polish.
type PolishRequest struct {
	VttContent string // the raw WebVTT subtitle to polish
	TermDict   string // Course.EffectiveTermDict(subject) — injected into prompt
	Subject    string // e.g. "象棋" / "数学" — sets domain context
	// SkipGlossaryMining 关闭术语挖矿：true 时用精简系统提示词（无 glossary 段），
	// 模型不再输出 glossary。re-polish 场景（source=llm_optimized）下字幕内容未变，
	// 上次已挖过术语，再挖既白烧 token 也分散注意力。runPolishJob 用 sub.Source
	// == "llm_optimized" 判断。
	SkipGlossaryMining bool

	// --- checkpoint / resume (断点续润) -----------------------------------
	// These three fields let runPolishJob resume an interrupted run instead of
	// re-burning tokens on chunks that already succeeded. All optional: nil/empty
	// means a fresh full run (the default for tests + first polish).
	//
	// PriorOutcomes carries the results of chunks that already completed in a
	// prior attempt of THIS job. Polish treats them as pre-resolved: it does NOT
	// call the LLM for them, but DOES include their changes/glossary/tokens in
	// the final reassembly + stats. Keyed by chunk index (0-based, matches the
	// deterministic chunk layout for a given VttContent).
	PriorOutcomes map[int]PriorChunkOutcome
	// OnChunkDone is invoked (from inside the per-chunk goroutine) once a chunk
	// finishes — successfully or after exhausting retries. The service layer uses
	// it to checkpoint the chunk to ai_polish_chunks so a crash/retry doesn't
	// lose it. nil = no checkpointing (tests). The callback MUST NOT block long
	// (it's inside the concurrency-limited goroutine) and MUST NOT panic-die the
	// pipeline (panics propagate to the Polish caller).
	OnChunkDone func(idx int, oc ChunkOutcome)
}

// PriorChunkOutcome is one chunk's persisted result fed back into Polish for
// resume. It's the serialized form of a chunkOutcome (the in-memory type has
// unexported fields). Service builds these from ai_polish_chunks rows.
type PriorChunkOutcome struct {
	Changes           []CueChange
	Glossary          []GlossaryCandidate
	PromptTokens      int
	CompletionTokens  int
	HighEditDistance  int
}

// ChunkOutcome is the exported per-chunk result passed to OnChunkDone. Mirrors
// the unexported chunkOutcome; service persists it to ai_polish_chunks.
type ChunkOutcome struct {
	Idx             int
	Changes         []CueChange
	Glossary        []GlossaryCandidate
	PromptTokens    int
	CompletionTokens int
	Failed          bool
	Retries         int
	HighEditDistance int
	ErrStr          string
}

// PolishResult is the output of Polish.
type PolishResult struct {
	PolishedVtt string            // rewritten VTT (timestamps preserved, texts per diff)
	Diff        []CueDiff         // every change that was applied, in cue order
	Glossary    []GlossaryCandidate // mined term candidates, deduped
	Stats       PolishStats
}

// CueDiff is one applied change to one cue.
type CueDiff struct {
	ID     int    `json:"id"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// GlossaryCandidate is a mined term-correction rule the LLM surfaced while
// polishing. confidence in [0,1]; only >= 0.7 are reported per the prompt.
type GlossaryCandidate struct {
	Original    string  `json:"original"`
	Corrected   string  `json:"corrected"`
	Context     string  `json:"context,omitempty"`
	Confidence  float64 `json:"confidence"`
	EvidenceIDs []int   `json:"evidence_ids,omitempty"`
}

// PolishStats accumulates cost/timing/retry telemetry for one Polish run.
type PolishStats struct {
	TotalCues        int           `json:"total_cues"`
	ChangedCues      int           `json:"changed_cues"`
	ChunkCount       int           `json:"chunk_count"`
	// LLMCalls counts chunks that PRODUCED A VALID RESPONSE this run, plus
	// prior-done chunks resumed from checkpoint (each counts as one). It is NOT
	// the raw HTTP request count — a chunk that retried twice (2 extra HTTP
	// calls) before succeeding bumps Retries, not LLMCalls. Failed chunks that
	// never produced valid output aren't counted here either (their tokens are
	// still in PromptTokens/CompletionTokens, since they billed regardless).
	// Read it as "how many chunks yielded usable output", not "API request count".
	LLMCalls         int           `json:"llm_calls"`
	FailedChunks     int           `json:"failed_chunks"` // chunks that exhausted retries
	Retries          int           `json:"retries"` // total retry attempts across chunks
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
	Duration         time.Duration `json:"duration"`
	PartialOptimized bool          `json:"partial_optimized"` // true if any chunk failed
	// HighEditDistanceCount is the number of applied changes whose edit-distance
	// ratio exceeds the soft warning threshold (length delta > 5 OR punctuation
	// changed OR Levenshtein/maxLen > 0.5). These changes are STILL APPLIED —
	// this is informational only, surfaced in the job detail so the admin knows
	// how many cues to spot-check in the subtitle diff UI. The whole point of
	// the relaxed validation (2026-07-21) is that we trust the LLM's corrections
	// by default and let humans review questionable ones via the diff view,
	// rather than the previous strict rules that rejected legitimate homophone
	// fixes on short cues (考算→口算, 合不变→和不变, etc.).
	HighEditDistanceCount int `json:"high_edit_distance_count"`
	// SkippedEdits counts find/replace edits that were dropped because their
	// find substring was absent or not unique in the cue. Informational only:
	// the rest of the change (other edits, or the Text fallback) still applies.
	// A non-zero number means the model's find was imprecise, not that polish
	// failed. Surfaced so the admin can tell imprecise finds apart from "the
	// model didn't try to fix this cue".
	SkippedEdits int `json:"skipped_edits"`
	// FailedChunkErrors maps failed chunk index → its last error string. Empty
	// unless PartialOptimized. Surfaced to the admin job detail so polish
	// failures are actionable (JSON parse vs network). Note: after the
	// validation relaxation, "validation reject" no longer exists as a failure
	// mode — only parse/network/unknown-id structural failures remain.
	// One entry per failed chunk; chunks that succeeded are absent.
	FailedChunkErrors map[int]string `json:"failed_chunk_errors,omitempty"`
}

// --- chunking tunables (from the design doc §2.2) -----------------------

const (
	chunkSize    = 150 // cues per chunk
	chunkOverlap = 3   // overlap cues between adjacent chunks
	concurrency  = 3   // in-flight LLM calls (user-imposed, relays limit hard)
	// maxRetries is attempts per chunk before giving up. Was 3; dropped to 2
	// (2026-07-22): the 3rd retry almost never helps (relay garbage repeats,
	// unknown-id drift repeats) and just burns billed tokens. With checkpoint/
	// resume, a chunk that exhausts retries fails the job but its done siblings
	// survive — so a failed chunk is cheap to retry on demand rather than paying
	// for a near-useless 3rd attempt inline.
	maxRetries = 2
	maxTokens  = 8000
)

// ChunkLayout returns the global cue-index span [first, last] (inclusive) of
// each chunk Polish would produce for numCues cues. Exposed so the service
// layer can seed ai_polish_chunks rows with the same boundaries Polish will
// use (the two MUST agree, or a chunk's persisted result won't line up with
// the cue index it's replayed against on resume). Mirrors the chunking math
// inside Polish exactly — if you change one, change the other.
func ChunkLayout(numCues int) [][2]int {
	step := chunkSize - chunkOverlap
	var out [][2]int
	for startIdx := 0; startIdx < numCues; startIdx += step {
		end := startIdx + chunkSize
		if end > numCues {
			end = numCues
		}
		out = append(out, [2]int{startIdx, end - 1})
		if end == numCues {
			break
		}
	}
	return out
}

// --- the entry point -----------------------------------------------------

// Polish runs the full pipeline:
//  1. VttToSrt + ParseSRT → flat cue list
//  2. chunk into 150-cue windows with 3-cue overlap
//  3. concurrently (3-way) call the LLM per chunk, retry failed chunks (see
//     maxRetries); chunks present in req.PriorOutcomes are SKIPPED (resume)
//  4. validate every returned change (only unknown cue id rejects+retries;
//     length/punctuation just flag HighEditDistance, applied anyway — the
//     2026-07-21 relaxation)
//  5. apply validated changes back to the cues (timestamps untouched)
//  6. reassemble SRT → VTT
//
// Timestamps are guaranteed byte-identical: the prompt never contains them,
// and reassembly rebuilds the SRT from the parsed cues' StartMs/EndMs.
//
// Checkpoint/resume (断点续润): if req.PriorOutcomes is non-empty, those chunks
// are NOT re-called — their changes/glossary/tokens are folded straight into
// the reassembly + stats. req.OnChunkDone (if set) fires per finished chunk so
// the caller can persist it; that's how a retry of a partially-failed job
// avoids re-burning tokens on the chunks that already succeeded.
func Polish(ctx context.Context, llm ai.LLMProvider, model string, req PolishRequest) (*PolishResult, error) {
	start := time.Now()
	if llm == nil {
		return nil, fmt.Errorf("polish: llm provider is nil")
	}

	// 1. Parse VTT → SRT → cues.
	srt := subtitle.VttToSrt(req.VttContent)
	cues, err := ai.ParseSRT(srt)
	if err != nil {
		return nil, fmt.Errorf("polish: parse subtitle: %w", err)
	}
	if len(cues) == 0 {
		return nil, fmt.Errorf("polish: zero cues parsed")
	}

	// 2. Chunk with overlap. Chunk i spans [i*step, i*step+chunkSize).
	// step = chunkSize - overlap so each chunk shares `overlap` cues with
	// the next, giving the model cross-boundary context without duplicating
	// work (changes for overlap cues are deduped at apply time by last-write
	// — the later chunk sees more right-side context).
	step := chunkSize - chunkOverlap
	chunks := make([][]cueRef, 0, len(cues)/step+1)
	for startIdx := 0; startIdx < len(cues); startIdx += step {
		end := startIdx + chunkSize
		if end > len(cues) {
			end = len(cues)
		}
		blk := make([]cueRef, 0, end-startIdx)
		for i := startIdx; i < end; i++ {
			blk = append(blk, cueRef{GlobalIdx: i, Cue: cues[i]})
		}
		chunks = append(chunks, blk)
		if end == len(cues) {
			break
		}
	}

	// 3. Concurrent LLM calls with a 3-way semaphore.
	sem := make(chan struct{}, concurrency)
	outcomes := make([]chunkOutcome, len(chunks))

	// Resume (断点续润): fold in any prior-completed chunks BEFORE the loop so
	// the loop only fires goroutines for chunks still needing LLM work. Their
	// tokens/changes/glossary count toward the final stats + reassembly exactly
	// as if they'd run this turn — that's the whole point (no re-burn).
	priorDone := make(map[int]bool, len(req.PriorOutcomes))
	// priorPrompt/priorCompletion accumulate the prior chunks' tokens so they're
	// folded into the run's totals — the tokens WERE spent (on the prior attempt),
	// so the run's cost stat must reflect them. Counted via atomics below.
	var priorPrompt, priorCompletion, priorLLMCalls int32
	for idx, p := range req.PriorOutcomes {
		if idx < 0 || idx >= len(chunks) {
			continue // stale chunk index from a different VttContent shape — ignore
		}
		outcomes[idx] = chunkOutcome{
			idx:              idx,
			changes:          p.Changes,
			glossary:         p.Glossary,
			promptTokens:     p.PromptTokens,
			completionTokens: p.CompletionTokens,
			highEditDistance: p.HighEditDistance,
		}
		priorDone[idx] = true
		priorPrompt += int32(p.PromptTokens)
		priorCompletion += int32(p.CompletionTokens)
		priorLLMCalls++ // a prior-done chunk counts as one completed LLM call
	}

	var wg sync.WaitGroup

	var totalPrompt, totalCompletion, totalLLMCalls, totalRetries, failedChunks int32
	// Seed the run totals with prior chunks' tokens + call count. Done AFTER the
	// var decls (atomics need to exist before we Add into them). The chunk loop
	// below only adds newly-run chunks on top.
	atomic.AddInt32(&totalPrompt, priorPrompt)
	atomic.AddInt32(&totalCompletion, priorCompletion)
	atomic.AddInt32(&totalLLMCalls, priorLLMCalls)

	for i, blk := range chunks {
		if priorDone[i] {
			continue // resume: this chunk already has an outcome, skip the LLM call
		}
		wg.Add(1)
		go func(i int, blk []cueRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var (
				parsed            *polishResponse
				pErr              error
				// promptAccum/completionAccum 累计本 chunk 所有 HTTP 成功 attempt 的
				// token（含最终被 validation 拒掉的那些）。之前用一个 `usage` 变量在
				// 每次 retry 时覆盖，会把前面 attempt 的 token 丢掉，导致实际花费被
				// 漏算少计（账单比 PolishStats 显示的更高）。改成累加后统计才反映真实账单。
				promptAccum       int
				completionAccum   int
				retries           int
				chunkHighEditDist int
			)
			// Retry loop: re-call the LLM until the response parses AND passes
			// structural validation. Since the 2026-07-21 relaxation, the only
			// validation failures that trigger retry are STRUCTURAL (JSON parse
			// error, unknown cue id) — suspicious-but-in-range text no longer
			// fails, it just increments chunkHighEditDist. So a well-formed
			// response with legit homophone fixes succeeds on attempt 1; only
			// genuinely broken responses (relay garbage, lost id alignment)
			// burn retries.
			// Labeled break (`break retryLoop`) is required because a plain
			// `break` inside the select would only exit the select, not the
			// for — and we'd keep re-calling polishChunk on a cancelled ctx
			// until maxRetries ran out.
		retryLoop:
			for attempt := 0; attempt < maxRetries; attempt++ {
				if attempt > 0 {
					retries++
					// small backoff so we don't hammer a rate-limited relay
					select {
					case <-time.After(time.Duration(attempt) * time.Second):
					case <-ctx.Done():
						pErr = ctx.Err()
						break retryLoop
					}
				}
				resp, callErr := polishChunk(ctx, llm, model, blk, req.TermDict, req.Subject, req.SkipGlossaryMining)
				if callErr != nil {
					pErr = callErr
					continue
				}
				// HTTP 成功就要算账 —— 即使下一步 validation 会拒掉这次结果，token
				// 也已经被计费了。累加而不是覆盖（见 promptAccum 注释）。
				promptAccum += resp.usage.PromptTokens
				completionAccum += resp.usage.CompletionTokens
				hed, validateErr := validateChanges(blk, resp.changes)
				if validateErr != nil {
					pErr = validateErr
					continue
				}
				chunkHighEditDist = hed
				parsed = &polishResponse{changes: resp.changes, glossary: resp.glossary}
				pErr = nil
				break
			}

			oc := chunkOutcome{idx: i, retries: retries, highEditDistance: chunkHighEditDist}
			if parsed != nil {
				oc.changes = parsed.changes
				oc.glossary = parsed.glossary
				oc.promptTokens = promptAccum
				oc.completionTokens = completionAccum
				atomic.AddInt32(&totalLLMCalls, 1)
			} else {
				oc.failed = true
				atomic.AddInt32(&failedChunks, 1)
				// pErr holds the last error from the retry loop (LLM error,
				// JSON parse error, or structural validation rejection). Stash
				// it so the caller can surface it in the job detail — without
				// this the admin sees "N chunks failed" with zero context.
				if pErr != nil {
					oc.errStr = pErr.Error()
				} else {
					oc.errStr = "unknown"
				}
				oc.changes = nil
			}
			atomic.AddInt32(&totalRetries, int32(retries))
			// 用累加值落账（覆盖了 retry 间被丢掉的 token）。失败的 chunk 也加：
			// 它们同样消耗了 token（HTTP 成功但 validation 失败），账单不会因为
			// 我们标记 failed 就免单。
			atomic.AddInt32(&totalPrompt, int32(promptAccum))
			atomic.AddInt32(&totalCompletion, int32(completionAccum))
			outcomes[i] = oc
			// Checkpoint hook: let the caller persist this chunk so a crash/retry
			// of the job resumes from here instead of re-burning tokens. Runs
			// inside the per-chunk goroutine (holding the semaphore) — the caller's
			// impl (service MarkChunkDone) is a quick single-row upsert, fine to
			// inline. Guarded so a nil callback (tests / no checkpointing) is free.
			if req.OnChunkDone != nil {
				req.OnChunkDone(i, ChunkOutcome{
					Idx:              oc.idx,
					Changes:          oc.changes,
					Glossary:         oc.glossary,
					PromptTokens:     oc.promptTokens,
					CompletionTokens: oc.completionTokens,
					Failed:           oc.failed,
					Retries:          oc.retries,
					HighEditDistance: oc.highEditDistance,
					ErrStr:           oc.errStr,
				})
			}
		}(i, blk)
	}
	wg.Wait()

	// 4. Apply changes to a map keyed by global cue index. Last write wins
	// (the later chunk has more right-side context and is preferred). We also
	// record before/after for the diff. Validation already ran per chunk;
	// we additionally dedup glossary entries here.
	//
	// find/replace (2026-07-22): each change resolves to its final text via
	// CueChange.resolveText (edits applied with uniqueness check, or Text
	// fallback). We count how many edits got skipped (find not unique/absent)
	// into skippedEdits so the admin can tell the model's find/replace wasn't
	// precise — not a correctness gate, the rest of the change still applies.
	editedText := make(map[int]string, len(cues))
	var skippedEdits int
	for _, oc := range outcomes {
		for _, ch := range oc.changes {
			// ch.ID is the chunk-local 1-based id → resolve to global idx.
			// (We sent chunk-local ids in the prompt so ids stay small and
			// dense, which models handle reliably.)
			if ch.ID < 1 || ch.ID > len(chunks[oc.idx]) {
				continue
			}
			globalIdx := chunks[oc.idx][ch.ID-1].GlobalIdx
			final, _, skipped, _ := ch.resolveText(cues[globalIdx].Text)
			skippedEdits += skipped
			editedText[globalIdx] = final
		}
	}

	// 5. Reassemble. We rebuild the SRT from the parsed cues so timestamps
	// come straight from StartMs/EndMs (never from the model).
	var changed int
	diff := make([]CueDiff, 0, len(editedText))
	for i, c := range cues {
		newText, ok := editedText[i]
		if !ok {
			continue
		}
		// Apply every LLM-returned change that actually differs from the
		// original. The previous design re-checked changeAllowed here as
		// defense-in-depth; that gate is gone (2026-07-21 relaxation), so the
		// only filter left is "is it actually a change". Recording the diff
		// unconditionally lets the subtitle version UI show every modification
		// for admin review — which is now the primary quality control, not
		// the validation rules.
		if newText != c.Text {
			changed++
			diff = append(diff, CueDiff{
				ID:     i + 1, // 1-based to match SRT cue numbering
				Before: c.Text,
				After:  newText,
			})
			cues[i].Text = newText
		}
	}

	polishedSRT := cuesToSRT(cues)
	polishedVTT := subtitle.SrtToVtt(polishedSRT)

	// Dedup glossary across chunks by (Original, Corrected) — take the
	// highest confidence and merge evidence ids.
	glossary := dedupGlossary(collectGlossary(outcomes))

	// Collect failed-chunk error strings (only when some chunk failed, so the
	// map is nil/empty on the common all-success path — keeps JSON output clean).
	var failedErrs map[int]string
	var totalHighEditDistance int
	for _, oc := range outcomes {
		if oc.failed {
			if failedErrs == nil {
				failedErrs = make(map[int]string)
			}
			failedErrs[oc.idx] = oc.errStr
		}
		totalHighEditDistance += oc.highEditDistance
	}

	stats := PolishStats{
		TotalCues:             len(cues),
		ChangedCues:           changed,
		ChunkCount:            len(chunks),
		LLMCalls:              int(atomic.LoadInt32(&totalLLMCalls)),
		FailedChunks:          int(atomic.LoadInt32(&failedChunks)),
		Retries:               int(atomic.LoadInt32(&totalRetries)),
		PromptTokens:          int(atomic.LoadInt32(&totalPrompt)),
		CompletionTokens:      int(atomic.LoadInt32(&totalCompletion)),
		Duration:              time.Since(start),
		PartialOptimized:      atomic.LoadInt32(&failedChunks) > 0,
		HighEditDistanceCount: totalHighEditDistance,
		SkippedEdits:          skippedEdits,
		FailedChunkErrors: failedErrs,
	}

	return &PolishResult{
		PolishedVtt: polishedVTT,
		Diff:        diff,
		Glossary:    glossary,
		Stats:       stats,
	}, nil
}

// --- per-chunk call ------------------------------------------------------

// cueRef carries a parsed cue along with its original global index in the
// episode, so we can re-map chunk-local ids back to global cue positions.
type cueRef struct {
	GlobalIdx int
	Cue       ai.SRTCue
}

// chunkOutcome is one chunk's resolved result after the retry loop. Promoted
// to package-level (not inline in Polish) so the glossary collector helper
// can reference the same type.
type chunkOutcome struct {
	idx      int
	changes  []CueChange
	glossary []GlossaryCandidate
	// promptTokens/completionTokens 是本 chunk 累计 token（含被 validation 拒掉的
	// attempt）。改为字段名（不再用 ai.Usage）是断点续润时 per-chunk 落库所需 ——
	// AIPolishChunk 行要存自己的 token，而累计值天然就在 outcome 上。usage 只在
	// polishChunk 返回时短暂存在（per-attempt），不该穿透到 outcome。
	promptTokens     int
	completionTokens int
	failed           bool
	retries  int
	// highEditDistance is the count of changes in this chunk flagged as
	// suspicious by validateChanges (length delta > maxLenDelta, punctuation
	// changed, or Levenshtein ratio > 0.5). The changes are still applied;
	// this count is summed into PolishStats.HighEditDistanceCount so the
	// admin knows how many cues to spot-check in the diff UI.
	highEditDistance int
	// errStr is the last error from the retry loop when failed=true. Surface
	// this up to the job detail so admin can tell JSON parse failure / network
	// error / unknown-id structural failure apart — polish runs 7-13 minutes
	// and "N chunks failed" with no cause is unactionable.
	errStr string
}

// CueChange is one LLM-returned change in the chunk-local id space.
//
// Two output shapes are accepted (the prompt tells the model to prefer Edits):
//
//	{"id":147,"edits":[{"find":"出军","replace":"出车"}]}   // find/replace 子串
//	{"id":147,"text":"修正后的整句"}                        // 整句（fallback）
//
// Edits 优先：有 Edits 就按子串替换处理，忽略 Text；没有 Edits 才用 Text。保留
// Text 是为了向后兼容（老模型/格式漂移）—— 也是现有测试的返回格式。find 的唯一性
// 由 apply 层兜底（strings.Count != 1 时跳过该 edit 并计入 SkippedEdits），不 retry
// （retry 会原样返回浪费配额）。
type CueChange struct {
	ID    int       `json:"id"`
	Text  string    `json:"text,omitempty"`
	Edits []CueEdit `json:"edits,omitempty"`
}

// CueEdit is one find/replace pair within a cue. Find must be a unique substring
// of the cue's original text (apply layer enforces uniqueness, else the edit is
// dropped — see CueChange doc).
type CueEdit struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

// resolveText applies this change to the cue's original text and returns the
// final text. hasEdits reports whether Edits drove the change (vs a Text fall-
// back). applyUniqueEdits=1 means every edit was uniquely located (the common
// case); <1 means at least one edit was dropped for being absent/ambiguous.
//
// This centralizes the edits-vs-text resolution so validateChanges and apply
// can't drift apart (both compute the final text the same way).
func (c CueChange) resolveText(orig string) (final string, appliedEdits, skippedEdits int, hasEdits bool) {
	if len(c.Edits) > 0 {
		hasEdits = true
		final = orig
		for _, e := range c.Edits {
			if e.Find == "" || strings.Count(final, e.Find) != 1 {
				// 找不到 / 多次出现（不唯一）→ 跳过该 edit，不替换。比 retry 便宜：
				// retry 模型大概率原样返回同样的 find。
				skippedEdits++
				continue
			}
			final = strings.Replace(final, e.Find, e.Replace, 1)
			appliedEdits++
		}
		return final, appliedEdits, skippedEdits, true
	}
	// Text fallback：整句替换。allowed iff 与 orig 不同（apply 层再判一次）。
	return c.Text, 0, 0, false
}

// polishResponse is the LLM's expected JSON output for one chunk.
type polishResponse struct {
	changes  []CueChange
	glossary []GlossaryCandidate
}

type callResult struct {
	changes  []CueChange
	glossary []GlossaryCandidate
	usage    ai.Usage
}

// polishChunk builds the prompt for one chunk and calls the LLM once. Returns
// a parsed polishResponse plus the token usage from the call. skipMining picks
// the glossary-free system prompt (see SystemPrompt).
func polishChunk(ctx context.Context, llm ai.LLMProvider, model string, blk []cueRef, termDict, subject string, skipMining bool) (*callResult, error) {
	sys := SystemPrompt(skipMining)
	user := buildUserPrompt(blk, termDict, subject)

	resp, err := llm.Chat(ctx, ai.ChatRequest{
		Model:       model,
		Temperature: 0,
		MaxTokens:   maxTokens,
		Messages: []ai.ChatMessage{
			{Role: ai.RoleSystem, Content: sys},
			{Role: ai.RoleUser, Content: user},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("llm chat: %w", err)
	}
	parsed, err := parsePolishJSON(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("parse polish json: %w (raw: %.300s)", err, resp.Content)
	}
	return &callResult{
		changes:  parsed.Changes,
		glossary: parsed.Glossary,
		usage:    resp.Usage,
	}, nil
}

// buildUserPrompt renders the chunk as a JSON document the model can parse.
// We send chunk-local 1-based ids (dense, small) — the caller re-maps them
// to global cue indices at apply time via the chunk's cueRef table.
func buildUserPrompt(blk []cueRef, termDict, subject string) string {
	type item struct {
		ID   int    `json:"id"`
		Text string `json:"text"`
	}
	items := make([]item, len(blk))
	for i, c := range blk {
		items[i] = item{ID: i + 1, Text: c.Cue.Text}
	}
	subs, _ := json.Marshal(items)

	// Build the request object by hand so the structure is stable and
	// ordered (term_dict → subject → subtitles). Models do better with a
	// fixed shape.
	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("  \"term_dict\": ")
	td, _ := json.Marshal(termDict)
	b.Write(td)
	b.WriteString(",\n")
	b.WriteString("  \"subject\": ")
	sb, _ := json.Marshal(subject)
	b.Write(sb)
	b.WriteString(",\n")
	b.WriteString("  \"subtitles\": ")
	b.Write(subs)
	b.WriteString("\n}")
	return b.String()
}

// --- validation ----------------------------------------------------------

// maxLenDelta is the hard ceiling on |Δrune length| between orig and corrected
// text. Changes beyond this are considered structural corruption (the LLM lost
// track of what it was doing) and STILL get applied — but they count toward
// HighEditDistanceCount so the admin can spot-check them. The previous design
// (threshold 2 + charOverlap ≥ 0.6) rejected these outright; the relaxed design
// (2026-07-21) applies everything and defers judgement to the subtitle diff UI.
//
// Why 5: legitimate homophone/terminology corrections change 1-3 characters
// (车/炮/马/被减数/位值原理...), well within ±2. A correction that adds or
// removes a short phrase (5 runes) is unusual but plausible for a transcription
// fix; beyond 5 the LLM is almost certainly rewriting, not correcting. 5 is
// the "this is definitely suspicious" line — not a correctness gate.
const maxLenDelta = 5

// highEditDistanceRatio is the Levenshtein/maxLen ratio above which a change
// is counted as "high edit distance" (informational warning). 0.5 means "at
// least half the cue was rewritten". Real homophone fixes sit at 0.1-0.3;
// hallucinated rewrites sit near 1.0. Used only for stats — never for blocking.
const highEditDistanceRatio = 0.5

// validateChanges checks structural integrity of a chunk's returned changes
// and counts how many are "suspicious" (high edit distance). It returns:
//   - highEditDistance: count of changes whose resolved final text exceeds the
//     soft warning threshold (length delta > maxLenDelta, punctuation changed,
//     or Levenshtein ratio > highEditDistanceRatio). These are STILL applied —
//     just flagged.
//   - error: non-nil ONLY for structural corruption that warrants a retry:
//     an id outside the chunk's cue range, OR a change that resolves to empty
//     text (no text + no usable edits — see the empty-text guard below). Both
//     mean the LLM lost something; retrying may help. Rejecting suspicious-but-
//     in-range text does not (the LLM will just return the same correction).
//
// Design shift (2026-07-21): the previous version rejected length/punctuation
// violations and retried. This caused legitimate homophone fixes on short cues
// to fail repeatedly (考算→口算 has |Δ|=0 and same punctuation but the old
// charOverlap gate killed it). Now we trust the LLM's corrections by default
// and let humans review questionable ones via the subtitle diff UI.
//
// find/replace (2026-07-22): changes may carry Edits (find/replace pairs) or a
// Text field (whole-sentence). We resolve the final text via CueChange.
// resolveText — which applies edits with uniqueness checks — so the high-edit-
// distance check sees the SAME final text the apply step will produce. An empty
// resolved text (bare id, or edits that all missed) is treated as structural
// corruption and retried — applying it would blank the cue's subtitle text.
func validateChanges(blk []cueRef, changes []CueChange) (highEditDistance int, err error) {
	// Build a set of valid ids and the original text for each.
	byID := make(map[int]string, len(blk))
	for i, c := range blk {
		byID[i+1] = c.Cue.Text
	}
	for _, ch := range changes {
		orig, ok := byID[ch.ID]
		if !ok {
			return 0, fmt.Errorf("unknown id %d in changes (chunk has %d cues)", ch.ID, len(blk))
		}
		final, _, _, _ := ch.resolveText(orig)
		// Empty resolved text = the model returned a change with no text AND no
		// usable edits (e.g. {"id":5} or {"id":5,"edits":[]}). Treat as structural
		// corruption and retry, same as unknown id: applying it would blank the
		// cue's text. The edits format makes this more likely than before (the
		// model may emit a bare id when it decides a cue needs no change but
		// forgets to omit it from changes), so we guard here rather than let an
		// empty string reach the apply step and wipe a subtitle line.
		if final == "" {
			return 0, fmt.Errorf("change for id %d resolved to empty text", ch.ID)
		}
		if isHighEditDistance(orig, final) {
			highEditDistance++
		}
	}
	return highEditDistance, nil
}

// isHighEditDistance reports whether a change exceeds the soft warning
// threshold. True means "the admin should spot-check this in the diff view";
// it does NOT mean "reject". Three triggers, any one of which flags the change:
//   - |Δrune length| > maxLenDelta (structural reshaping)
//   - punctuation sequence changed (the prompt explicitly forbids this)
//   - Levenshtein/maxLen > highEditDistanceRatio (substantial rewrite)
func isHighEditDistance(orig, text string) bool {
	dLen := utf8.RuneCountInString(orig) - utf8.RuneCountInString(text)
	if dLen < 0 {
		dLen = -dLen
	}
	if dLen > maxLenDelta {
		return true
	}
	if extractPunctuation(orig) != extractPunctuation(text) {
		return true
	}
	return editDistanceRatio(orig, text) > highEditDistanceRatio
}

// editDistanceRatio returns Levenshtein(a,b) / max(|a|,|b|) (by rune count),
// in [0,1]. 0 = identical; 1 = completely different. Used only for the
// informational HighEditDistanceCount stat — never as a correctness gate.
//
// Standard DP, O(len(a)*len(b)) time and space. Polish cues are short (a few
// dozen runes typically), so this is cheap. Both inputs are rune-sliced first
// so multibyte CJK characters count as 1 unit each.
func editDistanceRatio(a, b string) float64 {
	ra := []rune(a)
	rb := []rune(b)
	la := len(ra)
	lb := len(rb)
	if la == 0 && lb == 0 {
		return 0
	}
	maxLen := la
	if lb > maxLen {
		maxLen = lb
	}
	if maxLen == 0 {
		return 0
	}
	// dp[i][j] = edit distance between ra[:i] and rb[:j]. Use a 1D rolling
	// array since we only need the previous row.
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = del
			if ins < curr[j] {
				curr[j] = ins
			}
			if sub < curr[j] {
				curr[j] = sub
			}
		}
		prev, curr = curr, prev
	}
	return float64(prev[lb]) / float64(maxLen)
}

// extractPunctuation returns the punctuation runes of s in order, as a string.
// Two subtitles with the same punctuation sequence are considered
// punctuation-equivalent. We use unicode punctuation ranges that cover both
// ASCII (,.!?:;) and CJK full-width forms （，。！？：；）.
func extractPunctuation(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isPunct(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isPunct(r rune) bool {
	switch r {
	case ',', '.', '!', '?', ':', ';', '"', '\'', '，', '。', '！', '？', '：', '；', '“', '”', '‘', '’', '、',
		'…', '—', '－', '（', '）', '(', ')', '【', '】', '[', ']':
		return true
	}
	return false
}

// --- helpers: glossary + srt assembly ------------------------------------

func collectGlossary(outcomes []chunkOutcome) []GlossaryCandidate {
	var out []GlossaryCandidate
	for _, oc := range outcomes {
		out = append(out, oc.glossary...)
	}
	return out
}

// dedupGlossary merges glossary candidates by (Original, Corrected),
// keeping the highest confidence and unioning evidence ids.
func dedupGlossary(in []GlossaryCandidate) []GlossaryCandidate {
	type key struct{ o, c string }
	best := make(map[key]*GlossaryCandidate)
	for i := range in {
		g := in[i]
		k := key{g.Original, g.Corrected}
		if existing, ok := best[k]; ok {
			if g.Confidence > existing.Confidence {
				existing.Confidence = g.Confidence
				existing.Context = pickNonEmpty(existing.Context, g.Context)
			}
			existing.EvidenceIDs = unionInts(existing.EvidenceIDs, g.EvidenceIDs)
		} else {
			cp := g
			best[k] = &cp
		}
	}
	out := make([]GlossaryCandidate, 0, len(best))
	for _, v := range best {
		out = append(out, *v)
	}
	// Sort by confidence desc then Original asc so the output is stable.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Confidence > out[i].Confidence ||
				(out[j].Confidence == out[i].Confidence && out[j].Original < out[i].Original) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func unionInts(a, b []int) []int {
	seen := make(map[int]struct{}, len(a)+len(b))
	out := make([]int, 0, len(a)+len(b))
	for _, x := range a {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	for _, x := range b {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	// Sort for determinism.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func pickNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// cuesToSRT rebuilds an SRT document from a list of cues. Timestamps are
// formatted from StartMs/EndMs — they never come from the model, so they are
// byte-identical to what VttToSrt/ParseSRT produced from the source.
func cuesToSRT(cues []ai.SRTCue) string {
	var b strings.Builder
	for i, c := range cues {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d\n", i+1)
		fmt.Fprintf(&b, "%s --> %s\n", msToSRTTime(c.StartMs), msToSRTTime(c.EndMs))
		b.WriteString(c.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// msToSRTTime formats ms as "HH:MM:SS,mmm" (SRT timestamp convention).
func msToSRTTime(ms int) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3_600_000
	ms -= h * 3_600_000
	m := ms / 60_000
	ms -= m * 60_000
	s := ms / 1000
	ms -= s * 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// --- json parsing (tolerant) ---------------------------------------------

// polishJSONEnvelope is the strict shape we expect from the model.
type polishJSONEnvelope struct {
	Changes  []CueChange        `json:"changes"`
	Glossary []GlossaryCandidate `json:"glossary"`
}

// parsePolishJSON extracts the {changes, glossary} envelope from the model's
// raw response. The model is asked for pure JSON, but relays/models
// frequently wrap it in ```json fences or add stray prose — we strip the
// common wrappers before parsing, falling back to carving out the outermost
// { ... } span. Mirrors summarizer.parseSummaryJSON.
func parsePolishJSON(raw string) (*polishJSONEnvelope, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") {
		if start := strings.Index(s, "{"); start >= 0 {
			if end := strings.LastIndex(s, "}"); end > start {
				s = s[start : end+1]
			}
		}
	}
	var env polishJSONEnvelope
	if err := json.Unmarshal([]byte(s), &env); err != nil {
		return nil, err
	}
	if env.Changes == nil {
		env.Changes = []CueChange{}
	}
	if env.Glossary == nil {
		env.Glossary = []GlossaryCandidate{}
	}
	return &env, nil
}

// --- prompts ------------------------------------------------------------
//
// 2026-07-22 重写：与代码实际行为对齐 + 精简 + find/replace 输出格式。
//
// 历史：原提示词有「【严格规则——违反则整批结果作废】」+「3. 改动前后字符数差距
// ≤ 2」。但 2026-07-21 那轮校验放松把 validateChanges 从「长度/标点违规 reject +
// retry」改成「只记 HighEditDistanceCount，不拦」（唯一还 reject 的是 unknown cue
// id），maxLenDelta 也从 2 提到 5。代码放开的那扇门，提示词还在门口拦着——模型按
// 规则 3 自我审查，把合法的 3-5 字修正咽下去不输出，放松根本没生效。
//
// 现在的提示词：标题如实写「id 错误会作废，其他违规由 admin 复核」；删掉字符数
// 规则（代码只警告不拦，提示词就不承诺）；加入 edits(find/replace) 输出格式以省
// completion token（整句改一个字 → 只输出 find/replace 片段）。

// systemPrompt 是默认（含术语挖矿）的系统提示词。
const systemPrompt = `你是字幕校对器。输入：机器转录字幕(JSON, {id,text}) + 术语字典。
任务：找同音错字和术语错误，返回需修正条目；同时挖出可复用的术语规律。

规则（id 错误会导致整批作废；其他违规由 admin 复核，不拦）：
1. 只改：术语字典里的词，或 100% 确定的同音错字
2. 不改标点、语序、表达、语法
3. 用上下文判断字典词是否真是术语（避免歧义）
4. 没问题的条目不放进 changes
5. 只输出 JSON，无任何额外文字

changes 输出（两种二选一，优先用 edits）：
- edits：子串替换，find 必须在该 cue 内唯一
  {"id":147,"edits":[{"find":"出军","replace":"出车"}]}
  多处错字用数组：edits:[{...},{...}]
- text：整句替换（find 无法定位时用）
  {"id":147,"text":"修正后的整句"}

术语挖矿（glossary）：放本次观察到的、confidence≥0.7 的术语纠错规律。
- evidence_ids 填观察到该规律的 cue id
- 字典里已有的不重复放
{"original":"军","corrected":"车","context":"象棋术语","confidence":0.95,"evidence_ids":[147,152]}`

// systemPromptNoGlossary 是 SkipGlossaryMining=true 时用的精简版（无术语挖矿段）。
// re-polish 场景（source=llm_optimized）字幕内容没变，能挖的术语上次已挖，再让
// 模型挖既白烧 completion token 又分散注意力。省 ~120 prompt tok/chunk。
const systemPromptNoGlossary = `你是字幕校对器。输入：机器转录字幕(JSON, {id,text}) + 术语字典。
任务：找同音错字和术语错误，返回需修正条目。

规则（id 错误会导致整批作废；其他违规由 admin 复核，不拦）：
1. 只改：术语字典里的词，或 100% 确定的同音错字
2. 不改标点、语序、表达、语法
3. 用上下文判断字典词是否真是术语（避免歧义）
4. 没问题的条目不放进 changes
5. 只输出 JSON，无任何额外文字

changes 输出（两种二选一，优先用 edits）：
- edits：子串替换，find 必须在该 cue 内唯一
  {"id":147,"edits":[{"find":"出军","replace":"出车"}]}
  多处错字用数组：edits:[{...},{...}]
- text：整句替换（find 无法定位时用）
  {"id":147,"text":"修正后的整句"}`

// SystemPrompt 返回实际使用的系统提示词文本，供 recordPolishRun 写入
// ai_runs.system_prompt_text（让 admin 在「查看回放」里看到本次润色发的 prompt）。
// skipMining=true 时返回精简版（与 polishChunk 实际选择的版本一致）。
func SystemPrompt(skipMining bool) string {
	if skipMining {
		return systemPromptNoGlossary
	}
	return systemPrompt
}
