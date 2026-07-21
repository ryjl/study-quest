import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:study_quest/theme.dart';
import 'package:study_quest/ui/widget/badge_style.dart';

void main() {
  group('BadgeStyle.tokenFor', () {
    test('consecutive_days rule → fire icon + orange tint', () {
      final t = BadgeStyle.tokenFor('badge_streak_30', 'consecutive_days');
      expect(t.icon, Icons.local_fire_department_rounded);
      expect(t.color, const Color(0xFFF97316));
      expect(t.bgColor, const Color(0xFFFFEDD5));
    });

    test('points_earned rule → stars icon + violet tint', () {
      final t = BadgeStyle.tokenFor('badge_points_1000', 'points_earned');
      expect(t.icon, Icons.stars_rounded);
      expect(t.color, const Color(0xFF8B5CF6));
      expect(t.bgColor, const Color(0xFFF5F3FF));
    });

    test('course_completion rule → trophy icon + amber tint', () {
      final t = BadgeStyle.tokenFor('badge_course_done', 'course_completion');
      expect(t.icon, Icons.emoji_events_rounded);
      expect(t.color, const Color(0xFFEAB308));
    });

    test('distinct_subject_count rule → compass icon + teal tint', () {
      final t = BadgeStyle.tokenFor('badge_explorer', 'distinct_subject_count');
      expect(t.icon, Icons.explore_rounded);
      expect(t.color, const Color(0xFF14B8A6));
    });

    test('watch_duration rule → timer icon + accentGreen', () {
      final t = BadgeStyle.tokenFor('badge_time_10h', 'watch_duration');
      expect(t.icon, Icons.timer_rounded);
      expect(t.color, AppTheme.accentGreen);
      expect(t.bgColor, const Color(0xFFECFDF5));
    });

    test('unknown rule → generic military_tech icon + fallback amber', () {
      final t = BadgeStyle.tokenFor('badge_mystery', 'unknown_rule');
      expect(t.icon, Icons.military_tech_rounded);
      expect(t.color, const Color(0xFFD97706));
      expect(t.bgColor, const Color(0xFFFEF3C7));
    });

    test('code substring match wins over ruleType for icon', () {
      // Even with unknown ruleType, a "streak" in the code → fire icon.
      final t = BadgeStyle.tokenFor('my_custom_streak', 'unknown_rule');
      expect(t.icon, Icons.local_fire_department_rounded);
    });

    test('subject-name code substrings pick subject icons', () {
      expect(BadgeStyle.tokenFor('math_whiz', 'x').icon, Icons.architecture_rounded);
      expect(BadgeStyle.tokenFor('english_pro', 'x').icon, Icons.translate_rounded);
      expect(BadgeStyle.tokenFor('chinese_ace', 'x').icon, Icons.edit_note_rounded);
      expect(BadgeStyle.tokenFor('physics_master', 'x').icon, Icons.science_rounded);
    });
  });

  group('ledger helpers', () {
    test('ledgerIcon maps known reasons', () {
      expect(ledgerIcon('system_watch'), Icons.play_circle_rounded);
      expect(ledgerIcon('badge_unlocked'), Icons.emoji_events_rounded);
      expect(ledgerIcon('something_else'), Icons.history_rounded);
    });

    test('ledgerFallbackTitle maps known reasons to Chinese', () {
      expect(ledgerFallbackTitle('system_watch'), '完成了一次视频学习');
      expect(ledgerFallbackTitle('badge_unlocked'), '解锁了一个新成就');
      expect(ledgerFallbackTitle(''), '积分变动');
    });

    test('formatLedgerTime relative labels', () {
      final now = DateTime.now();
      expect(formatLedgerTime(now), '刚刚');
      expect(formatLedgerTime(now.subtract(const Duration(minutes: 30))), contains('分钟前'));
      expect(formatLedgerTime(now.subtract(const Duration(hours: 5))), contains('小时前'));
      expect(formatLedgerTime(now.subtract(const Duration(days: 3))), contains('天前'));
    });
  });
}
