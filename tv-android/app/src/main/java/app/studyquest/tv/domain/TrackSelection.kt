package app.studyquest.tv.domain

import app.studyquest.tv.data.remote.dto.EpisodeSubtitleDto

// ---------------------------------------------------------------------------
// Track abstractions (decoupled from ExoPlayer / media3)
// ---------------------------------------------------------------------------

/**
 * 播放器从视频容器(MKV/MP4)抽出的内嵌字幕轨的最小抽象。
 *
 * 真正的来源是 ExoPlayer 的 `tracks.text`(对应 Flutter 端 media_kit 的
 * `player.state.tracks.subtitle`)。播放器层负责把 ExoPlayer 的 Track 映射成这个
 * 类型,domain 层不直接依赖 media3 —— 方便单测、也方便日后换播放器。
 */
data class NativeSubtitleTrack(
    val id: String,
    val title: String?,
    val language: String?,
)

/**
 * 播放器抽出的音轨的最小抽象。语义同 [NativeSubtitleTrack],只是轨道类型不同。
 */
data class NativeAudioTrack(
    val id: String,
    val title: String?,
    val language: String?,
)

// ---------------------------------------------------------------------------
// Output option types
// ---------------------------------------------------------------------------

/**
 * 字幕来源枚举。对应契约第 1 节:OFF(关闭)/ BACKEND(LLM 校对版,优先)/
 * NATIVE(容器内嵌轨,兜底)。
 */
enum class SubtitleType { OFF, BACKEND, NATIVE }

/**
 * 字幕菜单里的一个可选项。`backend` / `nativeTrackId` 二选一(看 [type]),
 * 播放器层根据这个 type 决定是去加载 backend 的 VTT URL 还是切到 ExoPlayer 的轨。
 */
data class SubtitleOption(
    val label: String,
    val type: SubtitleType,
    val backend: EpisodeSubtitleDto? = null,
    val nativeTrackId: Any? = null,
)

/**
 * 音轨菜单里的一个可选项。
 */
data class AudioOption(
    val label: String,
    val track: NativeAudioTrack,
)

// ---------------------------------------------------------------------------
// Pure business rules
// ---------------------------------------------------------------------------

/**
 * 字幕合并的纯逻辑(无 ExoPlayer / media3 依赖,可单测)。
 *
 * 策略(契约 business-rules.md 第 1 节,语义对齐 Flutter
 * `track_selection_controller.dart#mergeSubtitleOptions` 行 48):
 *
 * 1. 菜单第一项永远是「无」(关闭字幕)。
 * 2. backend 字幕优先:逐条用其原始 label 展示,同时登记 label + language。
 *    (backend 是 Whisper 转录 + LLM 校对过的,质量更高)
 * 3. native 字幕兜底:逐条检查,若其 label 或 language 跟某个 backend 字幕重复 →
 *    跳过;否则展示。这避免「中文」+「中文(校对版)」双按钮、以及点 native 那个
 *    被同语言的 backend 覆盖导致「点了没字幕」的索引错位 bug。
 * 4. native 的 label 取值优先级:`title` → `language` → `"内置字幕 ${id}"`。
 *
 * @param backendSubtitles 后端 play-info 返回的优质字幕(已校对)
 * @param nativeSubs       播放器从容器抽出的内嵌轨(由播放器层映射成 [NativeSubtitleTrack])
 */
fun mergeSubtitleOptions(
    backendSubtitles: List<EpisodeSubtitleDto>,
    nativeSubs: List<NativeSubtitleTrack>,
): List<SubtitleOption> {
    val list = mutableListOf<SubtitleOption>()
    val seenLabels = mutableSetOf<String>()
    val seenLanguages = mutableSetOf<String>()

    // 第一项:关闭字幕。
    list.add(SubtitleOption(label = "无", type = SubtitleType.OFF))
    seenLabels.add("无")

    // 1) Backend 优先:全展示,登记 label + language 供 native 去重。
    //    (backend 字幕内部若 label 重复也跳过 —— 对齐 Flutter `seenLabels.add(...)`
    //     返回 false 时 continue 的语义。)
    for (sub in backendSubtitles) {
        if (!seenLabels.add(sub.label)) continue
        seenLanguages.add(sub.language)
        list.add(
            SubtitleOption(
                label = sub.label,
                type = SubtitleType.BACKEND,
                backend = sub,
            )
        )
    }

    // 2) Native 兜底:label 或 language 跟 backend 撞名就跳过。
    for (track in nativeSubs) {
        val label = track.title ?: track.language ?: "内置字幕 ${track.id}"
        val clashByLabel = label in seenLabels
        val clashByLang =
            track.language != null && track.language in seenLanguages
        if (clashByLabel || clashByLang) continue
        seenLabels.add(label)
        list.add(
            SubtitleOption(
                label = label,
                type = SubtitleType.NATIVE,
                nativeTrackId = track.id,
            )
        )
    }

    return list
}

/**
 * 音轨菜单的纯逻辑(契约第 2 节,对齐 Flutter `audioOptions` 行 90)。
 *
 * 1. 过滤掉 `id == "no"` 和 `id == "auto"`(media 容器的"无/自动"占位轨)。
 * 2. 剩下的逐条展示,label 取值优先级:`title` → `language` → `"音轨 ${id}"`。
 *
 * 注意:契约第 2.3 条「音轨数 > 1 才显示菜单」是 UI 层的决策,这里只管生成列表,
 * 是否显示由调用方根据 `result.size > 1` 判断(对齐 Flutter 端 audioOptions 也只
 * 返回列表、是否显示在 UI 层判定)。
 *
 * @param tracks 播放器抽出的全部音轨
 */
fun audioOptions(tracks: List<NativeAudioTrack>): List<AudioOption> {
    val list = mutableListOf<AudioOption>()
    for (track in tracks) {
        if (track.id == "no" || track.id == "auto") continue
        val label = track.title ?: track.language ?: "音轨 ${track.id}"
        list.add(AudioOption(label = label, track = track))
    }
    return list
}

/**
 * 选默认字幕的 index(对照 PAD `_autoSelectDefaultSubtitle` 行 1241)。
 *
 * 规则(契约补充,business-rules.md 第 1 节字幕合并的延伸):
 *   1. 优先级 backend(LLM polish 优质字幕)> native(容器内嵌兜底)。
 *      `mergeSubtitleOptions` 已按 backend→native 顺序追加,所以从 index 1 开始
 *      找第一条非 OFF 的即是最高优先级的可用字幕。
 *   2. 没有任何字幕(只有「无」)→ 返回 0(保持关闭)。
 *
 * 用户要求"有字幕默认打开中文字幕":backend 字幕通常是 zh-CN(Whisper 转录的中文
 * 内容),选第一条 backend/native 即满足。这里不强制匹配 language=zh —— 若有多语言
 * 字幕,默认选第一条(通常是主语言);用户可手动切。
 *
 * @param options [mergeSubtitleOptions] 的输出(首项是「无」)
 * @return 默认选中的 index(0 = 关闭,>0 = 某条字幕);无字幕时返回 0
 */
fun defaultSubtitleIndex(options: List<SubtitleOption>): Int {
    // 从 1 开始(跳过「无」),找第一条非 OFF 的。mergeSubtitleOptions 已按优先级排序。
    for (i in 1 until options.size) {
        if (options[i].type != SubtitleType.OFF) return i
    }
    return 0
}
