import 'package:flutter/material.dart';
import '../../theme.dart';
import 'button_3d.dart';
import 'glass_panel.dart';

class NumPad extends StatefulWidget {
  final String title;
  final int maxDigits;
  final Function(String) onSubmit;
  final VoidCallback onCancel;

  const NumPad({
    Key? key,
    this.title = '请输入 PIN 登录码',
    this.maxDigits = 6,
    required this.onSubmit,
    required this.onCancel,
  }) : super(key: key);

  @override
  State<NumPad> createState() => _NumPadState();
}

class _NumPadState extends State<NumPad> {
  String _currentPin = '';

  void _onKeyPress(String value) {
    if (_currentPin.length < widget.maxDigits) {
      setState(() {
        _currentPin += value;
      });
      // Auto-submit if max digits reached
      if (_currentPin.length == widget.maxDigits) {
        widget.onSubmit(_currentPin);
      }
    }
  }

  void _backspace() {
    if (_currentPin.isNotEmpty) {
      setState(() {
        _currentPin = _currentPin.substring(0, _currentPin.length - 1);
      });
    }
  }

  void _clear() {
    setState(() {
      _currentPin = '';
    });
  }

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return GlassPanel(
      borderRadius: 24.0,
      baseColor: colors.cardColor.withValues(alpha: 0.92),
      borderColor: colors.cardColor,
      borderWidth: 1.5,
      padding: const EdgeInsets.all(24),
      child: ConstrainedBox(
        // Cap at 330 on wide screens, but allow shrinking on narrow portrait
        // screens so the pad never overflows (360px screen − 48px panel padding
        // leaves 312px; a fixed 330 would overflow by ~18px).
        constraints: const BoxConstraints(maxWidth: 330),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            // Header
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  widget.title,
                  style: TextStyle(
                    fontFamily: 'Quicksand',
                    fontWeight: FontWeight.bold,
                    fontSize: 16,
                    color: colors.textWhite,
                  ),
                ),
                IconButton(
                  icon: Icon(Icons.close, color: colors.textMuted),
                  onPressed: widget.onCancel,
                ),
              ],
            ),
            const SizedBox(height: 18),

            // Display dots
            Container(
              height: 54,
              alignment: Alignment.center,
              decoration: BoxDecoration(
                color: colors.slate900.withValues(alpha: 0.05),
                borderRadius: BorderRadius.circular(16),
              ),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: List.generate(widget.maxDigits, (index) {
                  final filled = index < _currentPin.length;
                  return Container(
                    margin: const EdgeInsets.symmetric(horizontal: 10),
                    width: 12,
                    height: 12,
                    decoration: BoxDecoration(
                      color: filled ? colors.primaryColor : Colors.transparent,
                      shape: BoxShape.circle,
                      border: Border.all(
                        color: filled ? colors.primaryColor : colors.textMuted.withValues(alpha: 0.3),
                        width: 2,
                      ),
                    ),
                  );
                }),
              ),
            ),
            const SizedBox(height: 24),

            // 3x4 Keypad Grid
            //
            // 【TV 适配】FocusTraversalGroup 让 12 个键共享一个 reading-order
            // 遍历顺序,D-pad 上下左右按视觉网格跳。第一个键(1)给 autofocus,
            // 这样 PIN 蒙层打开时(外层 login_screen 已用 FocusScope 引导焦点进
            // 来),D-pad 首个落点是「1」而不是飘到背后模糊的用户卡上。
            FocusTraversalGroup(
              child: Column(
                children: [
                  Row(
                    children: [
                      _buildKey('1', autoFocus: true),
                      const SizedBox(width: 12),
                      _buildKey('2'),
                      const SizedBox(width: 12),
                      _buildKey('3'),
                    ],
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      _buildKey('4'),
                      const SizedBox(width: 12),
                      _buildKey('5'),
                      const SizedBox(width: 12),
                      _buildKey('6'),
                    ],
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      _buildKey('7'),
                      const SizedBox(width: 12),
                      _buildKey('8'),
                      const SizedBox(width: 12),
                      _buildKey('9'),
                    ],
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      _buildActionKey('C', _clear),
                      const SizedBox(width: 12),
                      _buildKey('0'),
                      const SizedBox(width: 12),
                      _buildActionKey('⌫', _backspace),
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildKey(String label, {bool autoFocus = false}) {
    return Expanded(
      child: Button3D.white(
        autoFocus: autoFocus,
        padding: const EdgeInsets.symmetric(vertical: 14),
        onPressed: () => _onKeyPress(label),
        child: Text(
          label,
          textAlign: TextAlign.center,
          style: const TextStyle(fontSize: 20, fontWeight: FontWeight.bold, color: AppTheme.textWhite),
        ),
      ),
    );
  }

  Widget _buildActionKey(String label, VoidCallback action) {
    return Expanded(
      child: Button3D.white(
        padding: const EdgeInsets.symmetric(vertical: 14),
        onPressed: action,
        child: Text(
          label,
          textAlign: TextAlign.center,
          style: TextStyle(
            fontSize: label == '⌫' ? 16 : 18,
            fontWeight: FontWeight.bold,
            color: label == 'C' ? AppTheme.accentOrange : AppTheme.textMuted,
          ),
        ),
      ),
    );
  }
}

