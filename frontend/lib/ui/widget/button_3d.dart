import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../theme.dart';

/// Whether [key] is a TV activation key (remote OK / Enter / gamepad A).
///
/// Centralized here so [Button3D] and other focusables share one source of
/// truth instead of each re-listing the same `LogicalKeyboardKey` checks.
/// Written as a function (not a `const Set`) because `LogicalKeyboardKey`
/// overrides `==`/`hashCode` and thus can't live in a `const` set.
bool isActivationKey(LogicalKeyboardKey key) =>
    key == LogicalKeyboardKey.enter ||
    key == LogicalKeyboardKey.select ||
    key == LogicalKeyboardKey.gameButtonSelect;

class Button3D extends StatefulWidget {
  final VoidCallback? onPressed;
  final Widget child;
  final Color backgroundColor;
  final Color shadowColor;
  final double borderRadius;
  final Border? border;
  final EdgeInsets padding;
  final bool autoFocus;

  const Button3D({
    Key? key,
    this.onPressed,
    required this.child,
    this.backgroundColor = const Color(0xFF3B82F6),
    this.shadowColor = AppTheme.blue600,
    this.borderRadius = 16.0,
    this.border,
    this.padding = const EdgeInsets.symmetric(horizontal: 24, vertical: 12),
    this.autoFocus = false,
  }) : super(key: key);

  // Helper factory for Blue 3D button
  factory Button3D.blue({
    Key? key,
    VoidCallback? onPressed,
    required Widget child,
    EdgeInsets padding = const EdgeInsets.symmetric(horizontal: 24, vertical: 12),
    bool autoFocus = false,
  }) {
    return Button3D(
      key: key,
      onPressed: onPressed,
      backgroundColor: const Color(0xFF3B82F6),
      shadowColor: AppTheme.blue600,
      borderRadius: 16.0,
      padding: padding,
      autoFocus: autoFocus,
      child: child,
    );
  }

  // Helper factory for White 3D button
  factory Button3D.white({
    Key? key,
    VoidCallback? onPressed,
    required Widget child,
    EdgeInsets padding = const EdgeInsets.symmetric(horizontal: 24, vertical: 12),
    bool autoFocus = false,
  }) {
    return Button3D(
      key: key,
      onPressed: onPressed,
      backgroundColor: Colors.white,
      shadowColor: AppTheme.borderMuted,
      borderRadius: 16.0,
      border: Border.all(color: AppTheme.slate100, width: 2.0),
      padding: padding,
      autoFocus: autoFocus,
      child: DefaultTextStyle.merge(
        style: const TextStyle(color: AppTheme.textMuted),
        child: child,
      ),
    );
  }

  @override
  State<Button3D> createState() => _Button3DState();
}

class _Button3DState extends State<Button3D> {
  bool _isPressed = false;
  bool _isFocused = false;

  @override
  Widget build(BuildContext context) {
    final double offsetTop = _isPressed ? 4.0 : 0.0;
    final double shadowHeight = _isPressed ? 0.0 : 4.0;

    // 焦点视觉(对齐 docs/design-tokens.md 焦点视觉章节)——
    // 原来 Button3D 焦点态只叠一层"白色 30% 光晕",白加白在 TV 上几乎不可见,
    // 导致 PIN 数字键 D-pad 移动时看不出停在哪。这里改成项目标准焦点反馈:
    // 蓝色边框(颜色变蓝) + 蓝色发光环 + 浅底键焦点背景叠淡蓝。
    //
    // 【关键】边框宽度必须焦点/非焦点保持一致 —— 边框宽度变化(2→3)会改变
    // 盒模型占位,推挤内部数字导致"边缘对不齐/数字抖动"。所以焦点态只换 border
    // 颜色为蓝,width 沿用 widget.border 原宽度;无 border 的键(蓝键)靠发光环
    // 反馈,不强行加边框(避免 0→有 的跳动)。
    final double baseBorderWidth = widget.border?.top.width ?? 0.0;
    final Border? focusedBorder = widget.border != null
        ? Border.all(color: AppTheme.primaryColor, width: baseBorderWidth)
        : null;
    // 浅底键(如 Button3D.white)焦点时背景提亮;深底键(蓝色键)已经是高饱和,
    // 不再叠色避免发灰。用 backgroundColor 的亮度简单判断。
    final bool isLightBase = widget.backgroundColor.computeLuminance() > 0.5;
    final Color focusedBg = _isFocused && isLightBase
        ? Color.alphaBlend(AppTheme.primaryColor.withValues(alpha: 0.10), widget.backgroundColor)
        : widget.backgroundColor;
    // 发光环:spreadRadius 0(不外扩,贴合按钮边缘不"糊"出去)、blur 柔和。
    final List<BoxShadow> focusGlow = _isFocused
        ? [
            BoxShadow(
              color: AppTheme.primaryColor.withValues(alpha: 0.40),
              spreadRadius: 0,
              blurRadius: 16,
              offset: const Offset(0, 0),
            ),
          ]
        : const [];

    return Focus(
      autofocus: widget.autoFocus,
      onFocusChange: (value) => setState(() => _isFocused = value),
      onKeyEvent: (node, event) {
        // TV 激活:遥控器 OK / 回车 / gamepad A 触发 onPressed。原来只有
        // FocusableActionDetector(只高亮),D-pad 下 Enter 不触发,导致
        // PIN 键盘、设置保存/清理等全 app 的 Button3D 在 TV 上是死的。
        if (event is KeyDownEvent && isActivationKey(event.logicalKey)) {
          widget.onPressed?.call();
          return KeyEventResult.handled;
        }
        return KeyEventResult.ignored;
      },
      child: GestureDetector(
        onTapDown: (_) {
          if (widget.onPressed != null) {
            setState(() => _isPressed = true);
          }
        },
        onTapUp: (_) {
          if (widget.onPressed != null) {
            setState(() => _isPressed = false);
            widget.onPressed!();
          }
        },
        onTapCancel: () {
          setState(() => _isPressed = false);
        },
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 120),
          margin: EdgeInsets.only(top: offsetTop, bottom: 4.0 - offsetTop),
          decoration: BoxDecoration(
            color: focusedBg,
            borderRadius: BorderRadius.circular(widget.borderRadius),
            border: _isFocused ? focusedBorder : widget.border,
            boxShadow: shadowHeight > 0
                ? [
                    BoxShadow(
                      color: widget.shadowColor,
                      offset: Offset(0, shadowHeight),
                      blurRadius: 0,
                    ),
                    ...focusGlow,
                  ]
                : focusGlow,
          ),
          padding: widget.padding,
          child: DefaultTextStyle.merge(
            style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 15),
            child: widget.child,
          ),
        ),
      ),
    );
  }
}
