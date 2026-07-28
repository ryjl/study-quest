package com.revin.studyquest.tv.ui.nav

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Explore
import androidx.compose.material.icons.rounded.School
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import com.revin.studyquest.tv.ui.footprint.FootprintScreen
import com.revin.studyquest.tv.ui.home.CourseHallScreen
import com.revin.studyquest.tv.ui.settings.SettingsScreen
import com.revin.studyquest.tv.ui.theme.brandGradient
import com.revin.studyquest.tv.ui.theme.primaryColor
import com.revin.studyquest.tv.ui.theme.slate400
import com.revin.studyquest.tv.ui.theme.slate900

// ── 路由常量 ──────────────────────────────────────────────────────────────────

/** 路由表。对照需求:login / home / footprint / settings / player/{episodeId}。 */
object Routes {
    const val LOGIN = "login"
    const val HOME = "home"
    const val FOOTPRINT = "footprint"
    const val SETTINGS = "settings"
    const val PLAYER = "player/{episodeId}"

    fun player(episodeId: Int) = "player/$episodeId"
}

/**
 * 应用导航骨架 —— TV 端顶层路由表。
 *
 * 结构(对照 PAD `main_navigation.dart` 横屏 sidebar 280px + 右侧内容区):
 *   - `login` / `player/{episodeId}`:全屏(无 sidebar),走独立路由。
 *   - `home` / `footprint` / `settings`:共享 [MainShell](左侧 sidebar +
 *     右侧内容区),sidebar 三 tab D-pad 切换。
 *
 * 砍掉的 PAD tab:阅读室、错题本(TV 不要,见需求)。
 *
 * sidebar tab 图标对齐 PAD AppFeature:
 *   - 学习大厅 → Icons.school_rounded (这里用 Rounded.School)
 *   - 成长足迹 → Icons.explore_rounded
 *   - 系统设置 → Icons.settings_rounded
 *
 * 注:登录态守卫(未登录跳 login)由后续阶段接入;当前默认进 home 让 UI 可见。
 */
@Composable
fun AppNav(sessionViewModel: com.revin.studyquest.tv.ui.SessionViewModel = hiltViewModel()) {
    val navController = rememberNavController()
    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentRoute = backStackEntry?.destination?.route
    val authState by sessionViewModel.authState.collectAsStateWithLifecycle()

    // 登录态守卫:单 NavHost,startDestination=LOGIN。
    //   - 已登录用户:进入 LOGIN composable 时 LaunchedEffect 检测到 Authenticated,
    //     自动 navigate(HOME) + popUpTo(LOGIN){inclusive},落到主界面。
    //   - 未登录用户:停在 LOGIN 页选人输 PIN。
    //   - 登出后:authState 变 Unauthenticated,SettingsScreen 已在 logout 后,
    //     这里监听到跳回 LOGIN。
    LaunchedEffect(authState) {
        if (authState == com.revin.studyquest.tv.ui.AuthState.Authenticated &&
            currentRoute == Routes.LOGIN
        ) {
            navController.navigate(Routes.HOME) {
                popUpTo(Routes.LOGIN) { inclusive = true }
            }
        } else if (authState == com.revin.studyquest.tv.ui.AuthState.Unauthenticated &&
            currentRoute != Routes.LOGIN && currentRoute != null
        ) {
            // 登出:回 LOGIN(清栈)
            navController.navigate(Routes.LOGIN) {
                popUpTo(0) { inclusive = true }
            }
        }
    }

    NavHost(
        navController = navController,
        startDestination = Routes.LOGIN,
    ) {
        // 登录:全屏。onSuccess 后 sessionViewModel.checkAuth() 刷新 authState,
        // 上面 LaunchedEffect 检测到 Authenticated 自动跳 HOME。
        composable(Routes.LOGIN) {
            com.revin.studyquest.tv.ui.auth.LoginScreen(
                onSuccess = { sessionViewModel.checkAuth() },
            )
        }

        // 主 shell 三个 tab(共享 sidebar)。
        composable(Routes.HOME) {
            MainShell(
                currentRoute = currentRoute,
                onNavigate = { route ->
                    navController.navigate(route) {
                        popUpTo(Routes.HOME) { saveState = true }
                        launchSingleTop = true
                        restoreState = true
                    }
                },
            ) {
                CourseHallScreen(
                    onOpenCourse = { course ->
                        navController.navigate(Routes.player(course.id))
                    },
                )
            }
        }
        composable(Routes.FOOTPRINT) {
            MainShell(
                currentRoute = currentRoute,
                onNavigate = { route ->
                    navController.navigate(route) {
                        popUpTo(Routes.HOME) { saveState = true }
                        launchSingleTop = true
                        restoreState = true
                    }
                },
            ) {
                FootprintScreen()
            }
        }
        composable(Routes.SETTINGS) {
            MainShell(
                currentRoute = currentRoute,
                onNavigate = { route ->
                    navController.navigate(route) {
                        popUpTo(Routes.HOME) { saveState = true }
                        launchSingleTop = true
                        restoreState = true
                    }
                },
            ) {
                SettingsScreen(
                    onLogout = { sessionViewModel.logout() },
                )
            }
        }

        // 播放器:全屏 ExoPlayer(阶段 3)。接 VideoPlayerScreen(真屏)。
        composable(
            route = Routes.PLAYER,
            arguments = listOf(navArgument("episodeId") { type = NavType.IntType }),
        ) { entry ->
            val episodeId = entry.arguments?.getInt("episodeId") ?: 0
            com.revin.studyquest.tv.ui.player.VideoPlayerScreen(
                episodeId = episodeId,
                onBack = { navController.popBackStack() },
            )
        }
    }
}

// ── 主 shell:sidebar + 内容区 ────────────────────────────────────────────────

/**
 * TV 横屏主布局:280dp sidebar + 右侧内容区。
 *
 * 对照 PAD `main_navigation.dart` `_buildSidebar`(280px 白底,这里改深底适配 TV)。
 * sidebar 含品牌 logo + 三个 tab(home / footprint / settings),D-pad 可聚焦切换。
 * 砍掉 PAD 的 profile card / 积分徽章 / 登出按钮(那些跟登录态 / 积分强耦合,
 * 占位阶段先不放,后续阶段在 settings 里出)。
 *
 * @param currentRoute 当前 NavHost 路由(决定哪个 tab 高亮)。
 * @param onNavigate tab 点击回调(导航层切路由)。
 * @param content 右侧内容区(各 tab 的屏)。
 */
@Composable
private fun MainShell(
    currentRoute: String?,
    onNavigate: (String) -> Unit,
    content: @Composable () -> Unit,
) {
    Row(modifier = Modifier.fillMaxSize().background(slate900)) {
        Sidebar(
            currentRoute = currentRoute,
            onNavigate = onNavigate,
            modifier = Modifier.fillMaxHeight().width(280.dp),
        )
        // 右侧内容区(深底)。各 tab 屏自带背景,这里只给容器。
        Box(modifier = Modifier.fillMaxHeight().weight(1f)) {
            content()
        }
    }
}

@Composable
private fun Sidebar(
    currentRoute: String?,
    onNavigate: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .background(slate900)
            .padding(vertical = 32.dp),
    ) {
        BrandHeader()
        Spacer(Modifier.height(32.dp))
        NavTab(
            icon = Icons.Rounded.School,
            label = "学习大厅",
            selected = currentRoute == Routes.HOME,
            onClick = { onNavigate(Routes.HOME) },
        )
        NavTab(
            icon = Icons.Rounded.Explore,
            label = "成长足迹",
            selected = currentRoute == Routes.FOOTPRINT,
            onClick = { onNavigate(Routes.FOOTPRINT) },
        )
        NavTab(
            icon = Icons.Rounded.Settings,
            label = "系统设置",
            selected = currentRoute == Routes.SETTINGS,
            onClick = { onNavigate(Routes.SETTINGS) },
        )
    }
}

/** 品牌 logo(对照 PAD sidebar 顶部 rocket_launch + "学途奇旅")。 */
@Composable
private fun BrandHeader() {
    Row(
        modifier = Modifier.padding(horizontal = 24.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier
                .size(40.dp)
                .background(brush = brandGradient, shape = RoundedCornerShape(12.dp)),
            contentAlignment = Alignment.Center,
        ) {
            Text(text = "学", color = Color.White, fontWeight = FontWeight.Bold, fontSize = 18.sp)
        }
        Spacer(Modifier.width(12.dp))
        Text(
            text = "学途奇旅",
            color = Color.White,
            fontSize = 20.sp,
            fontWeight = FontWeight.Bold,
        )
    }
}

/**
 * sidebar 导航 tab —— D-pad 可聚焦。
 *
 * 对照 PAD `_buildNavItem`:选中态左侧 primaryColor 竖条 + 图标/文字提亮。
 * TV 焦点态叠加发光环(对照 design-tokens.md 焦点视觉)。
 */
@Composable
private fun NavTab(
    icon: ImageVector,
    label: String,
    selected: Boolean,
    onClick: () -> Unit,
) {
    var focused by remember { mutableStateOf(false) }
    Surface(
        onClick = onClick,
        modifier = Modifier
            .padding(horizontal = 20.dp, vertical = 4.dp)
            .fillMaxWidth()
            .onFocusChanged { focused = it.isFocused }
            .then(
                if (focused) {
                    Modifier.shadow(
                        elevation = 16.dp,
                        shape = RoundedCornerShape(20.dp),
                        ambientColor = primaryColor.copy(alpha = 0.4f),
                        spotColor = primaryColor.copy(alpha = 0.4f),
                    )
                } else {
                    Modifier
                },
            ),
        shape = ClickableSurfaceDefaults.shape(shape = RoundedCornerShape(20.dp)),
        // 选中态用 primaryColor 弱底;聚焦态再提亮。对照 PAD active=blue100。
    ) {
        Row(
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            // 选中竖条(对照 PAD active 左侧 5x24 蓝条)。
            Box(
                modifier = Modifier
                    .width(5.dp)
                    .height(24.dp)
                    .background(
                        color = if (selected) primaryColor else Color.Transparent,
                        shape = RoundedCornerShape(3.dp),
                    ),
            )
            Spacer(Modifier.width(12.dp))
            Icon(
                imageVector = icon,
                contentDescription = null,
                tint = if (selected || focused) primaryColor else slate400,
                modifier = Modifier.size(24.dp),
            )
            Spacer(Modifier.width(16.dp))
            Text(
                text = label,
                color = if (selected || focused) primaryColor else slate400,
                fontSize = 16.sp,
                fontWeight = FontWeight.Bold,
            )
        }
    }
}
