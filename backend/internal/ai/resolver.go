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

// IsReady reports which capabilities have an enabled provider configured (does
// NOT build or ping them — that's the test-connection button's job). Used by the
// admin status endpoint to show "chat: ready / embedding: not configured".
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
			embed = true
		}
	}
	return chat, embed, nil
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

	row, err := r.enabledRow(model.AICapabilityEmbedding)
	if err != nil {
		return nil, err
	}
	p, err := r.buildEmbed(row)
	if err != nil {
		return nil, fmt.Errorf("build embedding provider %q: %w", row.Name, err)
	}
	r.cachedEmbed = &cachedProvider{rowID: row.ID, updatedAt: row.UpdatedAt, embedder: p}
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

// buildEmbed maps (provider_type, config) → Embedder. Today only onnx_local; an
// openai_compat embedding implementation would slot in here later without
// touching retrieval/agent code.
func (r *ProviderResolver) buildEmbed(row *model.AIProvider) (Embedder, error) {
	switch row.ProviderType {
	case "onnx_local":
		// ModelName is the model subdirectory under AIModelsDir (e.g.
		// "bge-small-zh-v1.5"). The conventional layout inside it is
		// model_quantized.onnx + vocab.txt.
		modelDir := joinPath(r.modelsDir, row.ModelName)
		// The onnxruntime shared library lives directly under AIModelsDir. Its
		// filename carries the version (libonnxruntime.so.1.26.0), so we DISCOVER
		// it rather than hardcoding the version here — that way bumping the
		// version is a single edit in the Makefile's ORT_VERSION + a re-fetch,
		// with no matching change needed in Go code. This avoids the "ABI version
		// mismatch" footgun where the .so and the onnxruntime_go build disagree.
		libPath, err := findOnnxRuntimeLib(r.modelsDir)
		if err != nil {
			return nil, fmt.Errorf("locate libonnxruntime in %s: %w (run 'make fetch-ai-models')", r.modelsDir, err)
		}
		cfg := OnnxEmbedderConfig{
			ModelPath: joinPath(modelDir, "model_quantized.onnx"),
			VocabPath: joinPath(modelDir, "vocab.txt"),
			LibPath:   libPath,
			SeqLen:    512,
			Dim:       512, // bge-small-zh; an extra_json override could go here later
		}
		return NewOnnxEmbedder(cfg)
	default:
		return nil, fmt.Errorf("unsupported embedding provider_type %q (only onnx_local is supported)", row.ProviderType)
	}
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
