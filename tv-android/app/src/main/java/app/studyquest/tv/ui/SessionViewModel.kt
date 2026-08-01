package app.studyquest.tv.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.studyquest.tv.data.local.TokenStore
import app.studyquest.tv.data.remote.ApiService
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * 会话状态 ViewModel —— 管理登录态,供导航层决定起始页 + 登出。
 *
 * 对照 PAD `AuthService`(ChangeNotifier):启动读回 token/user,判断 isAuthenticated。
 * TV 端用 StateFlow 暴露 [authState],AppNav 读它决定 startDestination。
 *
 * 启动时(uni)异步读 TokenStore;未登录 → LOGIN,已登录 → HOME。
 */
@HiltViewModel
class SessionViewModel @Inject constructor(
    private val tokenStore: TokenStore,
    private val apiService: ApiService,
) : ViewModel() {

    private val _authState = MutableStateFlow<AuthState>(AuthState.Loading)
    val authState: StateFlow<AuthState> = _authState.asStateFlow()

    init {
        checkAuth()
    }

    /** 读 TokenStore 判断登录态。登录后调(标记已登录)、登出后调(刷新)。 */
    fun checkAuth() {
        viewModelScope.launch {
            val token = tokenStore.getToken()
            val user = tokenStore.getCurrentUser()
            _authState.value = if (token != null && user != null) {
                AuthState.Authenticated
            } else {
                AuthState.Unauthenticated
            }
        }
    }

    /** 登出:调后端 logout(容错)+ 清本地 + 刷新状态。对照 PAD auth_service.logout。 */
    fun logout() {
        viewModelScope.launch {
            runCatching { apiService.logout() }
            tokenStore.clearAuth()
            _authState.value = AuthState.Unauthenticated
        }
    }
}

sealed interface AuthState {
    /** 启动读 TokenStore 中(短暂)。 */
    data object Loading : AuthState
    /** 已登录,进 HOME。 */
    data object Authenticated : AuthState
    /** 未登录,进 LOGIN。 */
    data object Unauthenticated : AuthState
}
