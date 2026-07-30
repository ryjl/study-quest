import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:study_quest/model/user.dart';
import 'package:study_quest/service/api_service.dart';
import 'package:study_quest/service/auth_service.dart';

User _user(int id) => User(
      id: id,
      nickname: 'user$id',
      avatarUrl: '',
      role: 'student',
      grade: '四年级',
    );

void main() {
  // device_info_plus needs the binding initialized for its platform channel
  // (it throws under test, which login swallows — but initializing here keeps
  // the debug noise out of test output).
  TestWidgetsFlutterBinding.ensureInitialized();

  // AuthService subscribes to static state on ApiService; reset each test.
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    ApiService.authToken = null;
    ApiService.onUnauthorized = null;
    ApiService.resetTestClient();
  });
  tearDown(() {
    ApiService.resetTestClient();
  });

  group('AuthService.login', () {
    test('on success persists user + token and sets ApiService.authToken',
        () async {
      // device_info_plus will fail under test (no platform channel) → login
      // swallows it and sends no device_name, which is the tested fallback.
      ApiService.bindTestClient(MockClient((req) async {
        return http.Response(
          jsonEncode({'token': 'tok-xyz', 'role': 'student', 'grade': '四年级', 'user_id': 3}),
          200,
          // 含中文(grade)必须带 utf-8 content-type,否则 http.Response 默认 latin1 编码会抛异常。
          headers: const {'content-type': 'application/json; charset=utf-8'},
        );
      }));

      final svc = AuthService();
      final ok = await svc.login(_user(3), '123456');
      expect(ok, isTrue);
      expect(svc.isAuthenticated, isTrue);
      expect(svc.currentUser?.id, 3);
      expect(svc.currentUser?.grade, '四年级');
      expect(ApiService.authToken, 'tok-xyz');

      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getString('auth_token'), 'tok-xyz');
      expect(prefs.getString('current_authenticated_user') != null, isTrue);
    });

    test('on failure leaves state untouched', () async {
      ApiService.bindTestClient(MockClient((req) async {
        return http.Response('{"error":"no"}', 401);
      }));
      final svc = AuthService();
      final ok = await svc.login(_user(3), '0000');
      expect(ok, isFalse);
      expect(svc.isAuthenticated, isFalse);
      expect(svc.currentUser, isNull);
      expect(ApiService.authToken, isNull);
    });
  });

  group('AuthService.init upgrade compatibility', () {
    test('user cached but NO token (old client) → treated as logged out',
        () async {
      // Simulate an upgrade from the pre-session scheme: only the user was
      // cached, no auth_token key exists.
      SharedPreferences.setMockInitialValues({
        'current_authenticated_user': jsonEncode({
          'ID': 7,
          'Nickname': 'legacy',
          'AvatarURL': '',
          'Role': 'student',
        }),
      });

      final svc = AuthService();
      await svc.init();
      expect(svc.isAuthenticated, isFalse,
          reason: 'user without token must be logged out');
      expect(svc.currentUser, isNull);

      // The stale user should also be cleaned up so it can't rehydrate later.
      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getString('current_authenticated_user'), isNull);
    });

    test('user + token cached → authenticated', () async {
      SharedPreferences.setMockInitialValues({
        'current_authenticated_user': jsonEncode({
          'ID': 7,
          'Nickname': 'good',
          'AvatarURL': '',
          'Role': 'student',
        }),
        'auth_token': 'persisted-tok',
      });
      final svc = AuthService();
      await svc.init();
      expect(svc.isAuthenticated, isTrue);
      expect(svc.currentUser?.id, 7);
      expect(ApiService.authToken, 'persisted-tok');
    });

    test('nothing cached → not authenticated', () async {
      final svc = AuthService();
      await svc.init();
      expect(svc.isAuthenticated, isFalse);
    });
  });

  group('AuthService.logout', () {
    test('clears user, token, and ApiService.authToken', () async {
      // Seed an authenticated state.
      SharedPreferences.setMockInitialValues({
        'current_authenticated_user': jsonEncode({
          'ID': 9,
          'Nickname': 'x',
          'AvatarURL': '',
          'Role': 'student',
        }),
        'auth_token': 'before',
      });
      ApiService.bindTestClient(MockClient((req) async {
        return http.Response('{"status":"ok"}', 200);
      }));
      final svc = AuthService();
      await svc.init();

      await svc.logout();
      expect(svc.isAuthenticated, isFalse);
      expect(svc.currentUser, isNull);
      expect(ApiService.authToken, isNull);
      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getString('current_authenticated_user'), isNull);
      expect(prefs.getString('auth_token'), isNull);
    });
  });

  group('AuthService.onUnauthorized callback', () {
    test('a 401 from any authenticated call logs the user out', () async {
      // Authenticate first.
      ApiService.bindTestClient(MockClient((req) async {
        if (req.url.path.endsWith('/login')) {
          return http.Response(
            jsonEncode({'token': 't', 'role': 'student', 'user_id': 1}),
            200,
          );
        }
        // Any protected call 401s.
        return http.Response('{"error":"unauthorized"}', 401);
      }));
      final svc = AuthService();
      await svc.login(_user(1), '123456');
      expect(svc.isAuthenticated, isTrue);

      // A protected call that 401s should trigger logout via onUnauthorized.
      await expectLater(ApiService.fetchCourses(1), throwsA(isA<Exception>()));
      // The onUnauthorized callback runs fire-and-forget; give it a turn.
      await Future.delayed(Duration.zero);
      expect(svc.isAuthenticated, isFalse,
          reason: '401 should have logged the user out via the global hook');
    });
  });
}
