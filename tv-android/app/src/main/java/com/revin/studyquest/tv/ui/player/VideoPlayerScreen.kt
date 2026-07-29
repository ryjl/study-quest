package com.revin.studyquest.tv.ui.player

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.FormatSize
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material.icons.filled.Subtitles
import androidx.compose.material.icons.filled.VolumeUp
import com.revin.studyquest.tv.player.NetdiskHttpFactory
import com.revin.studyquest.tv.player.ProgressReporter
import com.revin.studyquest.tv.player.ResumeWatchdog
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate400
import com.revin.studyquest.tv.ui.theme.slate900
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/**
 * 播放器主屏 —— 阶段 3 核心。
 *
 * 照搬 JetStream `VideoPlayerScreen` 结构,适配我们的网盘场景:
 *   1. 拉 play-info(url + headers + resumePosition + subtitles)
 *   2. 建 ExoPlayer,用 NetdiskHttpFactory 注入网盘 Referer 头(否则 403)
 *   3. 断点续播:resumePosition > 5s 时 seekTo + ResumeWatchdog 监听 CDN 回零
 *   4. ProgressReporter 定时上报(5s tick,防作弊)
 *   5. 控制层 + seek bar,D-pad 导航
 *
 * D-pad 事件(对照 JetStream dPadEvents):
 *   - 控件隐藏时 ◄► = seek ±10s,▲▼/Enter = 唤出控件
 *   - 控件显示时,D-pad 在控件间导航(由 focus system 处理)
 */
@androidx.annotation.OptIn(UnstableApi::class)
@Composable
fun VideoPlayerScreen(
    episodeId: Int,
    onBack: () -> Unit,
    viewModel: PlayerScreenViewModel = hiltViewModel(),
) {
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()
    val baseUrl by viewModel.baseUrl.collectAsStateWithLifecycle()
    val subtitleSizeIndex by viewModel.subtitleSizeIndex.collectAsStateWithLifecycle()
    val context = LocalContext.current

    // 进入屏幕拉 play-info(对照 PAD _initializeVideo 的 fetchPlayInfo)
    LaunchedEffect(episodeId) {
        viewModel.loadPlayInfo(episodeId)
    }

    when (val s = uiState) {
        is PlayerUiState.Loading -> Box(Modifier.fillMaxSize().background(slate900), Alignment.Center) {
            androidx.tv.material3.Text("加载中...", color = Color.White)
        }
        is PlayerUiState.Error -> Box(Modifier.fillMaxSize().background(slate900), Alignment.Center) {
            androidx.tv.material3.Text("加载失败: ${s.message}", color = Color.Red)
        }
        is PlayerUiState.Ready -> VideoPlayerContent(
            playInfo = s.playInfo,
            episodeId = episodeId,
            context = context,
            baseUrl = baseUrl,
            subtitleSizeIndex = subtitleSizeIndex,
            onSubtitleSizeChange = viewModel::setSubtitleSizeIndex,
            onBack = onBack,
            viewModel = viewModel,
        )
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun VideoPlayerContent(
    playInfo: com.revin.studyquest.tv.data.remote.dto.PlayInfoDto,
    episodeId: Int,
    context: Context,
    baseUrl: String?,
    subtitleSizeIndex: Int,
    onSubtitleSizeChange: (Int) -> Unit,
    onBack: () -> Unit,
    viewModel: PlayerScreenViewModel,
) {
    val netdiskFactory = viewModel.netdiskHttpFactory
    val apiService = viewModel.apiService
    val playerState = rememberVideoPlayerState(hideSeconds = 4)
    // Composable 生命周期 scope:页面退出自动取消(上报协程随之停)。
    // 不能用 viewModelScope(它是 ViewModel protected 属性,Composable 访问不到)。
    val coroutineScope = rememberCoroutineScope()

    // 1. 建 ExoPlayer(用 NetdiskHttpFactory 注入网盘头)。
    // remember(playInfo.url):playInfo 变化时(理论上不会,但防御)重建。
    //
    // **关键**:必须用 setMediaSource(factory.createMediaSource(mediaItem)) 而非
    // setMediaItem(mediaItem)。后者走 ExoPlayer 默认的 DefaultHttpDataSource.Factory,
    // 不带网盘鉴权头 + 不跟随跨 host 的 302 重定向(AList 代理 url 302 跳到云盘 CDN
    // 直链是跨 host),实测报 HttpDataSource$InvalidResponseCodeException: 302。
    // setMediaSource 强制用我们注入了 OkHttpDataSource.Factory 的 mediaSourceFactory,
    // OkHttp 默认跟随重定向 + 带自定义默认请求头。
    // DefaultTrackSelector:给字幕/音轨菜单切轨用(禁用 text track / 切 audio track)。
    // 单独 remember(跟 exoPlayer 同 url key 重建),避免用扩展属性存。
    val trackSelector = remember(playInfo.url) {
        // **多音轨容错**:多音轨视频(尤其带 5.1/7.1 多声道音频轨)在某些设备/
        // 模拟器上,ExoPlayer 的 audio renderer 可能因不支持该声道布局而失败,
        // 进而导致整个播放卡住(视频也不出)。限制最大声道数为立体声(2),
        // 让 ExoPlayer 优先选/降混到支持的音频格式。
        //
        // 对照 PAD libmpv(`audio-channels=stereo` 类似语义)—— mpv 自带软解兜底,
        // ExoPlayer 依赖平台 MediaCodec,需要更显式的约束。
        val params = androidx.media3.exoplayer.trackselection.DefaultTrackSelector
            .Parameters.Builder()
            .setMaxAudioChannelCount(2)
            .build()
        androidx.media3.exoplayer.trackselection.DefaultTrackSelector(context).apply {
            setParameters(parameters)
        }
    }
    val exoPlayer = remember(playInfo.url) {
        ExoPlayer.Builder(context)
            .setTrackSelector(trackSelector)
            // **缓存控制**(对照 PAD mpv `cache-secs=60`,business-rules 没硬性约定,
            // 但实测反馈默认前向缓存太大浪费流量):
            //   - minBufferMs=1500s(1.5s) → 起播快
            //   - maxBufferMs=60000(60s) → 向前最多缓存 1 分钟(按主流码率 ~1Mbps
            //     约 7.5MB,够吸收 CDN 抖动又不至于半部电影都拉下来)
            //   - prioritizeTimeOverSizeThresholds(true) → 用时间约束而非字节约束,
            //     让 maxBufferMs 真正生效
            // 中途退出播放,已下载的 60s 内数据就丢掉,不继续拉。
            .setLoadControl(
                androidx.media3.exoplayer.DefaultLoadControl.Builder()
                    .setBufferDurationsMs(
                        /* minBufferMs= */ 1_500,
                        /* maxBufferMs= */ 60_000,
                        /* playbackBufferMs= */ 1_000,
                        /* rebufferMs= */ 1_000,
                    )
                    .setPrioritizeTimeOverSizeThresholds(true)
                    .build(),
            )
            .build().also { p ->
            // 网盘头注入:OkHttpDataSource.Factory 设默认请求头(Referer 等)
            val dataSourceFactory = netdiskFactory.create(playInfo.headers)
            val mediaSourceFactory = DefaultMediaSourceFactory(context)
                .setDataSourceFactory(dataSourceFactory)

            // MediaItem(含字幕轨配置,WebVTT)。backend 字幕作为 side-loaded 轨,
            // 菜单选中时通过 trackSelector 启用对应轨。
            //
            // **关键修复**:backend 字幕 url 是**相对路径**(`/api/v1/subtitles/x.vtt`),
            // ExoPlayer 加载 side-loaded VTT 必须绝对 URL。原实现直接 Uri.parse(sub.url)
            // 会拿到相对 URI,ExoPlayer 加载失败(business-rules.md 第 1 节「backend 字幕
            // URL」明确这点)。用 [UrlResolver.absolute] 拼 baseUrl + sub.url。
            val subtitleConfigs = playInfo.subtitles.map { sub ->
                val absoluteSubUrl = com.revin.studyquest.tv.data.repo.UrlResolver.absolute(
                    baseUrl, sub.url,
                )
                MediaItem.SubtitleConfiguration.Builder(android.net.Uri.parse(absoluteSubUrl))
                    .setMimeType(androidx.media3.common.MimeTypes.TEXT_VTT)
                    .setLanguage(sub.language)
                    .setLabel(sub.label)
                    .setId("backend-${sub.id}")
                    .build()
            }
            val mediaItem = MediaItem.Builder()
                .setUri(playInfo.url)
                .setSubtitleConfigurations(subtitleConfigs)
                .build()

            // 用 mediaSourceFactory 创建 MediaSource,确保走 OkHttpDataSource
            // (跟随 302 + 网盘头),而非 ExoPlayer 默认的 DefaultHttpDataSource。
            val mediaSource = mediaSourceFactory.createMediaSource(mediaItem)
            p.setMediaSource(mediaSource)
            p.prepare()
            // **自动播放**:prepare 后必须显式 play(),否则视频加载了但停在第一帧。
            p.play()
        }
    }

    // 2. 断点续播 + 进度上报(对照 business-rules.md 第 4、5 节)
    val resumeSeconds = playInfo.progress?.lastPositionSeconds ?: 0
    val progressReporter = remember(exoPlayer) {
        ProgressReporter(apiService, coroutineScope)
    }

    LaunchedEffect(exoPlayer) {
        // 等播放器 ready 再 seek + 启动上报
        val listener = object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_READY && resumeSeconds > 5) {
                    val resumeMs = resumeSeconds * 1000L
                    exoPlayer.seekTo(resumeMs)
                    // 挂 watchdog 监听 CDN 回零
                    val watchdog = ResumeWatchdog(resumeMs).apply { player = exoPlayer }
                    exoPlayer.addListener(watchdog)
                    progressReporter.start(episodeId, exoPlayer, resumeSeconds)
                    exoPlayer.removeListener(this) // 一次性
                }
            }
        }
        exoPlayer.addListener(listener)
        // 若无断点(resumeSeconds<=5),直接启动上报
        if (resumeSeconds <= 5) {
            progressReporter.start(episodeId, exoPlayer, 0)
        }
    }

    // 3. 退出时释放资源(ExoPlayer + 上报)
    DisposableEffect(exoPlayer) {
        onDispose {
            progressReporter.stop()
            exoPlayer.release()
        }
    }

    // 3.1 播放错误捕获(诊断"放不了"类问题)。
    //
    // ExoPlayer 的解码/拉流错误(HEVC 不支持、多声道音频 renderer 失败、CDN 断流)
    // 默认走 `onPlayerError`,原来不接 → 用户只看到黑屏/卡住,不知道为啥。
    // 这里挂 listener 把错误转成 Compose state,UI 顶层覆盖一个**友好**错误提示
    // (中文文案 + 针对性建议)。原始 exception 也 logcat 打一份,方便 mumu 抓日志
    // 定位根因。
    //
    // 关键场景:HEVC HDR 10-bit 视频(如 `X265 ... HDR`)在 mumu 模拟器上解不了 ——
    // mumu 是 goldfish codec,只有 H.264 硬解,HEVC HDR 走 `OMX.qcom.video.decoder.hevc`
    // 会报 `NO_EXCEEDS_CAPABILITIES`(硬解不支持 HDR/10-bit)。真 Android TV 设备
    // 大多支持 HEVC HDR 硬解,生产环境正常。PAD 端靠 libmpv 软解兜底所以能放。
    var playbackError by remember {
        mutableStateOf<androidx.media3.common.PlaybackException?>(null)
    }
    LaunchedEffect(exoPlayer) {
        exoPlayer.addListener(object : Player.Listener {
            override fun onPlayerErrorChanged(error: androidx.media3.common.PlaybackException?) {
                playbackError = error
                if (error != null) {
                    // 关键诊断信息:errorCode + 错误类型 + cause。
                    android.util.Log.e(
                        "TVPlayer",
                        "onPlayerError: code=${error.errorCode} type=${error.javaClass.simpleName}",
                        error,
                    )
                }
            }
        })
    }

    // 3.5 收集容器内嵌的字幕/音轨 + 合并字幕选项(对照 business-rules.md 第 1、2 节)。
    //
    // onTracksChanged 在轨道信息可用时触发(MKV/MP4 解析后)。我们把 ExoPlayer 的
    // TrackGroup 映射成 domain 的 NativeSubtitleTrack/NativeAudioTrack,再调
    // mergeSubtitleOptions(backend + native)/ audioOptions(native)生成菜单选项。
    //
    // **修复"没显示音轨按钮 + 多音轨不播放"**:
    //   ① 旧实现 `if (!group.isSelected) continue` 只遍历**当前选中**的 group。多音轨
    //     时 trackSelector 只选了一个 group(默认选首个 audio group),其余 group
    //     `isSelected=false` → 全被跳过 → 菜单只有 1 条 → 不显示音轨按钮。
    //     正确做法:遍历**所有** group,把容器里**所有** text/audio 轨都收集出来。
    //   ② 顺手用 `TrackSelectionOverride` 做精确切轨(替代之前按 language 近似匹配,
    //     后者对"同语言多音轨"会切错)。
    //
    // 保存 native 轨的 (TrackGroup, trackIndex) 引用,给 applySubtitle/applyAudio 做
    // 精确 override 用。key = NativeSubtitleTrack.id / NativeAudioTrack.id(format.id)。
    var subtitleOptions by remember {
        mutableStateOf(
            com.revin.studyquest.tv.domain.mergeSubtitleOptions(
                playInfo.subtitles, emptyList(),
            ),
        )
    }
    var audioOptions by remember { mutableStateOf(emptyList<com.revin.studyquest.tv.domain.AudioOption>()) }
    // 当前选中的字幕索引(0 = 「无」/ 关闭)。
    var selectedSubtitleIndex by remember { mutableStateOf(0) }
    // 是否已自动选过默认字幕(只选一次,避免用户手动关闭后被 onTracksChanged 又打开)。
    var subtitleAutoSelected by remember { mutableStateOf(false) }
    // 待自动选默认字幕的 index(>0 表示需要选;listener 标记,LaunchedEffect 消费)。
    // 用这个间接层是因为 onTracksChanged 的 listener 定义在 applySubtitle 之前,
    // Kotlin 局部 fun 不能前向引用,所以 listener 不能直接调 applySubtitle。
    var pendingAutoSelectSubtitle by remember { mutableStateOf<Int?>(null) }
    var currentSpeed by remember { mutableStateOf(1.0f) }

    // UI 侧维护的"轨 id → (TrackGroup, format index)"映射,精确切轨用。
    // 注意:不能进 domain(domain 不依赖 media3),这里跟 subtitleOptions/audioOptions
    // 一起在 onTracksChanged 时同步刷新。
    var nativeTextOverrides by remember {
        mutableStateOf<Map<String, androidx.media3.common.TrackSelectionOverride>>(emptyMap())
    }
    var nativeAudioOverrides by remember {
        mutableStateOf<Map<String, androidx.media3.common.TrackSelectionOverride>>(emptyMap())
    }

    LaunchedEffect(exoPlayer) {
        val tracksListener = object : Player.Listener {
            override fun onTracksChanged(tracks: androidx.media3.common.Tracks) {
                val nativeSubs = mutableListOf<com.revin.studyquest.tv.domain.NativeSubtitleTrack>()
                val nativeAudios = mutableListOf<com.revin.studyquest.tv.domain.NativeAudioTrack>()
                val textOverrides = mutableMapOf<String, androidx.media3.common.TrackSelectionOverride>()
                val audioOverrides = mutableMapOf<String, androidx.media3.common.TrackSelectionOverride>()
                // **关键**:遍历所有 groups(不只 selected 的)。
                for (group in tracks.groups) {
                    val type = group.type
                    val trackGroup = group.mediaTrackGroup
                    if (trackGroup.length == 0) continue
                    for (i in 0 until trackGroup.length) {
                        val format = trackGroup.getFormat(i)
                        val id = format.id ?: i.toString()
                        when (type) {
                            androidx.media3.common.C.TRACK_TYPE_TEXT -> {
                                // 容器内嵌字幕轨(不包含 backend side-loaded —— 那些是
                                // MediaItem.SubtitleConfiguration 注入的,这里也能拿到,
                                // 但 mergeSubtitleOptions 会用 label/language 去重,不会重复)。
                                nativeSubs.add(
                                    com.revin.studyquest.tv.domain.NativeSubtitleTrack(
                                        id = id,
                                        title = format.label,
                                        language = format.language,
                                    ),
                                )
                                textOverrides[id] = androidx.media3.common.TrackSelectionOverride(
                                    trackGroup, i,
                                )
                            }
                            androidx.media3.common.C.TRACK_TYPE_AUDIO -> {
                                nativeAudios.add(
                                    com.revin.studyquest.tv.domain.NativeAudioTrack(
                                        id = id,
                                        title = format.label,
                                        language = format.language,
                                    ),
                                )
                                audioOverrides[id] = androidx.media3.common.TrackSelectionOverride(
                                    trackGroup, i,
                                )
                            }
                        }
                    }
                }
                // 合并 backend + native 字幕(契约第 1 节)。
                val merged = com.revin.studyquest.tv.domain.mergeSubtitleOptions(
                    playInfo.subtitles, nativeSubs,
                )
                subtitleOptions = merged
                // 音轨(契约第 2 节:过滤 no/auto 占位)。
                audioOptions = com.revin.studyquest.tv.domain.audioOptions(nativeAudios)
                nativeTextOverrides = textOverrides
                nativeAudioOverrides = audioOverrides

                // **默认字幕自动选择**(对照 PAD `_autoSelectDefaultSubtitle`):
                // 只在首次拿到字幕轨时选一次(避免用户手动关了又被重选)。
                // 优先级:backend(LLM polish 优质字幕)> native(容器内嵌兜底)> off。
                // 用户要求"有字幕默认打开中文字幕"——backend 字幕通常是中文,
                // 选第一条 backend/native 即满足。
                //
                // 这里只标记 pendingAutoSelectSubtitle,实际 applySubtitle 在单独的
                // LaunchedEffect 里调(避免局部 fun 前向引用问题)。
                if (!subtitleAutoSelected && merged.size > 1) {
                    val defaultIdx = com.revin.studyquest.tv.domain.defaultSubtitleIndex(merged)
                    if (defaultIdx > 0) {
                        pendingAutoSelectSubtitle = defaultIdx
                    }
                    subtitleAutoSelected = true
                }
            }
        }
        exoPlayer.addListener(tracksListener)
    }

    /**
     * 应用字幕选择(对照 PAD `_applySubtitleOption`)。
     *
     * 三种分支:
     *   - OFF:禁用 text track。
     *   - BACKEND:启用 text + 按 language 选。side-loaded 字幕(SideLoadedConfiguration)
     *     和容器内嵌轨都受 trackSelector 控制;`setPreferredTextLanguage` 匹配轨的
     *     `format.language`。backend 字幕的 language 来自 EpisodeSubtitleDto。
     *   - NATIVE:启用 text + 用 [TrackSelectionOverride] 精确切到对应 format(替代
     *     按 language 匹配,后者对同语言多轨会切错)。
     */
    fun applySubtitle(index: Int) {
        selectedSubtitleIndex = index
        val opt = subtitleOptions.getOrNull(index) ?: return
        val params = trackSelector.parameters.buildUpon()
        when (opt.type) {
            com.revin.studyquest.tv.domain.SubtitleType.OFF -> {
                params.setTrackTypeDisabled(androidx.media3.common.C.TRACK_TYPE_TEXT, true)
            }
            com.revin.studyquest.tv.domain.SubtitleType.BACKEND -> {
                params.setTrackTypeDisabled(androidx.media3.common.C.TRACK_TYPE_TEXT, false)
                // 清掉之前的精确 override(否则会和 preferredTextLanguage 冲突)。
                params.clearOverridesOfType(androidx.media3.common.C.TRACK_TYPE_TEXT)
                opt.backend?.language?.let { lang ->
                    params.setPreferredTextLanguage(lang)
                }
            }
            com.revin.studyquest.tv.domain.SubtitleType.NATIVE -> {
                params.setTrackTypeDisabled(androidx.media3.common.C.TRACK_TYPE_TEXT, false)
                val trackId = opt.nativeTrackId as? String
                val override = trackId?.let { nativeTextOverrides[it] }
                if (override != null) {
                    // 精确切到这个 native 轨(覆盖 preferredTextLanguage 的模糊匹配)。
                    params.clearOverridesOfType(androidx.media3.common.C.TRACK_TYPE_TEXT)
                    params.setOverrideForType(override)
                } else {
                    // 兜底:没拿到 TrackGroup 引用就退化到 language 匹配。
                    params.clearOverridesOfType(androidx.media3.common.C.TRACK_TYPE_TEXT)
                }
            }
        }
        trackSelector.setParameters(params.build())
    }

    // 消费"待自动选默认字幕"标记(由 onTracksChanged listener 设置)。
    // 拆出来是因为 listener 定义在 applySubtitle 之前,不能直接调;用 LaunchedEffect
    // 响应 state 变化,此时 applySubtitle 已定义可调。消费后清空标记。
    LaunchedEffect(pendingAutoSelectSubtitle) {
        pendingAutoSelectSubtitle?.let { idx ->
            selectedSubtitleIndex = idx
            applySubtitle(idx)
            pendingAutoSelectSubtitle = null
        }
    }

    /** 应用音轨选择(对照 PAD `_applyAudioOption`)。用 [TrackSelectionOverride] 精确切。 */
    fun applyAudio(option: com.revin.studyquest.tv.domain.AudioOption) {
        val override = nativeAudioOverrides[option.track.id]
        val params = trackSelector.parameters.buildUpon()
        if (override != null) {
            params.clearOverridesOfType(androidx.media3.common.C.TRACK_TYPE_AUDIO)
            params.setOverrideForType(override)
            trackSelector.setParameters(params.build())
        } else {
            // 兜底:没拿到 TrackGroup 引用就退化到 language 匹配(对单语言多音轨无效,
            // 但比啥都不做强)。这种情况说明 onTracksChanged 还没触发或轨 id 不匹配。
            option.track.language?.let { lang ->
                trackSelector.setParameters(
                    params.setPreferredAudioLanguage(lang).build(),
                )
            }
        }
    }

    /** 应用速度(对照 PAD `_setRate`)。 */
    fun applySpeed(rate: Float) {
        currentSpeed = rate
        exoPlayer.setPlaybackSpeed(rate)
    }

    // 4. 播放器画面 + 控制层 overlay
    //
    // D-pad 焦点策略(修复"焦点移不动 + 左右不调进度"):
    //   - 外层 Box 不 focusable(否则吞焦点,控制层子项永远拿不到焦点)。
    //   - 控制层可见时:左右键放行(return false)→ 焦点系统把左右交给 seeker
    //     (seeker 内部 onPreviewKeyEvent 做 seek ±10s)+ 焦点在控制行按钮间移动。
    //   - 控制层隐藏时:左右键直接 seek(没有可见的 seeker 接收)+ 唤出控制层。
    //   - 上下键:控制层隐藏 → 唤出;可见 → 放行(让焦点在 seeker/按钮间移动)。
    //   - Enter:始终切换播放/暂停 + 唤出控制层。
    //   - Back:退出播放器。
    //
    // **长按 ◄► 加速 seek**:控制层隐藏时,这里也接一份"按住时长"状态机
    // (跟 VideoPlayerSeeker 内部那份**分开** —— 两份不会冲突:控制层显/隐是互斥态,
    // 同一时刻只会有一处响应 ◄►)。
    var seekHoldStartMs by remember { androidx.compose.runtime.mutableLongStateOf(0L) }
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black)
            .onPreviewKeyEvent { event ->
                // KeyUp 复位长按计时器(松手时,无论控制层显隐)。
                if (event.type == KeyEventType.KeyUp &&
                    (event.key == Key.DirectionLeft || event.key == Key.DirectionRight)
                ) {
                    seekHoldStartMs = 0L
                    return@onPreviewKeyEvent false
                }
                if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                when (event.key) {
                    Key.DirectionLeft, Key.DirectionRight -> {
                        if (playerState.isControlsVisible) {
                            // 控制层可见:放行给 seeker(它内部处理 seek)+ 焦点移动。
                            // 同时刷新控制层计时(用户在操作,不要隐藏)。
                            playerState.showControls(exoPlayer.isPlaying)
                            false
                        } else {
                            // 控制层隐藏:直接 seek + 唤出控制层显示进度。
                            // 长按加速(对照 VideoPlayerSeeker 内部):首次按下记起始,
                            // 重复按下按"按住时长"换档位。
                            val now = android.os.SystemClock.uptimeMillis()
                            if (seekHoldStartMs == 0L) seekHoldStartMs = now
                            val step = seekStepForHoldMs(now - seekHoldStartMs)
                            val cur = exoPlayer.currentPosition
                            exoPlayer.seekTo(
                                if (event.key == Key.DirectionLeft) (cur - step).coerceAtLeast(0)
                                else (cur + step).coerceAtMost(exoPlayer.duration.coerceAtLeast(0))
                            )
                            playerState.showControls(exoPlayer.isPlaying)
                            true
                        }
                    }
                    Key.DirectionUp, Key.DirectionDown -> {
                        if (playerState.isControlsVisible) {
                            // 控制层可见:放行 → 焦点在 seeker / 控制按钮间移动。
                            false
                        } else {
                            // 控制层隐藏:唤出。
                            playerState.showControls(exoPlayer.isPlaying)
                            true
                        }
                    }
                    Key.Enter, Key.DirectionCenter -> {
                        if (playerState.isControlsVisible) {
                            // 控制层可见:放行给有焦点的子项(seeker 或控制按钮)。
                            // 焦点在按钮上 → 触发按钮 onClick;焦点在 seeker 上 → 透传。
                            // 同时刷新控制层计时(用户在操作)。
                            playerState.showControls(exoPlayer.isPlaying)
                            false
                        } else {
                            // 控制层隐藏:Enter 切换播放/暂停 + 唤出控制层。
                            playerState.showControls(exoPlayer.isPlaying)
                            if (exoPlayer.isPlaying) exoPlayer.pause() else exoPlayer.play()
                            true
                        }
                    }
                    Key.Back -> {
                        onBack()
                        true
                    }
                    else -> false
                }
            }
            // focusable:控制层隐藏时(seeker/按钮不存在),Box 作为焦点锚点接收
            // D-pad 事件(左右 seek / Enter 暂停 / 唤出)。控制层可见时焦点会被
            // LaunchedEffect 转移到 seeker,事件走 preview 阶段先到 Box 再放行。
            .focusable(),
    ) {
        // 视频画面 —— 用 media3-ui-compose 的 PlayerSurface(纯 Compose,对照 JetStream)。
        // 比 AndroidView+PlayerView 更符合 Compose 范式;useController=false 等价于
        // 不用 ExoPlayer 自带控制层(我们用自己的 Compose 控制层)。
        androidx.media3.ui.compose.PlayerSurface(
            player = exoPlayer,
            surfaceType = androidx.media3.ui.compose.SURFACE_TYPE_TEXTURE_VIEW,
            modifier = Modifier.fillMaxSize(),
        )

        // **字幕渲染层**(修复"字幕选了不显示"):
        // `PlayerSurface` 只画视频画面,**不渲染字幕**。要把 ExoPlayer 解析出来的
        // text cues 真正画到屏幕上,需要 media3-ui 的 `SubtitleView`(classic View)。
        //
        // **media3 1.6 注意**:旧的 `SubtitleView.setPlayer(Player)` 已废(那是 1.x
        // 早期 PlayerView 子 view 集成模式)。现在 `SubtitleView` 是个纯渲染容器,
        // 需要外部喂 cues:`Player.Listener.onCues(CueGroup)` → `setCues(cueGroup.cues)`。
        // 对照 PAD `SubtitleView`(Flutter 端由 media_kit 内置渲染,无需手动接)。
        //
        // **字幕字号**:用 [subtitleSizeDp] 把档位 index 换 dp 值(business-rules.md
        // 第 6 节,4 档 18/24/30/38dp),`setFixedTextSize(COMPLEX_UNIT_SP, dp)`。
        // 用 SP 不用 fractional:PAD 端是绝对 dp,TV 也用绝对值对齐跨端视觉;档位变时
        // update 块重设字号(Compose 范式:state 变 → 重组 → update → view 刷)。
        //
        // 布局:贴底部、留底部 padding 避开控制层;subtitleView 自己按 cue 的 line
        // positioning 绘制,默认底对齐。
        var subtitleCues by remember { mutableStateOf(emptyList<androidx.media3.common.text.Cue>()) }
        LaunchedEffect(exoPlayer) {
            exoPlayer.addListener(object : Player.Listener {
                override fun onCues(cues: androidx.media3.common.text.CueGroup) {
                    subtitleCues = cues.cues
                }
            })
        }
        val subtitleSizeDp = com.revin.studyquest.tv.domain.subtitleSizeDp(subtitleSizeIndex)
        androidx.compose.ui.viewinterop.AndroidView(
            modifier = Modifier.fillMaxSize(),
            factory = { ctx ->
                androidx.media3.ui.SubtitleView(ctx).apply {
                    // 字幕样式(白字黑描边,对照 design-tokens 字号中档 24dp)。
                    setFixedTextSize(
                        android.util.TypedValue.COMPLEX_UNIT_SP,
                        subtitleSizeDp,
                    )
                    // 注:applyEmbeddedFontSizes=false —— 让我们设的固定字号生效,
                    // 而不是被 VTT cue 自带的 size 覆盖(backend 字幕是 Whisper 轸转,
                    // cue 不带 size,但容器内嵌轨可能带,统一用我们的档位)。
                    setApplyEmbeddedFontSizes(false)
                    setApplyEmbeddedStyles(true)
                    // CaptionStyleCompat 在 media3 里是普通 data class(没 Builder),
                    // 直接构造:fg / bg / window / edgeType / edgeColor / typeface。
                    setStyle(
                        androidx.media3.ui.CaptionStyleCompat(
                            /* foregroundColor = */ android.graphics.Color.WHITE,
                            /* backgroundColor = */ android.graphics.Color.argb(0xB0, 0, 0, 0),
                            /* windowColor = */ android.graphics.Color.TRANSPARENT,
                            /* edgeType = */ androidx.media3.ui.CaptionStyleCompat.EDGE_TYPE_OUTLINE,
                            /* edgeColor = */ android.graphics.Color.BLACK,
                            /* typeface = */ android.graphics.Typeface.SANS_SERIF,
                        ),
                    )
                    // 控制层显示时上浮(避免被 seek bar + 控制行盖住):
                    //   控制层可见 → 底部留白 22%(约控制层高度),字幕上移到控制层上方
                    //   控制层隐藏 → 底部留白 8%(贴近底部,对照主流播放器)
                    // 在 update 块里根据 isControlsVisible 动态设,控制层显隐时实时刷新。
                    setBottomPaddingFraction(SUBTITLE_BOTTOM_PADDING_HIDDEN)
                }
            },
            // update:档位变(subtitleSizeDp 变)/ cues 变 / 控制层显隐时刷 view。
            update = { view ->
                view.setFixedTextSize(
                    android.util.TypedValue.COMPLEX_UNIT_SP,
                    subtitleSizeDp,
                )
                // 控制层显示时字幕上浮(避免被覆盖)。isControlsVisible 变化触发重组 →
                // update 块重跑 → 重设 bottomPaddingFraction(实时生效)。
                view.setBottomPaddingFraction(
                    if (playerState.isControlsVisible) SUBTITLE_BOTTOM_PADDING_WHEN_CONTROLS_VISIBLE
                    else SUBTITLE_BOTTOM_PADDING_HIDDEN
                )
                view.setCues(subtitleCues)
            },
        )

        // 控制层 overlay(可见时显示)。
        // 布局:渐变遮罩从底部往上淡出(不全屏盖死,视频画面上半部保持可见),
        // seek bar + 控制行贴底部排列。
        //
        // 焦点:控制层显示时,自动把焦点请求到 seeker(它 focusable + 能处理左右 seek)。
        // 这样外层 Box 的 onPreviewKeyEvent 放行左右/上下后,事件落到有焦点的 seeker
        // 上,seeker 内部做 seek,上下能移到控制按钮。
        val seekerFocusRequester = remember { FocusRequester() }
        LaunchedEffect(playerState.isControlsVisible) {
            if (playerState.isControlsVisible) {
                withFrameNanos { }
                runCatching { seekerFocusRequester.requestFocus() }
            }
        }
        if (playerState.isControlsVisible) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    // 底部渐变遮罩(对照腾讯/网易 TV:不全屏黑,底部渐变让控件清晰)。
                    .background(
                        brush = Brush.verticalGradient(
                            0.5f to Color.Transparent,
                            1f to Color.Black.copy(alpha = 0.7f),
                        ),
                    ),
            ) {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .align(Alignment.BottomCenter)
                        .padding(bottom = 24.dp),
                ) {
                    // seek bar(贴底部,不 fillMaxSize)。focusRequester 让控制层
                    // 显示时焦点自动落这里。
                    VideoPlayerSeeker(
                        player = exoPlayer,
                        modifier = Modifier
                            .fillMaxWidth()
                            .focusRequester(seekerFocusRequester),
                    )
                    // 控制行:播放/暂停 + 速度 + 字幕 + 音轨(条件)。
                    // 直接 inline Row(混普通按钮 PlayerControlButton + 菜单按钮 PlayerMenuButton)。
                    Row(
                        modifier = Modifier.padding(horizontal = 24.dp, vertical = 12.dp),
                        horizontalArrangement = Arrangement.spacedBy(16.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        // 播放/暂停(图标随状态切换)。
                        PlayPauseButton(exoPlayer = exoPlayer, playerState = playerState)
                        // 速度菜单(6 档)。
                        PlayerMenuButton(
                            icon = Icons.Filled.Speed,
                            label = "速度",
                            options = listOf(0.5f, 0.75f, 1.0f, 1.25f, 1.5f, 2.0f).map {
                                PlayerMenuOption(label = "${it}x", selected = it == currentSpeed)
                            },
                            onSelect = { idx ->
                                applySpeed(listOf(0.5f, 0.75f, 1.0f, 1.25f, 1.5f, 2.0f)[idx])
                            },
                        )
                        // 字幕菜单(合并 backend + native)。
                        PlayerMenuButton(
                            icon = Icons.Filled.Subtitles,
                            label = "字幕",
                            options = subtitleOptions.mapIndexed { idx, opt ->
                                PlayerMenuOption(label = opt.label, selected = idx == selectedSubtitleIndex)
                            },
                            onSelect = { idx -> applySubtitle(idx) },
                        )
                        // 字幕大小菜单(4 档,对照 business-rules.md 第 6 节 + PAD
                        // `_subtitleSizes`)。档位表统一从 domain 取(`SubtitleSize.kt`),
                        // 和设置页共用同一份。改档位 → VM 落盘 → StateFlow 触发重组 →
                        // SubtitleView update 块重设字号(实时生效)。
                        PlayerMenuButton(
                            icon = Icons.Filled.FormatSize,
                            label = "字幕大小",
                            options = com.revin.studyquest.tv.domain.SUBTITLE_SIZE_LABELS.mapIndexed { idx, label ->
                                PlayerMenuOption(label = label, selected = idx == subtitleSizeIndex)
                            },
                            onSelect = { idx -> onSubtitleSizeChange(idx) },
                        )
                        // 音轨菜单(条件:>1 才显示,契约第 2.3 条)。
                        if (audioOptions.size > 1) {
                            PlayerMenuButton(
                                icon = Icons.Filled.VolumeUp,
                                label = "音轨",
                                options = audioOptions.map { opt ->
                                    PlayerMenuOption(label = opt.label)
                                },
                                onSelect = { idx -> applyAudio(audioOptions[idx]) },
                            )
                        }
                    }
                }
            }
        }

        // 播放错误提示 overlay(诊断"放不了"类问题)。
        // ExoPlayer 解码/拉流失败时覆盖在画面上,显示**友好中文**错误提示 +
        // 针对性建议(如 HDR 视频换真机)+ 关闭按钮。不自动消失 —— 让用户看到
        // 信息(也方便 mumu 截图反馈)。
        playbackError?.let { err ->
            PlaybackErrorOverlay(
                error = err,
                onDismiss = { playbackError = null },
            )
        }
    }
}

/**
 * 把 ExoPlayer 的 [PlaybackException] 翻译成友好文案(标题 + 详情)。
 *
 * 针对已知根因给针对性提示,未知的兜底显示原始 errorCode + message(方便反馈)。
 * 当前已知根因(对照 handoff 修复记录):
 *   - `NO_EXCEEDS_CAPABILITIES` + HEVC/HDR:模拟器无 HEVC HDR 硬解。
 *     真机大多支持,提示换真机。
 */
private fun describePlaybackError(error: androidx.media3.common.PlaybackException): Pair<String, String> {
    // ExoPlayer 的 exception message 形如 "..., format=Format(..., video/hevc, ...), format_supported=NO_EXCEEDS_CAPABILITIES"
    // 用字符串特征判断 —— 不直接依赖 ExoPlaybackException 的内部字段(避免 @OptIn(UnstableApi) 蔓延)。
    val msg = error.message.orEmpty()
    val isExceedCapabilities = msg.contains("NO_EXCEEDS_CAPABILITIES") ||
        error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_DECODING_FORMAT_UNSUPPORTED
    val isHevc = msg.contains("video/hevc", ignoreCase = true) ||
        msg.contains("hvc1", ignoreCase = true) ||
        msg.contains("HEVC", ignoreCase = true)
    val isHdr = msg.contains("BT2020", ignoreCase = true) ||
        msg.contains("ST2084", ignoreCase = true) ||
        msg.contains("HDR", ignoreCase = true)

    return when {
        isExceedCapabilities && (isHevc || isHdr) -> {
            val detail = buildString {
                append("该视频使用 HEVC/HDR 编码,当前设备硬解不支持。")
                if (isHdr) append("(HDR 10-bit 内容)")
                append("\n建议在真机或 Android TV 盒子上播放。")
            }
            "视频编码不支持" to detail
        }
        isExceedCapabilities -> {
            "视频编码不支持" to "该视频编码超出当前设备解码能力,建议换设备播放。"
        }
        // IO 类错误码 media3 归在 2000-2999 段(ERROR_CODE_IO_* 系列)。用数值范围
        // 判断比列具体常量稳(常量值非连续,用 range 容易写反方向)。
        error.errorCode in 2000..2999 -> {
            "网络错误" to "视频流拉取失败(错误码 ${error.errorCode})。\n请检查网络连接或网盘链接是否有效,稍后重试。"
        }
        else -> {
            "播放失败" to "错误码 ${error.errorCode}\n${error.message ?: "未知错误"}"
        }
    }
}

/**
 * 播放错误 overlay:覆盖画面,显示友好错误文案 + 关闭按钮。
 */
@Composable
private fun PlaybackErrorOverlay(
    error: androidx.media3.common.PlaybackException,
    onDismiss: () -> Unit,
) {
    val (title, detail) = remember(error) { describePlaybackError(error) }
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black.copy(alpha = 0.75f)),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            modifier = Modifier.padding(48.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            androidx.tv.material3.Text(
                text = title,
                color = Color.White,
                fontSize = 26.sp,
                fontWeight = FontWeight.Bold,
            )
            androidx.tv.material3.Text(
                text = detail,
                color = slate400,
                fontSize = 15.sp,
                textAlign = TextAlign.Center,
            )
            androidx.tv.material3.Surface(
                onClick = onDismiss,
                shape = androidx.tv.material3.ClickableSurfaceDefaults.shape(
                    shape = androidx.compose.foundation.shape.RoundedCornerShape(20.dp),
                ),
                colors = androidx.tv.material3.ClickableSurfaceDefaults.colors(
                    containerColor = primaryColor,
                ),
            ) {
                androidx.tv.material3.Text(
                    text = "关闭",
                    color = Color.White,
                    fontSize = 16.sp,
                    fontWeight = FontWeight.Bold,
                    modifier = Modifier.padding(horizontal = 32.dp, vertical = 12.dp),
                )
            }
        }
    }
}

/**
 * 播放/暂停按钮(图标随 ExoPlayer isPlaying 状态切换)。
 * isPlaying 变化时刷新控制层显隐(暂停时常驻,播放时自动隐藏)。
 */
@Composable
private fun PlayPauseButton(
    exoPlayer: ExoPlayer,
    playerState: VideoPlayerState,
) {
    var isPlaying by remember { mutableStateOf(exoPlayer.isPlaying) }
    LaunchedEffect(exoPlayer) {
        exoPlayer.addListener(object : Player.Listener {
            override fun onIsPlayingChanged(playing: Boolean) {
                isPlaying = playing
                playerState.showControls(playing)
            }
        })
    }
    PlayerControlButton(
        icon = if (isPlaying) Icons.Filled.Pause else Icons.Filled.PlayArrow,
        label = if (isPlaying) "暂停" else "播放",
        onClick = { if (isPlaying) exoPlayer.pause() else exoPlayer.play() },
    )
}

// ── 字幕底部留白档位(对照主流播放器:控制层显示时字幕上浮)──────────────────
// SubtitleView.setBottomPaddingFraction 的参数是占视图高度的比例。
// 控制层(seek bar + 控制行 + 渐变遮罩)大约占底部 22% 屏幕。
private const val SUBTITLE_BOTTOM_PADDING_HIDDEN = 0.08f
private const val SUBTITLE_BOTTOM_PADDING_WHEN_CONTROLS_VISIBLE = 0.22f
