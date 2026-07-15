package router

import (
	"io/fs"
	"net/http"
	"strings"
	"time"
	"studyquest/backend/internal/admin/spa"
	"studyquest/backend/internal/handler"
	"studyquest/backend/internal/middleware"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes sets up all routes for client application, Python tooling, and Admin UI.
func RegisterRoutes(
	r *gin.Engine,
	health handler.HealthHandler,
	user handler.UserHandler,
	course handler.CourseHandler,
	episode handler.EpisodeHandler,
	progress handler.ProgressHandler,
	ingest handler.IngestHandler,
	subtitleJob handler.SubtitleJobHandler,
	admin handler.AdminHandler,
	badge handler.BadgeHandler,
	subject handler.SubjectHandler,
	tag handler.TagHandler,
	unlock handler.UnlockHandler,
	release handler.ReleaseHandler,
	reading handler.ReadingHandler,
	userRepo repository.UserRepository,
	settingsRepo repository.SettingsRepository,
	sessionService service.SessionService,
	ingestKey string,
) {
	// Enable Global recovery and logger middlewares
	r.Use(gin.Recovery())
	r.Use(middleware.CORSMiddleware())

	// Map local uploads folder
	r.Static("/uploads", "./data/uploads")

	// 1. Health check & Initial login list (Public)
	v1 := r.Group("/api/v1")
	{
		v1.GET("/health", health.Check)
		v1.GET("/users", user.GetUsers)
		// Login is rate-limited per source IP to throttle PIN brute-forcing.
		// Both GET (legacy) and POST share the limiter since either verifies a PIN.
		loginLimit := middleware.LoginRateLimitMiddleware(15*time.Minute, 5)
		v1.GET("/users/login", user.Login, loginLimit) // Allow GET login list too
		v1.POST("/users/login", user.Login, loginLimit)

		v1.GET("/subtitles/:id.vtt", episode.GetSubtitleVTT)

		// APK OTA distribution — FROZEN client contract. Public (no auth) so
		// even a client that can't reach login can still self-update. See
		// release_handler.go for the stability invariants.
		v1.GET("/app/latest", release.GetLatest)
		v1.GET("/app/download", release.Download)
	}

	// 2. Client restricted operations (UserAuth required)
	v1Restricted := r.Group("/api/v1")
	v1Restricted.Use(middleware.UserAuthMiddleware(sessionService, userRepo))
	{
		v1Restricted.GET("/courses", course.GetCourses)
		v1Restricted.GET("/courses/:id", course.GetCourseByID)
		v1Restricted.GET("/courses/:id/episodes", course.GetEpisodesByCourse)
		v1Restricted.GET("/courses/:id/chapters", course.GetChaptersByCourse)
		v1Restricted.GET("/courses/:id/unlock-status", course.GetUnlockStatus)
		v1Restricted.GET("/courses/:id/last-watched", progress.GetLastWatched)

		v1Restricted.GET("/episodes/:id", episode.GetEpisodeByID)
		// Episode media streaming. Previously public (for VLC / external
		// players); now behind UserAuth since this deployment only uses the
		// in-app player, which resolves the direct netdisk URL via the
		// authenticated play-info endpoint anyway.
		v1Restricted.GET("/episodes/:id/stream", episode.Stream)
		v1Restricted.GET("/episodes/:id/play-info", episode.GetPlayInfo)
		v1Restricted.GET("/episodes/:id/subtitle", episode.GetSubtitle)
		v1Restricted.GET("/episodes/:id/ai-content", episode.GetAIContent)
		v1Restricted.GET("/episodes/:id/attachments", episode.GetAttachments)
		// Resolve the Nth attachment of an episode into a 302 download link.
		v1Restricted.GET("/episodes/:id/attachments/:index/stream", episode.StreamAttachment)

		v1Restricted.POST("/progress/report", progress.ReportProgress)
		v1Restricted.GET("/progress", progress.GetProgressOverview)
		v1Restricted.GET("/progress/ledger", progress.GetPointsLedger)
		v1Restricted.GET("/progress/points", progress.GetPoints)
		v1Restricted.GET("/users/:id/badges", badge.GetUserBadges)
		v1Restricted.GET("/subjects", subject.ClientListSubjects)
		v1Restricted.GET("/tags", tag.ClientListTags)

		// Reading Room — PDF books + web articles
		v1Restricted.GET("/readings", reading.GetReadingRoom)
		v1Restricted.GET("/readings/series/:id", reading.GetSeries)
		v1Restricted.GET("/readings/books/:id/stream", reading.StreamBook)
		v1Restricted.GET("/readings/books/:id/progress", reading.GetBookProgress)
		v1Restricted.POST("/readings/books/:id/progress", reading.ReportBookProgress)
		v1Restricted.GET("/readings/articles/:id", reading.GetArticle)

		// Session management — revoke the caller's own session (logout).
		v1Restricted.POST("/users/logout", user.Logout)
	}

	// 3. Local Python Toolchain ingestion points. Public by default (LAN-only
	// deployments), but gated by X-Ingest-Key when INGEST_KEY is configured so
	// the endpoint isn't a public write surface on internet-facing setups.
	v1Ingest := r.Group("/api/v1")
	v1Ingest.Use(middleware.IngestKeyMiddleware(ingestKey))
	{
		v1Ingest.POST("/ingest/episodes", ingest.IngestEpisodes)
		v1Ingest.POST("/ingest/ai-content", ingest.IngestAIContent)

		// Subtitle generation worker protocol. Shares the X-Ingest-Key gate —
		// the whisper worker is another member of the Python toolchain.
		v1Ingest.POST("/subtitle-jobs/claim", subtitleJob.Claim)
		v1Ingest.POST("/subtitle-jobs/:id/complete", subtitleJob.Complete)
		v1Ingest.POST("/subtitle-jobs/:id/heartbeat", subtitleJob.Heartbeat)
		v1Ingest.POST("/subtitle-jobs/:id/fail", subtitleJob.Fail)
	}

	// 4. Admin auth APIs (Public — used by the SPA before session cookie exists)
	r.POST("/admin/api/login", admin.LoginAPI)
	r.POST("/admin/api/logout", admin.LogoutAPI)
	r.GET("/admin/api/me", admin.Me)

	// 5. Protected Admin JSON APIs
	adm := r.Group("/admin")
	adm.Use(middleware.AdminAuthMiddleware(settingsRepo))
	{
		// Users
		adm.GET("/api/users", admin.ListUsers)
		adm.POST("/api/users", admin.CreateUser)
		adm.PUT("/api/users/:id", admin.UpdateUser)
		adm.DELETE("/api/users/:id", admin.DeleteUser)
		adm.POST("/api/users/:id/access/bulk", admin.BulkAccess)
		adm.GET("/api/users/:id/ledger", admin.UserLedger)
		adm.GET("/api/users/:id/badges", admin.UserBadges)

		// User device sessions — list / revoke per-device / revoke all / relabel
		adm.GET("/api/users/:id/sessions", admin.ListUserSessions)
		adm.DELETE("/api/users/:id/sessions", admin.RevokeAllUserSessions)
		adm.DELETE("/api/users/:id/sessions/:token", admin.RevokeUserSession)
		adm.PATCH("/api/sessions/:token/note", admin.UpdateSessionNote)

		// Watch history — per-day heatmap + selected-day timeline
		adm.GET("/api/users/:id/watch-history", admin.GetUserWatchHistory)
		adm.GET("/api/users/:id/watch-events", admin.GetUserWatchEvents)

		// Courses
		adm.GET("/api/courses", admin.ListCourses)
		adm.GET("/api/courses/:id/detail", admin.GetCourseDetail)
		adm.POST("/api/courses", admin.CreateCourse)
		adm.PUT("/api/courses/:id", admin.UpdateCourse)
		adm.DELETE("/api/courses/:id", admin.DeleteCourse)

		// Episodes
		adm.GET("/api/courses/:id/episodes", admin.ListEpisodesByCourse)
		adm.POST("/api/courses/:id/episodes", admin.CreateEpisode)
		adm.PUT("/api/episodes/:id", admin.UpdateEpisode)
		adm.DELETE("/api/episodes/:id", admin.DeleteEpisode)
		adm.POST("/api/episodes/reorder", admin.ReorderEpisodes)
		adm.POST("/api/episodes/bulk-delete", admin.BulkDeleteEpisodes)
		adm.POST("/api/episodes/bulk-move", admin.BulkMoveEpisodes)

		// Chapters
		adm.GET("/api/courses/:id/chapters", admin.ListChaptersByCourse)
		adm.POST("/api/courses/:id/chapters", admin.CreateChapter)
		adm.PUT("/api/chapters/:id", admin.UpdateChapter)
		adm.DELETE("/api/chapters/:id", admin.DeleteChapter)
		adm.POST("/api/chapters/reorder", admin.ReorderChapters)

		// Access
		adm.POST("/api/access", admin.GrantAccess)
		adm.POST("/api/access/revoke", admin.RevokeAccess)

		// Reading Room — series / books / articles
		adm.GET("/api/reading-series", admin.ListReadingSeries)
		adm.GET("/api/reading-series/:id/detail", admin.GetReadingSeriesDetail)
		adm.POST("/api/reading-series", admin.CreateReadingSeries)
		adm.PUT("/api/reading-series/:id", admin.UpdateReadingSeries)
		adm.DELETE("/api/reading-series/:id", admin.DeleteReadingSeries)
		adm.GET("/api/reading-books", admin.ListReadingBooks)
		adm.POST("/api/reading-books", admin.CreateReadingBook)
		adm.PUT("/api/reading-books/:id", admin.UpdateReadingBook)
		adm.DELETE("/api/reading-books/:id", admin.DeleteReadingBook)
		adm.GET("/api/reading-articles", admin.ListReadingArticles)
		adm.POST("/api/reading-articles", admin.CreateReadingArticle)
		adm.PUT("/api/reading-articles/:id", admin.UpdateReadingArticle)
		adm.DELETE("/api/reading-articles/:id", admin.DeleteReadingArticle)
		adm.POST("/api/reading-articles/suggest-whitelist", admin.SuggestWhitelist)
		adm.POST("/api/reading-access", admin.GrantReadingAccess)
		adm.POST("/api/reading-access/revoke", admin.RevokeReadingAccess)
		adm.POST("/api/users/:id/reading-access/bulk", admin.BulkReadingAccess)
		adm.GET("/api/reading-import/preview-tree", admin.PreviewReadingImport)
		adm.POST("/api/reading-import/execute", admin.ExecuteReadingImport)

		// Import / Storage / Settings
		adm.GET("/api/import/scan", admin.Scan)
		adm.GET("/api/import/preview-tree", admin.PreviewTree)
		adm.POST("/api/import/execute", admin.ExecuteImport)
		adm.GET("/api/settings", admin.GetSettings)
		adm.PUT("/api/settings", admin.UpdateSettings)

		// Storage sources (multi-source CRUD + ping + user whitelist)
		adm.GET("/api/storage-sources", admin.ListStorageSources)
		adm.POST("/api/storage-sources", admin.CreateStorageSource)
		adm.PUT("/api/storage-sources/:id", admin.UpdateStorageSource)
		adm.DELETE("/api/storage-sources/:id", admin.DeleteStorageSource)
		adm.POST("/api/storage-sources/:id/ping", admin.PingStorageSource)
		adm.GET("/api/users/:id/storage-whitelist", admin.GetStorageWhitelist)
		adm.PUT("/api/users/:id/storage-whitelist", admin.SetStorageWhitelist)

		// Stats / Probe
		adm.GET("/api/stats/dashboard", admin.DashboardStats)
		adm.POST("/api/probe/scan-missing", admin.ScanMissingDurations)
		adm.GET("/api/probe/progress", admin.ProbeProgress)

		// Subtitles
		adm.GET("/api/episodes/:id/subtitles", admin.ListSubtitles)
		adm.GET("/api/subtitles/:id", admin.GetSubtitle)
		adm.POST("/api/episodes/:id/subtitles", admin.SaveSubtitle)
		adm.DELETE("/api/subtitles/:id", admin.DeleteSubtitle)
		adm.POST("/api/subtitles/auto-match", admin.AutoMatchSubtitle)

		// Subtitle generation queue (admin opts episodes in; a worker transcribes)
		adm.POST("/api/subtitle-jobs", admin.EnqueueSubtitleJobs)
		adm.GET("/api/subtitle-jobs", admin.ListSubtitleJobs)
		adm.POST("/api/subtitle-jobs/:id/skip", admin.SkipSubtitleJob)
		adm.POST("/api/subtitle-jobs/:id/retry", admin.RetrySubtitleJob)
		adm.GET("/api/subtitle-jobs/stats", admin.SubtitleJobStats)

		// Attachments
		adm.GET("/api/scan-attachments", admin.ScanAttachments)

		// Badges
		adm.GET("/api/badges", badge.AdminListBadges)
		adm.POST("/api/badges", badge.AdminCreateBadge)
		adm.PUT("/api/badges/:id", badge.AdminUpdateBadge)
		adm.DELETE("/api/badges/:id", badge.AdminDeleteBadge)

		// Subjects
		adm.GET("/api/subjects", subject.AdminListSubjects)
		adm.POST("/api/subjects", subject.AdminCreateSubject)
		adm.PUT("/api/subjects/:id", subject.AdminUpdateSubject)
		adm.DELETE("/api/subjects/:id", subject.AdminDeleteSubject)

		// Tags
		adm.GET("/api/tags", tag.AdminListTags)
		adm.POST("/api/tags", tag.AdminCreateTag)
		adm.PUT("/api/tags/:id", tag.AdminUpdateTag)
		adm.DELETE("/api/tags/:id", tag.AdminDeleteTag)

		// Unlock templates (course-level default strategy)
		adm.GET("/api/courses/:id/unlock-template", unlock.GetTemplate)
		adm.PUT("/api/courses/:id/unlock-template", unlock.SaveTemplate)
		adm.DELETE("/api/courses/:id/unlock-template", unlock.DeleteTemplate)

		// Unlock overrides (per user, course)
		adm.GET("/api/users/:id/unlock-overrides", unlock.ListUserOverrides)
		adm.GET("/api/users/:id/courses/:cid/unlock-override", unlock.GetOverride)
		adm.PUT("/api/users/:id/courses/:cid/unlock-override", unlock.SaveOverride)
		adm.DELETE("/api/users/:id/courses/:cid/unlock-override", unlock.DeleteOverride)
		adm.POST("/api/users/:id/courses/:cid/manual-unlock", unlock.ManualUnlock)
		adm.POST("/api/users/:id/courses/:cid/manual-unlock-undo", unlock.ManualUnlockUndo)
		adm.PUT("/api/users/:id/courses/:cid/allowed-episodes", unlock.SetAllowedEpisodes)
		adm.GET("/api/users/:id/courses/:cid/unlock-preview", unlock.UnlockPreview)

		// Uploads
		adm.POST("/api/upload/image", admin.UploadImage)

		// App releases (APK OTA distribution management)
		adm.GET("/api/releases", release.List)
		adm.POST("/api/releases/upload", release.Upload)
		adm.PUT("/api/releases/:id", release.Update)
		adm.DELETE("/api/releases/:id", release.Delete)
	}

	// 6. Serve the embedded SPA. We expose hashed assets under /admin/assets
	// (registered BEFORE the SPA-index fallback) and treat any other /admin
	// path as a client-side route, returning index.html.
	registerAdminSPA(r)
}

// registerAdminSPA serves the React build embedded via spa.Assets. Any path
// under /admin that isn't an /admin/api/* route or a hashed asset falls through
// to index.html so the SPA router can take over (deep links, refreshes).
//
// On a fresh clone (before `make build`), dist/ is empty, so every /admin
// request falls back to spa.NotBuiltHTML — a friendly "please build the SPA"
// page. The Go binary still compiles and boots; only the admin UI is unavailable.
func registerAdminSPA(r *gin.Engine) {
	assets, err := fs.Sub(spa.Assets, "dist")
	if err != nil {
		// Should never happen (embed always succeeds), but guard anyway.
		serveNotBuilt(r)
		return
	}

	// Check whether a real build exists. If not, register the "not built"
	// fallback for all admin routes.
	if _, statErr := fs.Stat(assets, "index.html"); statErr != nil {
		serveNotBuilt(r)
		return
	}

	// Hashed static assets (JS/CSS/images) — long-cacheable.
	r.StaticFS("/admin/assets", http.FS(subFS(assets, "assets")))

	// Explicit root + login routes return index.html.
	r.GET("/admin", serveSPAIndex(assets))
	r.GET("/admin/login", serveSPAIndex(assets))

	// Catch-all for any other /admin/* path that wasn't matched above (deep
	// links like /admin/courses/123). Gin's NoRoute handles all unmatched
	// routes app-wide; we restrict to /admin so other 404s still work.
	// /admin/api/* misses are NOT SPA routes — they get a JSON 404.
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/admin/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if strings.HasPrefix(p, "/admin") {
			serveSPAIndex(assets)(c)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})
}

// serveNotBuilt registers /admin and a /admin/* catch-all that returns the
// "SPA not yet built" page, used when dist/index.html is missing.
func serveNotBuilt(r *gin.Engine) {
	r.GET("/admin", func(c *gin.Context) {
		c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8", spa.NotBuiltHTML)
	})
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/admin") {
			c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8", spa.NotBuiltHTML)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})
}

// subFS returns a sub-filesystem rooted at dir if it exists, otherwise the
// original fs (StaticFS handles a missing dir gracefully enough for boot).
func subFS(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return fsys
	}
	return sub
}

func serveSPAIndex(assets fs.FS) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8", spa.NotBuiltHTML)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	}
}
