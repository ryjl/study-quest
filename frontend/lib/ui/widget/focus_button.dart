import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../../theme.dart';

class FocusButton extends StatefulWidget {
  final Widget child;
  final VoidCallback onPressed;
  final double borderRadius;
  final Color baseColor;
  final Color borderColor;
  final EdgeInsets padding;
  final FocusNode? focusNode;
  final bool autoFocus;

  const FocusButton({
    Key? key,
    required this.child,
    required this.onPressed,
    this.borderRadius = AppTheme.borderRadiusValue,
    this.baseColor = AppTheme.cardColor,
    this.borderColor = AppTheme.borderMuted,
    this.padding = const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
    this.focusNode,
    this.autoFocus = false,
  }) : super(key: key);

  @override
  State<FocusButton> createState() => _FocusButtonState();
}

class _FocusButtonState extends State<FocusButton> {
  late FocusNode _focusNode;
  bool _hasFocus = false;
  bool _isHovered = false;

  @override
  void initState() {
    super.initState();
    _focusNode = widget.focusNode ?? FocusNode();
    _focusNode.addListener(_onFocusChange);
  }

  @override
  void dispose() {
    // Only dispose if created internally
    if (widget.focusNode == null) {
      _focusNode.dispose();
    }
    super.dispose();
  }

  void _onFocusChange() {
    setState(() {
      _hasFocus = _focusNode.hasFocus;
    });
  }

  @override
  Widget build(BuildContext context) {
    final active = _hasFocus || _isHovered;

    return Focus(
      focusNode: _focusNode,
      autofocus: widget.autoFocus,
      onKeyEvent: (FocusNode node, KeyEvent event) {
        if (event is KeyDownEvent) {
          final key = event.logicalKey;
          if (key == LogicalKeyboardKey.enter ||
              key == LogicalKeyboardKey.select ||
              key == LogicalKeyboardKey.gameButtonSelect) {
            widget.onPressed();
            return KeyEventResult.handled;
          }
        }
        return KeyEventResult.ignored;
      },
      child: MouseRegion(
        onEnter: (_) => setState(() => _isHovered = true),
        onExit: (_) => setState(() => _isHovered = false),
        child: GestureDetector(
          onTap: widget.onPressed,
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 150),
            padding: widget.padding,
            decoration: AppTheme.switchDecoration(
              hasFocus: active,
              bg: active ? AppTheme.primaryColor.withOpacity(0.15) : widget.baseColor,
              border: active ? AppTheme.primaryColor : widget.borderColor,
            ),
            child: DefaultTextStyle(
              style: TextStyle(
                color: active ? AppTheme.textWhite : AppTheme.textWhite,
                fontWeight: active ? FontWeight.bold : FontWeight.normal,
              ),
              child: widget.child,
            ),
          ),
        ),
      ),
    );
  }
}
