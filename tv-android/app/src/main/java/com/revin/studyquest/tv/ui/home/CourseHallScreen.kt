package com.revin.studyquest.tv.ui.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.PlayCircle
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.tv.material3.Border
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import coil.compose.AsyncImage
import com.revin.studyquest.tv.data.remote.dto.CourseDto
import com.revin.studyquest.tv.ui.theme.brandGradient
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate400
import com.revin.studyquest.tv.ui.theme.slate700
import com.revin.studyquest.tv.ui.theme.slate900

/**
 * 课程大厅屏 —— TV 端核心首页。
 *
 * 对照 PAD `frontend/lib/ui/screen/course_list_screen.dart`:
 *   - 顶栏标题(学习大厅 + 副标题"今天想探索哪个领域呢?"),
 *   - 课程卡片网格(对照 `_buildCourseCard` 行 487):学科渐变 banner +
 *     chip 标签(tag | grade)+ 标题 + 进入提示。
 *
 * TV 适配:
 *   - 用标准 [LazyVerticalGrid]铺卡片。Compose for TV 1.0.0 的 tv-foundation
 *     不含 TvLazyVerticalGrid(已废弃),改用 compose-foundation 的 LazyVerticalGrid;
 *     卡片本身是 tv-material3 [Surface](onClick),D-pad 可聚焦/Enter 触发,
 *     方向键导航由焦点系统接管。
 *   - 卡片聚焦时发光环(对照 design-tokens.md「焦点视觉 TV 版」:
 *     primaryColor alpha 0.35 blurRadius 24)。
 *   - 字号比 PAD 大(远距离客厅可读)。
 *
 * 砍掉的内容(对照 PAD):搜索框 / 学段年级过滤 / 标签过滤 / 继续学习按钮 / drip
 * 解锁徽章。第一版只铺卡片网格,后续阶段补。
 *
 * @param onOpenCourse 点击课程卡 → 跳详情(导航层回调;占位阶段打日志即可)。
 * @param viewModel Hilt 注入。
 */
@Composable
fun CourseHallScreen(
    onOpenCourse: (CourseDto) -> Unit,
    viewModel: CourseHallViewModel = hiltViewModel(),
) {
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(slate900),
    ) {
        // 顶栏(对照 PAD header:学习大厅 + 副标题)。
        HallHeader(
            modifier = Modifier.padding(start = 48.dp, end = 48.dp, top = 40.dp, bottom = 24.dp),
        )

        when (val state = uiState) {
            is CourseHallUiState.Loading -> LoadingState()
            is CourseHallUiState.Error -> ErrorState(
                message = state.message,
                onRetry = { viewModel.loadCourses() },
            )
            is CourseHallUiState.Done -> {
                if (state.courses.isEmpty()) {
                    EmptyState(onRetry = { viewModel.loadCourses() })
                } else {
                    CourseGrid(
                        courses = state.courses,
                        viewModel = viewModel,
                        onOpenCourse = onOpenCourse,
                    )
                }
            }
        }
    }
}

// ── 顶栏 ──────────────────────────────────────────────────────────────────────

@Composable
private fun HallHeader(modifier: Modifier = Modifier) {
    Column(modifier = modifier) {
        Text(
            text = "学习大厅",
            color = Color.White,
            fontSize = 36.sp,
            fontWeight = FontWeight.Bold,
        )
        Spacer(Modifier.height(8.dp))
        Text(
            text = "今天想探索哪个领域呢?",
            color = slate400,
            fontSize = 18.sp,
            fontWeight = FontWeight.Medium,
        )
    }
}

// ── 状态分支 ─────────────────────────────────────────────────────────────────

@Composable
private fun LoadingState() {
    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        Text(text = "加载中...", color = slate400, fontSize = 22.sp)
    }
}

@Composable
private fun ErrorState(message: String, onRetry: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    Column(
        modifier = Modifier.fillMaxSize(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            text = "加载失败",
            color = Color.White,
            fontSize = 28.sp,
            fontWeight = FontWeight.Bold,
        )
        Spacer(Modifier.height(12.dp))
        Text(
            text = message.ifBlank { "请检查网络或后端配置" },
            color = slate400,
            fontSize = 18.sp,
        )
        Spacer(Modifier.height(32.dp))
        // 重试按钮:D-pad 可聚焦(tv-material3 Surface)。
        Surface(
            onClick = onRetry,
            modifier = Modifier.onFocusChanged { focused = it.isFocused },
            shape = ClickableSurfaceDefaults.shape(shape = RoundedCornerShape(20.dp)),
            colors = ClickableSurfaceDefaults.colors(
                containerColor = primaryColor,
                focusedContainerColor = primaryColor,
            ),
        ) {
            Text(
                text = "重试",
                color = Color.White,
                fontSize = 20.sp,
                fontWeight = FontWeight.Bold,
                modifier = Modifier.padding(horizontal = 32.dp, vertical = 14.dp),
            )
        }
    }
}

@Composable
private fun EmptyState(onRetry: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    Column(
        modifier = Modifier.fillMaxSize(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            text = "没有找到已授权的课程",
            color = Color.White,
            fontSize = 26.sp,
            fontWeight = FontWeight.Bold,
        )
        Spacer(Modifier.height(12.dp))
        Text(
            text = "请在后台分配可学习的课程",
            color = slate400,
            fontSize = 18.sp,
        )
        Spacer(Modifier.height(32.dp))
        Surface(
            onClick = onRetry,
            modifier = Modifier.onFocusChanged { focused = it.isFocused },
            shape = ClickableSurfaceDefaults.shape(shape = RoundedCornerShape(20.dp)),
            colors = ClickableSurfaceDefaults.colors(
                containerColor = primaryColor,
                focusedContainerColor = primaryColor,
            ),
        ) {
            Text(
                text = "刷新列表",
                color = Color.White,
                fontSize = 20.sp,
                fontWeight = FontWeight.Bold,
                modifier = Modifier.padding(horizontal = 32.dp, vertical = 14.dp),
            )
        }
    }
}

// ── 课程网格 ──────────────────────────────────────────────────────────────────

@Composable
private fun CourseGrid(
    courses: List<CourseDto>,
    viewModel: CourseHallViewModel,
    onOpenCourse: (CourseDto) -> Unit,
) {
    // TV 横屏 4 列(对照 PAD 宽屏 crossAxisCount=4),卡片宽高比 0.86 接近 PAD。
    LazyVerticalGrid(
        columns = GridCells.Fixed(4),
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 48.dp, end = 48.dp, bottom = 48.dp),
        verticalArrangement = Arrangement.spacedBy(24.dp),
        horizontalArrangement = Arrangement.spacedBy(24.dp),
    ) {
        items(
            items = courses,
            key = { course -> course.id },
        ) { course ->
            // 封面绝对 URL:viewModel 同步拼(baseUrl 已在 init 读进 StateFlow)。
            val coverUrl = remember(course.id, course.coverUrl) {
                viewModel.absoluteCover(course)
            }
            CourseCard(
                course = course,
                coverUrl = coverUrl,
                onClick = { onOpenCourse(course) },
            )
        }
    }
}

/**
 * 课程卡 —— 对照 PAD `_buildCourseCard`(course_list_screen.dart 行 487)。
 *
 * 复刻的 PAD 视觉元素:
 *   1. 顶部学科渐变 banner(`getSubjectGradientFromColor`)。TV 端暂无学科 catalog,
 *      用固定 [brandGradient](蓝→靛)兜底。
 *   2. banner 左上角 chip 标签:"tag | grade"(对照 PAD `cardLabel`)。
 *   3. banner 右下角播放图标(对照 PAD PlayCircleFill)。
 *   4. banner 区可有封面图(有 coverUrl 时 AsyncImage 覆盖)。
 *   5. 信息区:学科名 chip + 标题(2 行省略)+ "点击进入学习" 提示。
 *
 * TV 焦点态(对照 design-tokens.md「焦点视觉 TV 版」):
 *   - 边框 primaryColor 3dp + 发光环(primaryColor alpha 0.35 blurRadius 24)。
 *   - 非聚焦:surface 底色 + slate700 1dp 细边。
 */
@Composable
private fun CourseCard(
    course: CourseDto,
    coverUrl: String,
    onClick: () -> Unit,
) {
    var focused by remember { mutableStateOf(false) }
    // chip 标签 = 首个 tag | grade 首个 key(对照 PAD cardLabel)。
    val firstTag = course.tagsList.firstOrNull().orEmpty()
    val firstGrade = course.grade.split(",").firstOrNull()?.trim().orEmpty()
    val cardLabel = when {
        firstTag.isEmpty() && firstGrade.isEmpty() -> ""
        firstTag.isEmpty() -> firstGrade
        firstGrade.isEmpty() -> firstTag
        else -> "$firstTag | $firstGrade"
    }

    Surface(
        onClick = onClick,
        modifier = Modifier
            .fillMaxWidth()
            .aspectRatio(0.86f)
            .onFocusChanged { focused = it.isFocused }
            .then(
                if (focused) {
                    // 发光环:primaryColor alpha 0.35 blurRadius 24(对照 design-tokens.md TV 版)。
                    Modifier.shadow(
                        elevation = 24.dp,
                        shape = RoundedCornerShape(20.dp),
                        ambientColor = primaryColor.copy(alpha = 0.35f),
                        spotColor = primaryColor.copy(alpha = 0.35f),
                    )
                } else {
                    Modifier
                },
            ),
        shape = ClickableSurfaceDefaults.shape(shape = RoundedCornerShape(20.dp)),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Color(0xFF1E293B), // slate800 卡片底
            focusedContainerColor = primaryColor.copy(alpha = 0.12f), // 聚焦背景微提亮(对照 token)
        ),
        border = ClickableSurfaceDefaults.border(
            border = Border(
                border = androidx.compose.foundation.BorderStroke(
                    width = if (focused) 3.dp else 1.dp,
                    color = if (focused) primaryColor else slate700,
                ),
            ),
        ),
    ) {
        Column(modifier = Modifier.fillMaxSize()) {
            // 顶部 banner:学科渐变 + 封面图(有则覆盖)+ chip + 播放图标。
            Banner(
                course = course,
                coverUrl = coverUrl,
                cardLabel = cardLabel,
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f),
            )
            // 信息区:学科名 chip + 标题 + 进入提示。
            CardInfo(
                subjectKey = course.subject,
                title = course.title,
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f)
                    .padding(12.dp),
            )
        }
    }
}

@Composable
private fun Banner(
    course: CourseDto,
    coverUrl: String,
    cardLabel: String,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier.background(
            brush = if (coverUrl.isNotEmpty()) Brush.linearGradient(
                colors = listOf(Color.Transparent, Color.Transparent),
            ) else brandGradient,
        ),
    ) {
        // 封面图(有则覆盖渐变底)。
        if (coverUrl.isNotEmpty()) {
            AsyncImage(
                model = coverUrl,
                contentDescription = course.title,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        // chip 标签(左上角,对照 PAD cardLabel chip)。
        if (cardLabel.isNotEmpty()) {
            Box(
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .padding(12.dp)
                    .background(
                        color = Color.White.copy(alpha = 0.25f),
                        shape = RoundedCornerShape(12.dp),
                    ),
            ) {
                Text(
                    text = cardLabel,
                    color = Color.White,
                    fontSize = 13.sp,
                    fontWeight = FontWeight.Bold,
                    modifier = Modifier.padding(horizontal = 10.dp, vertical = 5.dp),
                )
            }
        }
        // 播放图标(右下角,对照 PAD PlayCircleFill)。
        Icon(
            imageVector = Icons.Rounded.PlayCircle,
            contentDescription = null,
            tint = Color.White.copy(alpha = 0.7f),
            modifier = Modifier
                .align(Alignment.BottomEnd)
                .padding(12.dp)
                .size(30.dp),
        )
    }
}

@Composable
private fun CardInfo(
    subjectKey: String,
    title: String,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier) {
        // 学科名 chip(对照 PAD 学科名 chip)。
        Box(
            modifier = Modifier
                .background(
                    color = slate700.copy(alpha = 0.5f),
                    shape = RoundedCornerShape(8.dp),
                ),
        ) {
            Text(
                text = if (subjectKey.isEmpty()) "课程" else subjectKey,
                color = slate400,
                fontSize = 12.sp,
                fontWeight = FontWeight.Bold,
                modifier = Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
            )
        }
        Spacer(Modifier.height(8.dp))
        // 标题(2 行省略,对照 PAD maxLines:2)。
        Text(
            text = title.ifBlank { "未命名课程" },
            color = Color.White,
            fontSize = 17.sp,
            fontWeight = FontWeight.Bold,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Spacer(Modifier.weight(1f))
        // 进入提示(对照 PAD "点击进入学习" + 箭头)。
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "点击进入学习",
                color = primaryColor,
                fontSize = 13.sp,
                fontWeight = FontWeight.Bold,
            )
        }
    }
}
