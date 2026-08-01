package app.studyquest.tv.domain

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * 进度上报防作弊决策的单测。
 *
 * 用例对照契约 business-rules.md 第 4 节 + Flutter `_startProgressTimer`(行 407),
 * 覆盖所有分支:正常 delta / delta=0 / delta>30 seek / delta<0 倒退 / 不在播放。
 */
class ProgressRulesTest {

    @Test
    fun `normal forward delta - report with current pos and delta`() {
        val decision = decideProgressTick(
            playing = true,
            currentPos = 105,
            lastLoggedPos = 100,
        )
        // delta=5,落在 1..30 → Report。
        val report = decision as ProgressTickDecision.Report
        assertEquals(105, report.position)
        assertEquals(5, report.delta)
    }

    @Test
    fun `delta exactly 1 - report`() {
        val decision = decideProgressTick(
            playing = true,
            currentPos = 101,
            lastLoggedPos = 100,
        )
        val report = decision as ProgressTickDecision.Report
        assertEquals(1, report.delta)
    }

    @Test
    fun `delta exactly 30 - report`() {
        val decision = decideProgressTick(
            playing = true,
            currentPos = 130,
            lastLoggedPos = 100,
        )
        val report = decision as ProgressTickDecision.Report
        assertEquals(30, report.delta)
    }

    @Test
    fun `delta zero - resync baseline no report`() {
        val decision = decideProgressTick(
            playing = true,
            currentPos = 100,
            lastLoggedPos = 100,
        )
        // delta=0(卡住)→ ResyncBaseline,不上报。
        val resync = decision as ProgressTickDecision.ResyncBaseline
        assertEquals(100, resync.position)
    }

    @Test
    fun `delta over 30 - seek, resync baseline no report`() {
        val decision = decideProgressTick(
            playing = true,
            currentPos = 200,
            lastLoggedPos = 100,
        )
        // delta=100 > 30(seek 跳跃)→ ResyncBaseline,那段不算观看时长。
        val resync = decision as ProgressTickDecision.ResyncBaseline
        assertEquals(200, resync.position)
    }

    @Test
    fun `delta just over 30 (31) - resync`() {
        val decision = decideProgressTick(
            playing = true,
            currentPos = 131,
            lastLoggedPos = 100,
        )
        // 31 越过上限 → 不上报。
        assertEquals(
            ProgressTickDecision.ResyncBaseline(position = 131),
            decision,
        )
    }

    @Test
    fun `negative delta - skip and keep baseline`() {
        val decision = decideProgressTick(
            playing = true,
            currentPos = 0,
            lastLoggedPos = 100,
        )
        // delta=-100(CDN 重连回零)→ SkipKeepBaseline,保基线。
        assertEquals(ProgressTickDecision.SkipKeepBaseline, decision)
    }

    @Test
    fun `small negative delta - skip and keep baseline`() {
        val decision = decideProgressTick(
            playing = true,
            currentPos = 98,
            lastLoggedPos = 100,
        )
        assertEquals(ProgressTickDecision.SkipKeepBaseline, decision)
    }

    @Test
    fun `not playing - skip and keep baseline`() {
        val decision = decideProgressTick(
            playing = false,
            currentPos = 1000, // 即使 position 远超基线
            lastLoggedPos = 0,
        )
        // !playing → skip(语义上根本不该 tick)。
        assertEquals(ProgressTickDecision.SkipKeepBaseline, decision)
    }
}
