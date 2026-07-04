import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../../config.dart';
import '../../model/user.dart';
import '../../model/progress.dart';
import '../../service/api_service.dart';
import '../../service/auth_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/glass_panel.dart';
import '../widget/dot_pattern_background.dart';
import '../widget/button_3d.dart';
import 'course_list_screen.dart';
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

    return Scaffold(
      body: Row(
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
          _buildNavItem(1, Icons.explore_rounded, '成长足迹'),
          _buildNavItem(2, Icons.settings_rounded, '系统设置'),

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
        return _buildProgressScreen(activeUserId);
      case 2:
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

        final userPoint = snapshot.data?[0] as UserPoint;
        final progressList = snapshot.data?[1] as List<UserProgress>;
        final completedCount = progressList.where((p) => p.isCompleted).length;

        // Mock study hours (e.g. 340 minutes) for visualization
        final mockStudyMinutes = 340 + (userPoint.currentPoints % 15);

        return SingleChildScrollView(
          padding: const EdgeInsets.all(40.0),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Header title
              const Text(
                '成长足迹',
                style: TextStyle(
                  fontFamily: 'Quicksand',
                  fontSize: 28,
                  fontWeight: FontWeight.w900,
                  color: AppTheme.textWhite,
                ),
              ),
              const SizedBox(height: 6),
              const Text(
                '看看你取得了多少成就！🏅',
                style: TextStyle(
                  fontSize: 14,
                  color: AppTheme.textMuted,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: 32),

              // 3 Gradient Metric Cards
              Row(
                children: [
                  // Card 1: Star Points (Orange)
                  Expanded(
                    child: Container(
                      padding: const EdgeInsets.all(28),
                      decoration: BoxDecoration(
                        gradient: const LinearGradient(
                          colors: [Color(0xFFF97316), Color(0xFFFB923C)],
                        ),
                        borderRadius: BorderRadius.circular(32),
                        border: Border.all(color: Colors.white, width: 2),
                        boxShadow: [
                          BoxShadow(
                            color: const Color(0xFFF97316).withOpacity(0.2),
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
                              Icons.star_rounded,
                              color: Colors.white.withOpacity(0.2),
                              size: 80,
                            ),
                          ),
                          Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              const Text(
                                '累计获得积分',
                                style: TextStyle(color: Color(0xFFFFEDD5), fontWeight: FontWeight.bold, fontSize: 14),
                              ),
                              const SizedBox(height: 12),
                              Text(
                                '${userPoint.currentPoints}',
                                style: const TextStyle(
                                  fontSize: 40,
                                  fontWeight: FontWeight.w900,
                                  color: Colors.white,
                                  fontFamily: 'Quicksand',
                                ),
                              ),
                            ],
                          ),
                        ],
                      ),
                    ),
                  ),
                  const SizedBox(width: 24),

                  // Card 2: Focus Minutes (Blue)
                  Expanded(
                    child: Container(
                      padding: const EdgeInsets.all(28),
                      decoration: BoxDecoration(
                        gradient: const LinearGradient(
                          colors: [Color(0xFF3B82F6), Color(0xFF60A5FA)],
                        ),
                        borderRadius: BorderRadius.circular(32),
                        border: Border.all(color: Colors.white, width: 2),
                        boxShadow: [
                          BoxShadow(
                            color: const Color(0xFF3B82F6).withOpacity(0.2),
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
                              Icons.watch_later_rounded,
                              color: Colors.white.withOpacity(0.2),
                              size: 80,
                            ),
                          ),
                          Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              const Text(
                                '专注学习时长',
                                style: TextStyle(color: Color(0xFFEFF6FF), fontWeight: FontWeight.bold, fontSize: 14),
                              ),
                              const SizedBox(height: 12),
                              Row(
                                crossAxisAlignment: CrossAxisAlignment.baseline,
                                textBaseline: TextBaseline.alphabetic,
                                children: [
                                  Text(
                                    '$mockStudyMinutes',
                                    style: const TextStyle(
                                      fontSize: 40,
                                      fontWeight: FontWeight.w900,
                                      color: Colors.white,
                                      fontFamily: 'Quicksand',
                                    ),
                                  ),
                                  const SizedBox(width: 6),
                                  const Text(
                                    '分钟',
                                    style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.bold),
                                  ),
                                ],
                              ),
                            ],
                          ),
                        ],
                      ),
                    ),
                  ),
                  const SizedBox(width: 24),

                  // Card 3: Completed Courses (Emerald)
                  Expanded(
                    child: Container(
                      padding: const EdgeInsets.all(28),
                      decoration: BoxDecoration(
                        gradient: const LinearGradient(
                          colors: [Color(0xFF10B981), Color(0xFF34D399)],
                        ),
                        borderRadius: BorderRadius.circular(32),
                        border: Border.all(color: Colors.white, width: 2),
                        boxShadow: [
                          BoxShadow(
                            color: const Color(0xFF10B981).withOpacity(0.2),
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
                              Icons.check_circle_rounded,
                              color: Colors.white.withOpacity(0.2),
                              size: 80,
                            ),
                          ),
                          Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              const Text(
                                '已圆满通关',
                                style: TextStyle(color: Color(0xFFECFDF5), fontWeight: FontWeight.bold, fontSize: 14),
                              ),
                              const SizedBox(height: 12),
                              Row(
                                crossAxisAlignment: CrossAxisAlignment.baseline,
                                textBaseline: TextBaseline.alphabetic,
                                children: [
                                  Text(
                                    '$completedCount',
                                    style: const TextStyle(
                                      fontSize: 40,
                                      fontWeight: FontWeight.w900,
                                      color: Colors.white,
                                      fontFamily: 'Quicksand',
                                    ),
                                  ),
                                  const SizedBox(width: 6),
                                  const Text(
                                    '门课',
                                    style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.bold),
                                  ),
                                ],
                              ),
                            ],
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 40),

              // Bottom Grid: Left Timeline (Flex 2) + Right Badges (Flex 1)
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  // Left Bento Panel: Recent dynamics
                  Expanded(
                    flex: 2,
                    child: GlassPanel(
                      padding: const EdgeInsets.all(32),
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
                          if (progressList.isEmpty)
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
                              itemCount: progressList.take(4).length,
                              itemBuilder: (context, index) {
                                final item = progressList[index];
                                final isCompleted = item.isCompleted;

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
                                          isCompleted ? Icons.check_circle_rounded : Icons.play_arrow_rounded,
                                          color: isCompleted ? AppTheme.accentGreen : AppTheme.primaryColor,
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
                                                      isCompleted
                                                          ? '完成了 课时 ID: ${item.episodeId}'
                                                          : '正在挑战 课时 ID: ${item.episodeId}',
                                                      style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 14),
                                                    ),
                                                    const SizedBox(height: 4),
                                                    const Text(
                                                      '今天 14:30',
                                                      style: TextStyle(color: AppTheme.textMuted, fontSize: 11, fontWeight: FontWeight.bold),
                                                    ),
                                                  ],
                                                ),
                                              ),
                                              Container(
                                                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                                                decoration: BoxDecoration(
                                                  color: const Color(0xFFFFEDD5),
                                                  borderRadius: BorderRadius.circular(10),
                                                  border: Border.all(color: const Color(0xFFFFDBB5)),
                                                ),
                                                child: Text(
                                                  isCompleted ? '+10' : '+5',
                                                  style: const TextStyle(color: Color(0xFFF97316), fontWeight: FontWeight.w900, fontSize: 13),
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
                    ),
                  ),
                  const SizedBox(width: 28),

                  // Right Bento Panel: Badge List
                  Expanded(
                    flex: 1,
                    child: GlassPanel(
                      padding: const EdgeInsets.all(32),
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

                          // Badges widgets
                          _buildBadgeItem('七日先锋', '连续登录学习7天', Icons.local_fire_department_rounded, const Color(0xFFF97316), const Color(0xFFFFEDD5), true),
                          const SizedBox(height: 16),
                          _buildBadgeItem('数学达人', '通关5次数学挑战', Icons.architecture_rounded, const Color(0xFF3B82F6), const Color(0xFFEFF6FF), true),
                          const SizedBox(height: 16),
                          _buildBadgeItem('英语之星', '看完一整部英语外文片', Icons.translate_rounded, const Color(0xFF8B5CF6), const Color(0xFFF5F3FF), false),

                          const SizedBox(height: 24),
                          Button3D.white(
                            onPressed: () {},
                            padding: const EdgeInsets.symmetric(vertical: 14),
                            child: const Center(
                              child: Text('查看所有 12 个成就', style: TextStyle(fontWeight: FontWeight.w900)),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
            ],
          ),
        );
      },
    );
  }

  Widget _buildBadgeItem(String name, String desc, IconData icon, Color color, Color bgColor, bool unlocked) {
    return Opacity(
      opacity: unlocked ? 1.0 : 0.5,
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: Colors.white,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(color: const Color(0xFFE2E8F0), width: 2),
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
                  Text(
                    name,
                    style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 15, color: AppTheme.textWhite),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    desc,
                    style: const TextStyle(color: AppTheme.textMuted, fontSize: 11, fontWeight: FontWeight.bold),
                  ),
                ],
              ),
            ),
            if (!unlocked)
              const Icon(Icons.lock_rounded, color: Color(0xFF94A3B8), size: 16),
          ],
        ),
      ),
    );
  }

  // 2. Settings Screen (to configure server URL address)
  Widget _buildSettingsScreen() {
    return Padding(
      padding: const EdgeInsets.all(40.0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            '系统设置',
            style: TextStyle(fontFamily: 'Quicksand', fontSize: 28, fontWeight: FontWeight.w900, color: AppTheme.textWhite),
          ),
          const SizedBox(height: 6),
          const Text('配置你的专属学习环境 ⚙️', style: TextStyle(color: AppTheme.textMuted, fontWeight: FontWeight.bold)),
          const SizedBox(height: 32),

          // Server settings card
          Container(
            constraints: const BoxConstraints(maxWidth: 800),
            child: GlassPanel(
              padding: const EdgeInsets.all(32),
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
                  Row(
                    children: [
                      Expanded(
                        child: TextField(
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
                        ),
                      ),
                      const SizedBox(width: 16),
                      Button3D.blue(
                        onPressed: _saveIP,
                        padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
                        child: _isSavingIp
                            ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
                            : const Text('保存修改', style: TextStyle(fontWeight: FontWeight.w900, color: Colors.white)),
                      ),
                    ],
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: const [
                      Icon(Icons.info_rounded, color: AppTheme.textMuted, size: 14),
                      SizedBox(width: 6),
                      Text('离开局域网时，请输入内网穿透或虚拟局域网（如 ZeroTier）的 API 地址。', style: TextStyle(color: AppTheme.textMuted, fontSize: 12, fontWeight: FontWeight.bold)),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}
