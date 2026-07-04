import 'package:flutter/material.dart';

class DotPatternBackground extends StatelessWidget {
  final Widget child;
  final Color backgroundColor;
  final Color dotColor;
  final double spacing;
  final double dotRadius;

  const DotPatternBackground({
    Key? key,
    required this.child,
    this.backgroundColor = const Color(0xFFF8FAFC),
    this.dotColor = const Color(0xFFCBD5E1),
    this.spacing = 24.0,
    this.dotRadius = 1.2,
  }) : super(key: key);

  @override
  Widget build(BuildContext context) {
    return Stack(
      children: [
        Positioned.fill(
          child: Container(
            color: backgroundColor,
          ),
        ),
        Positioned.fill(
          child: CustomPaint(
            painter: _DotPatternPainter(
              dotColor: dotColor,
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
