package com.revin.studyquest.tv.ui.footprint

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.revin.studyquest.tv.data.local.TokenStore
import com.revin.studyquest.tv.data.remote.ApiService
import com.revin.studyquest.tv.data.remote.dto.UserPointDto
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * 成长足迹屏状态机。
 *
 * 对照 PAD `frontend/lib/ui/screen/growth_footprint_screen.dart` 的 FutureBuilder:
 *   1. `ApiService.fetchUserPoints()` → UserPointDto(currentPoints/totalEarnedPoints)
 *   2. PAD 还并发拉 fetchProgressOverview / fetchPointsLedger / fetchUserBadges —— 但
 *      当前 TV 端 ApiService 只暴露了 fetchUserPoints 一个方法(见 ApiService.kt 第 4 节),
 *      其余三个端点(fetchProgressOverview / fetchPointsLedger / fetchUserBadges)未实现。
 *      网络层方法补齐是网络层 agent 的职责,这里**不自己往 ApiService 加方法**,
 *      只做积分卡部分,缺失部分(时长/通关/时间线/荣誉墙)留 TODO 占位。
 *
 * 等级计算(对照 PAD main_navigation.dart 的 `_levelForPoints`):
 *   level = currentPoints / 100 + 1（从 Lv.1 开始）。
 */
@HiltViewModel
class FootprintViewModel @Inject constructor(
    private val apiService: ApiService,
    private val tokenStore: TokenStore,
) : ViewModel() {

    private val _uiState = MutableStateFlow<FootprintUiState>(FootprintUiState.Loading)
    val uiState: StateFlow<FootprintUiState> = _uiState.asStateFlow()

    init {
        load()
    }

    /** 拉积分 + 当前用户昵称。失败 → Error 态带重试。 */
    fun load() {
        _uiState.value = FootprintUiState.Loading
        viewModelScope.launch {
            try {
                // 并发拉积分 + 当前用户(对照 PAD 的 Future.wait)。
                // 只拉 ApiService 已暴露的方法;progressOverview/ledger/badges 缺失,留 TODO。
                val points = apiService.fetchUserPoints()
                val nickname = tokenStore.getCurrentUser()?.nickname
                _uiState.value = FootprintUiState.Loaded(
                    points = points,
                    nickname = nickname,
                )
            } catch (e: Exception) {
                _uiState.value = FootprintUiState.Error(e.message ?: "获取足迹数据失败")
            }
        }
    }

    companion object {
        /**
         * 等级计算 —— 对照 PAD `main_navigation.dart` 的 `_levelForPoints`:
         *   int _levelForPoints() => (_userPoint?.currentPoints ?? 0) ~/ 100 + 1;
         * 每 100 分 = +1 等级,从 Lv.1 开始。
         */
        fun levelForPoints(currentPoints: Int): Int = currentPoints / 100 + 1
    }
}

/** 成长足迹屏顶层状态。 */
sealed interface FootprintUiState {
    /** 加载中。 */
    data object Loading : FootprintUiState
    /** 加载成功(目前只有积分 + 昵称;时长/通关/时间线/徽章待 ApiService 补端点)。 */
    data class Loaded(
        val points: UserPointDto,
        val nickname: String?,
    ) : FootprintUiState
    /** 加载失败(连不上服务器 / baseUrl 没配 / 未登录)。 */
    data class Error(val message: String) : FootprintUiState
}
