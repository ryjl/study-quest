import 'package:flutter/material.dart';

import '../../theme.dart';

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

    return FocusableActionDetector(
      autofocus: widget.autoFocus,
      onFocusChange: (value) => setState(() => _isFocused = value),
      onShowFocusHighlight: (value) => setState(() => _isFocused = value),
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
          duration: const Duration(milliseconds: 50),
          margin: EdgeInsets.only(top: offsetTop, bottom: 4.0 - offsetTop),
          decoration: BoxDecoration(
            color: widget.backgroundColor,
            borderRadius: BorderRadius.circular(widget.borderRadius),
            border: widget.border,
            boxShadow: shadowHeight > 0
                ? [
                    BoxShadow(
                      color: widget.shadowColor,
                      offset: Offset(0, shadowHeight),
                      blurRadius: 0,
                    ),
                    if (_isFocused)
                      BoxShadow(
                        color: widget.backgroundColor.withValues(alpha: 0.3),
                        spreadRadius: 4,
                        blurRadius: 8,
                      ),
                  ]
                : [
                    if (_isFocused)
                      BoxShadow(
                        color: widget.backgroundColor.withValues(alpha: 0.3),
                        spreadRadius: 4,
                        blurRadius: 8,
                      ),
                  ],
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
