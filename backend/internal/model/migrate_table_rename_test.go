package model

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newLegacyProviderDB builds a file-backed DB in a per-test temp dir with ONLY
// the misnamed `a_iproviders` table (mimicking a pre-upgrade prod DB) seeded
// with sample rows. Each test gets its own file (via t.TempDir) so they don't
// share state. It deliberately does NOT use AutoMigrate (which would now create
// the correctly-named table) — we hand-craft the legacy shape.
func newLegacyProviderDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath+"?_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Create the legacy misnamed table with the exact historical schema.
	if err := db.Exec(`CREATE TABLE a_iproviders (
		id integer PRIMARY KEY AUTOINCREMENT,
		capability text NOT NULL,
		name text NOT NULL,
		provider_type text NOT NULL,
		base_url text,
		api_key text,
		model_name text NOT NULL,
		extra_json text,
		is_enabled numeric DEFAULT false,
		created_at datetime,
		updated_at datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec(`CREATE INDEX idx_a_iproviders_capability ON a_iproviders(capability)`).Error; err != nil {
		t.Fatalf("create legacy index: %v", err)
	}
	// Seed two rows (one chat, one embedding) — the shapes that matter for
	// resolver correctness after upgrade.
	if err := db.Exec(`INSERT INTO a_iproviders (capability, name, provider_type, base_url, api_key, model_name, is_enabled, created_at, updated_at) VALUES
		('chat', '主聊天模型', 'openai_compat', 'https://relay.example.com', 'sk-secret', 'gpt-5.4-mini', 1, '2026-01-01', '2026-01-01'),
		('embedding', '本地向量', 'onnx_local', '', '', 'bge-small-zh-v1.5', 1, '2026-01-01', '2026-01-01')
	`).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// TestMigrateAIProvidersTableName_RenamesAndPreservesData is the upgrade-safety
// proof: against a DB shaped like pre-upgrade prod (legacy `a_iproviders` with
// real config rows), AutoMigrate must rename the table in place and preserve
// every row + the capability index, with zero data movement.
func TestMigrateAIProvidersTableName_RenamesAndPreservesData(t *testing.T) {
	db := newLegacyProviderDB(t)

	// Pre-conditions.
	oldExists, _ := tableExists(db, "a_iproviders")
	newExists, _ := tableExists(db, "ai_providers")
	if !oldExists || newExists {
		t.Fatalf("setup wrong: a_iproviders=%v ai_providers=%v", oldExists, newExists)
	}
	var beforeCount int64
	db.Raw("SELECT COUNT(*) FROM a_iproviders").Scan(&beforeCount)
	if beforeCount != 2 {
		t.Fatalf("seed count = %d, want 2", beforeCount)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	// Post: legacy gone, new exists.
	if oe, _ := tableExists(db, "a_iproviders"); oe {
		t.Errorf("legacy a_iproviders still exists; rename did not fire")
	}
	if ne, _ := tableExists(db, "ai_providers"); !ne {
		t.Errorf("ai_providers missing; rename failed")
	}

	// Rows preserved.
	var afterCount int64
	db.Raw("SELECT COUNT(*) FROM ai_providers").Scan(&afterCount)
	if afterCount != beforeCount {
		t.Errorf("row count changed: %d → %d", beforeCount, afterCount)
	}

	// Model reads via TableName() from the renamed table.
	var providers []AIProvider
	if err := db.Find(&providers).Error; err != nil {
		t.Fatalf("model Find failed: %v", err)
	}
	if len(providers) != 2 {
		t.Errorf("model read %d providers, want 2", len(providers))
	}
	// Verify the chat provider's secret survived (not clobbered).
	for _, p := range providers {
		if p.Capability == "chat" && p.APIKey != "sk-secret" {
			t.Errorf("chat api_key lost: got %q", p.APIKey)
		}
	}

	// The capability index must have come along (SQLite renames indexes with
	// the table). AutoMigrate should not need to recreate it.
	var idxCount int64
	db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='ai_providers'").Scan(&idxCount)
	if idxCount == 0 {
		t.Errorf("no indexes on ai_providers after rename; capability index lost")
	}
}

// TestMigrateAIProvidersTableName_Idempotent verifies a second AutoMigrate run
// (after the rename already happened) is a clean no-op — no error, no data loss.
func TestMigrateAIProvidersTableName_Idempotent(t *testing.T) {
	db := newLegacyProviderDB(t)
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	var firstCount int64
	db.Raw("SELECT COUNT(*) FROM ai_providers").Scan(&firstCount)

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var secondCount int64
	db.Raw("SELECT COUNT(*) FROM ai_providers").Scan(&secondCount)
	if firstCount != secondCount {
		t.Errorf("idempotent run changed rows: %d → %d", firstCount, secondCount)
	}
	// Legacy name must not reappear.
	if oe, _ := tableExists(db, "a_iproviders"); oe {
		t.Errorf("legacy table reappeared after idempotent run")
	}
}

// TestMigrateAIProvidersTableName_FreshInstall verifies that on a brand-new DB
// (no legacy table), the migration is a no-op and AutoMigrate creates
// `ai_providers` normally.
func TestMigrateAIProvidersTableName_FreshInstall(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	db, err := gorm.Open(sqlite.Open(dbPath+"?_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate on fresh DB: %v", err)
	}
	if ne, _ := tableExists(db, "ai_providers"); !ne {
		t.Errorf("fresh install did not create ai_providers")
	}
	if oe, _ := tableExists(db, "a_iproviders"); oe {
		t.Errorf("fresh install somehow created legacy a_iproviders")
	}
}
