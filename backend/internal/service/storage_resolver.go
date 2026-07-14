package service

import (
	"errors"
	"fmt"
	"sync"

	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/storage"
)

// StorageProviderResolver turns a (possibly nil) SourceID into a concrete
// storage.StorageProvider. It centralizes the construction logic that was
// previously copy-pasted across four services as getActiveProvider().
//
// Resolution order:
//  1. sourceID non-nil → look up the StorageSource row, construct an
//     AList/WebDAV provider from its fields.
//  2. sourceID nil (legacy row, or admin hasn't configured sources yet) →
//     fall back to the global storage_* settings keys. This keeps the existing
//     dev database working through the transition.
//  3. Neither yields usable config → error.
//
// Constructed providers are cached in-process (keyed by source row identity +
// the resolved settings for the nil path) so repeated play-info/stream
// resolutions don't re-read the DB or rebuild HTTP clients. The admin mutation
// handlers call Invalidate / InvalidateSettings after a source/settings change
// so stale entries don't persist.
type StorageProviderResolver struct {
	sourceRepo   repository.StorageSourceRepository
	settingsRepo repository.SettingsRepository

	mu     sync.RWMutex
	byID   map[uint]storage.StorageProvider // sourceID → provider
	settingSnap *settingSnapshot            // provider for the nil (legacy) path
}

// settingSnapshot captures the 5 global storage_* settings keys at the moment
// a legacy provider was built, so we can detect drift on the next Resolve(nil)
// and rebuild (instead of serving a provider wired to stale credentials).
type settingSnapshot struct {
	typ, url, user, pass, token string
	provider                    storage.StorageProvider
}

// NewStorageProviderResolver constructs a resolver. Both repos are required.
func NewStorageProviderResolver(sourceRepo repository.StorageSourceRepository, settingsRepo repository.SettingsRepository) *StorageProviderResolver {
	return &StorageProviderResolver{
		sourceRepo:   sourceRepo,
		settingsRepo: settingsRepo,
		byID:         make(map[uint]storage.StorageProvider),
	}
}

// Invalidate drops the cached provider for one source. Call after editing a
// source's credentials/type so the next Resolve rebuilds it.
func (r *StorageProviderResolver) Invalidate(sourceID uint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, sourceID)
}

// InvalidateAll drops every cached provider (per-source and the settings
// fallback). Call after bulk source changes.
func (r *StorageProviderResolver) InvalidateAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID = make(map[uint]storage.StorageProvider)
	r.settingSnap = nil
}

// InvalidateSettings drops only the legacy settings-fallback cache. Call after
// the admin updates the global storage_* settings keys.
func (r *StorageProviderResolver) InvalidateSettings() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settingSnap = nil
}

// Resolve returns the provider for sourceID. A nil sourceID selects the legacy
// settings fallback; callers that already hold the content row should prefer
// ResolveForSourceID.
func (r *StorageProviderResolver) Resolve(sourceID *uint) (storage.StorageProvider, error) {
	if sourceID == nil {
		return r.resolveFromSettings()
	}
	return r.resolveFromSource(*sourceID)
}

func (r *StorageProviderResolver) resolveFromSource(sourceID uint) (storage.StorageProvider, error) {
	r.mu.RLock()
	if p, ok := r.byID[sourceID]; ok {
		r.mu.RUnlock()
		return p, nil
	}
	r.mu.RUnlock()

	src, err := r.sourceRepo.FindByID(sourceID)
	if err != nil {
		return nil, fmt.Errorf("lookup storage source %d: %w", sourceID, err)
	}
	if src == nil {
		return nil, fmt.Errorf("storage source %d not found", sourceID)
	}
	p, err := buildProvider(src.Type, src.URL, src.Username, src.Password, src.Token)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.byID[sourceID] = p
	r.mu.Unlock()
	return p, nil
}

func (r *StorageProviderResolver) resolveFromSettings() (storage.StorageProvider, error) {
	typ := r.settingsRepo.GetWithDefault("storage_type", "alist")
	url := r.settingsRepo.GetWithDefault("storage_url", "http://localhost:5244")
	user, _ := r.settingsRepo.Get("storage_username")
	pass, _ := r.settingsRepo.Get("storage_password")
	token, _ := r.settingsRepo.Get("storage_token")

	r.mu.RLock()
	snap := r.settingSnap
	r.mu.RUnlock()
	if snap != nil && snap.typ == typ && snap.url == url && snap.user == user && snap.pass == pass && snap.token == token {
		return snap.provider, nil
	}

	p, err := buildProvider(typ, url, user, pass, token)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.settingSnap = &settingSnapshot{typ: typ, url: url, user: user, pass: pass, token: token, provider: p}
	r.mu.Unlock()
	return p, nil
}

// buildProvider is the single place that maps (type, credentials) → provider.
// Shared by the source-row path and the settings-fallback path so the two
// never drift on how alist vs webdav is constructed.
func buildProvider(typ, url, user, pass, token string) (storage.StorageProvider, error) {
	switch typ {
	case "alist":
		return storage.NewAListProvider(url, user, pass, token), nil
	case "webdav":
		return storage.NewWebDAVProvider(url, user, pass), nil
	default:
		return nil, errors.New("unsupported storage_type configured: " + typ)
	}
}
