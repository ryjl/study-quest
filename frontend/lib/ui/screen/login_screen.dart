import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../../model/user.dart';
import '../../service/api_service.dart';
import '../../service/auth_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/num_pad.dart';
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
      body: Stack(
        children: [
          // Background layout
          Positioned.fill(
            child: Container(
              decoration: const BoxDecoration(
                gradient: LinearGradient(
                  colors: [AppTheme.backgroundColor, Color(0xFF0F172A)],
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                ),
              ),
            ),
          ),

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
                          fontSize: 48,
                          fontWeight: FontWeight.bold,
                          letterSpacing: 2,
                          color: AppTheme.textWhite,
                        ),
                      ),
                      const SizedBox(height: 8),
                      const Text(
                        '学 途 奇 旅',
                        style: TextStyle(
                          fontSize: 20,
                          fontWeight: FontWeight.w600,
                          letterSpacing: 6,
                          color: AppTheme.accentGreen,
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

          // Overlay PIN Pad
          if (_showPinPad && _selectedUser != null)
            Positioned.fill(
              child: Container(
                color: Colors.black.withOpacity(0.85),
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
                        const SizedBox(height: 18),
                        Text(
                          _errorMessage,
                          style: const TextStyle(color: Colors.redAccent, fontSize: 16, fontWeight: FontWeight.bold),
                        ),
                      ],
                    ],
                  ),
                ),
              ),
            ),
        ],
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
          padding: const EdgeInsets.all(16),
          onPressed: () => _onSelectUser(user),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              // Avatar
              Container(
                width: 100,
                height: 100,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  border: Border.all(color: Colors.white24, width: 2),
                  image: user.avatarUrl.isNotEmpty
                      ? DecorationImage(
                          image: NetworkImage(user.avatarUrl),
                          fit: BoxFit.cover,
                        )
                      : null,
                ),
                child: user.avatarUrl.isEmpty
                    ? const Icon(Icons.person, size: 50, color: AppTheme.textMuted)
                    : null,
              ),
              const SizedBox(height: 12),
              // Nickname
              Text(
                user.nickname,
                style: const TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
              ),
              const SizedBox(height: 4),
              // Role badge
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                decoration: BoxDecoration(
                  color: Colors.white10,
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Text(
                  user.role.toUpperCase(),
                  style: const TextStyle(fontSize: 10, color: AppTheme.textMuted),
                ),
              ),
            ],
          ),
        );
      }).toList(),
    );
  }

  Widget _buildErrorBox(String error) {
    return Container(
      padding: const EdgeInsets.all(24),
      decoration: BoxDecoration(
        color: Colors.red.withOpacity(0.1),
        border: Border.all(color: Colors.red.withOpacity(0.3), width: AppTheme.borderWidthValue),
        borderRadius: BorderRadius.circular(AppTheme.borderRadiusValue),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.warning_amber_rounded, size: 48, color: Colors.redAccent),
          const SizedBox(height: 12),
          const Text(
            '无法连接到 StudyQuest 服务器',
            style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
          ),
          const SizedBox(height: 8),
          Text(
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
                onPressed: () {
                  // Direct to MainNavigation settings view
                  Navigator.push(
                    context,
                    MaterialPageRoute(
                      builder: (context) => const MainNavigation(initialTabIndex: 2), // settings tab
                    ),
                  ).then((_) => _refreshUsers());
                },
                child: const Text('去配置 IP'),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildEmptyBox() {
    return Container(
      padding: const EdgeInsets.all(24),
      decoration: AppTheme.switchDecoration(hasFocus: false),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.supervised_user_circle, size: 48, color: AppTheme.textMuted),
          const SizedBox(height: 12),
          const Text(
            '系统尚未创建任何用户',
            style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
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
