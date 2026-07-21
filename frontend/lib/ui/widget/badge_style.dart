import 'package:flutter/material.dart';

import '../../theme.dart';

/// Consolidated badge / ledger presentation tokens.
///
/// The three parallel switch statements (`_badgeIcon` / `_badgeColor` /
/// `_badgeBgColor`) previously lived as private methods inside
/// `_MainNavigationState`; consolidating them here means a new badge rule
/// only needs to be added in one place. The ledger helpers are colocated
/// because they share the same "rule-type → visual token" domain.
class BadgeStyle {
  final IconData icon;
  final Color color;
  final Color bgColor;

  const BadgeStyle({
    required this.icon,
    required this.color,
    required this.bgColor,
  });

  /// Returns the icon/color/bg triple for a given badge rule type.
  ///
  /// The icon lookup also falls back to a substring match on the badge
  /// `code` (e.g. any code containing "streak" maps to the fire icon),
  /// so newly-added badges without an explicit rule branch still render
  /// something sensible instead of the generic military_tech fallback.
  static BadgeStyle tokenFor(String code, String ruleType) {
    return BadgeStyle(
      icon: _iconFor(code, ruleType),
      color: colorFor(ruleType),
      bgColor: bgColorFor(ruleType),
    );
  }

  static IconData _iconFor(String code, String ruleType) {
    final c = code.toLowerCase();
    if (c.contains('streak') || ruleType == 'consecutive_days') {
      return Icons.local_fire_department_rounded;
    }
    if (c.contains('first')) return Icons.flag_rounded;
    if (c.contains('time') || ruleType == 'watch_duration') {
      return Icons.timer_rounded;
    }
    if (c.contains('episode') || ruleType == 'episode_completed_count') {
      return Icons.check_circle_outline_rounded;
    }
    if (c.contains('course') || ruleType == 'course_completion') {
      return Icons.emoji_events_rounded;
    }
    if (c.contains('point') || ruleType == 'points_earned') {
      return Icons.stars_rounded;
    }
    if (c.contains('explorer') || ruleType == 'distinct_subject_count') {
      return Icons.explore_rounded;
    }
    if (c.contains('weekly') || ruleType == 'weekly_all_present') {
      return Icons.calendar_month_rounded;
    }
    if (c.contains('math')) return Icons.architecture_rounded;
    if (c.contains('english')) return Icons.translate_rounded;
    if (c.contains('chinese')) return Icons.edit_note_rounded;
    if (c.contains('physics')) return Icons.science_rounded;
    return Icons.military_tech_rounded;
  }

  static Color colorFor(String ruleType) {
    switch (ruleType) {
      case 'consecutive_days':
        return const Color(0xFFF97316);
      case 'subject_count':
        return const Color(0xFF3B82F6);
      case 'episode_completed_count':
        return const Color(0xFF0EA5E9);
      case 'points_earned':
        return const Color(0xFF8B5CF6);
      case 'course_completion':
        return const Color(0xFFEAB308);
      case 'weekly_all_present':
        return const Color(0xFFEC4899);
      case 'distinct_subject_count':
        return const Color(0xFF14B8A6);
      case 'watch_duration':
        return AppTheme.accentGreen;
      default:
        return const Color(0xFFD97706);
    }
  }

  static Color bgColorFor(String ruleType) {
    switch (ruleType) {
      case 'consecutive_days':
        return const Color(0xFFFFEDD5);
      case 'subject_count':
        return const Color(0xFFEFF6FF);
      case 'episode_completed_count':
        return const Color(0xFFE0F2FE);
      case 'points_earned':
        return const Color(0xFFF5F3FF);
      case 'course_completion':
        return const Color(0xFFFEF9C3);
      case 'weekly_all_present':
        return const Color(0xFFFCE7F3);
      case 'distinct_subject_count':
        return const Color(0xFFCCFBF1);
      case 'watch_duration':
        return const Color(0xFFECFDF5);
      default:
        return const Color(0xFFFEF3C7);
    }
  }
}

// --- Ledger timeline helpers ---

IconData ledgerIcon(String reasonType) {
  switch (reasonType) {
    case 'system_watch':
      return Icons.play_circle_rounded;
    case 'badge_unlocked':
      return Icons.emoji_events_rounded;
    default:
      return Icons.history_rounded;
  }
}

String ledgerFallbackTitle(String reasonType) {
  switch (reasonType) {
    case 'system_watch':
      return '完成了一次视频学习';
    case 'badge_unlocked':
      return '解锁了一个新成就';
    default:
      return '积分变动';
  }
}

String formatLedgerTime(DateTime t) {
  // Backend stores UTC; show a friendly relative-ish label.
  final now = DateTime.now();
  final diff = now.difference(t);
  if (diff.inMinutes < 1) return '刚刚';
  if (diff.inMinutes < 60) return '${diff.inMinutes} 分钟前';
  if (diff.inHours < 24) return '${diff.inHours} 小时前';
  if (diff.inDays < 7) return '${diff.inDays} 天前';
  return '${t.month}/${t.day}';
}
