package ai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// OnnxEmbedder is the local Embedder implementation: it runs a quantized BERT
// ONNX model (BGE-small-zh-v1.5 by default) in-process via onnxruntime, with NO
// network dependency. This is what lets the embedding backend be fully decoupled
// from the chat LLM — even if the relay endpoint has no embedding support, RAG
// still works, and swapping to an API-backed embedder later is a resolver change
// only.
//
// Resource shape (measured during the feasibility check): the quantized BGE
// model is ~23MB on disk, resident memory ~67MB including the onnxruntime
// engine, and a single 32-token inference is ~1.6ms warm on CPU. That fits the
// 2c2g target with room to spare, and is fast enough to embed a whole episode's
// subtitle chunks (hundreds) in well under a second.
//
// The onnxruntime C library is loaded at runtime via dlopen (NOT linked at
// build time), so building the server needs no special CGo setup — the only
// requirement is that the .so is present on disk at the configured path. The
// Makefile fetches the exact version that matches this package's ABI (see
// SetSharedLibraryPath note below).
type OnnxEmbedder struct {
	modelPath   string // path to model_quantized.onnx
	vocabPath   string // path to vocab.txt
	libPath     string // path to libonnxruntime.so (must match onnxruntime_go's ABI version)
	tokenizer   *BertTokenizer
	seqLen      int // max tokens per input (512 = BGE's hard cap)
	dim         int // embedding dimensionality (512 for bge-small-zh)
	session     *ort.DynamicAdvancedSession
	mu          sync.Mutex // serializes Run; onnxruntime sessions are not concurrency-safe
	initialized bool       // onnxruntime environment + session loaded; guarded by mu (lazy init)
}

// OnnxEmbedderConfig configures an OnnxEmbedder. All paths must be absolute or
// resolvable from the server's working directory.
type OnnxEmbedderConfig struct {
	// ModelPath is the path to the quantized ONNX model file, e.g.
	// "./data/ai-models/bge-small-zh/model_quantized.onnx".
	ModelPath string
	// VocabPath is the path to vocab.txt matching the model (BERT WordPiece).
	VocabPath string
	// LibPath is the path to libonnxruntime.so.X.Y.Z (the real versioned file,
	// NOT the unversioned symlink). MUST match the ABI version onnxruntime_go
	// was built against — v1.31.0 pins ORT API version 26, so libonnxruntime
	// 1.26.0 is required. A version mismatch fails at InitializeEnvironment with
	// a cryptic C error, hence the emphasis.
	LibPath string
	// SeqLen is the max token sequence length. 512 is BGE's maximum; shorter is
	// faster but truncates long inputs. 512 is safe.
	SeqLen int
	// Dim is the model's output dimension (512 for bge-small-zh). Used to
	// preallocate and to report Dim() without running a dummy inference.
	Dim int
}

// NewOnnxEmbedder constructs an embedder WITHOUT loading the model yet. The
// model, tokenizer and onnxruntime library are loaded lazily on the first Embed
// / Ping call (see ensureInitialized). Lazy init means a misconfigured embedder
// doesn't crash server startup — the rest of the system runs fine with AI
// disabled, which is the core "add-on layer" guarantee.
func NewOnnxEmbedder(cfg OnnxEmbedderConfig) (*OnnxEmbedder, error) {
	if cfg.ModelPath == "" || cfg.VocabPath == "" || cfg.LibPath == "" {
		return nil, errors.New("onnx embedder: modelPath, vocabPath and libPath are all required")
	}
	if cfg.SeqLen <= 0 {
		cfg.SeqLen = 512
	}
	if cfg.Dim <= 0 {
		// bge-small-zh default; callers should set it explicitly to match their model.
		cfg.Dim = 512
	}
	return &OnnxEmbedder{
		modelPath: cfg.ModelPath,
		vocabPath: cfg.VocabPath,
		seqLen:    cfg.SeqLen,
		dim:       cfg.Dim,
		libPath:   cfg.LibPath,
	}, nil
}

func (e *OnnxEmbedder) ProviderType() string { return "onnx_local" }

// Dim returns the embedding dimensionality.
func (e *OnnxEmbedder) Dim() int { return e.dim }

// ensureInitialized performs lazy one-time setup:
//  1. Point onnxruntime at the shared library (dlopen) and initialize the
//     global ORT environment. This is process-global state in onnxruntime, so
//     it's done once even if multiple embedders exist (uncommon).
//  2. Load the BERT vocab.txt.
//  3. Open a DynamicAdvancedSession on the ONNX model file.
//
// All three are done under e.mu. If init fails, the error is returned but
// `initialized` stays false so the NEXT call retries — a transient failure
// (missing file at boot, fixed later) is recoverable rather than permanent.
// onnxRuntimeOnce + initOnnxRuntimeError make the onnxruntime PROCESS-GLOBAL
// environment initialize exactly once. The ORT environment is shared C state
// (one dlopen'd lib, one OrtEnv), so it must NOT be created/destroyed per
// OnnxEmbedder instance — calling InitializeEnvironment twice errors, and
// DestroyEnvironment from one embedder would yank the rug from under another.
// The first embedder that runs initializes it; the library path it sets is the
// one used process-wide (all embedders are expected to share the same .so).
var (
	onnxRuntimeOnce    sync.Once
	initOnnxRuntimeErr error
)

// initOnnxRuntime performs the one-time global ORT setup. Subsequent callers
// get the cached result (nil if it succeeded). Never tears down — the
// environment lives for the process lifetime.
func initOnnxRuntime(libPath string) error {
	onnxRuntimeOnce.Do(func() {
		ort.SetSharedLibraryPath(libPath)
		if err := ort.InitializeEnvironment(); err != nil {
			initOnnxRuntimeErr = fmt.Errorf("onnx: initialize environment (lib path=%s; check the .so version matches onnxruntime_go's ABI): %w", libPath, err)
		}
	})
	return initOnnxRuntimeErr
}

func (e *OnnxEmbedder) ensureInitialized() error {
	if e.initialized {
		return nil
	}
	// 1. onnxruntime GLOBAL environment (process-wide, once). The first embedder
	// to run sets the .so path; this is fine because all embedders share the same
	// libonnxruntime build (a single Makefile fetches it).
	if err := initOnnxRuntime(e.libPath); err != nil {
		return err
	}

	// 2. tokenizer (per-instance: different models may use different vocabs).
	tok, err := NewBertTokenizer(e.vocabPath)
	if err != nil {
		return err
	}
	e.tokenizer = tok

	// 3. ONNX session (per-instance). BERT models expose three named inputs
	// (input_ids, attention_mask, token_type_ids) and one output
	// (last_hidden_state, shape [batch, seq, dim]). "Dynamic" lets the seq
	// length vary per call; we still pad to e.seqLen for batching simplicity.
	// Note: we do NOT DestroyEnvironment on session failure — the environment
	// is process-global and outlives this instance.
	inputNames := []string{"input_ids", "attention_mask", "token_type_ids"}
	outputNames := []string{"last_hidden_state"}
	sess, err := ort.NewDynamicAdvancedSession(e.modelPath, inputNames, outputNames, nil)
	if err != nil {
		return fmt.Errorf("onnx: create session for %s: %w", e.modelPath, err)
	}
	e.session = sess
	e.initialized = true
	return nil
}

// Embed produces one normalized embedding per input text, in order.
//
// Pipeline per text (the standard BGE flow):
//  1. tokenize → input_ids / attention_mask / token_type_ids, padded to seqLen
//  2. ONNX forward → last_hidden_state [1, seqLen, dim]
//  3. mean-pool over the real tokens (attention_mask > 0), ignoring [PAD]
//  4. L2-normalize the pooled vector (BGE documents require this for cosine
//     similarity to equal dot product)
//
// Empty/nil input returns an empty slice. The context is accepted for API
// symmetry with LLMProvider, but onnxruntime inferences are not cancellable
// mid-call — a cancelled context is checked between texts, not within one.
func (e *OnnxEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureInitialized(); err != nil {
		return nil, err
	}

	out := make([][]float32, 0, len(texts))
	for i, text := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		vec, err := e.embedOne(text)
		if err != nil {
			return nil, fmt.Errorf("onnx: embed text #%d: %w", i, err)
		}
		out = append(out, vec)
	}
	return out, nil
}

// Ping forces lazy init (loads the .so, vocab, model) and runs one trivial
// embed. Used by the admin test-connection button to confirm the whole local
// stack works end to end.
func (e *OnnxEmbedder) Ping(ctx context.Context) error {
	_, err := e.Embed(ctx, []string{"测试"})
	return err
}

// embedOne runs the full pipeline for a single text. Caller holds e.mu.
func (e *OnnxEmbedder) embedOne(text string) ([]float32, error) {
	inputIDs, attentionMask, tokenTypeIDs := e.tokenizer.Encode(text, e.seqLen)

	// Build the three [1, seqLen] int64 input tensors.
	shape := ort.NewShape(1, int64(e.seqLen))
	inIDs, err := ort.NewTensor[int64](shape, inputIDs)
	if err != nil {
		return nil, fmt.Errorf("new input_ids tensor: %w", err)
	}
	defer inIDs.Destroy()
	inMask, err := ort.NewTensor[int64](shape, attentionMask)
	if err != nil {
		return nil, fmt.Errorf("new attention_mask tensor: %w", err)
	}
	defer inMask.Destroy()
	inTypes, err := ort.NewTensor[int64](shape, tokenTypeIDs)
	if err != nil {
		return nil, fmt.Errorf("new token_type_ids tensor: %w", err)
	}
	defer inTypes.Destroy()

	// Run: outputs[0] is nil, onnxruntime allocates it. We MUST Destroy it.
	outputs := []ort.Value{nil}
	if err := e.session.Run(
		[]ort.Value{inIDs, inMask, inTypes},
		outputs,
	); err != nil {
		return nil, fmt.Errorf("onnx run: %w", err)
	}
	// After Run, outputs[0] was replaced with the allocated tensor. Assert and
	// clean up.
	outTensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		if outputs[0] != nil {
			outputs[0].Destroy()
		}
		return nil, fmt.Errorf("onnx: unexpected output type %T (want *Tensor[float32])", outputs[0])
	}
	defer outTensor.Destroy()

	data := outTensor.GetData() // length seqLen*dim, row-major [seqLen][dim]
	return poolAndNormalize(data, attentionMask, e.dim), nil
}

// poolAndNormalize does mean-pooling over real tokens then L2 normalization.
//
// Why mean-pool (not [CLS]): BGE-small uses mean-pooling over the token
// representations weighted by attention_mask, then L2-normalizes. The [CLS]
// token is NOT the sentence embedding for BGE (unlike some sentence-bert
// variants). This matches the reference BGE usage.
//
// data is laid out as [seqLen][dim] flattened. attentionMask is [seqLen], 1 for
// real tokens. We sum the rows where mask==1 and divide by the count, then scale
// the vector to unit length.
func poolAndNormalize(data []float32, attentionMask []int64, dim int) []float32 {
	seqLen := len(attentionMask)
	if len(data) < seqLen*dim {
		// Defensive: should never happen if the model output shape matches.
		seqLen = len(data) / dim
	}
	accum := make([]float64, dim) // float64 accumulator for numerical stability
	count := 0
	for t := 0; t < seqLen; t++ {
		if attentionMask[t] == 0 {
			continue
		}
		count++
		off := t * dim
		for d := 0; d < dim; d++ {
			accum[d] += float64(data[off+d])
		}
	}
	if count == 0 {
		// All-pad input: return a zero vector (degenerate but well-shaped).
		out := make([]float32, dim)
		return out
	}
	// Mean.
	for d := 0; d < dim; d++ {
		accum[d] /= float64(count)
	}
	// L2 normalize.
	var norm float64
	for d := 0; d < dim; d++ {
		norm += accum[d] * accum[d]
	}
	norm = math.Sqrt(norm)
	out := make([]float32, dim)
	if norm > 0 {
		for d := 0; d < dim; d++ {
			out[d] = float32(accum[d] / norm)
		}
	}
	return out
}
