package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// SettingsRepository defines database operations for system configuration.
type SettingsRepository interface {
	Get(key string) (string, error)
	GetWithDefault(key, defaultValue string) string
	Set(key, value, description string) error
	GetAll() (map[string]string, error)
}

type settingsRepo struct {
	db *gorm.DB
}

// NewSettingsRepository creates an implementation of SettingsRepository.
func NewSettingsRepository(db *gorm.DB) SettingsRepository {
	return &settingsRepo{db: db}
}

func (r *settingsRepo) Get(key string) (string, error) {
	var setting model.Setting
	err := r.db.First(&setting, "key = ?", key).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return setting.Value, nil
}

func (r *settingsRepo) GetWithDefault(key, defaultValue string) string {
	val, err := r.Get(key)
	if err != nil || val == "" {
		return defaultValue
	}
	return val
}

func (r *settingsRepo) Set(key, value, description string) error {
	var setting model.Setting
	err := r.db.First(&setting, "key = ?", key).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Create new
			setting = model.Setting{
				Key:         key,
				Value:       value,
				Description: description,
			}
			return r.db.Create(&setting).Error
		}
		return err
	}

	// Update existing
	setting.Value = value
	if description != "" {
		setting.Description = description
	}
	return r.db.Save(&setting).Error
}

func (r *settingsRepo) GetAll() (map[string]string, error) {
	var settings []model.Setting
	if err := r.db.Find(&settings).Error; err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}
