package repository

import (
	"errors"
	"sort"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// StorageSourceRepository covers two related concerns:
//   - CRUD for StorageSource rows (the admin-configured netdisk backends), and
//   - the per-user storage-source allow-list (UserStorageSource), which is
//     default-deny: a user may access a source iff it is in their list, and an
//     EMPTY list means the user is allowed nothing. See IsAllowed.
//
// The whitelist is the 防呆 layer: even if an admin accidentally grants a
// course from source Y to a user whose list is [X], the grant-time gate
// refuses; the access-time gate refuses again at play-info/stream. Both gates
// go through IsAllowed so the default-deny semantics stay identical across
// modules.
type StorageSourceRepository interface {
	// ── StorageSource CRUD ──
	Create(s *model.StorageSource) error
	Update(s *model.StorageSource) error
	Delete(id uint) error
	FindByID(id uint) (*model.StorageSource, error)
	List() ([]model.StorageSource, error)
	// GetDefault returns the source flagged IsDefault, or (nil, nil) if none.
	// Used as the import-time default when the request doesn't specify one.
	GetDefault() (*model.StorageSource, error)
	// ClearDefault unsets IsDefault on every row (used before setting a new
	// default so at most one row carries the flag).
	ClearDefault() error
	// CountReferences counts episodes and reading books whose SourceID points
	// at this source. Used by the delete handler to REFUSE deletion of a source
	// that is still in use (otherwise those rows would silently lose their
	// provider and playback/import would break). Two COUNT(*) queries.
	CountReferences(sourceID uint) (episodes int64, books int64, err error)
	// WithTx returns a copy bound to an in-progress transaction.
	WithTx(tx *gorm.DB) StorageSourceRepository

	// ── User allow-list ──
	// WhitelistForUser returns the sorted source ids the user is allowed to
	// access. An empty slice means the user is allowed nothing (default-deny).
	WhitelistForUser(userID uint) ([]uint, error)
	// SetWhitelist replaces the user's allow-list wholesale (delete-then-insert
	// inside one transaction). An empty sourceIDs slice clears the list,
	// returning the user to default-deny (allowed nothing).
	SetWhitelist(userID uint, sourceIDs []uint) error
// IsAllowed reports whether the user may access the given source. The user's
// whitelist is an ALLOW-list: the user may access a source if and only if it
// appears in their whitelist. An EMPTY whitelist means the user is allowed
// NOTHING (every source is denied) — this is the default-deny posture; an
// admin must explicitly grant at least one source before the user can stream.
	IsAllowed(userID, sourceID uint) (bool, error)
}

type storageSourceRepo struct {
	db *gorm.DB
}

// NewStorageSourceRepository creates an instance of StorageSourceRepository.
func NewStorageSourceRepository(db *gorm.DB) StorageSourceRepository {
	return &storageSourceRepo{db: db}
}

func (r *storageSourceRepo) WithTx(tx *gorm.DB) StorageSourceRepository {
	return &storageSourceRepo{db: tx}
}

// ── StorageSource CRUD ──

func (r *storageSourceRepo) Create(s *model.StorageSource) error {
	return r.db.Create(s).Error
}

func (r *storageSourceRepo) Update(s *model.StorageSource) error {
	return r.db.Save(s).Error
}

func (r *storageSourceRepo) Delete(id uint) error {
	return r.db.Delete(&model.StorageSource{}, id).Error
}

func (r *storageSourceRepo) FindByID(id uint) (*model.StorageSource, error) {
	var s model.StorageSource
	if err := r.db.First(&s, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *storageSourceRepo) List() ([]model.StorageSource, error) {
	var sources []model.StorageSource
	if err := r.db.Order("is_default DESC, id ASC").Find(&sources).Error; err != nil {
		return nil, err
	}
	return sources, nil
}

func (r *storageSourceRepo) GetDefault() (*model.StorageSource, error) {
	var s model.StorageSource
	if err := r.db.Where("is_default = ?", true).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *storageSourceRepo) ClearDefault() error {
	return r.db.Model(&model.StorageSource{}).Where("is_default = ?", true).
		Update("is_default", false).Error
}

// CountReferences counts episodes and reading books bound to this source via
// their SourceID column. Neither has a FK to storage_sources (source_id is a
// loose indexed column with no constraint), so there is no DB-level guard
// preventing a delete from orphaning them. The admin delete handler uses these
// counts to refuse the operation with a 409 instead of leaving dangling rows
// that would silently break playback / re-import disaster recovery.
func (r *storageSourceRepo) CountReferences(sourceID uint) (int64, int64, error) {
	var episodes int64
	if err := r.db.Model(&model.Episode{}).Where("source_id = ?", sourceID).Count(&episodes).Error; err != nil {
		return 0, 0, err
	}
	var books int64
	if err := r.db.Model(&model.ReadingBook{}).Where("source_id = ?", sourceID).Count(&books).Error; err != nil {
		return 0, 0, err
	}
	return episodes, books, nil
}

// ── User whitelist ──

func (r *storageSourceRepo) WhitelistForUser(userID uint) ([]uint, error) {
	var rows []model.UserStorageSource
	if err := r.db.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.SourceID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// SetWhitelist replaces the user's whitelist wholesale. Same-source duplicate
// ids in the input are deduped; the composite PK makes the insert idempotent
// anyway, but deduping keeps the row count honest.
func (r *storageSourceRepo) SetWhitelist(userID uint, sourceIDs []uint) error {
	seen := make(map[uint]struct{}, len(sourceIDs))
	unique := make([]uint, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserStorageSource{}).Error; err != nil {
			return err
		}
		if len(unique) == 0 {
			return nil // empty list → default-deny (allowed nothing)
		}
		rows := make([]model.UserStorageSource, len(unique))
		for i, id := range unique {
			rows[i] = model.UserStorageSource{UserID: userID, SourceID: id}
		}
		return tx.Create(&rows).Error
	})
}

// IsAllowed is a pure membership check against the user's allow-list. The list
// is default-deny: an empty list (or a source not in it) → false.
func (r *storageSourceRepo) IsAllowed(userID, sourceID uint) (bool, error) {
	var allowed int64
	if err := r.db.Model(&model.UserStorageSource{}).
		Where("user_id = ? AND source_id = ?", userID, sourceID).
		Count(&allowed).Error; err != nil {
		return false, err
	}
	return allowed > 0, nil
}
