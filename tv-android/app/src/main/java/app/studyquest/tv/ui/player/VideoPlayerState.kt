package app.studyquest.tv.ui.player

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.consumeAsFlow
import kotlinx.coroutines.flow.debounce

/**
 * 播放器控制层显隐状态机。
 *
 * 照搬 Google JetStreamCompose 的 `rememberVideoPlayerState` 模式
 * (com/google/jetstream/.../videoPlayer/components/VideoPlayerState.kt)。
 *
 * 机制:
 *   - 播放时,控制层 [hideSeconds] 秒后自动隐藏(D-pad 无操作)
 *   - 暂停时,控制层常驻不隐藏(用户随时要操作)
 *   - 任何 D-pad 操作(方向键 / Enter)唤出控制层并重置计时器
 *
 * 用 conflated Channel + debounce 实现:showControls 往 channel 发秒数,
 * observe 的 debounce 在该秒数后无新事件才隐藏。conflation 保证只记最新计时。
 *
 * 对照 PAD 端 `_scheduleAutoHide`(Timer 4 秒),但 TV 用协程 + debounce 更地道。
 */
class VideoPlayerState internal constructor(
    private val hideSeconds: Int,
) {
    /** 控制层是否可见。初始 true(进播放器就显示)。 */
    var isControlsVisible by mutableStateOf(true)
        private set

    private val channel = Channel<Int>(Channel.CONFLATED)

    /**
     * 唤出控制层。播放时 [hideSeconds] 后自动隐藏;暂停时常驻。
     * 对照 JetStream `showControls(isPlaying)`。
     */
    fun showControls(isPlaying: Boolean = true) {
        updateControlVisibility(if (isPlaying) hideSeconds else Int.MAX_VALUE)
    }

    /** 立即隐藏控制层(ESC / 系统返回键等)。 */
    fun hideControls() {
        isControlsVisible = false
    }

    private fun updateControlVisibility(seconds: Int = hideSeconds) {
        isControlsVisible = true
        channel.trySend(seconds)
    }

    /** 启动 debounce 观察循环(在 LaunchedEffect 里调)。 */
    internal suspend fun observe() {
        channel.consumeAsFlow()
            .debounce { it.toLong() * 1000 }
            .collect {
                isControlsVisible = false
            }
    }
}

/**
 * 创建并记住 [VideoPlayerState],自动启动显隐观察。
 *
 * @param hideSeconds 播放时控制层多少秒后隐藏(对照 PAD 4 秒,TV 默认 4)。
 */
@Composable
fun rememberVideoPlayerState(hideSeconds: Int = 4): VideoPlayerState {
    val state = remember { VideoPlayerState(hideSeconds) }
    LaunchedEffect(state) { state.observe() }
    return state
}
