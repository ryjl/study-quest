import 'package:flutter/material.dart';
import '../../theme.dart';

class GlassPanel extends StatelessWidget {
  final Widget child;
  final double borderRadius;
  final bool hasFocus;
  final Color baseColor;
  final Color borderColor;
  final double borderWidth;
  final EdgeInsetsGeometry? padding;

  const GlassPanel({
    Key? key,
    required this.child,
    this.borderRadius = AppTheme.borderRadiusValue,
    this.hasFocus = false,
    this.baseColor = AppTheme.cardColor,
    this.borderColor = AppTheme.borderMuted,
    this.borderWidth = 2.0,
    this.padding,
  }) : super(key: key);

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: padding,
      decoration: BoxDecoration(
        color: baseColor,
        borderRadius: BorderRadius.circular(borderRadius),
        border: Border.all(
          color: hasFocus ? AppTheme.primaryColor : borderColor,
          width: hasFocus ? AppTheme.borderWidthValue : borderWidth,
        ),
        boxShadow: hasFocus
            ? [
                BoxShadow(
                  color: AppTheme.primaryColor.withOpacity(0.15),
                  blurRadius: 16,
                  spreadRadius: 1,
                  offset: const Offset(0, 0),
                )
              ]
            : [
                BoxShadow(
                  color: const Color(0xFF0F172A).withOpacity(0.04),
                  blurRadius: 24,
                  offset: const Offset(0, 8),
                )
              ],
      ),
      child: child,
    );
  }
}
