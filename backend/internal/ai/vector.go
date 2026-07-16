package ai

import (
	"encoding/json"
	"fmt"
)

// This file holds small pure helpers for vector similarity (RAG retrieval) and
// is deliberately dependency-free so it can be unit-tested in isolation. The
// quiz agent's retrieval tool (search_subtitles) and the memory/grading logic
// all build on these.

// CosineSim computes the cosine similarity between two equal-length float32
// vectors. Returns 0 for empty/degenerate vectors (rather than NaN) so callers
// can treat "no overlap" and "missing vector" uniformly — a chunk with a
// zeroed/garbage embedding simply won't surface as a top match.
//
// Why brute-force cosine and not a vector index: per-episode chunk counts are
// in the hundreds (a 31-minute lesson → 27 chunks). Scanning a few hundred
// 512-dim vectors is microseconds; an ANN index's overhead (build + memory +
// approximation) isn't worth it until chunk counts reach tens of thousands.
func CosineSim(a, b []float32) float32 {
	n := len(a)
	if n == 0 || n != len(b) {
		return 0
	}
	var dot, na, nb float32
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	// dot / (sqrt(na) * sqrt(nb)). float32 math is precise enough for ranking.
	return dot / (sqrtf(na) * sqrtf(nb))
}

// sqrtf is a float32 sqrt without pulling in math.Sqrt's float64 round-trips.
// Accuracy is more than sufficient for similarity ranking.
func sqrtf(x float32) float32 {
	if x <= 0 {
		return 0
	}
	// Newton's method, a couple of iterations from a reasonable seed.
	z := x
	for i := 0; i < 8; i++ {
		z = (z + x/z) * 0.5
	}
	return z
}

// ParseEmbedding decodes the JSON-serialized float32 vector stored on
// content_chunks.embedding. Chunks persist embeddings as a JSON array string
// (e.g. "[0.012, -0.34, ...]") because SQLite has no native vector type. This
// is the inverse of what runSegmentJob writes (json.Marshal of [][]float32[i]).
// Returns an empty slice (not an error) for an empty/null string so callers can
// treat "no embedding" as "this chunk is not retrievable" without special-casing.
func ParseEmbedding(jsonStr string) ([]float32, error) {
	if jsonStr == "" {
		return nil, nil
	}
	var v []float32
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return nil, fmt.Errorf("parse embedding: %w", err)
	}
	return v, nil
}

// TopK returns the indices of the k highest-similarity vectors in haystack to
// needle, in descending similarity order. Used by search_subtitles to pick the
// most relevant chunks for a query. Returns fewer than k if haystack is smaller.
// Similarity is computed via CosineSim; ties keep their original (stable) order.
func TopK(needle []float32, haystack [][]float32, k int) []int {
	if k <= 0 || len(haystack) == 0 {
		return nil
	}
	type pair struct {
		idx int
		sim float32
	}
	pairs := make([]pair, len(haystack))
	for i, h := range haystack {
		pairs[i] = pair{idx: i, sim: CosineSim(needle, h)}
	}
	// Insertion sort highest-first. n is small (chunk count per episode, in the
	// hundreds) and insertion sort is stable on index for ties, unlike
	// sort.Slice's non-deterministic tie handling — stability matters so a
	// re-run surfaces the same chunks for a given query.
	for i := 1; i < len(pairs); i++ {
		j := i
		for j > 0 && pairs[j].sim > pairs[j-1].sim {
			pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
			j--
		}
	}
	if k > len(pairs) {
		k = len(pairs)
	}
	out := make([]int, k)
	for i := 0; i < k; i++ {
		out[i] = pairs[i].idx
	}
	return out
}

// ---------------------------------------------------------------------------
// Text normalization for fill-in-the-blank grading
// ---------------------------------------------------------------------------

// NormalizeText canonicalizes a free-text answer for comparison. Fill-in-the-
// blank questions are limited to knowledge points with a UNIQUE answer (math
// results, factual recall), so exact-match-after-normalization is the right
// grading policy — we do NOT want fuzzy/semantic matching here (that would
// silently accept wrong math answers like "11" ≈ "12").
//
// Transforms applied:
//   - fold full-width chars (全角→半角): students on Chinese IMEs often type
//     "１２" or "Ａ"; these must equal "12" / "a". Done first so a full-width
//     symbol like ＝ folds to = then gets stripped.
//   - lowercase ASCII letters
//   - KEEP only: letters, digits, CJK ideographs, and a small math allowlist
//     (".", "-", "/") needed for numeric answers like "3.14", "-5", "1/2".
//   - strip everything else (whitespace, punctuation, math symbols like = + *,
//     decorative chars). This is deliberately an explicit KEEP-list rather than
//     unicode.IsPunct, because Punct misses math symbols (= is category Sm) and
//     a naive "strip all punct" would also strip the decimal point, breaking
//     "3.14" → "314" false-matches.
//
// Returns "" for empty/whitespace-only input. GradeFill treats "" as always
// wrong (a blank answer is never correct).
func NormalizeText(s string) string {
	var b []rune
	for _, r := range s {
		// Step 1: fold full-width ASCII (U+FF00..FFEF) to half-width first.
		if r >= 0xFF00 && r <= 0xFFEF {
			r = r - 0xFF00 + 0x20
		}
		// Step 2: lowercase ASCII letters.
		if r >= 'A' && r <= 'Z' {
			r = r - 'A' + 'a'
		}
		// Step 3: keep only meaningful content chars.
		if !keepChar(r) {
			continue
		}
		b = append(b, r)
	}
	return string(b)
}

// keepChar decides whether a (already folded + lowercased) rune survives
// normalization. True for content that distinguishes answers; false for noise.
func keepChar(r rune) bool {
	switch r {
	case '.', '-', '/': // math: decimals, negatives, fractions
		return true
	}
	// ASCII letters/digits
	if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
		return true
	}
	// CJK ideographs and CJK-adjacent ranges (answers like "十二")
	if r >= 0x4E00 && r <= 0x9FFF { // CJK Unified Ideographs
		return true
	}
	if r >= 0x3400 && r <= 0x4DBF { // CJK Extension A
		return true
	}
	return false
}
