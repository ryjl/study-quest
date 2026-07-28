package com.revin.studyquest.tv.domain

// ---------------------------------------------------------------------------
// Minimal abstractions (decoupled from full Episode / Chapter DTOs)
// ---------------------------------------------------------------------------

/**
 * 章节的最小抽象 —— 分组逻辑只关心 (id, title, sortOrder),不耦合完整的
 * `EpisodeDto` / `ChapterDto`。播放器层 / 列表层把后端 DTO 映射成这个,
 * domain 层纯函数方便单测、也方便以后改 DTO 字段。
 */
data class ChapterRef(
    val id: Int,
    val title: String,
    val sortOrder: Int,
)

/**
 * Episode 的最小抽象 —— 分组只关心 (id, chapterId)。完整 DTO(标题、时长、
 * 视频路径等)由 UI 层在拿到分组结果后另行填充。
 */
data class EpisodeRef(
    val id: Int,
    val chapterId: Int,
)

// ---------------------------------------------------------------------------
// Output
// ---------------------------------------------------------------------------

/**
 * 一个分组结果(展示用)。对应契约第 3 节的数据结构。
 *
 * @param title        chapter 标题,或「其他课时」/「全部课时」
 * @param episodes     该分组下的 episode 列表
 * @param isUngrouped  标记未分组桶(影响 UI 样式,如「其他课时」/「全部课时」)
 */
data class GroupedChapter(
    val title: String,
    val episodes: List<EpisodeRef>,
    val isUngrouped: Boolean,
)

// ---------------------------------------------------------------------------
// Pure business rule
// ---------------------------------------------------------------------------

/**
 * 把平铺的 episode 列表按 chapter 分组(契约 business-rules.md 第 3 节,
 * 对齐 Flutter `chapter_grouper.dart#groupEpisodesByChapter` 行 32)。
 *
 * 规则:
 * 1. **chapters 排序**:按 `sortOrder` 升序,`sortOrder` 相同则按 `id` 升序。
 * 2. **episode 分桶**:`chapterId > 0` 且该 id 在 chapters 列表里 → 归对应桶;
 *    否则(`chapterId == 0` 或指向不存在的 chapter)→ 归未分组桶。
 * 3. **输出顺序**:排序后的 chapters 顺序,每个 chapter(桶非空)输出一个分组;
 *    最后如果有未分组 episode,追加一个未分组桶。
 * 4. **未分组桶标题**:chapters 列表为空 → 「全部课时」;非空 → 「其他课时」。
 * 5. **兜底**:都没内容 → 空列表;有 episode 但没生成任何分组(防御性)→
 *    单个「全部课时」分组。
 *
 * @param episodes 平铺的 episode 列表
 * @param chapters 章节目录
 */
fun groupEpisodesByChapter(
    episodes: List<EpisodeRef>,
    chapters: List<ChapterRef>,
): List<GroupedChapter> {
    // 1) chapters 排序:sortOrder 升序,id 升序兜底。
    val sortedChapters = chapters.sortedWith(
        compareBy({ it.sortOrder }, { it.id })
    )

    // 2) episode 分桶。
    val byChapter = LinkedHashMap<Int, MutableList<EpisodeRef>>()
    val ungrouped = mutableListOf<EpisodeRef>()
    for (ep in episodes) {
        val belongs = ep.chapterId > 0 &&
            sortedChapters.any { it.id == ep.chapterId }
        if (belongs) {
            byChapter.getOrPut(ep.chapterId) { mutableListOf() }.add(ep)
        } else {
            ungrouped.add(ep)
        }
    }

    // 3) 输出真实 chapter 在前。
    val groups = mutableListOf<GroupedChapter>()
    for (ch in sortedChapters) {
        val list = byChapter[ch.id]
        if (!list.isNullOrEmpty()) {
            groups.add(
                GroupedChapter(
                    title = ch.title,
                    episodes = list,
                    isUngrouped = false,
                )
            )
        }
    }

    // 未分组在后。
    if (ungrouped.isNotEmpty()) {
        groups.add(
            GroupedChapter(
                // 4) 标题:无 chapter → 「全部课时」;有 → 「其他课时」。
                title = if (sortedChapters.isEmpty()) "全部课时" else "其他课时",
                episodes = ungrouped,
                isUngrouped = true,
            )
        )
    }

    // 5) 防御兜底:有 episode 但没分组(如所有 chapter 桶都空、又没 ungrouped ——
    //    逻辑上不可能同时满足,但保留语义对齐 Flutter)。
    if (groups.isEmpty() && episodes.isNotEmpty()) {
        groups.add(
            GroupedChapter(
                title = "全部课时",
                episodes = episodes,
                isUngrouped = true,
            )
        )
    }

    return groups
}
