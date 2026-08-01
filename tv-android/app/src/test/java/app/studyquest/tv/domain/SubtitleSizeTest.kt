package app.studyquest.tv.domain

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * 字幕字号档位表单测(对照 business-rules.md 第 6 节)。
 *
 * 重点保护:
 *   - 4 档值固定(18/24/30/38),改档位是跨端契约变更,不能静默改
 *   - label 4 个对齐档位
 *   - 默认 index = 1(中)
 *   - clamp / 取值对越界兜底
 */
class SubtitleSizeTest {

    @Test
    fun `sizes match business-rules spec`() {
        // 跨端契约:PAD `_subtitleSizes` = [18.0, 24.0, 30.0, 38.0]
        assertEquals(listOf(18.0f, 24.0f, 30.0f, 38.0f), SUBTITLE_SIZES_DP)
    }

    @Test
    fun `labels match sizes count and order`() {
        assertEquals(SUBTITLE_SIZES_DP.size, SUBTITLE_SIZE_LABELS.size)
        assertEquals(listOf("小", "中", "大", "超大"), SUBTITLE_SIZE_LABELS)
    }

    @Test
    fun `default index is middle`() {
        // business-rules.md 第 6 节:默认 index = 1(中,24dp)
        assertEquals(1, DEFAULT_SUBTITLE_SIZE_INDEX)
        assertEquals(24.0f, subtitleSizeDp(DEFAULT_SUBTITLE_SIZE_INDEX))
    }

    @Test
    fun `subtitleSizeDp returns correct value per index`() {
        assertEquals(18.0f, subtitleSizeDp(0))
        assertEquals(24.0f, subtitleSizeDp(1))
        assertEquals(30.0f, subtitleSizeDp(2))
        assertEquals(38.0f, subtitleSizeDp(3))
    }

    @Test
    fun `subtitleSizeDp clamps out_of_range indices`() {
        // 负数 → 最小档(18)
        assertEquals(18.0f, subtitleSizeDp(-1))
        assertEquals(18.0f, subtitleSizeDp(-100))
        // 超界 → 最大档(38)
        assertEquals(38.0f, subtitleSizeDp(4))
        assertEquals(38.0f, subtitleSizeDp(999))
    }

    @Test
    fun `clampSubtitleSizeIndex clamps to valid range`() {
        assertEquals(0, clampSubtitleSizeIndex(-1))
        assertEquals(0, clampSubtitleSizeIndex(0))
        assertEquals(2, clampSubtitleSizeIndex(2))
        assertEquals(3, clampSubtitleSizeIndex(3))
        assertEquals(3, clampSubtitleSizeIndex(5))
    }
}
