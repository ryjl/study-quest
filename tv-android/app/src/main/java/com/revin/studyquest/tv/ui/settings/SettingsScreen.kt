package com.revin.studyquest.tv.ui.settings

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.tv.material3.Surface
import androidx.tv.material3.SurfaceDefaults
import androidx.tv.material3.Text
import com.revin.studyquest.tv.ui.theme.BorderRadiusValue
import com.revin.studyquest.tv.ui.theme.NormalBorderWidthValue
import com.revin.studyquest.tv.ui.theme.accentOrange
import com.revin.studyquest.tv.ui.theme.blue100
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate400
import com.revin.studyquest.tv.ui.theme.slate500
import com.revin.studyquest.tv.ui.theme.slate700
import com.revin.studyquest.tv.ui.theme.slate800
import com.revin.studyquest.tv.ui.theme.slate900

/**
 * 系统设置屏 —— TV 端。
 *
 * 对照 PAD `frontend/lib/ui/screen/settings_screen.dart`(服务器地址 / 本地偏好) +
 * `main_navigation.dart` 里 IP 配置 / 登出逻辑:
 *   1. 服务器地址配置(API Endpoint):BasicTextField + 保存按钮 →
 *      [com.revin.studyquest.tv.data.local.TokenStore.saveBaseUrl]。首次启动 baseUrl
 *      为空时在这里配置(对照 PAD AppConfig)。
 *   2. 字幕字号档位:4 档(小/中/大/超大,见 business-rules.md 第 6 节)。segmented
 *      单选列表 → TokenStore.saveSubtitleSizeIndex(index)。
 *   3. 当前登录用户信息(从 TokenStore.getCurrentUser() 读 nickname)。
 *   4. 登出:ApiService.logout() + TokenStore.clearAuth() → 回调 [onLogout](导航
 *      层跳 login)。
 *
 * TV 适配:
 *   - 深色底 slate900。
 *   - LazyColumn + focusable 子项做 D-pad 可滚动。
 *   - 焦点发光环对照 design-tokens.md 焦点视觉 TV 版。
 *
 * **D-pad 焦点陷阱**:BasicTextField(EditableText)默认吞方向键做光标移动,D-pad
 * 进了输入框出不来(跳不到「保存修改」按钮)。PAD 端用 `dpadEscapeFocusNode` 解决。
 * Compose for TV 暂无等价 helper,这里用 [BasicTextField] + [KeyboardOptions] +
 * `Modifier.onFocusChanged`,并把方向键逃逸留 TODO(见 [ServerUrlCard] 注释)。
 * 当前可用的解法:遥控器「确认」键进入软键盘编辑态,「Back」退出;方向键在非编辑态
 * 仍可能被吞,后续接入 tv-foundation 的 focus requester + key event 拦截补全。
 *
 * @param onLogout 登出完成后导航层回调(跳 login)。默认空 lambda,导航层
 *   ([com.revin.studyquest.tv.ui.nav.AppNav])应自行接入:登出后 navController
 *   navigate(LOGIN) { popUpTo(HOME) { inclusive = true } }。默认值保证 nav 占位
 *   调用点 `SettingsScreen()` 仍可编译。
 */
@Composable
fun SettingsScreen(
    onLogout: () -> Unit = {},
    viewModel: SettingsViewModel = hiltViewModel(),
) {
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(slate900),
    ) {
        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 48.dp, vertical = 32.dp),
            verticalArrangement = Arrangement.spacedBy(24.dp),
        ) {
            item {
                // Header
                Column {
                    Text(
                        text = "系统设置",
                        color = Color.White,
                        fontSize = 36.sp,
                        fontWeight = FontWeight.Bold,
                    )
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = "配置你的专属学习环境",
                        color = slate400,
                        fontSize = 16.sp,
                        fontWeight = FontWeight.Bold,
                    )
                    // 当前登录用户(对照需求:从 TokenStore 读 nickname)。
                    uiState.nickname?.let { name ->
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text = "当前账号：$name",
                            color = slate400,
                            fontSize = 14.sp,
                        )
                    }
                }
            }

            item {
                ServerUrlCard(
                    state = uiState,
                    onBaseUrlChange = viewModel::onBaseUrlChange,
                    onSave = viewModel::saveBaseUrl,
                    onClearSaved = viewModel::clearBaseUrlSavedFlag,
                )
            }

            item {
                SubtitleSizeCard(
                    state = uiState,
                    onSelect = viewModel::selectSubtitleSize,
                )
            }

            item {
                LogoutCard(
                    state = uiState,
                    onLogout = { viewModel.logout(onLogout) },
                )
            }
        }
    }
}

// ── 服务器地址卡(对照 PAD 高级配置卡) ────────────────────────────────────────

@Composable
private fun ServerUrlCard(
    state: SettingsUiState,
    onBaseUrlChange: (String) -> Unit,
    onSave: () -> Unit,
    onClearSaved: () -> Unit,
) {
    SettingsCard(title = "高级配置（家长专区）", subtitle = "后端 API 地址") {
        // 「保存成功」transient 提示消费。
        LaunchedEffect(state.baseUrlSaved) {
            if (state.baseUrlSaved) {
                kotlinx.coroutines.delay(1500)
                onClearSaved()
            }
        }

        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(16.dp),
            modifier = Modifier.fillMaxWidth(),
        ) {
            // 输入框:BasicTextField(注意 TV D-pad 焦点陷阱,见下方注释)。
            Box(modifier = Modifier.weight(1f)) {
                ServerUrlField(
                    value = state.baseUrlInput,
                    onValueChange = onBaseUrlChange,
                    error = state.baseUrlError,
                )
            }
            SaveButton(
                isSaving = state.isSavingBaseUrl,
                enabled = state.baseUrlInput.isNotBlank() && !state.isSavingBaseUrl,
                onClick = onSave,
            )
        }

        Spacer(Modifier.height(10.dp))
        Text(
            text = "离开局域网时，请输入内网穿透或虚拟局域网（如 ZeroTier）的 API 地址。",
            color = slate400,
            fontSize = 12.sp,
            fontWeight = FontWeight.Bold,
        )

        if (state.savedBaseUrl.isNotEmpty()) {
            Spacer(Modifier.height(6.dp))
            Text(
                text = "当前：${state.savedBaseUrl}",
                color = slate500,
                fontSize = 12.sp,
                fontFamily = FontFamily.Monospace,
            )
        }
        if (state.baseUrlSaved) {
            Spacer(Modifier.height(6.dp))
            Text(
                text = "已保存",
                color = accentOrange,
                fontSize = 13.sp,
                fontWeight = FontWeight.Bold,
            )
        }
    }
}

/**
 * baseUrl 输入框。
 *
 * **D-pad 焦点陷阱的三轮修复**(踩坑记录,避免再走弯路):
 *
 * **v1**(失败):`onPreviewKeyEvent` 拦所有方向键 → moveFocus。问题:D-pad **进入**
 * EditText 的那个方向键也走 KeyUp,结果刚聚焦进去就被 moveFocus 弹走,用户根本
 * 进不去(实测反馈"光标移进去就被弹出")。
 *
 * **v2**(失败):干脆全放行方向键,只拦 Back。问题:BasicTextField 聚焦后自己吞
 * 方向键做光标移动,焦点进去就**出不来**(实测反馈"edit 框进去出不来")。
 *
 * **v3(本版)**:用 `justEntered` 标志状态机区分"进入瞬间"和"已在内"。
 * 关键洞察:D-pad 从外部移进 EditText 时,那个导致进入的方向键的 KeyUp 事件
 * 也会经过 onPreviewKeyEvent(此时焦点刚落到 EditText)。如果不特殊处理,这一帧
 * 就会触发 moveFocus 把焦点弹走(v1 的 bug)。
 *
 * 状态机:
 *   - `focused == false`:不在编辑态,所有方向键放行(让焦点系统正常移入)。
 *   - `justEntered == true`(进入后第一次收到方向键 KeyUp):消化掉这个事件
 *     (return true 拦截),不 moveFocus,然后清 justEntered 标志。这样进入用的
 *     那个方向键不会自弹。
 *   - `justEntered == false`(用户之后**主动**按的方向键):
 *     - Up/Down → moveFocus 逃逸(满足"上下能出来")。
 *     - Left/Right → 放行给 BasicTextField 做光标移动(URL 输入要左右定位光标)。
 *   - Back/ESC → clearFocus 收键盘退出。
 *
 * 这样:进入不自弹(消化进入事件)+ 进去后上下能逃逸(用户主动按时)+ 左右可移
 * 光标(编辑需要)+ Back 兜底退出。所有路径都打通。
 */
@Composable
private fun ServerUrlField(
    value: String,
    onValueChange: (String) -> Unit,
    error: String?,
) {
    var focused by remember { mutableStateOf(false) }
    // 刚进入标志:防止"进入用的方向键"自弹焦点。进入瞬间为 true,消化第一个方向
    // KeyUp 后置 false。焦点离开 EditText 时也要重置(下次进入重新生效)。
    var justEntered by remember { mutableStateOf(false) }
    val focusManager = LocalFocusManager.current
    val borderColor = if (error != null) Color.Red.copy(alpha = 0.7f)
        else if (focused) primaryColor
        else slate700
    val borderWidth = if (focused || error != null) NormalBorderWidthValue else 1.dp

    BasicTextField(
        value = value,
        onValueChange = onValueChange,
        singleLine = true,
        // TV 软键盘按 URI 输入(http://192.168.x.x:8080)。IME action = Done,
        // 软键盘上按 Done 收键盘退出编辑态(等同 Back)。
        keyboardOptions = KeyboardOptions(
            keyboardType = KeyboardType.Uri,
            imeAction = ImeAction.Done,
        ),
        keyboardActions = KeyboardActions(
            onDone = { focusManager.clearFocus() },
        ),
        textStyle = TextStyle(
            color = Color.White,
            fontSize = 16.sp,
            fontFamily = FontFamily.Monospace,
            fontWeight = FontWeight.Bold,
        ),
        cursorBrush = androidx.compose.ui.graphics.SolidColor(primaryColor),
        modifier = Modifier
            .fillMaxWidth()
            .onPreviewKeyEvent { event ->
                // 只在 KeyUp 处理(Down/Up 双触发会跳两格)。
                if (event.type != KeyEventType.KeyUp) return@onPreviewKeyEvent false
                when (event.key) {
                    Key.Back, Key.Escape -> {
                        focusManager.clearFocus()
                        true
                    }
                    // 进入瞬间(消化进入用的那个方向键,防自弹)+ 用户主动按的
                    // Up/Down(逃逸)。两者都在这里处理。
                    Key.DirectionUp, Key.DirectionDown -> {
                        if (justEntered) {
                            // 进入后的第一个方向键:消化掉,不 moveFocus(否则自弹)。
                            // 之后用户主动按的 Up/Down 才逃逸。
                            justEntered = false
                            true
                        } else {
                            // 用户主动按 Up/Down → 逃逸到上下邻居(满足"上下能出来")。
                            val dir = if (event.key == Key.DirectionUp) FocusDirection.Up else FocusDirection.Down
                            focusManager.moveFocus(dir)
                            true
                        }
                    }
                    // Left/Right 放行给 BasicTextField 做光标移动(URL 输入要左右定位)。
                    // 进入瞬间按 Left/Right 也算消化 justEntered。
                    Key.DirectionLeft, Key.DirectionRight -> {
                        justEntered = false
                        false
                    }
                    else -> false
                }
            }
            .onFocusChanged { state ->
                focused = state.isFocused
                // 焦点进入 EditText → 设 justEntered,等消化进入用的方向键。
                // 焦点离开 → 清 justEntered(下次进入重新生效)。
                justEntered = state.isFocused
            }
            .background(slate800, RoundedCornerShape(12.dp))
            .border(borderWidth, borderColor, RoundedCornerShape(12.dp))
            .padding(horizontal = 18.dp, vertical = 16.dp),
        decorationBox = { innerTextField ->
            Box {
                if (value.isEmpty()) {
                    Text(
                        text = "http://192.168.x.x:8080",
                        color = slate400,
                        fontSize = 16.sp,
                        fontFamily = FontFamily.Monospace,
                    )
                }
                innerTextField()
            }
        },
    )
}

/** 保存按钮:聚焦发光环 + 菊花态。 */
@Composable
private fun SaveButton(isSaving: Boolean, enabled: Boolean, onClick: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    val bgColor = if (!enabled) slate700 else if (focused) primaryColor else primaryColor.copy(alpha = 0.85f)
    // 用 Box + focusable + clickable 而非 clickable Surface:tv-material3 clickable
    // Surface 形参链(ClickableSurfaceShape/Colors/Border)类型繁琐,Box 模式更可控
    // (对照 LoginScreen.FocusableTextButton / UserCard 的做法)。
    Box(
        modifier = Modifier
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .clickable(enabled = enabled, onClick = onClick)
            .then(
                if (focused) Modifier.shadow(
                    elevation = 16.dp,
                    shape = RoundedCornerShape(12.dp),
                    ambientColor = primaryColor,
                    spotColor = primaryColor,
                ) else Modifier
            )
            .background(bgColor, RoundedCornerShape(12.dp))
            .border(
                width = if (focused) NormalBorderWidthValue else 1.dp,
                color = if (focused) primaryColor else slate700,
                shape = RoundedCornerShape(12.dp),
            ),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = if (isSaving) "保存中..." else "保存修改",
            modifier = Modifier.padding(horizontal = 24.dp, vertical = 16.dp),
            color = Color.White,
            fontSize = 16.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}

// ── 字幕字号档位卡(对照 business-rules.md 第 6 节) ───────────────────────────

@Composable
private fun SubtitleSizeCard(
    state: SettingsUiState,
    onSelect: (Int) -> Unit,
) {
    SettingsCard(title = "播放设置（本地偏好）", subtitle = "字幕字号档位") {
        // Segmented 单选:4 档横排,选中高亮 primaryColor。
        Row(
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            modifier = Modifier.fillMaxWidth(),
        ) {
            SettingsViewModel.SUBTITLE_LABELS.forEachIndexed { index, label ->
                val sizeSp = com.revin.studyquest.tv.domain.SUBTITLE_SIZES_DP[index]
                SubtitleSegment(
                    modifier = Modifier.weight(1f),
                    label = label,
                    sizeSp = sizeSp,
                    selected = index == state.subtitleSizeIndex,
                    onClick = { onSelect(index) },
                )
            }
        }
        Spacer(Modifier.height(10.dp))
        Text(
            text = "档位仅本机生效,实时预览字号(${com.revin.studyquest.tv.domain.SUBTITLE_SIZES_DP[state.subtitleSizeIndex].toInt()}dp)",
            color = slate400,
            fontSize = 12.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}

/** 单个字幕档位胶囊。 */
@Composable
private fun SubtitleSegment(
    modifier: Modifier = Modifier,
    label: String,
    sizeSp: Float,
    selected: Boolean,
    onClick: () -> Unit,
) {
    var focused by remember { mutableStateOf(false) }
    val bgColor = when {
        selected -> primaryColor
        focused -> primaryColor.copy(alpha = 0.25f)
        else -> slate800
    }
    // Box + focusable + clickable(对照 SaveButton 注释:避开 clickable Surface 类型链)。
    Box(
        modifier = modifier
            .height(80.dp)
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .clickable(onClick = onClick)
            .then(
                if (focused) Modifier.shadow(
                    elevation = 16.dp,
                    shape = RoundedCornerShape(12.dp),
                    ambientColor = primaryColor,
                    spotColor = primaryColor,
                ) else Modifier
            )
            .background(bgColor, RoundedCornerShape(12.dp))
            .border(
                width = if (selected || focused) NormalBorderWidthValue else 1.dp,
                color = if (selected || focused) primaryColor else slate700,
                shape = RoundedCornerShape(12.dp),
            ),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            modifier = Modifier.fillMaxSize(),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                text = label,
                color = Color.White,
                fontSize = 16.sp,
                fontWeight = FontWeight.Bold,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = "${sizeSp.toInt()}sp",
                color = Color.White.copy(alpha = 0.7f),
                fontSize = 12.sp,
            )
        }
    }
}

// ── 登出卡 ─────────────────────────────────────────────────────────────────────

@Composable
private fun LogoutCard(
    state: SettingsUiState,
    onLogout: () -> Unit,
) {
    SettingsCard(title = "账号", subtitle = null) {
        var focused by remember { mutableStateOf(false) }
        // Box + focusable + clickable(对照 SaveButton 注释:避开 clickable Surface 类型链)。
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .onFocusChanged { focused = it.isFocused }
                .focusable()
                .clickable(onClick = onLogout)
                .then(
                    if (focused) Modifier.shadow(
                        elevation = 16.dp,
                        shape = RoundedCornerShape(12.dp),
                        ambientColor = accentOrange,
                        spotColor = accentOrange,
                    ) else Modifier
                )
                .background(
                    if (focused) accentOrange.copy(alpha = 0.18f) else Color.Transparent,
                    RoundedCornerShape(12.dp),
                )
                .border(
                    width = if (focused) NormalBorderWidthValue else 1.dp,
                    color = if (focused) accentOrange else slate700,
                    shape = RoundedCornerShape(12.dp),
                ),
            contentAlignment = Alignment.Center,
        ) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 20.dp, vertical = 16.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.Center,
            ) {
                Text(
                    text = "↩",
                    color = if (focused) accentOrange else slate400,
                    fontSize = 18.sp,
                )
                Spacer(Modifier.width(8.dp))
                Text(
                    text = if (state.isLoggingOut) "正在退出..." else "退出当前账号",
                    color = if (focused) accentOrange else slate400,
                    fontSize = 16.sp,
                    fontWeight = FontWeight.Bold,
                )
            }
        }
    }
}

// ── 通用设置卡(对照 PAD GlassPanel) ──────────────────────────────────────────

@Composable
private fun SettingsCard(
    title: String,
    subtitle: String?,
    content: @Composable () -> Unit,
) {
    Surface(
        shape = RoundedCornerShape(BorderRadiusValue),
        colors = SurfaceDefaults.colors(containerColor = slate800),
        border = androidx.tv.material3.Border(
            androidx.compose.foundation.BorderStroke(1.dp, slate700),
        ),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(modifier = Modifier.padding(28.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(36.dp)
                        .background(blue100, RoundedCornerShape(10.dp)),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = "●",
                        color = primaryColor,
                        fontSize = 18.sp,
                    )
                }
                Spacer(Modifier.width(14.dp))
                Column {
                    Text(
                        text = title,
                        color = Color.White,
                        fontSize = 20.sp,
                        fontWeight = FontWeight.Bold,
                    )
                    subtitle?.let {
                        Spacer(Modifier.height(2.dp))
                        Text(
                            text = it,
                            color = slate400,
                            fontSize = 13.sp,
                        )
                    }
                }
            }
            Spacer(Modifier.height(24.dp))
            content()
        }
    }
}

/**
 * 居中错误态(保留:baseUrl 配置失败 / 未登录时 nav 守卫未接前的兜底)。
 * 当前主流程不直接用到,留作后续扩展。
 */
@Composable
private fun CenterError(message: String, onRetry: () -> Unit) {
    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                text = message,
                color = Color.Red.copy(alpha = 0.8f),
                fontSize = 18.sp,
                fontWeight = FontWeight.Bold,
                textAlign = TextAlign.Center,
            )
            Spacer(Modifier.height(16.dp))
            RetryButton(onRetry = onRetry)
        }
    }
}

@Composable
private fun RetryButton(onRetry: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    val bgColor = if (focused) primaryColor else primaryColor.copy(alpha = 0.8f)
    // Box + focusable + clickable(对照 SaveButton 注释:避开 clickable Surface 类型链)。
    Box(
        modifier = Modifier
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .clickable(onClick = onRetry)
            .background(bgColor, RoundedCornerShape(12.dp)),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = "重试",
            modifier = Modifier.padding(horizontal = 24.dp, vertical = 12.dp),
            color = Color.White,
            fontSize = 16.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}
