// Command backfill_sources is a one-shot migration tool that bridges the
// single-global-storage-source era to the multi-source era.
//
// It reads the 5 legacy storage_* settings keys, creates (or reuses) a
// StorageSource named "default" from them, then backfills every episode /
// reading_book whose source_id is NULL with that source's id. The legacy
// settings keys are left in place (marked via a settings note) so the global
// fallback in StorageProviderResolver keeps working for any row still on NULL.
//
// Usage:
//
//	# preview what would change (no writes)
//	go run ./cmd/backfill_sources -dry-run
//
//	# apply
//	go run ./cmd/backfill_sources
//
// Most deployments rebuild the DB from scratch on upgrade (rm data/studyquest.db),
// in which case this tool is unnecessary — every row starts with its real
// source_id at import time. Run this only when upgrading in place.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"studyquest/backend/internal/config"
	"studyquest/backend/internal/model"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "print what would change without writing")
	flag.Parse()

	cfg := config.LoadConfig()

	db, err := gorm.Open(sqlite.Open(cfg.DBPath), &gorm.Config{
		Logger: logger.New(log.New(os.Stdout, "", 0), logger.Config{LogLevel: logger.Warn}),
	})
	if err != nil {
		log.Fatalf("open db %s: %v", cfg.DBPath, err)
	}
	if sqlDB, err := db.DB(); err == nil {
		_, _ = sqlDB.Exec("PRAGMA journal_mode=WAL;")
	}
	// Ensure the storage_sources table exists (AutoMigrate is idempotent).
	if err := model.AutoMigrate(db); err != nil {
		log.Fatalf("auto-migrate: %v", err)
	}

	// 1. Read the legacy global storage_* settings.
	settings := readSettings(db, []string{"storage_type", "storage_url", "storage_username", "storage_password", "storage_token"})
	if settings["storage_url"] == "" {
		log.Fatalf("no storage_url in settings — nothing to backfill from. Configure storage in the admin panel first.")
	}
	typ := settings["storage_type"]
	if typ == "" {
		typ = "alist"
	}
	fmt.Printf("Legacy storage config: type=%s url=%s user=%s token_set=%t\n", typ, settings["storage_url"], settings["storage_username"], settings["storage_token"] != "")

	// 2. Create or reuse the "default" source.
	var defaultSource model.StorageSource
	err = db.Where("name = ?", "default").First(&defaultSource).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			log.Fatalf("lookup default source: %v", err)
		}
		defaultSource = model.StorageSource{
			Name:      "default",
			Type:      typ,
			URL:       settings["storage_url"],
			Username:  settings["storage_username"],
			Password:  settings["storage_password"],
			Token:     settings["storage_token"],
			IsDefault: true,
		}
		fmt.Printf("Will CREATE default source (id pending)\n")
		if !*dryRun {
			if err := db.Create(&defaultSource).Error; err != nil {
				log.Fatalf("create default source: %v", err)
			}
		}
	} else {
		fmt.Printf("Reusing existing default source (id=%d)\n", defaultSource.ID)
	}
	sourceID := defaultSource.ID
	if *dryRun && sourceID == 0 {
		// Pretend it would get id 1 for the dry-run counts.
		sourceID = 1
	}

	// 3. Count + backfill episodes.
	var epCount int64
	db.Model(&model.Episode{}).Where("source_id IS NULL").Count(&epCount)
	fmt.Printf("Episodes with NULL source_id: %d\n", epCount)
	if !*dryRun && epCount > 0 {
		res := db.Model(&model.Episode{}).Where("source_id IS NULL").Update("source_id", defaultSource.ID)
		if res.Error != nil {
			log.Fatalf("backfill episodes: %v", res.Error)
		}
		fmt.Printf("  → updated %d rows\n", res.RowsAffected)
	}

	// 4. Count + backfill reading books.
	var bookCount int64
	db.Model(&model.ReadingBook{}).Where("source_id IS NULL").Count(&bookCount)
	fmt.Printf("Reading books with NULL source_id: %d\n", bookCount)
	if !*dryRun && bookCount > 0 {
		res := db.Model(&model.ReadingBook{}).Where("source_id IS NULL").Update("source_id", defaultSource.ID)
		if res.Error != nil {
			log.Fatalf("backfill books: %v", res.Error)
		}
		fmt.Printf("  → updated %d rows\n", res.RowsAffected)
	}

	// 5. Mark the legacy settings deprecated (in-place note update, no value
	// change — keeps the fallback working during the transition).
	if !*dryRun {
		setSettingNote(db, "storage_type", "[deprecated] superseded by storage_sources.default; kept as fallback")
	}

	if *dryRun {
		fmt.Println("\n(dry-run: no changes written)")
	} else {
		fmt.Println("\nBackfill complete. Legacy storage_* settings retained as fallback.")
	}
}

func readSettings(db *gorm.DB, keys []string) map[string]string {
	out := make(map[string]string, len(keys))
	var rows []model.Setting
	db.Where("key IN ?", keys).Find(&rows)
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out
}

func setSettingNote(db *gorm.DB, key, note string) {
	db.Model(&model.Setting{}).Where("key = ?", key).Update("description", note)
}
