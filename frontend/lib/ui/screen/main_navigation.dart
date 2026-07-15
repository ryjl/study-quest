import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:path_provider/path_provider.dart';
import '../../config.dart';
import '../../model/user.dart';
import '../../model/progress.dart';
import '../../model/badge.dart';
import '../../service/api_service.dart';
import '../../service/auth_service.dart';
import '../../service/update_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/glass_panel.dart';
import '../widget/dot_pattern_background.dart';
import '../widget/button_3d.dart';
import '../responsive.dart';
import 'course_list_screen.dart';
import 'reading_room_screen.dart';
import 'entertainment_screen.dart';
import 'login_screen.dart';

class MainNavigation extends StatefulWidget {
  final int initialTabIndex;
  const MainNavigation({Key? key, this.initialTabIndex = 0}) : super(key: key);

  @override
  State<MainNavigation> createState() => _MainNavigationState();
}

class _MainNavigationState extends State<MainNavigation> {
  late int _selectedTab;
  final TextEditingController _ipController = TextEditingController();
  UserPoint? _userPoint;
  bool _loadingPoints = true;
  bool _isSavingIp = false;

  @override
  void initState() {
    super.initState();
    _selectedTab = widget.initialTabIndex;
    _ipController.text = AppConfig.baseUrl;
    _loadUserPoints();
    // Non-blocking OTA check: runs once after login. Errors are swallowed
    // inside the service, so this can never break app startup.
    _checkForUpdate();
  }

  // Checks the server for a newer APK build and shows an update dialog if one
  // exists. Force-update builds show a non-dismissible dialog.
  void _checkForUpdate() async {
    await Future.delayed(const Duration(milliseconds: 500)); // let UI settle
    if (!mounted) return;
    try {
      final update = await AppUpdateService.checkForUpdate();
      if (mounted && update.hasUpdate) {
        _showUpdateDialog(update);
      }
    } catch (_) {
      // Swallow: update checks are best-effort.
    }
  }

  void _showUpdateDialog(UpdateInfo update) {
    showDialog(
      context: context,
      // Force-update builds are NOT dismissible: the user must install before
      // they can use the app (used for critical fixes). Regular updates can be
      // skipped.
      barrierDismissible: !update.forceUpdate,
      builder: (ctx) => _UpdateDialog(update: update, forceUpdate: update.forceUpdate),
    );
  }

  @override
  void dispose() {
    _ipController.dispose();
    super.dispose();
  }

  void _loadUserPoints() async {
    final auth = Provider.of<AuthService>(context, listen: false);
    final user = auth.currentUser;
    if (user != null) {
      try {
        final pts = await ApiService.fetchUserPoints(user.id);
        if (mounted) {
          setState(() {
            _userPoint = pts;
            _loadingPoints = false;
          });
        }
      } catch (_) {
        if (mounted) {
          setState(() => _loadingPoints = false);
        }
      }
    }
  }

  void _onLogout() async {
    final auth = Provider.of<AuthService>(context, listen: false);
    await auth.logout();
    if (!mounted) return;
    Navigator.pushReplacement(
      context,
      MaterialPageRoute(builder: (context) => const LoginScreen()),
    );
  }

  void _saveIP() async {
    setState(() => _isSavingIp = true);
    await AppConfig.setBaseUrl(_ipController.text);
    setState(() => _isSavingIp = false);
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('服务器 IP 地址更新成功！')),
    );
  }

  @override
  Widget build(BuildContext context) {
    final auth = Provider.of<AuthService>(context);
    final user = auth.currentUser;

    if (isPortrait(context)) {
      // Portrait (tablet rotated, or handset): bottom nav bar + compact user
      // header on top of the content. Replaces the 280px sidebar, which crowds
      // a portrait screen.
      return Scaffold(
        body: SafeArea(
          child: DotPatternBackground(
            child: Column(
              children: [
                if (user != null) _buildCompactHeader(user),
                Expanded(
                  child: _buildCurrentScreen(user?.id ?? 0),
                ),
              ],
            ),
          ),
        ),
        bottomNavigationBar: _buildBottomNav(),
      );
    }

    // Wide (tablet landscape): full 280px sidebar + content.
    return Scaffold(
      body: SafeArea(
        child: Row(
          children: [
            // Sidebar (w-280, bg-white, border-r)
            _buildSidebar(user),

            // Main content with DotPatternBackground
            Expanded(
              child: DotPatternBackground(
                child: _buildCurrentScreen(user?.id ?? 0),
              ),
            ),
          ],
        ),
      ),
    );
  }

  /// Bottom navigation bar for narrow/portrait layouts. Mirrors the four tabs
  /// in the sidebar ([_buildNavItem] order).
  Widget _buildBottomNav() {
    return BottomNavigationBar(
      type: BottomNavigationBarType.fixed,
      currentIndex: _selectedTab,
      selectedItemColor: const Color(0xFF2563EB),
      unselectedItemColor: AppTheme.textMuted,
      selectedFontSize: 12,
      unselectedFontSize: 12,
      onTap: (i) {
        setState(() => _selectedTab = i);
        _loadUserPoints();
      },
      items: const [
        BottomNavigationBarItem(
            icon: Icon(Icons.school_rounded), label: '学习大厅'),
        BottomNavigationBarItem(
            icon: Icon(Icons.menu_book_rounded), label: '阅读室'),
        BottomNavigationBarItem(
            icon: Icon(Icons.explore_rounded), label: '成长足迹'),
        BottomNavigationBarItem(
            icon: Icon(Icons.settings_rounded), label: '系统设置'),
      ],
    );
  }

  /// Compact horizontal user header for portrait layouts. Shows avatar, level,
  /// nickname and points in a single row — a slimmed-down version of the
  /// sidebar's profile card.
  Widget _buildCompactHeader(User user) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      decoration: const BoxDecoration(
        color: Colors.white,
        border: Border(bottom: BorderSide(color: Color(0xFFF1F5F9), width: 1.5)),
      ),
      child: Row(
        children: [
          // Avatar + level badge
          Stack(
            clipBehavior: Clip.none,
            children: [
              Container(
                width: 38,
                height: 38,
                padding: const EdgeInsets.all(2.0),
                decoration: const BoxDecoration(
                  shape: BoxShape.circle,
                  gradient: LinearGradient(
                    colors: [Color(0xFF60A5FA), Color(0xFFC084FC)],
                  ),
                ),
                child: Container(
                  decoration: const BoxDecoration(
                    shape: BoxShape.circle,
                    color: Colors.white,
                  ),
                  padding: const EdgeInsets.all(1.5),
                  child: ClipOval(
                    child: user.avatarUrl.isNotEmpty
                        ? Image.network(user.avatarUrl, fit: BoxFit.cover)
                        : const Icon(Icons.person, color: AppTheme.textMuted, size: 20),
                  ),
                ),
              ),
              Positioned(
                bottom: -4,
                right: -2,
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 1),
                  decoration: BoxDecoration(
                    gradient: const LinearGradient(
                      colors: [Color(0xFFFB923C), Color(0xFFFACC15)],
                    ),
                    borderRadius: BorderRadius.circular(6),
                    border: Border.all(color: Colors.white, width: 1),
                  ),
                  child: Text(
                    'Lv.${(_userPoint?.currentPoints ?? 0) ~/ 100 + 1}',
                    style: const TextStyle(
                      color: Colors.white,
                      fontSize: 8,
                      fontWeight: FontWeight.w900,
                    ),
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  user.nickname,
                  style: const TextStyle(
                    fontWeight: FontWeight.w900,
                    fontSize: 14,
                    color: AppTheme.textWhite,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 2),
                Text(
                  user.role == 'student' ? '四年级' : '家长',
                  style: const TextStyle(
                    fontSize: 10,
                    color: AppTheme.textMuted,
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ],
            ),
          ),
          // Points badge
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
            decoration: BoxDecoration(
              color: const Color(0xFFFFEDD5),
              borderRadius: BorderRadius.circular(12),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Icon(Icons.star_rounded, color: Color(0xFFF97316), size: 14),
                const SizedBox(width: 4),
                Text(
                  '${_userPoint?.currentPoints ?? 0}',
                  style: const TextStyle(
                    color: Color(0xFFF97316),
                    fontWeight: FontWeight.w900,
                    fontSize: 14,
                    fontFamily: 'Quicksand',
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildSidebar(User? user) {
    return Container(
      width: 280,
      decoration: const BoxDecoration(
        color: Colors.white,
        border: Border(
          right: BorderSide(color: Color(0xFFF1F5F9), width: 2.0),
        ),
        boxShadow: [
          BoxShadow(
            color: Color(0x0800000),
            blurRadius: 30,
            offset: Offset(8, 0),
          ),
        ],
      ),
      child: LayoutBuilder(
        builder: (context, constraints) {
          return SingleChildScrollView(
            child: ConstrainedBox(
              constraints: BoxConstraints(minHeight: constraints.maxHeight),
              child: IntrinsicHeight(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    // Brand Logo
                    Padding(
                      padding: const EdgeInsets.only(top: 32, bottom: 16, left: 32, right: 32),
                      child: Row(
                        children: [
                          Container(
                            width: 40,
                            height: 40,
                            decoration: BoxDecoration(
                              gradient: const LinearGradient(
                                colors: [Color(0xFF3B82F6), Color(0xFF6366F1)],
                                begin: Alignment.topLeft,
                                end: Alignment.bottomRight,
                              ),
                              borderRadius: BorderRadius.circular(12),
                              boxShadow: [
                                BoxShadow(
                                  color: const Color(0xFF3B82F6).withOpacity(0.3),
                                  blurRadius: 10,
                                  offset: const Offset(0, 4),
                                )
                              ],
                            ),
                            child: const Icon(
                              Icons.rocket_launch,
                              color: Colors.white,
                              size: 20,
                            ),
                          ),
                          const SizedBox(width: 12),
                          const Text(
                            '学途奇旅',
                            style: TextStyle(
                              fontFamily: 'Quicksand',
                              fontWeight: FontWeight.w900,
                              fontSize: 20,
                              color: AppTheme.textWhite,
                              letterSpacing: -0.5,
                            ),
                          ),
                        ],
                      ),
                    ),

                    // Profile Container (bouncy gradient card)
                    if (user != null)
                      Container(
                        margin: const EdgeInsets.symmetric(horizontal: 20, vertical: 16),
                        padding: const EdgeInsets.all(20),
                        decoration: BoxDecoration(
                          gradient: const LinearGradient(
                            colors: [Color(0xFFEFF6FF), Colors.white],
                            begin: Alignment.topCenter,
                            end: Alignment.bottomCenter,
                          ),
                          borderRadius: BorderRadius.circular(24),
                          border: Border.all(color: const Color(0xFFDBEAFE), width: 1.0),
                        ),
                        child: Column(
                          children: [
                            Row(
                              children: [
                                Stack(
                                  clipBehavior: Clip.none,
                                  children: [
                                    Container(
                                      width: 64,
                                      height: 64,
                                      padding: const EdgeInsets.all(3.0),
                                      decoration: const BoxDecoration(
                                        shape: BoxShape.circle,
                                        gradient: LinearGradient(
                                          colors: [Color(0xFF60A5FA), Color(0xFFC084FC)],
                                        ),
                                      ),
                                      child: Container(
                                        decoration: const BoxDecoration(
                                          shape: BoxShape.circle,
                                          color: Colors.white,
                                        ),
                                        padding: const EdgeInsets.all(2.0),
                                        child: ClipOval(
                                          child: user.avatarUrl.isNotEmpty
                                              ? Image.network(user.avatarUrl, fit: BoxFit.cover)
                                              : const Icon(Icons.person, color: AppTheme.textMuted),
                                        ),
                                      ),
                                    ),
                                    Positioned(
                                      bottom: -6,
                                      right: -4,
                                      child: Container(
                                        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                                        decoration: BoxDecoration(
                                          gradient: const LinearGradient(
                                            colors: [Color(0xFFFB923C), Color(0xFFFACC15)],
                                          ),
                                          borderRadius: BorderRadius.circular(10),
                                          border: Border.all(color: Colors.white, width: 1.5),
                                        ),
                                        child: Text(
                                          'Lv.${(_userPoint?.currentPoints ?? 0) ~/ 100 + 1}',
                                          style: const TextStyle(
                                            color: Colors.white,
                                            fontSize: 9,
                                            fontWeight: FontWeight.w900,
                                          ),
                                        ),
                                      ),
                                    ),
                                  ],
                                ),
                                const SizedBox(width: 14),
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment: CrossAxisAlignment.start,
                                    children: [
                                      Text(
                                        user.nickname,
                                        style: const TextStyle(
                                          fontWeight: FontWeight.w900,
                                          fontSize: 18,
                                          color: AppTheme.textWhite,
                                        ),
                                        overflow: TextOverflow.ellipsis,
                                      ),
                                      const SizedBox(height: 4),
                                      Container(
                                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                                        decoration: BoxDecoration(
                                          color: const Color(0xFFE2E8F0),
                                          borderRadius: BorderRadius.circular(6),
                                        ),
                                        child: Text(
                                          user.role == 'student' ? '四年级' : '家长',
                                          style: const TextStyle(
                                            fontSize: 10,
                                            color: AppTheme.textMuted,
                                            fontWeight: FontWeight.bold,
                                          ),
                                        ),
                                      ),
                                    ],
                                  ),
                                ),
                              ],
                            ),
                            const SizedBox(height: 20),
                            // Points Badge white card
                            Container(
                              padding: const EdgeInsets.all(12),
                              decoration: BoxDecoration(
                                color: Colors.white,
                                borderRadius: BorderRadius.circular(16),
                                border: Border.all(color: const Color(0xFFE2E8F0)),
                                boxShadow: [
                                  BoxShadow(
                                    color: const Color(0xFF0F172A).withOpacity(0.04),
                                    blurRadius: 8,
                                    offset: const Offset(0, 2),
                                  ),
                                ],
                              ),
                              child: Row(
                                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                                children: [
                                  Row(
                                    children: [
                                      Container(
                                        width: 32,
                                        height: 32,
                                        decoration: const BoxDecoration(
                                          color: Color(0xFFFFEDD5),
                                          shape: BoxShape.circle,
                                        ),
                                        child: const Icon(Icons.star_rounded, color: Color(0xFFF97316), size: 18),
                                      ),
                                      const SizedBox(width: 8),
                                      const Text(
                                        '我的积分',
                                        style: TextStyle(
                                          fontWeight: FontWeight.w800,
                                          fontSize: 13,
                                          color: AppTheme.textWhite,
                                        ),
                                      ),
                                    ],
                                  ),
                                  Text(
                                    '${_userPoint?.currentPoints ?? 0}',
                                    style: const TextStyle(
                                      color: Color(0xFFF97316),
                                      fontWeight: FontWeight.w900,
                                      fontSize: 18,
                                      fontFamily: 'Quicksand',
                                    ),
                                  ),
                                ],
                              ),
                            ),
                          ],
                        ),
                      ),

                    // Menu Navigation Tabs
                    const SizedBox(height: 12),
                    _buildNavItem(0, Icons.school_rounded, '学习大厅'),
                    _buildNavItem(1, Icons.menu_book_rounded, '阅读室'),
                    _buildNavItem(2, Icons.explore_rounded, '成长足迹'),
                    _buildNavItem(3, Icons.settings_rounded, '系统设置'),

                    const Spacer(),

                    // Logout Button
                    Padding(
                      padding: const EdgeInsets.all(20.0),
                      child: FocusButton(
                        baseColor: Colors.transparent,
                        borderColor: Colors.transparent,
                        onPressed: _onLogout,
                        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
                        child: Row(
                          mainAxisAlignment: MainAxisAlignment.center,
                          children: const [
                            Icon(Icons.logout_rounded, color: Color(0xFF94A3B8), size: 18),
                            SizedBox(width: 8),
                            Text(
                              '退出当前账号',
                              style: TextStyle(
                                color: Color(0xFF94A3B8),
                                fontWeight: FontWeight.bold,
                                fontSize: 14,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          );
        },
      ),
    );
  }

  Widget _buildNavItem(int index, IconData icon, String label) {
    final active = _selectedTab == index;
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 20, vertical: 4),
      child: FocusButton(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        borderRadius: 20,
        baseColor: active ? const Color(0xFFEFF6FF) : Colors.transparent,
        borderColor: active ? const Color(0xFFDBEAFE) : Colors.transparent,
        onPressed: () {
          setState(() => _selectedTab = index);
          _loadUserPoints();
        },
        child: Row(
          children: [
            if (active)
              Container(
                width: 5,
                height: 24,
                decoration: BoxDecoration(
                  color: const Color(0xFF3B82F6),
                  borderRadius: BorderRadius.circular(3),
                ),
              ),
            if (active) const SizedBox(width: 12) else const SizedBox(width: 17),
            Icon(
              icon,
              color: active ? const Color(0xFF2563EB) : AppTheme.textMuted,
              size: 22,
            ),
            const SizedBox(width: 16),
            Expanded(
              child: Text(
                label,
                style: TextStyle(
                  fontWeight: FontWeight.bold,
                  fontSize: 15,
                  color: active ? const Color(0xFF2563EB) : AppTheme.textMuted,
                  fontFamily: 'Quicksand',
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildCurrentScreen(int activeUserId) {
    switch (_selectedTab) {
      case 0:
        return CourseListScreen(activeUserId: activeUserId);
      case 1:
        return ReadingRoomScreen(activeUserId: activeUserId);
      case 2:
        return _buildProgressScreen(activeUserId);
      case 3:
        return _buildSettingsScreen();
      default:
        return CourseListScreen(activeUserId: activeUserId);
    }
  }

  // 1. Growth Footprints Dashboard Screen
  Widget _buildProgressScreen(int activeUserId) {
    return FutureBuilder(
      future: Future.wait([
        ApiService.fetchUserPoints(activeUserId),
        ApiService.fetchProgressOverview(activeUserId),
        ApiService.fetchPointsLedger(activeUserId, limit: 8),
        ApiService.fetchUserBadges(activeUserId),
      ]),
      builder: (context, AsyncSnapshot<List<dynamic>> snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const Center(child: CircularProgressIndicator(color: AppTheme.primaryColor));
        }
        if (snapshot.hasError) {
          return Center(
            child: Text(
              '获取足迹数据失败: ${snapshot.error}',
              style: const TextStyle(color: Colors.redAccent, fontWeight: FontWeight.bold),
            ),
          );
        }

        final userPoint = snapshot.data![0] as UserPoint;
        final progressList = snapshot.data![1] as List<UserProgress>;
        final rawLedger = snapshot.data![2] as List<PointsLedger>;
        final badges = snapshot.data![3] as List<BadgeStatus>;

        // Curate the timeline: show badge unlocks/ups FIRST (they're the
        // highlights), then at most 2 recent video-completion rows (so the
        // list isn't dominated by repetitive "完成视频" entries). Keep the
        // original recency order within each group.
        final badgeEntries = rawLedger.where((e) => e.reasonType == 'badge_unlocked').take(4).toList();
        final watchEntries = rawLedger.where((e) => e.reasonType == 'system_watch').take(2).toList();
        final ledger = [...badgeEntries, ...watchEntries];

        final completedCount = progressList.where((p) => p.isCompleted).length;

        // Real accumulated study minutes from watch-seconds across all episodes.
        final totalWatchSeconds =
            progressList.fold<int>(0, (sum, p) => sum + p.watchSeconds);
        final studyMinutes = (totalWatchSeconds / 60).round();
        // Star counting: each unlocked tier = 1 star. Multi-tier badges
        // contribute (tier+1) stars (e.g. reached tier 2 = 3 stars); single-
        // tier badges contribute 1. This is more granular and rewarding than
        // "unlocked X/Y badges" — a child sees progress on every tier clear.
        final unlockedStars = badges.fold<int>(0, (sum, b) => sum + (b.unlocked ? (b.tier + 1) : 0));
        final totalStars = badges.fold<int>(0, (sum, b) => sum + b.tierCount);

        return SingleChildScrollView(
          padding: portraitAwarePadding(context),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Header title
              Text(
                '成长足迹',
                style: TextStyle(
                  fontFamily: 'Quicksand',
                  fontSize: isPortrait(context) ? 22 : 28,
                  fontWeight: FontWeight.w900,
                  color: AppTheme.textWhite,
                ),
              ),
              const SizedBox(height: 6),
              Text(
                '看看你取得了多少成就！🏅',
                style: TextStyle(
                  fontSize: 14,
                  color: AppTheme.textMuted,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: 32),

              // 3 Gradient Metric Cards — row on wide screens, stacked column on
              // narrow (portrait) screens so each card gets full width.
              _buildMetricCardsRow(
                context,
                userPoint: userPoint,
                studyMinutes: studyMinutes,
                completedCount: completedCount,
              ),
              SizedBox(height: isPortrait(context) ? 24 : 40),

              // Bottom Grid: Left Timeline + Right Badges.
              // Wide: side-by-side (timeline flex 2, badges flex 1).
              // Narrow (portrait): stacked vertically, each full width.
              Builder(builder: (context) {
                final timeline = _buildTimelinePanel(context, ledger);
                final badgeWall = _buildBadgeWallPanel(context, badges, unlockedStars, totalStars);
                if (isPortrait(context)) {
                  return Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      timeline,
                      const SizedBox(height: 24),
                      badgeWall,
                    ],
                  );
                }
                return Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(flex: 2, child: timeline),
                    const SizedBox(width: 28),
                    Expanded(flex: 1, child: badgeWall),
                  ],
                );
              }),
            ],
          ),
        );
      },
    );
  }

  /// The 3 gradient metric cards (积分 / 时长 / 通关). Wide: a single Row of 3
  /// Expanded cards. Narrow: stacked vertically, each full-width.
  Widget _buildMetricCardsRow(
    BuildContext context, {
    required UserPoint userPoint,
    required int studyMinutes,
    required int completedCount,
  }) {
    final compact = isPortrait(context);
    final gap = compact ? const SizedBox(height: 16) : const SizedBox(width: 24);

    Widget card1 = _metricCard(
      gradient: const [Color(0xFFF97316), Color(0xFFFB923C)],
      shadowColor: const Color(0xFFF97316),
      label: '累计获得积分',
      labelColor: const Color(0xFFFFEDD5),
      icon: Icons.star_rounded,
      value: '${userPoint.totalEarnedPoints}',
      compact: compact,
    );
    Widget card2 = _metricCard(
      gradient: const [Color(0xFF3B82F6), Color(0xFF60A5FA)],
      shadowColor: const Color(0xFF3B82F6),
      label: '专注学习时长',
      labelColor: const Color(0xFFEFF6FF),
      icon: Icons.watch_later_rounded,
      value: '$studyMinutes',
      unit: '分钟',
      compact: compact,
    );
    Widget card3 = _metricCard(
      gradient: const [Color(0xFF10B981), Color(0xFF34D399)],
      shadowColor: const Color(0xFF10B981),
      label: '已圆满通关',
      labelColor: const Color(0xFFECFDF5),
      icon: Icons.check_circle_rounded,
      value: '$completedCount',
      unit: '门课',
      compact: compact,
    );

    if (compact) {
      return Column(children: [card1, gap, card2, gap, card3]);
    }
    return Row(
      children: [
        Expanded(child: card1),
        gap,
        Expanded(child: card2),
        gap,
        Expanded(child: card3),
      ],
    );
  }

  /// One gradient metric card. [unit] is optional (积分 card has none).
  Widget _metricCard({
    required List<Color> gradient,
    required Color shadowColor,
    required String label,
    required Color labelColor,
    required IconData icon,
    required String value,
    String? unit,
    required bool compact,
  }) {
    return Container(
      padding: EdgeInsets.all(compact ? 20 : 28),
      decoration: BoxDecoration(
        gradient: LinearGradient(colors: gradient),
        borderRadius: BorderRadius.circular(compact ? 24 : 32),
        border: Border.all(color: Colors.white, width: 2),
        boxShadow: [
          BoxShadow(
            color: shadowColor.withOpacity(0.2),
            blurRadius: 20,
            offset: const Offset(0, 8),
          )
        ],
      ),
      child: Stack(
        children: [
          Positioned(
            right: -10,
            bottom: -10,
            child: Icon(
              icon,
              color: Colors.white.withOpacity(0.2),
              size: compact ? 56 : 80,
            ),
          ),
          Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                label,
                style: TextStyle(color: labelColor, fontWeight: FontWeight.bold, fontSize: 14),
              ),
              const SizedBox(height: 12),
              Row(
                crossAxisAlignment: CrossAxisAlignment.baseline,
                textBaseline: TextBaseline.alphabetic,
                children: [
                  Text(
                    value,
                    style: TextStyle(
                      fontSize: compact ? 32 : 40,
                      fontWeight: FontWeight.w900,
                      color: Colors.white,
                      fontFamily: 'Quicksand',
                    ),
                  ),
                  if (unit != null) ...[
                    const SizedBox(width: 6),
                    Text(
                      unit,
                      style: const TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.bold),
                    ),
                  ],
                ],
              ),
            ],
          ),
        ],
      ),
    );
  }

  /// Left bento panel: recent points-ledger activity (timeline).
  Widget _buildTimelinePanel(BuildContext context, List<PointsLedger> ledger) {
    return GlassPanel(
      padding: EdgeInsets.all(isPortrait(context) ? 20 : 32),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                padding: const EdgeInsets.all(8),
                decoration: BoxDecoration(
                  color: const Color(0xFFEFF6FF),
                  borderRadius: BorderRadius.circular(12),
                ),
                child: const Icon(Icons.history_rounded, color: Color(0xFF2563EB), size: 24),
              ),
              const SizedBox(width: 14),
              const Text(
                '最近动态',
                style: TextStyle(fontSize: 22, fontWeight: FontWeight.w900, color: AppTheme.textWhite),
              ),
            ],
          ),
          const SizedBox(height: 32),

          // Dynamic timeline items
          if (ledger.isEmpty)
            const SizedBox(
              height: 200,
              child: Center(
                child: Text('暂无最近活动', style: TextStyle(color: AppTheme.textMuted)),
              ),
            )
          else
            ListView.builder(
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              itemCount: ledger.take(6).length,
              itemBuilder: (context, index) {
                final item = ledger[index];
                final isGain = item.changeAmount > 0;
                final isNeutral = item.changeAmount == 0;

                return Container(
                  margin: const EdgeInsets.only(bottom: 24),
                  child: Row(
                    children: [
                      Container(
                        width: 44,
                        height: 44,
                        decoration: BoxDecoration(
                          color: const Color(0xFFEFF6FF),
                          shape: BoxShape.circle,
                          border: Border.all(color: Colors.white, width: 3),
                          boxShadow: const [
                            BoxShadow(color: Colors.black12, blurRadius: 4)
                          ],
                        ),
                        child: Icon(
                          _ledgerIcon(item.reasonType),
                          color: isGain
                              ? AppTheme.accentGreen
                              : (isNeutral
                                  ? AppTheme.primaryColor
                                  : Colors.redAccent),
                        ),
                      ),
                      const SizedBox(width: 16),
                      Expanded(
                        child: Container(
                          padding: const EdgeInsets.all(16),
                          decoration: BoxDecoration(
                            color: const Color(0xFFF8FAFC),
                            borderRadius: BorderRadius.circular(20),
                            border: Border.all(color: const Color(0xFFE2E8F0)),
                          ),
                          child: Row(
                            mainAxisAlignment: MainAxisAlignment.spaceBetween,
                            children: [
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(
                                      item.description.isEmpty
                                          ? _ledgerFallbackTitle(item.reasonType)
                                          : item.description,
                                      style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 13),
                                      maxLines: 2,
                                      overflow: TextOverflow.ellipsis,
                                    ),
                                    const SizedBox(height: 4),
                                    Text(
                                      _formatLedgerTime(item.createdAt),
                                      style: const TextStyle(color: AppTheme.textMuted, fontSize: 11, fontWeight: FontWeight.bold),
                                    ),
                                  ],
                                ),
                              ),
                              Container(
                                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                                decoration: BoxDecoration(
                                  color: isGain
                                      ? const Color(0xFFFFEDD5)
                                      : const Color(0xFFF1F5F9),
                                  borderRadius: BorderRadius.circular(10),
                                  border: Border.all(
                                    color: isGain
                                        ? const Color(0xFFFFDBB5)
                                        : const Color(0xFFE2E8F0),
                                  ),
                                ),
                                child: Text(
                                  isNeutral ? '—' : '${item.changeAmount > 0 ? '+' : ''}${item.changeAmount}',
                                  style: TextStyle(
                                    color: isGain
                                        ? const Color(0xFFF97316)
                                        : AppTheme.textMuted,
                                    fontWeight: FontWeight.w900,
                                    fontSize: 13,
                                  ),
                                ),
                              ),
                            ],
                          ),
                        ),
                      ),
                    ],
                  ),
                );
              },
            ),
        ],
      ),
    );
  }

  /// Right bento panel: honor wall (badge list).
  Widget _buildBadgeWallPanel(BuildContext context, List<BadgeStatus> badges, int unlockedStars, int totalStars) {
    return GlassPanel(
      padding: EdgeInsets.all(isPortrait(context) ? 20 : 32),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                padding: const EdgeInsets.all(8),
                decoration: BoxDecoration(
                  color: const Color(0xFFFEF3C7),
                  borderRadius: BorderRadius.circular(12),
                ),
                child: const Icon(Icons.military_tech_rounded, color: Color(0xFFD97706), size: 24),
              ),
              const SizedBox(width: 14),
              const Text(
                '荣誉墙',
                style: TextStyle(fontSize: 22, fontWeight: FontWeight.w900, color: AppTheme.textWhite),
              ),
            ],
          ),
          const SizedBox(height: 32),

          // Badges widgets (real data)
          if (badges.isEmpty)
            const SizedBox(
              height: 120,
              child: Center(
                child: Text('暂无成就', style: TextStyle(color: AppTheme.textMuted)),
              ),
            )
          else
            Column(
              children: [
                for (int i = 0; i < badges.length; i++) ...[
                  _buildBadgeItem(badges[i]),
                  if (i < badges.length - 1) const SizedBox(height: 16),
                ],
              ],
            ),

          const SizedBox(height: 24),
          Button3D.white(
            onPressed: () {},
            padding: const EdgeInsets.symmetric(vertical: 14),
            child: Center(
              child: Text('⭐ $unlockedStars / $totalStars',
                  style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 16)),
            ),
          ),
        ],
      ),
    );
  }

  /// One badge row in the honor wall. Multi-tier badges show tier dots + a
  /// progress bar toward the next tier; single-tier badges show lock/unlock.
  Widget _buildBadgeItem(BadgeStatus st) {
    final unlocked = st.unlocked;
    final icon = _badgeIcon(st.badge.iconName, st.badge.ruleType);
    final color = _badgeColor(st.badge.ruleType);
    final bgColor = _badgeBgColor(st.badge.ruleType);
    final multiTier = st.badge.isMultiTier;

    return Opacity(
      opacity: unlocked ? 1.0 : 0.55,
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: Colors.white,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: unlocked ? const Color(0xFFE2E8F0) : const Color(0xFFF1F5F9),
            width: 2,
          ),
        ),
        child: Row(
          children: [
            Container(
              width: 48,
              height: 48,
              decoration: BoxDecoration(
                color: bgColor,
                borderRadius: BorderRadius.circular(16),
              ),
              child: Icon(icon, color: color, size: 26),
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Flexible(
                        child: Text(
                          st.badge.title,
                          style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 15, color: AppTheme.textWhite),
                        ),
                      ),
                      if (multiTier && unlocked && st.tier >= st.tierCount - 1) ...[
                        const SizedBox(width: 6),
                        const Text('👑', style: TextStyle(fontSize: 14)),
                      ],
                    ],
                  ),
                  const SizedBox(height: 4),
                  Text(
                    st.badge.description,
                    style: const TextStyle(color: AppTheme.textMuted, fontSize: 11, fontWeight: FontWeight.bold),
                  ),
                  if (multiTier) ...[
                    const SizedBox(height: 8),
                    _buildTierProgress(st),
                  ],
                ],
              ),
            ),
            if (!multiTier && !unlocked)
              const Icon(Icons.lock_rounded, color: Color(0xFF94A3B8), size: 16),
          ],
        ),
      ),
    );
  }

  /// Tier dots (●●●○○) + a progress bar showing progress → next tier.
  Widget _buildTierProgress(BadgeStatus st) {
    final cleared = st.tier + 1; // 0 if none cleared
    final total = st.tierCount;
    final maxed = st.tier >= 0 && st.tier >= total - 1;

    // Dots: filled for cleared tiers, empty for remaining.
    final dots = Row(
      children: [
        for (int i = 0; i < total; i++) ...[
          if (i > 0) const SizedBox(width: 4),
          Icon(
            i <= st.tier ? Icons.circle : Icons.circle_outlined,
            size: 8,
            color: i <= st.tier ? _badgeColor(st.badge.ruleType) : const Color(0xFFCBD5E1),
          ),
        ],
        const SizedBox(width: 8),
        Text(
          maxed ? '满级' : '$cleared/$total',
          style: TextStyle(
            fontSize: 10,
            fontWeight: FontWeight.w900,
            color: maxed ? const Color(0xFFF59E0B) : AppTheme.textMuted,
          ),
        ),
      ],
    );

    // Progress bar: only when not maxed and there's a next tier threshold.
    Widget? bar;
    if (!maxed && st.nextTier > 0) {
      final tiers = st.badge.parsedTiers;
      // Guard against tier index out of range (e.g. admin reduced the tier
      // count after the user already cleared a higher tier).
      final safeTier = (st.tier >= 0 && st.tier < tiers.length) ? st.tier : -1;
      final prevThreshold = safeTier >= 0 ? tiers[safeTier].t : 0;
      final span = st.nextTier - prevThreshold;
      final frac = span > 0 ? ((st.progress - prevThreshold) / span).clamp(0.0, 1.0) : 0.0;
      bar = Padding(
        padding: const EdgeInsets.only(top: 6),
        child: Row(
          children: [
            Expanded(
              child: ClipRRect(
                borderRadius: BorderRadius.circular(4),
                child: LinearProgressIndicator(
                  value: frac,
                  minHeight: 5,
                  backgroundColor: const Color(0xFFF1F5F9),
                  valueColor: AlwaysStoppedAnimation<Color>(_badgeColor(st.badge.ruleType)),
                ),
              ),
            ),
            const SizedBox(width: 8),
            Text(
              '${st.progress}/${st.nextTier}',
              style: const TextStyle(fontSize: 9, color: AppTheme.textMuted, fontWeight: FontWeight.bold),
            ),
          ],
        ),
      );
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        dots,
        if (bar != null) bar,
      ],
    );
  }

  // --- Badge visual helpers: map backend rule_type/icon_name to UI tokens ---

  IconData _badgeIcon(String iconName, String ruleType) {
    // Prefer explicit icon name hints, fall back to rule type semantics.
    final name = iconName.toLowerCase();
    if (name.contains('streak') || ruleType == 'consecutive_days') {
      return Icons.local_fire_department_rounded;
    }
    if (name.contains('math')) return Icons.architecture_rounded;
    if (name.contains('english')) return Icons.translate_rounded;
    if (name.contains('first') || ruleType == 'watch_duration') {
      return Icons.timer_rounded;
    }
    if (ruleType == 'episode_completed_count') return Icons.check_circle_outline_rounded;
    if (ruleType == 'points_earned') return Icons.stars_rounded;
    if (ruleType == 'course_completion') return Icons.emoji_events_rounded;
    if (ruleType == 'weekly_all_present') return Icons.calendar_month_rounded;
    if (ruleType == 'distinct_subject_count') return Icons.explore_rounded;
    return Icons.military_tech_rounded;
  }

  Color _badgeColor(String ruleType) {
    switch (ruleType) {
      case 'consecutive_days':
        return const Color(0xFFF97316);
      case 'subject_count':
        return const Color(0xFF3B82F6);
      case 'episode_completed_count':
        return const Color(0xFF0EA5E9);
      case 'points_earned':
        return const Color(0xFF8B5CF6);
      case 'course_completion':
        return const Color(0xFFEAB308);
      case 'weekly_all_present':
        return const Color(0xFFEC4899);
      case 'distinct_subject_count':
        return const Color(0xFF14B8A6);
      case 'watch_duration':
        return AppTheme.accentGreen;
      default:
        return const Color(0xFFD97706);
    }
  }

  Color _badgeBgColor(String ruleType) {
    switch (ruleType) {
      case 'consecutive_days':
        return const Color(0xFFFFEDD5);
      case 'subject_count':
        return const Color(0xFFEFF6FF);
      case 'episode_completed_count':
        return const Color(0xFFE0F2FE);
      case 'points_earned':
        return const Color(0xFFF5F3FF);
      case 'course_completion':
        return const Color(0xFFFEF9C3);
      case 'weekly_all_present':
        return const Color(0xFFFCE7F3);
      case 'distinct_subject_count':
        return const Color(0xFFCCFBF1);
      case 'watch_duration':
        return const Color(0xFFECFDF5);
      default:
        return const Color(0xFFFEF3C7);
    }
  }

  // --- Ledger timeline helpers ---

  IconData _ledgerIcon(String reasonType) {
    switch (reasonType) {
      case 'system_watch':
        return Icons.play_circle_rounded;
      case 'badge_unlocked':
        return Icons.emoji_events_rounded;
      case 'parent_grant':
        return Icons.card_giftcard_rounded;
      case 'redeem_gift':
        return Icons.redeem_rounded;
      default:
        return Icons.history_rounded;
    }
  }

  String _ledgerFallbackTitle(String reasonType) {
    switch (reasonType) {
      case 'system_watch':
        return '完成了一次视频学习';
      case 'badge_unlocked':
        return '解锁了一个新成就';
      case 'parent_grant':
        return '家长奖励';
      case 'redeem_gift':
        return '兑换了礼物';
      default:
        return '积分变动';
    }
  }

  String _formatLedgerTime(DateTime t) {
    // Backend stores UTC; show a friendly relative-ish label.
    final now = DateTime.now();
    final diff = now.difference(t);
    if (diff.inMinutes < 1) return '刚刚';
    if (diff.inMinutes < 60) return '${diff.inMinutes} 分钟前';
    if (diff.inHours < 24) return '${diff.inHours} 小时前';
    if (diff.inDays < 7) return '${diff.inDays} 天前';
    return '${t.month}/${t.day}';
  }

  // 2. Settings Screen (to configure server URL address)
  Widget _buildSettingsScreen() {
    final compact = isPortrait(context);
    return Padding(
      padding: portraitAwarePadding(context),
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
          Text(
            '系统设置',
            style: TextStyle(fontFamily: 'Quicksand', fontSize: compact ? 22 : 28, fontWeight: FontWeight.w900, color: AppTheme.textWhite),
          ),
          const SizedBox(height: 6),
          const Text('配置你的专属学习环境 ⚙️', style: TextStyle(color: AppTheme.textMuted, fontWeight: FontWeight.bold)),
          const SizedBox(height: 32),

          // Server settings card
          Container(
            constraints: const BoxConstraints(maxWidth: 800),
            child: GlassPanel(
              padding: EdgeInsets.all(compact ? 20 : 32),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  // Paren Zone
                  Row(
                    children: [
                      Container(
                        padding: const EdgeInsets.all(8),
                        decoration: const BoxDecoration(
                          color: Color(0xFFEFF6FF),
                          shape: BoxShape.circle,
                        ),
                        child: const Icon(Icons.shield_rounded, color: Color(0xFF2563EB), size: 24),
                      ),
                      const SizedBox(width: 12),
                      const Text('高级配置（家长专区）', style: TextStyle(fontWeight: FontWeight.w900, fontSize: 18, color: AppTheme.textWhite)),
                    ],
                  ),
                  const SizedBox(height: 24),
                  const Text('后端 API 地址 (API Endpoint)', style: TextStyle(fontWeight: FontWeight.w800, fontSize: 14)),
                  const SizedBox(height: 12),
                  // Wide: input + button side by side. Narrow: stacked so the
                  // text field gets full width and the button sits below it.
                  if (compact)
                    Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        _buildIpTextField(),
                        const SizedBox(height: 12),
                        _buildSaveButton(),
                      ],
                    )
                  else
                    Row(
                      children: [
                        Expanded(child: _buildIpTextField()),
                        const SizedBox(width: 16),
                        _buildSaveButton(),
                      ],
                    ),
                  const SizedBox(height: 12),
                  Row(
                    children: const [
                      Icon(Icons.info_rounded, color: AppTheme.textMuted, size: 14),
                      SizedBox(width: 6),
                      Expanded(
                        child: Text('离开局域网时，请输入内网穿透或虚拟局域网（如 ZeroTier）的 API 地址。', style: TextStyle(color: AppTheme.textMuted, fontSize: 12, fontWeight: FontWeight.bold)),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 24),

          // Local settings card
          Container(
            constraints: const BoxConstraints(maxWidth: 800),
            child: GlassPanel(
              padding: EdgeInsets.all(compact ? 20 : 32),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Container(
                        padding: const EdgeInsets.all(8),
                        decoration: const BoxDecoration(
                          color: Color(0xFFEFF6FF),
                          shape: BoxShape.circle,
                        ),
                        child: const Icon(Icons.settings_suggest_rounded, color: Color(0xFF2563EB), size: 24),
                      ),
                      const SizedBox(width: 12),
                      const Text('播放设置（本地偏好）', style: TextStyle(fontWeight: FontWeight.w900, fontSize: 18, color: AppTheme.textWhite)),
                    ],
                  ),
                  const SizedBox(height: 24),
                  // Hardware Decoding Switch
                  FutureBuilder<bool>(
                    future: SharedPreferences.getInstance().then((prefs) => prefs.getBool('enable_hw_acceleration') ?? false),
                    builder: (context, snapshot) {
                      final isEnabled = snapshot.data ?? false;
                      return SwitchListTile(
                        contentPadding: EdgeInsets.zero,
                        title: const Text('硬件加速解码 (HW Acceleration)', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 15, color: AppTheme.textWhite)),
                        subtitle: const Text('开启后使用硬件加速解码，若播放异常请关闭', style: TextStyle(color: AppTheme.textMuted, fontSize: 12, fontWeight: FontWeight.bold)),
                        value: isEnabled,
                        activeColor: AppTheme.primaryColor,
                        onChanged: (val) async {
                          final prefs = await SharedPreferences.getInstance();
                          await prefs.setBool('enable_hw_acceleration', val);
                          setState(() {});
                        },
                      );
                    },
                  ),
                  const Divider(color: Color(0xFFE2E8F0), height: 32),
                  // Cache Cleaning
                  ListTile(
                    contentPadding: EdgeInsets.zero,
                    title: const Text('清理本地缓存空间', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 15, color: AppTheme.textWhite)),
                    subtitle: const Text('清理 PDF 讲义和临时缓存的媒体文件', style: TextStyle(color: AppTheme.textMuted, fontSize: 12, fontWeight: FontWeight.bold)),
                    trailing: Button3D.white(
                      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                      onPressed: () async {
                        try {
                          final tempDir = await getTemporaryDirectory();
                          if (tempDir.existsSync()) {
                            tempDir.deleteSync(recursive: true);
                          }
                          if (context.mounted) {
                            ScaffoldMessenger.of(context).showSnackBar(
                              const SnackBar(content: Text('本地缓存清理成功！')),
                            );
                          }
                        } catch (e) {
                          if (context.mounted) {
                            ScaffoldMessenger.of(context).showSnackBar(
                              SnackBar(content: Text('清理失败: $e')),
                            );
                          }
                        }
                      },
                      child: const Text('立即清理', style: TextStyle(fontWeight: FontWeight.bold)),
                    ),
                  ),
                ],
              ),
            ),
          ),

          // On narrow layouts the sidebar (with its logout button) is gone, so
          // surface logout here instead. Wide layouts keep logout in the
          // sidebar and skip this duplicate.
          if (compact) ...[
            const SizedBox(height: 32),
            FocusButton(
              baseColor: Colors.transparent,
              borderColor: const Color(0xFFE2E8F0),
              onPressed: _onLogout,
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: const [
                  Icon(Icons.logout_rounded, color: Color(0xFF94A3B8), size: 18),
                  SizedBox(width: 8),
                  Text(
                    '退出当前账号',
                    style: TextStyle(
                      color: Color(0xFF94A3B8),
                      fontWeight: FontWeight.bold,
                      fontSize: 14,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ],
      ),
      ),
    );
  }

  Widget _buildIpTextField() {
    return TextField(
      controller: _ipController,
      style: const TextStyle(fontFamily: 'monospace', fontSize: 15, color: AppTheme.textWhite, fontWeight: FontWeight.bold),
      decoration: InputDecoration(
        filled: true,
        fillColor: Colors.white,
        contentPadding: const EdgeInsets.symmetric(horizontal: 18, vertical: 16),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: const BorderSide(color: Color(0xFFCBD5E1), width: 1.5),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: const BorderSide(color: AppTheme.primaryColor, width: 2),
        ),
        prefixIcon: const Icon(Icons.lan, color: AppTheme.primaryColor),
        hintText: 'http://192.168.x.x:8080',
        hintStyle: const TextStyle(color: AppTheme.textMuted),
      ),
    );
  }

  Widget _buildSaveButton() {
    return Button3D.blue(
      onPressed: _saveIP,
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
      child: _isSavingIp
          ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
          : const Text('保存修改', style: TextStyle(fontWeight: FontWeight.w900, color: Colors.white)),
    );
  }
}

/// Update dialog shown when a newer APK build is available. Downloads the APK
/// with a progress bar, then hands off to the system installer.
///
/// When [forceUpdate] is true the dialog cannot be dismissed — the user must
/// install before continuing (used for critical fixes).
class _UpdateDialog extends StatefulWidget {
  final UpdateInfo update;
  final bool forceUpdate;

  const _UpdateDialog({required this.update, required this.forceUpdate});

  @override
  State<_UpdateDialog> createState() => _UpdateDialogState();
}

class _UpdateDialogState extends State<_UpdateDialog> {
  bool _downloading = false;
  int _progress = 0;
  String? _error;

  void _startDownload() async {
    setState(() {
      _downloading = true;
      _error = null;
      _progress = 0;
    });
    try {
      await AppUpdateService.downloadAndInstall(
        widget.update.downloadUrl,
        onProgress: (p) {
          if (mounted) setState(() => _progress = p);
        },
      );
      // The system installer is now showing; leave the dialog as-is. If the
      // user cancels the install, they'll re-trigger the check next launch.
    } catch (e) {
      if (mounted) {
        setState(() {
          _downloading = false;
          _error = e.toString();
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final u = widget.update;
    return PopScope(
      // Prevent back-button dismissal when force-update.
      canPop: !widget.forceUpdate,
      child: AlertDialog(
        title: Text(u.forceUpdate ? '需要更新到新版本' : '发现新版本'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('${u.versionName} (build ${u.versionCode})',
                style: const TextStyle(fontWeight: FontWeight.bold)),
            const SizedBox(height: 8),
            if (u.releaseNotes.isNotEmpty)
              ConstrainedBox(
                constraints: const BoxConstraints(maxHeight: 160),
                child: SingleChildScrollView(
                  child: Text(u.releaseNotes, style: const TextStyle(fontSize: 13)),
                ),
              ),
            if (u.downloadSize > 0)
              Padding(
                padding: const EdgeInsets.only(top: 4),
                child: Text('大小: ${_formatBytes(u.downloadSize)}',
                    style: TextStyle(fontSize: 12, color: Colors.grey[600])),
              ),
            if (_downloading) ...[
              const SizedBox(height: 16),
              LinearProgressIndicator(value: _progress / 100),
              const SizedBox(height: 4),
              Text('下载中 $_progress%', style: const TextStyle(fontSize: 12)),
            ],
            if (_error != null) ...[
              const SizedBox(height: 12),
              Text(_error!, style: const TextStyle(color: Colors.red, fontSize: 12)),
            ],
          ],
        ),
        actions: [
          if (!widget.forceUpdate && !_downloading)
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('稍后'),
            ),
          if (!_downloading)
            ElevatedButton(
              onPressed: _startDownload,
              child: const Text('立即更新'),
            ),
        ],
      ),
    );
  }
}

String _formatBytes(int n) {
  if (n < 1024) return '$n B';
  if (n < 1024 * 1024) return '${(n / 1024).toStringAsFixed(1)} KB';
  return '${(n / 1024 / 1024).toStringAsFixed(1)} MB';
}
