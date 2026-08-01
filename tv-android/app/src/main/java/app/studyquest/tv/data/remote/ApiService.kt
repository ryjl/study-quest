package app.studyquest.tv.data.remote

import app.studyquest.tv.data.remote.dto.AdviceResponseDto
import app.studyquest.tv.data.remote.dto.ChapterDto
import app.studyquest.tv.data.remote.dto.CourseDto
import app.studyquest.tv.data.remote.dto.EpisodeDto
import app.studyquest.tv.data.remote.dto.EpisodeSummaryDto
import app.studyquest.tv.data.remote.dto.GradeTagDto
import app.studyquest.tv.data.remote.dto.LoginRequestDto
import app.studyquest.tv.data.remote.dto.LoginResponseDto
import app.studyquest.tv.data.remote.dto.PlayInfoDto
import app.studyquest.tv.data.remote.dto.PointsLedgerDto
import app.studyquest.tv.data.remote.dto.ReportProgressRequest
import app.studyquest.tv.data.remote.dto.SubjectDto
import app.studyquest.tv.data.remote.dto.UserDto
import app.studyquest.tv.data.remote.dto.UserPointDto
import app.studyquest.tv.data.remote.dto.UserProgressDto
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Retrofit API 接口。方法签名对照 PAD 端
 * `frontend/lib/service/api_service.dart`（FROZEN 契约，路径/参数/字段名不可改）。
 *
 * 鉴权：`Authorization: Bearer <token>` 由 NetworkModule 里的拦截器统一注入
 * （token 从 TokenStore 读，可能为空——未登录时仍可调 `/users` 与 `/users/login`）。
 *
 * baseUrl 动态：Retrofit 构造时给占位 `http://localhost:8080`（baseUrl 不能为空），
 * 真实请求由拦截器改写到 TokenStore 里存的 backend_base_url。首次启动未配置时
 * 不应该发任何业务请求（UI 引导用户先配 baseUrl）。
 */
interface ApiService {

    // ── 1. 登录选人页 ───────────────────────────────────────────────────────

    /** GET /api/v1/users —— 列出可选用户（登录选人页）。无需鉴权。 */
    @GET("api/v1/users")
    suspend fun fetchUsers(): List<UserDto>

    /**
     * POST /api/v1/users/login —— PIN 登录。
     * 请求体 `{user_id, pin, device_name?}`，响应 `{token, role, user_id}`。
     * 成功后由调用方把 token 存进 TokenStore。
     */
    @POST("api/v1/users/login")
    suspend fun login(@Body body: LoginRequestDto): LoginResponseDto

    /** POST /api/v1/users/logout —— 注销当前 session（token 从 header 取）。幂等。 */
    @POST("api/v1/users/logout")
    suspend fun logout()

    // ── 2. 课程大厅 / 详情 ─────────────────────────────────────────────────

    /** GET /api/v1/courses?content_type=learning —— 课程大厅。 */
    @GET("api/v1/courses")
    suspend fun fetchCourses(
        @Query("content_type") contentType: String = "learning",
    ): List<CourseDto>

    /** GET /api/v1/courses/{id} —— 单个课程详情。 */
    @GET("api/v1/courses/{id}")
    suspend fun fetchCourse(@Path("id") courseId: Int): CourseDto

    /** GET /api/v1/courses/{id}/episodes —— 课时明细（平铺列表，前端按 chapter 分组）。 */
    @GET("api/v1/courses/{id}/episodes")
    suspend fun fetchEpisodes(@Path("id") courseId: Int): List<EpisodeDto>

    /** GET /api/v1/courses/{id}/chapters —— 章节目录（章节分组器输入，见 business-rules.md 第 3 节）。 */
    @GET("api/v1/courses/{id}/chapters")
    suspend fun fetchChapters(@Path("id") courseId: Int): List<ChapterDto>

    /** GET /api/v1/courses/{id}/last-watched —— 最近观看的课时（断点续看入口）。 */
    @GET("api/v1/courses/{id}/last-watched")
    suspend fun fetchLastWatched(@Path("id") courseId: Int): EpisodeDto

    /**
     * GET /api/v1/courses/grade-tags —— 学段 tag 目录(课程卡片角标 / 过滤栏)。
     * 对照 PAD `ApiService.fetchGradeTags`。失败时 Repository 用 [PresetGradeTags] 兜底。
     */
    @GET("api/v1/courses/grade-tags")
    suspend fun fetchGradeTags(): List<GradeTagDto>

    /**
     * GET /api/v1/subjects —— 学科目录(课程卡片渐变色 / 学科名 label)。
     * 对照 PAD `ApiService.fetchSubjects`。需要学生鉴权头。
     */
    @GET("api/v1/subjects")
    suspend fun fetchSubjects(): List<SubjectDto>

    // ── 3. 播放器 ──────────────────────────────────────────────────────────

    /**
     * GET /api/v1/episodes/{id}/play-info —— 播放器核心契约。
     * 返回 url/headers/progress/subtitles。`headers` 是给 ExoPlayer 的网盘
     * 鉴权头（Referer/UA），不是 Retrofit 的请求头（见 business-rules.md 第 7 节）。
     */
    @GET("api/v1/episodes/{id}/play-info")
    suspend fun fetchPlayInfo(@Path("id") episodeId: Int): PlayInfoDto

    // ── 4. 进度 / 积分 ─────────────────────────────────────────────────────

    /**
     * POST /api/v1/progress/report —— 进度上报（防作弊，business-rules.md 第 4 节）。
     * 客户端只在 5s tick 且 delta∈(0,30] 时调用；后端再 clamp 兜底。
     */
    @POST("api/v1/progress/report")
    suspend fun reportProgress(@Body body: ReportProgressRequest): UserProgressDto

    /** GET /api/v1/progress/points —— 积分余额。 */
    @GET("api/v1/progress/points")
    suspend fun fetchUserPoints(): UserPointDto

    /** GET /api/v1/progress —— 用户所有课时进度列表(成长足迹:完成数/总时长统计)。 */
    @GET("api/v1/progress")
    suspend fun fetchProgressOverview(): List<UserProgressDto>

    /**
     * GET /api/v1/progress/ledger?limit=&offset= —— 积分流水(成长足迹时间线)。
     * limit 默认后端给,客户端按需传(足迹页取最近若干条)。
     */
    @GET("api/v1/progress/ledger")
    suspend fun fetchPointsLedger(
        @Query("limit") limit: Int? = null,
        @Query("offset") offset: Int? = null,
    ): List<PointsLedgerDto>

    // ── 5. AI 学习（总结 / 建议） ──────────────────────────────────────────

    /**
     * GET /api/v1/episodes/{id}/ai-summary —— 单集总结。
     * 404 表示无总结 / AI 未开（见后端 ai_handler.go），Repository 层用
     * `Response<EpisodeSummaryDto>` 或 try/catch HttpException 转成 null。
     */
    @GET("api/v1/episodes/{id}/ai-summary")
    suspend fun fetchEpisodeSummary(@Path("id") episodeId: Int): EpisodeSummaryDto

    /**
     * GET /api/v1/episodes/{id}/ai-advice —— 单集学习建议。
     * 200/202 → parse adviceResponse；404 → unavailable（AI 未配置 / 无 mastery）。
     */
    @GET("api/v1/episodes/{id}/ai-advice")
    suspend fun fetchEpisodeAdvice(@Path("id") episodeId: Int): AdviceResponseDto
}
