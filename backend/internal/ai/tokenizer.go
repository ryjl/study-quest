package ai

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// BertTokenizer is a hand-written BERT-style WordPiece tokenizer.
//
// The BGE embedding model family (BGE-small-zh-v1.5 etc.) uses a standard
// BERT tokenizer: each input is wrapped with [CLS] ... [SEP], every CJK
// character is its own token, Latin/digit runs go through greedy longest-match
// WordPiece against the vocab, and unknown pieces become [UNK]. The three
// tensors a BERT ONNX model expects — input_ids, attention_mask, token_type_ids
// — are all derived from the token id sequence.
//
// Why hand-write this instead of pulling a tokenizer library:
//   1. The Go ecosystem has no canonical BERT tokenizer; the well-known ones
//      either pull huge transitive deps or are unmaintained.
//   2. For Chinese BERT specifically the logic is small (CJK = char-by-char,
//      Latin = greedy WordPiece). ~80 lines covers it.
//   3. Keeping it in-repo means no extra dependency and the exact tokenization
//      is inspectable/debuggable — important when an embedding "looks wrong" and
//      you need to see how the input was chopped up.
//
// The vocab.txt format is one token per line (HuggingFace bert-base-chinese /
// BGE convention); the 0-based line number is the token id. Special tokens
// occupy fixed positions: [PAD]=0, [UNK]=100, [CLS]=101, [SEP]=102 in those
// vocabs.
type BertTokenizer struct {
	vocab    map[string]int // token → id
	idsToTok map[int]string // id → token (only needed for debugging; kept small)
	clsID    int
	sepID    int
	unkID    int
	padID    int
}

// Special token literals as they appear in vocab.txt.
const (
	tokCLS = "[CLS]"
	tokSEP = "[SEP]"
	tokUNK = "[UNK]"
	tokPAD = "[PAD]"
)

// NewBertTokenizer loads vocab.txt and indexes it. vocabPath is the path to a
// HuggingFace-style vocab.txt (one token per line). Returns an error if the
// file can't be read or is empty.
func NewBertTokenizer(vocabPath string) (*BertTokenizer, error) {
	f, err := os.Open(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("open vocab file %q: %w", vocabPath, err)
	}
	defer f.Close()

	vocab := make(map[string]int)
	idsToTok := make(map[int]string)
	scanner := bufio.NewScanner(f)
	// bert-base-chinese vocab.txt is ~21128 lines, well under the default 64KB
	// scanner buffer, but bump it defensively in case a custom vocab has long
	// lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	id := 0
	for scanner.Scan() {
		tok := strings.TrimRight(scanner.Text(), "\r")
		vocab[tok] = id
		idsToTok[id] = tok
		id++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan vocab file: %w", err)
	}
	if len(vocab) == 0 {
		return nil, fmt.Errorf("vocab file %q is empty", vocabPath)
	}

	t := &BertTokenizer{vocab: vocab, idsToTok: idsToTok}
	// Special-token ids are looked up rather than hardcoded so this works with
	// any compatible vocab, not just the canonical bert-base-chinese layout.
	t.clsID = vocab[tokCLS]
	t.sepID = vocab[tokSEP]
	t.unkID = vocab[tokUNK]
	t.padID = vocab[tokPAD]
	return t, nil
}

// VocabSize returns the number of tokens in the loaded vocab.
func (t *BertTokenizer) VocabSize() int { return len(t.vocab) }

// Encode turns raw text into the three parallel int64 slices a BERT ONNX model
// consumes, padded/truncated to exactly seqLen:
//   - input_ids:       the token id sequence, with [CLS] and [SEP], padded to seqLen with [PAD]
//   - attention_mask:  1 for real tokens (incl. [CLS]/[SEP]), 0 for [PAD]
//   - token_type_ids:  0 everywhere (single-sentence input; sentence-pair would use 0/1 segments)
//
// seqLen must be > 2 (room for at least [CLS] + one token + [SEP]). Text longer
// than seqLen-2 tokens is TRUNCATED — BGE models have a hard 512-position cap,
// so the caller should pick a seqLen that fits the model (512 is the safe max).
//
// The three slices always have length exactly seqLen, so they drop straight into
// an ONNX input tensor of shape [1, seqLen].
func (t *BertTokenizer) Encode(text string, seqLen int) (inputIDs, attentionMask, tokenTypeIDs []int64) {
	if seqLen <= 2 {
		// Degenerate: no room for content. Return an all-pad sequence so callers
		// get a well-shaped (if meaningless) tensor rather than crashing.
		return filled(seqLen, int64(t.padID)), filled(seqLen, 0), filled(seqLen, 0)
	}

	rawTokens := t.tokenize(text)

	// Reserve 2 slots for [CLS] and [SEP]; truncate content if it overflows.
	maxContent := seqLen - 2
	if len(rawTokens) > maxContent {
		rawTokens = rawTokens[:maxContent]
	}

	// Assemble: [CLS] content... [SEP] [PAD]...
	tokens := make([]string, 0, seqLen)
	tokens = append(tokens, tokCLS)
	tokens = append(tokens, rawTokens...)
	tokens = append(tokens, tokSEP)

	inputIDs = make([]int64, seqLen)
	attentionMask = make([]int64, seqLen)
	tokenTypeIDs = make([]int64, seqLen) // all-zero for single-sentence
	for i, tok := range tokens {
		inputIDs[i] = int64(t.idOf(tok))
		attentionMask[i] = 1
	}
	// Remaining slots stay [PAD] (id 0) with mask 0, already zero-initialized.
	return inputIDs, attentionMask, tokenTypeIDs
}

// tokenize splits raw text into WordPiece tokens (BEFORE special-token wrapping
// and padding). CJK characters are each their own token; contiguous Latin/digit
// runs form a word that is then greedy-longest-match WordPieced against vocab.
//
// This mirrors HuggingFace's BertTokenizer logic closely enough for BGE: the
// embedding quality is not sensitive to rare tokenizer edge cases (e.g. exactly
// how CJK punctuation is split), and any minor divergence shows up as a slightly
// different vector — still a valid embedding, just not bit-identical to the
// Python reference. That tradeoff is accepted to keep this dependency-free.
func (t *BertTokenizer) tokenize(text string) []string {
	var tokens []string
	var latinRun strings.Builder

	flushLatin := func() {
		if latinRun.Len() == 0 {
			return
		}
		// WordPiece a Latin/digit word: try the whole word first, then peel off
		// the longest matching prefix each step, marking continuations with "##".
		// "playing" -> ["playing"] if in vocab, else ["play","##ing"] etc.
		word := latinRun.String()
		latinRun.Reset()
		if word[0] == '#' && (len(word) == 1 || word[1] != '#') {
			// Literal '#' that wasn't part of "##": emit as a standalone token to
			// avoid confusion with the continuation marker.
			tokens = append(tokens, t.idOrUNK(word))
			return
		}
		tokens = append(tokens, t.wordPiece(word)...)
	}

	for _, r := range text {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flushLatin()
		case isLatinAlnum(r):
			// ASCII letter/digit: accumulate into the current Latin run (lowercased).
			latinRun.WriteRune(unicode.ToLower(r))
		case unicode.Is(unicode.Han, r) || unicode.IsLetter(r) || unicode.IsDigit(r):
			// CJK or other letter/digit: ends any Latin run, then is its own token.
			flushLatin()
			tokens = append(tokens, t.idOrUNK(string(r)))
		default:
			// Punctuation / symbols: flush any in-progress Latin run, then emit
			// the symbol as its own token (BERT splits on punctuation).
			flushLatin()
			tokens = append(tokens, t.idOrUNK(string(r)))
		}
	}
	flushLatin()
	return tokens
}

// wordPiece applies greedy longest-match WordPiece to a lowercase Latin/digit
// word, splitting it into [head, "##"continuation...] tokens. Each piece is
// looked up in vocab; a piece not in vocab becomes [UNK] for the WHOLE word
// (standard BERT behavior: WordPiece failure rejects the entire word).
func (t *BertTokenizer) wordPiece(word string) []string {
	// Fast path: the whole word is in vocab.
	if _, ok := t.vocab[word]; ok {
		return []string{word}
	}

	// Slow path: greedy longest-match from the start; continuations prefixed "##".
	var pieces []string
	remaining := word
	first := true
	for len(remaining) > 0 {
		var match string
		matchLen := 0
		// Try the longest possible prefix down to length 1.
		for end := len(remaining); end > 0; end-- {
			candidate := remaining[:end]
			if !first {
				candidate = "##" + candidate
			}
			if _, ok := t.vocab[candidate]; ok {
				match = candidate
				matchLen = end
				break
			}
		}
		if matchLen == 0 {
			// No sub-piece matched: the whole word is [UNK].
			return []string{tokUNK}
		}
		pieces = append(pieces, match)
		remaining = remaining[matchLen:]
		first = false
	}
	return pieces
}

// idOrUNK maps a pre-split token to its vocab id, falling back to [UNK] if the
// exact token string isn't present. Used for CJK chars and punctuation, which
// are looked up as-is (no WordPiece).
func (t *BertTokenizer) idOrUNK(tok string) string {
	if _, ok := t.vocab[tok]; ok {
		return tok
	}
	return tokUNK
}

// idOf returns the vocab id for a token, defaulting to [UNK]'s id. Tokens passed
// here are already the result of tokenize()/wordPiece(), so the only miss case
// is a stray [UNK].
func (t *BertTokenizer) idOf(tok string) int {
	if id, ok := t.vocab[tok]; ok {
		return id
	}
	return t.unkID
}

// isLatinAlnum reports whether r is an ASCII letter or digit — the set that
// forms a "word" run to be WordPieced (as opposed to CJK chars, which are
// standalone tokens). Non-ASCII letters (accented, etc.) are treated as
// standalone tokens like CJK.
func isLatinAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// filled returns an int64 slice of length n where every element is v. Small
// helper to keep Encode readable.
func filled(n int, v int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = v
	}
	return out
}
