package com.revin.studyquest.tv.ui.theme

import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Shapes

/**
 * 形状 token — 对照 `docs/design-tokens.md` 圆角与描边节。
 *
 *   borderRadiusValue = 20.0 dp  (卡片 / 按钮 / 输入框圆角)
 *   borderWidthValue  = 3.0 dp   (焦点态边框宽度)
 *
 * TV 用 [androidx.tv.material3.Shapes](不是普通 material3)。所有形状统一 20dp 圆角,
 * 与 PAD 端 AppTheme.borderRadiusValue 一致。
 */

/** 卡片 / 按钮 / 输入框圆角(两端一致 20dp)。 */
val BorderRadiusValue = 20.dp

/** 焦点态边框宽度(两端一致 3dp)。 */
val BorderWidthValue = 3.dp

/** 普通态(非焦点)边框宽度(两端一致 2dp)。 */
val NormalBorderWidthValue = 2.dp

/** TV 非焦点边框宽度(深底更细,弱化非焦点)— 1dp,见 design-tokens.md TV 版。 */
val TvUnfocusedBorderWidthValue = 1.dp

private val componentShape = RoundedCornerShape(BorderRadiusValue)

/**
 * TV Material Shapes。给 [androidx.tv.material3.Surface] / [Button] / [Card] 等组件
 * 用。所有槽位统一 20dp 圆角,与 PAD 端一致。
 */
val StudyQuestShapes = Shapes(
    extraSmall = componentShape,
    small = componentShape,
    medium = componentShape,
    large = componentShape,
    extraLarge = componentShape,
)
