package ai

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// ProviderResolver turns stored AIProvider config rows into live LLMProvider /
// Embedder instances, with in-process caching. It is the AI equivalent of
// service.StorageProviderResolver: admin CRUD handlers call Invalidate after a
// config change, and every agent/embedding/segmentation call site resolves a
// provider by capability rather than constructing one directly.
//
// Design goals:
//   - The agent and retrieval code depend only on the LLMProvider/Embedder
//     INTERFACES, never on a concrete provider. Swapping the chat vendor from a
//     relay to (say) a local llama.cpp server is one new implementation + a
//     resolver switch case — no agent change. This mirrors the storage layer.
//   - Provider construction is memoized: a steady-state request resolves in O(1)
//     with no DB hit, since building an HTTP client (or, for ONNX, the resolved
//     path) per request would be wasteful.
//   - Missing providers are NOT fatal. If no enabled chat provider is configured,
//     ResolveChat returns ErrNoProvider — the caller surfaces "AI not configured"
//     rather than crashing. This is the "add-on layer" guarantee: the server
//     starts and runs fine with AI entirely off.
type ProviderResolver struct {
	providerRepo repository.AIProviderRepository // reads AIProvider rows
	modelsDir    string                          // base dir for ONNX artifacts (lib + model + vocab)

	mu          sync.Mutex
	cachedChat  *cachedProvider // last-built chat LLMProvider, keyed by the source row's identity
	cachedEmbed *cachedProvider // last-built Embedder
}

// cachedProvider ties a built provider instance to the config row identity it
// was built from, so Invalidate can tell whether a cached instance is stale.
type cachedProvider struct {
	rowID    uint       // the AIProvider.ID this was built from
	updatedAt time.Time // the AIProvider.UpdatedAt at build time
	chat     LLMProvider
	embedder Embedder
}

// NewProviderResolver constructs a resolver. modelsDir is the ONNX artifacts
// directory (config.AIModelsDir); only used when an onnx_local embedder is built.
func NewProviderResolver(repo repository.AIProviderRepository, modelsDir string) *ProviderResolver {
	return &ProviderResolver{providerRepo: repo, modelsDir: modelsDir}
}

// ErrNoProvider means no enabled provider is configured for the requested
// capability. Callers should map this to a clear "AI not configured" message
// (HTTP 503 for admin diagnostics, or a graceful empty result for the client).
var ErrNoProvider = errors.New("no enabled AI provider configured for this capability")

// ResolveChat returns the enabled chat LLMProvider, building+caching it on first
// use (or when the config row changed since last build). Returns ErrNoProvider
// if none is enabled.
func (r *ProviderResolver) ResolveChat() (LLMProvider, error) {
	return r.resolveChat()
}

// ResolveEmbedder returns the enabled embedding Embedder, building+caching on
// first use. Returns ErrNoProvider if none is enabled.
func (r *ProviderResolver) ResolveEmbedder() (Embedder, error) {
	return r.resolveEmbed()
}

// IsReady reports which capabilities are ready (does NOT build or ping them —
// that's the test-connection button's job). Used by the admin status endpoint to
// show "chat: ready / embedding: not configured".
//
//   - chat: an enabled chat provider row exists in the DB.
//   - embedding: the bundled local model files exist on disk (the local embedder
//     does NOT use a DB row — it's built from AIModelsDir, so readiness = files
//     present). An admin-configured external embedding row would also count.
func (r *ProviderResolver) IsReady() (chat, embed bool, err error) {
	providers, err := r.providerRepo.List()
	if err != nil {
		return false, false, err
	}
	for _, p := range providers {
		if !p.IsEnabled {
			continue
		}
		switch p.Capability {
		case model.AICapabilityChat:
			chat = true
		case model.AICapabilityEmbedding:
			// Only an EXTERNAL embedding row counts here; the onnx_local default
			// is not represented in the DB (readiness for it comes from the file
			// check below). An onnx_local row in the DB is a stale leftover from
			// before this cleanup and is intentionally ignored.
			if p.ProviderType != "onnx_local" {
				embed = true
			}
		}
	}
	// Bundled local embedder: ready iff the model files are on disk.
	if !embed && r.localEmbedderFilesPresent() {
		embed = true
	}
	return chat, embed, nil
}

// localEmbedderFilesPresent is a lightweight readiness probe for the bundled
// local ONNX embedder: true iff the .so, the model, and the vocab all exist
// under AIModelsDir. It does NOT load the model (that's expensive — left to the
// first ResolveEmbedder call / the test button). Used by IsReady so the admin
// status page can show "embedding: ready" without a full build.
func (r *ProviderResolver) localEmbedderFilesPresent() bool {
	if r.modelsDir == "" {
		return false
	}
	if _, err := findOnnxRuntimeLib(r.modelsDir); err != nil {
		return false
	}
	modelDir := joinPath(r.modelsDir, DefaultEmbeddingModel)
	if _, err := os.Stat(joinPath(modelDir, "model_quantized.onnx")); err != nil {
		return false
	}
	if _, err := os.Stat(joinPath(modelDir, "vocab.txt")); err != nil {
		return false
	}
	return true
}

// ChatModelName returns the model name of the enabled chat provider (e.g.
// "gpt-5.4-mini"), or "" if none is enabled. Used to stamp ai_runs/ai_summaries
// with which model produced a result, for observability. Reads the DB row
// (cheap, only called once per generation job) rather than threading the name
// through the LLMProvider interface.
func (r *ProviderResolver) ChatModelName() string {
	row, err := r.enabledRow(model.AICapabilityChat)
	if err != nil || row == nil {
		return ""
	}
	return row.ModelName
}

// Invalidate drops the cached provider for one capability. Called by the admin
// handler after a provider row is created/updated/deleted, so the next resolve
// rebuilds from the new config.
func (r *ProviderResolver) Invalidate(capability string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch capability {
	case model.AICapabilityChat:
		r.cachedChat = nil
	case model.AICapabilityEmbedding:
		r.cachedEmbed = nil
	}
}

// InvalidateAll drops every cached provider. Used on bulk config changes.
func (r *ProviderResolver) InvalidateAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cachedChat = nil
	r.cachedEmbed = nil
}

// --- internal resolution ---

func (r *ProviderResolver) resolveChat() (LLMProvider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Cache hit: return the previously built instance. Cache validity is
	// maintained by Invalidate() — every admin write path calls it, so in this
	// single-process server a cached instance is always current without needing
	// a DB check on every resolve. (The previous version queried the DB every
	// resolve to diff UpdatedAt, which defeated the cache.)
	if r.cachedChat != nil {
		return r.cachedChat.chat, nil
	}

	row, err := r.enabledRow(model.AICapabilityChat)
	if err != nil {
		return nil, err
	}
	p, err := r.buildChat(row)
	if err != nil {
		return nil, fmt.Errorf("build chat provider %q: %w", row.Name, err)
	}
	r.cachedChat = &cachedProvider{rowID: row.ID, updatedAt: row.UpdatedAt, chat: p}
	return p, nil
}

func (r *ProviderResolver) resolveEmbed() (Embedder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cachedEmbed != nil {
		return r.cachedEmbed.embedder, nil
	}

	// Embedding resolution prefers an admin-configured EXTERNAL embedding row
	// (e.g. a future openai_compat embedding provider) when one exists; otherwise
	// it falls back to the BUNDLED local ONNX model that ships in the docker
	// image. The local model is NOT stored in the ai_providers table — it's a
	// built-in default, configured purely by AIModelsDir (which points into the
	// image). This keeps the embedding subsystem out of the DB entirely: no seed
	// row, no admin config, no "data 目录下没有" confusion. Adding a remote
	// embedding API later = one ai_providers row (capability=embedding,
	// provider_type != onnx_local) + a buildEmbedExternal case — the local model
	// then stays as the default fallback.
	row, err := r.enabledRow(model.AICapabilityEmbedding)
	if err == nil && row != nil && row.ProviderType != "onnx_local" {
		p, berr := r.buildEmbedExternal(row)
		if berr != nil {
			return nil, fmt.Errorf("build external embedding provider %q: %w", row.Name, berr)
		}
		r.cachedEmbed = &cachedProvider{rowID: row.ID, updatedAt: row.UpdatedAt, embedder: p}
		return p, nil
	}

	// No external embedding configured → use the bundled local ONNX model.
	p, err := r.buildLocalEmbedder()
	if err != nil {
		return nil, err
	}
	// rowID=0 marks the local-bundled embedder (no backing DB row). Invalidate
	// still drops it correctly (it nils cachedEmbed regardless of rowID).
	r.cachedEmbed = &cachedProvider{rowID: 0, embedder: p}
	return p, nil
}

// enabledRow returns the (first) enabled AIProvider for a capability. If multiple
// are enabled we pick the lowest ID for determinism — MVP supports one active
// provider per capability; a future "failover" enhancement could rotate.
func (r *ProviderResolver) enabledRow(capability string) (*model.AIProvider, error) {
	providers, err := r.providerRepo.List()
	if err != nil {
		return nil, err
	}
	var best *model.AIProvider
	for i := range providers {
		p := &providers[i]
		if !p.IsEnabled || p.Capability != capability {
			continue
		}
		if best == nil || p.ID < best.ID {
			best = p
		}
	}
	if best == nil {
		return nil, ErrNoProvider
	}
	return best, nil
}

// buildChat is the single place that maps (provider_type, config) → LLMProvider.
// Adding a new vendor = one case here + its implementation file.
func (r *ProviderResolver) buildChat(row *model.AIProvider) (LLMProvider, error) {
	switch row.ProviderType {
	case "openai_compat":
		p := NewOpenAICompatProvider(row.BaseURL, row.APIKey)
		p.SetModel(row.ModelName)
		return p, nil
	default:
		return nil, fmt.Errorf("unsupported chat provider_type %q (only openai_compat is supported; local LLM is a future option)", row.ProviderType)
	}
}

// buildEmbedExternal maps an admin-configured EXTERNAL embedding provider row
// (provider_type != onnx_local) to an Embedder. Today no external embedding type
// is implemented — this is the documented extension hook. When a remote embedding
// API is added later, its case goes here and resolveEmbed's preference for it
// over the local default already works.
func (r *ProviderResolver) buildEmbedExternal(row *model.AIProvider) (Embedder, error) {
	return nil, fmt.Errorf("external embedding provider_type %q not implemented yet (only the bundled local ONNX model is supported; this row is a placeholder for a future remote embedding API)", row.ProviderType)
}

// buildLocalEmbedder constructs the bundled local ONNX BGE embedder from
// AIModelsDir. This is the default embedding backend — it ships in the docker
// image and requires no ai_providers row. ModelName is a fixed constant (the
// bundled model subdir), not admin-configurable.
func (r *ProviderResolver) buildLocalEmbedder() (Embedder, error) {
	modelDir := joinPath(r.modelsDir, DefaultEmbeddingModel)
	// The onnxruntime shared library lives directly under AIModelsDir. Its
	// filename carries the version (libonnxruntime.so.1.26.0), so we DISCOVER it
	// rather than hardcoding the version — bumping the version is a single edit
	// in the Makefile/Dockerfile, no Go change. This avoids the "ABI version
	// mismatch" footgun where the .so and the onnxruntime_go build disagree.
	libPath, err := findOnnxRuntimeLib(r.modelsDir)
	if err != nil {
		return nil, fmt.Errorf("locate libonnxruntime in %s: %w (模型文件不在镜像/卷里——检查 AI_MODELS_DIR 是否指向正确路径)", r.modelsDir, err)
	}
	// Sanity-check the model files exist too, so a missing .onnx/vocab fails here
	// with a clear message rather than deep inside the ONNX runtime.
	modelPath := joinPath(modelDir, "model_quantized.onnx")
	vocabPath := joinPath(modelDir, "vocab.txt")
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("embedding model not found at %s: %w (检查 AI_MODELS_DIR / docker 卷挂载)", modelPath, err)
	}
	if _, err := os.Stat(vocabPath); err != nil {
		return nil, fmt.Errorf("embedding vocab not found at %s: %w (检查 AI_MODELS_DIR / docker 卷挂载)", vocabPath, err)
	}
	cfg := OnnxEmbedderConfig{
		ModelPath: modelPath,
		VocabPath: vocabPath,
		LibPath:   libPath,
		SeqLen:    512,
		Dim:       512, // bge-small-zh; an extra_json override could go here later
	}
	return NewOnnxEmbedder(cfg)
}

// findOnnxRuntimeLib locates the versioned onnxruntime shared library inside
// dir. onnxruntime releases name it "libonnxruntime.so.X.Y.Z" (the real file —
// NOT the unversioned "libonnxruntime.so" symlink, which the dlopen loader
// resolves but onnxruntime_go's ABI check needs the exact versioned path). We
// match the versioned pattern so a future version bump needs no code change.
//
// If multiple versioned libs exist (e.g. an old fetch wasn't cleaned up), the
// highest-named one wins by lexical sort — acceptable since only one .so should
// be present in a correctly-fetched models dir.
func findOnnxRuntimeLib(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var best string
	for _, e := range entries {
		name := e.Name()
		// Match libonnxruntime.so.X.Y.Z but NOT the bare libonnxruntime.so link.
		if !strings.HasPrefix(name, "libonnxruntime.so.") {
			continue
		}
		if best == "" || name > best {
			best = name
		}
	}
	if best == "" {
		return "", fmt.Errorf("no libonnxruntime.so.* found")
	}
	return joinPath(dir, best), nil
}

// DefaultEmbeddingModel is the ONNX embedding model bundled into the docker
// image (a subdir under AIModelsDir). The local embedder is built directly from
// this constant + AIModelsDir — it is NOT stored in the ai_providers table, so
// the admin never configures it and there's no startup seed. Bumping the model
// = changing this constant + the Dockerfile/Makefile fetch (keep them in sync).
const DefaultEmbeddingModel = "bge-small-zh-v1.5"
