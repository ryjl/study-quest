package com.revin.studyquest.tv.ui.theme

import androidx.compose.runtime.Composable
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.tv.material3.Typography

/**
 * TV 端 Typography — 对照 `docs/design-tokens.md` 字体表。
 *
 * 字号 token 基准:
 *   displayLarge  bold      32sp  (主标题)
 *   titleLarge    semibold  20sp  (标题)
 *   bodyLarge     medium    18sp  (正文)
 *   bodyMedium    medium    16sp  (辅助)
 *
 * TV 端字号在基准上 ×1.1(远距离可读,见 design-tokens.md 字体节脚注)。
 * 这里采用的系数是 1.1:
 *   displayLarge  32 → 35
 *   titleLarge    20 → 22
 *   bodyLarge     18 → 20
 *   bodyMedium    16 → 18
 *
 * 字族:两端 token 一致为 Quicksand。PAD 用 google_fonts;TV 暂用系统默认
 * (FontFamily.Default),后续接入 Downloadable Fonts 或 res/font/ 时统一替换
 * [QuicksandFontFamily] 的实现即可,见 TODO。
 *
 * TV Material 1.0.0 的 [Typography] 只暴露部分 style 槽(display/title/body/
 * label 各 Large/Medium/Small),其余字段用默认。
 */

// TODO(字体):接入 Quicksand。两选一:
//   (1) res/font/ 放 Quicksand-Regular / Medium / SemiBold / Bold,用
//       FontFamily(Font(R.font.quicksand_bold, FontWeight.Bold), ...)。
//   (2) Downloadable Fonts(Google Fonts via Compose),用 GoogleFont + FontProvider。
// 当前用 FontFamily.Default 占位,排版样式(fontWeight/fontSize)已对齐 token。
val QuicksandFontFamily: FontFamily = FontFamily.Default

/** TV 端字号 ×1.1 倍率(相对 token 基准),远距离客厅可读。 */
private const val TV_FONT_SCALE = 1.1f

private val displayLargeSize = (32 * TV_FONT_SCALE).sp
private val titleLargeSize = (20 * TV_FONT_SCALE).sp
private val bodyLargeSize = (18 * TV_FONT_SCALE).sp
private val bodyMediumSize = (16 * TV_FONT_SCALE).sp

/**
 * TV 端 Typography。注意 android 侧 [TextStyle.color] 在 [Typography] 里通常不设
 * (随 MaterialTheme.colorScheme 走);这里也保持不设,颜色由使用方决定。
 */
val StudyQuestTypography = Typography(
    displayLarge = TextStyle(
        fontFamily = QuicksandFontFamily,
        fontWeight = FontWeight.Bold,
        fontSize = displayLargeSize,
        lineHeight = (displayLargeSize.value * 1.2f).sp,
    ),
    titleLarge = TextStyle(
        fontFamily = QuicksandFontFamily,
        fontWeight = FontWeight.SemiBold,
        fontSize = titleLargeSize,
        lineHeight = (titleLargeSize.value * 1.25f).sp,
    ),
    bodyLarge = TextStyle(
        fontFamily = QuicksandFontFamily,
        fontWeight = FontWeight.Medium,
        fontSize = bodyLargeSize,
        lineHeight = (bodyLargeSize.value * 1.35f).sp,
    ),
    bodyMedium = TextStyle(
        fontFamily = QuicksandFontFamily,
        fontWeight = FontWeight.Medium,
        fontSize = bodyMediumSize,
        lineHeight = (bodyMediumSize.value * 1.4f).sp,
    ),
)
