package app.studyquest.tv.ui.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.studyquest.tv.data.local.TokenStore
import app.studyquest.tv.data.remote.ApiService
import app.studyquest.tv.data.remote.dto.LoginRequestDto
import app.studyquest.tv.data.remote.dto.UserDto
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * 登录屏状态机。
 *
 * 对照 PAD 端 `frontend/lib/ui/screen/login_screen.dart` 的 `_LoginScreenState`:
 *   1. 启动拉用户列表(GET /users),展示选人页
 *   2. 选中用户 → 弹 PIN 键盘蒙层
 *   3. 提交 PIN → POST /users/login → 成功存 token+user → 跳 MainNav
 *
 * TV 端差异:PIN 用遥控器数字键盘(D-pad 聚焦 0-9 + 确认 + 删除),不是触屏 NumPad。
 */
@HiltViewModel
class LoginViewModel @Inject constructor(
    private val apiService: ApiService,
    private val tokenStore: TokenStore,
) : ViewModel() {

    private val _uiState = MutableStateFlow<LoginUiState>(LoginUiState.Loading)
    val uiState: StateFlow<LoginUiState> = _uiState.asStateFlow()

    /** 当前选中、待验证 PIN 的用户(进入 PIN 蒙层)。null = 还在选人页。 */
    private val _pendingUser = MutableStateFlow<UserDto?>(null)
    val pendingUser: StateFlow<UserDto?> = _pendingUser.asStateFlow()

    /** PIN 输入值(蒙层内)。4 位数字。 */
    private val _pinInput = MutableStateFlow("")
    val pinInput: StateFlow<String> = _pinInput.asStateFlow()

    /** PIN 验证错误提示。 */
    private val _pinError = MutableStateFlow<String?>(null)
    val pinError: StateFlow<String?> = _pinError.asStateFlow()

    /** 当前 baseUrl(给配置 dialog 显示当前值)。 */
    private val _baseUrl = MutableStateFlow("")
    val baseUrl: StateFlow<String> = _baseUrl.asStateFlow()

    init {
        loadBaseUrl()
        loadUsers()
    }

    private fun loadBaseUrl() {
        viewModelScope.launch {
            _baseUrl.value = tokenStore.getBaseUrl() ?: ""
        }
    }

    /** 保存 baseUrl(配置 dialog 用)。保存后重拉用户列表。 */
    fun saveBaseUrl(url: String) {
        viewModelScope.launch {
            tokenStore.saveBaseUrl(url)
            _baseUrl.value = url
            loadUsers()
        }
    }

    fun loadUsers() {
        _uiState.value = LoginUiState.Loading
        viewModelScope.launch {
            try {
                val users = apiService.fetchUsers()
                _uiState.value = if (users.isEmpty()) {
                    LoginUiState.Empty
                } else {
                    LoginUiState.UsersLoaded(users)
                }
            } catch (e: Exception) {
                _uiState.value = LoginUiState.Error(e.message ?: "无法连接服务器")
            }
        }
    }

    /** 选人 → 进入 PIN 蒙层。对照 PAD `_onSelectUser`。 */
    fun selectUser(user: UserDto) {
        _pendingUser.value = user
        _pinInput.value = ""
        _pinError.value = null
    }

    /** 取消 PIN 输入 → 回到选人页。对照 PAD `_onCancelPin`。 */
    fun cancelPin() {
        _pendingUser.value = null
        _pinInput.value = ""
        _pinError.value = null
    }

    /** PIN 键盘按键:数字 0-9 追加(最多 4 位)、退格、提交。 */
    fun onPinDigit(digit: Char) {
        if (_pinInput.value.length < PIN_MAX_LENGTH) {
            _pinInput.value = _pinInput.value + digit
            _pinError.value = null
        }
    }

    fun onPinDelete() {
        if (_pinInput.value.isNotEmpty()) {
            _pinInput.value = _pinInput.value.dropLast(1)
            _pinError.value = null
        }
    }

    /** 提交 PIN 验证。对照 PAD `_onSubmitPin`。成功回调 [onSuccess]。 */
    fun submitPin(onSuccess: () -> Unit) {
        val user = _pendingUser.value ?: return
        val pin = _pinInput.value
        if (pin.length != PIN_MAX_LENGTH) return

        viewModelScope.launch {
            try {
                val resp = apiService.login(
                    LoginRequestDto(
                        userId = user.id,
                        pin = pin,
                        deviceName = "Android TV", // TODO: Build.MODEL
                    )
                )
                // 持久化 token + user(对照 PAD auth_service.login 存 prefs)
                tokenStore.saveToken(resp.token)
                tokenStore.saveCurrentUser(
                    app.studyquest.tv.data.local.StoredUser(
                        id = user.id,
                        nickname = user.nickname,
                        avatarUrl = user.avatarUrl,
                        role = user.role,
                    )
                )
                onSuccess()
            } catch (e: Exception) {
                _pinError.value = "PIN 码错误,请重试"
            }
        }
    }

    companion object {
        const val PIN_MAX_LENGTH = 4
    }
}

/** 登录屏顶层状态(选人页部分)。 */
sealed interface LoginUiState {
    /** 加载用户列表中。 */
    data object Loading : LoginUiState
    /** 用户列表加载成功。 */
    data class UsersLoaded(val users: List<UserDto>) : LoginUiState
    /** 用户列表为空(后端没建用户)。 */
    data object Empty : LoginUiState
    /** 加载失败(连不上服务器 / baseUrl 没配)。 */
    data class Error(val message: String) : LoginUiState
}
