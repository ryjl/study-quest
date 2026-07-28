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
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material.icons.filled.Subtitles
import androidx.compose.material.icons.filled.VolumeUp
import com.revin.studyquest.tv.player.NetdiskHttpFactory
import com.revin.studyquest.tv.player.ProgressReporter
import com.revin.studyquest.tv.player.ResumeWatchdog
import com.revin.studyquest.tv.ui.theme.slate900
import androidx.compose.ui.unit.dp

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
            onBack = onBack,
        )
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun VideoPlayerContent(
    playInfo: com.revin.studyquest.tv.data.remote.dto.PlayInfoDto,
    episodeId: Int,
    context: Context,
    onBack: () -> Unit,
    viewModel: PlayerScreenViewModel = hiltViewModel(),
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
        androidx.media3.exoplayer.trackselection.DefaultTrackSelector(context)
    }
    val exoPlayer = remember(playInfo.url) {
        ExoPlayer.Builder(context)
            .setTrackSelector(trackSelector)
            .build().also { p ->
            // 网盘头注入:OkHttpDataSource.Factory 设默认请求头(Referer 等)
            val dataSourceFactory = netdiskFactory.create(playInfo.headers)
            val mediaSourceFactory = DefaultMediaSourceFactory(context)
                .setDataSourceFactory(dataSourceFactory)

            // MediaItem(含字幕轨配置,WebVTT)。backend 字幕作为 side-loaded 轨,
            // 菜单选中时通过 trackSelector 启用对应轨。
            val subtitleConfigs = playInfo.subtitles.map { sub ->
                MediaItem.SubtitleConfiguration.Builder(android.net.Uri.parse(sub.url))
                    .setMimeType("application/vtt")
                    .setLanguage(sub.language)
                    .setLabel(sub.label)
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

    // 3.5 收集容器内嵌的字幕/音轨 + 合并字幕选项(对照 business-rules.md 第 1、2 节)。
    //
    // onTracksChanged 在轨道信息可用时触发(MKV/MP4 解析后)。我们把 ExoPlayer 的
    // TrackGroup 映射成 domain 的 NativeSubtitleTrack/NativeAudioTrack,再调
    // mergeSubtitleOptions(backend + native)/ audioOptions(native)生成菜单选项。
    var subtitleOptions by remember {
        mutableStateOf(
            com.revin.studyquest.tv.domain.mergeSubtitleOptions(
                playInfo.subtitles, emptyList(),
            ),
        )
    }
    var audioOptions by remember { mutableStateOf(emptyList<com.revin.studyquest.tv.domain.AudioOption>()) }
    // 当前选中的字幕/音轨索引(-1 = 未选 / off)。
    var selectedSubtitleIndex by remember { mutableStateOf(0) } // 0 = 「无」(关闭)
    var currentSpeed by remember { mutableStateOf(1.0f) }

    LaunchedEffect(exoPlayer) {
        val tracksListener = object : Player.Listener {
            override fun onTracksChanged(tracks: androidx.media3.common.Tracks) {
                // 映射容器内嵌字幕轨(C.TRACK_TYPE_TEXT)。
                val nativeSubs = mutableListOf<com.revin.studyquest.tv.domain.NativeSubtitleTrack>()
                val nativeAudios = mutableListOf<com.revin.studyquest.tv.domain.NativeAudioTrack>()
                for (group in tracks.groups) {
                    if (!group.isSelected) continue
                    val type = group.type
                    val trackGroup = group.mediaTrackGroup
                    for (i in 0 until trackGroup.length) {
                        val format = trackGroup.getFormat(i)
                        val id = format.id ?: i.toString()
                        when (type) {
                            androidx.media3.common.C.TRACK_TYPE_TEXT -> {
                                nativeSubs.add(
                                    com.revin.studyquest.tv.domain.NativeSubtitleTrack(
                                        id = id,
                                        title = format.label,
                                        language = format.language,
                                    ),
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
                            }
                        }
                    }
                }
                // 合并 backend + native 字幕(契约第 1 节)。
                subtitleOptions = com.revin.studyquest.tv.domain.mergeSubtitleOptions(
                    playInfo.subtitles, nativeSubs,
                )
                // 音轨(契约第 2 节:过滤 no/auto 占位)。
                audioOptions = com.revin.studyquest.tv.domain.audioOptions(nativeAudios)
            }
        }
        exoPlayer.addListener(tracksListener)
    }

    /** 应用字幕选择(对照 PAD `_applySubtitleOption`)。 */
    fun applySubtitle(index: Int) {
        selectedSubtitleIndex = index
        val opt = subtitleOptions.getOrNull(index) ?: return
        val params = trackSelector.parameters.buildUpon()
        when (opt.type) {
            com.revin.studyquest.tv.domain.SubtitleType.OFF -> {
                // 禁用所有 text track。
                params.setTrackTypeDisabled(androidx.media3.common.C.TRACK_TYPE_TEXT, true)
            }
            else -> {
                // 启用 text track(backend VTT 已 side-loaded,native 也是 text 类型)。
                // 简化:启用 text + 按 language 选;若 backend/native 有具体 id,精确匹配。
                params.setTrackTypeDisabled(androidx.media3.common.C.TRACK_TYPE_TEXT, false)
                // backend 字幕用 language 匹配(SideLoaded 轨的 language = sub.language)。
                if (opt.type == com.revin.studyquest.tv.domain.SubtitleType.BACKEND) {
                    opt.backend?.language?.let { lang ->
                        params.setPreferredTextLanguage(lang)
                    }
                }
            }
        }
        trackSelector.setParameters(params.build())
    }

    /** 应用音轨选择(对照 PAD `_applyAudioOption`)。 */
    fun applyAudio(option: com.revin.studyquest.tv.domain.AudioOption) {
        // 简化:按 language 选(精确 id 切换需要 TrackGroup 信息,这里用 language 近似)。
        val lang = option.track.language
        if (lang != null) {
            trackSelector.setParameters(
                trackSelector.parameters.buildUpon()
                    .setPreferredAudioLanguage(lang)
                    .build(),
            )
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
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black)
            .onPreviewKeyEvent { event ->
                if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                when (event.key) {
                    Key.DirectionLeft, Key.DirectionRight -> {
                        if (playerState.isControlsVisible) {
                            // 控制层可见:放行给 seeker(它内部处理 seek)+ 焦点移动。
                            // 同时刷新控制层计时(用户在操作,不要隐藏)。
                            playerState.showControls(exoPlayer.isPlaying)
                            false
                        } else {
                            // 控制层隐藏:直接 seek ±10s + 唤出控制层显示进度。
                            val step = 10_000L
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
