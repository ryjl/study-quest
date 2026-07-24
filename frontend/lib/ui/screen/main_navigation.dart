import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../../config.dart';
import '../../model/user.dart';
import '../../model/progress.dart';
import '../../service/api_service.dart';
import '../../service/auth_service.dart';
import '../../service/update_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/dot_pattern_background.dart';
import '../widget/update_dialog.dart';
import '../responsive.dart';
import 'course_list_screen.dart';
import 'reading_room_screen.dart';
import 'login_screen.dart';
import 'settings_screen.dart';
import 'growth_footprint_screen.dart';
import 'wrong_book_screen.dart';

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
  bool _isSavingIp = false;
  // 错题本未掌握数(tab 角标用)。0 不显示角标。
  int _unmasteredCount = 0;

  /// Level derived from total points: every 100 points = +1 level, starting
  /// from Lv.1. Centralised here because the badge is rendered in two places
  /// (compact header + sidebar).
  int _levelForPoints() => (_userPoint?.currentPoints ?? 0) ~/ 100 + 1;

  @override
  void initState() {
    super.initState();
    _selectedTab = widget.initialTabIndex;
    _ipController.text = AppConfig.baseUrl;
    _loadUserPoints();
    _loadUnmasteredCount();
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
      builder: (ctx) => UpdateDialog(update: update, forceUpdate: update.forceUpdate),
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
          });
        }
      } catch (_) {
        // Points are best-effort; the sidebar/header fall back to Lv.1.
      }
    }
  }

  // 取错题本未掌握数刷新 tab 角标。best-effort:失败静默(角标不是核心功能)。
  void _loadUnmasteredCount() async {
    final auth = Provider.of<AuthService>(context, listen: false);
    final user = auth.currentUser;
    if (user == null) return;
    try {
      final n = await ApiService.fetchUnmasteredCount(user.id);
      if (mounted) setState(() => _unmasteredCount = n);
    } catch (_) {
      // 角标失败不阻塞。
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
      selectedItemColor: AppTheme.blue600,
      unselectedItemColor: AppTheme.textMuted,
      selectedFontSize: 12,
      unselectedFontSize: 12,
      onTap: (i) {
        setState(() => _selectedTab = i);
        _loadUserPoints();
        _loadUnmasteredCount();
      },
      items: [
        const BottomNavigationBarItem(
            icon: Icon(Icons.school_rounded), label: '学习大厅'),
        const BottomNavigationBarItem(
            icon: Icon(Icons.menu_book_rounded), label: '阅读室'),
        const BottomNavigationBarItem(
            icon: Icon(Icons.explore_rounded), label: '成长足迹'),
        // 错题本:图标用 spellcheck(语义=检查/纠错),和阅读室的 menu_book 区分;
        // 未掌握数 > 0 时带角标红点提示有题要复习。
        BottomNavigationBarItem(
            icon: Badge(
              isLabelVisible: _unmasteredCount > 0,
              label: Text('$_unmasteredCount'),
              child: const Icon(Icons.spellcheck_rounded),
            ),
            label: '错题本'),
        const BottomNavigationBarItem(
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
        border: Border(bottom: BorderSide(color: AppTheme.slate100, width: 1.5)),
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
                    'Lv.${_levelForPoints()}',
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
                const Icon(Icons.star_rounded, color: AppTheme.accentOrange, size: 14),
                const SizedBox(width: 4),
                Text(
                  '${_userPoint?.currentPoints ?? 0}',
                  style: const TextStyle(
                    color: AppTheme.accentOrange,
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
          right: BorderSide(color: AppTheme.slate100, width: 2.0),
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
                                  color: const Color(0xFF3B82F6).withValues(alpha: 0.3),
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
                            colors: [AppTheme.blue100, Colors.white],
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
                                          'Lv.${_levelForPoints()}',
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
                                          color: AppTheme.borderMuted,
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
                                border: Border.all(color: AppTheme.borderMuted),
                                boxShadow: [
                                  BoxShadow(
                                    color: AppTheme.slate900.withValues(alpha: 0.04),
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
                                        child: const Icon(Icons.star_rounded, color: AppTheme.accentOrange, size: 18),
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
                                      color: AppTheme.accentOrange,
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
                    // 错题本:图标 spellcheck 区分阅读室;未掌握数 > 0 带角标。
                    _buildNavItemWithBadge(3, Icons.spellcheck_rounded, '错题本', _unmasteredCount),
                    _buildNavItem(4, Icons.settings_rounded, '系统设置'),

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
                            Icon(Icons.logout_rounded, color: AppTheme.slate400, size: 18),
                            SizedBox(width: 8),
                            Text(
                              '退出当前账号',
                              style: TextStyle(
                                color: AppTheme.slate400,
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
    return _buildNavItemRaw(
      index: index,
      icon: icon,
      label: label,
      iconWidget: null,
    );
  }

  // _buildNavItemWithBadge 同 _buildNavItem,但图标带未掌握数角标(错题本专用)。
  Widget _buildNavItemWithBadge(int index, IconData icon, String label, int badge) {
    return _buildNavItemRaw(
      index: index,
      icon: icon,
      label: label,
      iconWidget: Badge(
        isLabelVisible: badge > 0,
        label: Text('$badge'),
        child: Icon(
          icon,
          color: _selectedTab == index ? AppTheme.blue600 : AppTheme.textMuted,
          size: 22,
        ),
      ),
    );
  }

  Widget _buildNavItemRaw({
    required int index,
    required IconData icon,
    required String label,
    required Widget? iconWidget,
  }) {
    final active = _selectedTab == index;
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 20, vertical: 4),
      child: FocusButton(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        borderRadius: 20,
        baseColor: active ? AppTheme.blue100 : Colors.transparent,
        borderColor: active ? const Color(0xFFDBEAFE) : Colors.transparent,
        onPressed: () {
          setState(() => _selectedTab = index);
          _loadUserPoints();
          _loadUnmasteredCount();
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
            iconWidget ??
                Icon(
                  icon,
                  color: active ? AppTheme.blue600 : AppTheme.textMuted,
                  size: 22,
                ),
            const SizedBox(width: 16),
            Expanded(
              child: Text(
                label,
                style: TextStyle(
                  fontWeight: FontWeight.bold,
                  fontSize: 15,
                  color: active ? AppTheme.blue600 : AppTheme.textMuted,
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
        return WrongBookScreen(
          activeUserId: activeUserId,
          onWrongBookChanged: _loadUnmasteredCount,
        );
      case 4:
        return _buildSettingsScreen();
      default:
        return CourseListScreen(activeUserId: activeUserId);
    }
  }

  // 1. Growth Footprints Dashboard Screen
  Widget _buildProgressScreen(int activeUserId) {
    return GrowthFootprintScreen(activeUserId: activeUserId);
  }


  // 2. Settings Screen (to configure server URL address)
  Widget _buildSettingsScreen() {
    return SettingsScreen(
      ipController: _ipController,
      isSavingIp: _isSavingIp,
      onSaveIp: _saveIP,
      onLogout: _onLogout,
      onPreferencesChanged: () => setState(() {}),
    );
  }
}

