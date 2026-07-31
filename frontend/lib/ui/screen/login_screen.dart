import 'dart:async';
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
import '../widget/tv_focus.dart';
import '../../config.dart';
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
  // 登录请求进行中。期间禁用 NumPad 防重复提交(自动提交满 6 位即触发,网络慢时
  // 用户可能再按 C 重输触发第二次 login(),两次请求竞速状态不可测),并显示"登录中…"
  // 反馈,而非让用户对着静止的界面干等。
  bool _isAuthenticating = false;
  // Controls the NumPad so we can clear the buffered PIN after a wrong attempt
  // (auto-submit fills maxDigits; without a clear the next digit would no-op
  // against a full buffer and the user couldn't retype).
  final GlobalKey<NumPadState> _numPadKey = GlobalKey<NumPadState>();

  // Lockout countdown — DISPLAY ONLY. The backend is the single source of
  // truth for whether an account is locked: every login attempt is decided
  // server-side, and a 429 + Retry-After refreshes this message. We do NOT
  // freeze input or "unlock" on the client — that would let a user bypass the
  // lock by restarting the app (this state is in-memory only). The timer just
  // makes the hint count down in real time (15:00 → 14:59 → …) so the user
  // sees the wait shrinking instead of a frozen "15 分钟". When it hits zero
  // we only clear the message; the user is free to retry, and if the backend
  // is still locking it will 429 again and refresh the countdown.
  Timer? _lockTimer;
  DateTime? _lockCountdownEnd;

  @override
  void initState() {
    super.initState();
    _refreshUsers();
  }

  @override
  void dispose() {
    _lockTimer?.cancel();
    super.dispose();
  }

  void _refreshUsers() {
    setState(() {
      _usersFuture = ApiService.fetchUsers();
      _errorMessage = '';
    });
  }

  void _showIpConfigDialog() {
    final controller = TextEditingController(text: AppConfig.baseUrl);
    // 焦点陷阱修复:TextField 默认会吞掉方向键(光标移动),D-pad 进了就出不来,
    // 跳不到「取消 / 保存并重试」按钮。dpadEscapeFocusNode 在 EditableText 之前
    // 截断方向键转 nextFocus/previousFocus,字母数字回车放行给输入。
    final ipFocusNode = dpadEscapeFocusNode();
    final colors = context.colors;
    showDialog(
      context: context,
      barrierDismissible: true,
      builder: (dialogContext) {
        return AlertDialog(
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(24),
          ),
          backgroundColor: colors.cardColor,
          title: Row(
            children: [
              Icon(Icons.lan_rounded, color: colors.primaryColor, size: 28),
              const SizedBox(width: 12),
              Text(
                '配置服务器地址',
                style: TextStyle(fontWeight: FontWeight.bold, fontSize: 20, color: colors.textWhite),
              ),
            ],
          ),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                '请输入后端 API 的局域网或外网穿透地址：',
                style: TextStyle(fontSize: 14, color: colors.textMuted, fontWeight: FontWeight.bold),
              ),
              const SizedBox(height: 16),
              TextField(
                controller: controller,
                focusNode: ipFocusNode,
                autofocus: true,
                style: TextStyle(fontFamily: 'monospace', fontSize: 16, fontWeight: FontWeight.bold, color: colors.textWhite),
                decoration: InputDecoration(
                  filled: true,
                  // 用 backgroundColor(页面底色,比 cardColor 卡片底深一档)做输入框
                  // 填充:dialog 自身是 cardColor,深色模式下 slate100 浅底叠 textWhite
                  // 浅字会撞色看不见;改用语义自适应的底色保证亮暗都和卡片有层次且字可见。
                  fillColor: colors.backgroundColor,
                  contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(16),
                    borderSide: BorderSide(color: colors.slate300, width: 1.5),
                  ),
                  focusedBorder: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(16),
                    borderSide: BorderSide(color: colors.primaryColor, width: 2),
                  ),
                  hintText: 'http://192.168.x.x:8080',
                  hintStyle: TextStyle(color: colors.slate400),
                ),
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Icon(Icons.info_rounded, color: colors.textMuted, size: 14),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      '例如 http://192.168.1.100:8080，请确保与后端在同一局域网',
                      style: TextStyle(color: colors.textMuted, fontSize: 12, fontWeight: FontWeight.bold),
                    ),
                  ),
                ],
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext),
              child: Text('取消', style: TextStyle(color: colors.textMuted, fontWeight: FontWeight.bold, fontSize: 16)),
            ),
            ElevatedButton(
              style: ElevatedButton.styleFrom(
                backgroundColor: colors.primaryColor,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
                padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 12),
              ),
              onPressed: () async {
                final url = controller.text.trim();
                if (url.isNotEmpty) {
                  await AppConfig.setBaseUrl(url);
                  if (mounted) {
                    Navigator.pop(dialogContext);
                    _refreshUsers();
                  }
                }
              },
              child: const Text('保存并重试', style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold, fontSize: 16)),
            ),
          ],
        );
      },
    ).whenComplete(ipFocusNode.dispose);
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
      _isAuthenticating = false;
    });
  }

  Future<void> _onSubmitPin(String pin) async {
    if (_selectedUser == null || _isAuthenticating) return;

    final authService = Provider.of<AuthService>(context, listen: false);
    setState(() {
      _isAuthenticating = true;
      _errorMessage = '';
    });
    LoginResult result;
    try {
      result = await authService.login(_selectedUser!, pin);
    } catch (_) {
      // 网络错误(超时/服务器无响应):login 链路无 try/catch,异常会向上抛成
      // 未处理异常(红屏),且下方的 clear() 走不到 → NumPad 永远卡在满 6 位,
      // 用户再按任何键都 no-op,只能强杀 App。这里兜底:清缓冲 + 人话提示 + 解锁。
      if (!mounted) return;
      _numPadKey.currentState?.clear();
      setState(() {
        _isAuthenticating = false;
        _errorMessage = '网络连接失败，请检查网络后重试';
      });
      return;
    }

    if (result.success) {
      if (!mounted) return;
      Navigator.pushReplacement(
        context,
        MaterialPageRoute(builder: (context) => const MainNavigation()),
      );
      return;
    }

    // Failed: always clear the pad so the user starts fresh (auto-submit fills
    // maxDigits, so without this the buffer stays full).
    _numPadKey.currentState?.clear();
    setState(() => _isAuthenticating = false);

    if (result.locked) {
      _showLockoutMessage(result.retryAfterSeconds);
    } else {
      setState(() {
        _errorMessage = 'PIN 码错误，请重试！';
      });
    }
  }

  /// Shows (or refreshes) the lockout hint based on the backend's last 429.
  /// Display-only: this never freezes input or decides when the user may
  /// retry — that's the backend's call. We just count down the [retrySeconds]
  /// the server handed us so the message shrinks in real time. When it reaches
  /// zero we clear the message; the user can retry, and if still locked the
  /// backend returns 429 again and refreshes this hint.
  void _showLockoutMessage(int? retrySeconds) {
    _lockTimer?.cancel();
    if (retrySeconds == null) {
      // No Retry-After header (rare — a reverse proxy stripping it). Show a
      // static hint; the next attempt will get a fresh verdict from backend.
      setState(() {
        _lockCountdownEnd = null;
        _errorMessage = '尝试次数过多，账户已临时锁定，请稍后重试';
      });
      return;
    }
    setState(() {
      _lockCountdownEnd = DateTime.now().add(Duration(seconds: retrySeconds));
      _errorMessage = '尝试次数过多，请 ${formatLockoutWait(retrySeconds)} 后重试';
    });
    _lockTimer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (!mounted) {
        _lockTimer?.cancel();
        return;
      }
      final remaining = secondsUntilUnlock(_lockCountdownEnd!, DateTime.now());
      if (remaining <= 0) {
        // Countdown elapsed: stop the timer and clear the hint. We do NOT
        // gate further attempts on this — the backend is the lock authority.
        _lockTimer!.cancel();
        _lockTimer = null;
        setState(() {
          _lockCountdownEnd = null;
          _errorMessage = '';
        });
      } else {
        setState(() {
          _errorMessage = '尝试次数过多，请 ${formatLockoutWait(remaining)} 后重试';
        });
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    // 整屏 FocusTraversalGroup:用户卡 / 设置按钮 / 错误态按钮 / PIN 键盘
    // 共享一个 reading-order 遍历链,D-pad 才能在它们之间自然移动。
    return FocusTraversalGroup(
      child: Scaffold(
      body: DotPatternBackground(
        child: Stack(
          children: [
            // Top Right Settings/IP Config button
            Positioned(
              top: 16,
              right: 16,
              child: SafeArea(
                child: FocusButton(
                  padding: const EdgeInsets.all(12),
                  borderRadius: 16,
                  baseColor: colors.cardColor.withValues(alpha: 0.8),
                  borderColor: colors.borderMuted,
                  onPressed: _showIpConfigDialog,
                  child: Icon(
                    Icons.settings_rounded,
                    color: colors.primaryColor,
                    size: 28,
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
                      Text(
                        'StudyQuest',
                        style: TextStyle(
                          fontFamily: 'Quicksand',
                          fontSize: 54,
                          fontWeight: FontWeight.bold,
                          letterSpacing: 2,
                          color: colors.primaryColor,
                        ),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        '学 途 奇 旅',
                        style: TextStyle(
                          fontSize: 20,
                          fontWeight: FontWeight.w600,
                          letterSpacing: 6,
                          color: colors.primaryColor.withValues(alpha: 0.7),
                        ),
                      ),
                      const SizedBox(height: 48),

                      // Users List Grid
                      FutureBuilder<List<User>>(
                        future: _usersFuture,
                        builder: (context, snapshot) {
                          if (snapshot.connectionState == ConnectionState.waiting) {
                            return CircularProgressIndicator(color: colors.primaryColor);
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
          //
          // 【TV 适配】FocusScope(autofocus:true):PIN 蒙层打开时焦点自动进入
          // NumPad(NumPad 内部「1」键再 autofocus),D-pad 落在数字键上而不是
          // 背后模糊的用户卡。蒙层移除时焦点自然还回外层用户卡。
          if (_showPinPad && _selectedUser != null)
            Positioned.fill(
              child: Container(
                color: Colors.black.withValues(alpha: 0.15), // Light dim overlay
                child: BackdropFilter(
                  filter: ImageFilter.blur(sigmaX: 18, sigmaY: 18),
                  child: FocusScope(
                    autofocus: true,
                    child: Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          NumPad(
                            key: _numPadKey,
                            title: '验证 ${_selectedUser!.nickname} 的 PIN 码',
                            maxDigits: 6, // 6-digit PIN (security: 6-digit minimum)
                            // NOTE: input is NOT frozen during a lockout — the
                            // backend is the lock authority, so the user may
                            // retry anytime; a still-locked attempt returns 429
                            // and refreshes the countdown hint.
                            // 但登录请求进行中要禁用(_isAuthenticating),防重复提交。
                            enabled: !_isAuthenticating,
                            onSubmit: _onSubmitPin,
                            onCancel: _onCancelPin,
                          ),
                          if (_isAuthenticating) ...[
                            const SizedBox(height: 20),
                            Row(
                              mainAxisAlignment: MainAxisAlignment.center,
                              children: const [
                                SizedBox(
                                    width: 16, height: 16,
                                    child: CircularProgressIndicator(strokeWidth: 2)),
                                SizedBox(width: 10),
                                Text('登录中…',
                                    style: TextStyle(fontSize: 15, fontWeight: FontWeight.bold)),
                              ],
                            ),
                          ] else if (_errorMessage.isNotEmpty) ...[
                            const SizedBox(height: 20),
                            Container(
                              padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                              decoration: BoxDecoration(
                                color: Colors.redAccent.withValues(alpha: 0.15),
                                borderRadius: BorderRadius.circular(12),
                                border: Border.all(color: Colors.redAccent.withValues(alpha: 0.3)),
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
            ),
        ],
      ),
    ),
    ),
  );
}

  Widget _buildUsersGrid(List<User> users) {
    final colors = context.colors;
    return Wrap(
      spacing: 24,
      runSpacing: 24,
      alignment: WrapAlignment.center,
      children: users.asMap().entries.map((entry) {
        final user = entry.value;
        final isFirst = entry.key == 0;
        return FocusButton(
          // 首个用户卡 autofocus:首屏 D-pad 落点确定(而不是飘到右上设置按钮
          // 或不可预测的位置)。用户列表异步加载,FutureBuilder 数据到达后
          // 首次构建这个 Wrap,autofocus 此时生效。
          autoFocus: isFirst,
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
                  border: Border.all(color: colors.cardColor, width: 3),
                ),
                child: ClipOval(
                  // 用 Image.network + errorBuilder 兜底(对齐 bookshelf/reading):
                  // 原 DecorationImage(NetworkImage) 无 onError,头像 URL 非空但
                  // 加载失败(404/内网穿透失效)时 debug 抛异常、release 渲染成空圆圈。
                  // errorBuilder 回退到 person 占位图标,保证始终有可辨识的头像。
                  child: user.avatarUrl.isEmpty
                      ? Center(child: Icon(Icons.person, size: 44, color: colors.textMuted))
                      : Image.network(
                          user.avatarUrl,
                          fit: BoxFit.cover,
                          width: 90,
                          height: 90,
                          errorBuilder: (_, __, ___) => Center(
                              child: Icon(Icons.person, size: 44, color: colors.textMuted)),
                        ),
                ),
              ),
              const SizedBox(height: 14),
              // Nickname
              Text(
                user.nickname,
                style: TextStyle(
                  fontFamily: 'Quicksand',
                  fontSize: 18,
                  fontWeight: FontWeight.bold,
                  color: colors.textWhite,
                ),
              ),
              const SizedBox(height: 6),
              // Role badge
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                decoration: BoxDecoration(
                  // 凹槽底深色感知:亮色 slate900@5% 浅灰,深色 textWhite@5% 浅凹槽
                  // (slate900 在深色下=背景色,5% 透明会消失)。
                  color: (Theme.of(context).brightness == Brightness.dark
                          ? colors.textWhite
                          : colors.slate900)
                      .withValues(alpha: 0.05),
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Text(
                  user.role.toUpperCase(),
                  style: TextStyle(fontSize: 10, color: colors.textMuted, fontWeight: FontWeight.bold),
                ),
              ),
            ],
          ),
        );
      }).toList(),
    );
  }

  Widget _buildErrorBox(String error) {
    final colors = context.colors;
    return GlassPanel(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.warning_amber_rounded, size: 48, color: Colors.redAccent),
          const SizedBox(height: 12),
          Text(
            '无法连接到 StudyQuest 服务器',
            style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16, color: colors.textWhite),
          ),
          const SizedBox(height: 8),
          Text(
            '请检查您的局域网连接或配置正确的服务器 IP。',
            style: TextStyle(color: colors.textMuted, fontSize: 14),
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
                borderColor: colors.primaryColor,
                onPressed: _showIpConfigDialog,
                child: Text('去配置 IP', style: TextStyle(color: colors.primaryColor)),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildEmptyBox() {
    final colors = context.colors;
    return GlassPanel(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.supervised_user_circle, size: 48, color: colors.textMuted),
          const SizedBox(height: 12),
          Text(
            '系统尚未创建任何用户',
            style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16, color: colors.textWhite),
          ),
          const SizedBox(height: 8),
          Text(
            '请登录后台管理系统 (Admin Web) 添加学生账户。',
            style: TextStyle(color: colors.textMuted, fontSize: 14),
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

/// Formats a Retry-After seconds value into a friendly "X 分 Y 秒" / "X 分钟" /
/// "X 秒" string for the lockout message. Top-level so it's unit-testable
/// without pumping the whole login screen (the overlay's BackdropFilter +
/// async login make full widget tests brittle, and this is the only piece of
/// real logic in the lockout UX path).
String formatLockoutWait(int seconds) {
  if (seconds < 60) return '$seconds 秒';
  final m = seconds ~/ 60;
  final s = seconds % 60;
  return s == 0 ? '$m 分钟' : '$m 分 $s 秒';
}

/// Seconds left until [lockedUntil], measured against [now] (injectable so
/// tests can advance virtual time without waiting). Returns ≤ 0 once the
/// lockout has elapsed. The countdown timer ticks this once per second to
/// refresh the message (15:00 → 14:59 → … → 0 → unlock).
int secondsUntilUnlock(DateTime lockedUntil, DateTime now) {
  return lockedUntil.difference(now).inSeconds;
}

