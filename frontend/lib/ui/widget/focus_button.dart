import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../../theme.dart';
import 'glass_panel.dart';

class FocusButton extends StatefulWidget {
  final Widget child;
  final VoidCallback onPressed;
  final double borderRadius;
  final Color? baseColor;
  final Color? borderColor;
  final EdgeInsets padding;
  final FocusNode? focusNode;
  final bool autoFocus;

  const FocusButton({
    Key? key,
    required this.child,
    required this.onPressed,
    this.borderRadius = AppTheme.borderRadiusValue,
    this.baseColor,
    this.borderColor,
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
    final colors = context.colors;

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
          child: AnimatedScale(
            scale: active ? 0.98 : 1.0, // Scale down slightly when active for tactile clicky feedback!
            duration: const Duration(milliseconds: 100),
            child: GlassPanel(
              borderRadius: widget.borderRadius,
              hasFocus: active,
              baseColor: active ? colors.primaryColor.withValues(alpha: 0.12) : widget.baseColor,
              borderColor: active ? colors.primaryColor : widget.borderColor,
              padding: widget.padding,
              child: DefaultTextStyle(
                style: TextStyle(
                  color: colors.textWhite,
                  fontSize: 16,
                  fontWeight: active ? FontWeight.bold : FontWeight.w600,
                  fontFamily: 'Quicksand',
                ),
                child: widget.child,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

