package com.revin.studyquest.tv.ui.player

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.unit.dp
import androidx.compose.ui.graphics.vector.rememberVectorPainter
import androidx.tv.material3.Icon
import com.revin.studyquest.tv.ui.theme.primaryColor

/**
 * 控制行 —— 一排连续 focusable 按钮(对照腾讯/网易 TV,无 Spacer)。
 *
 * 对照 design-tokens.md「控制行图标按钮聚焦态」+ PAD `_buildControlsRow`。
 * 按钮:播放/暂停 → 速度 → 字幕 → 字幕大小 → 音轨(条件) → 全屏。
 *
 * **关键**:连续一排,◄► 在按钮间移动(几何遍历可靠,因为无 Spacer 分簇)。
 * 这正是 Flutter 端 FocusButton 在复杂布局下失败的根因 —— TV 原生 Compose
 * 的 focus system 处理密集连续行比 Flutter 可靠得多。
 */
@Composable
fun VideoPlayerControls(
    buttons: List<PlayerControlButtonData>,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier.padding(horizontal = 24.dp, vertical = 12.dp),
        horizontalArrangement = Arrangement.spacedBy(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        buttons.forEach { btn ->
            PlayerControlButton(icon = btn.icon, label = btn.label, onClick = btn.onClick)
        }
    }
}

/** 控制行单个按钮的数据。 */
data class PlayerControlButtonData(
    val icon: ImageVector,
    val label: String,
    val onClick: () -> Unit,
)

/**
 * 单个控制按钮 —— 圆形,D-pad 聚焦发光环。
 *
 * 对照 design-tokens.md「控制行图标按钮聚焦态」:
 *   - 圆形背景 primaryColor alpha 0.2(聚焦)
 *   - 白色图标 28sp
 *   - 发光环 primaryColor alpha 0.4 blurRadius 16
 *
 * Enter/Select 激活(对照 PAD isActivationKey)。
 */
@Composable
fun PlayerControlButton(
    icon: ImageVector,
    label: String,
    onClick: () -> Unit,
) {
    var focused by remember { mutableStateOf(false) }
    val bgColor = if (focused) primaryColor.copy(alpha = 0.3f) else Color.White.copy(alpha = 0.08f)

    Box(
        modifier = Modifier
            .size(48.dp)
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .onPreviewKeyEvent { event ->
                // Enter / Select / DPadCenter 激活(对照 isActivationKey)
                if (event.type == KeyEventType.KeyDown && event.key in ACTIVATION_KEYS) {
                    onClick()
                    true
                } else false
            }
            .clickable(onClick = onClick) // 鼠标/触屏也能点(虽然 TV 主用 D-pad)
            .background(bgColor, CircleShape),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = label,
            tint = Color.White,
            modifier = Modifier.size(28.dp),
        )
    }
}

/** 激活键集合(对照 PAD isActivationKey:Enter / Select / gameButtonSelect)。 */
private val ACTIVATION_KEYS = setOf(
    Key.Enter,
    Key.DirectionCenter, // D-pad 中心键(Select)
)
