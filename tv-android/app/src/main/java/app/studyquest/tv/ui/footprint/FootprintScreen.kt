package app.studyquest.tv.ui.footprint

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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.tv.material3.Surface
import androidx.tv.material3.SurfaceDefaults
import androidx.tv.material3.Text
import app.studyquest.tv.ui.theme.BorderRadiusValue
import app.studyquest.tv.ui.theme.NormalBorderWidthValue
import app.studyquest.tv.ui.theme.accentGreen
import app.studyquest.tv.ui.theme.accentOrange
import app.studyquest.tv.ui.theme.blue100
import app.studyquest.tv.ui.theme.brandGradient
import app.studyquest.tv.ui.theme.levelBadgeGradient
import app.studyquest.tv.ui.theme.orange400
import app.studyquest.tv.ui.theme.primaryColor
import app.studyquest.tv.ui.theme.slate400
import app.studyquest.tv.ui.theme.slate700
import app.studyquest.tv.ui.theme.slate800
import app.studyquest.tv.ui.theme.slate900
import app.studyquest.tv.ui.theme.yellow400

/**
 * 成长足迹屏 —— TV 端 dashboard。
 *
 * 对照 PAD `frontend/lib/ui/screen/growth_footprint_screen.dart` 的布局结构:
 *   1. Header 标题(成长足迹 / 看看你取得了多少成就)
 *   2. 顶部大号积分展示 + 等级徽章(currentPoints 用 accentOrange + levelBadgeGradient,
 *      字号大,TV 远距离可读)
 *   3. 3 张渐变 metric 卡(累计积分 / 专注学习时长 / 已圆满通关)—— 积分卡用真实数据,
 *      时长 / 通关卡因 ApiService 缺端点(fetchProgressOverview)留占位 + TODO。
 *   4. 最近动态(时间线)+ 荣誉墙(徽章)—— 因 ApiService 缺 fetchPointsLedger /
 *      fetchUserBadges 留占位 + TODO。
 *
 * TV 适配:
 *   - 深色底 slate900(对照 design-tokens.md TV 版)
 *   - 用 [LazyColumn] 做 D-pad 可滚动(LazyColumn 子项 focusable 即可被 D-pad 滚动;
 *     tv-material 1.0.0 没有独立的 TvLazyColumn 稳定 API,LazyColumn 完全够用)
 *   - 卡片 focusable + 聚焦发光环(对照 design-tokens.md 焦点视觉 TV 版:边框 primaryColor
 *     3dp + shadow 发光环 alpha 0.35)
 *
 * 数据加载和等级计算见 [FootprintViewModel]。
 *
 * 注意:本可组合项早期占位版签名是 `FootprintScreen()`(无参,nav 骨架用)。现在签名是
 * `FootprintScreen(viewModel)`。导航层([app.studyquest.tv.ui.nav.AppNav])
 * 调用处需更新为不传参(nav 用 hiltViewModel() 默认注入);调用点改动属导航层职责。
 */
@Composable
fun FootprintScreen(
    viewModel: FootprintViewModel = hiltViewModel(),
) {
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(slate900),
    ) {
        when (val s = uiState) {
            is FootprintUiState.Loading -> CenterStatus("加载中...", color = primaryColor)
            is FootprintUiState.Error -> CenterError(
                message = s.message,
                onRetry = viewModel::load,
            )
            is FootprintUiState.Loaded -> FootprintContent(state = s)
        }
    }
}

// ── 主体内容(对照 PAD build 方法的 Column) ────────────────────────────────────

@Composable
private fun FootprintContent(state: FootprintUiState.Loaded) {
    val points = state.points
    val level = FootprintViewModel.levelForPoints(points.currentPoints)

    LazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = 48.dp, vertical = 32.dp),
        verticalArrangement = Arrangement.spacedBy(32.dp),
    ) {
        item {
            // Header 标题
            Column {
                Text(
                    text = "成长足迹",
                    color = Color.White,
                    fontSize = 36.sp,
                    fontWeight = FontWeight.Bold,
                )
                Spacer(Modifier.height(6.dp))
                Text(
                    text = "看看你取得了多少成就！",
                    color = slate400,
                    fontSize = 16.sp,
                    fontWeight = FontWeight.Bold,
                )
                state.nickname?.let { name ->
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = "当前账号：$name",
                        color = slate400,
                        fontSize = 14.sp,
                    )
                }
            }
        }

        item {
            // 顶部大号积分展示 + 等级徽章(对照 PAD _buildMetricCardsRow 的积分卡 +
            // main_navigation.dart 侧边栏的 Lv.X 徽章)。TV 远距离可读:字号 64sp。
            HeroPointsCard(
                currentPoints = points.currentPoints,
                totalEarnedPoints = points.totalEarnedPoints,
                level = level,
            )
        }

        item {
            // 3 张渐变 metric 卡(对照 PAD _buildMetricCardsRow)。
            // 积分卡 = 真实数据;时长 / 通关卡 = 占位(ApiService 缺端点)。
            MetricCardsRow(
                totalEarnedPoints = points.totalEarnedPoints,
                studyMinutes = null,   // TODO: 需要 ApiService.fetchProgressOverview() 拿 watchSeconds 求和
                completedCount = null, // TODO: 需要 ApiService.fetchProgressOverview() 拿 isCompleted 计数
            )
        }

        item {
            // 最近动态(时间线)+ 荣誉墙(徽章)左右并列(对照 PAD bottom Grid)。
            BottomPanelsRow()
        }
    }
}

// ── 顶部大号积分卡(对照 PAD 积分卡 + Lv.X 徽章) ───────────────────────────────

@Composable
private fun HeroPointsCard(
    currentPoints: Int,
    totalEarnedPoints: Int,
    level: Int,
) {
    Surface(
        shape = RoundedCornerShape(BorderRadiusValue),
        colors = SurfaceDefaults.colors(
            containerColor = Color.Transparent,
        ),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .background(
                    brush = brandGradient,
                    shape = RoundedCornerShape(BorderRadiusValue),
                )
                .border(
                    width = NormalBorderWidthValue,
                    color = Color.White,
                    shape = RoundedCornerShape(BorderRadiusValue),
                )
                .padding(32.dp),
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
            ) {
                Column {
                    Text(
                        text = "当前积分",
                        color = Color.White.copy(alpha = 0.85f),
                        fontSize = 16.sp,
                        fontWeight = FontWeight.Bold,
                    )
                    Spacer(Modifier.height(8.dp))
                    Row(
                        verticalAlignment = Alignment.Bottom,
                    ) {
                        Text(
                            text = "$currentPoints",
                            color = Color.White,
                            fontSize = 64.sp,
                            fontWeight = FontWeight.Bold,
                        )
                        Spacer(Modifier.width(10.dp))
                        Text(
                            text = "分",
                            color = Color.White,
                            fontSize = 22.sp,
                            fontWeight = FontWeight.Bold,
                            modifier = Modifier.padding(bottom = 12.dp),
                        )
                    }
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = "累计获得 $totalEarnedPoints 分",
                        color = Color.White.copy(alpha = 0.8f),
                        fontSize = 14.sp,
                        fontWeight = FontWeight.Bold,
                    )
                }

                // 等级徽章(对照 PAD main_navigation.dart 的 Lv.X,用 levelBadgeGradient
                // 橙→黄渐变,accentOrange 系,字号大远距离可读)。
                LevelBadge(level = level)
            }
        }
    }
}

/** 等级徽章:橙→黄渐变圆角块,中央 Lv.X。 */
@Composable
private fun LevelBadge(level: Int) {
    Box(
        modifier = Modifier
            .size(120.dp)
            .background(
                brush = levelBadgeGradient,
                shape = RoundedCornerShape(24.dp),
            )
            .border(
                width = NormalBorderWidthValue,
                color = Color.White,
                shape = RoundedCornerShape(24.dp),
            ),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                text = "Lv.$level",
                color = Color.White,
                fontSize = 40.sp,
                fontWeight = FontWeight.Bold,
            )
            Spacer(Modifier.height(2.dp))
            Text(
                text = "等级",
                color = Color.White.copy(alpha = 0.9f),
                fontSize = 14.sp,
                fontWeight = FontWeight.Bold,
            )
        }
    }
}

// ── 3 张渐变 metric 卡(对照 PAD _buildMetricCardsRow / _metricCard) ────────────

@Composable
private fun MetricCardsRow(
    totalEarnedPoints: Int,
    studyMinutes: Int?,
    completedCount: Int?,
) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(24.dp),
        modifier = Modifier.fillMaxWidth(),
    ) {
        MetricCard(
            modifier = Modifier.weight(1f),
            gradient = listOf(accentOrange, orange400),
            label = "累计获得积分",
            value = "$totalEarnedPoints",
            unit = null,
        )
        MetricCard(
            modifier = Modifier.weight(1f),
            gradient = listOf(Color(0xFF3B82F6), Color(0xFF60A5FA)),
            label = "专注学习时长",
            value = studyMinutes?.let { "$it" } ?: "—",
            unit = "分钟",
            dimmed = studyMinutes == null,
        )
        MetricCard(
            modifier = Modifier.weight(1f),
            gradient = listOf(accentGreen, Color(0xFF34D399)),
            label = "已圆满通关",
            value = completedCount?.let { "$it" } ?: "—",
            unit = "门课",
            dimmed = completedCount == null,
        )
    }
}

/**
 * 单张渐变 metric 卡(对照 PAD _metricCard)。
 *
 * TV 焦点视觉(对照 design-tokens.md 焦点视觉 TV 版):focusable + 聚焦发光环
 * (primaryColor alpha 0.35 / blurRadius 24 / 边框 3dp)。metric 卡是纯展示,
 * 这里仍标 focusable 让 D-pad 能落在上面(TV 上没有任何 focusable 项时方向键无响应)。
 */
@Composable
private fun MetricCard(
    modifier: Modifier = Modifier,
    gradient: List<Color>,
    label: String,
    value: String,
    unit: String?,
    dimmed: Boolean = false,
) {
    var focused by remember { mutableStateOf(false) }
    Surface(
        shape = RoundedCornerShape(BorderRadiusValue),
        colors = SurfaceDefaults.colors(containerColor = Color.Transparent),
        modifier = modifier
            .height(140.dp)
            .onFocusChanged { focused = it.isFocused }
            .then(
                if (focused) Modifier.shadow(
                    elevation = 24.dp,
                    shape = RoundedCornerShape(BorderRadiusValue),
                    ambientColor = primaryColor,
                    spotColor = primaryColor,
                ) else Modifier
            ),
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(
                    brush = Brush.linearGradient(gradient),
                    shape = RoundedCornerShape(BorderRadiusValue),
                )
                .border(
                    width = if (focused) 3.dp else NormalBorderWidthValue,
                    color = if (focused) primaryColor else Color.White,
                    shape = RoundedCornerShape(BorderRadiusValue),
                )
                .padding(24.dp),
        ) {
            Column {
                Text(
                    text = label,
                    color = Color.White.copy(alpha = if (dimmed) 0.6f else 0.9f),
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Bold,
                )
                Spacer(Modifier.height(10.dp))
                Row(verticalAlignment = Alignment.Bottom) {
                    Text(
                        text = value,
                        color = Color.White,
                        fontSize = 40.sp,
                        fontWeight = FontWeight.Bold,
                    )
                    unit?.let {
                        Spacer(Modifier.width(6.dp))
                        Text(
                            text = it,
                            color = Color.White,
                            fontSize = 18.sp,
                            fontWeight = FontWeight.Bold,
                            modifier = Modifier.padding(bottom = 6.dp),
                        )
                    }
                }
                if (dimmed) {
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = "数据待接入",
                        color = Color.White.copy(alpha = 0.7f),
                        fontSize = 11.sp,
                    )
                }
            }
        }
    }
}

// ── 底部时间线 + 荣誉墙(对照 PAD bottom Grid) ──────────────────────────────────

@Composable
private fun BottomPanelsRow() {
    Row(
        horizontalArrangement = Arrangement.spacedBy(24.dp),
        modifier = Modifier.fillMaxWidth(),
    ) {
        // 左:最近动态(时间线)。
        TimelinePanel(modifier = Modifier.weight(2f))
        // 右:荣誉墙(徽章)。
        BadgeWallPanel(modifier = Modifier.weight(1f))
    }
}

/**
 * 左面板:最近动态(对照 PAD _buildTimelinePanel)。
 *
 * TODO: 需要 ApiService.fetchPointsLedger(userId, limit) 拿流水(reasonType /
 * changeAmount / description / createdAt)。网络层补端点前显示「暂无最近活动」占位。
 */
@Composable
private fun TimelinePanel(modifier: Modifier = Modifier) {
    GlassPanel(
        modifier = modifier,
        title = "最近动态",
        titleIconColor = Color(0xFF2563EB),
        titleIconBg = blue100,
    ) {
        // TODO(ApiService): 接入 fetchPointsLedger 后替换为真实流水列表。
        //   顶层 curation(对照 PAD):badge_unlocked 取 4 条 + system_watch 取 2 条。
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(160.dp),
            contentAlignment = Alignment.Center,
        ) {
            Text(
                text = "暂无最近活动",
                color = slate400,
                fontSize = 14.sp,
                fontWeight = FontWeight.Bold,
            )
        }
    }
}

/**
 * 右面板:荣誉墙(对照 PAD _buildBadgeWallPanel)。
 *
 * TODO: 需要 ApiService.fetchUserBadges(userId) 拿徽章状态(badge/tier/unlocked/
 * tierCount/nextTier/progress)。网络层补端点前显示「暂无成就」占位 + 星数 0/0。
 */
@Composable
private fun BadgeWallPanel(modifier: Modifier = Modifier) {
    GlassPanel(
        modifier = modifier,
        title = "荣誉墙",
        titleIconColor = Color(0xFFD97706),
        titleIconBg = Color(0xFFFEF3C7),
    ) {
        // TODO(ApiService): 接入 fetchUserBadges 后替换为徽章列表(对照 PAD
        //   _buildBadgeItem / _buildTierProgress:多档徽章画 tier 点 + 进度条,
        //   星数 = sum(unlocked ? tier+1 : 0))。
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(120.dp),
            contentAlignment = Alignment.Center,
        ) {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Text(
                    text = "暂无成就",
                    color = slate400,
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Bold,
                )
                Spacer(Modifier.height(12.dp))
                StarBadge(unlocked = 0, total = 0)
            }
        }
    }
}

/** 星数胶囊(对照 PAD 荣誉墙底部的 Button3D.unlockedStars/totalStars)。 */
@Composable
private fun StarBadge(unlocked: Int, total: Int) {
    Surface(
        shape = RoundedCornerShape(12.dp),
        colors = SurfaceDefaults.colors(
            containerColor = Color.White.copy(alpha = 0.08f),
            contentColor = yellow400,
        ),
    ) {
        Row(
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "★",
                color = yellow400,
                fontSize = 18.sp,
            )
            Spacer(Modifier.width(6.dp))
            Text(
                text = "$unlocked / $total",
                color = Color.White,
                fontSize = 16.sp,
                fontWeight = FontWeight.Bold,
            )
        }
    }
}

// ── 通用玻璃面板(对照 PAD GlassPanel) ────────────────────────────────────────

/**
 * 深色玻璃面板:slate800 半透明底 + slate700 边框 + 标题行(色块图标 + 标题)。
 * 对照 PAD `frontend/lib/widget/glass_panel.dart` 的 GlassPanel。
 */
@Composable
private fun GlassPanel(
    modifier: Modifier = Modifier,
    title: String,
    titleIconColor: Color,
    titleIconBg: Color,
    content: @Composable () -> Unit,
) {
    Surface(
        shape = RoundedCornerShape(BorderRadiusValue),
        colors = SurfaceDefaults.colors(containerColor = slate800),
        border = androidx.tv.material3.Border(
            androidx.compose.foundation.BorderStroke(1.dp, slate700),
        ),
        modifier = modifier,
    ) {
        Column(modifier = Modifier.padding(28.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(36.dp)
                        .background(titleIconBg, RoundedCornerShape(10.dp)),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = "●",
                        color = titleIconColor,
                        fontSize = 18.sp,
                    )
                }
                Spacer(Modifier.width(14.dp))
                Text(
                    text = title,
                    color = Color.White,
                    fontSize = 22.sp,
                    fontWeight = FontWeight.Bold,
                )
            }
            Spacer(Modifier.height(24.dp))
            content()
        }
    }
}

// ── 状态 / 错误(对照 PAD FutureBuilder waiting/error 分支) ─────────────────────

@Composable
private fun CenterStatus(text: String, color: Color = Color.White) {
    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        Text(text = text, color = color, fontSize = 20.sp)
    }
}

@Composable
private fun CenterError(message: String, onRetry: () -> Unit) {
    Box(
        modifier = Modifier.fillMaxSize(),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                text = "获取足迹数据失败",
                color = accentOrange,
                fontSize = 22.sp,
                fontWeight = FontWeight.Bold,
            )
            Spacer(Modifier.height(12.dp))
            Text(
                text = message,
                color = slate400,
                fontSize = 14.sp,
                textAlign = TextAlign.Center,
            )
            Spacer(Modifier.height(24.dp))
            FocusableTextButton(text = "重试", onClick = onRetry)
        }
    }
}

/** 聚焦按钮(对照 LoginScreen.FocusableTextButton,本地副本避免跨屏依赖)。 */
@Composable
private fun FocusableTextButton(text: String, onClick: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    val bgColor = if (focused) primaryColor else primaryColor.copy(alpha = 0.8f)
    // Box + focusable + clickable:tv-material3 clickable Surface 形参链
    // (ClickableSurfaceShape/Colors/Border)类型繁琐,Box 模式更可控
    // (对照 LoginScreen.FocusableTextButton / UserCard 做法)。
    Box(
        modifier = Modifier
            .onFocusChanged { focused = it.isFocused }
            .focusable()
            .clickable(onClick = onClick)
            .background(bgColor, RoundedCornerShape(12.dp)),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = text,
            modifier = Modifier.padding(horizontal = 24.dp, vertical = 12.dp),
            color = Color.White,
            fontSize = 16.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}
