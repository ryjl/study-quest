package com.revin.studyquest.tv.ui.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.revin.studyquest.tv.data.local.TokenStore
import com.revin.studyquest.tv.data.remote.dto.CourseDto
import com.revin.studyquest.tv.data.remote.dto.GradeTagDto
import com.revin.studyquest.tv.data.remote.dto.SubjectDto
import com.revin.studyquest.tv.data.repo.CourseRepo
import com.revin.studyquest.tv.data.repo.UrlResolver
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * 课程大厅 ViewModel —— 对照 PAD `course_list_screen.dart` 的 `_CourseListScreenState`。
 *
 * 职责:
 *  1. init 时调 [CourseRepo.fetchCourses] 拉课程列表(contentType="learning"),
 *     用 sealed [CourseHallUiState] 暴露 Loading / Error / Done 三态。
 *  2. 加载 subject / grade-tag 两个 catalog(对照 PAD `_subjectsCatalog` /
 *     `_gradeTagsCatalog`),给卡片渲染 label / 渐变色用。
 *  3. 暴露 [baseUrl] / [subjects] / [gradeTags] 三个 StateFlow,让 UI 用
 *     `collectAsStateWithLifecycle` 订阅 —— 这是封面 URL 异步竞态修复的关键:
 *     初次组合时 baseUrl 可能还没读到(SP 异步),旧实现 `remember(course.id,
 *     course.coverUrl)` 不含 baseUrl 进 key,导致 baseUrl 到位后封面永久不刷新。
 *     现在 baseUrl 进 StateFlow,UI 重组时 `remember(baseUrl, ...)` 会重算封面 URL。
 *
 * 不做筛选 / 搜索(PAD 端的 subject / grade / tag 过滤)—— TV 端 D-pad 操作成本高,
 * 第一版只铺平卡片网格。后续阶段再加。
 *
 * @param courseRepo 课程仓库(注入)。
 * @param tokenStore 持久化存储,用来读 backend baseUrl 解析封面 URL(注入)。
 */
@HiltViewModel
class CourseHallViewModel @Inject constructor(
    private val courseRepo: CourseRepo,
    private val tokenStore: TokenStore,
) : ViewModel() {

    private val _uiState = MutableStateFlow<CourseHallUiState>(CourseHallUiState.Loading)
    val uiState: StateFlow<CourseHallUiState> = _uiState.asStateFlow()

    /**
     * 后端 baseUrl(从 TokenStore 读)。给 UI 用 [UrlResolver.absolute] 同步拼封面 URL,
     * 避免 composable 里 runBlocking 读 SP。init 时加载一次;baseUrl 改动需重载
     * (设置页改完会重建 Activity,这里不监听变化)。
     *
     * **竞态修复**:作为 StateFlow 暴露给 UI,UI collectAsState 后参与重组,使
     * `remember(baseUrl, ...)` 在 baseUrl 到位时重算封面 URL。
     */
    private val _baseUrl = MutableStateFlow<String?>(null)
    val baseUrl: StateFlow<String?> = _baseUrl.asStateFlow()

    /** 学科目录(对照 PAD `_subjectsCatalog`)。空表时 UI 用 fallback 显示原始 key。 */
    private val _subjects = MutableStateFlow<List<SubjectDto>>(emptyList())
    val subjects: StateFlow<List<SubjectDto>> = _subjects.asStateFlow()

    /** 学段 tag 目录(对照 PAD `_gradeTagsCatalog`)。失败兜底 PresetGradeTags。 */
    private val _gradeTags = MutableStateFlow<List<GradeTagDto>>(emptyList())
    val gradeTags: StateFlow<List<GradeTagDto>> = _gradeTags.asStateFlow()

    init {
        loadBaseUrl()
        loadCourses()
        loadCatalogs()
    }

    /** 从 TokenStore 读 baseUrl 进 StateFlow(给 UI 同步拼封面 URL 用)。 */
    private fun loadBaseUrl() {
        viewModelScope.launch { _baseUrl.value = tokenStore.getBaseUrl() }
    }

    /**
     * 拉 subject / grade-tag catalog(非阻塞,失败各自兜底)。
     *
     * 课程列表加载不依赖这两个 catalog(catalog 慢一点顶多卡片先显示原始 key,
     * catalog 到位后 UI 重组刷新成 label),所以跟 [loadCourses] 并行发起,
     * 任一失败不阻塞主列表。
     */
    private fun loadCatalogs() {
        viewModelScope.launch {
            _subjects.value = courseRepo.fetchSubjects()
            _gradeTags.value = courseRepo.fetchGradeTags()
        }
    }

    /** 拉课程列表。失败 → Error 态(带 message);成功 → Done(data)。 */
    fun loadCourses() {
        _uiState.update { CourseHallUiState.Loading }
        viewModelScope.launch {
            val result = runCatching { courseRepo.fetchCourses(contentType = "learning") }
            _uiState.update {
                result.fold(
                    onSuccess = { courses -> CourseHallUiState.Done(courses) },
                    onFailure = { e -> CourseHallUiState.Error(e.message ?: "加载失败") },
                )
            }
        }
    }

    /**
     * 把课程封面(server-relative 或绝对)解析成可直接给 Coil 加载的绝对 URL。
     *
     * 对照 PAD `course_list_screen.dart` 里 `ApiService.absoluteUrl(course.coverUrl)`。
     * 用 [baseUrl] StateFlow 的当前值同步拼接(composable 调用,不能 suspend)。
     */
    fun absoluteCover(course: CourseDto): String =
        UrlResolver.absolute(_baseUrl.value, course.coverUrl)
}

/**
 * 课程大厅 UI 状态机。
 *
 * 对照 PAD `FutureBuilder` 的 ConnectionState.waiting / hasError / data 三分支,
 * 这里用 sealed class 表达,Compose `when` 穷尽匹配。
 */
sealed interface CourseHallUiState {
    /** 加载中。 */
    data object Loading : CourseHallUiState

    /** 加载失败。 */
    data class Error(val message: String) : CourseHallUiState

    /** 加载成功。 */
    data class Done(val courses: List<CourseDto>) : CourseHallUiState
}
