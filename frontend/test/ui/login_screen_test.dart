import 'package:flutter_test/flutter_test.dart';
import 'package:study_quest/ui/screen/login_screen.dart';

// The login screen's lockout UX turns the backend's Retry-After (seconds) into
// a human message. This tests the formatting logic in isolation — the full
// screen's HTTP/state wiring is covered by api_service_test + auth_service_test,
// and the NumPad clear behavior by num_pad_test. A full widget test of the
// login overlay (BackdropFilter + async login + platform channels) proved too
// brittle to maintain, so the pieces are tested directly.
void main() {
  group('formatLockoutWait', () {
    test('sub-minute seconds render as "N 秒"', () {
      expect(formatLockoutWait(0), '0 秒');
      expect(formatLockoutWait(1), '1 秒');
      expect(formatLockoutWait(59), '59 秒');
    });

    test('whole minutes render as "N 分钟"', () {
      expect(formatLockoutWait(60), '1 分钟');
      expect(formatLockoutWait(900), '15 分钟'); // the default 15-min lockout
      expect(formatLockoutWait(1800), '30 分钟');
    });

    test('minutes with a remainder render as "N 分 M 秒"', () {
      expect(formatLockoutWait(65), '1 分 5 秒');
      expect(formatLockoutWait(125), '2 分 5 秒');
      expect(formatLockoutWait(901), '15 分 1 秒');
    });
  });

  // The countdown timer recomputes the message each second from the unlock
  // instant the backend reported. This is DISPLAY-ONLY: the frontend never
  // decides whether the account is locked (that's the backend's call on every
  // login attempt). These tests verify the "remaining seconds" math that
  // drives the message — e.g. a backend-reported 15-min wait, 5 real minutes
  // later, shows "10 分钟" (not the frozen original "15 分钟"). The timer
  // machinery (Timer.periodic + setState) is glue over these two pure funcs.
  group('secondsUntilUnlock', () {
    test('15-min lockout, 5 min later → 600s (→ "10 分钟")', () {
      final locked = DateTime(2026, 7, 30, 12, 0, 0);
      final now = locked.add(const Duration(minutes: 5));
      final remaining = secondsUntilUnlock(locked.add(const Duration(minutes: 15)), now);
      expect(remaining, 600);
      expect(formatLockoutWait(remaining), '10 分钟');
    });

    test('decrements each second', () {
      final locked = DateTime(2026, 7, 30, 12, 0, 0);
      final unlock = locked.add(const Duration(seconds: 30));
      expect(secondsUntilUnlock(unlock, locked), 30);
      expect(secondsUntilUnlock(unlock, locked.add(const Duration(seconds: 10))), 20);
      expect(secondsUntilUnlock(unlock, locked.add(const Duration(seconds: 29))), 1);
    });

    test('returns ≤ 0 once elapsed (clears the hint; backend still gates)', () {
      final unlock = DateTime(2026, 7, 30, 12, 0, 0);
      expect(secondsUntilUnlock(unlock, unlock), 0);
      expect(secondsUntilUnlock(unlock, unlock.add(const Duration(seconds: 1))), -1);
    });
  });
}
