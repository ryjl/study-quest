package com.revin.studyquest.tv.ui.player

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
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
import androidx.compose.material.icons.filled.PlayArrow
import com.revin.studyquest.tv.player.NetdiskHttpFactory
import com.revin.studyquest.tv.player.ProgressReporter
import com.revin.studyquest.tv.player.ResumeWatchdog
import com.revin.studyquest.tv.ui.theme.slate900

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
    val exoPlayer = remember(playInfo.url) {
        ExoPlayer.Builder(context).build().also { p ->
            // 网盘头注入:OkHttpDataSource.Factory 设默认请求头(Referer 等)
            val dataSourceFactory = netdiskFactory.create(playInfo.headers)
            val mediaSourceFactory = DefaultMediaSourceFactory(context)
                .setDataSourceFactory(dataSourceFactory)

            // MediaItem(含字幕轨配置,WebVTT)
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

            p.setMediaItem(mediaItem)
            p.prepare()
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

    // 4. 播放器画面 + 控制层 overlay
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black)
            // D-pad 事件:控件隐藏时 ◄► seek,▲▼/Enter 唤出(对照 JetStream dPadEvents)
            .onPreviewKeyEvent { event ->
                if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                when (event.key) {
                    Key.DirectionLeft, Key.DirectionRight -> {
                        if (!playerState.isControlsVisible) {
                            // 控件隐藏:◄► = seek ±10s
                            val step = 10_000L
                            val cur = exoPlayer.currentPosition
                            exoPlayer.seekTo(
                                if (event.key == Key.DirectionLeft) (cur - step).coerceAtLeast(0)
                                else (cur + step).coerceAtMost(exoPlayer.duration.coerceAtLeast(0))
                            )
                            true
                        } else false // 控件显示:透传给 seek bar
                    }
                    Key.DirectionUp, Key.DirectionDown, Key.Enter, Key.DirectionCenter -> {
                        playerState.showControls(exoPlayer.isPlaying)
                        if (event.key == Key.Enter || event.key == Key.DirectionCenter) {
                            // Enter 同时切换播放/暂停(控件显示时)
                            if (exoPlayer.isPlaying) exoPlayer.pause() else exoPlayer.play()
                        }
                        true
                    }
                    Key.Back -> {
                        onBack()
                        true
                    }
                    else -> false
                }
            }
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

        // 控制层 overlay(可见时显示)
        if (playerState.isControlsVisible) {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .background(Color.Black.copy(alpha = 0.4f)),
            ) {
                // 顶部留白(后续放顶栏:返回 + 标题 + AI 入口)
                Box(Modifier.weight(1f))

                // seek bar
                VideoPlayerSeeker(
                    player = exoPlayer,
                    modifier = Modifier.fillMaxSize(),
                )

                // 控制行(占位:后续补速度/字幕/音轨菜单)
                VideoPlayerControls(
                    buttons = buildControlsButtons(exoPlayer),
                )
            }
        }
    }
}

/** 构建控制行按钮(占位,后续补菜单逻辑)。 */
private fun buildControlsButtons(exoPlayer: ExoPlayer): List<PlayerControlButtonData> {
    return listOf(
        PlayerControlButtonData(
            icon = Icons.Filled.PlayArrow,
            label = "播放/暂停",
            onClick = { if (exoPlayer.isPlaying) exoPlayer.pause() else exoPlayer.play() },
        ),
        // TODO: 速度 / 字幕 / 字幕大小 / 音轨 / 全屏 菜单
    )
}
