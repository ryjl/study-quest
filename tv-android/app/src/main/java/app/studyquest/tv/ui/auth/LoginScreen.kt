package app.studyquest.tv.ui.auth

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.layout.wrapContentSize
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.focusable
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
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
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import coil.compose.AsyncImage
import app.studyquest.tv.data.remote.dto.UserDto
import app.studyquest.tv.ui.theme.accentOrange
import app.studyquest.tv.ui.theme.primaryColor
import app.studyquest.tv.ui.theme.slate400
import app.studyquest.tv.ui.theme.slate600
import app.studyquest.tv.ui.theme.slate900

/**
 * 登录屏 —— TV 端核心入口。
 *
 * 对照 PAD `frontend/lib/ui/screen/login_screen.dart`:
 *   1. 拉用户列表 → 选人卡(TV 用 D-pad 聚焦,横向/网格排列)
 *   2. 选中 → PIN 蒙层(TV 用遥控器数字键盘,不是触屏 NumPad)
 *   3. 验证通过 → onSuccess 回调(导航层跳 MainNav)
 *
 * 视觉:深色底(slate900)+ 品牌蓝标题 + 用户卡聚焦发光环(对照 design-tokens.md)。
 * 首次连不上时显示错误态 + 重试 + baseUrl 配置入口(对照 PAD 错误态)。
 */
@Composable
fun LoginScreen(
    onSuccess: () -> Unit,
    viewModel: LoginViewModel = hiltViewModel(),
) {
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()
    val pendingUser by viewModel.pendingUser.collectAsStateWithLifecycle()
    val currentBaseUrl by viewModel.baseUrl.collectAsStateWithLifecycle()
    var showConfigDialog by remember { mutableStateOf(false) }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(slate900),
    ) {
        // 右上角:配置服务器地址按钮(常驻,任何状态都能点 —— 首次启动必用)。
        // 对照 PAD login_screen.dart 右上角 settings 按钮。
        FocusableTextButton(
            text = "配置服务器",
            onClick = { showConfigDialog = true },
            modifier = Modifier.align(Alignment.TopEnd).padding(24.dp),
        )

        // 中央内容
        Box(
            modifier = Modifier.fillMaxSize(),
            contentAlignment = Alignment.Center,
        ) {
            when (val s = uiState) {
                is LoginUiState.Loading -> Text("加载中...", color = primaryColor, fontSize = 20.sp)
                is LoginUiState.Empty -> EmptyState(onRetry = viewModel::loadUsers)
                is LoginUiState.Error -> ErrorState(
                    message = s.message,
                    onRetry = viewModel::loadUsers,
                    onConfig = { showConfigDialog = true },
                )
                is LoginUiState.UsersLoaded -> UsersPicker(
                    users = s.users,
                    onSelect = viewModel::selectUser,
                )
            }
        }
    }

    // 配置服务器地址 dialog
    if (showConfigDialog) {
        ServerConfigDialog(
            currentUrl = currentBaseUrl,
            onSave = { url ->
                viewModel.saveBaseUrl(url)
                showConfigDialog = false
            },
            onDismiss = { showConfigDialog = false },
        )
    }

    // PIN 蒙层(覆盖在选人页之上)。pendingUser 非空时显示。
    pendingUser?.let { user ->
        PinOverlay(
            user = user,
            viewModel = viewModel,
            onSuccess = onSuccess,
        )
    }
}

// ── 选人页 ──────────────────────────────────────────────────────────────────

@Composable
private fun UsersPicker(
    users: List<UserDto>,
    onSelect: (UserDto) -> Unit,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        // 标题(对照 PAD login 的 "学途奇旅")
        Text(
            text = "学途奇旅",
            color = primaryColor,
            fontSize = 48.sp,
            fontWeight = FontWeight.Bold,
        )
        Spacer(Modifier.height(8.dp))
        Text(
            text = "请选择你的账号",
            color = slate400,
            fontSize = 18.sp,
        )
        Spacer(Modifier.height(48.dp))

        // 用户卡:横向排列(TV 惯例 TvLazyRow,这里用户数少用 Row 简化)。
        // 卡片聚焦发光环对照 design-tokens.md「焦点视觉 TV 版」。
        Row(
            horizontalArrangement = Arrangement.spacedBy(24.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            users.forEach { user ->
                UserCard(user = user, onSelect = { onSelect(user) })
            }
        }
    }
}

@Composable
private fun UserCard(user: UserDto, onSelect: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    // 聚焦态:边框 primaryColor 3dp + 发光环(对照 design-tokens.md TV 版)
    val borderColor = if (focused) primaryColor else slate600
    val borderWidth = if (focused) 3.dp else 1.dp
    val bgColor = if (focused) Color(0xFF1D4ED8).copy(alpha = 0.15f) else Color(0xFF1E293B)

    // TV material3 的 clickable Surface 参数类型链复杂(ClickableSurfaceShape/Colors/Border),
    // 这里直接用 Box + clickable + focusable,更可控也更贴近 JetStream 的 Box.focusable 模式。
    Box(
        modifier = Modifier
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .clickable(onClick = onSelect)
            .then(
                if (focused) Modifier.shadow(
                    elevation = 24.dp,
                    shape = RoundedCornerShape(24.dp),
                    ambientColor = primaryColor,
                    spotColor = primaryColor,
                ) else Modifier
            )
            .background(bgColor, RoundedCornerShape(24.dp))
            .border(borderWidth, borderColor, RoundedCornerShape(24.dp)),
    ) {
        Column(
            modifier = Modifier.padding(24.dp).width(180.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            // 头像
            if (user.avatarUrl.isNotEmpty()) {
                AsyncImage(
                    model = user.avatarUrl,
                    contentDescription = user.nickname,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier
                        .size(90.dp)
                        .background(Color.White, CircleShape),
                )
            } else {
                Box(
                    modifier = Modifier
                        .size(90.dp)
                        .background(slate600, CircleShape),
                    contentAlignment = Alignment.Center,
                ) {
                    Text("?", color = Color.White, fontSize = 36.sp)
                }
            }
            Spacer(Modifier.height(14.dp))
            Text(
                text = user.nickname,
                color = Color.White,
                fontSize = 20.sp,
                fontWeight = FontWeight.Bold,
                textAlign = TextAlign.Center,
            )
            Spacer(Modifier.height(6.dp))
            Text(
                text = user.role.uppercase(),
                color = slate400,
                fontSize = 12.sp,
            )
        }
    }
}

// ── 错误 / 空态(对照 PAD _buildErrorBox / _buildEmptyBox) ───────────────────

@Composable
private fun ErrorState(message: String, onRetry: () -> Unit, onConfig: () -> Unit) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = Modifier.padding(32.dp),
    ) {
        Text("⚠ 无法连接服务器", color = Color.Red.copy(alpha = 0.8f), fontSize = 22.sp, fontWeight = FontWeight.Bold)
        Spacer(Modifier.height(12.dp))
        Text(
            "请检查后端是否运行,或点下方配置服务器地址。\n($message)",
            color = slate400,
            fontSize = 16.sp,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(24.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            FocusableTextButton(text = "重试", onClick = onRetry)
            FocusableTextButton(text = "配置服务器", onClick = onConfig)
        }
    }
}

@Composable
private fun EmptyState(onRetry: () -> Unit) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text("系统尚未创建任何用户", color = Color.White, fontSize = 20.sp, fontWeight = FontWeight.Bold)
        Spacer(Modifier.height(8.dp))
        Text("请登录后台管理系统添加学生账户", color = slate400, fontSize = 14.sp)
        Spacer(Modifier.height(20.dp))
        FocusableTextButton(text = "刷新加载", onClick = onRetry)
    }
}

/** 临时小型聚焦按钮(B 的 TvIconButton 回来后替换)。 */
@Composable
fun FocusableTextButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var focused by remember { mutableStateOf(false) }
    val bgColor = if (focused) primaryColor else primaryColor.copy(alpha = 0.8f)
    Box(
        modifier = modifier
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .clickable(onClick = onClick)
            .background(bgColor, RoundedCornerShape(12.dp)),
    ) {
        Text(
            text = text,
            modifier = Modifier.padding(horizontal = 24.dp, vertical = 12.dp),
            color = Color.White,
            fontSize = 16.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}

// ── 配置服务器地址 dialog(对照 PAD _showIpConfigDialog) ─────────────────────

@Composable
private fun ServerConfigDialog(
    currentUrl: String,
    onSave: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    // 输入框初始值:已有 baseUrl,否则给占位提示。
    var input by remember(currentUrl) { mutableStateOf(currentUrl) }
    val keyboard = LocalSoftwareKeyboardController.current

    // 半透明遮罩 + 居中卡片
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black.copy(alpha = 0.7f)),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .background(Color(0xFF1E293B), RoundedCornerShape(24.dp))
                .padding(32.dp)
                .width(560.dp),
        ) {
            Column {
                Text(
                    text = "配置服务器地址",
                    color = Color.White,
                    fontSize = 22.sp,
                    fontWeight = FontWeight.Bold,
                )
                Spacer(Modifier.height(16.dp))
                Text(
                    text = "请输入后端 API 地址(局域网或外网穿透):",
                    color = slate400,
                    fontSize = 14.sp,
                )
                Spacer(Modifier.height(12.dp))

                // 输入框(TV 上点聚焦弹软键盘)
                BasicTextField(
                    value = input,
                    onValueChange = { input = it },
                    textStyle = TextStyle(
                        color = Color.White,
                        fontSize = 18.sp,
                        fontFamily = FontFamily.Monospace,
                    ),
                    cursorBrush = androidx.compose.ui.graphics.SolidColor(primaryColor),
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(
                        keyboardType = KeyboardType.Uri,
                        imeAction = ImeAction.Done,
                    ),
                    keyboardActions = KeyboardActions(
                        onDone = {
                            keyboard?.hide()
                            if (input.isNotBlank()) onSave(input.trim())
                        },
                    ),
                    modifier = Modifier
                        .fillMaxWidth()
                        .background(slate900, RoundedCornerShape(12.dp))
                        .border(1.dp, slate600, RoundedCornerShape(12.dp))
                        .padding(16.dp),
                    decorationBox = { inner ->
                        if (input.isEmpty()) {
                            Text(
                                "http://192.168.1.100:8080",
                                color = slate400.copy(alpha = 0.5f),
                                fontSize = 16.sp,
                                fontFamily = FontFamily.Monospace,
                            )
                        }
                        inner()
                    },
                )

                Spacer(Modifier.height(12.dp))
                Text(
                    text = "例如 http://192.168.1.100:8080,需与后端在同一局域网",
                    color = slate400,
                    fontSize = 12.sp,
                )
                Spacer(Modifier.height(24.dp))

                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.End,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    FocusableTextButton(
                        text = "取消",
                        onClick = onDismiss,
                    )
                    Spacer(Modifier.width(16.dp))
                    FocusableTextButton(
                        text = "保存并重试",
                        onClick = { if (input.isNotBlank()) onSave(input.trim()) },
                    )
                }
            }
        }
    }
}
