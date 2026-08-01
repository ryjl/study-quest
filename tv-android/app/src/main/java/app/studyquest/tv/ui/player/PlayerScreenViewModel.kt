package app.studyquest.tv.ui.player

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.studyquest.tv.data.local.TokenStore
import app.studyquest.tv.data.remote.ApiService
import app.studyquest.tv.data.remote.dto.PlayInfoDto
import app.studyquest.tv.domain.DEFAULT_SUBTITLE_SIZE_INDEX
import app.studyquest.tv.domain.clampSubtitleSizeIndex
import app.studyquest.tv.player.NetdiskHttpFactory
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * 播放器屏 ViewModel。
 *
 * 职责:
 *   1. 拉 play-info(GET /episodes/:id/play-info)—— 拿 url/headers/progress/subtitles
 *   2. 暴露 [netdiskHttpFactory] 给 Screen 建播放器时注入网盘头
 *   3. 暴露 [apiService] 给 ProgressReporter 上报进度
 *   4. 暴露 [baseUrl] StateFlow —— 拼 backend 字幕相对 URL(`/api/v1/subtitles/x.vtt`)
 *      成绝对 URL 用(对照 [app.studyquest.tv.data.repo.UrlResolver.absolute])。
 *
 * 对照 PAD `_initializeVideo`(player_screen.dart 行 238):fetchPlayInfo →
 * 建 Player(Media(url, httpHeaders: headers)) → 断点续播 seek。
 */
@HiltViewModel
class PlayerScreenViewModel @Inject constructor(
    val apiService: ApiService,
    val netdiskHttpFactory: NetdiskHttpFactory,
    private val tokenStore: TokenStore,
) : ViewModel() {

    private val _uiState = MutableStateFlow<PlayerUiState>(PlayerUiState.Loading)
    val uiState: StateFlow<PlayerUiState> = _uiState.asStateFlow()

    /**
     * 后端 baseUrl(从 TokenStore 读)。
     *
     * 给 Screen 拼 backend 字幕绝对 URL 用 —— play-info 返回的 `subtitles[].url` 是
     * 相对路径(`/api/v1/subtitles/x.vtt`),ExoPlayer 加载 side-loaded VTT 必须绝对 URL。
     * 作为 StateFlow 暴露,UI collectAsState 后参与重组(对照 CourseHallViewModel 的
     * baseUrl 模式)。
     */
    private val _baseUrl = MutableStateFlow<String?>(null)
    val baseUrl: StateFlow<String?> = _baseUrl.asStateFlow()

    /**
     * 字幕字号档位 index(对照 business-rules.md 第 6 节,4 档 18/24/30/38dp)。
     *
     * 从 TokenStore 读,作为 StateFlow 暴露 —— 播放器"字幕大小"菜单改档位时,UI
     * collectAsState 触发重组,SubtitleView 跟着重设字号。档位表统一从 domain 层
     * 取(`domain/SubtitleSize.kt`),避免和 SettingsViewModel 散落两份。
     */
    private val _subtitleSizeIndex = MutableStateFlow(DEFAULT_SUBTITLE_SIZE_INDEX)
    val subtitleSizeIndex: StateFlow<Int> = _subtitleSizeIndex.asStateFlow()

    init {
        viewModelScope.launch {
            _baseUrl.value = tokenStore.getBaseUrl()
            _subtitleSizeIndex.value = clampSubtitleSizeIndex(tokenStore.getSubtitleSizeIndex())
        }
    }

    /**
     * 设置字幕字号档位:更新 StateFlow + 落盘(对照 SettingsViewModel.selectSubtitleSize)。
     * 播放器菜单和设置页改档位最终都走 TokenStore,语义统一。
     */
    fun setSubtitleSizeIndex(index: Int) {
        val clamped = clampSubtitleSizeIndex(index)
        _subtitleSizeIndex.value = clamped
        viewModelScope.launch { tokenStore.saveSubtitleSizeIndex(clamped) }
    }

    fun loadPlayInfo(episodeId: Int) {
        _uiState.value = PlayerUiState.Loading
        viewModelScope.launch {
            try {
                val playInfo = apiService.fetchPlayInfo(episodeId)
                _uiState.value = PlayerUiState.Ready(playInfo)
            } catch (e: Exception) {
                _uiState.value = PlayerUiState.Error(e.message ?: "加载播放信息失败")
            }
        }
    }
}

sealed interface PlayerUiState {
    data object Loading : PlayerUiState
    data class Ready(val playInfo: PlayInfoDto) : PlayerUiState
    data class Error(val message: String) : PlayerUiState
}
