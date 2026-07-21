import 'dart:ui';
import 'package:flutter/material.dart';

import '../../theme.dart';
import 'focus_button.dart';

/// A frosted-glass panel hosting a horizontal row of selectable chips.
///
/// Extracted verbatim from `PlayerScreenState`'s `_buildInlineMenuWrapper` +
/// `_buildCustomChip` so the three inline menus (speed / subtitle / audio) in
/// `player_screen.dart` can render their chips through one widget instead of
/// three near-identical copy-pastes.
class InlineChipMenu extends StatelessWidget {
  /// Prepended title label, e.g. "播放速度：".
  final String title;

  /// List of selectable chips. Each item carries its own label, selected
  /// state, and tap callback.
  final List<InlineChipItem> items;

  /// When non-null, the title row scrolls horizontally; otherwise the items
  /// are laid out inline. Both behaviours match the original screens.
  const InlineChipMenu({
    super.key,
    required this.title,
    required this.items,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 4, vertical: 4),
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      decoration: BoxDecoration(
        color: Colors.black.withValues(alpha: 0.55),
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: Colors.white.withValues(alpha: 0.08), width: 1),
      ),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(16),
        child: BackdropFilter(
          filter: ImageFilter.blur(sigmaX: 10, sigmaY: 10),
          child: Row(
            children: [
              Text(
                title,
                style: const TextStyle(
                  color: Colors.white70,
                  fontSize: 13,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: Row(
                    children: items
                        .map((e) => Padding(
                              padding: const EdgeInsets.only(right: 8.0),
                              child: _InlineChip(item: e),
                            ))
                        .toList(),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A single selectable chip shown inside an [InlineChipMenu].
class InlineChipItem {
  final String label;
  final bool selected;
  final VoidCallback onTap;

  const InlineChipItem({
    required this.label,
    required this.selected,
    required this.onTap,
  });
}

class _InlineChip extends StatelessWidget {
  final InlineChipItem item;
  const _InlineChip({required this.item});

  @override
  Widget build(BuildContext context) {
    final selected = item.selected;
    return FocusButton(
      onPressed: item.onTap,
      borderRadius: 16,
      baseColor: selected ? AppTheme.primaryColor : Colors.white.withValues(alpha: 0.12),
      borderColor: Colors.transparent,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (selected) ...[
            const Icon(Icons.check_rounded, color: Colors.white, size: 14),
            const SizedBox(width: 4),
          ],
          Text(
            item.label,
            style: TextStyle(
              color: selected ? Colors.white : Colors.white70,
              fontWeight: FontWeight.bold,
              fontSize: 13,
            ),
          ),
        ],
      ),
    );
  }
}
