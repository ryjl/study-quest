package com.revin.studyquest.tv.domain

import com.revin.studyquest.tv.data.remote.dto.EpisodeSubtitleDto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 字幕合并 + 音轨逻辑的单测。
 *
 * 用例直接对照 Flutter `frontend/test/service/track_selection_controller_test.dart`
 * 翻译,保证语义一致(backend 优先 / native 去重 label / 去重 language / 无 backend
 * native 兜底 / 第一项「无」)。
 */
class TrackSelectionTest {

    // ---- helpers -----------------------------------------------------------

    private fun backendSub(
        id: Int,
        language: String = "zh-CN",
        label: String = "中文",
    ) = EpisodeSubtitleDto(
        id = id,
        language = language,
        label = label,
        url = "/api/v1/subtitles/$id.vtt",
    )

    private fun nativeTrack(
        id: String,
        title: String? = null,
        language: String? = null,
    ) = NativeSubtitleTrack(id = id, title = title, language = language)

    private fun labels(options: List<SubtitleOption>) =
        options.map { it.label }

    // ---- mergeSubtitleOptions ---------------------------------------------

    @Test
    fun `backend and native same label - keep only one backend`() {
        // 原 bug 场景:libmpv 抽出内嵌「中文」,backend 也有「中文」。
        // 之前会变成「中文」+「中文(校对版)」两个,且点校对版无字幕。
        val options = mergeSubtitleOptions(
            backendSubtitles = listOf(backendSub(id = 10, label = "中文")),
            nativeSubs = listOf(nativeTrack(id = "1", title = "中文", language = "zh-CN")),
        )
        // 无 + 唯一一个「中文」(backend),native 被跳过。
        assertEquals(listOf("无", "中文"), labels(options))
        assertEquals(SubtitleType.BACKEND, options.last().type)
    }

    @Test
    fun `backend and native same language different label - native also skipped`() {
        // language 撞名也算覆盖 —— 防止「中文」(backend) + 「Chinese」(native,
        // language=zh-CN) 同时出现。
        val options = mergeSubtitleOptions(
            backendSubtitles = listOf(backendSub(id = 10, label = "中文")),
            nativeSubs = listOf(nativeTrack(id = "2", title = "Chinese", language = "zh-CN")),
        )
        assertEquals(listOf("无", "中文"), labels(options))
        assertEquals(SubtitleType.BACKEND, options.last().type)
    }

    @Test
    fun `no backend - native shown as fallback`() {
        // backend 缺失的剧集(无 Whisper 转录),native 内嵌轨保留。
        val options = mergeSubtitleOptions(
            backendSubtitles = emptyList(),
            nativeSubs = listOf(nativeTrack(id = "3", title = "中文", language = "zh-CN")),
        )
        assertEquals(listOf("无", "中文"), labels(options))
        assertEquals(SubtitleType.NATIVE, options.last().type)
    }

    @Test
    fun `backend multi-language plus native not clashing - all kept`() {
        val options = mergeSubtitleOptions(
            backendSubtitles = listOf(
                backendSub(id = 10, label = "中文", language = "zh-CN")
            ),
            nativeSubs = listOf(nativeTrack(id = "4", title = "English", language = "en")),
        )
        // 关闭 + 中文(backend) + English(native,不撞名)。
        assertEquals(listOf("无", "中文", "English"), labels(options))
    }

    @Test
    fun `no subtitles at all - only off`() {
        val options = mergeSubtitleOptions(
            backendSubtitles = emptyList(),
            nativeSubs = emptyList(),
        )
        assertEquals(listOf("无"), labels(options))
        assertEquals(SubtitleType.OFF, options.single().type)
    }

    @Test
    fun `no proofread suffix appears`() {
        // 回归保护:旧逻辑会给重名 backend 加「(校对版)」后缀,新逻辑不该再有。
        val options = mergeSubtitleOptions(
            backendSubtitles = listOf(backendSub(id = 11, label = "中文")),
            nativeSubs = listOf(nativeTrack(id = "5", title = "中文", language = "zh-CN")),
        )
        val hasProofreadSuffix = options.any { it.label.contains("校对") }
        assertFalse(hasProofreadSuffix)
    }

    @Test
    fun `native title null - fall back to language as label`() {
        // 内嵌轨有时 title 为空,应回退到 language 而不是崩。
        val options = mergeSubtitleOptions(
            backendSubtitles = emptyList(),
            nativeSubs = listOf(nativeTrack(id = "6", title = null, language = "ja")),
        )
        assertEquals(listOf("无", "ja"), labels(options))
    }

    // ---- 补充用例(Kotlin 端额外覆盖 native label 三级兜底 + option 字段) ----

    @Test
    fun `native label falls back to placeholder when title and language null`() {
        val options = mergeSubtitleOptions(
            backendSubtitles = emptyList(),
            nativeSubs = listOf(nativeTrack(id = "7", title = null, language = null)),
        )
        assertEquals(listOf("无", "内置字幕 7"), labels(options))
    }

    @Test
    fun `first option is always off with null backend`() {
        val options = mergeSubtitleOptions(
            backendSubtitles = listOf(backendSub(id = 1)),
            nativeSubs = emptyList(),
        )
        val first = options.first()
        assertEquals("无", first.label)
        assertEquals(SubtitleType.OFF, first.type)
        assertNull(first.backend)
        assertNull(first.nativeTrackId)
    }

    @Test
    fun `backend option carries dto and native option carries track id`() {
        val sub = backendSub(id = 42, label = "中文", language = "zh-CN")
        val track = nativeTrack(id = "9", title = "English", language = "en")
        val options = mergeSubtitleOptions(
            backendSubtitles = listOf(sub),
            nativeSubs = listOf(track),
        )

        val backendOpt = options[1]
        assertEquals(SubtitleType.BACKEND, backendOpt.type)
        assertSame(sub, backendOpt.backend)
        assertNull(backendOpt.nativeTrackId)

        val nativeOpt = options[2]
        assertEquals(SubtitleType.NATIVE, nativeOpt.type)
        assertEquals("9", nativeOpt.nativeTrackId)
        assertNull(nativeOpt.backend)
    }

    @Test
    fun `duplicate backend label - second backend skipped`() {
        val options = mergeSubtitleOptions(
            backendSubtitles = listOf(
                backendSub(id = 1, label = "中文", language = "zh-CN"),
                backendSub(id = 2, label = "中文", language = "en"), // 同 label,跳过
            ),
            nativeSubs = emptyList(),
        )
        assertEquals(listOf("无", "中文"), labels(options))
    }

    // ---- audioOptions ------------------------------------------------------

    @Test
    fun `audio filters out no and auto placeholder ids`() {
        val options = audioOptions(
            listOf(
                NativeAudioTrack(id = "no", title = "无", language = null),
                NativeAudioTrack(id = "auto", title = "自动", language = null),
                NativeAudioTrack(id = "1", title = "国语", language = "zh"),
                NativeAudioTrack(id = "2", title = null, language = "en"),
            )
        )
        assertEquals(listOf("国语", "en"), options.map { it.label })
    }

    @Test
    fun `audio label falls back to placeholder when title and language null`() {
        val options = audioOptions(
            listOf(NativeAudioTrack(id = "3", title = null, language = null))
        )
        assertEquals(listOf("音轨 3"), options.map { it.label })
    }

    @Test
    fun `audio empty input - empty output`() {
        assertTrue(audioOptions(emptyList()).isEmpty())
    }

    @Test
    fun `audio option carries original track`() {
        val track = NativeAudioTrack(id = "1", title = "国语", language = "zh")
        val options = audioOptions(listOf(track))
        assertSame(track, options.single().track)
    }

    // ---- defaultSubtitleIndex (默认字幕自动选择,对照 PAD _autoSelectDefaultSubtitle) ----

    @Test
    fun `default subtitle prefers backend when present`() {
        // 用户要求"有字幕默认打开中文字幕":有 backend(中文)时默认选它。
        val options = mergeSubtitleOptions(
            backendSubtitles = listOf(backendSub(id = 1, label = "中文")),
            nativeSubs = listOf(nativeTrack(id = "2", title = "English", language = "en")),
        )
        // [无, 中文(backend), English(native)] → 默认选 index 1(backend 优先)。
        assertEquals(1, defaultSubtitleIndex(options))
        assertEquals(SubtitleType.BACKEND, options[defaultSubtitleIndex(options)].type)
    }

    @Test
    fun `default subtitle falls back to native when no backend`() {
        val options = mergeSubtitleOptions(
            backendSubtitles = emptyList(),
            nativeSubs = listOf(nativeTrack(id = "1", title = "中文", language = "zh-CN")),
        )
        // [无, 中文(native)] → 默认选 index 1。
        assertEquals(1, defaultSubtitleIndex(options))
        assertEquals(SubtitleType.NATIVE, options[defaultSubtitleIndex(options)].type)
    }

    @Test
    fun `default subtitle returns zero when only off`() {
        // 无任何字幕 → 默认保持关闭(index 0 = 「无」)。
        val options = mergeSubtitleOptions(
            backendSubtitles = emptyList(),
            nativeSubs = emptyList(),
        )
        assertEquals(0, defaultSubtitleIndex(options))
        assertEquals(SubtitleType.OFF, options[0].type)
    }
}
