package polish

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"studyquest/backend/internal/ai"
)

// fakeLLM is a scriptable ai.LLMProvider for testing Polish. Each Chat call
// pops the next canned response (scripted by response index = call order). This
// lets a test drive Polish through a known trajectory: e.g. "chunk 0 returns a
// homophone fix, chunk 1 returns empty changes". Mirrors agent_test.go's
// mockLLM pattern.
type fakeLLM struct {
	mu        sync.Mutex
	responses []fakeResp
	calls     int
	// lastReq captures the most recent ChatRequest, so a test can assert the
	// prompt shape (e.g. that term_dict was injected, that ids are 1-based
	// chunk-local).
	lastReq ai.ChatRequest
	// allReqs captures every request, for tests that care about all chunks.
	allReqs []ai.ChatRequest
}

type fakeResp struct {
	content string
	err     error
}

func (m *fakeLLM) Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastReq = req
	m.allReqs = append(m.allReqs, req)
	idx := m.calls
	m.calls++
	if idx >= len(m.responses) {
		return nil, errors.New("fakeLLM: no more scripted responses")
	}
	r := m.responses[idx]
	if r.err != nil {
		return nil, r.err
	}
	return &ai.ChatResponse{Content: r.content, FinishReason: "stop"}, nil
}

func (m *fakeLLM) Ping(ctx context.Context) error { return nil }
func (m *fakeLLM) ProviderType() string           { return "fake" }

// makeVTT builds a small VTT subtitle string with n cues, each cue's text being
// "line<i>" so tests have predictable, distinguishable content.
func makeVTT(n int) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for i := 1; i <= n; i++ {
		// cue times: 1s apart so they're all distinct + legal
		start := (i - 1) * 1000
		end := start + 900
		b.WriteString(cubeVTT(i, start, end, "line"+itoa(i)))
	}
	return b.String()
}

func cubeVTT(idx, startMs, endMs int, text string) string {
	return itoa(idx) + "\n" + msToVTT(startMs) + " --> " + msToVTT(endMs) + "\n" + text + "\n\n"
}

func msToVTT(ms int) string {
	h := ms / 3600000
	ms -= h * 3600000
	m := ms / 60000
	ms -= m * 60000
	s := ms / 1000
	ms -= s * 1000
	return pad2(h) + ":" + pad2(m) + ":" + pad2(s) + "." + pad3(ms)
}
func pad2(n int) string { if n < 10 { return "0" + itoa(n) } ; return itoa(n) }
func pad3(n int) string {
	if n < 10 { return "00" + itoa(n) }
	if n < 100 { return "0" + itoa(n) }
	return itoa(n)
}
func itoa(n int) string {
	if n == 0 { return "0" }
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg { n = -n }
	for n > 0 { i--; buf[i] = byte('0' + n%10); n /= 10 }
	if neg { i--; buf[i] = '-' }
	return string(buf[i:])
}

// --- Polish entry-point tests -----------------------------------------------

// TestPolish_AppliesValidChanges: one chunk, LLM returns one valid homophone
// fix. Asserts the polished VTT contains the fixed text and that the diff
// records before/after.
func TestPolish_AppliesValidChanges(t *testing.T) {
	// 3 cues: line1, line2, line3. LLM fixes cue 2's text (homophone-style
	// swap: same length, same punctuation, high char overlap).
	vtt := makeVTT(3)
	fix := `{"changes":[{"id":2,"text":"lina2"}],"glossary":[]}`
	llm := &fakeLLM{responses: []fakeResp{{content: fix}}}

	res, err := Polish(context.Background(), llm, "fake-model", PolishRequest{
		VttContent: vtt,
		TermDict:   "",
		Subject:    "test",
	})
	if err != nil {
		t.Fatalf("Polish: %v", err)
	}
	if res.Stats.ChangedCues != 1 {
		t.Errorf("ChangedCues = %d, want 1 (full stats: %+v)", res.Stats.ChangedCues, res.Stats)
	}
	if !strings.Contains(res.PolishedVtt, "lina2") {
		t.Errorf("polished VTT missing fix: %s", res.PolishedVtt)
	}
	if strings.Contains(res.PolishedVtt, "line2") {
		t.Errorf("polished VTT still has old text 'line2'")
	}
	// Cue 1 and 3 untouched.
	if !strings.Contains(res.PolishedVtt, "line1") || !strings.Contains(res.PolishedVtt, "line3") {
		t.Errorf("polished VTT dropped unchanged cues: %s", res.PolishedVtt)
	}
	if len(res.Diff) != 1 || res.Diff[0].ID != 2 {
		t.Errorf("Diff = %+v, want one entry with ID=2", res.Diff)
	}
	if res.Diff[0].Before != "line2" || res.Diff[0].After != "lina2" {
		t.Errorf("Diff before/after wrong: %+v", res.Diff[0])
	}
}

// TestPolish_LengthViolationAppliedAndFlagged: LLM returns a change whose
// length delta exceeds the old hard threshold (was 2, now maxLenDelta=5 is the
// warning line). Under the relaxed design (2026-07-21) the change is STILL
// APPLIED — we trust the LLM by default and surface the warning via
// HighEditDistanceCount so the admin can spot-check in the diff UI. This is
// the inversion of the old behavior, which rejected these outright and caused
// legitimate homophone fixes on short cues to fail repeatedly.
func TestPolish_LengthViolationAppliedAndFlagged(t *testing.T) {
	vtt := makeVTT(2)
	// "line1" (5 chars) → "completely different long text" (way more than +5).
	// This is clearly a rewrite, not a correction — it should be flagged but
	// STILL APPLIED (admin reviews via diff UI).
	bad := `{"changes":[{"id":1,"text":"completely different long text"}],"glossary":[]}`
	llm := &fakeLLM{responses: []fakeResp{{content: bad}}}

	res, err := Polish(context.Background(), llm, "fake-model", PolishRequest{VttContent: vtt})
	if err != nil {
		t.Fatalf("Polish: %v", err)
	}
	// Change was applied (not rejected).
	if res.Stats.ChangedCues != 1 {
		t.Errorf("ChangedCues = %d, want 1 (relaxed design applies the change)", res.Stats.ChangedCues)
	}
	if !strings.Contains(res.PolishedVtt, "completely different long text") {
		t.Errorf("polished VTT should contain the LLM's text: %s", res.PolishedVtt)
	}
	// Chunk did NOT fail (only structural errors fail now).
	if res.Stats.FailedChunks != 0 {
		t.Errorf("FailedChunks = %d, want 0 (length violation is no longer a failure)", res.Stats.FailedChunks)
	}
	if res.Stats.PartialOptimized {
		t.Errorf("PartialOptimized = true, want false")
	}
	// But it WAS flagged as high edit distance for admin review.
	if res.Stats.HighEditDistanceCount != 1 {
		t.Errorf("HighEditDistanceCount = %d, want 1", res.Stats.HighEditDistanceCount)
	}
}

// TestPolish_HallucinationAppliedAndFlagged: the rewrite case — same length,
// same punctuation, but completely different characters. The old charOverlap
// gate rejected this; the relaxed design applies it and flags it. Rationale:
// the LLM occasionally rewrites a cue, and we'd rather surface every change
// for human review than silently drop legitimate fixes that happen to share
// few characters with the original (the failure mode that motivated the
// relaxation — see TestChangeAllowed_Relaxed).
func TestPolish_HallucinationAppliedAndFlagged(t *testing.T) {
	vtt := makeVTT(2)
	// "line1" → "abcde": same length (5), no punctuation, zero shared chars.
	hallucination := `{"changes":[{"id":1,"text":"abcde"}],"glossary":[]}`
	llm := &fakeLLM{responses: []fakeResp{{content: hallucination}}}

	res, err := Polish(context.Background(), llm, "fake-model", PolishRequest{VttContent: vtt})
	if err != nil {
		t.Fatalf("Polish: %v", err)
	}
	// Applied (relaxed design).
	if res.Stats.ChangedCues != 1 {
		t.Errorf("ChangedCues = %d, want 1", res.Stats.ChangedCues)
	}
	// Flagged for review.
	if res.Stats.HighEditDistanceCount != 1 {
		t.Errorf("HighEditDistanceCount = %d, want 1 (hallucination should be flagged)", res.Stats.HighEditDistanceCount)
	}
}

// TestPolish_RealHomophoneFixesPass: the regression suite for the bug that
// motivated the relaxation. These are actual failure cases from production
// polish runs (see ai_jobs.error in the DB) where the LLM returned CORRECT
// terminology fixes but the old charOverlap ≥ 0.6 gate rejected them because
// short cues have low Jaccard even for 1-character swaps. All of these must
// now apply cleanly with HighEditDistanceCount == 0 (they're normal fixes,
// not suspicious rewrites).
func TestPolish_RealHomophoneFixesPass(t *testing.T) {
	cases := []struct {
		name string
		orig string
		fix  string
	}{
		// Real failures from polish job #1 (course 1, math) and #4 (course 2,
		// xiangqi). The LLM was right; our rules were wrong.
		{"考算→口算 (2字短词改1字)", "考算", "口算"},
		{"合不变→和不变 (3字改1字)", "合不变", "和不变"},
		{"实境制→十进制 (3字术语纠错)", "没有这个实境制", "没有这个十进制"},
		// Plus normal cases from the glossary candidates — all should pass
		// without being flagged.
		{"军→车 (象棋术语,2字改1字)", "出军", "出车"},
		{"码→马 (象棋术语)", "跳码", "跳马"},
		{"泡→炮 (象棋术语)", "打泡", "打炮"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vtt := cubeVTT(1, 0, 900, c.orig)
			fix := fmt.Sprintf(`{"changes":[{"id":1,"text":%q}],"glossary":[]}`, c.fix)
			llm := &fakeLLM{responses: []fakeResp{{content: fix}}}

			res, err := Polish(context.Background(), llm, "fake-model",
				PolishRequest{VttContent: "WEBVTT\n\n" + vtt})
			if err != nil {
				t.Fatalf("Polish: %v", err)
			}
			if res.Stats.ChangedCues != 1 {
				t.Errorf("ChangedCues = %d, want 1 (fix should apply)", res.Stats.ChangedCues)
			}
			if res.Stats.FailedChunks != 0 {
				t.Errorf("FailedChunks = %d, want 0", res.Stats.FailedChunks)
			}
			if res.Stats.HighEditDistanceCount != 0 {
				t.Errorf("HighEditDistanceCount = %d, want 0 (this is a normal fix, not a rewrite)",
					res.Stats.HighEditDistanceCount)
			}
			if !strings.Contains(res.PolishedVtt, c.fix) {
				t.Errorf("polished VTT missing fix %q: %s", c.fix, res.PolishedVtt)
			}
		})
	}
}

// TestPolish_PreservesTimestamps: the core invariant. Whatever the LLM returns,
// the polished VTT's timestamps must be byte-identical to the original. We
// check by extracting the timestamp lines from both.
func TestPolish_PreservesTimestamps(t *testing.T) {
	vtt := makeVTT(5)
	fix := `{"changes":[{"id":3,"text":"lin3"}],"glossary":[]}`
	llm := &fakeLLM{responses: []fakeResp{{content: fix}}}

	res, err := Polish(context.Background(), llm, "fake-model", PolishRequest{VttContent: vtt})
	if err != nil {
		t.Fatalf("Polish: %v", err)
	}
	origTS := extractTimestamps(vtt)
	newTS := extractTimestamps(res.PolishedVtt)
	if origTS != newTS {
		t.Errorf("timestamps changed!\norig: %s\nnew:  %s", origTS, newTS)
	}
}

// TestPolish_RetriesThenSucceeds: first call returns junk, second returns a
// valid change. Polish should retry and ultimately apply the fix.
func TestPolish_RetriesThenSucceeds(t *testing.T) {
	vtt := makeVTT(2)
	junk := `not json at all`
	good := `{"changes":[{"id":1,"text":"lina1"}],"glossary":[]}`
	llm := &fakeLLM{responses: []fakeResp{
		{content: junk}, // attempt 1: parse fails → retry
		{content: good}, // attempt 2: valid
	}}

	res, err := Polish(context.Background(), llm, "fake-model", PolishRequest{VttContent: vtt})
	if err != nil {
		t.Fatalf("Polish: %v", err)
	}
	if res.Stats.ChangedCues != 1 {
		t.Errorf("ChangedCues = %d, want 1 (retry should have succeeded)", res.Stats.ChangedCues)
	}
	if res.Stats.FailedChunks != 0 {
		t.Errorf("FailedChunks = %d, want 0", res.Stats.FailedChunks)
	}
	if res.Stats.Retries < 1 {
		t.Errorf("Retries = %d, want >= 1", res.Stats.Retries)
	}
}

// TestPolish_GlossaryCollected: LLM returns both a change and a glossary
// candidate. The result should surface the glossary entry (deduped across
// chunks).
func TestPolish_GlossaryCollected(t *testing.T) {
	vtt := makeVTT(2)
	resp := `{"changes":[{"id":1,"text":"lina1"}],"glossary":[{"original":"line","corrected":"lina","context":"test term","confidence":0.9,"evidence_ids":[1]}]}`
	llm := &fakeLLM{responses: []fakeResp{{content: resp}}}

	res, err := Polish(context.Background(), llm, "fake-model", PolishRequest{VttContent: vtt})
	if err != nil {
		t.Fatalf("Polish: %v", err)
	}
	if len(res.Glossary) != 1 {
		t.Fatalf("Glossary len = %d, want 1: %+v", len(res.Glossary), res.Glossary)
	}
	g := res.Glossary[0]
	if g.Original != "line" || g.Corrected != "lina" {
		t.Errorf("glossary entry wrong: %+v", g)
	}
	if g.Confidence != 0.9 {
		t.Errorf("confidence = %v, want 0.9", g.Confidence)
	}
}

// TestPolish_NilProvider: Polish with a nil provider must error, not panic.
func TestPolish_NilProvider(t *testing.T) {
	_, err := Polish(context.Background(), nil, "x", PolishRequest{VttContent: makeVTT(1)})
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

// TestPolish_EmptyVTT: an empty/zero-cue subtitle should error cleanly.
func TestPolish_EmptyVTT(t *testing.T) {
	_, err := Polish(context.Background(), &fakeLLM{}, "x", PolishRequest{VttContent: "WEBVTT\n\n"})
	if err == nil {
		t.Fatal("expected error for zero-cue subtitle")
	}
}

// TestPolish_ChunkLocalIDs: the prompt sends chunk-local 1-based ids. With a
// subtitle larger than one chunk, chunk 2's first cue should be id=1 in the
// prompt (not the global index). We assert by inspecting the request body.
func TestPolish_ChunkLocalIDs(t *testing.T) {
	// chunkSize=150, so 2 chunks needs >150 cues. Make 160 to force 2 chunks
	// (chunk 0 = cues 1..150, chunk 1 = cues 148..160 with overlap 3 — but
	// ids inside the prompt are 1-based local).
	vtt := makeVTT(160)
	// Both chunks return empty changes (we only care about the prompt shape).
	empty := `{"changes":[],"glossary":[]}`
	resps := []fakeResp{{content: empty}, {content: empty}}
	llm := &fakeLLM{responses: resps}

	_, err := Polish(context.Background(), llm, "fake-model", PolishRequest{VttContent: vtt})
	if err != nil {
		t.Fatalf("Polish: %v", err)
	}
	if len(llm.allReqs) < 2 {
		t.Fatalf("expected >= 2 LLM calls, got %d", len(llm.allReqs))
	}
	// Second chunk's prompt should contain "id":1 (local), NOT the global
	// index 148+. We check the user prompt content of the 2nd request.
	secondPrompt := llm.allReqs[1].Messages[1].Content
	if !strings.Contains(secondPrompt, `"id":1`) {
		t.Errorf("chunk 2 prompt should use local id=1, got: %.300s", secondPrompt)
	}
}

// --- validation unit tests --------------------------------------------------

func TestValidateChanges_UnknownIDRejected(t *testing.T) {
	blk := []cueRef{{Cue: ai.SRTCue{Text: "a"}}, {Cue: ai.SRTCue{Text: "b"}}}
	// Unknown id is a structural failure (LLM lost alignment) — still retries.
	hed, err := validateChanges(blk, []CueChange{{ID: 99, Text: "x"}})
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
	if hed != 0 {
		t.Errorf("highEditDistance = %d, want 0 on structural failure", hed)
	}
}

// TestValidateChanges_FlagsSuspiciousButAccepts: the core behavior change.
// Suspicious changes (length delta > maxLenDelta, punctuation changed, or high
// Levenshtein ratio) are NO LONGER rejected. They're counted in the returned
// highEditDistance and the caller applies them — admin reviews via diff UI.
// Only unknown-id structural corruption returns an error.
func TestValidateChanges_FlagsSuspiciousButAccepts(t *testing.T) {
	blk := []cueRef{{Cue: ai.SRTCue{Text: "abcde"}}}
	// |Δ|=20 (> maxLenDelta=5) → flagged, no error.
	hed, err := validateChanges(blk, []CueChange{{ID: 1, Text: "completely different long text"}})
	if err != nil {
		t.Fatalf("expected no error for suspicious change, got %v", err)
	}
	if hed != 1 {
		t.Errorf("highEditDistance = %d, want 1", hed)
	}

	// Punctuation changed → flagged, no error.
	blk2 := []cueRef{{Cue: ai.SRTCue{Text: "你好。"}}}
	hed, err = validateChanges(blk2, []CueChange{{ID: 1, Text: "你好！"}})
	if err != nil {
		t.Fatalf("expected no error for punctuation change, got %v", err)
	}
	if hed != 1 {
		t.Errorf("highEditDistance = %d, want 1 (punctuation change)", hed)
	}

	// Normal homophone fix → no flag, no error.
	blk3 := []cueRef{{Cue: ai.SRTCue{Text: "考算"}}}
	hed, err = validateChanges(blk3, []CueChange{{ID: 1, Text: "口算"}})
	if err != nil {
		t.Fatalf("expected no error for normal fix, got %v", err)
	}
	if hed != 0 {
		t.Errorf("highEditDistance = %d, want 0 (normal homophone fix)", hed)
	}

	// Empty changes → zero flags, no error.
	hed, err = validateChanges(blk3, []CueChange{})
	if err != nil || hed != 0 {
		t.Errorf("empty changes: hed=%d err=%v, want 0/nil", hed, err)
	}
}

// TestIsHighEditDistance pins the three triggers independently. Each case is
// crafted to trip exactly one trigger so a regression in any single check is
// obvious.
func TestIsHighEditDistance(t *testing.T) {
	cases := []struct {
		name string
		orig string
		text string
		want bool
	}{
		// All-clear cases (normal homophone / terminology fixes):
		{"identical", "abcde", "abcde", false},
		{"1-char homophone swap", "考算", "口算", false},
		{"3-char term fix", "合不变", "和不变", false},
		{"longer sentence 1-char fix", "没有这个实境制", "没有这个十进制", false},
		{"3-char delta (not flagged)", "abcde", "abcdefgh", false}, // |Δ|=3 ≤ 5, ratio low

		// Trigger 1: |Δlen| > maxLenDelta (5). Boundary |Δ|=5 is NOT flagged
		// (strict >); |Δ|=6 is.
		{"delta 5 boundary not flagged", "abcde", "abcdefghij", false},  // |Δ|=5
		{"delta 6 flagged", "abcde", "abcdefghijk", true},                // |Δ|=6

		// Trigger 2: punctuation sequence changed.
		{"punct added flagged", "你好", "你好。", true},
		{"punct swapped flagged", "你好。", "你好！", true},

		// Trigger 3: Levenshtein/maxLen > 0.5 (same length, big rewrite).
		{"5-char rewrite flagged", "abcde", "wxyzf", true}, // dist 5, ratio 1.0
		{"3-char 1-swap not flagged", "abc", "axc", false}, // dist 1, ratio 0.33
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isHighEditDistance(c.orig, c.text)
			if got != c.want {
				t.Errorf("isHighEditDistance(%q,%q) = %v, want %v", c.orig, c.text, got, c.want)
			}
		})
	}
}

// TestEditDistanceRatio spot-checks the Levenshtein computation against known
// values. This is the math underlying the HighEditDistanceCount stat — a
// regression here would silently miscount suspicious changes. The randomized
// cross-check in TestEditDistanceRatio_CrossCheckReference is the stronger
// guarantee; this test pins specific cases so a regression points at the exact
// input shape that broke.
func TestEditDistanceRatio(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want float64
	}{
		// Boundaries — the rolling-array DP is most likely to misbehave here.
		{"both empty", "", "", 0},
		{"a empty b nonempty", "", "abc", 1.0},        // 3 insertions / maxLen 3
		{"a nonempty b empty", "abc", "", 1.0},         // 3 deletions / maxLen 3
		{"single char same", "a", "a", 0},
		{"single char diff", "a", "b", 1.0},
		{"single vs double", "a", "ab", 0.5},           // 1 insertion / maxLen 2
		// Identical strings of varying length (the DP should short-circuit via
		// the diagonal; a buggy cost=1 substitution branch would return >0).
		{"identical 3", "abc", "abc", 0},
		{"identical 10", "abcdefghij", "abcdefghij", 0},
		// Classic textbook cases.
		{"abc abx 1-sub", "abc", "abx", 1.0 / 3},
		{"abc xyz full-rewrite", "abc", "xyz", 1.0},
		{"abc abcde 2-ins", "abc", "abcde", 2.0 / 5},
		// Asymmetric lengths — exercises the rolling array swap + the maxLen
		// denominator choice (max, not min or sum).
		{"short vs long", "ab", "abcdefgh", 6.0 / 8},   // 6 insertions / maxLen 8
		{"long vs short", "abcdefgh", "ab", 6.0 / 8},   // symmetric (deletions)
		// CJK multibyte: each rune counts as 1 unit (not 3 bytes). This is the
		// actual polish use case — a byte-based DP would give wildly wrong ratios.
		{"考算 口算", "考算", "口算", 0.5},               // 1 of 2 runes changed
		{"合不变 和不变", "合不变", "和不变", 1.0 / 3},
		{"车马炮 车马包", "车马炮", "车马包", 1.0 / 3},     // 1 of 3 runes
		// Emoji / astral plane — each codepoint is 1 rune via []rune. A byte-based
		// DP would see 4 bytes per emoji and miscount.
		{"emoji same", "😀ab", "😀ab", 0},
		{"emoji swap", "😀ab", "😃ab", 1.0 / 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := editDistanceRatio(c.a, c.b)
			// Float compare with tolerance.
			if got < c.want-0.001 || got > c.want+0.001 {
				t.Errorf("editDistanceRatio(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// referenceLevenshtein is a naive recursive Levenshtein used ONLY as a
// cross-check oracle for the optimized DP in editDistanceRatio. It's the
// textbook 3-way-min recursion with no memoization — exponential, but
// correct by construction (and obvious enough to read as a spec). The DP
// under test must agree with it on every input; a disagreement proves the
// rolling-array implementation has a bug (stale cell, off-by-one, wrong cost).
//
// We compute it over RUNES (not bytes) to match editDistanceRatio's unit.
func referenceLevenshtein(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	if a[0] == b[0] {
		return referenceLevenshtein(a[1:], b[1:])
	}
	del := 1 + referenceLevenshtein(a[1:], b)
	ins := 1 + referenceLevenshtein(a, b[1:])
	sub := 1 + referenceLevenshtein(a[1:], b[1:])
	min := del
	if ins < min {
		min = ins
	}
	if sub < min {
		min = sub
	}
	return min
}

// TestEditDistanceRatio_CrossCheckReference is the real correctness guarantee
// for the DP. It generates a few thousand random string pairs (ASCII + CJK
// mixes, varied lengths) and asserts editDistanceRatio's result matches a
// naive recursive Levenshtein / maxLen. A rolling-array bug that passes the
// hand-picked cases above will almost certainly fail here — the random inputs
// hit cell combinations the hand cases don't.
//
// Kept deterministic via a fixed seed so a failure reproduces. The case count
// is tuned to run in well under a second (recursion is exponential, so we
// keep individual strings short — ≤8 runes — but draw many pairs).
func TestEditDistanceRatio_CrossCheckReference(t *testing.T) {
	// Alphabet mixes ASCII + CJK so we exercise the rune (not byte) counting
	// under realistic polish-like inputs.
	const alphabet = "abc车马炮口算和考😀😃"
	rng := rand.New(rand.NewSource(1)) // fixed seed → reproducible
	const numCases = 3000
	maxLenObserved := 0
	for i := 0; i < numCases; i++ {
		// Draw lengths in [0, 8] — short enough that the exponential reference
		// stays fast, long enough to exercise multi-row DP iterations.
		la := rng.Intn(9)
		lb := rng.Intn(9)
		if la+lb > maxLenObserved {
			maxLenObserved = la + lb
		}
		a := randRunes(rng, la, alphabet)
		b := randRunes(rng, lb, alphabet)
		want := referenceLevenshtein(a, b)
		maxLen := la
		if lb > maxLen {
			maxLen = lb
		}
		wantRatio := 0.0
		if maxLen > 0 {
			wantRatio = float64(want) / float64(maxLen)
		}
		got := editDistanceRatio(string(a), string(b))
		if got < wantRatio-0.001 || got > wantRatio+0.001 {
			t.Errorf("case %d: editDistanceRatio(%q,%q) = %v, reference = %v (lev=%d maxLen=%d)",
				i, string(a), string(b), got, wantRatio, want, maxLen)
		}
	}
}

// randRunes draws n runes (with replacement) from the given alphabet. Used by
// the cross-check test to build random inputs.
func randRunes(rng *rand.Rand, n int, alphabet string) []rune {
	runes := []rune(alphabet)
	out := make([]rune, n)
	for i := range out {
		out[i] = runes[rng.Intn(len(runes))]
	}
	return out
}

// --- helpers ----------------------------------------------------------------

func extractTimestamps(vtt string) string {
	var b strings.Builder
	for _, line := range strings.Split(vtt, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "-->") {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
