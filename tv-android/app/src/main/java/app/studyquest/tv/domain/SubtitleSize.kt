package app.studyquest.tv.domain

// ---------------------------------------------------------------------------
// 字幕字号档位(对照 business-rules.md 第 6 节 + PAD `ui_prefs.dart` `_subtitleSizes`)
// ---------------------------------------------------------------------------

/**
 * 字幕字号档位表。
 *
 * **跨端契约**(business-rules.md 第 6 节):两端档位数、label、字号值必须一致。
 *   - index 0:小 18.0
 *   - index 1:中 24.0(默认)
 *   - index 2:大 30.0
 *   - index 3:超大 38.0
 *
 * PAD 实现:`frontend/lib/service/ui_prefs.dart` 的 `_subtitleSizes`(行 32)。
 * 单位两端都是 dp(Flutter 的 logical pixel = Android 的 dp)。
 *
 * TV 端渲染时把 dp 喂给 [androidx.media3.ui.SubtitleView.setFixedTextSize]
 * (用 `COMPLEX_UNIT_SP`,TV 上系统字号通常默认 1.0,SP≈dp;选 SP 是 media3 内部
 * 惯例,且字幕跟随系统无障碍字号设置也合理)。
 *
 * 定义成 top-level const + 函数(domain 层无 Android 依赖,纯逻辑,可单测),
 * 而不是 enum:档位本身是"序号 → 值"的纯映射,enum 反而增加样板。
 */

/** 字幕字号档位值(dp),index 对应菜单顺序。 */
val SUBTITLE_SIZES_DP: List<Float> = listOf(18.0f, 24.0f, 30.0f, 38.0f)

/** 字幕字号档位的中文 label(给菜单用),index 与 [SUBTITLE_SIZES_DP] 对齐。 */
val SUBTITLE_SIZE_LABELS: List<String> = listOf("小", "中", "大", "超大")

/** 默认档位 index = 1(中,24dp),对照 business-rules.md 第 6 节 + PAD `UiPrefs`。 */
const val DEFAULT_SUBTITLE_SIZE_INDEX = 1

/**
 * 安全取档位值(index 越界时 clamp 到合法范围 + 兜底默认档)。
 * 调用方(VM/SP 读取)可能拿到脏数据,这里统一兜底。
 */
fun subtitleSizeDp(index: Int): Float =
    SUBTITLE_SIZES_DP[index.coerceIn(0, SUBTITLE_SIZES_DP.lastIndex)]

/** 安全取档位 index(SP 读出来的可能越界,clamp 一下)。 */
fun clampSubtitleSizeIndex(index: Int): Int =
    index.coerceIn(0, SUBTITLE_SIZES_DP.lastIndex)
