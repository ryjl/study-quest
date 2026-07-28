package com.revin.studyquest.tv.ui.theme

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.lerp

/**
 * 渐变 token — 对照 `docs/design-tokens.md` 渐变表。
 *
 * 两端方向一致:`Alignment.topLeft` → `Alignment.bottomRight`。
 * Compose 里用 [Brush.linearGradient] + [Offset] 控制方向;
 *   topLeft    = (0f, 0f)     即左上角
 *   bottomRight = (Infinite, Infinite) 即右下角
 * 这里用 [Brush.linearGradient] 重载,start = topLeft,end = bottomRight。
 *
 * 注:对照 PAD `frontend/lib/theme.dart` 的 `LinearGradient(... Alignment.topLeft, Alignment.bottomRight)`。
 */

/** topLeft→bottomRight 的位置常量。 */
private val TopLeft = Offset(0f, 0f)
private val BottomRight = Offset(Float.POSITIVE_INFINITY, Float.POSITIVE_INFINITY)

private fun linear(colors: List<Color>): Brush =
    Brush.linearGradient(colors = colors, start = TopLeft, end = BottomRight)

// ---- 固定渐变(对照 design-tokens.md)----

/** 品牌主渐变:`#3B82F6` → `#6366F1`。主 CTA / header。 */
val brandGradient: Brush = linear(listOf(Color(0xFF3B82F6), Color(0xFF6366F1)))

/** 等级 / XP 徽章:`#FB923C` → `#FACC15`。 */
val levelBadgeGradient: Brush = linear(listOf(Color(0xFFFB923C), Color(0xFFFACC15)))

/** 头像光环:`#60A5FA` → `#C084FC`。 */
val avatarRingGradient: Brush = linear(listOf(Color(0xFF60A5FA), Color(0xFFC084FC)))

/** 学科卡片(蓝系):`#60A5FA` → `#3B82F6`。 */
val blueGradient: Brush = linear(listOf(Color(0xFF60A5FA), Color(0xFF3B82F6)))

/** 学科卡片(靛系):`#818CF8` → `#6366F1`。 */
val indigoGradient: Brush = linear(listOf(Color(0xFF818CF8), Color(0xFF6366F1)))

/** 学科卡片(天蓝系):`#38BDF8` → `#0EA5E9`。 */
val skyGradient: Brush = linear(listOf(Color(0xFF38BDF8), Color(0xFF0EA5E9)))

/** 学科卡片(翠系):`#34D399` → `#10B981`。 */
val emeraldGradient: Brush = linear(listOf(Color(0xFF34D399), Color(0xFF10B981)))

// ---- 动态学科卡片渐变 ----

/**
 * 解析 hex 字符串(如 "#f59e0b" / "f59e0b" / "fb9")为 [Color]。
 *
 * 对照 PAD `theme.dart` `_parseHex`:
 *   - 去掉 "#"
 *   - 3 位 hex 展开为 6 位(如 "fb9" → "ffbb99")
 *   - 6 位 hex 补全 ARGB 不透明前缀("FF")
 *   - 解析失败返回 null
 *
 * @return 解析出的 [Color];非法返回 null。
 */
fun parseHexOrNull(hex: String?): Color? {
    if (hex.isNullOrEmpty()) return null
    var h = hex.trim()
    if (h.startsWith("#")) h = h.substring(1)
    if (h.length == 3) {
        // 展开每 1 位为 2 位,如 "fb9" → "ffbb99"
        h = h.toCharArray().joinToString("") { "${it}${it}" }
    }
    if (h.length == 6) h = "FF$h" // 补 ARGB alpha=FF(不透明)
    if (h.length != 8) return null
    val value = h.toLongOrNull(16) ?: return null
    return Color(value.toInt())
}

/**
 * hex 字符串解析为 [Color],解析失败 fallback 到 [primaryColor]。
 *
 * 对照 PAD `theme.dart` `colorFromHex`。
 */
fun colorFromHex(hex: String?): Color = parseHexOrNull(hex) ?: primaryColor

/**
 * 根据后端配置的学科 hex 色动态度生成对角渐变。
 *
 * 对照 PAD `theme.dart` `getSubjectGradientFromColor`:
 *   - 起点 = hex 色 + 0.55 alpha 叠白(变浅)
 *   - 终点 = hex 原色
 *   - 方向 topLeft → bottomRight
 *   - hex 解析失败 fallback 到 [primaryColor] `#3B82F6`
 *
 * "0.55 alpha 叠白" 等价于 Compose 的 [lerp](白色, 原色, 0.55),
 * 即在白底上用 0.55 alpha 画原色,结果一致(预乘 alpha 后双线性插值)。
 *
 * @param hex 学科色 hex,如 "#f59e0b"。非法 / null 时 fallback [primaryColor]。
 */
fun subjectGradientFromColor(hex: String?): Brush {
    val base = parseHexOrNull(hex) ?: primaryColor
    val lightened = lerp(Color.White, base, 0.55f)
    return linear(listOf(lightened, base))
}
