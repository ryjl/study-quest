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
}
