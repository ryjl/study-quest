import 'package:flutter/material.dart';

import '../../theme.dart';
import 'focus_button.dart';

/// 播放器设置菜单(2026-07-27 重构,从 MenuAnchor 改为 Dialog)。
///
/// **为什么放弃 MenuAnchor**:MenuAnchor 的菜单 overlay 在 Navigator 顶层渲染,
/// 焦点归位靠 MenuController 状态机 + FocusManager 全局监听,跟手动焦点管理冲突
/// 严重 —— 实测菜单关闭后焦点丢失到 ModalScope、ESC 后焦点消失再也点不回来、
/// 系统返回键直接退出页面等一堆 bug,修了多轮没根治。
///
/// **Dialog 方案**:`showDialog` 是 Flutter 最成熟的焦点隔离机制:
///  - 自动 FocusScope(菜单内焦点隔离,不跑到背后视频区)
///  - 自动 ESC 关闭(routes.dart:1198 _DismissModalAction 绑 DismissIntent)
///  - 自动系统返回键关闭(ModalRoute 注册 popEntry)
///  - **await 线性流程**:关闭后代码继续执行,显式 requestFocus 归位,干净可靠
/// 视觉上用 SimpleDialog + 自定义选项列表(选中有醒目高亮,无尺寸抖动),
/// 三端(TV/PAD/手机)统一一套代码。
///
/// **泛型 [T]**:选项值类型(如 `double` 速度、`int` 字幕档位)。
class PlayerSettingsMenu<T> extends StatefulWidget {
  /// 触发按钮的图标。
  final IconData icon;

  /// 菜单选项。每个 [PlayerMenuOption] 携带值、显示标签。
  final List<PlayerMenuOption<T>> options;

  /// 当前选中值(用于高亮选中项)。
  final T? selectedValue;

  /// 选中某项时回调(参数是该选项的 value)。
  final ValueChanged<T> onSelected;

  /// 可选的菜单标题(显示在菜单顶部)。null 则不显示。
  final String? menuTitle;

  /// 是否高亮触发按钮(表示当前菜单打开或有非默认值)。
  final bool active;

  const PlayerSettingsMenu({
    super.key,
    required this.icon,
    required this.options,
    required this.selectedValue,
    required this.onSelected,
    this.menuTitle,
    this.active = false,
  });

  @override
  State<PlayerSettingsMenu<T>> createState() => _PlayerSettingsMenuState<T>();
}

class _PlayerSettingsMenuState<T> extends State<PlayerSettingsMenu<T>> {
  // 触发按钮的 FocusNode。菜单关闭后 requestFocus 回它,保证焦点连续(D-pad 不丢)。
  // 内部管理(不暴露),Dialog 关后 await 流程里 requestFocus,可靠归位。
  final FocusNode _triggerFocus = FocusNode(debugLabel: 'playerMenuTrigger');

  @override
  void dispose() {
    _triggerFocus.dispose();
    super.dispose();
  }

  Future<void> _openMenu(BuildContext context) async {
    // showDialog 是 await 的 —— 关闭后代码继续,这里显式 requestFocus 归位。
    // 这是 Dialog 相对 MenuAnchor 的核心优势:线性流程,焦点归位可靠。
    final result = await showDialog<T>(
      context: context,
      builder: (ctx) => _PlayerSettingsDialog<T>(
        title: widget.menuTitle,
        options: widget.options,
        selectedValue: widget.selectedValue,
      ),
    );
    // 选中某项:回调。ESC/返回键:result 是 null,不回调。
    if (result != null) {
      widget.onSelected(result);
    }
    // 菜单关闭后(无论选中/ESC/返回键),焦点回触发按钮。
    // 防御性检查(node 可能已被 dispose)。
    if (mounted && _triggerFocus.canRequestFocus) {
      _triggerFocus.requestFocus();
    }
  }

  @override
  Widget build(BuildContext context) {
    return FocusButton(
      focusNode: _triggerFocus,
      onPressed: () => _openMenu(context),
      borderRadius: 24,
      baseColor: widget.active ? AppTheme.primaryColor : Colors.white12,
      borderColor: Colors.transparent,
      padding: const EdgeInsets.all(8),
      child: Icon(widget.icon, color: Colors.white, size: 28),
    );
  }
}

/// Dialog 内容:垂直选项列表,选中有醒目高亮(背景色 + 勾标记),
/// 无尺寸抖动(选中态只改背景色/前景色,不改 padding/size)。
class _PlayerSettingsDialog<T> extends StatelessWidget {
  final String? title;
  final List<PlayerMenuOption<T>> options;
  final T? selectedValue;

  const _PlayerSettingsDialog({
    this.title,
    required this.options,
    required this.selectedValue,
  });

  @override
  Widget build(BuildContext context) {
    // 找到选中项的索引,用于 autofocus(D-pad 打开菜单时焦点落当前选中项,
    // 而非第 0 项 —— 用户能直接看到当前设置,▲▼ 从当前位置调)。
    int selectedIndex = options.indexWhere((o) => o.value == selectedValue);
    if (selectedIndex < 0) selectedIndex = 0;

    return SimpleDialog(
      backgroundColor: const Color(0xF0122840),
      contentPadding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        if (title != null)
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 8, 24, 12),
            child: Text(
              title!,
              style: const TextStyle(
                color: Colors.white70,
                fontSize: 13,
                fontWeight: FontWeight.bold,
              ),
            ),
          ),
        for (var i = 0; i < options.length; i++)
          _DialogOption<T>(
            option: options[i],
            selected: options[i].value == selectedValue,
            autofocus: i == selectedIndex,
            onTap: () => Navigator.of(context).pop(options[i].value),
          ),
      ],
    );
  }
}

class _DialogOption<T> extends StatefulWidget {
  final PlayerMenuOption<T> option;
  final bool selected;
  final bool autofocus;
  final VoidCallback onTap;

  const _DialogOption({
    required this.option,
    required this.selected,
    required this.autofocus,
    required this.onTap,
  });

  @override
  State<_DialogOption<T>> createState() => _DialogOptionState<T>();
}

class _DialogOptionState<T> extends State<_DialogOption<T>> {
  late final FocusNode _focusNode;
  bool _hasFocus = false;

  @override
  void initState() {
    super.initState();
    _focusNode = FocusNode(debugLabel: 'dialogOption');
    _focusNode.addListener(_onFocusChange);
  }

  @override
  void dispose() {
    _focusNode.removeListener(_onFocusChange);
    _focusNode.dispose();
    super.dispose();
  }

  void _onFocusChange() {
    setState(() => _hasFocus = _focusNode.hasFocus);
  }

  @override
  Widget build(BuildContext context) {
    // 选中态只改颜色(背景 primaryColor 半透明 + 文字白 + 勾),不改尺寸 → 无抖动。
    final selected = widget.selected;
    return ListTile(
      onTap: widget.onTap,
      focusNode: _focusNode,
      autofocus: widget.autofocus,
      // 聚焦 + 选中:用最醒目的背景(实色 primaryColor)。
      // 只聚焦:用半透明高亮提示。
      // 都不:透明背景。
      tileColor: selected
          ? AppTheme.primaryColor.withValues(alpha: _hasFocus ? 1.0 : 0.7)
          : (_hasFocus
              ? Colors.white.withValues(alpha: 0.15)
              : Colors.transparent),
      leading: selected
          ? const Icon(Icons.check_rounded, color: Colors.white, size: 20)
          : const SizedBox(width: 20),
      title: Text(
        widget.option.label,
        style: TextStyle(
          color: Colors.white,
          fontWeight: selected ? FontWeight.bold : FontWeight.w500,
          fontSize: 15,
        ),
      ),
      contentPadding: const EdgeInsets.symmetric(horizontal: 24),
    );
  }
}

/// [PlayerSettingsMenu] 的单个选项。
class PlayerMenuOption<T> {
  final T value;
  final String label;

  const PlayerMenuOption({required this.value, required this.label});
}
