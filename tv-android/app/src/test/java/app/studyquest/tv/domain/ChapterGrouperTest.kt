package app.studyquest.tv.domain

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 章节分组逻辑的单测。
 *
 * 用例直接对照 Flutter `frontend/test/service/chapter_grouper_test.dart` 翻译,
 * 保证语义一致(正常分组 / chapterId 缺失归未分组 / 无 chapter 全部「全部课时」/
 * 有 chapter 有未分组「其他课时」/ 空列表 / 排序)。
 */
class ChapterGrouperTest {

    private fun ep(id: Int, chapterId: Int = 0) = EpisodeRef(id = id, chapterId = chapterId)
    private fun ch(id: Int, title: String = "", sortOrder: Int = 0) =
        ChapterRef(id = id, title = title, sortOrder = sortOrder)

    private fun ids(group: GroupedChapter) = group.episodes.map { it.id }

    @Test
    fun `empty episodes and empty chapters - empty list`() {
        val groups = groupEpisodesByChapter(emptyList(), emptyList())
        assertTrue(groups.isEmpty())
    }

    @Test
    fun `multiple chapters - episodes filed under matching chapter`() {
        val chapters = listOf(
            ch(1, title = "第一章", sortOrder = 1),
            ch(2, title = "第二章", sortOrder = 2),
        )
        val episodes = listOf(
            ep(10, chapterId = 1),
            ep(11, chapterId = 1),
            ep(20, chapterId = 2),
        )
        val groups = groupEpisodesByChapter(episodes, chapters)

        assertEquals(2, groups.size)
        assertEquals("第一章", groups[0].title)
        assertEquals(false, groups[0].isUngrouped)
        assertEquals(listOf(10, 11), ids(groups[0]))
        assertEquals("第二章", groups[1].title)
        assertEquals(listOf(20), ids(groups[1]))
    }

    @Test
    fun `episode chapterId pointing at unknown chapter - ungrouped bucket`() {
        val chapters = listOf(ch(1, title = "第一章", sortOrder = 1))
        val episodes = listOf(
            ep(10, chapterId = 1),
            ep(11, chapterId = 999), // orphan: chapter 999 doesn't exist
            ep(12, chapterId = 0),   // chapterId 0 → ungrouped
        )
        val groups = groupEpisodesByChapter(episodes, chapters)

        assertEquals(2, groups.size)
        assertEquals("第一章", groups[0].title)
        assertEquals(true, groups[1].isUngrouped)
        assertEquals("其他课时", groups[1].title) // chapters exist → 「其他课时」
        assertEquals(listOf(11, 12), ids(groups[1]))
    }

    @Test
    fun `episodes with no chapters - all-quan-bu-ke-shi ungrouped bucket`() {
        val episodes = listOf(ep(1), ep(2), ep(3))
        val groups = groupEpisodesByChapter(episodes, emptyList())

        assertEquals(1, groups.size)
        assertEquals("全部课时", groups[0].title)
        assertEquals(true, groups[0].isUngrouped)
        assertEquals(3, groups[0].episodes.size)
    }

    @Test
    fun `chapters are sorted by sortOrder then id`() {
        val chapters = listOf(
            ch(3, title = "C", sortOrder = 2),
            ch(1, title = "A", sortOrder = 1),
            ch(2, title = "B", sortOrder = 1), // same sortOrder, lower id
        )
        val episodes = listOf(
            ep(10, chapterId = 1),
            ep(20, chapterId = 2),
            ep(30, chapterId = 3),
        )
        val groups = groupEpisodesByChapter(episodes, chapters)

        // Expected order: A(1,so=1), B(2,so=1), C(3,so=2)
        assertEquals(listOf("A", "B", "C"), groups.map { it.title })
    }

    @Test
    fun `empty chapter with no matching episodes - dropped`() {
        val chapters = listOf(
            ch(1, title = "第一章", sortOrder = 1),
            ch(2, title = "第二章", sortOrder = 2), // no episodes
        )
        val episodes = listOf(ep(10, chapterId = 1))
        val groups = groupEpisodesByChapter(episodes, chapters)

        assertEquals(1, groups.size)
        assertEquals("第一章", groups.first().title)
    }

    // ---- 补充用例(Kotlin 端额外覆盖章节+未分组混合、episode 顺序保持) ----

    @Test
    fun `chapters plus ungrouped trailing bucket - real chapters first then ungrouped`() {
        val chapters = listOf(ch(1, title = "上篇", sortOrder = 1))
        val episodes = listOf(
            ep(1, chapterId = 1),
            ep(2, chapterId = 0), // ungrouped
        )
        val groups = groupEpisodesByChapter(episodes, chapters)

        assertEquals(2, groups.size)
        assertEquals("上篇", groups[0].title)
        assertEquals(false, groups[0].isUngrouped)
        assertEquals("其他课时", groups[1].title)
        assertEquals(true, groups[1].isUngrouped)
        assertEquals(listOf(2), ids(groups[1]))
    }

    @Test
    fun `episode order preserved within bucket`() {
        val chapters = listOf(ch(1, title = "A", sortOrder = 1))
        val episodes = listOf(
            ep(7, chapterId = 1),
            ep(3, chapterId = 1),
            ep(5, chapterId = 1),
        )
        val groups = groupEpisodesByChapter(episodes, chapters)

        assertEquals(listOf(7, 3, 5), ids(groups[0]))
    }

    @Test
    fun `chapter only with no episodes and no ungrouped - empty list`() {
        val chapters = listOf(ch(1, title = "A", sortOrder = 1))
        val groups = groupEpisodesByChapter(emptyList(), chapters)
        // 没任何 episode → 空(防御兜底只在 episodes.isNotEmpty() 时触发)。
        assertTrue(groups.isEmpty())
    }
}
