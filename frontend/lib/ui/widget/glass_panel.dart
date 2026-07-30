import 'package:flutter/material.dart';
import '../../theme.dart';

class GlassPanel extends StatelessWidget {
  final Widget child;
  final double borderRadius;
  final bool hasFocus;
  final Color? baseColor;
  final Color? borderColor;
  final double borderWidth;
  final EdgeInsetsGeometry? padding;

  const GlassPanel({
    super.key,
    required this.child,
    this.borderRadius = AppTheme.borderRadiusValue,
    this.hasFocus = false,
    this.baseColor,
    this.borderColor,
    this.borderWidth = 2.0,
    this.padding,
  });

  @override
  Widget build(BuildContext context) {
    // 默认表层面色随主题:未传时取 context.colors 的卡片/边框色。
    final colors = context.colors;
    final bg = baseColor ?? colors.cardColor;
    final bd = borderColor ?? colors.borderMuted;
    return Container(
      padding: padding,
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(borderRadius),
        border: Border.all(
          color: hasFocus ? colors.primaryColor : bd,
          width: hasFocus ? AppTheme.borderWidthValue : borderWidth,
        ),
        boxShadow: hasFocus
            ? [
                BoxShadow(
                  color: colors.primaryColor.withValues(alpha: 0.15),
                  blurRadius: 16,
                  spreadRadius: 1,
                  offset: const Offset(0, 0),
                )
              ]
            : [
                BoxShadow(
                  // 非焦点阴影用 slate900 深色;暗色下卡片本身就是深色,阴影不可见
                  // 属预期(深底不加可见阴影,与 design-tokens.md TV 深底规范一致)。
                  color: colors.slate900.withValues(alpha: 0.04),
                  blurRadius: 24,
                  offset: const Offset(0, 8),
                )
              ],
      ),
      child: child,
    );
  }
}
