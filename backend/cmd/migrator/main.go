// Command migrator runs model.AutoMigrate against the configured database and
// exits. Used during development when a schema change ships but we don't want
// to delete+rebuild the whole DB (GORM AutoMigrate is additive: it adds new
// columns/tables but never drops or renames). Production runs AutoMigrate on
// every server boot, so this binary is just a convenience for verifying a
// migration locally without starting the full server.
package main

import (
	"flag"
	"fmt"
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

func main() {
	dbPath := flag.String("db", "data/studyquest.db", "path to studyquest.db")
	flag.Parse()

	dsn := "file:" + *dbPath + "?_busy_timeout=5000&_foreign_keys=on"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", *dbPath, err)
		os.Exit(1)
	}
	if err := model.AutoMigrate(db); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("AutoMigrate OK against", *dbPath)

	type Col struct {
		Name string
		Type string
	}
	for _, table := range []string{"subtitles", "glossary_candidates", "ai_providers"} {
		var cols []Col
		if err := db.Raw("PRAGMA table_info(" + table + ")").Scan(&cols).Error; err != nil {
			fmt.Fprintf(os.Stderr, "inspect %s: %v\n", table, err)
			continue
		}
		fmt.Printf("\n%s:\n", table)
		for _, c := range cols {
			fmt.Printf("  %s %s\n", c.Name, c.Type)
		}
	}
}
