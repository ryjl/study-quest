import 'package:flutter/material.dart';
import '../../theme.dart';
import 'focus_button.dart';

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
    return Container(
      width: 380,
      padding: const EdgeInsets.all(24),
      decoration: BoxDecoration(
        color: AppTheme.backgroundColor,
        borderRadius: BorderRadius.circular(AppTheme.borderRadiusValue),
        border: Border.all(color: AppTheme.borderMuted, width: AppTheme.borderWidthValue),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Header
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                widget.title,
                style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 18),
              ),
              IconButton(
                icon: const Icon(Icons.close, color: AppTheme.textMuted),
                onPressed: widget.onCancel,
              ),
            ],
          ),
          const SizedBox(height: 18),

          // Display dots
          Container(
            height: 60,
            alignment: Alignment.center,
            decoration: BoxDecoration(
              color: Colors.black.withOpacity(0.3),
              borderRadius: BorderRadius.circular(AppTheme.borderRadiusValue / 2),
            ),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: List.generate(widget.maxDigits, (index) {
                final filled = index < _currentPin.length;
                return Container(
                  margin: const EdgeInsets.symmetric(horizontal: 10),
                  width: 14,
                  height: 14,
                  decoration: BoxDecoration(
                    color: filled ? AppTheme.primaryColor : Colors.transparent,
                    shape: BoxShape.circle,
                    border: Border.all(
                      color: filled ? AppTheme.primaryColor : AppTheme.textMuted.withOpacity(0.4),
                      width: 2,
                    ),
                  ),
                );
              }),
            ),
          ),
          const SizedBox(height: 24),

          // 3x4 Keypad Grid
          Column(
            children: [
              Row(
                children: [
                  _buildKey('1'),
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
        ],
      ),
    );
  }

  Widget _buildKey(String label) {
    return Expanded(
      child: FocusButton(
        padding: const EdgeInsets.symmetric(vertical: 16),
        onPressed: () => _onKeyPress(label),
        child: Text(
          label,
          textAlign: TextAlign.center,
          style: const TextStyle(fontSize: 22, fontWeight: FontWeight.bold),
        ),
      ),
    );
  }

  Widget _buildActionKey(String label, VoidCallback action) {
    return Expanded(
      child: FocusButton(
        padding: const EdgeInsets.symmetric(vertical: 16),
        borderColor: AppTheme.borderMuted,
        onPressed: action,
        child: Text(
          label,
          textAlign: TextAlign.center,
          style: TextStyle(
            fontSize: label == '⌫' ? 18 : 20,
            fontWeight: FontWeight.bold,
            color: label == 'C' ? AppTheme.accentOrange : AppTheme.textMuted,
          ),
        ),
      ),
    );
  }
}
