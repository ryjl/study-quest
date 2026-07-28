import 'package:flutter/material.dart';
import 'package:path_provider/path_provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../service/tv_mode.dart';
import '../../theme.dart';
import '../responsive.dart';
import '../widget/button_3d.dart';
import '../widget/focus_button.dart';
import '../widget/glass_panel.dart';
import '../widget/tv_focus.dart';

/// 系统设置 page — server URL config + local playback prefs.
///
/// Extracted verbatim from `_MainNavigationState._buildSettingsScreen`. The
/// parent (MainNavigation) owns the [ipController], [isSavingIp] flag and the
/// save/logout side effects and passes them in as props, since they also
/// drive the rest of the app (e.g. baseUrl is read globally by ApiService).
///
/// StatefulWidget 以持有 [_ipFocusNode] —— IP TextField 用 dpadEscapeFocusNode
/// 解 D-pad 焦点陷阱(否则方向键被 EditableText 吞掉,跳不到"保存修改"按钮),
/// 节点需要在 dispose 时释放,StatelessWidget 做不到。
class SettingsScreen extends StatefulWidget {
  final TextEditingController ipController;
  final bool isSavingIp;
  final VoidCallback onSaveIp;
  final VoidCallback onLogout;
  final VoidCallback onPreferencesChanged;

  const SettingsScreen({
    super.key,
    required this.ipController,
    required this.isSavingIp,
    required this.onSaveIp,
    required this.onLogout,
    required this.onPreferencesChanged,
  });

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  // 焦点陷阱修复:TextField 默认吞掉方向键做光标移动,D-pad 进了出不来。
  // dpadEscapeFocusNode 在 EditableText 之前截断方向键转 nextFocus/previousFocus,
  // 让 D-pad 能从 IP 输入框跳到"保存修改"按钮。字母数字回车放行给输入。
  late final FocusNode _ipFocusNode = dpadEscapeFocusNode();

  @override
  void dispose() {
    _ipFocusNode.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final compact = isPortrait(context);
    // 整屏 FocusTraversalGroup:IP 输入框 / 保存按钮 / 开关 / 清理按钮 /
    // 登出按钮共享一个 reading-order 遍历链,D-pad 才能在它们之间自然移动。
    return FocusTraversalGroup(
      child: Padding(
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
                          color: AppTheme.blue100,
                          shape: BoxShape.circle,
                        ),
                        child: const Icon(Icons.shield_rounded, color: AppTheme.blue600, size: 24),
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
                          color: AppTheme.blue100,
                          shape: BoxShape.circle,
                        ),
                        child: const Icon(Icons.settings_suggest_rounded, color: AppTheme.blue600, size: 24),
                      ),
                      const SizedBox(width: 12),
                      const Text('播放设置（本地偏好）', style: TextStyle(fontWeight: FontWeight.w900, fontSize: 18, color: AppTheme.textWhite)),
                    ],
                  ),
                  const SizedBox(height: 24),
                  // Hardware Decoding Switch
                  //
                  // 【TV 适配】SwitchListTile 自身 Enter/Space 能切(Material 默认),
                  // 但 TV 下焦点高亮很弱(只一条细边)。用 _tvFocusableTile 包一层:
                  // 焦点时加蓝色边框 + 轻微背景,让用户看清 D-pad 落点。下面的
                  // force_tv 开关(测试这一切的入口)同样包一层。
                  FutureBuilder<bool>(
                    future: SharedPreferences.getInstance().then((prefs) => prefs.getBool('enable_hw_acceleration') ?? false),
                    builder: (context, snapshot) {
                      final isEnabled = snapshot.data ?? false;
                      return _tvFocusableTile(
                        SwitchListTile(
                          contentPadding: EdgeInsets.zero,
                          title: const Text('硬件加速解码 (HW Acceleration)', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 15, color: AppTheme.textWhite)),
                          subtitle: const Text('开启后使用硬件加速解码，若播放异常请关闭', style: TextStyle(color: AppTheme.textMuted, fontSize: 12, fontWeight: FontWeight.bold)),
                          value: isEnabled,
                          activeThumbColor: AppTheme.primaryColor,
                          onChanged: (val) async {
                            final prefs = await SharedPreferences.getInstance();
                            await prefs.setBool('enable_hw_acceleration', val);
                            widget.onPreferencesChanged();
                          },
                        ),
                      );
                    },
                  ),
                  const Divider(color: AppTheme.borderMuted, height: 32),
                  // TV 模式预览(调试):给 MuMu 模拟器(PAD 形态)开发用,
                  // 打开后强制走 TV 分支布局,这样不用真机 TV 也能验证 TV 页面。
                  // 真机 TV 走自动检测,本开关对其无影响(本来就走 TV)。
                  // 需要 App 重启后全局生效 —— TvMode 在启动时只读一次。
                  _tvFocusableTile(
                    SwitchListTile(
                      contentPadding: EdgeInsets.zero,
                      title: const Text('预览 TV 模式(调试)', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 15, color: AppTheme.textWhite)),
                      subtitle: const Text('打开后 App 强制使用 TV 布局,用于在模拟器上验证 TV 体验。重启 App 后生效。', style: TextStyle(color: AppTheme.textMuted, fontSize: 12, fontWeight: FontWeight.bold)),
                      value: TvMode.instance.forceEnabled,
                      activeThumbColor: AppTheme.primaryColor,
                      onChanged: (val) async {
                        await TvMode.instance.setForceEnabled(val);
                        widget.onPreferencesChanged();
                        if (val) {
                          ScaffoldMessenger.of(context).showSnackBar(
                            const SnackBar(content: Text('已开启 TV 模式预览。请重启 App(或热重启)使全部页面切换到 TV 布局。')),
                          );
                        }
                      },
                    ),
                  ),
                  const Divider(color: AppTheme.borderMuted, height: 32),
                  // Cache Cleaning
                  _tvFocusableTile(
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
              borderColor: AppTheme.borderMuted,
              onPressed: widget.onLogout,
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
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
          ],
        ],
      ),
      ),
      ),
    );
  }

  Widget _buildIpTextField() {
    return TextField(
      controller: widget.ipController,
      focusNode: _ipFocusNode,
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
      onPressed: widget.onSaveIp,
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
      child: widget.isSavingIp
          ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
          : const Text('保存修改', style: TextStyle(fontWeight: FontWeight.w900, color: Colors.white)),
    );
  }

  /// 给 SwitchListTile / ListTile 这种 Material 行控件套一层 TV 友好的焦点
  /// 高亮:聚焦时加蓝色边框 + 淡蓝背景,让用户看清 D-pad 落点。非 TV 模式下
  /// 纯透明,不影响触屏视觉。
  Widget _tvFocusableTile(Widget child) => _TvFocusableTile(child: child);
}

class _TvFocusableTile extends StatefulWidget {
  final Widget child;
  const _TvFocusableTile({required this.child});

  @override
  State<_TvFocusableTile> createState() => _TvFocusableTileState();
}

class _TvFocusableTileState extends State<_TvFocusableTile> {
  bool _focused = false;

  @override
  Widget build(BuildContext context) {
    return Focus(
      canRequestFocus: true,
      onFocusChange: (v) => setState(() => _focused = v),
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 120),
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(12),
          border: _focused
              ? Border.all(color: AppTheme.primaryColor, width: 2)
              : Border.all(color: Colors.transparent, width: 2),
          color: _focused
              ? AppTheme.primaryColor.withValues(alpha: 0.08)
              : Colors.transparent,
        ),
        child: widget.child,
      ),
    );
  }
}
