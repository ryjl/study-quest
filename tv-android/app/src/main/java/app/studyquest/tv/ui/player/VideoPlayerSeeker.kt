package app.studyquest.tv.ui.player

import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
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
import app.studyquest.tv.ui.theme.primaryColor
import app.studyquest.tv.ui.theme.slate400
import kotlinx.coroutines.delay

/**
 * Seek bar —— TV 遥控器进度条。
 *
 * 对照 PAD 端 `_buildSeekBar` + design-tokens.md「seek bar 聚焦态」:
 *   - ◄► 单按 = seek ±10 秒(TV 惯例,对照 Google Android TV 规范)
 *   - **长按 ◄► 加速 seek**(对照腾讯/网易 TV):按住越久步长越大,松手即停
 *   - **Enter 切换播放/暂停**(焦点在进度条时,对照主流播放器:不必移焦点到播放按钮)
 *   - 聚焦时 track 加粗(4→6dp)、thumb 变大、时间文字加蓝色 glow
 *   - **缓冲进度条**(对照主流播放器):灰色缓冲条铺在已播蓝色条下层,
 *     显示 ExoPlayer 向前预加载到哪
 *   - 进度轮询:LaunchedEffect 每 250ms 读 player.currentPosition / bufferedPosition
 *     (JetStream 作者标注的"临时方案",但 TV 场景够用;后续可改 onPositionDiscontinuity)
 *
 * **D-pad 焦点是 TV 播放器的核心**:seek bar 是默认落点,◄► 直接 seek;
 * ▲▼ 跳控制行(由 VideoPlayerScreen 的 dPadEvents 处理,这里只管 ◄►)。
 *
 * @param player ExoPlayer 实例
 */
@Composable
fun VideoPlayerSeeker(
    player: ExoPlayer,
    modifier: Modifier = Modifier,
) {
    var focused by remember { mutableStateOf(false) }
    var positionMs by remember { mutableLongStateOf(0L) }
    var durationMs by remember { mutableLongStateOf(0L) }
    var bufferedMs by remember { mutableLongStateOf(0L) }

    // 进度轮询(对照 JetStream VideoPlayerSeeker 的 LaunchedEffect + delay)。
    // 同时取 currentPosition / bufferedPosition 给 played + buffered 两个进度条。
    LaunchedEffect(player) {
        while (true) {
            positionMs = player.currentPosition.coerceAtLeast(0L)
            durationMs = player.duration.coerceAtLeast(0L)
            bufferedMs = player.bufferedPosition.coerceAtLeast(0L)
            delay(SEEK_POLL_INTERVAL_MS)
        }
    }

    val trackHeight = if (focused) 6.dp else 4.dp
    val playedFraction = if (durationMs > 0) (positionMs.toFloat() / durationMs).coerceIn(0f, 1f) else 0f
    // 缓冲进度:已预加载到的位置占比(可能 > played,因为 ExoPlayer 向前缓存)。
    val bufferedFraction = if (durationMs > 0) (bufferedMs.toFloat() / durationMs).coerceIn(0f, 1f) else 0f

    // ── 长按 ◄► 加速 seek 状态机 ──────────────────────────────────────────────
    // Android 系统在用户长按方向键时持续派发 KeyDown 事件(KeyEvent.repeatCount
    // 累加)。Compose 的 KeyEvent 没直接暴露 repeatCount,所以自己跟踪:首次 KeyDown
    // 记下起始时间,重复 KeyDown 按"按住时长"换步长档位,KeyUp 复位。
    // 档位表(对照腾讯/网易 TV 的体感):
    //   0–500ms   : 10s(单按,基础档)
    //   500–1500ms: 30s(开始加速)
    //   1500–3000ms: 60s
    //   >3000ms   : 120s(长按飞跳)
    var seekHoldStartMs by remember { mutableLongStateOf(0L) }

    /** 按当前"按住时长"返回 seek 步长(毫秒)。 */
    fun seekStepFor(holdMs: Long): Long = seekStepForHoldMs(holdMs)

    Row(
        modifier = modifier
            .fillMaxWidth()
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .onPreviewKeyEvent { event ->
                if (event.type != KeyEventType.KeyDown) {
                    // KeyUp:复位长按计时器(松手)。
                    if (event.type == KeyEventType.KeyUp &&
                        (event.key == Key.DirectionLeft || event.key == Key.DirectionRight)
                    ) {
                        seekHoldStartMs = 0L
                    }
                    return@onPreviewKeyEvent false
                }
                when (event.key) {
                    Key.DirectionLeft, Key.DirectionRight -> {
                        val now = android.os.SystemClock.uptimeMillis()
                        // 首次按下记录起始;重复按下(now - start 随时间增长)算档位。
                        if (seekHoldStartMs == 0L) seekHoldStartMs = now
                        val step = seekStepFor(now - seekHoldStartMs)
                        val target = if (event.key == Key.DirectionLeft) {
                            (positionMs - step).coerceAtLeast(0L)
                        } else {
                            (positionMs + step).coerceAtMost(durationMs)
                        }
                        player.seekTo(target)
                        // 更新本地 positionMs 避免下次重复事件还拿旧值(轮询 250ms 太慢)。
                        positionMs = target
                        true
                    }
                    Key.Enter, Key.DirectionCenter -> {
                        // 焦点在进度条时,Enter 切换播放/暂停(对照主流播放器:进度条
                        // 是默认焦点落点,Enter 应能直接暂停/继续,不必先移焦点到播放按钮)。
                        // 控制层显隐由 PlayPauseButton 的 onIsPlayingChanged listener 自动
                        // 刷新(暂停时常驻),这里只管切换播放状态。
                        if (player.isPlaying) player.pause() else player.play()
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

        // 进度条(自绘:底色 track + 缓冲 buffered + 已播 played + thumb)
        // 层级从下到上:track(灰底) → buffered(浅灰,前向缓存)→ played(蓝色,已播)。
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
            // 缓冲部分(浅灰,铺满 bufferedFraction)。clamp 到不小于 played,
            // 避免缓冲 < 已播时露白缝(理论上 buffered >= played,但 CDN 抖动可能反常)。
            val bufferedDraw = maxOf(bufferedFraction, playedFraction)
            Box(
                modifier = Modifier
                    .fillMaxWidth(bufferedDraw)
                    .height(trackHeight)
                    .background(Color.White.copy(alpha = 0.25f), RoundedCornerShape(3.dp)),
            )
            // 已播部分(蓝色)
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

private const val SEEK_POLL_INTERVAL_MS = 250L // 进度轮询间隔

/**
 * 长按方向键的 seek 步长档位(对照腾讯/网易 TV)。
 *
 * 按住越久步长越大:
 *   - <500ms  : 10s(单按)
 *   - <1.5s   : 30s(开始加速)
 *   - <3s     : 60s
 *   - >=3s    : 120s(长按飞跳)
 *
 * 这函数同时给 [VideoPlayerSeeker](控制层可见时)和 [VideoPlayerScreen] 的
 * "控制层隐藏时 ◄► seek"用 —— 两处行为一致(否则用户会觉得"控制层显示时
 * 快进手感变了")。
 */
internal fun seekStepForHoldMs(holdMs: Long): Long = when {
    holdMs < 500 -> 10_000L
    holdMs < 1_500 -> 30_000L
    holdMs < 3_000 -> 60_000L
    else -> 120_000L
}
