package com.revin.studyquest.tv.data.remote.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * 用户对单集的观看进度。对应 Dart: `UserProgress`。
 *
 * 进度上报 `/progress/report` 响应、进度列表 `/progress` 都返回同一形状。
 * 后端字段 snake_case（progress_handler.go）。
 */
@Serializable
data class UserProgressDto(
    @SerialName("id") val id: Int = 0,
    @SerialName("user_id") val userId: Int = 0,
    @SerialName("episode_id") val episodeId: Int = 0,
    @SerialName("last_position_seconds") val lastPositionSeconds: Int = 0,
    @SerialName("watch_seconds") val watchSeconds: Int = 0,
    @SerialName("is_completed") val isCompleted: Boolean = false,
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
