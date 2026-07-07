package repository

import (
	"studyquest/backend/internal/model"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open in-memory SQLite DB: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		t.Fatalf("Failed to run schema migration: %v", err)
	}

	return db
}

// seedTestSubjects inserts the canonical subject set and returns a key→Subject
// map, so test fixtures can reference a real SubjectID when building Courses.
func seedTestSubjects(t *testing.T, db *gorm.DB) map[string]model.Subject {
	t.Helper()
	defaults := []model.Subject{
		{Key: "chinese", Label: "语文", SortOrder: 1},
		{Key: "math", Label: "数学", SortOrder: 2},
		{Key: "english", Label: "英语", SortOrder: 3},
		{Key: "physics", Label: "物理/科学", SortOrder: 4},
	}
	for i := range defaults {
		if err := db.Create(&defaults[i]).Error; err != nil {
			t.Fatalf("seed subject %s: %v", defaults[i].Key, err)
		}
	}
	out := make(map[string]model.Subject, len(defaults))
	for _, s := range defaults {
		out[s.Key] = s
	}
	return out
}

func TestSettingsRepository(t *testing.T) {
	db := setupTestDB(t)
	repo := NewSettingsRepository(db)

	t.Run("GetAndSet", func(t *testing.T) {
		err := repo.Set("test_key", "value123", "a testing key")
		if err != nil {
			t.Fatalf("Failed to set config value: %v", err)
		}

		val, err := repo.Get("test_key")
		if err != nil {
			t.Fatalf("Failed to get config value: %v", err)
		}
		if val != "value123" {
			t.Errorf("Expected 'value123', got '%s'", val)
		}

		// Non-existent key should return empty string, not err
		valEmpty, err := repo.Get("non_existent_key")
		if err != nil {
			t.Fatalf("Expected no error for missing key, got: %v", err)
		}
		if valEmpty != "" {
			t.Errorf("Expected empty string, got: %s", valEmpty)
		}
	})

	t.Run("GetWithDefault", func(t *testing.T) {
		val := repo.GetWithDefault("some_missing_config", "fallback_value")
		if val != "fallback_value" {
			t.Errorf("Expected 'fallback_value', got '%s'", val)
		}

		_ = repo.Set("some_existing_config", "actual_value", "")
		val2 := repo.GetWithDefault("some_existing_config", "fallback_value")
		if val2 != "actual_value" {
			t.Errorf("Expected 'actual_value', got '%s'", val2)
		}
	})

	t.Run("GetAll", func(t *testing.T) {
		_ = repo.Set("key_a", "val_a", "")
		_ = repo.Set("key_b", "val_b", "")

		all, err := repo.GetAll()
		if err != nil {
			t.Fatalf("Failed to fetch all settings: %v", err)
		}

		if len(all) < 2 {
			t.Errorf("Expected at least 2 settings, got: %d", len(all))
		}
		if all["key_a"] != "val_a" || all["key_b"] != "val_b" {
			t.Error("Settings mapping incorrect or missing elements")
		}
	})
}
