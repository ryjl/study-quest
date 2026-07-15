package repository

import (
	"errors"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// AIProviderRepository is the CRUD surface for admin-configured AI providers
// (the ai_providers table). Modeled on StorageSourceRepository: interface + impl
// + New constructor, all in one file. Used by the ProviderResolver (reads) and
// the admin AI handler (full CRUD).
//
// Only provider rows are handled here; the heavier AI tables (jobs, chunks,
// memory, quizzes, runs) get their own repo types as they're implemented in
// later phases, to keep each concern isolated.
type AIProviderRepository interface {
	Create(p *model.AIProvider) error
	Update(p *model.AIProvider) error
	Delete(id uint) error
	FindByID(id uint) (*model.AIProvider, error)
	List() ([]model.AIProvider, error)
	ListByCapability(capability string) ([]model.AIProvider, error)
	WithTx(tx *gorm.DB) AIProviderRepository
}

type aiProviderRepo struct {
	db *gorm.DB
}

// NewAIProviderRepository creates an AIProviderRepository bound to db.
func NewAIProviderRepository(db *gorm.DB) AIProviderRepository {
	return &aiProviderRepo{db: db}
}

func (r *aiProviderRepo) WithTx(tx *gorm.DB) AIProviderRepository {
	return &aiProviderRepo{db: tx}
}

func (r *aiProviderRepo) Create(p *model.AIProvider) error {
	return r.db.Create(p).Error
}

func (r *aiProviderRepo) Update(p *model.AIProvider) error {
	return r.db.Save(p).Error
}

func (r *aiProviderRepo) Delete(id uint) error {
	return r.db.Delete(&model.AIProvider{}, id).Error
}

func (r *aiProviderRepo) FindByID(id uint) (*model.AIProvider, error) {
	var p model.AIProvider
	if err := r.db.First(&p, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// List returns all providers ordered by capability then id, so the admin UI
// groups them predictably (chat providers together, then embedding, then rerank).
func (r *aiProviderRepo) List() ([]model.AIProvider, error) {
	var providers []model.AIProvider
	if err := r.db.Order("capability ASC, id ASC").Find(&providers).Error; err != nil {
		return nil, err
	}
	return providers, nil
}

func (r *aiProviderRepo) ListByCapability(capability string) ([]model.AIProvider, error) {
	var providers []model.AIProvider
	if err := r.db.Where("capability = ?", capability).Order("id ASC").Find(&providers).Error; err != nil {
		return nil, err
	}
	return providers, nil
}
