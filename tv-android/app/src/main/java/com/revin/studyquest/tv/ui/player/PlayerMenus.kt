package com.revin.studyquest.tv.ui.player

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Popup
import androidx.compose.ui.window.PopupProperties
import androidx.tv.material3.Icon
import androidx.tv.material3.Text
import com.revin.studyquest.tv.ui.theme.accentGreen
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate400
import com.revin.studyquest.tv.ui.theme.slate700
import com.revin.studyquest.tv.ui.theme.slate800
import com.revin.studyquest.tv.ui.theme.slate900

/**
 * 带弹出菜单的控制按钮(对照腾讯/网易 TV:点按 → 按钮上方弹出选项列表)。
 *
 * 交互:
 *   - 点击按钮(ENTER / 触屏)→ 菜单在按钮**正上方**弹出(不居中,贴着按钮)。
 *   - 菜单首项自动聚焦(D-pad 从按钮往上自然进菜单)。
 *   - 上下在选项间移动,ENTER 确认,Back / ESC / 选外部 关闭菜单,焦点回按钮。
 *   - 选中某项 → 触发 [onSelect] + 关闭菜单。
 *
 * @param icon 按钮图标(速度/字幕/音轨)。
 * @param label 无障碍标签。
 * @param options 选项列表(label + 是否选中)。
 * @param onSelect 选中某项的回调(index)。
 */
@Composable
fun PlayerMenuButton(
    icon: ImageVector,
    label: String,
    options: List<PlayerMenuOption>,
    onSelect: (Int) -> Unit,
    modifier: Modifier = Modifier,
) {
    var menuOpen by remember { mutableStateOf(false) }
    var focused by remember { mutableStateOf(false) }
    val bgColor = if (focused) primaryColor.copy(alpha = 0.3f) else Color.White.copy(alpha = 0.08f)

    Box(modifier = modifier) {
        // 触发按钮(圆形,同 PlayerControlButton 风格)。
        Box(
            modifier = Modifier
                .size(48.dp)
                .onFocusChanged { focused = it.isFocused }
                .focusable()
                .onPreviewKeyEvent { event ->
                    if (event.type == KeyEventType.KeyDown &&
                        (event.key == Key.Enter || event.key == Key.DirectionCenter)
                    ) {
                        menuOpen = true
                        true
                    } else false
                }
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

        // 弹出菜单(贴按钮上方)。Popup 的 alignment 用 alignmentOffset 让它出现在按钮正上方。
        if (menuOpen && options.isNotEmpty()) {
            PlayerOptionPopup(
                options = options,
                onDismiss = { menuOpen = false },
                onSelect = { index ->
                    menuOpen = false
                    onSelect(index)
                },
            )
        }
    }
}

/**
 * 菜单选项数据。
 *
 * @param label 显示文字。
 * @param selected 是否当前选中(显示绿色对勾标记)。
 */
data class PlayerMenuOption(
    val label: String,
    val selected: Boolean = false,
)

/**
 * 弹出选项列表(Popup 锚定在调用位置的上方)。
 *
 * 用 Compose [Popup] + [alignmentOffset] 让列表出现在触发按钮上方。popupPositionProvider
 * 让 Popup 的左下角对齐按钮的左上角(默认 Popup 是左上对齐,这里需要往上偏移列表高度)。
 * Compose Popup 默认 alignment 到 parent 的 (0,0),用 onPlacementRemoved / offset 调整。
 *
 * 焦点:首项自动聚焦(ENTER 进菜单后,方向键在选项间移动)。选中/Back/ESC 关闭。
 */
@Composable
private fun PlayerOptionPopup(
    options: List<PlayerMenuOption>,
    onDismiss: () -> Unit,
    onSelect: (Int) -> Unit,
) {
    val firstFocusRequester = remember { FocusRequester() }
    // 首项聚焦(进菜单后方向键直接生效)。
    LaunchedEffect(Unit) {
        withFrameNanos { }
        runCatching { firstFocusRequester.requestFocus() }
    }

    Popup(
        // alignment 到 parent 的 TopStart,然后 offset 往上偏移列表自身高度。
        // 这里用 offset = (0, -列表高度估算)。更准确的做法是用 onPlacementRemoved
        // 测量,但 Popup 简化处理:offset y = -(options.size * 行高 + padding)。
        // 行高约 40dp,我们用 density 换算。
        alignment = Alignment.TopStart,
        offset = IntOffset(0, with(LocalDensity.current) { -(options.size * 40 + 16).dp.toPx().toInt() }),
        onDismissRequest = onDismiss,
        properties = PopupProperties(
            focusable = true,
            dismissOnBackPress = true,
            dismissOnClickOutside = true,
        ),
    ) {
        Column(
            modifier = Modifier
                .background(slate900.copy(alpha = 0.95f), RoundedCornerShape(12.dp))
                .border(1.dp, slate700, RoundedCornerShape(12.dp))
                .padding(vertical = 6.dp),
            verticalArrangement = Arrangement.spacedBy(2.dp),
        ) {
            options.forEachIndexed { index, opt ->
                PlayerOptionItem(
                    label = opt.label,
                    selected = opt.selected,
                    isFirst = index == 0,
                    firstFocusRequester = firstFocusRequester,
                    onClick = { onSelect(index) },
                )
            }
        }
    }
}

/** 单个选项行。 */
@Composable
private fun PlayerOptionItem(
    label: String,
    selected: Boolean,
    isFirst: Boolean,
    firstFocusRequester: FocusRequester,
    onClick: () -> Unit,
) {
    var focused by remember { mutableStateOf(false) }
    val bgColor = when {
        focused -> primaryColor.copy(alpha = 0.25f)
        selected -> slate800
        else -> Color.Transparent
    }
    Box(
        modifier = Modifier
            .then(if (isFirst) Modifier.focusRequester(firstFocusRequester) else Modifier)
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .onPreviewKeyEvent { event ->
                if (event.type == KeyEventType.KeyDown &&
                    (event.key == Key.Enter || event.key == Key.DirectionCenter)
                ) {
                    onClick()
                    true
                } else if (event.type == KeyEventType.KeyDown && event.key == Key.Back) {
                    false // 让 Popup 的 dismissOnBackPress 处理
                } else false
            }
            .background(bgColor, RoundedCornerShape(8.dp))
            .padding(horizontal = 16.dp, vertical = 8.dp),
    ) {
        androidx.compose.foundation.layout.Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            // 选中标记(绿色对勾)。
            if (selected) {
                Text("✓", color = accentGreen, fontSize = 14.sp, fontWeight = FontWeight.Bold)
            } else {
                Box(modifier = Modifier.size(14.dp))
            }
            Text(
                text = label,
                color = if (focused || selected) Color.White else slate400,
                fontSize = 14.sp,
                fontWeight = if (selected) FontWeight.Bold else FontWeight.Normal,
            )
        }
    }
}
