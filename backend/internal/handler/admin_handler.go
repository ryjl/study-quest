package handler

import (
	"crypto/rand"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"
)


// AdminHandler handles backend administrator views and control APIs.
type AdminHandler interface {
	// Auth (public)
	LoginAPI(c *gin.Context)
	LogoutAPI(c *gin.Context)
	Me(c *gin.Context)

	// Stats / probe
	DashboardStats(c *gin.Context)
	ScanMissingDurations(c *gin.Context)
	ProbeProgress(c *gin.Context)

	// Users
	ListUsers(c *gin.Context)
	CreateUser(c *gin.Context)
	UpdateUser(c *gin.Context)
	DeleteUser(c *gin.Context)
	BulkAccess(c *gin.Context)
	UserLedger(c *gin.Context)
	UserBadges(c *gin.Context)

	// Courses
	ListCourses(c *gin.Context)
	GetCourseDetail(c *gin.Context)
	CreateCourse(c *gin.Context)
	UpdateCourse(c *gin.Context)
	DeleteCourse(c *gin.Context)

	// Episodes
	ListEpisodesByCourse(c *gin.Context)
	CreateEpisode(c *gin.Context)
	UpdateEpisode(c *gin.Context)
	DeleteEpisode(c *gin.Context)
	ReorderEpisodes(c *gin.Context)
	BulkDeleteEpisodes(c *gin.Context)
	BulkMoveEpisodes(c *gin.Context)

	// Chapters
	ListChaptersByCourse(c *gin.Context)
	CreateChapter(c *gin.Context)
	UpdateChapter(c *gin.Context)
	DeleteChapter(c *gin.Context)
	ReorderChapters(c *gin.Context)

	// Access
	GrantAccess(c *gin.Context)
	RevokeAccess(c *gin.Context)

	// Reading Room — series / books / articles CRUD + access
	ListReadingSeries(c *gin.Context)
	GetReadingSeriesDetail(c *gin.Context)
	CreateReadingSeries(c *gin.Context)
	UpdateReadingSeries(c *gin.Context)
	DeleteReadingSeries(c *gin.Context)
	ListReadingBooks(c *gin.Context)
	CreateReadingBook(c *gin.Context)
	UpdateReadingBook(c *gin.Context)
	DeleteReadingBook(c *gin.Context)
	ListReadingArticles(c *gin.Context)
	CreateReadingArticle(c *gin.Context)
	UpdateReadingArticle(c *gin.Context)
	DeleteReadingArticle(c *gin.Context)
	SuggestWhitelist(c *gin.Context)
	GrantReadingAccess(c *gin.Context)
	RevokeReadingAccess(c *gin.Context)
	BulkReadingAccess(c *gin.Context)
	PreviewReadingImport(c *gin.Context)
	ExecuteReadingImport(c *gin.Context)

	// Import / Settings / Storage
	Scan(c *gin.Context)
	PreviewTree(c *gin.Context)
	ExecuteImport(c *gin.Context)
	GetSettings(c *gin.Context)
	UpdateSettings(c *gin.Context)

	// Subtitles
	ListSubtitles(c *gin.Context)
	GetSubtitle(c *gin.Context)
	SaveSubtitle(c *gin.Context)
	DeleteSubtitle(c *gin.Context)
	AutoMatchSubtitle(c *gin.Context)

	// Subtitle generation queue
	EnqueueSubtitleJobs(c *gin.Context)
	ListSubtitleJobs(c *gin.Context)
	SkipSubtitleJob(c *gin.Context)
	RetrySubtitleJob(c *gin.Context)
	SubtitleJobStats(c *gin.Context)

	// Attachments + Uploads
	ScanAttachments(c *gin.Context)
	UploadImage(c *gin.Context)

	// User sessions (admin manages which devices are logged in per user)
	ListUserSessions(c *gin.Context)
	RevokeUserSession(c *gin.Context)
	RevokeAllUserSessions(c *gin.Context)
	UpdateSessionNote(c *gin.Context)

	// Watch history (admin per-day viewing timeline + heatmap)
	GetUserWatchHistory(c *gin.Context)
	GetUserWatchEvents(c *gin.Context)

	// Storage sources (multi-source CRUD + per-source ping + user whitelist)
	ListStorageSources(c *gin.Context)
	CreateStorageSource(c *gin.Context)
	UpdateStorageSource(c *gin.Context)
	DeleteStorageSource(c *gin.Context)
	PingStorageSource(c *gin.Context)
	GetStorageWhitelist(c *gin.Context)
	SetStorageWhitelist(c *gin.Context)
	// AI module (Step 3) — provider config + diagnostics.
	ListAIProviders(c *gin.Context)
	CreateAIProvider(c *gin.Context)
	UpdateAIProvider(c *gin.Context)
	DeleteAIProvider(c *gin.Context)
	TestAIProvider(c *gin.Context)
	GetAIStatus(c *gin.Context)
	// AI module — generation jobs + observability.
	EnqueueAIJobs(c *gin.Context)
	ListAIJobs(c *gin.Context)
	GetAIJob(c *gin.Context)
	ResetAIJob(c *gin.Context)
	ListAIRuns(c *gin.Context)
	GetAIRun(c *gin.Context)
	// Phase C — quiz observability (per-user drill-down + summary content).
	GetAISummary(c *gin.Context)
	ListUserQuizzes(c *gin.Context)
	GetQuizDetail(c *gin.Context)
	// Phase D — 课程级总结(admin 触发生成 + 读取,course-unique 纯内容总结)。
	TriggerCourseSummary(c *gin.Context)
	GetCourseSummary(c *gin.Context)
	// Phase E — admin 用户学习报告(agent 驱动,跨课程画像)。
	TriggerUserStudyReport(c *gin.Context)
	GetUserStudyReport(c *gin.Context)
}

type adminHandler struct {
	settingsRepo        repository.SettingsRepository
	userRepo            repository.UserRepository
	courseRepo          repository.CourseRepository
	episodeRepo         repository.EpisodeRepository
	chapterRepo         repository.ChapterRepository
	progressRepo        repository.ProgressRepository
	subjectRepo         repository.SubjectRepository
	badgeRepo           repository.BadgeRepository
	readingSeriesRepo   repository.ReadingSeriesRepository
	readingBookRepo     repository.ReadingBookRepository
	readingArticleRepo  repository.ReadingArticleRepository
	userService         service.UserService
	courseService       service.CourseService
	importService       service.ImportService
	episodeService      service.EpisodeService
	chapterService      service.ChapterService
	badgeService        service.BadgeService
	readingSeriesService service.ReadingSeriesService
	readingBookService   service.ReadingBookService
	readingArticleService service.ReadingArticleService
	readingImportService service.ReadingImportService
	probeWorker         *service.ProbeWorker
	subtitleJobService  service.SubtitleJobService
	sessionService      service.SessionService
	watchEventRepo      repository.WatchEventRepository
	storageSourceRepo   repository.StorageSourceRepository
	storageResolver     *service.StorageProviderResolver
	// AI module (Step 3). nil-safe: when the AI subsystem isn't wired, these
	// stay nil and the admin AI endpoints respond with 503 / empty, so the rest
	// of the panel is unaffected.
	aiProviderRepo repository.AIProviderRepository
	aiResolver     *ai.ProviderResolver
	aiService      service.AIService
}

// NewAdminHandler creates an instance of AdminHandler.
// AdminHandlerDeps carries all the dependencies an AdminHandler needs, as
// named fields. This replaces the old 15-positional-argument constructor,
// where swapping two interface args of similar shape compiled fine but wired
// the wrong repo. Construct via NewAdminHandlerDeps() + fluent setters, then
// Build(). main.go and testhelper are the only two call sites.
type AdminHandlerDeps struct {
	SettingsRepo repository.SettingsRepository
	UserRepo     repository.UserRepository
	CourseRepo   repository.CourseRepository
	EpisodeRepo  repository.EpisodeRepository
	ChapterRepo  repository.ChapterRepository
	ProgressRepo repository.ProgressRepository
	SubjectRepo  repository.SubjectRepository
	BadgeRepo    repository.BadgeRepository

	ReadingSeriesRepo  repository.ReadingSeriesRepository
	ReadingBookRepo    repository.ReadingBookRepository
	ReadingArticleRepo repository.ReadingArticleRepository

	UserService          service.UserService
	CourseService        service.CourseService
	ImportService        service.ImportService
	EpisodeService       service.EpisodeService
	ChapterService       service.ChapterService
	BadgeService         service.BadgeService
	ReadingSeriesService service.ReadingSeriesService
	ReadingBookService   service.ReadingBookService
	ReadingArticleService service.ReadingArticleService
	ReadingImportService service.ReadingImportService
	ProbeWorker          *service.ProbeWorker
	SubtitleJobService   service.SubtitleJobService
	SessionService       service.SessionService
	WatchEventRepo       repository.WatchEventRepository
	StorageSourceRepo    repository.StorageSourceRepository
	StorageResolver      *service.StorageProviderResolver
	AIProviderRepo       repository.AIProviderRepository
	AIResolver           *ai.ProviderResolver
	AIService            service.AIService
}

// NewAdminHandlerDeps is the entry point for the AdminHandler builder.
func NewAdminHandlerDeps() *AdminHandlerDeps { return &AdminHandlerDeps{} }

// Fluent setters — each returns the deps pointer so calls chain. Named setters
// make wiring self-documenting and swap-proof (vs the old positional args).

func (d *AdminHandlerDeps) WithSettings(r repository.SettingsRepository) *AdminHandlerDeps        { d.SettingsRepo = r; return d }
func (d *AdminHandlerDeps) WithUsers(r repository.UserRepository) *AdminHandlerDeps               { d.UserRepo = r; return d }
func (d *AdminHandlerDeps) WithCourses(r repository.CourseRepository) *AdminHandlerDeps           { d.CourseRepo = r; return d }
func (d *AdminHandlerDeps) WithEpisodes(r repository.EpisodeRepository) *AdminHandlerDeps          { d.EpisodeRepo = r; return d }
func (d *AdminHandlerDeps) WithChapters(r repository.ChapterRepository) *AdminHandlerDeps          { d.ChapterRepo = r; return d }
func (d *AdminHandlerDeps) WithProgress(r repository.ProgressRepository) *AdminHandlerDeps         { d.ProgressRepo = r; return d }
func (d *AdminHandlerDeps) WithSubjects(r repository.SubjectRepository) *AdminHandlerDeps          { d.SubjectRepo = r; return d }
func (d *AdminHandlerDeps) WithBadges(r repository.BadgeRepository) *AdminHandlerDeps              { d.BadgeRepo = r; return d }
func (d *AdminHandlerDeps) WithReadingSeriesRepo(r repository.ReadingSeriesRepository) *AdminHandlerDeps  { d.ReadingSeriesRepo = r; return d }
func (d *AdminHandlerDeps) WithReadingBookRepo(r repository.ReadingBookRepository) *AdminHandlerDeps      { d.ReadingBookRepo = r; return d }
func (d *AdminHandlerDeps) WithReadingArticleRepo(r repository.ReadingArticleRepository) *AdminHandlerDeps { d.ReadingArticleRepo = r; return d }
func (d *AdminHandlerDeps) WithUserService(s service.UserService) *AdminHandlerDeps                { d.UserService = s; return d }
func (d *AdminHandlerDeps) WithCourseService(s service.CourseService) *AdminHandlerDeps            { d.CourseService = s; return d }
func (d *AdminHandlerDeps) WithImportService(s service.ImportService) *AdminHandlerDeps            { d.ImportService = s; return d }
func (d *AdminHandlerDeps) WithEpisodeService(s service.EpisodeService) *AdminHandlerDeps          { d.EpisodeService = s; return d }
func (d *AdminHandlerDeps) WithChapterService(s service.ChapterService) *AdminHandlerDeps          { d.ChapterService = s; return d }
func (d *AdminHandlerDeps) WithBadgeService(s service.BadgeService) *AdminHandlerDeps              { d.BadgeService = s; return d }
func (d *AdminHandlerDeps) WithReadingSeriesService(s service.ReadingSeriesService) *AdminHandlerDeps   { d.ReadingSeriesService = s; return d }
func (d *AdminHandlerDeps) WithReadingBookService(s service.ReadingBookService) *AdminHandlerDeps       { d.ReadingBookService = s; return d }
func (d *AdminHandlerDeps) WithReadingArticleService(s service.ReadingArticleService) *AdminHandlerDeps { d.ReadingArticleService = s; return d }
func (d *AdminHandlerDeps) WithReadingImportService(s service.ReadingImportService) *AdminHandlerDeps  { d.ReadingImportService = s; return d }
func (d *AdminHandlerDeps) WithProbeWorker(w *service.ProbeWorker) *AdminHandlerDeps               { d.ProbeWorker = w; return d }
func (d *AdminHandlerDeps) WithSubtitleJobService(s service.SubtitleJobService) *AdminHandlerDeps   { d.SubtitleJobService = s; return d }
func (d *AdminHandlerDeps) WithSessionService(s service.SessionService) *AdminHandlerDeps          { d.SessionService = s; return d }
func (d *AdminHandlerDeps) WithWatchEventRepo(r repository.WatchEventRepository) *AdminHandlerDeps { d.WatchEventRepo = r; return d }
func (d *AdminHandlerDeps) WithStorageSources(r repository.StorageSourceRepository) *AdminHandlerDeps { d.StorageSourceRepo = r; return d }
func (d *AdminHandlerDeps) WithStorageResolver(r *service.StorageProviderResolver) *AdminHandlerDeps { d.StorageResolver = r; return d }
func (d *AdminHandlerDeps) WithAIProviderRepo(r repository.AIProviderRepository) *AdminHandlerDeps { d.AIProviderRepo = r; return d }
func (d *AdminHandlerDeps) WithAIResolver(r *ai.ProviderResolver) *AdminHandlerDeps            { d.AIResolver = r; return d }
func (d *AdminHandlerDeps) WithAIService(s service.AIService) *AdminHandlerDeps                 { d.AIService = s; return d }

// Build assembles the AdminHandler from the configured deps. Call this last.
func (d *AdminHandlerDeps) Build() AdminHandler {
	return &adminHandler{
		settingsRepo:         d.SettingsRepo,
		userRepo:             d.UserRepo,
		courseRepo:           d.CourseRepo,
		episodeRepo:          d.EpisodeRepo,
		chapterRepo:          d.ChapterRepo,
		progressRepo:         d.ProgressRepo,
		subjectRepo:          d.SubjectRepo,
		badgeRepo:            d.BadgeRepo,
		readingSeriesRepo:    d.ReadingSeriesRepo,
		readingBookRepo:      d.ReadingBookRepo,
		readingArticleRepo:   d.ReadingArticleRepo,
		userService:          d.UserService,
		courseService:        d.CourseService,
		importService:        d.ImportService,
		episodeService:       d.EpisodeService,
		chapterService:       d.ChapterService,
		badgeService:         d.BadgeService,
		readingSeriesService:  d.ReadingSeriesService,
		readingBookService:    d.ReadingBookService,
		readingArticleService: d.ReadingArticleService,
		readingImportService: d.ReadingImportService,
		probeWorker:          d.ProbeWorker,
		subtitleJobService:   d.SubtitleJobService,
		sessionService:       d.SessionService,
		watchEventRepo:       d.WatchEventRepo,
		storageSourceRepo:    d.StorageSourceRepo,
		storageResolver:      d.StorageResolver,
		aiProviderRepo:       d.AIProviderRepo,
		aiResolver:           d.AIResolver,
		aiService:            d.AIService,
	}
}

// LoginAPI authenticates the admin password and sets the session cookie.
// Replaces the old form-based LoginPost with a JSON endpoint for the SPA.
func parseUintParam(c *gin.Context, name string) (uint, error) {
	id, err := strconv.ParseUint(c.Param(name), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint(id), nil
}

// API CONTROLLERS

func generateRandomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// ScanMissingDurations finds every episode whose duration_seconds is NULL and
// enqueues it onto the background probe worker. The worker probes one at a
// time with a fixed gap (see probe_worker.go); progress is polled separately
// via ProbeProgress.