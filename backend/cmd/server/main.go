package main

import (
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

	// 7. Initialize Services
	userService := service.NewUserService(userRepo)
	courseService := service.NewCourseService(courseRepo, userRepo)
	episodeService := service.NewEpisodeService(episodeRepo, settingsRepo)
	progressService := service.NewProgressService(progressRepo, episodeRepo)
	importService := service.NewImportService(episodeRepo, courseRepo, settingsRepo)

	// 8. Initialize Handlers
	healthHandler := handler.NewHealthHandler()
	userHandler := handler.NewUserHandler(userService)
	courseHandler := handler.NewCourseHandler(courseService, episodeService)
	episodeHandler := handler.NewEpisodeHandler(episodeService, settingsRepo)
	progressHandler := handler.NewProgressHandler(progressService)
	ingestHandler := handler.NewIngestHandler(episodeRepo, episodeService)
	adminHandler := handler.NewAdminHandler(
		settingsRepo,
		userRepo,
		courseRepo,
		episodeRepo,
		userService,
		courseService,
		importService,
		episodeService,
	)

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
		userRepo,
		settingsRepo,
	)

	// Print startup information
	fmt.Printf("\n-------------------------------------------------\n")
	fmt.Printf("StudyQuest Go Backend Server is running!\n")
	fmt.Printf("Listening Address : %s\n", cfg.ServerAddr)
	fmt.Printf("SQLite Database   : %s\n", cfg.DBPath)
	fmt.Printf("-------------------------------------------------\n\n")

	if err := r.Run(cfg.ServerAddr); err != nil {
		log.Fatalf("Server startup failed on '%s': %v", cfg.ServerAddr, err)
	}
}
