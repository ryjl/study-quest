package com.revin.studyquest.tv.ui.player

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.revin.studyquest.tv.data.remote.ApiService
import com.revin.studyquest.tv.data.remote.dto.PlayInfoDto
import com.revin.studyquest.tv.player.NetdiskHttpFactory
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
 *
 * 对照 PAD `_initializeVideo`(player_screen.dart 行 238):fetchPlayInfo →
 * 建 Player(Media(url, httpHeaders: headers)) → 断点续播 seek。
 */
@HiltViewModel
class PlayerScreenViewModel @Inject constructor(
    val apiService: ApiService,
    val netdiskHttpFactory: NetdiskHttpFactory,
) : ViewModel() {

    private val _uiState = MutableStateFlow<PlayerUiState>(PlayerUiState.Loading)
    val uiState: StateFlow<PlayerUiState> = _uiState.asStateFlow()

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
