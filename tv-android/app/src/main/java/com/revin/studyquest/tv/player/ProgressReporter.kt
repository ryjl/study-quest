package com.revin.studyquest.tv.player

import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import com.revin.studyquest.tv.data.remote.ApiService
import com.revin.studyquest.tv.data.remote.dto.ReportProgressRequest
import com.revin.studyquest.tv.domain.ProgressTickDecision
import com.revin.studyquest.tv.domain.decideProgressTick
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * 进度上报定时器 —— 防作弊规则。
 *
 * 对照 `docs/business-rules.md` 第 4 节 + PAD 端
 * `frontend/lib/ui/screen/player_screen.dart` 的 `_startProgressTimer`(行 407)。
 *
 * 规则:每 [TICK_INTERVAL_SECONDS] 秒一 tick,根据 [decideProgressTick] 的决策:
 *   - [ProgressTickDecision.Report] → 调 `POST /progress/report`,更新基线
 *   - [ProgressTickDecision.SkipKeepBaseline] → 跳过本 tick,基线不动(CDN 回零)
 *   - [ProgressTickDecision.ResyncBaseline] → 只更新基线不上报(seek 跳跃 / delta=0)
 *
 * 后端 server-side 再 clamp(600s)+ 单调前进校验,双重防作弊。
 *
 * 生命周期:[start] 在播放器 prepared 后调,[stop] 在页面退出/暂停时调。
 * 用 [CoroutineScope] 跑循环(调用方传 viewModelScope),协程取消即停。
 */
class ProgressReporter(
    private val apiService: ApiService,
    private val scope: CoroutineScope,
) {
    private var job: Job? = null
    private var episodeId: Int = 0
    private var lastLoggedPos: Int = 0
    private var player: ExoPlayer? = null

    /**
     * 开始上报。在播放器 prepared 且拿到真实 duration 后调。
     *
     * @param episodeId 当前课时 id(上报用)
     * @param player ExoPlayer 实例(读 currentPosition / isPlaying)
     * @param startPosition 初始基线(断点续播的起点,见 ResumeWatchdog)
     */
    fun start(episodeId: Int, player: ExoPlayer, startPosition: Int) {
        stop()
        this.episodeId = episodeId
        this.player = player
        this.lastLoggedPos = startPosition
        job = scope.launch {
            while (isActive) {
                delay(TICK_INTERVAL_SECONDS * 1000L)
                tick()
            }
        }
    }

    /** 单次 tick:读播放器状态 → 决策 → 执行(上报 / 重置基线 / 跳过)。 */
    private suspend fun tick() {
        val p = player ?: return
        val playing = p.isPlaying
        val currentPos = (p.currentPosition / 1000).toInt() // ms → s

        when (val decision = decideProgressTick(playing, currentPos, lastLoggedPos)) {
            is ProgressTickDecision.Report -> {
                runCatching {
                    withContext(Dispatchers.IO) {
                        apiService.reportProgress(
                            ReportProgressRequest(
                                episodeId = episodeId,
                                positionSeconds = decision.position,
                                deltaWatchSeconds = decision.delta,
                            )
                        )
                    }
                }
                // 上报成功(或失败都不影响)更新基线。失败时 PAD 端是 try/catch 吞掉,
                // 基线仍推进(避免失败导致下一 tick 把累积时长算成大 delta 丢掉)。
                lastLoggedPos = decision.position
            }
            is ProgressTickDecision.ResyncBaseline -> {
                lastLoggedPos = decision.position
            }
            ProgressTickDecision.SkipKeepBaseline -> {
                // 不动基线(CDN 回零,下次 forward tick 仍跟真实位置比)
            }
        }
    }

    fun stop() {
        job?.cancel()
        job = null
        player = null
    }

    companion object {
        /** 上报间隔(秒)。对照 PAD 端 Duration(seconds: 5)。 */
        const val TICK_INTERVAL_SECONDS = 5L
    }
}
