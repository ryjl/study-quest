package com.revin.studyquest.tv.ui.detail

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.revin.studyquest.tv.data.remote.dto.ChapterDto
import com.revin.studyquest.tv.data.remote.dto.CourseDto
import com.revin.studyquest.tv.data.remote.dto.EpisodeDto
import com.revin.studyquest.tv.data.remote.dto.SubjectDto
import com.revin.studyquest.tv.data.remote.dto.UserProgressDto
import com.revin.studyquest.tv.data.remote.dto.resolveSubject
import com.revin.studyquest.tv.data.repo.CourseRepo
import com.revin.studyquest.tv.domain.ChapterRef
import com.revin.studyquest.tv.domain.EpisodeRef
import com.revin.studyquest.tv.domain.GroupedChapter
import com.revin.studyquest.tv.domain.groupEpisodesByChapter
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * 课程详情屏 ViewModel —— 对照 PAD `course_detail_screen.dart` 的 `_CourseDetailScreenState`。
 *
 * 职责:
 *  1. [load] 并发拉 4 份数据(对照 PAD `_refreshData` 行 78):
 *     - [CourseRepo.fetchCourse]:课程元信息(标题/学科/封面等。hall 列表项字段可能
 *       不全,详情页自己拉一次更稳妥)。
 *     - [CourseRepo.fetchEpisodes] + [CourseRepo.fetchChapters]:章节分组输入。
 *     - [CourseRepo.fetchProgressOverview]:全量进度,用于完成对勾 / 续播提示 / 进度%。
 *  2. 另起 [CourseRepo.fetchSubjects] 拉学科目录(对照 PAD initState 行 73),给 Hero
 *     渐变色 + 学科 label 用。非阻塞,失败 → 空 catalog(降级显示 raw key)。
 *  3. episodes/chapters 映射成 [EpisodeRef]/[ChapterRef] 喂给 [groupEpisodesByChapter]
 *     (domain 纯函数,契约 business-rules.md 第 3 节,已单测覆盖)。
 *  4. 暴露 [CourseDetailUiState.Done] 内含分组结果 + ref→DTO 回查 map + 进度 map +
 *     完成数 + 进度%(对照 PAD 行 148-159 的 completionMap/resumeMap/progressPercent)。
 *
 * @param courseRepo 课程仓库(注入)。
 */
@HiltViewModel
class CourseDetailViewModel @Inject constructor(
    private val courseRepo: CourseRepo,
) : ViewModel() {

    private val _uiState = MutableStateFlow<CourseDetailUiState>(CourseDetailUiState.Loading)
    val uiState: StateFlow<CourseDetailUiState> = _uiState.asStateFlow()

    /** 学科目录(对照 PAD `_subjectsCatalog`)。 */
    private val _subjects = MutableStateFlow<List<SubjectDto>>(emptyList())
    val subjects: StateFlow<List<SubjectDto>> = _subjects.asStateFlow()

    /**
     * 加载详情页全部数据。
     *
     * 对照 PAD `_refreshData`(course_detail_screen.dart 行 78)的 `Future.wait`。
     * Kotlin 用 [async]/[awaitAll] 并发;任一核心请求失败 → 整页 Error(对照 PAD
     * FutureBuilder 的 hasError 分支)。subjects 单独发,失败不拖累主流程(对照
     * PAD initState 的非阻塞 fetchSubjects)。
     *
     * @param courseId NavHost 路由参数(Screen 用 LaunchedEffect 传入)。
     */
    fun load(courseId: Int) {
        _uiState.value = CourseDetailUiState.Loading
        viewModelScope.launch {
            // subjects 非阻塞单独拉(失败 → 空,UI 用 fallback 显示 raw key)。
            launch { _subjects.value = courseRepo.fetchSubjects() }
            // 核心 4 请求并发:任一失败 → catch 进 Error 态。
            try {
                // 4 个并发请求,各自 await 拿强类型结果(awaitAll 返回 List<Any>
                // 无法解构成不同类型,这里逐个 await)。
                val courseDef = async { courseRepo.fetchCourse(courseId) }
                val episodesDef = async { courseRepo.fetchEpisodes(courseId) }
                val chaptersDef = async { courseRepo.fetchChapters(courseId) }
                val progressDef = async { courseRepo.fetchProgressOverview() }

                val c = courseDef.await()
                val eps = episodesDef.await()
                val chs = chaptersDef.await()
                val progList = progressDef.await()

                if (eps.isEmpty()) {
                    // 对照 PAD 行 137-145:episodes 为空 → empty 态。
                    _uiState.value = CourseDetailUiState.Empty
                    return@launch
                }

                // 章节分组(契约 business-rules.md 第 3 节)。
                val grouped = groupEpisodesByChapter(
                    episodes = eps.map { EpisodeRef(id = it.id, chapterId = it.chapterId) },
                    chapters = chs.map { ChapterRef(id = it.id, title = it.title, sortOrder = it.sortOrder) },
                )
                // ref→DTO 回查 map(分组结果只含 ref,UI 渲染要完整字段)。
                val episodeMap = eps.associateBy { it.id }
                // 进度 map(对照 PAD completionMap 行 148 + resumeMap 行 149)。
                val progressMap = progList.associateBy { it.episodeId }
                // 完成数 + 进度%(对照 PAD 行 158-159)。
                val completedCount = eps.count { progressMap[it.id]?.isCompleted == true }
                val progressPercent = if (eps.isEmpty()) 0 else completedCount * 100 / eps.size

                _uiState.value = CourseDetailUiState.Done(
                    course = c,
                    grouped = grouped,
                    episodeMap = episodeMap,
                    progressMap = progressMap,
                    episodeCount = eps.size,
                    completedCount = completedCount,
                    progressPercent = progressPercent,
                )
            } catch (e: Exception) {
                _uiState.value = CourseDetailUiState.Error(e.message ?: "加载课程详情失败")
            }
        }
    }

    /**
     * 学科查询 helper(对照 PAD `resolveSubject`)。供 UI 拿 label/color。
     * catalog 为空时 fallback:label = raw key,color = 灰。
     */
    fun resolveSubject(key: String): SubjectDto = resolveSubject(key, _subjects.value)
}

/**
 * 课程详情屏状态机(对照 PAD FutureBuilder 的 waiting/hasError/data 三分支,
 * 额外拆出 Empty —— PAD 在 data 分支内判断 episodes.isEmpty 内联处理,这里独立成态
 * 让 Compose `when` 穷尽匹配更清晰)。
 */
sealed interface CourseDetailUiState {
    /** 加载中(对照 PAD ConnectionState.waiting)。 */
    data object Loading : CourseDetailUiState

    /** 加载失败(对照 PAD hasError)。 */
    data class Error(val message: String) : CourseDetailUiState

    /** 课程无课时(对照 PAD 行 137-145 的 emptyStateBox)。 */
    data object Empty : CourseDetailUiState

    /**
     * 加载成功(对照 PAD data 分支,内含分组结果 + 进度数据)。
     *
     * @param course 课程元信息。
     * @param grouped 章节分组结果(每个 GroupedChapter 含 List<EpisodeRef>)。
     * @param episodeMap ref→DTO 回查(UI 渲染课时卡时要完整字段:标题/时长/封面/锁定)。
     * @param progressMap episodeId→进度(UI 查完成态/续播位)。
     * @param episodeCount 课时总数(给 Hero "共N讲" 用)。
     * @param completedCount 已完成数(给进度% 用)。
     * @param progressPercent 学习进度百分比(0..100,给 Hero 旋转卡用)。
     */
    data class Done(
        val course: CourseDto,
        val grouped: List<GroupedChapter>,
        val episodeMap: Map<Int, EpisodeDto>,
        val progressMap: Map<Int, UserProgressDto>,
        val episodeCount: Int,
        val completedCount: Int,
        val progressPercent: Int,
    ) : CourseDetailUiState
}
