package service

import (
	"errors"
	"fmt"
	"sync"

	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/storage"
)

// StorageProviderResolver turns a SourceID into a concrete
// storage.StorageProvider. It centralizes the construction logic that was
// previously copy-pasted across four services as getActiveProvider().
//
// Every content row (episode / reading_book) MUST carry a non-nil SourceID;
// a nil SourceID is an error (there is no longer a global-settings fallback —
// that transition path was removed once every deployment migrated to
// storage_sources).
//
// Constructed providers are cached in-process (keyed by source row identity)
// so repeated play-info/stream resolutions don't re-read the DB or rebuild
// HTTP clients. The admin mutation handlers call Invalidate after a source
// change so stale entries don't persist.
type StorageProviderResolver struct {
	sourceRepo repository.StorageSourceRepository

	mu   sync.RWMutex
	byID map[uint]storage.StorageProvider // sourceID → provider
}

// NewStorageProviderResolver constructs a resolver.
func NewStorageProviderResolver(sourceRepo repository.StorageSourceRepository) *StorageProviderResolver {
	return &StorageProviderResolver{
		sourceRepo: sourceRepo,
		byID:       make(map[uint]storage.StorageProvider),
	}
}

// Invalidate drops the cached provider for one source. Call after editing a
// source's credentials/type so the next Resolve rebuilds it.
func (r *StorageProviderResolver) Invalidate(sourceID uint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, sourceID)
}

// InvalidateAll drops every cached provider. Call after bulk source changes.
func (r *StorageProviderResolver) InvalidateAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID = make(map[uint]storage.StorageProvider)
}

// Resolve returns the provider for sourceID. A nil sourceID is an error —
// callers must resolve a content row's SourceID before streaming.
func (r *StorageProviderResolver) Resolve(sourceID *uint) (storage.StorageProvider, error) {
	if sourceID == nil {
		return nil, errors.New("no storage source: content row has nil SourceID (run backfill_sources or re-import)")
	}
	id := *sourceID
	r.mu.RLock()
	if p, ok := r.byID[id]; ok {
		r.mu.RUnlock()
		return p, nil
	}
	r.mu.RUnlock()

	src, err := r.sourceRepo.FindByID(id)
	if err != nil {
		return nil, fmt.Errorf("lookup storage source %d: %w", id, err)
	}
	if src == nil {
		return nil, fmt.Errorf("storage source %d not found", id)
	}
	p, err := buildProvider(src.Type, src.URL, src.Username, src.Password, src.Token)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.byID[id] = p
	r.mu.Unlock()
	return p, nil
}

// buildProvider is the single place that maps (type, credentials) → provider.
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
