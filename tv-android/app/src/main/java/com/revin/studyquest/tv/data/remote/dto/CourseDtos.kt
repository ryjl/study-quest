package com.revin.studyquest.tv.data.remote.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * 课程大厅卡片 DTO。
 *
 * 字段大小写对齐后端 `clientCourseDTO`（`/api/v1/courses`）：课程/课时/章节主对象
 * 走 PascalCase（`ID` / `Title` / `CoverURL` / `TagsList` ...），与 Flutter 端
 * `Course.fromJson` 的双键读取保持一致。`@SerialName` 显式标注后端实际下发的
 * PascalCase 字段；老后端偶发的 snake_case 由 `Json.ignoreUnknownKeys` 兜底（
 * 无法两者都显式声明，所以这里以契约实际下发的 PascalCase 为准）。
 *
 * 对应 Dart: frontend/lib/model/course.dart `Course`。
 */
@Serializable
data class CourseDto(
    @SerialName("ID") val id: Int = 0,
    @SerialName("Title") val title: String = "",
    @SerialName("Grade") val grade: String = "universal",
    @SerialName("Subject") val subject: String = "",
    @SerialName("ContentType") val contentType: String = "",
    @SerialName("CoverURL") val coverUrl: String = "",
    @SerialName("TagsList") val tagsList: List<String> = emptyList(),
    @SerialName("TagIDs") val tagIds: List<Int> = emptyList(),
    // Drip 解锁摘要（仅学生角色下发；零值 = 无 drip 节奏 → 卡片隐藏徽章）。
    @SerialName("UnlockStrategy") val unlockStrategy: String = "",
    @SerialName("UnlockStrategyLabel") val unlockStrategyLabel: String = "",
    @SerialName("UnlockedCount") val unlockedCount: Int = 0,
    @SerialName("EpisodeTotal") val episodeTotal: Int = 0,
    @SerialName("NextUnlockAt") val nextUnlockAt: String = "",
) {
    /** 是否启用 drip 解锁节奏（vs 全开放）。驱动课程卡徽章可见性。 */
    val hasUnlockSchedule: Boolean
        get() = unlockStrategy.isNotEmpty() && unlockStrategy != "all_open"
}

/**
 * 单个章节（模块）。对应 Dart: `Chapter`；后端 PascalCase。
 */
@Serializable
data class ChapterDto(
    @SerialName("ID") val id: Int = 0,
    @SerialName("CourseID") val courseId: Int = 0,
    @SerialName("Title") val title: String = "",
    @SerialName("Description") val description: String = "",
    @SerialName("CoverURL") val coverUrl: String = "",
    @SerialName("SortOrder") val sortOrder: Int = 0,
)

/**
 * 单个课时。对应 Dart: `Episode`；后端 `clientEpisodeDTO`。
 *
 * 注意 `locked` 是契约里唯一的小写字段（后端 client_dto.go 注释明确说明：
 * 「The only client-facing field NOT mirrored from the model」），其余字段
 * 全是 PascalCase。三个 AI/字幕回显字段（AISummaryEnabled/AIQuizEnabled/
 * HasSubtitle）为 add-only，默认 false 兼容老后端。
 */
@Serializable
data class EpisodeDto(
    @SerialName("ID") val id: Int = 0,
    @SerialName("CourseID") val courseId: Int = 0,
    @SerialName("ChapterID") val chapterId: Int = 0,
    @SerialName("SortOrder") val sortOrder: Int = 1,
    @SerialName("Title") val title: String = "",
    @SerialName("VideoRelativePath") val videoRelativePath: String = "",
    @SerialName("CoverURL") val coverUrl: String = "",
    @SerialName("AttachmentJSON") val attachmentJson: String = "[]",
    @SerialName("FileHash") val fileHash: String = "",
    @SerialName("FileSize") val fileSize: Long = 0,
    @SerialName("DurationSeconds") val durationSeconds: Int = 0,
    // 唯一小写字段（见类注释）。
    @SerialName("locked") val locked: Boolean = false,
    @SerialName("AISummaryEnabled") val aiSummaryEnabled: Boolean = false,
    @SerialName("AIQuizEnabled") val aiQuizEnabled: Boolean = false,
    @SerialName("HasSubtitle") val hasSubtitle: Boolean = false,
)

/**
 * play-info 里返回的字幕轨道（解析成 WebVTT URL）。
 *
 * 命名为 EpisodeSubtitleDto 避免和 ExoPlayer 自身的字幕类型冲突（对齐 Dart 端
 * `EpisodeSubtitle` 的命名理由，见 course.dart 行 209）。play-info 端点用
 * snake_case，对应 episode_handler.go 的 gin.H 字段。
 */
@Serializable
data class EpisodeSubtitleDto(
    @SerialName("id") val id: Int = 0,
    @SerialName("language") val language: String = "zh-CN",
    @SerialName("label") val label: String = "字幕",
    @SerialName("url") val url: String = "",
)

/**
 * play-info 内 `progress` 子对象。后端 episode_handler.go 用 gin.H 下发，全小写。
 * `isCompleted` 历史上同时可能是 bool / int 0/1，这里按 bool 反序列化（kotlinx
 * 能直接吃 bool；int 0/1 的情况由 Json isLenient + 后续宽容处理兜底，当前后端
 * 实际下发 bool）。
 */
@Serializable
data class PlayInfoProgressDto(
    @SerialName("last_position_seconds") val lastPositionSeconds: Int = 0,
    @SerialName("watch_seconds") val watchSeconds: Int = 0,
    @SerialName("is_completed") val isCompleted: Boolean = false,
)

/**
 * `/episodes/:id/play-info` 响应 —— **播放器核心契约**（business-rules.md
 * 多处依赖：断点续播读 progress.last_position_seconds、字幕菜单读 subtitles[]、
 * 网盘鉴权头读 headers）。
 *
 * 后端字段全小写（见 episode_handler.go GetPlayInfo 的 c.JSON）：
 *   - url：AList 网盘代理地址（302 重定向到云盘 CDN 直链）。
 *   - headers：网盘鉴权头（115 网盘的 Referer 等），给 ExoPlayer 用，**不是**
 *     Retrofit 的请求头。
 *   - progress：可空（未登录或无进度记录时后端下发 null）。
 *   - subtitles：可空；后端保证非 nil，但防御性标可空。
 *
 * 对应 Dart: `PlayInfo`。`url`/`headers` 默认空值兼容字段缺失。
 */
@Serializable
data class PlayInfoDto(
    @SerialName("url") val url: String = "",
    @SerialName("headers") val headers: Map<String, String> = emptyMap(),
    @SerialName("progress") val progress: PlayInfoProgressDto? = null,
    @SerialName("subtitles") val subtitles: List<EpisodeSubtitleDto> = emptyList(),
) {
    /** 断点续播位置（秒），无进度记录时为 null。 */
    val resumePositionSeconds: Int? get() = progress?.takeIf { it.lastPositionSeconds > 0 }?.lastPositionSeconds
    /** 是否已看完（progress 缺失视为未完成）。 */
    val isCompleted: Boolean get() = progress?.isCompleted == true
}
