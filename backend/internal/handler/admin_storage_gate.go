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
// user's allow-list. The list is default-deny: if the user's list is EMPTY,
// any content that has a source is refused (the admin must grant at least one
// source first). Content with no source dimension (empty course, reading
// articles) passes through unconditionally. A nil repo (feature not wired)
// short-circuits to allow.
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
		// Target has no source dimension (empty course / article) — nothing to
		// gate. Pass through.
		return nil
	}
	allowed, err := repo.WhitelistForUser(userID)
	if err != nil {
		return fmt.Errorf("check storage whitelist: %w", err)
	}
	if len(allowed) == 0 {
		// Default-deny: user is allowed no sources. Name the first one for a
		// helpful message so the admin knows what to grant.
		return &errStorageWhitelistDenied{msg: deniedMsg(repo, firstKey(needed))}
	}
	allowedSet := make(map[uint]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	for id := range needed {
		if _, ok := allowedSet[id]; ok {
			continue
		}
		return &errStorageWhitelistDenied{msg: deniedMsg(repo, id)}
	}
	return nil
}

// deniedMsg renders a Chinese denial message naming the offending source.
func deniedMsg(repo repository.StorageSourceRepository, id uint) string {
	name := fmt.Sprintf("source #%d", id)
	if src, _ := repo.FindByID(id); src != nil {
		name = src.Name
	}
	return fmt.Sprintf("该用户不被允许访问存储源「%s」", name)
}

func firstKey(m map[uint]struct{}) uint {
	for k := range m {
		return k
	}
	return 0
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
