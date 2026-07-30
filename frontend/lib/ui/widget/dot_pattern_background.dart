import 'package:flutter/material.dart';

import '../../theme.dart';

/// 全 App 共用的点阵背景(登录页、主导航内容区)。
///
/// 背景色与点色默认跟随主题:未传时取 [context.colors] 的背景色和 slate300。
/// 调用方仍可显式传固定色覆盖(目前无此需求)。
class DotPatternBackground extends StatelessWidget {
  final Widget child;
  final Color? backgroundColor;
  final Color? dotColor;
  final double spacing;
  final double dotRadius;

  const DotPatternBackground({
    super.key,
    required this.child,
    this.backgroundColor,
    this.dotColor,
    this.spacing = 24.0,
    this.dotRadius = 1.2,
  });

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final bg = backgroundColor ?? colors.backgroundColor;
    // 点色随主题:浅底用 slate300(亮灰,可见但不抢眼);深底用 slate700(深灰),
    // 并叠 50% alpha —— 否则 slate300 在 slate900 深底上像撒了盐一样刺眼发白。
    // 调用方未传 dotColor 时按亮度自适应。
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final dot = dotColor ??
        (isDark
            ? colors.slate700.withValues(alpha: 0.6)
            : colors.slate300);
    return Stack(
      children: [
        Positioned.fill(
          child: Container(
            color: bg,
          ),
        ),
        Positioned.fill(
          child: CustomPaint(
            painter: _DotPatternPainter(
              dotColor: dot,
              spacing: spacing,
              dotRadius: dotRadius,
            ),
          ),
        ),
        Positioned.fill(
          child: child,
        ),
      ],
    );
  }
}

class _DotPatternPainter extends CustomPainter {
  final Color dotColor;
  final double spacing;
  final double dotRadius;

  _DotPatternPainter({
    required this.dotColor,
    required this.spacing,
    required this.dotRadius,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = dotColor
      ..style = PaintingStyle.fill;

    for (double x = spacing / 2; x < size.width; x += spacing) {
      for (double y = spacing / 2; y < size.height; y += spacing) {
        canvas.drawCircle(Offset(x, y), dotRadius, paint);
      }
    }
  }

  @override
  bool shouldRepaint(covariant _DotPatternPainter oldDelegate) {
    return oldDelegate.dotColor != dotColor ||
        oldDelegate.spacing != spacing ||
        oldDelegate.dotRadius != dotRadius;
  }
}
