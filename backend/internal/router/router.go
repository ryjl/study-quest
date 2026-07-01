package router

import (
	"studyquest/backend/internal/handler"
	"studyquest/backend/internal/middleware"
	"studyquest/backend/internal/repository"

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
	admin handler.AdminHandler,
	userRepo repository.UserRepository,
	settingsRepo repository.SettingsRepository,
) {
	// Enable Global recovery and logger middlewares
	r.Use(gin.Recovery())
	r.Use(middleware.CORSMiddleware())

	// Load Admin HTML templates
	r.LoadHTMLGlob("internal/admin/templates/*.html")

	// 1. Health check & Initial login list (Public)
	v1 := r.Group("/api/v1")
	{
		v1.GET("/health", health.Check)
		v1.GET("/users", user.GetUsers)
		v1.GET("/users/login", user.Login) // Allow GET login list too
		v1.POST("/users/login", user.Login)
		
		// VLC / external player playback streaming is public for format compatibility
		v1.GET("/episodes/:id/stream", episode.Stream)
	}

	// 2. Client restricted operations (UserAuth required)
	v1Restricted := r.Group("/api/v1")
	v1Restricted.Use(middleware.UserAuthMiddleware(userRepo))
	{
		v1Restricted.GET("/courses", course.GetCourses)
		v1Restricted.GET("/courses/:id", course.GetCourseByID)
		v1Restricted.GET("/courses/:id/episodes", course.GetEpisodesByCourse)
		
		v1Restricted.GET("/episodes/:id", episode.GetEpisodeByID)
		v1Restricted.GET("/episodes/:id/play-info", episode.GetPlayInfo)
		v1Restricted.GET("/episodes/:id/subtitle", episode.GetSubtitle)
		v1Restricted.GET("/episodes/:id/ai-content", episode.GetAIContent)
		v1Restricted.GET("/episodes/:id/attachments", episode.GetAttachments)

		v1Restricted.POST("/progress/report", progress.ReportProgress)
		v1Restricted.GET("/progress", progress.GetProgressOverview)
		v1Restricted.GET("/progress/points", progress.GetPoints)
	}

	// 3. Local Python Toolchain ingestion points (Public)
	v1Ingest := r.Group("/api/v1")
	{
		v1Ingest.POST("/ingest/episodes", ingest.IngestEpisodes)
		v1Ingest.POST("/ingest/ai-content", ingest.IngestAIContent)
	}

	// 4. Admin panel session routes (Public)
	r.GET("/admin/login", admin.LoginGet)
	r.POST("/admin/login", admin.LoginPost)
	r.GET("/admin/logout", admin.Logout)

	// 5. Protected Admin dashboard panel and APIs
	adm := r.Group("/admin")
	adm.Use(middleware.AdminAuthMiddleware(settingsRepo))
	{
		// Render pages
		adm.GET("/", admin.Dashboard)
		adm.GET("/users", admin.UsersGet)
		adm.GET("/courses", admin.CoursesGet)
		adm.GET("/import", admin.ImportGet)
		adm.GET("/settings", admin.SettingsGet)

		// Backend actions
		adm.POST("/api/users", admin.CreateUser)
		adm.DELETE("/api/users/:id", admin.DeleteUser)
		adm.POST("/api/courses", admin.CreateCourse)
		adm.DELETE("/api/courses/:id", admin.DeleteCourse)
		
		adm.POST("/api/access", admin.GrantAccess)
		adm.POST("/api/access/revoke", admin.RevokeAccess)
		
		adm.GET("/api/import/scan", admin.Scan)
		adm.POST("/api/import/execute", admin.ExecuteImport)
		adm.PUT("/api/settings", admin.UpdateSettings)
		adm.GET("/api/storage/ping", admin.PingStorage)
		adm.POST("/api/storage/ping", admin.PingStorage)
	}
}
