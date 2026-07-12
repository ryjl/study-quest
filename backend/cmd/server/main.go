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
	readingSeriesRepo := repository.NewReadingSeriesRepository(db)
	readingBookRepo := repository.NewReadingBookRepository(db)
	readingArticleRepo := repository.NewReadingArticleRepository(db)
	entertainmentRepo := repository.NewEntertainmentRepository(db)

	// 7. Initialize Services
	userService := service.NewUserService(userRepo)
	courseService := service.NewCourseService(courseRepo, userRepo)
	episodeService := service.NewEpisodeService(episodeRepo, settingsRepo)
	badgeService := service.NewBadgeService(db, badgeRepo, progressRepo)
	progressService := service.NewProgressService(db, progressRepo, episodeRepo, badgeService, courseRepo, entertainmentRepo)
	subjectService := service.NewSubjectService(db, subjectRepo, badgeRepo, badgeService)
	tagService := service.NewTagService(tagRepo)
	// Probe worker must exist before import/ingest handlers so they can wire
	// its Enqueue callback. Started as a goroutine below.
	probeWorker := service.NewProbeWorker(episodeService, episodeRepo)
	importService := service.NewImportService(db, episodeRepo, courseRepo, settingsRepo, chapterRepo, subjectRepo, probeWorker.Enqueue)
	chapterService := service.NewChapterService(chapterRepo)
	unlockService := service.NewUnlockService(unlockRepo, episodeRepo)
	readingSeriesService := service.NewReadingSeriesService(readingSeriesRepo, readingBookRepo, readingArticleRepo)
	readingBookService := service.NewReadingBookService(readingBookRepo, settingsRepo, readingSeriesRepo)
	readingArticleService := service.NewReadingArticleService(readingArticleRepo, readingSeriesRepo)
	readingImportService := service.NewReadingImportService(db, readingSeriesRepo, readingBookRepo, subjectRepo, settingsRepo)

	// Seed default badges and subjects (idempotent). Badges seed FIRST because
	// subject seeding auto-generates subject_count badges and the order keeps
	// the seed logs readable.
	if err := badgeService.SeedDefaultBadges(); err != nil {
		log.Printf("Warning: failed to seed default badges: %v", err)
	}
	if err := subjectService.SeedDefaultSubjects(); err != nil {
		log.Printf("Warning: failed to seed default subjects: %v", err)
	}
	if err := tagService.SeedDefaultTags(); err != nil {
		log.Printf("Warning: failed to seed default tags: %v", err)
	}

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
		WithReadingSeriesRepo(readingSeriesRepo).
		WithReadingBookRepo(readingBookRepo).
		WithReadingArticleRepo(readingArticleRepo).
		// Services
		WithUserService(userService).
		WithCourseService(courseService).
		WithImportService(importService).
		WithEpisodeService(episodeService).
		WithChapterService(chapterService).
		WithBadgeService(badgeService).
		WithReadingSeriesService(readingSeriesService).
		WithReadingBookService(readingBookService).
		WithReadingArticleService(readingArticleService).
		WithReadingImportService(readingImportService).
		WithProbeWorker(probeWorker).
		Build()
	badgeHandler := handler.NewBadgeHandler(badgeService)
	subjectHandler := handler.NewSubjectHandler(subjectService)
	tagHandler := handler.NewTagHandler(tagService)
	unlockHandler := handler.NewUnlockHandler(unlockService)
	releaseHandler := handler.NewReleaseHandler(releaseRepo)
	readingHandler := handler.NewReadingHandler(readingSeriesService, readingBookService, readingArticleService, subjectRepo)

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
		readingHandler,
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

