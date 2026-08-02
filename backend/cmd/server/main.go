package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/config"
	"studyquest/backend/internal/handler"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/router"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	log.Println("Starting StudyQuest Go Backend Server...")

	// 1. Load Configurations
	cfg := config.LoadConfig()

	// 2. Prepare database directory
	dbDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Fatalf("Failed to create database directory '%s': %v", dbDir, err)
	}

	// 3. Connect to SQLite database
	// busy_timeout AND foreign_keys are set in the DSN (not via PRAGMA Exec) so
	// every pooled connection honors them — SQLite PRAGMAs are per-connection,
	// and GORM hands queries to arbitrary pooled connections. A PRAGMA Exec
	// only configures the one connection it ran on, leaving every other conn
	// at the default. Without _busy_timeout in the DSN, a concurrent writer
	// (subtitle-queue claim vs a progress report) fails with "database is
	// locked" instead of queueing. Without _foreign_keys, the OnDelete:CASCADE
	// / RESTRICT constraints declared on ~20 GORM relations silently do nothing
	// on every pooled connection except the one that ran the PRAGMA — which in
	// practice means none of them fire, and the manual cascade deletes in the
	// repos become the only line of defense (a known historical footgun).
	//
	// _loc=UTC makes the "storage is always UTC" rule (CLAUDE.md #3) explicit
	// at the driver level: go-sqlite3 tags read-back time.Time with this zone,
	// and writing an explicit zone here (rather than relying on the driver's
	// nil-loc default) keeps behavior obvious and immune to upstream changes.
	// NowFunc is the matching write-side guarantee: GORM stamps
	// CreatedAt/UpdatedAt using it, so returning UTC keeps auto-managed columns
	// in the same zone as raw-SQL CURRENT_TIMESTAMP (UTC) and as every explicit
	// time.Now().UTC() in the repositories. Together they close the historical
	// skew where a Go time.Time column and a CURRENT_TIMESTAMP column on the
	// same row disagreed by the host's UTC offset (+8h), which once made the
	// job reaper reap freshly-claimed jobs and silently kill every polish run.
	dsn := cfg.DBPath
	if strings.Contains(dsn, "?") {
		dsn += "&_busy_timeout=5000&_foreign_keys=on&_loc=UTC"
	} else {
		dsn += "?_busy_timeout=5000&_foreign_keys=on&_loc=UTC"
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
		Logger: logger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			logger.Config{
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
			},
		),
		TranslateError: true,
	})
	if err != nil {
		log.Fatalf("Failed to connect to SQLite database: %v", err)
	}

	// 4. Configure SQLite WAL journal mode. Unlike busy_timeout/foreign_keys,
	// journal_mode is DATABASE-level (not per-connection): once set it persists
	// in the database file header, so a single PRAGMA Exec on any connection
	// suffices. busy_timeout and foreign_keys are per-connection and were
	// already baked into the DSN above.
	sqlDB, err := db.DB()
	if err == nil {
		_, _ = sqlDB.Exec("PRAGMA journal_mode=WAL;")
	}

	// 5. Run Database Auto-Migrations
	log.Println("Running database migrations...")
	if err := model.AutoMigrate(db); err != nil {
		log.Fatalf("Database auto-migration failed: %v", err)
	}

	// 6. Initialize Repositories
	settingsRepo := repository.NewSettingsRepository(db)
	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
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
	watchEventRepo := repository.NewWatchEventRepository(db)
	storageSourceRepo := repository.NewStorageSourceRepository(db)
	subtitleJobRepo := repository.NewSubtitleJobRepository(db)
	aiProviderRepo := repository.NewAIProviderRepository(db)

	// 7. Initialize Services
	userService := service.NewUserService(userRepo)
	sessionService := service.NewSessionService(sessionRepo, cfg.SessionTTL)
	courseService := service.NewCourseService(courseRepo, userRepo)
	// The storage resolver centralizes provider construction (replaces the 4
	// per-service getActiveProvider copies). nil SourceID → global settings
	// fallback (legacy); non-nil → that source's configured backend.
	storageResolver := service.NewStorageProviderResolver(storageSourceRepo)
	episodeService := service.NewEpisodeService(episodeRepo, storageResolver)
	badgeService := service.NewBadgeService(db, badgeRepo, progressRepo)
	progressService := service.NewProgressService(db, progressRepo, episodeRepo, badgeService, courseRepo, entertainmentRepo, watchEventRepo, cfg.WatchMergeWindow)
	subjectService := service.NewSubjectService(db, subjectRepo, badgeRepo, badgeService)
	tagService := service.NewTagService(tagRepo)
	// Probe worker must exist before import/ingest handlers so they can wire
	// its Enqueue callback. Started as a goroutine below.
	probeWorker := service.NewProbeWorker(episodeService, episodeRepo)
	importService := service.NewImportService(db, episodeRepo, courseRepo, storageResolver, chapterRepo, subjectRepo, probeWorker.Enqueue)
	chapterService := service.NewChapterService(chapterRepo)
	// episodeServiceTx is the transactional variant of episodeService — it
	// wires *gorm.DB + chapterRepo so BulkMoveEpisodes / ReorderEpisodes can
	// run as a single tx with a course-membership guard. The non-tx
	// episodeService above stays in scope because probeWorker/importService
	// captured it before chapterRepo existed; both point at the same repo.
	episodeServiceTx := service.NewEpisodeServiceWithDB(db, episodeRepo, chapterRepo, storageResolver)
	unlockService := service.NewUnlockService(unlockRepo, episodeRepo)
	readingSeriesService := service.NewReadingSeriesService(readingSeriesRepo, readingBookRepo, readingArticleRepo)
	readingBookService := service.NewReadingBookService(readingBookRepo, storageResolver, readingSeriesRepo)
	readingArticleService := service.NewReadingArticleService(readingArticleRepo, readingSeriesRepo)
	readingImportService := service.NewReadingImportService(db, readingSeriesRepo, readingBookRepo, subjectRepo, storageResolver)
	subtitleJobService := service.NewSubtitleJobService(subtitleJobRepo, episodeRepo, episodeService, courseRepo, chapterRepo, subjectRepo)
	// AI subsystem resolver (Step 3). Turns stored ai_providers config rows into
	// live LLM/Embedder instances, cached in-process. Built unconditionally; if
	// no provider is enabled the resolver returns ErrNoProvider and AI endpoints
	// degrade gracefully — the rest of the server is unaffected.
	aiResolver := ai.NewProviderResolver(aiProviderRepo, cfg.AIModelsDir)
	// AI service owns the in-process job worker (segment/summary) and the
	// observability reads. Built unconditionally; when no provider is configured
	// jobs are recorded but processed as "skipped: AI not configured".
	aiContentRepo := repository.NewAIContentRepository(db)
	// glossaryRepo stores term-correction candidates mined by the polish job
	// (PR2). Nil-safe in the service layer, but we always wire it in main.
	glossaryRepo := repository.NewGlossaryRepository(db)
	// polishChunkRepo stores the per-chunk checkpoint rows for 断点续润 (resume
	// a partially-failed polish job without re-burning done chunks). Nil-safe.
	polishChunkRepo := repository.NewAIPolishChunkRepository(db)
	// logRepo stores structured log entries for the /admin/logs page (TODO.md
	// P1). appendLog is nil-safe, but we always wire it in main.
	logRepo := repository.NewLogRepository(db)
	// wrongBookRepo stores 错题本 curation 状态(交卷时对做错的题 upsert)。Nil-safe
	// in the service layer, but we always wire it in main.
	wrongBookRepo := repository.NewWrongBookRepository(db)
	// examRepo stores 课程考试(Exam/ExamQuestion/ExamAnswer)。Nil-safe in the
	// service layer, but we always wire it in main.
	examRepo := repository.NewExamRepository(db)
	// homeworkRepo stores 课后作业卷(Homework/HomeworkSection/HomeworkQuestion/
	// HomeworkPromptConfig)。Nil-safe in the service layer, but we always wire it in main.
	homeworkRepo := repository.NewHomeworkRepository(db)
	aiService := service.NewAIService(db, aiContentRepo, episodeRepo, courseRepo, aiResolver, unlockService, userRepo, glossaryRepo, subjectRepo, polishChunkRepo, logRepo, wrongBookRepo, examRepo, homeworkRepo, settingsRepo)
	// Start the background job-draining worker. NewAIService doesn't auto-start
	// it (so tests can construct a worker-free service); production starts it
	// here, once, for the process lifetime.
	aiService.Start()
	// Connect Step 2 → Step 3: when a subtitle lands, auto-enqueue a segment job
	// (only if the course has AI enabled). The callback keeps the subtitle
	// service free of any AI import — it just calls a function if set.
	subtitleJobService.SetOnSubtitleCompleted(aiService.OnSubtitleCompleted)

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
	userHandler := handler.NewUserHandler(userService, sessionService)
	courseHandler := handler.NewCourseHandler(courseService, episodeService, chapterService, subjectRepo, unlockService, courseRepo, episodeRepo)
	episodeHandler := handler.NewEpisodeHandler(episodeService, progressService, settingsRepo, unlockService, storageSourceRepo)
	progressHandler := handler.NewProgressHandler(progressService)
	ingestHandler := handler.NewIngestHandler(episodeRepo, episodeService, probeWorker.Enqueue)
	subtitleJobHandler := handler.NewSubtitleJobHandler(subtitleJobService)
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
		WithEpisodeService(episodeServiceTx).
		WithChapterService(chapterService).
		WithBadgeService(badgeService).
		WithReadingSeriesService(readingSeriesService).
		WithReadingBookService(readingBookService).
		WithReadingArticleService(readingArticleService).
		WithReadingImportService(readingImportService).
		WithProbeWorker(probeWorker).
		WithSubtitleJobService(subtitleJobService).
		WithSessionService(sessionService).
		WithWatchEventRepo(watchEventRepo).
		WithStorageSources(storageSourceRepo).
		WithStorageResolver(storageResolver).
		WithAIProviderRepo(aiProviderRepo).
		WithAIResolver(aiResolver).
		WithAIService(aiService).
		Build()
	badgeHandler := handler.NewBadgeHandler(badgeService)
	subjectHandler := handler.NewSubjectHandler(subjectService)
	tagHandler := handler.NewTagHandler(tagService)
	// gradeService manages the open-tag-system grade CRUD (rename/merge/delete
	// across the four grade tables). Constructed against the same db as every
	// other repo; nil-safe in degenerate builds.
	gradeService := service.NewGradeService(repository.NewGradeRepository(db))
	gradeHandler := handler.NewGradeHandler(gradeService)
	unlockHandler := handler.NewUnlockHandler(unlockService)
	aiHandler := handler.NewAIHandler(aiService, unlockService)
	releaseHandler := handler.NewReleaseHandler(releaseRepo)
	readingHandler := handler.NewReadingHandler(readingSeriesService, readingBookService, readingArticleService, subjectRepo, storageSourceRepo)

	// 9. Boot up Gin Server Router
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Trust configured proxies so c.ClientIP() reads X-Forwarded-For from a
	// local caddy/nginx instead of seeing all requests as 127.0.0.1. This keeps
	// per-IP login rate-limiting meaningful behind a reverse proxy. On a direct
	// LAN deployment (no proxy) ClientIP() falls back to RemoteAddr.
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Printf("Warning: failed to set trusted proxies: %v", err)
	}

	// Surface the ingest-key state at boot so an internet-facing operator is
	// reminded that the ingest endpoints are currently unauthenticated.
	if cfg.IngestKey == "" {
		log.Println("Warning: INGEST_KEY is not set — /api/v1/ingest/* endpoints are unauthenticated. Set INGEST_KEY before exposing this server publicly.")
	}

	// Register all HTTP routes
	router.RegisterRoutes(
		r,
		healthHandler,
		userHandler,
		courseHandler,
		episodeHandler,
		progressHandler,
		ingestHandler,
		subtitleJobHandler,
		adminHandler,
		badgeHandler,
		subjectHandler,
		tagHandler,
		gradeHandler,
		unlockHandler,
		releaseHandler,
		readingHandler,
		aiHandler,
		userRepo,
		settingsRepo,
		sessionService,
		cfg.IngestKey,
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

	// Background reaper for the subtitle queue: a worker that crashed or was
	// powered off mid-transcription leaves its job in 'processing' with a stale
	// claimed_at. Every 5 minutes we flip jobs older than the service's stale
	// threshold back to 'queued' so they become claimable again.
	//
	// This goroutine has its own context (not probeCtx) so the two background
	// loops are independent: renaming/removing the probe worker can't
	// accidentally take down the reaper, and vice versa.
	reaperCtx, reaperCancel := context.WithCancel(context.Background())
	defer reaperCancel()
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-reaperCtx.Done():
				return
			case <-ticker.C:
				if n, err := subtitleJobService.ReapStale(); err != nil {
					log.Printf("[subtitle-reaper] error: %v", err)
				} else if n > 0 {
					log.Printf("[subtitle-reaper] reaped %d stale job(s) back to queued", n)
				}
				// AI job reaper 复用同一个 ticker/循环:进程被 hard-kill 时会把正在
				// 跑的 LLM 作业留在 'processing',这里把它们捞回 queued。aiService 在
				// 集成里始终被构造(非 nil),但加 nil 检查以防未来改成按需注入。
				if aiService != nil {
					if n, err := aiService.ReapStaleJobs(); err != nil {
						log.Printf("[ai-reaper] error: %v", err)
					} else if n > 0 {
						log.Printf("[ai-reaper] reaped %d stale job(s) back to queued", n)
					}
				}
			}
		}
	}()

	if err := r.Run(cfg.ServerAddr); err != nil {
		log.Fatalf("Server startup failed on '%s': %v", cfg.ServerAddr, err)
	}
}
