package app.studyquest.tv.data.remote.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * `/episodes/:id/ai-summary` 响应。对应 Dart: `EpisodeSummary`；后端
 * `summaryResponse`（ai_handler.go 行 84）字段全 snake_case。
 *
 * 注意 404 时后端不带 body（或 `{"error":"..."}`），客户端按 404 → 无总结处理，
 * 不走反序列化路径（见 ApiService 注释）。
 */
@Serializable
data class EpisodeSummaryDto(
    @SerialName("headline") val headline: String = "",
    @SerialName("sections") val sections: List<SummarySectionDto> = emptyList(),
    @SerialName("key_points") val keyPoints: List<String> = emptyList(),
    @SerialName("methods") val methods: List<String> = emptyList(),
    @SerialName("common_mistakes") val commonMistakes: List<String> = emptyList(),
    @SerialName("concepts") val concepts: List<String> = emptyList(),
    @SerialName("takeaway") val takeaway: String = "",
    @SerialName("pre_adventure") val preAdventure: List<PreAdventurePromptDto> = emptyList(),
) {
    val isEmpty: Boolean
        get() = headline.isEmpty() && keyPoints.isEmpty() && concepts.isEmpty() &&
            sections.isEmpty() && methods.isEmpty() && commonMistakes.isEmpty() &&
            takeaway.isEmpty()
}

/** 知识点小节（Phase F）。对应 Dart: `SummarySection`。 */
@Serializable
data class SummarySectionDto(
    @SerialName("title") val title: String = "",
    @SerialName("points") val points: List<String> = emptyList(),
)

/** 课前探险问题。对应 Dart: `PreAdventurePrompt`。 */
@Serializable
data class PreAdventurePromptDto(
    @SerialName("prompt") val prompt: String = "",
    @SerialName("hint") val hint: String = "",
)

/**
 * `/episodes/:id/ai-advice` 响应。对应 Dart: `AdviceResponse`；后端
 * `adviceResponse`（ai_handler.go 行 445）。
 *
 * status：`ready`（advice 已生成）/ `generating`（已入队，轮询）/
 * `cooling`（连续失败熔断，客户端提示「卡住、可重试」）/ `unavailable`（AI 未配置
 * 或该 scope 不支持，客户端隐藏）。advice 在非 ready 时省略（omitempty）。
 */
@Serializable
data class AdviceResponseDto(
    @SerialName("status") val status: String = "unavailable",
    @SerialName("scope") val scope: String = "",
    @SerialName("id") val id: Int = 0,
    @SerialName("advice") val advice: StudyAdviceDto? = null,
) {
    val isReady: Boolean get() = status == "ready"
    val isGenerating: Boolean get() = status == "generating"
    val isCooling: Boolean get() = status == "cooling"
    val isUnavailable: Boolean get() = status == "unavailable"
}

/**
 * 学习建议正文。对应后端 `model.StudyAdvice`（model/ai.go 行 417）。
 * `adviceText` 是 agent 生成的自然语言建议；TV 端只读不触发，渲染即可。
 */
@Serializable
data class StudyAdviceDto(
    @SerialName("scope") val scope: String = "",
    @SerialName("scope_id") val scopeId: Int = 0,
    @SerialName("advice_text") val adviceText: String = "",
    @SerialName("model_used") val modelUsed: String? = null,
    @SerialName("generated_at") val generatedAt: String? = null,
)
