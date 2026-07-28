package com.revin.studyquest.tv.ui.detail

import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.ChevronRight
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.History
import androidx.compose.material.icons.rounded.Lock
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.VideoLibrary
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.launch
import androidx.tv.material3.Border
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.Glow
import androidx.tv.material3.Icon
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import coil.compose.AsyncImage
import com.revin.studyquest.tv.data.remote.dto.EpisodeDto
import com.revin.studyquest.tv.data.remote.dto.PresetGradeTags
import com.revin.studyquest.tv.data.remote.dto.UserProgressDto
import com.revin.studyquest.tv.data.remote.dto.gradeLabelOf
import com.revin.studyquest.tv.domain.EpisodeRef
import com.revin.studyquest.tv.domain.GroupedChapter
import com.revin.studyquest.tv.ui.theme.accentGreen
import com.revin.studyquest.tv.ui.theme.blue100
import com.revin.studyquest.tv.ui.theme.blue600
import com.revin.studyquest.tv.ui.theme.emerald100
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate300
import com.revin.studyquest.tv.ui.theme.slate400
import com.revin.studyquest.tv.ui.theme.slate50
import com.revin.studyquest.tv.ui.theme.slate600
import com.revin.studyquest.tv.ui.theme.slate700
import com.revin.studyquest.tv.ui.theme.slate800
import com.revin.studyquest.tv.ui.theme.slate900
import com.revin.studyquest.tv.ui.theme.subjectGradientFromColor

/**
 * 课程详情屏 —— TV 端,1:1 对齐 PAD `course_detail_screen.dart`(仅底色适配 TV 深底,
 * 砍掉 AI 相关:总览卡 / 考试入口 / 课时 AI 学习按钮 —— TV 端 AI 已有独立只读页)。
 *
 * 对照 PAD 结构(course_detail_screen.dart):
 *   - StickyTopBar(返回大厅按钮,行 170-191)
 *   - Hero 渐变卡(学科色 + chips + 标题 + "共N讲" + 进度%旋转卡,行 204-225)
 *   - 章节目录面板(行 235-330):
 *     - "闯关目录"标题行(行 253-269)
 *     - 章节分组遍历(groupEpisodesByChapter,行 274-326)
 *   - 课时行 EpisodeRow(行 664-967):
 *     - locked 灰显 + 锁图标(行 693-753)
 *     - 正常:缩略图 + 状态圆 + 标题 + 时长 + 续播文字+进度条(行 755-967)
 *
 * @param courseId NavHost 路由参数。
 * @param onPlayEpisode 点课时 → 跳播放器(传 episodeId)。
 * @param onBack 返回大厅。
 * @param viewModel Hilt 注入。
 */
@Composable
fun CourseDetailScreen(
    courseId: Int,
    onPlayEpisode: (Int) -> Unit,
    onBack: () -> Unit,
    viewModel: CourseDetailViewModel = hiltViewModel(),
) {
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()

    // 进入屏幕触发加载(对照 VideoPlayerScreen 的 LaunchedEffect(episodeId){ loadPlayInfo } 范式)。
    LaunchedEffect(courseId) {
        viewModel.load(courseId)
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(slate900),
    ) {
        when (val state = uiState) {
            is CourseDetailUiState.Loading -> CenterMessage("加载中...")
            is CourseDetailUiState.Error -> CenterMessage("加载失败:${state.message}")
            is CourseDetailUiState.Empty -> CenterMessage("该课程库下暂无课时视频")
            is CourseDetailUiState.Done -> DetailContent(
                state = state,
                viewModel = viewModel,
                onPlayEpisode = onPlayEpisode,
                onBack = onBack,
            )
        }
    }
}

// ── 主内容(顶栏 + 滚动区) ─────────────────────────────────────────────────────

@Composable
private fun DetailContent(
    state: CourseDetailUiState.Done,
    viewModel: CourseDetailViewModel,
    onPlayEpisode: (Int) -> Unit,
    onBack: () -> Unit,
) {
    // 首集焦点锚定:进详情页后焦点要落到第一个可点的课时上(否则 D-pad 无处可去,
    // 实测反馈"焦点丢失,CENTER 无效")。算出第一个非 locked 课时的 id 作为锚点。
    val firstEpisodeId = remember(state) {
        state.grouped.firstNotNullOfOrNull { group ->
            group.episodes.firstOrNull { ref ->
                state.episodeMap[ref.id]?.locked == false
            }?.id
        }
    }
    val firstFocusRequester = remember { FocusRequester() }
    // 进入 Done 态后等一帧(课时行组合完)再请求焦点。
    LaunchedEffect(firstEpisodeId) {
        if (firstEpisodeId != null) {
            withFrameNanos { }
            runCatching { firstFocusRequester.requestFocus() }
        }
    }

    // 滚动状态提到外层:返回大厅按钮聚焦时 scrollTo(0) 让 Hero 卡回顶可见。
    val scrollState = rememberScrollState()
    val scope = androidx.compose.runtime.rememberCoroutineScope()

    Column(modifier = Modifier.fillMaxSize()) {
        // StickyTopBar(对照 PAD 行 170-191)。TV padding 收紧:48→32。
        // 聚焦到返回大厅时自动滚到顶 —— 否则焦点从课时往上跳到返回大厅,
        // 滚动位置停在首集,Hero 卡(课程标题/进度)还在上方滚出去看不见
        // (实测反馈"光标最多回到返回大厅,最上面的课程标题看不见")。
        TopBackBar(
            onBack = onBack,
            onFocus = { scope.launch { scrollState.scrollTo(0) } },
        )

        // 滚动主区(对照 PAD SingleChildScrollView 行 194-334 —— 全量渲染 + 可滚动)。
        // TV padding 收紧:horizontal 48→32,vertical 32→16,间距 40→24。
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(scrollState)
                .padding(horizontal = 32.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(24.dp),
        ) {
            HeroCard(
                state = state,
                viewModel = viewModel,
            )
            ChapterDirectoryPanel(
                state = state,
                firstEpisodeId = firstEpisodeId,
                firstFocusRequester = firstFocusRequester,
                onPlayEpisode = onPlayEpisode,
            )
        }
    }
}

/** 顶栏返回按钮。聚焦时触发 [onFocus](滚到顶,让 Hero 可见)。 */
@Composable
private fun TopBackBar(onBack: () -> Unit, onFocus: () -> Unit = {}) {
    BackBarButton(
        text = "返回大厅",
        onClick = onBack,
        onFocus = onFocus,
        modifier = Modifier.padding(start = 32.dp, top = 16.dp, bottom = 4.dp),
    )
}

// ── Hero 卡(对照 PAD _buildHeroContent 行 490-621) ───────────────────────────

/**
 * Hero 渐变卡:学科色渐变 + 白边 + chips + 标题 + "共N讲" + 进度%旋转卡。
 *
 * 对照 PAD 行 204-225(容器)+ 行 490-621(内容)。TV 版:深底下阴影不可见,
 * 用白边框 + 渐变本身的视觉冲击代替 PAD 的 boxShadow。
 */
@Composable
private fun HeroCard(
    state: CourseDetailUiState.Done,
    viewModel: CourseDetailViewModel,
) {
    val subject = remember(state.course.subject) { viewModel.resolveSubject(state.course.subject) }
    val firstTag = state.course.tagsList.firstOrNull()
    // grade key → label(对照 PAD gradeLabelOf,用 PresetGradeTags 兜底)。
    val gradeLabel = remember(state.course.grade) {
        val firstKey = state.course.grade.split(",").firstOrNull()?.trim().orEmpty()
        if (firstKey.isEmpty()) "通用" else gradeLabelOf(firstKey, PresetGradeTags)
    }

    Box(
        modifier = Modifier
            .fillMaxWidth()
            .background(
                brush = subjectGradientFromColor(subject.color),
                shape = RoundedCornerShape(24.dp),
            )
            .border(width = 2.dp, color = Color.White, shape = RoundedCornerShape(24.dp)),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(24.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            // 左侧详情列(TV 字号全面缩小:标题 36→20,chips 间距 10→8)。
            Column(modifier = Modifier.weight(1f)) {
                // chips:学科 / 年级 / 首标签。
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    HeaderChip(text = subject.label.ifBlank { "课程" })
                    HeaderChip(text = gradeLabel)
                    firstTag?.let { HeaderChip(text = it) }
                }
                Spacer(Modifier.height(12.dp))
                // 课程标题(TV 20sp,手机 36sp 在 TV 大屏太夸张)。
                Text(
                    text = state.course.title,
                    color = Color.White,
                    fontSize = 20.sp,
                    fontWeight = FontWeight.Black,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                Spacer(Modifier.height(8.dp))
                // "共 N 讲挑战任务"。
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(
                        imageVector = Icons.Rounded.VideoLibrary,
                        contentDescription = null,
                        tint = Color.White.copy(alpha = 0.7f),
                        modifier = Modifier.size(14.dp),
                    )
                    Spacer(Modifier.width(6.dp))
                    Text(
                        text = "共 ${state.episodeCount} 讲挑战任务",
                        color = Color.White.copy(alpha = 0.7f),
                        fontSize = 12.sp,
                        fontWeight = FontWeight.Bold,
                    )
                }
            }
            Spacer(Modifier.width(16.dp))
            // 右侧进度%旋转卡。
            ProgressPercentCard(percent = state.progressPercent)
        }
    }
}

/**
 * 学习进度%旋转卡(对照 PAD Transform.rotate 3°,行 564-600)。
 * 白底卡片 + "学习进度" 小字 + 大号百分比数字。
 */
@Composable
private fun ProgressPercentCard(percent: Int) {
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(20.dp))
            .background(Color.White)
            .border(width = 1.5.dp, color = Color.White, shape = RoundedCornerShape(20.dp))
            .rotate(3f) // 3 度旋转(对照 PAD angle: 3 * pi / 180)
            .padding(horizontal = 16.dp, vertical = 10.dp),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                text = "学习进度",
                color = slate400,
                fontSize = 9.sp,
                fontWeight = FontWeight.Black,
            )
            Spacer(Modifier.height(2.dp))
            Row(verticalAlignment = Alignment.Bottom) {
                Text(
                    text = "$percent",
                    color = slate800,
                    fontSize = 28.sp,
                    fontWeight = FontWeight.Black,
                )
                Text(
                    text = "%",
                    color = slate400,
                    fontSize = 12.sp,
                    fontWeight = FontWeight.Bold,
                )
            }
        }
    }
}

/** Hero 卡的半透明白底 chip(TV 字号 10sp,对照 PAD 12sp)。 */
@Composable
private fun HeaderChip(text: String) {
    Box(
        modifier = Modifier
            .background(
                color = Color.White.copy(alpha = 0.2f),
                shape = RoundedCornerShape(8.dp),
            )
            .border(
                width = 1.dp,
                color = Color.White.copy(alpha = 0.2f),
                shape = RoundedCornerShape(8.dp),
            )
            .padding(horizontal = 10.dp, vertical = 4.dp),
    ) {
        Text(
            text = text,
            color = Color.White,
            fontSize = 10.sp,
            fontWeight = FontWeight.Black,
        )
    }
}

// ── 章节目录面板(对照 PAD 行 235-330) ────────────────────────────────────────

/**
 * "闯关目录"面板 + 章节分组列表。
 *
 * 对照 PAD 行 235-330:白卡 + 标题行 + 章节分组遍历。
 * TV 版:slate800 深卡 + slate700 边框(深底适配)。
 */
@Composable
private fun ChapterDirectoryPanel(
    state: CourseDetailUiState.Done,
    firstEpisodeId: Int?,
    firstFocusRequester: FocusRequester,
    onPlayEpisode: (Int) -> Unit,
) {
    val context = LocalContext.current

    Surface(
        shape = RoundedCornerShape(24.dp),
        colors = androidx.tv.material3.SurfaceDefaults.colors(containerColor = slate800),
        border = Border(androidx.compose.foundation.BorderStroke(1.dp, slate700)),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(modifier = Modifier.padding(20.dp)) {
            // 标题行(TV 字号 16sp,对照 PAD 24sp)。
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(28.dp)
                        .background(blue100, RoundedCornerShape(8.dp)),
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(
                        imageVector = Icons.Rounded.VideoLibrary,
                        contentDescription = null,
                        tint = blue600,
                        modifier = Modifier.size(18.dp),
                    )
                }
                Spacer(Modifier.width(10.dp))
                Text(
                    text = "闯关目录",
                    color = Color.White,
                    fontSize = 16.sp,
                    fontWeight = FontWeight.Black,
                )
            }
            Spacer(Modifier.height(20.dp))

            // 章节分组遍历(对照 PAD 行 274-326)。
            state.grouped.forEachIndexed { index, group ->
                val isLast = index == state.grouped.size - 1
                ChapterGroup(
                    group = group,
                    isLast = isLast,
                    groupCount = state.grouped.size,
                    episodeMap = state.episodeMap,
                    progressMap = state.progressMap,
                    firstEpisodeId = firstEpisodeId,
                    firstFocusRequester = firstFocusRequester,
                    onPlayEpisode = onPlayEpisode,
                    onLockedClick = {
                        Toast.makeText(context, "🔒 这一节还没解锁,耐心等待吧~", Toast.LENGTH_SHORT).show()
                    },
                )
                if (!isLast) Spacer(Modifier.height(20.dp))
            }
        }
    }
}

/**
 * 单个章节分组:可选章节头 + 课时列表。
 *
 * 对照 PAD 行 280-325:章节头(渐变竖条 + 标题)在「非 ungrouped 或独立分组」时显示;
 * ungrouped 且有多个分组时隐藏标题(读作普通列表)。
 */
@Composable
private fun ChapterGroup(
    group: GroupedChapter,
    isLast: Boolean,
    groupCount: Int,
    episodeMap: Map<Int, EpisodeDto>,
    progressMap: Map<Int, UserProgressDto>,
    firstEpisodeId: Int?,
    firstFocusRequester: FocusRequester,
    onPlayEpisode: (Int) -> Unit,
    onLockedClick: () -> Unit,
) {
    // 对照 PAD 行 279:ungrouped 且有多个分组 → 隐藏章节头。
    val showChapterHeader = !(group.isUngrouped && groupCount > 1)

    Column {
        if (showChapterHeader) {
            // 章节头:4×16 渐变竖条 + 标题(TV 14sp,对照 PAD 18sp)。
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .width(4.dp)
                        .height(16.dp)
                        .background(
                            brush = Brush.verticalGradient(listOf(Color(0xFF60A5FA), blue600)),
                            shape = RoundedCornerShape(2.dp),
                        )
                )
                Spacer(Modifier.width(8.dp))
                Text(
                    text = group.title,
                    color = Color.White,
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Black,
                )
            }
            Spacer(Modifier.height(10.dp))
        }
        // 课时列表(TV 缩进 12dp,间距 8dp)。
        Column(
            modifier = if (showChapterHeader) Modifier.padding(start = 12.dp) else Modifier,
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            group.episodes.forEach { ref ->
                val episode = episodeMap[ref.id]
                if (episode != null) {
                    val progress = progressMap[ref.id]
                    EpisodeRow(
                        episode = episode,
                        progress = progress,
                        isFirstFocus = firstEpisodeId != null && episode.id == firstEpisodeId,
                        firstFocusRequester = firstFocusRequester,
                        onClick = { onPlayEpisode(episode.id) },
                        onLockedClick = onLockedClick,
                    )
                }
            }
        }
    }
}

// ── 课时行(对照 PAD _buildEpisodeRow 行 664-967) ─────────────────────────────

/**
 * 单个课时卡。
 *
 * 对照 PAD 行 664-967,分两支:
 *   - locked(行 693-753):灰显 + 锁图标 + "等待解锁",点击只弹提示。
 *   - 正常(行 755-967):缩略图 + 状态圆 + 标题 + 时长徽章 + 续播文字+进度条。
 *
 * **焦点方案**:用 `Box + focusable + clickable` 而非 tv-material3 Surface。
 * 实测发现 tv-material3 的 clickable Surface 在 `verticalScroll` 的 Column 里
 * 焦点不稳(进详情页后无 focused 节点,CENTER/tap 都不响应)。Box 方案与
 * SettingsScreen 的按钮同款,焦点行为可靠。
 *
 * **TV 字号适配**:不直接抄 PAD 手机端字号。PAD 标题 16sp 是手机近距离,
 * TV 3 米观看用 14sp(配合更紧凑的 padding),让一屏能显示更多课时。
 */
@Composable
private fun EpisodeRow(
    episode: EpisodeDto,
    progress: UserProgressDto?,
    isFirstFocus: Boolean,
    firstFocusRequester: FocusRequester,
    onClick: () -> Unit,
    onLockedClick: () -> Unit,
) {
    var focused by remember { mutableStateOf(false) }
    val isCompleted = progress?.isCompleted == true
    // 续播位(对照 PAD resumeMap 行 152:!isCompleted && lastPositionSeconds > 5)。
    val resumeSeconds = if (!isCompleted && progress != null && progress.lastPositionSeconds > 5) {
        progress.lastPositionSeconds
    } else 0
    val hasResume = resumeSeconds > 5
    // 续播百分比(对照 PAD 行 680:clamp 0..99)。
    val resumePct = if (hasResume && episode.durationSeconds > 0) {
        (resumeSeconds * 100 / episode.durationSeconds).coerceIn(0, 99)
    } else 0

    Box(
        modifier = Modifier
            .fillMaxWidth()
            .then(if (isFirstFocus) Modifier.focusRequester(firstFocusRequester) else Modifier)
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .clickable { if (episode.locked) onLockedClick() else onClick() }
            .background(
                color = if (focused) primaryColor.copy(alpha = 0.12f) else slate800,
                shape = RoundedCornerShape(16.dp),
            )
            .border(
                width = if (focused) 3.dp else 1.dp,
                color = if (focused) primaryColor else slate700,
                shape = RoundedCornerShape(16.dp),
            )
            .padding(12.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            // 左:缩略图区(TV 缩小到 80×45,对照 PAD 120×68 等比缩)。
            Thumbnail(
                episode = episode,
                isCompleted = isCompleted,
                modifier = Modifier.size(width = 80.dp, height = 45.dp),
            )
            Spacer(Modifier.width(12.dp))

            // 中:详情列。
            Column(modifier = Modifier.weight(1f)) {
                // 标题(完成时灰显,对照 PAD 行 835-842)。TV 字号 14sp。
                Text(
                    text = episode.title,
                    color = if (isCompleted || episode.locked) slate400 else Color.White,
                    fontSize = 14.sp,
                    fontWeight = FontWeight.ExtraBold,
                    maxLines = 1,
                    overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis,
                )
                Spacer(Modifier.height(4.dp))

                if (episode.locked) {
                    // locked:等待解锁。
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Icon(
                            imageVector = Icons.Rounded.Lock,
                            contentDescription = null,
                            tint = slate400,
                            modifier = Modifier.size(11.dp),
                        )
                        Spacer(Modifier.width(4.dp))
                        Text(
                            text = "等待解锁",
                            color = slate400,
                            fontSize = 10.sp,
                            fontWeight = FontWeight.Bold,
                        )
                    }
                } else {
                    // 时长徽章(TV 字号 10sp)。
                    DurationBadge(seconds = episode.durationSeconds)
                }

                // 续播提示(对照 PAD 行 928-956:文字 + 进度条)。
                if (hasResume) {
                    Spacer(Modifier.height(6.dp))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Icon(
                            imageVector = Icons.Rounded.History,
                            contentDescription = null,
                            tint = primaryColor,
                            modifier = Modifier.size(11.dp),
                        )
                        Spacer(Modifier.width(4.dp))
                        Text(
                            text = "已观看 $resumePct%  ·  续播 ${formatDuration(resumeSeconds)}",
                            color = primaryColor,
                            fontSize = 10.sp,
                            fontWeight = FontWeight.Bold,
                        )
                    }
                    Spacer(Modifier.height(4.dp))
                    ResumeProgressBar(percent = resumePct)
                }
            }
            Spacer(Modifier.width(8.dp))

            // 右:箭头(locked 不显示)。
            if (!episode.locked) {
                Icon(
                    imageVector = Icons.Rounded.ChevronRight,
                    contentDescription = null,
                    tint = slate400,
                    modifier = Modifier.size(20.dp),
                )
            }
        }
    }
}

/**
 * 缩略图区(对照 PAD 行 769-827):封面图或占位渐变 + 半透明遮罩 + 中央状态圆。
 *
 * 状态圆:完成 → ✓(绿底);未完成 → ▶(蓝底)。locked 由调用方用占位框 + 锁图标。
 */
@Composable
private fun Thumbnail(
    episode: EpisodeDto,
    isCompleted: Boolean,
    modifier: Modifier = Modifier,
) {
    val borderColor = if (isCompleted) emerald100 else slate300
    Box(
        modifier = modifier
            .clip(RoundedCornerShape(12.dp))
            .background(
                // 无封面 → 占位渐变(对照 PAD _buildThumbnailPlaceholder 行 647-662)。
                brush = if (episode.coverUrl.isEmpty()) Brush.linearGradient(listOf(slate400, slate600))
                else Brush.linearGradient(listOf(Color.Transparent, Color.Transparent)),
            )
            .border(width = 1.5.dp, color = borderColor, shape = RoundedCornerShape(12.dp)),
        contentAlignment = Alignment.Center,
    ) {
        // 封面图(有则覆盖,对照 PAD 行 786-792)。
        if (episode.coverUrl.isNotEmpty()) {
            AsyncImage(
                model = episode.coverUrl,
                contentDescription = episode.title,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        // 半透明遮罩(对照 PAD 行 795-797:让中央图标更醒目)。
        Box(modifier = Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.15f)))
        // 中央状态圆(对照 PAD 行 800-823):✓ 完成 / ▶ 播放。
        Box(
            modifier = Modifier
                .size(32.dp)
                .background(
                    color = if (isCompleted) emerald100.copy(alpha = 0.9f) else Color.White.copy(alpha = 0.9f),
                    shape = CircleShape,
                ),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = if (isCompleted) Icons.Rounded.Check else Icons.Rounded.PlayArrow,
                contentDescription = null,
                tint = if (isCompleted) accentGreen else primaryColor,
                modifier = Modifier.size(20.dp),
            )
        }
    }
}

/** 时长徽章(TV 字号 9sp,对照 PAD 11sp)。 */
@Composable
private fun DurationBadge(seconds: Int) {
    Box(
        modifier = Modifier
            .background(slate700.copy(alpha = 0.5f), RoundedCornerShape(6.dp))
            .padding(horizontal = 6.dp, vertical = 2.dp),
    ) {
        Text(
            text = formatDuration(seconds),
            color = slate400,
            fontSize = 9.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}

/** 续播进度条(对照 PAD LinearProgressIndicator 行 946-955)。 */
@Composable
private fun ResumeProgressBar(percent: Int) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(4.dp)
            .clip(RoundedCornerShape(4.dp))
            .background(slate700),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth(percent / 100f)
                .height(4.dp)
                .background(primaryColor),
        )
    }
}

// ── 通用组件 ──────────────────────────────────────────────────────────────────

/** 返回大厅按钮(对照 PAD Button3D.white,行 178-188)。 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
private fun BackBarButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    onFocus: () -> Unit = {},
) {
    var focused by remember { mutableStateOf(false) }
    LaunchedEffect(focused) {
        if (focused) onFocus()
    }
    Surface(
        onClick = onClick,
        modifier = modifier.onFocusChanged { focused = it.isFocused },
        shape = ClickableSurfaceDefaults.shape(shape = RoundedCornerShape(20.dp)),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.05f),
        glow = ClickableSurfaceDefaults.glow(
            focusedGlow = Glow(elevation = 16.dp, elevationColor = primaryColor.copy(alpha = 0.35f)),
        ),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = slate800,
            focusedContainerColor = primaryColor.copy(alpha = 0.12f),
        ),
        border = ClickableSurfaceDefaults.border(
            border = Border(androidx.compose.foundation.BorderStroke(1.dp, slate700)),
            focusedBorder = Border(androidx.compose.foundation.BorderStroke(3.dp, primaryColor)),
        ),
    ) {
        Row(
            modifier = Modifier.padding(horizontal = 20.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                imageVector = Icons.AutoMirrored.Rounded.ArrowBack,
                contentDescription = null,
                tint = slate400,
                modifier = Modifier.size(18.dp),
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = text,
                color = slate400,
                fontSize = 14.sp,
                fontWeight = FontWeight.Bold,
            )
        }
    }
}

/** 居中文字(loading/error/empty 态)。 */
@Composable
private fun CenterMessage(message: String) {
    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = message,
            color = slate400,
            fontSize = 22.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}

// ── 工具函数(对照 PAD fmt 行 666-674) ───────────────────────────────────────

/**
 * 时长格式化(对照 PAD `fmt` 行 666-674):
 *   - 0 或负 → "--:--"
 *   - 有小时 → H:MM:SS
 *   - 无小时 → M:SS
 */
fun formatDuration(seconds: Int): String {
    if (seconds <= 0) return "--:--"
    val h = seconds / 3600
    val m = (seconds % 3600) / 60
    val s = seconds % 60
    return if (h > 0) "$h:${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}"
    else "$m:${s.toString().padStart(2, '0')}"
}
