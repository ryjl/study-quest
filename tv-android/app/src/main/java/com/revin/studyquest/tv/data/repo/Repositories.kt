package com.revin.studyquest.tv.data.repo

import com.revin.studyquest.tv.data.local.StoredUser
import com.revin.studyquest.tv.data.local.TokenStore
import com.revin.studyquest.tv.data.remote.ApiService
import com.revin.studyquest.tv.data.remote.dto.LoginRequestDto
import com.revin.studyquest.tv.data.remote.dto.LoginResponseDto
import com.revin.studyquest.tv.data.remote.dto.UserDto
import retrofit2.HttpException
import javax.inject.Inject
import javax.inject.Singleton

/**
 * 鉴权仓库：薄封装 ApiService + TokenStore，承接 PAD 端 AuthService 的职责。
 *
 * 关键语义对齐 auth_service.dart：
 *  - `login` 成功后把 token + user 持久化（PAD 端 login() 里做 prefs.setString）。
 *  - `logout` 先调后端 revoke（失败也吞），再清本地（网络失败不能把坏 token 留内存）。
 *  - `isAuthenticated` 要求 user + token 都在（旧客户端升级：有 user 无 token 视为未登录）。
 */
@Singleton
class AuthRepo @Inject constructor(
    private val api: ApiService,
    private val tokenStore: TokenStore,
) {

    /** 列出可选用户（登录选人页）。 */
    suspend fun fetchUsers(): List<UserDto> = api.fetchUsers()

    /**
     * PIN 登录。成功 → 持久化 token + user，返回 true；失败抛异常或返回 false。
     * deviceName 可空（OS 设备标签给 admin 设备列表，缺失后端回退到 UA）。
     */
    suspend fun login(user: UserDto, pin: String, deviceName: String? = null): Boolean {
        val resp: LoginResponseDto = try {
            api.login(LoginRequestDto(userId = user.id, pin = pin, deviceName = deviceName))
        } catch (e: HttpException) {
            // 401 = PIN 错；其他状态也不该算成功。
            return false
        }
        if (resp.token.isBlank()) return false
        tokenStore.saveToken(resp.token)
        // 存的 user 形状对齐 PAD 端 auth_service.dart（id/nickname/avatarUrl/role）。
        tokenStore.saveCurrentUser(
            StoredUser(
                id = user.id,
                nickname = user.nickname,
                avatarUrl = user.avatarUrl,
                role = user.role,
            )
        )
        return true
    }

    /** 注销：调后端 revoke（失败吞掉），再清本地登录态。幂等。 */
    suspend fun logout() {
        runCatching { api.logout() } // 网络失败不阻塞本地清理。
        tokenStore.clearAuth()
    }

    /** 当前是否已登录（user + token 都在）。 */
    suspend fun isAuthenticated(): Boolean {
        return tokenStore.getToken()?.isNotBlank() == true &&
            tokenStore.getCurrentUser() != null
    }
}

/**
 * 课程仓库：薄封装课程/课时/章节/进度相关端点。
 *
 * AI summary / advice 的 404 → null 转换也放这里（PAD 端在 api_service.dart 里
 * 做：`if (response.statusCode == 404) return null`）。Retrofit suspend 函数会
 * 把非 2xx 抛 HttpException，这里 catch 404 转成 null。
 */
@Singleton
class CourseRepo @Inject constructor(
    private val api: ApiService,
) {

    suspend fun fetchCourses(contentType: String = "learning") = api.fetchCourses(contentType)
    suspend fun fetchCourse(id: Int) = api.fetchCourse(id)
    suspend fun fetchEpisodes(courseId: Int) = api.fetchEpisodes(courseId)
    suspend fun fetchChapters(courseId: Int) = api.fetchChapters(courseId)
    suspend fun fetchLastWatched(courseId: Int) = api.fetchLastWatched(courseId)

    /** play-info：播放器核心契约。 */
    suspend fun fetchPlayInfo(episodeId: Int) = api.fetchPlayInfo(episodeId)

    /** 进度上报（防作弊，business-rules.md 第 4 节；客户端只在 delta∈(0,30] 调）。 */
    suspend fun reportProgress(episodeId: Int, positionSeconds: Int, deltaWatchSeconds: Int) =
        api.reportProgress(
            com.revin.studyquest.tv.data.remote.dto.ReportProgressRequest(
                episodeId = episodeId,
                positionSeconds = positionSeconds,
                deltaWatchSeconds = deltaWatchSeconds,
            )
        )

    /** 积分余额。 */
    suspend fun fetchUserPoints() = api.fetchUserPoints()

    /**
     * 单集总结。404 → null（无总结 / AI 未开）。
     * 其他非 2xx 重新抛出（让 ViewModel 决定怎么提示）。
     */
    suspend fun fetchEpisodeSummary(episodeId: Int): com.revin.studyquest.tv.data.remote.dto.EpisodeSummaryDto? {
        return try {
            api.fetchEpisodeSummary(episodeId)
        } catch (e: HttpException) {
            if (e.code() == 404) null else throw e
        }
    }

    /**
     * 单集学习建议。404 → unavailable 状态对象（对齐 PAD 端 api_service.dart
     * `fetchEpisodeAdvice` 的 404 分支：返回 `AdviceResponse(status: unavailable)`）。
     */
    suspend fun fetchEpisodeAdvice(episodeId: Int): com.revin.studyquest.tv.data.remote.dto.AdviceResponseDto {
        return try {
            api.fetchEpisodeAdvice(episodeId)
        } catch (e: HttpException) {
            if (e.code() == 404) {
                com.revin.studyquest.tv.data.remote.dto.AdviceResponseDto(status = "unavailable")
            } else {
                throw e
            }
        }
    }
}

/**
 * URL 工具：把后端 server-relative 路径（`/uploads/xxx.jpg`、
 * `/api/v1/subtitles/3.vtt`）拼成绝对 URL。
 *
 * 对齐 PAD 端 api_service.dart 的 `absoluteUrl` + user.dart 的 `_resolveAvatar`。
 * 已经是绝对 URL（http/https）直接透传。空串透传空串。
 *
 * 放这里而不是 DTO 里：拼接需要 baseUrl，DTO 保持纯数据。调用方（UI / 播放器）
 * 注入 baseUrl 调这个函数。
 */
object UrlResolver {
    fun absolute(baseUrl: String?, relativeOrAbsolute: String): String {
        if (relativeOrAbsolute.isEmpty()) return relativeOrAbsolute
        if (relativeOrAbsolute.startsWith("http://") ||
            relativeOrAbsolute.startsWith("https://")
        ) return relativeOrAbsolute
        val base = baseUrl?.trimEnd('/')
            ?: return relativeOrAbsolute // 无 baseUrl 兜底原样返回
        return if (relativeOrAbsolute.startsWith("/")) {
            base + relativeOrAbsolute
        } else {
            "$base/$relativeOrAbsolute"
        }
    }
}
