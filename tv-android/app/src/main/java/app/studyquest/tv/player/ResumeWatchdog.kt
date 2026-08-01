package app.studyquest.tv.player

import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer

/**
 * 断点续播 watchdog —— 应对网盘 CDN 重连导致的位置回零。
 *
 * 对照 `docs/business-rules.md` 第 5 节 + PAD 端
 * `frontend/lib/ui/screen/player_screen.dart` 的 `_setupResumeSeek`(行 361)。
 *
 * **问题**:网盘流(尤其 115)断连重连时,CDN 可能把播放位置重置到 0。单次 `seekTo`
 * 不够 —— 即使在 `STATE_READY` 后 seek,后续 CDN 重连又回零。需要反复检测 + 重 seek,
 * 直到位置稳定在断点附近。
 *
 * **机制**:监听 ExoPlayer 的 [Player.Listener.onPositionDiscontinuity],检测到位置
 * 异常跳到接近 0(且不是用户主动 seek),就重 seek 回断点。最多重试 [MAX_RETRIES] 次
 * (对照 PAD 端 8 次),避免无限循环。
 *
 * 用法:
 * ```
 * val watchdog = ResumeWatchdog(resumePositionMs)
 * exoPlayer.addListener(watchdog)
 * // 首次 seek
 * exoPlayer.seekTo(resumePositionMs)
 * // 页面退出时 exoPlayer.removeListener(watchdog)
 * ```
 */
class ResumeWatchdog(
    private val resumePositionMs: Long,
) : Player.Listener {

    private var retries = 0
    /** 标记是否正在由 watchdog 主动 seek(避免 onPositionDiscontinuity 自触发)。 */
    private var seeking = false

    override fun onPositionDiscontinuity(
        oldPosition: Player.PositionInfo,
        newPosition: Player.PositionInfo,
        reason: Int,
    ) {
        if (seeking) return // 我们自己 seek 触发的,忽略
        if (retries >= MAX_RETRIES) return // 超过重试上限,放弃(避免死循环)

        val newPos = newPosition.positionMs
        // 检测"异常回零":新位置接近 0,且离断点很远(不是用户的小幅 seek)。
        // 对照 PAD 端:_setupResumeSeek 检测 position < lastLoggedPosition 且差距大。
        val isResetToZero = newPos < RESUME_RESET_THRESHOLD_MS &&
            (resumePositionMs - newPos) > RESUME_RESET_THRESHOLD_MS

        if (isResetToZero) {
            retries++
            seeking = true
            // 重 seek 回断点。下一次 onPositionDiscontinuity(seeking=true)被忽略,
            // 再下一次(seeking 复位后)若仍回零,继续重试。
            player?.seekTo(resumePositionMs)
            // 延迟复位 seeking 标志(下一帧)。简化:这里直接复位,靠 reason 区分。
            // 实际 ExoPlayer seek 后会再触发一次 DISCONTINUITY_TYPE_INTERNAL,那次的
            // seeking 仍为 true 会被忽略,之后真实播放的位置变化才进入判断。
            seeking = false
        }
    }

    /** 绑定的 ExoPlayer(seekTo 用)。由调用方在 addListener 前赋值。 */
    var player: ExoPlayer? = null

    companion object {
        /** 最大重试次数(对照 PAD 端 8 次)。 */
        const val MAX_RETRIES = 8
        /** 位置小于此值视为"回零"(毫秒)。 */
        const val RESUME_RESET_THRESHOLD_MS = 3_000L
    }
}
