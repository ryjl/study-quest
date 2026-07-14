package handler

import (
	"fmt"

	"studyquest/backend/internal/repository"
)

// errStorageWhitelistDenied is returned by the grant-time gate when an admin
// tries to grant a user access to content whose storage source is not in the
// user's whitelist. The message is surfaced verbatim to the admin.
type errStorageWhitelistDenied struct{ msg string }

func (e *errStorageWhitelistDenied) Error() string { return e.msg }

// checkStorageWhitelist is the grant-time 防呆 gate. It computes the set of
// storage sources the target content lives on, then verifies each is in the
// user's whitelist. Returns nil if everything is allowed (including when the
// whitelist is empty = unrestricted, and when the target has no source
// dimension — e.g. reading articles). Returns errStorageWhitelistDenied with a
// specific message naming the first offending source otherwise.
//
// sourceIDs may contain zeros (content with no SourceID set); those are
// ignored — legacy NULL-source rows ride the global fallback and are never
// gated. A nil repo (source feature not wired) short-circuits to allow.
func checkStorageWhitelist(repo repository.StorageSourceRepository, userID uint, sourceIDs []uint) error {
	if repo == nil {
		return nil
	}
	// Collect the distinct non-zero source ids the grant would expose.
	needed := make(map[uint]struct{})
	for _, id := range sourceIDs {
		if id != 0 {
			needed[id] = struct{}{}
		}
	}
	if len(needed) == 0 {
		return nil // no source dimension on any target row
	}
	allowed, err := repo.WhitelistForUser(userID)
	if err != nil {
		return fmt.Errorf("check storage whitelist: %w", err)
	}
	if len(allowed) == 0 {
		return nil // empty whitelist = unrestricted (backward compatible)
	}
	allowedSet := make(map[uint]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	// Resolve names for a helpful error message.
	for id := range needed {
		if _, ok := allowedSet[id]; ok {
			continue
		}
		name := fmt.Sprintf("source #%d", id)
		if src, _ := repo.FindByID(id); src != nil {
			name = src.Name
		}
		return &errStorageWhitelistDenied{msg: fmt.Sprintf("该用户不被允许访问存储源「%s」", name)}
	}
	return nil
}

// courseSourceSet returns the distinct source ids of every episode in the
// course (zeros filtered by the caller). Used by the course grant gate.
func courseSourceSet(episodeRepo repository.EpisodeRepository, courseID uint) ([]uint, error) {
	eps, err := episodeRepo.ListByCourse(courseID)
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(eps))
	for _, ep := range eps {
		if ep.SourceID != nil {
			ids = append(ids, *ep.SourceID)
		}
	}
	return ids, nil
}
