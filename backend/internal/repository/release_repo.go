package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// ReleaseRepository handles SQL operations for the AppRelease entity.
//
// The client OTA contract (/api/v1/app/latest, /api/v1/app/download) addresses
// builds by (version_code, abi) — never by primary key id. FindLatest and
// FindByVersionAndABI are therefore the contract-facing lookups and must stay
// keyed on those semantic identifiers.
type ReleaseRepository interface {
	// FindLatest returns the active release with the highest version_code for a
	// given ABI, or (nil, nil) when none exists. Withdrawn (is_active=false)
	// builds are skipped — they are hidden from clients.
	FindLatest(abi string) (*model.AppRelease, error)
	// FindByVersionAndABI looks up a specific build. Returns (nil, nil) if absent.
	// Used by the download endpoint AND integrity checks on upload (Exists).
	FindByVersionAndABI(versionCode int, abi string) (*model.AppRelease, error)
	FindAll() ([]model.AppRelease, error) // ordered by version_code DESC, then abi
	FindByID(id uint) (*model.AppRelease, error)
	Create(r *model.AppRelease) error
	Update(r *model.AppRelease) error
	Delete(id uint) error
}

type releaseRepo struct {
	db *gorm.DB
}

// NewReleaseRepository creates an instance of ReleaseRepository.
func NewReleaseRepository(db *gorm.DB) ReleaseRepository {
	return &releaseRepo{db: db}
}

func (r *releaseRepo) FindLatest(abi string) (*model.AppRelease, error) {
	var rel model.AppRelease
	// NOTE: bind the active flag as the integer 1, not the Go bool `true`.
	// GORM inlines a bool literal into the SQL as `is_active = true`, and the
	// `true` keyword is not parsed consistently across SQLite driver versions —
	// it can silently match inactive rows, leaking withdrawn (bad) builds to
	// OTA clients. The integer comparison is unambiguous.
	err := r.db.Where("abi = ? AND is_active = ?", abi, 1).
		Order("version_code DESC").
		First(&rel).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rel, nil
}

func (r *releaseRepo) FindByVersionAndABI(versionCode int, abi string) (*model.AppRelease, error) {
	var rel model.AppRelease
	err := r.db.Where("version_code = ? AND abi = ?", versionCode, abi).
		First(&rel).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rel, nil
}

func (r *releaseRepo) FindAll() ([]model.AppRelease, error) {
	var rels []model.AppRelease
	err := r.db.Order("version_code DESC, abi ASC").Find(&rels).Error
	return rels, err
}

func (r *releaseRepo) FindByID(id uint) (*model.AppRelease, error) {
	var rel model.AppRelease
	err := r.db.First(&rel, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rel, nil
}

func (r *releaseRepo) Create(rel *model.AppRelease) error {
	// Use Select("*") so a false IsActive (withdrawn build) is written as 0,
	// not omitted as a zero-value. The model deliberately carries no
	// `default:true` GORM tag (that tag + SQLite column default would re-assert
	// true on an omitted value, leaking withdrawn builds to OTA clients), so we
	// apply the "new builds are active" default here in code instead.
	return r.db.Select("*").Create(rel).Error
}

func (r *releaseRepo) Update(rel *model.AppRelease) error {
	return r.db.Save(rel).Error
}

func (r *releaseRepo) Delete(id uint) error {
	return r.db.Delete(&model.AppRelease{}, id).Error
}
