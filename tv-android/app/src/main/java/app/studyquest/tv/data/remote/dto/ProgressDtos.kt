package app.studyquest.tv.data.remote.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * 用户对单集的观看进度。对应 Dart: `UserProgress`。
 *
 * **字段大小写注意**:后端有两个进度端点,字段大小写不同:
 *   - `/progress/report` 响应:snake_case(`episode_id` / `is_completed` 等)。
 *   - `/progress` 列表:**PascalCase**(`EpisodeID` / `IsCompleted` 等),且带嵌套
 *     关联对象(User/Episode)。这是 Go ORM 默认序列化的结果。
 *
 * 课程详情页用 `/progress` 列表(对照 PAD `fetchProgressOverview`),所以这里按
 * **PascalCase** 标注(对齐实际下发的 `/progress` 响应)。`/progress/report` 的
 * 响应字段在 [ReportProgressRequest] 是请求体不涉及,且 report 的响应 TV 端目前
 * 不消费(只看成功/失败),所以 PascalCase 标注不影响 report 流程。
 *
 * 嵌套的 User/Episode 关联对象(后端 ORM preload 的)这里不解析 ——
 * `Json.ignoreUnknownKeys = true` 会忽略它们,我们只取扁平的进度字段。
 *
 * 对应 Dart: `UserProgress.fromJson`(PAD 端用双键读取兼容大小写,
 * 这里以 PascalCase 为准,跟 CourseDto/EpisodeDto 一致)。
 */
@Serializable
data class UserProgressDto(
    @SerialName("ID") val id: Int = 0,
    @SerialName("UserID") val userId: Int = 0,
    @SerialName("EpisodeID") val episodeId: Int = 0,
    @SerialName("LastPositionSeconds") val lastPositionSeconds: Int = 0,
    @SerialName("WatchSeconds") val watchSeconds: Int = 0,
    @SerialName("IsCompleted") val isCompleted: Boolean = false,
)

/**
 * `/progress/points` 响应：积分余额。对应 Dart: `UserPoint`。
 */
@Serializable
data class UserPointDto(
    @SerialName("user_id") val userId: Int = 0,
    @SerialName("current_points") val currentPoints: Int = 0,
    @SerialName("total_earned_points") val totalEarnedPoints: Int = 0,
)

/**
 * `/progress/report` 请求体（business-rules.md 第 4 节进度上报防作弊）。
 *
 * 字段对齐 Dart: api_service.dart reportProgress 的 body；后端 progress_handler
 * 读 `episode_id` / `position_seconds` / `delta_watch_seconds`。
 *
 * 注意：客户端只在 5 秒 tick 且 delta∈(0,30] 时上报（见 business-rules.md 第 4 节
 * watchdog 伪代码），seek/卡住/回零都不上报。后端还会再做一次 clamp 兜底。
 */
@Serializable
data class ReportProgressRequest(
    @SerialName("episode_id") val episodeId: Int,
    @SerialName("position_seconds") val positionSeconds: Int,
    @SerialName("delta_watch_seconds") val deltaWatchSeconds: Int,
)

/**
 * `/progress/ledger` 单条记录。对应后端 `model.PointsLedger` + Dart `PointsLedger`。
 *
 * 字段 snake_case(progress_handler.go GetPointsLedger 序列化)。
 * reasonType 见 model/identity.go 的 Reason* 常量(badge_unlocked / system_watch / 等)。
 */
@Serializable
data class PointsLedgerDto(
    @SerialName("id") val id: Int = 0,
    @SerialName("user_id") val userId: Int = 0,
    @SerialName("change_amount") val changeAmount: Int = 0,
    @SerialName("reason_type") val reasonType: String = "",
    @SerialName("description") val description: String = "",
    @SerialName("created_at") val createdAt: String = "",
)
