package com.revin.studyquest.tv.ui.components

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.InteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.draw.scale
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.revin.studyquest.tv.ui.theme.BorderWidthValue
import com.revin.studyquest.tv.ui.theme.NormalBorderWidthValue
import com.revin.studyquest.tv.ui.theme.TvUnfocusedBorderWidthValue
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate700

/**
 * 焦点视觉参数 — 对照 `docs/design-tokens.md`「焦点视觉」节 TV 版。
 *
 * TV 端比 PAD 更亮 / 柔光更大(远距离可辨识):
 *   - 聚焦态边框 = primaryColor,宽 [BorderWidthValue] (3dp)
 *   - 聚焦发光环 = primaryColor + alpha 0.35,blurRadius 24,offset (0,0)
 *   - 聚焦背景微提亮 = primaryColor alpha 0.12 叠加
 *   - 非聚焦态边框 = slate700(#334155)宽 1dp,无阴影
 *   - (可选)聚焦 scale 1.0 → 1.05,参考腾讯/网易 TV
 *
 * @param shape 发光环 / 边框应用的形状(卡片圆角等),默认 RectangleShape。
 * @param focused 是否处于聚焦态。各组件用 [collectIsFocusedAsState] 拿到这个值传入。
 */
data class FocusGlowSpec(
    val shape: Shape = RectangleShape,
    val focused: Boolean = false,
    val focusedBorderColor: Color = primaryColor,
    val focusedBorderWidth: Dp = BorderWidthValue,
    val unfocusedBorderColor: Color = slate700,
    val unfocusedBorderWidth: Dp = TvUnfocusedBorderWidthValue,
    /** 发光环 blur 半径(px)。design-tokens.md TV 版 = 24dp,由调用方换算成 px 传入。 */
    val glowBlurRadiusPx: Float = DEFAULT_GLOW_BLUR_RADIUS_PX,
    /** 发光环颜色 = primaryColor alpha 0.35。 */
    val glowColor: Color = primaryColor.copy(alpha = 0.35f),
    /** 聚焦背景叠加色 = primaryColor alpha 0.12。 */
    val focusedOverlayColor: Color = primaryColor.copy(alpha = 0.12f),
    /** 聚焦缩放(可选)。1.05 参考 design-tokens.md 焦点缩放节。 */
    val focusedScale: Float = 1.05f,
) {
    companion object {
        /**
         * 默认发光环 blur 半径(px)。design-tokens.md TV 版规范是 24dp;[Modifier.focusGlow]
         * 在 composed 作用域内会按 density 重算,这里给个 1x mdpi 的兜底常量。
         */
        const val DEFAULT_GLOW_BLUR_RADIUS_PX: Float = 48f
    }
}

/**
 * 应用 TV 焦点发光环 + 边框 + 背景提亮 + (可选)缩放。
 *
 * 这是一个 `Modifier.composed {}` 扩展,内部用 [collectIsFocusedAsState] 读取
 * [InteractionSource] 的聚焦态——这是 Compose for TV D-pad 导航的标准做法。
 *
 * 发光环用 [drawWithContent] 在内容下方画一层高斯模糊色块(近似 BoxShadow 的 blur)。
 * 注:Compose 原生没直接 BoxShadow API,这里用扩展 padding + 半透明色块近似柔光。
 * 后续若接入 RenderEffect.createBlurEffect(API 31+)可换成真模糊,留 TODO。
 *
 * 用法:
 * ```
 * val interaction = remember { MutableInteractionSource() }
 * Modifier
 *     .focusGlow(interactionSource = interaction, shape = RoundedCornerShape(20.dp))
 *     .focusable(interactionSource = interaction)
 * ```
 *
 * @param interactionSource 与 `Modifier.focusable(interactionSource = ...)` 共用同一个
 *   实例,这样发光环和原生聚焦态绑定。
 * @param shape 发光环 / 边框的形状。默认 RectangleShape(图标按钮等圆角场景请传 CircleShape)。
 * @param enabled 是否启用发光效果(disabled 的按钮不画)。
 * @param applyScale 是否应用聚焦缩放(1.05),卡片密集场景可关掉避免布局抖动。
 */
fun Modifier.focusGlow(
    interactionSource: InteractionSource,
    shape: Shape = RectangleShape,
    enabled: Boolean = true,
    applyScale: Boolean = true,
): Modifier = composed {
    val focused by interactionSource.collectIsFocusedAsState()
    if (!enabled) return@composed this

    // density 感知的发光环半径(design-tokens.md 规范 24dp → px)。
    val glowBlurRadiusPx = with(androidx.compose.ui.platform.LocalDensity.current) { 24.dp.toPx() }

    // 平滑动画:聚焦/失焦过渡更柔和(TV 大屏视觉)。
    val focusProgress by animateFloatAsState(
        targetValue = if (focused) 1f else 0f,
        animationSpec = tween(durationMillis = 180),
        label = "focusGlowProgress",
    )

    val spec = FocusGlowSpec(
        shape = shape,
        focused = focused,
        glowBlurRadiusPx = glowBlurRadiusPx,
    )

    // 1. 边框(聚焦 primaryColor 3dp ↔ 非聚焦 slate700 1dp)
    val borderColor = if (focused) spec.focusedBorderColor else spec.unfocusedBorderColor
    val borderWidth = if (focused) spec.focusedBorderWidth else spec.unfocusedBorderWidth
    val bordered = this.border(width = borderWidth, color = borderColor, shape = shape)

    // 2. 缩放(可选,聚焦 1.05)
    val scaled = if (applyScale) bordered.scale(scale = 1f + (spec.focusedScale - 1f) * focusProgress) else bordered

    // 3. 发光环(柔光)+ 背景提亮:drawWithContent 内先画发光底,再画内容。
    scaled.drawWithContent {
        // 背景微提亮(primaryColor alpha 0.12 * focusProgress),在内容之下。
        if (focusProgress > 0f) {
            drawRect(color = spec.focusedOverlayColor.copy(alpha = spec.focusedOverlayColor.alpha * focusProgress))
        }
        // 发光环:在内容外围画一圈柔光。通过向外扩展绘制半透明色块近似 blurRadius 24。
        // TODO(发光环):Compose 原生无 BoxShadow。若 minSdk 升到 31+,可改用
        //   graphicsLayer { renderEffect = RenderEffect.createBlurEffect(24f, 24f, ...) }
        //   画一层模糊副本得到更准确的高斯柔光。当前用向外扩展半透明色块近似。
        if (focusProgress > 0f) {
            val glowAlpha = spec.glowColor.alpha * focusProgress
            val pad = spec.glowBlurRadiusPx
            drawRect(
                color = spec.glowColor.copy(alpha = glowAlpha),
                topLeft = androidx.compose.ui.geometry.Offset(-pad, -pad),
                size = androidx.compose.ui.geometry.Size(size.width + pad * 2, size.height + pad * 2),
            )
        }
        drawContent()
    }
}

/**
 * 非聚焦态默认边框宽度常量(对照 design-tokens.md TV 版 1dp)。
 * 保留对外暴露,某些组件若只想要非焦点边框而不要发光环可用。
 */
val ComponentNormalBorderWidthValue = NormalBorderWidthValue

/**
 * 给纯展示场景的轻量 padding helper(发光环需要向外留空间不被裁剪)。
 * 卡片 / 按钮在 `focusGlow` 外包一层 padding,避免发光环被父容器 clip。
 */
fun Modifier.focusGlowPadding(glowRadius: Dp = 24.dp): Modifier = this.padding(glowRadius)

/**
 * Composable 形式获取密度感知的 blur 半径(px),便于外部自定义发光环尺寸。
 */
@Composable
fun rememberGlowBlurRadiusPx(radius: Dp = 24.dp): Float =
    with(androidx.compose.ui.platform.LocalDensity.current) { radius.toPx() }
