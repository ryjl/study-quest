import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../../config.dart';
import '../../model/progress.dart';
import '../../service/api_service.dart';
import '../../service/auth_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
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
  bool _isSavingIp = false;

  @override
  void initState() {
    super.initState();
    _selectedTab = widget.initialTabIndex;
    _ipController.text = AppConfig.baseUrl;
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
          // Sidebar Nav Rail (D-pad optimized navigation sidebar)
          Container(
            width: 250,
            color: AppTheme.cardColor,
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 24),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                // Child profile card
                if (user != null) ...[
                  Row(
                    children: [
                      Container(
                        width: 48,
                        height: 48,
                        decoration: BoxDecoration(
                          shape: BoxShape.circle,
                          border: Border.all(color: AppTheme.primaryColor, width: 2),
                          image: user.avatarUrl.isNotEmpty
                              ? DecorationImage(
                                  image: NetworkImage(user.avatarUrl),
                                  fit: BoxFit.cover,
                                )
                              : null,
                        ),
                        child: user.avatarUrl.isEmpty
                            ? const Icon(Icons.person, color: AppTheme.textMuted)
                            : null,
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Text(
                              user.nickname,
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
                            ),
                            Text(
                              user.role.toUpperCase(),
                              style: const TextStyle(fontSize: 10, color: AppTheme.textMuted),
                            ),
                          ],
                        ),
                      )
                    ],
                  ),
                  const SizedBox(height: 24),
                  const Divider(color: Colors.white10),
                  const SizedBox(height: 24),
                ],

                // Navigation Tabs list
                _buildNavItem(0, Icons.menu_book, '学习大厅'),
                const SizedBox(height: 12),
                _buildNavItem(1, Icons.emoji_events, '我的足迹'),
                const SizedBox(height: 12),
                _buildNavItem(2, Icons.settings, '设置中心'),

                const Spacer(),

                // Logout button
                FocusButton(
                  baseColor: Colors.transparent,
                  borderColor: Colors.transparent,
                  onPressed: _onLogout,
                  child: Row(
                    children: const [
                      Icon(Icons.logout, color: Colors.redAccent, size: 20),
                      SizedBox(width: 12),
                      Text('切换账号', style: TextStyle(color: Colors.redAccent)),
                    ],
                  ),
                ),
              ],
            ),
          ),

          // Main View Content
          Expanded(
            child: Container(
              color: AppTheme.backgroundColor,
              child: _buildCurrentScreen(user?.id ?? 0),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildNavItem(int index, IconData icon, String label) {
    final active = _selectedTab == index;
    return FocusButton(
      baseColor: active ? AppTheme.primaryColor : Colors.transparent,
      borderColor: Colors.transparent,
      onPressed: () => setState(() => _selectedTab = index),
      child: Row(
        children: [
          Icon(icon, color: active ? Colors.white : AppTheme.textMuted, size: 22),
          const SizedBox(width: 16),
          Text(
            label,
            style: TextStyle(
              fontWeight: active ? FontWeight.bold : FontWeight.normal,
              color: active ? Colors.white : AppTheme.textMuted,
            ),
          ),
        ],
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

  // 1. My progress statistics screen
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
          return Center(child: Text('获取数据失败: ${snapshot.error}', style: const TextStyle(color: Colors.redAccent)));
        }

        final userPoint = snapshot.data?[0] as UserPoint;
        final progressList = snapshot.data?[1] as List<UserProgress>;
        final completedCount = progressList.where((p) => p.isCompleted).length;

        return Padding(
          padding: const EdgeInsets.all(40.0),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('成长足迹', style: TextStyle(fontSize: 32, fontWeight: FontWeight.bold)),
              const SizedBox(height: 32),

              // Summary Stats grid
              Row(
                children: [
                  // Total points card
                  Expanded(
                    child: Container(
                      padding: const EdgeInsets.all(24),
                      decoration: AppTheme.switchDecoration(hasFocus: false),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          const Text('当前积分', style: TextStyle(color: AppTheme.textMuted, fontSize: 16)),
                          const SizedBox(height: 12),
                          Row(
                            children: [
                              const Icon(Icons.stars, color: AppTheme.accentOrange, size: 36),
                              const SizedBox(width: 12),
                              Text(
                                '${userPoint.currentPoints}',
                                style: const TextStyle(fontSize: 36, fontWeight: FontWeight.bold, color: AppTheme.accentOrange),
                              ),
                            ],
                          ),
                          const SizedBox(height: 8),
                          Text('累计获得: ${userPoint.totalEarnedPoints} 分', style: const TextStyle(color: AppTheme.textMuted, fontSize: 12)),
                        ],
                      ),
                    ),
                  ),
                  const SizedBox(width: 24),

                  // Total completed lessons card
                  Expanded(
                    child: Container(
                      padding: const EdgeInsets.all(24),
                      decoration: AppTheme.switchDecoration(hasFocus: false),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          const Text('已通关课时', style: TextStyle(color: AppTheme.textMuted, fontSize: 16)),
                          const SizedBox(height: 12),
                          Row(
                            children: [
                              const Icon(Icons.task_alt, color: AppTheme.accentGreen, size: 36),
                              const SizedBox(width: 12),
                              Text(
                                '$completedCount',
                                style: const TextStyle(fontSize: 36, fontWeight: FontWeight.bold, color: AppTheme.accentGreen),
                              ),
                            ],
                          ),
                          const SizedBox(height: 8),
                          Text('已参与学习: ${progressList.length} 课时', style: const TextStyle(color: AppTheme.textMuted, fontSize: 12)),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 32),

              // Title list
              const Text('最近通关明细', style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold)),
              const SizedBox(height: 16),
              Expanded(
                child: progressList.isEmpty
                    ? const Center(child: Text('暂无学习进度记录，快去学习大厅选课吧！', style: TextStyle(color: AppTheme.textMuted)))
                    : ListView.builder(
                        itemCount: progressList.length,
                        itemBuilder: (context, index) {
                          final p = progressList[index];
                          return Container(
                            margin: const EdgeInsets.only(bottom: 12),
                            padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 16),
                            decoration: BoxDecoration(
                              color: AppTheme.cardColor,
                              borderRadius: BorderRadius.circular(12),
                              border: Border.all(color: AppTheme.borderMuted),
                            ),
                            child: Row(
                              mainAxisAlignment: MainAxisAlignment.spaceBetween,
                              children: [
                                Row(
                                  children: [
                                    Icon(
                                      p.isCompleted ? Icons.check_circle : Icons.play_circle_outline,
                                      color: p.isCompleted ? AppTheme.accentGreen : AppTheme.primaryColor,
                                    ),
                                    const SizedBox(width: 16),
                                    Text(
                                      '课时 ID: ${p.episodeId}',
                                      style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
                                    ),
                                  ],
                                ),
                                Text(
                                  p.isCompleted ? '已通关' : '学习中',
                                  style: TextStyle(
                                    color: p.isCompleted ? AppTheme.accentGreen : AppTheme.textMuted,
                                    fontWeight: FontWeight.bold,
                                  ),
                                ),
                              ],
                            ),
                          );
                        },
                      ),
              )
            ],
          ),
        );
      },
    );
  }

  // 2. Settings screen (to configure server URL address)
  Widget _buildSettingsScreen() {
    return Padding(
      padding: const EdgeInsets.all(40.0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text('设置中心', style: TextStyle(fontSize: 32, fontWeight: FontWeight.bold)),
          const SizedBox(height: 8),
          const Text('配置局域网 StudyQuest 后端服务连接参数。', style: TextStyle(color: AppTheme.textMuted)),
          const SizedBox(height: 32),

          // Server settings card
          Container(
            padding: const EdgeInsets.all(24),
            decoration: AppTheme.switchDecoration(hasFocus: false),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text('服务器 IP 与端口', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16)),
                const SizedBox(height: 8),
                const Text('输入后端服务器的 API 基准地址（例如：http://192.168.1.100:8080）', style: TextStyle(color: AppTheme.textMuted, fontSize: 12)),
                const SizedBox(height: 16),
                TextField(
                  controller: _ipController,
                  style: const TextStyle(fontFamily: 'monospace', fontSize: 16),
                  decoration: const InputDecoration(
                    border: OutlineInputBorder(),
                    prefixIcon: Icon(Icons.lan, color: AppTheme.primaryColor),
                    hintText: 'http://192.168.x.x:8080',
                  ),
                ),
                const SizedBox(height: 24),
                Row(
                  children: [
                    FocusButton(
                      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 12),
                      baseColor: AppTheme.primaryColor,
                      onPressed: _saveIP,
                      child: _isSavingIp
                          ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
                          : const Text('保存修改', style: TextStyle(fontWeight: FontWeight.bold)),
                    ),
                    const SizedBox(width: 16),
                    FocusButton(
                      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 12),
                      baseColor: Colors.transparent,
                      onPressed: () {
                        setState(() {
                          _ipController.text = AppConfig.defaultUrl;
                        });
                      },
                      child: const Text('恢复默认'),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
