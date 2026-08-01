package app.studyquest.tv.ui.theme

import androidx.compose.runtime.Composable
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.darkColorScheme

/**
 * TV 端主题入口 — Compose for TV 专属。
 *
 * 对照 `docs/design-tokens.md` 和 PAD 端 `frontend/lib/theme.dart` 的 `AppTheme.lightTheme`。
 *
 * 关键差异(本就是设计意图):
 *   - PAD 是浅色底(slate50) + 白卡。
 *   - TV 是深色底(slate900) + slate600 主文字(业界 TV APP 惯例:客厅不刺眼 /
 *     OLED 防烧 / 远距离可读)。design-tokens.md 明确「底色不同——PAD 浅色,TV 深色」。
 *
 * 这里固定提供 dark color scheme(无 lightScheme),TV APK 永远是 TV,无需切换。
 *
 * 用 [androidx.tv.material3.MaterialTheme](不是普通 androidx.compose.material3),
 * 给 [androidx.tv.material3.Surface] / [Button] / [Text] / [Card] 等 TV 专属组件
 * 提供统一 colorScheme / typography / shapes。
 *
 * 使用:
 * ```
 * StudyQuestTheme {
 *     Surface { ... }
 * }
 * ```
 */
@Composable
fun StudyQuestTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = StudyQuestDarkColorScheme,
        typography = StudyQuestTypography,
        shapes = StudyQuestShapes,
        content = content,
    )
}

/**
 * TV 深色 color scheme。
 *
 * 对照 PAD `theme.dart` 的 colorScheme(浅色),色值取自 design-tokens.md 的「TV 用途」列:
 *   - background / surface = slate900(`#0F172A`)—— TV 深色底
 *   - onBackground / onSurface = slate600(`#475569`)—— TV 主文字
 *   - primary = primaryColor(Blue-500)
 *   - secondary = accentGreen(Emerald-500,对齐 PAD secondary)
 *   - tertiary = accentOrange(Orange-500)
 *
 * tv-material3 1.0.0 的 [androidx.tv.material3.darkColorScheme] 入参接近普通 material3,
 * 但用 TV 专属的 `border` / `borderVariant`(注意不是 compose.material3 的 outline /
 * outlineVariant,也没有 borderDisabled)。这里显式覆盖常用槽,其余槽位用默认值。
 * border = slate700 对齐 design-tokens.md「TV 非焦点边框 #334155」。
 */
private val StudyQuestDarkColorScheme = darkColorScheme(
    primary = primaryColor,
    onPrimary = slate50,
    primaryContainer = indigo500,
    onPrimaryContainer = slate50,
    secondary = accentGreen,
    onSecondary = slate900,
    tertiary = accentOrange,
    onTertiary = slate900,
    background = slate900,
    onBackground = slate600,
    surface = slate900,
    onSurface = slate600,
    surfaceVariant = slate800,
    onSurfaceVariant = slate500,
    error = accentOrange,
    onError = slate50,
    // TV 专属:border 用于非聚焦态边框(对照 design-tokens.md TV 版 slate700 #334155)。
    border = slate700,
    borderVariant = slate800,
)
