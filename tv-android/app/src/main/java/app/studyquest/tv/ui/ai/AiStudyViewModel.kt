package app.studyquest.tv.ui.ai

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.studyquest.tv.data.remote.ApiService
import app.studyquest.tv.data.remote.dto.AdviceResponseDto
import app.studyquest.tv.data.remote.dto.EpisodeSummaryDto
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import retrofit2.HttpException
import javax.inject.Inject

/**
 * AI 学习页 ViewModel —— TV 端只读(看 summary + advice,无 quiz/做题)。
 *
 * 对照 PAD `frontend/lib/ui/screen/ai_study_screen.dart` 的数据加载部分,
 * 但砍掉 quiz/history/输入(TV 只读,见 app_features 设计)。
 *
 * 数据:
 *   1. summary:GET /episodes/:id/ai-summary(404→null,无总结/AI 未开)
 *   2. advice:GET /episodes/:id/ai-advice(agent 驱动 lazy 生成)
 *      - ready:直接展示
 *      - generating:轮询(首次返 generating,每 [ADVICE_POLL_INTERVAL_MS] 拉一次直到 ready)
 *      - cooling:连续失败熔断,提示重试
 *      - unavailable:AI 未配置,隐藏建议区
 */
@HiltViewModel
class AiStudyViewModel @Inject constructor(
    private val apiService: ApiService,
) : ViewModel() {

    private val _uiState = MutableStateFlow<AiUiState>(AiUiState.Loading)
    val uiState: StateFlow<AiUiState> = _uiState.asStateFlow()

    fun load(episodeId: Int) {
        _uiState.value = AiUiState.Loading
        viewModelScope.launch {
            // summary + advice 并发拉(advice 后面单独轮询)
            val summary = fetchSummarySafely(episodeId)
            _uiState.value = AiUiState.SummaryLoaded(summary)

            // advice 可能 generating,启动轮询
            pollAdvice(episodeId)
        }
    }

    /** 拉 summary,404 → null(无总结)。其它错误也 → null(降级,不阻塞)。 */
    private suspend fun fetchSummarySafely(episodeId: Int): EpisodeSummaryDto? {
        return runCatching { apiService.fetchEpisodeSummary(episodeId) }
            .recoverCatching { e ->
                if (e is HttpException && e.code() == 404) null else throw e
            }
            .getOrNull()
    }

    /** 轮询 advice 直到 ready/unavailable/cooling,或超过最大轮询次数。 */
    private suspend fun pollAdvice(episodeId: Int) {
        var attempts = 0
        while (attempts < MAX_ADVICE_POLLS) {
            val advice = runCatching { apiService.fetchEpisodeAdvice(episodeId) }.getOrNull()
            if (advice == null) {
                // 网络错误/404 → unavailable
                updateAdvice(null)
                return
            }
            when {
                advice.isReady || advice.isCooling || advice.isUnavailable -> {
                    updateAdvice(advice)
                    return
                }
                advice.isGenerating -> {
                    updateAdvice(advice) // 显示"生成中"
                    delay(ADVICE_POLL_INTERVAL_MS)
                    attempts++
                }
                else -> {
                    updateAdvice(advice)
                    return
                }
            }
        }
        // 超过最大轮询,显示当前态(可能仍 generating → 提示超时)
    }

    private fun updateAdvice(advice: AdviceResponseDto?) {
        val current = _uiState.value
        if (current is AiUiState.SummaryLoaded) {
            _uiState.value = current.copy(advice = advice)
        }
    }

    companion object {
        private const val ADVICE_POLL_INTERVAL_MS = 3000L
        private const val MAX_ADVICE_POLLS = 20 // 最多轮询 60 秒
    }
}

sealed interface AiUiState {
    data object Loading : AiUiState
    /** summary 已加载(可能 null),advice 异步轮询中。 */
    data class SummaryLoaded(
        val summary: EpisodeSummaryDto?,
        val advice: AdviceResponseDto? = null,
    ) : AiUiState
}
