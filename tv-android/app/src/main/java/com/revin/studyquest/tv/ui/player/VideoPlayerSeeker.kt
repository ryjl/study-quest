package com.revin.studyquest.tv.ui.player

import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.media3.exoplayer.ExoPlayer
import androidx.tv.material3.Text
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate400
import kotlinx.coroutines.delay

/**
 * Seek bar —— TV 遥控器进度条。
 *
 * 对照 PAD 端 `_buildSeekBar` + design-tokens.md「seek bar 聚焦态」:
 *   - ◄► 单按 = seek ±10 秒(TV 惯例,对照 Google Android TV 规范)
 *   - 聚焦时 track 加粗(4→6dp)、thumb 变大、时间文字加蓝色 glow
 *   - 进度轮询:LaunchedEffect 每 250ms 读 player.currentPosition
 *     (JetStream 作者标注的"临时方案",但 TV 场景够用;后续可改 onPositionDiscontinuity)
 *
 * **D-pad 焦点是 TV 播放器的核心**:seek bar 是默认落点,◄► 直接 seek;
 * ▲▼ 跳控制行(由 VideoPlayerScreen 的 dPadEvents 处理,这里只管 ◄►)。
 *
 * @param player ExoPlayer 实例
 * @param isFocused externally tracked focus state(可选;null 则内部自管)
 * @param onSeekLeft / onSeekRight ◄► 回调(由 VideoPlayerScreen 传入 seek 逻辑)
 */
@Composable
fun VideoPlayerSeeker(
    player: ExoPlayer,
    modifier: Modifier = Modifier,
) {
    var focused by remember { mutableStateOf(false) }
    var positionMs by remember { mutableLongStateOf(0L) }
    var durationMs by remember { mutableLongStateOf(0L) }

    // 进度轮询(对照 JetStream VideoPlayerSeeker 的 LaunchedEffect + delay)
    LaunchedEffect(player) {
        while (true) {
            positionMs = player.currentPosition.coerceAtLeast(0L)
            durationMs = player.duration.coerceAtLeast(0L)
            delay(SEEK_POLL_INTERVAL_MS)
        }
    }

    val trackHeight = if (focused) 6.dp else 4.dp
    val playedFraction = if (durationMs > 0) (positionMs.toFloat() / durationMs).coerceIn(0f, 1f) else 0f

    Row(
        modifier = modifier
            .fillMaxWidth()
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .onPreviewKeyEvent { event ->
                // ◄► seek ±10s(TV 惯例)。其它键透传给父级 dPadEvents 处理。
                if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                when (event.key) {
                    Key.DirectionLeft -> {
                        player.seekTo((positionMs - SEEK_STEP_MS).coerceAtLeast(0))
                        true
                    }
                    Key.DirectionRight -> {
                        player.seekTo((positionMs + SEEK_STEP_MS).coerceAtMost(durationMs))
                        true
                    }
                    else -> false
                }
            }
            .padding(horizontal = 24.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        // 当前时间
        Text(
            text = formatTime(positionMs),
            color = Color.White,
            fontSize = 14.sp,
            fontWeight = FontWeight.Bold,
        )

        // 进度条(自绘:底色 track + 已播 played + thumb)
        Box(
            modifier = Modifier
                .weight(1f)
                .height(trackHeight.coerceAtLeast(4.dp))
                .then(
                    if (focused) Modifier.shadow(8.dp, RoundedCornerShape(3.dp), spotColor = primaryColor)
                    else Modifier
                )
                .background(slate400.copy(alpha = 0.3f), RoundedCornerShape(3.dp)),
        ) {
            // 已播部分
            Box(
                modifier = Modifier
                    .fillMaxWidth(playedFraction)
                    .height(trackHeight)
                    .background(primaryColor, RoundedCornerShape(3.dp)),
            )
        }

        // 总时长
        Text(
            text = formatTime(durationMs),
            color = slate400,
            fontSize = 14.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}

/** 毫秒 → "m:ss" 或 "h:mm:ss"。 */
private fun formatTime(ms: Long): String {
    if (ms <= 0) return "0:00"
    val totalSec = ms / 1000
    val h = totalSec / 3600
    val m = (totalSec % 3600) / 60
    val s = totalSec % 60
    return if (h > 0) String.format("%d:%02d:%02d", h, m, s)
    else String.format("%d:%02d", m, s)
}

private const val SEEK_STEP_MS = 10_000L // ◄► seek 步长 10 秒
private const val SEEK_POLL_INTERVAL_MS = 250L // 进度轮询间隔
