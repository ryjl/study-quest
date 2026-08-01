package app.studyquest.tv.domain

// ---------------------------------------------------------------------------
// Decision type
// ---------------------------------------------------------------------------

/**
 * 进度上报一次 tick 的纯决策结果(契约 business-rules.md 第 4 节,
 * 对齐 Flutter `player_screen.dart#_startProgressTimer` 行 407)。
 *
 * 把决策从 Timer / 网络 / 副作用里抽出来,这样播放器层只负责:
 * 每 5s tick → 调 [decideProgressTick] → 按返回的决策执行(上报 / 跳过 / 重基线)。
 * 决策本身是纯函数,可以单测覆盖所有分支。
 *
 * - [Report]        正常前进(delta ∈ 1..30 秒):用 currentPos 和 delta 上报。
 *                   对应 Flutter: `reportProgress(...)` + `lastLoggedPosition = currentPos`。
 * - [SkipKeepBaseline] 位置倒退(delta < 0):跳过本 tick,**保持基线不变**。
 *                   防止 CDN 重连回零后,把基线锚在 0 导致后续 seek 回断点被算成
 *                   巨大虚假 delta。
 * - [ResyncBaseline]    delta == 0(卡住)或 delta > 30(seek 跳跃):**重置基线
 *                   到 currentPos**,不上报,避免把 seek 算成观看时长。
 */
sealed class ProgressTickDecision {
    /** 上报:用 [position] 和 [delta] 调 `/api/v1/progress/report`。 */
    data class Report(
        val position: Int,
        val delta: Int,
    ) : ProgressTickDecision()

    /** 跳过本 tick,基线不变(下次仍与上次真实前进位置比)。 */
    object SkipKeepBaseline : ProgressTickDecision()

    /** 重置基线到 [position],不上报。 */
    data class ResyncBaseline(val position: Int) : ProgressTickDecision()
}

// ---------------------------------------------------------------------------
// Pure decision function
// ---------------------------------------------------------------------------

/**
 * 进度上报防作弊的纯决策(契约第 4 节)。
 *
 * @param playing       当前是否正在播放(`!playing` 直接 skip)
 * @param currentPos    本次 tick 的播放位置(秒)
 * @param lastLoggedPos 上次上报(或重置)后的基线位置(秒)
 * @return 决策结果,见 [ProgressTickDecision] 各分支
 *
 * 分支表:
 * - `playing == false`                                  → [ProgressTickDecision.SkipKeepBaseline]
 *   (语义上不该 tick,但返回 skip;调用方一般根本不会调进来)
 * - `delta = currentPos - lastLoggedPos` ∈ `1..30`      → [ProgressTickDecision.Report]
 *   正常前进,上报 currentPos + delta
 * - `delta < 0`                                         → [ProgressTickDecision.SkipKeepBaseline]
 *   位置倒退(CDN 重连回零),保基线
 * - `delta == 0` 或 `delta > 30`                        → [ProgressTickDecision.ResyncBaseline]
 *   卡住或 seek 跳跃,重置基线不上报
 *
 * delta 上限 30s(5s tick 的 6 倍裕量):应对缓冲/GC/resume 重 seek 的偶发延迟,
 * 避免误丢合法的 6-10s delta —— 之前 10s 上限静默丢了观看时长,admin "学习时长"
 * 卡在 0。后端另有 clamp(600s)+ 单调前进兜底,大跳跃无法膨胀总量。
 */
fun decideProgressTick(
    playing: Boolean,
    currentPos: Int,
    lastLoggedPos: Int,
): ProgressTickDecision {
    if (!playing) return ProgressTickDecision.SkipKeepBaseline

    val delta = currentPos - lastLoggedPos
    return when {
        delta in 1..30 -> ProgressTickDecision.Report(
            position = currentPos,
            delta = delta,
        )
        delta < 0 -> ProgressTickDecision.SkipKeepBaseline
        // delta == 0(卡住)或 delta > 30(seek):重置基线不上报。
        else -> ProgressTickDecision.ResyncBaseline(position = currentPos)
    }
}
