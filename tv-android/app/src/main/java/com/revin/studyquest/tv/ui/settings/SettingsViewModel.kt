package com.revin.studyquest.tv.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.revin.studyquest.tv.data.local.TokenStore
import com.revin.studyquest.tv.data.remote.ApiService
import com.revin.studyquest.tv.domain.SUBTITLE_SIZE_LABELS
import com.revin.studyquest.tv.domain.clampSubtitleSizeIndex
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * 系统设置屏状态机。
 *
 * 对照 PAD `frontend/lib/ui/screen/settings_screen.dart` + `main_navigation.dart` 里
 * IP 配置 / 登出的逻辑(那里 [SettingsScreen] 只管渲染,真实副作用在父级;TV 端这里
 * 把副作用收敛进 ViewModel):
 *   - 服务器地址(baseUrl)配置:首次启动为空 → 在这里配。调 [TokenStore.saveBaseUrl]。
 *   - 字幕字号档位:4 档(小/中/大/超大,见 business-rules.md 第 6 节)。调
 *     [TokenStore.saveSubtitleSizeIndex]。
 *   - 登出:调 [ApiService.logout] + [TokenStore.clearAuth],完成后回调 [onLoggedOut]
 *     (导航层据此跳 login)。
 *   - 当前登录用户信息:从 [TokenStore.getCurrentUser] 读 nickname。
 *
 * 状态用单一 data class [SettingsUiState] + [MutableStateFlow];所有副作用走
 * viewModelScope.launch,UI 单向收集。
 */
@HiltViewModel
class SettingsViewModel @Inject constructor(
    private val apiService: ApiService,
    private val tokenStore: TokenStore,
) : ViewModel() {

    private val _uiState = MutableStateFlow(SettingsUiState())
    val uiState: StateFlow<SettingsUiState> = _uiState.asStateFlow()

    init {
        loadInitial()
    }

    /** 启动加载当前 baseUrl / 字幕档位 / 昵称(都是本地 prefs,不发网络)。 */
    private fun loadInitial() {
        viewModelScope.launch {
            val baseUrl = tokenStore.getBaseUrl().orEmpty()
            val subtitleIndex = tokenStore.getSubtitleSizeIndex()
            val nickname = tokenStore.getCurrentUser()?.nickname
            _uiState.update {
                it.copy(
                    baseUrlInput = baseUrl,
                    savedBaseUrl = baseUrl,
                    subtitleSizeIndex = clampSubtitleSizeIndex(subtitleIndex),
                    nickname = nickname,
                    initialized = true,
                )
            }
        }
    }

    /** 用户改 baseUrl 输入框(纯 UI 态,不落盘)。 */
    fun onBaseUrlChange(value: String) {
        _uiState.update { it.copy(baseUrlInput = value) }
    }

    /**
     * 保存 baseUrl:trim + 去尾斜杠(TokenStore 内部也做一次,双保险)。对照 PAD
     * `AppConfig.setBaseUrl` 的清理逻辑。保存后刷新 savedBaseUrl + 清错误。
     */
    fun saveBaseUrl() {
        val raw = _uiState.value.baseUrlInput.trim()
        if (raw.isEmpty()) {
            _uiState.update { it.copy(baseUrlError = "地址不能为空") }
            return
        }
        _uiState.update { it.copy(isSavingBaseUrl = true, baseUrlError = null) }
        viewModelScope.launch {
            try {
                tokenStore.saveBaseUrl(raw)
                val saved = tokenStore.getBaseUrl().orEmpty()
                _uiState.update {
                    it.copy(
                        isSavingBaseUrl = false,
                        savedBaseUrl = saved,
                        baseUrlInput = saved,
                        baseUrlSaved = true,
                    )
                }
            } catch (e: Exception) {
                _uiState.update {
                    it.copy(isSavingBaseUrl = false, baseUrlError = e.message ?: "保存失败")
                }
            }
        }
    }

    /** 用户吃掉了「已保存」提示。 */
    fun clearBaseUrlSavedFlag() {
        _uiState.update { it.copy(baseUrlSaved = false) }
    }

    /** 选字幕字号档位(index 0..3)。落盘 + 更新 UI。 */
    fun selectSubtitleSize(index: Int) {
        val clamped = clampSubtitleSizeIndex(index)
        _uiState.update { it.copy(subtitleSizeIndex = clamped) }
        viewModelScope.launch {
            tokenStore.saveSubtitleSizeIndex(clamped)
        }
    }

    /**
     * 登出:调后端 revoke(失败吞)+ 清本地登录态(对照 AuthRepo.logout / PAD
     * auth_service.logout)。完成后回调 [onLoggedOut](导航层跳 login)。
     */
    fun logout(onLoggedOut: () -> Unit) {
        if (_uiState.value.isLoggingOut) return
        _uiState.update { it.copy(isLoggingOut = true) }
        viewModelScope.launch {
            // 对照 Repositories.kt AuthRepo.logout:网络失败不阻塞本地清理。
            runCatching { apiService.logout() }
            tokenStore.clearAuth()
            _uiState.update { it.copy(isLoggingOut = false) }
            onLoggedOut()
        }
    }

    companion object {
        // 字幕字号档位表已收敛到 domain 层(`domain/SubtitleSize.kt`),这里只做转发
        // 兼容已有调用方(SettingsScreen),避免散落两份定义。新代码应直接引用 domain。
        val SUBTITLE_LABELS: List<String> get() = SUBTITLE_SIZE_LABELS
    }
}

/**
 * 设置屏 UI 状态(单一 data class,UI 单向收集)。
 *
 * @param baseUrlInput 输入框当前文本(用户正在编辑,未落盘)。
 * @param savedBaseUrl 已落盘的 baseUrl(展示「当前」用)。
 * @param subtitleSizeIndex 字幕字号档位 0..3。
 * @param nickname 当前登录用户昵称(未登录为 null)。
 * @param isSavingBaseUrl 保存中(按钮转菊花)。
 * @param isLoggingOut 登出中(防重入 + 按钮 disarmed)。
 * @param baseUrlError baseUrl 校验 / 保存错误提示(null = 无)。
 * @param baseUrlSaved 刚保存成功的 transient 提示(UI 展示后调 clearBaseUrlSavedFlag)。
 * @param initialized 初始 prefs 加载完成(避免输入框被空串闪一下覆盖)。
 */
data class SettingsUiState(
    val baseUrlInput: String = "",
    val savedBaseUrl: String = "",
    val subtitleSizeIndex: Int = 1,
    val nickname: String? = null,
    val isSavingBaseUrl: Boolean = false,
    val isLoggingOut: Boolean = false,
    val baseUrlError: String? = null,
    val baseUrlSaved: Boolean = false,
    val initialized: Boolean = false,
)
