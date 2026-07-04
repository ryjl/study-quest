import 'dart:ui';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../../model/user.dart';
import '../../service/api_service.dart';
import '../../service/auth_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/glass_panel.dart';
import '../widget/num_pad.dart';
import '../widget/dot_pattern_background.dart';
import 'main_navigation.dart';

class LoginScreen extends StatefulWidget {
  const LoginScreen({Key? key}) : super(key: key);

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  late Future<List<User>> _usersFuture;
  User? _selectedUser;
  bool _showPinPad = false;
  String _errorMessage = '';

  @override
  void initState() {
    super.initState();
    _refreshUsers();
  }

  void _refreshUsers() {
    setState(() {
      _usersFuture = ApiService.fetchUsers();
      _errorMessage = '';
    });
  }

  void _onSelectUser(User user) {
    setState(() {
      _selectedUser = user;
      _showPinPad = true;
      _errorMessage = '';
    });
  }

  void _onCancelPin() {
    setState(() {
      _showPinPad = false;
      _selectedUser = null;
    });
  }

  Future<void> _onSubmitPin(String pin) async {
    if (_selectedUser == null) return;
    
    final authService = Provider.of<AuthService>(context, listen: false);
    final success = await authService.login(_selectedUser!, pin);

    if (success) {
      if (!mounted) return;
      Navigator.pushReplacement(
        context,
        MaterialPageRoute(builder: (context) => const MainNavigation()),
      );
    } else {
      setState(() {
        _errorMessage = 'PIN 码错误，请重试！';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: DotPatternBackground(
        child: Stack(
          children: [
            // Main View Content
          SafeArea(
            child: Center(
              child: SingleChildScrollView(
                child: Padding(
                  padding: const EdgeInsets.all(32.0),
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      // LOGO & Title
                      const Text(
                        'StudyQuest',
                        style: TextStyle(
                          fontFamily: 'Quicksand',
                          fontSize: 54,
                          fontWeight: FontWeight.bold,
                          letterSpacing: 2,
                          color: AppTheme.primaryColor,
                        ),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        '学 途 奇 旅',
                        style: TextStyle(
                          fontSize: 20,
                          fontWeight: FontWeight.w600,
                          letterSpacing: 6,
                          color: AppTheme.primaryColor.withOpacity(0.7),
                        ),
                      ),
                      const SizedBox(height: 48),

                      // Users List Grid
                      FutureBuilder<List<User>>(
                        future: _usersFuture,
                        builder: (context, snapshot) {
                          if (snapshot.connectionState == ConnectionState.waiting) {
                            return const CircularProgressIndicator(color: AppTheme.primaryColor);
                          }
                          if (snapshot.hasError) {
                            return _buildErrorBox(snapshot.error.toString());
                          }
                          final users = snapshot.data ?? [];
                          if (users.isEmpty) {
                            return _buildEmptyBox();
                          }
                          return _buildUsersGrid(users);
                        },
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),

          // Overlay PIN Pad with high-blur frosted glass
          if (_showPinPad && _selectedUser != null)
            Positioned.fill(
              child: Container(
                color: Colors.black.withOpacity(0.15), // Light dim overlay
                child: BackdropFilter(
                  filter: ImageFilter.blur(sigmaX: 18, sigmaY: 18),
                  child: Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        NumPad(
                          title: '验证 ${_selectedUser!.nickname} 的 PIN 码',
                          maxDigits: 4, // standard 4-digit PIN
                          onSubmit: _onSubmitPin,
                          onCancel: _onCancelPin,
                        ),
                        if (_errorMessage.isNotEmpty) ...[
                          const SizedBox(height: 20),
                          Container(
                            padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                            decoration: BoxDecoration(
                              color: Colors.redAccent.withOpacity(0.15),
                              borderRadius: BorderRadius.circular(12),
                              border: Border.all(color: Colors.redAccent.withOpacity(0.3)),
                            ),
                            child: Text(
                              _errorMessage,
                              style: const TextStyle(color: Colors.redAccent, fontSize: 16, fontWeight: FontWeight.bold),
                            ),
                          ),
                        ],
                      ],
                    ),
                  ),
                ),
            ),
          ),
        ],
      ),
    ),
  );
}

  Widget _buildUsersGrid(List<User> users) {
    return Wrap(
      spacing: 24,
      runSpacing: 24,
      alignment: WrapAlignment.center,
      children: users.map((user) {
        return FocusButton(
          padding: const EdgeInsets.all(20),
          borderRadius: 24,
          onPressed: () => _onSelectUser(user),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              // Avatar
              Container(
                width: 90,
                height: 90,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  border: Border.all(color: Colors.white, width: 3),
                  image: user.avatarUrl.isNotEmpty
                      ? DecorationImage(
                          image: NetworkImage(user.avatarUrl),
                          fit: BoxFit.cover,
                        )
                      : null,
                ),
                child: user.avatarUrl.isEmpty
                    ? const Icon(Icons.person, size: 44, color: AppTheme.textMuted)
                    : null,
              ),
              const SizedBox(height: 14),
              // Nickname
              Text(
                user.nickname,
                style: const TextStyle(
                  fontFamily: 'Quicksand',
                  fontSize: 18,
                  fontWeight: FontWeight.bold,
                  color: AppTheme.textWhite,
                ),
              ),
              const SizedBox(height: 6),
              // Role badge
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                decoration: BoxDecoration(
                  color: Colors.black.withOpacity(0.05),
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Text(
                  user.role.toUpperCase(),
                  style: const TextStyle(fontSize: 10, color: AppTheme.textMuted, fontWeight: FontWeight.bold),
                ),
              ),
            ],
          ),
        );
      }).toList(),
    );
  }

  Widget _buildErrorBox(String error) {
    return GlassPanel(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.warning_amber_rounded, size: 48, color: Colors.redAccent),
          const SizedBox(height: 12),
          const Text(
            '无法连接到 StudyQuest 服务器',
            style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16, color: AppTheme.textWhite),
          ),
          const SizedBox(height: 8),
          const Text(
            '请检查您的局域网连接或配置正确的服务器 IP。',
            style: TextStyle(color: AppTheme.textMuted, fontSize: 14),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 24),
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              FocusButton(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                onPressed: _refreshUsers,
                child: const Text('重试连接'),
              ),
              const SizedBox(width: 16),
              FocusButton(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                baseColor: Colors.transparent,
                borderColor: AppTheme.primaryColor,
                onPressed: () {
                  Navigator.push(
                    context,
                    MaterialPageRoute(
                      builder: (context) => const MainNavigation(initialTabIndex: 2), // settings tab
                    ),
                  ).then((_) => _refreshUsers());
                },
                child: const Text('去配置 IP', style: TextStyle(color: AppTheme.primaryColor)),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildEmptyBox() {
    return GlassPanel(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.supervised_user_circle, size: 48, color: AppTheme.textMuted),
          const SizedBox(height: 12),
          const Text(
            '系统尚未创建任何用户',
            style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16, color: AppTheme.textWhite),
          ),
          const SizedBox(height: 8),
          const Text(
            '请登录后台管理系统 (Admin Web) 添加学生账户。',
            style: TextStyle(color: AppTheme.textMuted, fontSize: 14),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 20),
          FocusButton(
            onPressed: _refreshUsers,
            child: const Text('刷新加载'),
          ),
        ],
      ),
    );
  }
}

