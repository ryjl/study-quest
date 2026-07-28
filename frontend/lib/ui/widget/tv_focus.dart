import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'button_3d.dart' show isActivationKey;

/// Whether [key] advances through the focus chain under our reading order
/// (top→bottom, left→right): down / right.
bool _isDpadNext(LogicalKeyboardKey key) =>
    key == LogicalKeyboardKey.arrowDown ||
    key == LogicalKeyboardKey.arrowRight;

/// Whether [key] reverses through the focus chain: up / left.
bool _isDpadPrevious(LogicalKeyboardKey key) =>
    key == LogicalKeyboardKey.arrowUp ||
    key == LogicalKeyboardKey.arrowLeft;

/// A [FocusNode] that turns D-pad arrows into focus traversal and lets every
/// other key fall through to the widget (typically a [TextField]).
///
/// Why this exists: a plain `TextField` swallows arrow keys for caret movement
/// and multi-line navigation, so once a D-pad lands inside it the user is
/// trapped — arrows never reach the traversal system. Attaching this node to
/// the `TextField` intercepts arrows *before* `EditableText` sees them and
/// converts them to [FocusNode.nextFocus] / [FocusNode.previousFocus], so the
/// user can D-pad out again. Letters / digits / enter / backspace pass through
/// unchanged.
///
/// [onEscape] (optional) is wired to Escape / browser-back so screens can pop
/// on the remote's Back key without each call site re-implementing it.
FocusNode dpadEscapeFocusNode({VoidCallback? onEscape}) {
  return FocusNode(onKeyEvent: (node, event) {
    if (event is! KeyDownEvent) return KeyEventResult.ignored;
    final key = event.logicalKey;

    if (_isDpadNext(key)) {
      node.nextFocus();
      return KeyEventResult.handled;
    }
    if (_isDpadPrevious(key)) {
      node.previousFocus();
      return KeyEventResult.handled;
    }
    if (onEscape != null &&
        (key == LogicalKeyboardKey.escape ||
            key == LogicalKeyboardKey.browserBack)) {
      onEscape();
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  });
}

/// Wraps a child in a focusable node that activates on TV activation keys
/// (Enter / Select / gamepad A) while preserving any inner tap target.
///
/// Use this when you have an existing tappable widget (a `GestureDetector`
/// card, an icon button) that should *also* be D-pad reachable, but you don't
/// want to rewrite it into a [FocusButton]. The inner gesture stays intact for
/// touch; this layer adds the focus + key path for remotes.
class TvFocus extends StatefulWidget {
  final Widget child;
  final VoidCallback onPressed;
  final FocusNode? focusNode;
  final bool autoFocus;
  final ValueChanged<bool>? onFocusChange;

  const TvFocus({
    super.key,
    required this.child,
    required this.onPressed,
    this.focusNode,
    this.autoFocus = false,
    this.onFocusChange,
  });

  @override
  State<TvFocus> createState() => _TvFocusState();
}

class _TvFocusState extends State<TvFocus> {
  late final FocusNode _node;

  @override
  void initState() {
    super.initState();
    _node = widget.focusNode ?? FocusNode();
    if (widget.onFocusChange != null) {
      _node.addListener(() => widget.onFocusChange!(_node.hasFocus));
    }
  }

  @override
  void dispose() {
    if (widget.focusNode == null) _node.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Focus(
      focusNode: _node,
      autofocus: widget.autoFocus,
      onKeyEvent: (node, event) {
        if (event is KeyDownEvent && isActivationKey(event.logicalKey)) {
          widget.onPressed();
          return KeyEventResult.handled;
        }
        return KeyEventResult.ignored;
      },
      // 同时响应触屏 tap —— TvFocus 的角色是"让任何 child 既可 D-pad 激活又可
      // tap",所以 GestureDetector 不能省,否则触屏路径会失效(只键盘能用)。
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.onPressed,
        child: widget.child,
      ),
    );
  }
}
