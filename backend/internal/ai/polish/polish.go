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
	LLMCalls         int           `json:"llm_calls"` // successful only
	FailedChunks     int           `json:"failed_chunks"` // chunks that exhausted retries
	Retries          int           `json:"retries"` // total retry attempts across chunks
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
	Duration         time.Duration `json:"duration"`
	PartialOptimized bool          `json:"partial_optimized"` // true if any chunk failed
	// FailedChunkErrors maps failed chunk index → its last error string. Empty
	// unless PartialOptimized. Surfaced to the admin job detail so polish
	// failures are actionable (validation reject vs JSON parse vs network).
	// One entry per failed chunk; chunks that succeeded are absent.
	FailedChunkErrors map[int]string `json:"failed_chunk_errors,omitempty"`
}

// --- chunking tunables (from the design doc §2.2) -----------------------

const (
	chunkSize    = 150 // cues per chunk
	chunkOverlap = 3   // overlap cues between adjacent chunks
	concurrency  = 3   // in-flight LLM calls (user-imposed, relays limit hard)
	maxRetries   = 3   // retries per chunk before giving up (uses raw text)
	maxTokens    = 8000
)

// --- the entry point -----------------------------------------------------

// Polish runs the full pipeline:
//  1. VttToSrt + ParseSRT → flat cue list
//  2. chunk into 150-cue windows with 3-cue overlap
//  3. concurrently (3-way) call the LLM per chunk, retry failed chunks 3x
//  4. validate every returned change (length Δ ≤ 2, punctuation untouched,
//     id set matches)
//  5. apply validated changes back to the cues (timestamps untouched)
//  6. reassemble SRT → VTT
//
// Timestamps are guaranteed byte-identical: the prompt never contains them,
// and reassembly rebuilds the SRT from the parsed cues' StartMs/EndMs.
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
	var wg sync.WaitGroup

	var totalPrompt, totalCompletion, totalLLMCalls, totalRetries, failedChunks int32

	for i, blk := range chunks {
		wg.Add(1)
		go func(i int, blk []cueRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var (
				parsed  *polishResponse
				pErr    error
				usage   ai.Usage
				retries int
			)
			// Retry loop: re-call the LLM until the response parses AND validates.
			// Labeled break (`break retryLoop`) is required because a plain `break`
			// inside the select would only exit the select, not the for — and we'd
			// keep re-calling polishChunk on a cancelled ctx until maxRetries ran out.
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
				resp, callErr := polishChunk(ctx, llm, model, blk, req.TermDict, req.Subject)
				if callErr != nil {
					pErr = callErr
					continue
				}
				usage = resp.usage
				if respParseErr := validateChanges(blk, resp.changes); respParseErr != nil {
					pErr = respParseErr
					continue
				}
				parsed = &polishResponse{changes: resp.changes, glossary: resp.glossary}
				pErr = nil
				break
			}

			oc := chunkOutcome{idx: i, retries: retries}
			if parsed != nil {
				oc.changes = parsed.changes
				oc.glossary = parsed.glossary
				oc.usage = usage
				atomic.AddInt32(&totalLLMCalls, 1)
			} else {
				oc.failed = true
				atomic.AddInt32(&failedChunks, 1)
				// pErr holds the last error from the retry loop (LLM error,
				// JSON parse error, or validation rejection). Stash it so the
				// caller can surface it in the job detail — without this the
				// admin sees "N chunks failed" with zero context.
				if pErr != nil {
					oc.errStr = pErr.Error()
				} else {
					oc.errStr = "unknown"
				}
				oc.changes = nil
			}
			atomic.AddInt32(&totalRetries, int32(retries))
			if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
				atomic.AddInt32(&totalPrompt, int32(usage.PromptTokens))
				atomic.AddInt32(&totalCompletion, int32(usage.CompletionTokens))
			}
			outcomes[i] = oc
		}(i, blk)
	}
	wg.Wait()

	// 4. Apply changes to a map keyed by global cue index. Last write wins
	// (the later chunk has more right-side context and is preferred). We also
	// record before/after for the diff. Validation already ran per chunk;
	// we additionally dedup glossary entries here.
	editedText := make(map[int]string, len(cues))
	for _, oc := range outcomes {
		for _, ch := range oc.changes {
			// ch.ID is the chunk-local 1-based id → resolve to global idx.
			// (We sent chunk-local ids in the prompt so ids stay small and
			// dense, which models handle reliably.)
			if ch.ID < 1 || ch.ID > len(chunks[oc.idx]) {
				continue
			}
			globalIdx := chunks[oc.idx][ch.ID-1].GlobalIdx
			editedText[globalIdx] = ch.Text
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
		// Sanity: even though per-chunk validation already enforced this,
		// re-check at apply time too — cheap and defense-in-depth.
		if !changeAllowed(c.Text, newText) {
			continue
		}
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
	for _, oc := range outcomes {
		if oc.failed {
			if failedErrs == nil {
				failedErrs = make(map[int]string)
			}
			failedErrs[oc.idx] = oc.errStr
		}
	}

	stats := PolishStats{
		TotalCues:         len(cues),
		ChangedCues:       changed,
		ChunkCount:        len(chunks),
		LLMCalls:          int(atomic.LoadInt32(&totalLLMCalls)),
		FailedChunks:      int(atomic.LoadInt32(&failedChunks)),
		Retries:           int(atomic.LoadInt32(&totalRetries)),
		PromptTokens:      int(atomic.LoadInt32(&totalPrompt)),
		CompletionTokens:  int(atomic.LoadInt32(&totalCompletion)),
		Duration:          time.Since(start),
		PartialOptimized:  atomic.LoadInt32(&failedChunks) > 0,
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
	usage    ai.Usage
	failed   bool
	retries  int
	// errStr is the last error from the retry loop when failed=true. Surface
	// this up to the job detail so admin can tell validation rejection / JSON
	// parse failure / network error apart — polish runs 7-13 minutes and
	// "N chunks failed" with no cause is unactionable.
	errStr string
}

// CueChange is one LLM-returned change in the chunk-local id space.
type CueChange struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
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
// a parsed polishResponse plus the token usage from the call.
func polishChunk(ctx context.Context, llm ai.LLMProvider, model string, blk []cueRef, termDict, subject string) (*callResult, error) {
	sys := systemPrompt
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

// validateChanges enforces the doc's three per-change rules over a chunk's
// returned changes:
//  1. every id is in the chunk's id range
//  2. length delta (runes) ≤ 2
//  3. punctuation sequence is unchanged
//
// If any change violates, the whole chunk is rejected — the retry loop will
// re-call the LLM. This is stricter than per-change skipping: it teaches the
// model (via retry) that violations are not tolerated, and avoids partial /
// misleading output.
func validateChanges(blk []cueRef, changes []CueChange) error {
	// Build a set of valid ids and the original text for each.
	byID := make(map[int]string, len(blk))
	for i, c := range blk {
		byID[i+1] = c.Cue.Text
	}
	for _, ch := range changes {
		orig, ok := byID[ch.ID]
		if !ok {
			return fmt.Errorf("unknown id %d in changes (chunk has %d cues)", ch.ID, len(blk))
		}
		if !changeAllowed(orig, ch.Text) {
			return fmt.Errorf("id %d rejected: orig=%q new=%q (len delta or punctuation changed)", ch.ID, orig, ch.Text)
		}
	}
	return nil
}

// changeAllowed returns true if changing `orig` → `text` satisfies the
// length-delta, punctuation-preservation, AND character-overlap rules. Used
// both at per-chunk validation and at apply time (defense-in-depth).
//
// The character-overlap rule catches LLM hallucinations where the model
// rewrites a whole cue with the same length + same punctuation but completely
// different content (e.g. replacing "对方这个棋跟我走的向三进五" with "这个棋等一下我就给大家说一下" —
// 14 chars each, same punctuation, totally different meaning). Requiring ≥60%
// shared characters (by rune-frequency-set Jaccard) rejects these rewrites
// while still allowing legitimate homophone corrections (which change 1-3
// characters in an otherwise-identical string).
func changeAllowed(orig, text string) bool {
	dLen := utf8.RuneCountInString(orig) - utf8.RuneCountInString(text)
	if dLen < 0 {
		dLen = -dLen
	}
	if dLen > 2 {
		return false
	}
	if extractPunctuation(orig) != extractPunctuation(text) {
		return false
	}
	return charOverlap(orig, text) >= 0.6
}

// charOverlap returns the Jaccard similarity of the rune-frequency multisets
// of a and b: |intersection| / |union|, in [0, 1]. Two strings sharing most of
// their characters score high; a complete rewrite scores low.
//
// This is intentionally a SET-based measure, not an edit-distance one. We want
// to catch "rewrote the sentence" while tolerating "swapped a few characters
// around" — Jaccard on rune frequencies does exactly that, cheaply.
func charOverlap(a, b string) float64 {
	if a == "" && b == "" {
		return 1.0
	}
	ra := utf8.RuneCountInString(a)
	rb := utf8.RuneCountInString(b)
	if ra+rb == 0 {
		return 1.0
	}
	fa := runeFreq(a)
	fb := runeFreq(b)
	// Multiset intersection: sum of min counts per rune.
	inter := 0
	for r, ca := range fa {
		if cb, ok := fb[r]; ok {
			if ca < cb {
				inter += ca
			} else {
				inter += cb
			}
		}
	}
	// Multiset union = |a| + |b| - |intersection|.
	union := ra + rb - inter
	if union == 0 {
		return 1.0
	}
	return float64(inter) / float64(union)
}

func runeFreq(s string) map[rune]int {
	m := make(map[rune]int)
	for _, r := range s {
		m[r]++
	}
	return m
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

// --- prompts (from docs/subtitle-system-overhaul.md §五 PR2) -------------

const systemPrompt = `你是一个字幕校对器。你会收到一段机器转录的字幕（JSON）以及术语字典。
你的任务是找出其中的【同音错字和术语错误】，返回需要修正的条目，同时把本次发现的术语规律挖出来供后续课程使用。

【严格规则——违反则整批结果作废】
1. 只改【术语字典】里明确列出的词，以及你能 100% 确定是同音错字的词
2. 不改标点、不改语序、不优化表达、不纠正语法
3. 改动前后字符数差距 ≤ 2
4. 利用上下文判断：字典里的词在当前句中是否真的是术语
   （如"动车"是动词+车 vs "车走到中路"是象棋术语）
5. 没问题的条目不要放进 changes
6. 严格只输出 JSON，不要任何额外说明文字

【术语挖矿】
把本次观察到的、有把握的术语纠错规律放进 glossary 字段。
- 只放 confidence ≥ 0.7 的（多次观察、上下文一致）
- evidence_ids 写观察到这个规律的 cue id 数组（1-based，对应用户给的 id）
- 字典里已有的不要重复放

输出格式（严格遵守）：
{
  "changes": [
    {"id": 147, "text": "修正后的整句文本"}
  ],
  "glossary": [
    {
      "original": "军",
      "corrected": "车",
      "context": "象棋术语，指棋子",
      "confidence": 0.95,
      "evidence_ids": [147, 152, 198]
    }
  ]
}`
