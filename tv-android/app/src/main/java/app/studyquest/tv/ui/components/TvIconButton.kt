package app.studyquest.tv.ui.components

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Border
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.Glow
import androidx.tv.material3.Icon
import androidx.tv.material3.Surface
import app.studyquest.tv.ui.theme.BorderWidthValue
import app.studyquest.tv.ui.theme.TvUnfocusedBorderWidthValue
import app.studyquest.tv.ui.theme.primaryColor
import app.studyquest.tv.ui.theme.slate50
import app.studyquest.tv.ui.theme.slate700

/**
 * TV 图标按钮 — 圆形 / 圆角,D-pad 可聚焦。
 *
 * 参考风格:Google JetStreamCompose(androidx.tv.material3 系列)。
 *
 * 焦点视觉对照 `docs/design-tokens.md`「焦点视觉」节 TV 版(由 tv-material3 的
 * [Surface] 原生聚焦态支持,通过 [ClickableSurfaceDefaults] 的 scale / border / glow
 * 配置,而不是手动 modifier):
 *   - 聚焦态边框:[primaryColor] 宽 [BorderWidthValue] (3dp)
 *   - 聚焦发光环:[primaryColor] alpha 0.35,blurRadius 24(Glow elevation)
 *   - 聚焦背景微提亮:[primaryColor] alpha 0.12
 *   - 聚焦缩放:1.0 → 1.05(对照 design-tokens.md 焦点缩放节)
 *   - 非聚焦态边框:[slate700] 宽 1dp(TV 深底中间灰,弱化非焦点)
 *
 * 用 [androidx.tv.material3.Surface] 的可点按重载,自带 D-pad 焦点处理——
 * 遥控器方向键可聚焦,Enter / OK 键触发 [onClick]。无需手动 `Modifier.focusable()`。
 *
 * @param icon 图标矢量(用 `androidx.compose.material.icons.Icons.Filled.*`)。
 * @param onClick 点击回调(D-pad Enter / 鼠标点击触发)。
 * @param modifier 外部 modifier。
 * @param enabled 是否启用。disabled 时仍可聚焦但不可点按(TV 惯例,用于展示 disabled 态)。
 * @param size 按钮尺寸(正方形边长)。默认 48dp。
 * @param shape 形状,默认 [CircleShape](播放器控制行用圆形);卡片行可用 RoundedCornerShape(20.dp)。
 * @param iconTint 图标颜色。默认 [slate50](白)。
 * @param backgroundColor 非聚焦底色。默认透明(叠在父容器上)。
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun TvIconButton(
    icon: ImageVector,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    size: Dp = 48.dp,
    shape: Shape = CircleShape,
    iconTint: Color = slate50,
    backgroundColor: Color = Color.Transparent,
) {
    Surface(
        onClick = onClick,
        enabled = enabled,
        modifier = modifier.size(size),
        shape = ClickableSurfaceDefaults.shape(shape = shape),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.05f),
        border = ClickableSurfaceDefaults.border(
            // 非聚焦态(默认 border 参数):slate700 细边框,弱化非焦点
            border = Border(
                border = BorderStroke(width = TvUnfocusedBorderWidthValue, color = slate700),
            ),
            // 聚焦态:primaryColor 粗边框
            focusedBorder = Border(
                border = BorderStroke(width = BorderWidthValue, color = primaryColor),
            ),
            // 聚焦但 disabled:同非聚焦
            focusedDisabledBorder = Border(
                border = BorderStroke(width = TvUnfocusedBorderWidthValue, color = slate700),
            ),
        ),
        glow = ClickableSurfaceDefaults.glow(
            focusedGlow = Glow(elevation = 24.dp, elevationColor = primaryColor.copy(alpha = 0.35f)),
        ),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = backgroundColor,
            focusedContainerColor = primaryColor.copy(alpha = 0.12f), // 聚焦背景微提亮
            disabledContainerColor = backgroundColor,
            contentColor = iconTint,
            focusedContentColor = iconTint,
            disabledContentColor = iconTint.copy(alpha = 0.38f),
        ),
    ) {
        Box(
            modifier = Modifier.size(size),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = icon,
                contentDescription = null, // 由调用方通过 modifier.semantics 或外层包一层给 a11y 描述
                tint = iconTint,
            )
        }
    }
}
