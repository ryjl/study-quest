package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"studyquest/backend/internal/config"
	"studyquest/backend/internal/handler"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/router"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
	log.Println("Starting StudyQuest (学途奇旅) Go Backend Server...")

	// 1. Load Configurations
	cfg := config.LoadConfig()

	// 2. Prepare database directory
	dbDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Fatalf("Failed to create database directory '%s': %v", dbDir, err)
	}

	// 3. Connect to SQLite database
	db, err := gorm.Open(sqlite.Open(cfg.DBPath), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to SQLite database: %v", err)
	}

	// 4. Configure SQLite optimization params
	sqlDB, err := db.DB()
	if err == nil {
		_, _ = sqlDB.Exec("PRAGMA journal_mode=WAL;")
		_, _ = sqlDB.Exec("PRAGMA foreign_keys=ON;")
	}

	// 5. Run Database Auto-Migrations
	log.Println("Running database migrations...")
	if err := model.AutoMigrate(db); err != nil {
		log.Fatalf("Database auto-migration failed: %v", err)
	}

	// Warn about a legacy schema: the old Course.Subject string column was
	// replaced by Course.SubjectID + a subjects table with an FK constraint.
	// SQLite cannot add an FK constraint to an existing column, so a DB that
	// predates this change needs to be recreated. AutoMigrate has already
	// added the new subject_id column and created the subjects table, but the
	// RESTRICT constraint on subject_id will be missing on the legacy table.
	if hasLegacySubjectColumn(db) {
		log.Printf("⚠️  检测到旧版 courses.subject 列。本次升级引入了 subjects 表 + 外键约束，")
		log.Printf("    SQLite 不支持给已有表加 FK 约束。为获得完整的删除保护，请删除数据库文件后重启：")
		log.Printf("    rm %s", cfg.DBPath)
		log.Printf("    (按约定数据可丢弃重建， subjects/badges 会在重启后自动 seed。)")
	}

	// 6. Initialize Repositories
	settingsRepo := repository.NewSettingsRepository(db)
	userRepo := repository.NewUserRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	progressRepo := repository.NewProgressRepository(db)
	chapterRepo := repository.NewChapterRepository(db)
	badgeRepo := repository.NewBadgeRepository(db)
	subjectRepo := repository.NewSubjectRepository(db)
	tagRepo := repository.NewTagRepository(db)
	unlockRepo := repository.NewUnlockRepository(db)
	releaseRepo := repository.NewReleaseRepository(db)

	// 7. Initialize Services
	userService := service.NewUserService(userRepo)
	courseService := service.NewCourseService(courseRepo, userRepo)
	episodeService := service.NewEpisodeService(episodeRepo, settingsRepo)
	badgeService := service.NewBadgeService(db, badgeRepo, progressRepo)
	progressService := service.NewProgressService(db, progressRepo, episodeRepo, badgeService)
	subjectService := service.NewSubjectService(subjectRepo, badgeRepo)
	tagService := service.NewTagService(tagRepo)
	// Probe worker must exist before import/ingest handlers so they can wire
	// its Enqueue callback. Started as a goroutine below.
	probeWorker := service.NewProbeWorker(episodeService, episodeRepo)
	importService := service.NewImportService(db, episodeRepo, courseRepo, settingsRepo, chapterRepo, subjectRepo, probeWorker.Enqueue)
	chapterService := service.NewChapterService(chapterRepo)
	unlockService := service.NewUnlockService(unlockRepo, episodeRepo)

	// Seed default badges and subjects (idempotent). Subjects must seed BEFORE
	// badges so the subject_count badge rules' rule_target keys ("math",
	// "english") resolve against a populated subjects table.
	if err := subjectService.SeedDefaultSubjects(); err != nil {
		log.Printf("Warning: failed to seed default subjects: %v", err)
	}
	if err := tagService.SeedDefaultTags(); err != nil {
		log.Printf("Warning: failed to seed default tags: %v", err)
	}
	if err := badgeService.SeedDefaultBadges(); err != nil {
		log.Printf("Warning: failed to seed default badges: %v", err)
	}

	// Backfill is_system on pre-existing seeded rows. On instances that were
	// seeded BEFORE the IsSystem column existed, the starter rows have
	// is_system=false (the column's default), so the delete-protection guard
	// wouldn't apply to them. This one-shot UPDATE marks the canonical default
	// keys as system so old installs converge to the same protected state as
	// fresh ones. Idempotent — running it repeatedly just re-asserts the flag.
	markSystemDefaults(db)

	// 8. Initialize Handlers
	healthHandler := handler.NewHealthHandler()
	userHandler := handler.NewUserHandler(userService)
	courseHandler := handler.NewCourseHandler(courseService, episodeService, chapterService, subjectRepo, unlockService)
	episodeHandler := handler.NewEpisodeHandler(episodeService, progressService, settingsRepo, unlockService)
	progressHandler := handler.NewProgressHandler(progressService)
	ingestHandler := handler.NewIngestHandler(episodeRepo, episodeService, probeWorker.Enqueue)
	adminHandler := handler.NewAdminHandlerDeps().
		// Repos
		WithSettings(settingsRepo).
		WithUsers(userRepo).
		WithCourses(courseRepo).
		WithEpisodes(episodeRepo).
		WithChapters(chapterRepo).
		WithProgress(progressRepo).
		WithSubjects(subjectRepo).
		WithBadges(badgeRepo).
		// Services
		WithUserService(userService).
		WithCourseService(courseService).
		WithImportService(importService).
		WithEpisodeService(episodeService).
		WithChapterService(chapterService).
		WithBadgeService(badgeService).
		WithProbeWorker(probeWorker).
		Build()
	badgeHandler := handler.NewBadgeHandler(badgeService)
	subjectHandler := handler.NewSubjectHandler(subjectService)
	tagHandler := handler.NewTagHandler(tagService)
	unlockHandler := handler.NewUnlockHandler(unlockService)
	releaseHandler := handler.NewReleaseHandler(releaseRepo)

	// 9. Boot up Gin Server Router
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Register all HTTP routes
	router.RegisterRoutes(
		r,
		healthHandler,
		userHandler,
		courseHandler,
		episodeHandler,
		progressHandler,
		ingestHandler,
		adminHandler,
		badgeHandler,
		subjectHandler,
		tagHandler,
		unlockHandler,
		releaseHandler,
		userRepo,
		settingsRepo,
	)

	// Print startup information
	fmt.Printf("\n-------------------------------------------------\n")
	fmt.Printf("StudyQuest Go Backend Server is running!\n")
	fmt.Printf("Listening Address : %s\n", cfg.ServerAddr)
	fmt.Printf("SQLite Database   : %s\n", cfg.DBPath)
	fmt.Printf("-------------------------------------------------\n\n")

	// Start the background media-probe worker (ffprobe backfill). It runs for
	// the lifetime of the process; cancellation is implicit on exit.
	probeCtx, probeCancel := context.WithCancel(context.Background())
	defer probeCancel()
	go probeWorker.Start(probeCtx)

	if err := r.Run(cfg.ServerAddr); err != nil {
		log.Fatalf("Server startup failed on '%s': %v", cfg.ServerAddr, err)
	}
}

// hasLegacySubjectColumn reports whether the courses table still carries the
// old `subject` text column (pre-subjects-table schema). Used only to emit a
// migration warning at boot.
func hasLegacySubjectColumn(db *gorm.DB) bool {
	type columnInfo struct {
		Name string `gorm:"column:name"`
	}
	var cols []columnInfo
	// SQLite introspection: PRAGMA table_info returns one row per column.
	db.Raw("PRAGMA table_info(courses)").Scan(&cols)
	for _, c := range cols {
		if c.Name == "subject" {
			return true
		}
	}
	return false
}

// markSystemDefaults flags the canonical seeded subject/tag/badge rows as
// IsSystem=true. This is a backfill for instances seeded before the IsSystem
// column existed: their starter rows otherwise carry is_system=false and
// wouldn't be delete-protected. Idempotent. The key lists are owned by the
// service package (seed_keys.go) so they can't drift from the SeedDefault*
// inserts.
func markSystemDefaults(db *gorm.DB) {
	// Keys come from the service package's single source of truth (seed_keys.go)
	// — no hand-redeclared copies, so they can't drift from the SeedDefault*
	// lists.
	tables := []struct {
		table string
		col   string // the key/code column name
		keys  []string
	}{
		{"subjects", "key", service.SystemSubjectKeys},
		{"tags", "key", service.SystemTagKeys},
		{"badges", "code", service.SystemBadgeCodes},
	}
	for _, t := range tables {
		if err := db.Table(t.table).Where(t.col+" IN ?", t.keys).
			Update("is_system", true).Error; err != nil {
			log.Printf("Warning: failed to mark system %s defaults: %v", t.table, err)
		}
	}
}
