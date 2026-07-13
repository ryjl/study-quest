import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:study_quest/service/api_service.dart';

void main() {
  // ApiService uses static state (authToken, onUnauthorized, http client) that
  // persists across tests; reset before AND after each so tests stay isolated.
  setUp(() {
    ApiService.authToken = null;
    ApiService.onUnauthorized = null;
    ApiService.resetTestClient();
  });
  tearDown(() {
    ApiService.resetTestClient();
  });

  group('ApiService._headers (via loginUser)', () {
    test('loginUser stores and returns the opaque token on 200', () async {
      ApiService.bindTestClient(MockClient((request) async {
        expect(request.url.path, endsWith('/api/v1/users/login'));
        return http.Response(
          jsonEncode({'token': 'opaque-abc', 'role': 'student', 'user_id': 5}),
          200,
        );
      }));

      final tok = await ApiService.loginUser(5, '1234');
      expect(tok, 'opaque-abc');
      expect(ApiService.authToken, 'opaque-abc');
    });

    test('loginUser returns null on wrong PIN (401) and does not set token',
        () async {
      ApiService.bindTestClient(MockClient((request) async {
        return http.Response(jsonEncode({'error': 'nope'}), 401);
      }));
      final tok = await ApiService.loginUser(5, '9999');
      expect(tok, isNull);
      expect(ApiService.authToken, isNull);
    });

    test('loginUser forwards device_name when provided', () async {
      String? capturedDevice;
      ApiService.bindTestClient(MockClient((request) async {
        final body = jsonDecode(request.body) as Map<String, dynamic>;
        capturedDevice = body['device_name'] as String?;
        return http.Response(
          jsonEncode({'token': 'tok', 'role': 'student', 'user_id': 1}),
          200,
        );
      }));
      await ApiService.loginUser(1, '1234', deviceName: '客厅iPad');
      expect(capturedDevice, '客厅iPad');
    });
  });

  group('ApiService 401 handling', () {
    test('a 401 from a protected route triggers onUnauthorized', () async {
      var triggered = 0;
      ApiService.onUnauthorized = () async {
        triggered++;
      };
      ApiService.authToken = 'some-token';
      ApiService.bindTestClient(MockClient((request) async {
        // Any protected endpoint that comes back 401.
        return http.Response(jsonEncode({'error': 'unauthorized'}), 401);
      }));

      // fetchCourses throws (non-200); the 401 hook should fire exactly once.
      await expectLater(
        ApiService.fetchCourses(1),
        throwsA(isA<Exception>()),
      );
      expect(triggered, 1);
    });

    test('a non-401 failure does NOT trigger onUnauthorized', () async {
      var triggered = 0;
      ApiService.onUnauthorized = () async {
        triggered++;
      };
      ApiService.authToken = 'some-token';
      ApiService.bindTestClient(MockClient((request) async {
        return http.Response('server error', 500);
      }));
      await expectLater(
        ApiService.fetchCourses(1),
        throwsA(isA<Exception>()),
      );
      expect(triggered, 0);
    });
  });

  group('ApiService.logout', () {
    test('clears the local token even when the network call fails', () async {
      ApiService.authToken = 'will-be-cleared';
      ApiService.bindTestClient(MockClient((request) async {
        throw http.ClientException('network down');
      }));
      // Must not throw — logout swallows network errors.
      await ApiService.logout();
      expect(ApiService.authToken, isNull);
    });

    test('posts to the logout endpoint with the bearer token', () async {
      ApiService.authToken = 'abc';
      String? sentAuth;
      String? sentPath;
      ApiService.bindTestClient(MockClient((request) async {
        sentAuth = request.headers['Authorization'];
        sentPath = request.url.path;
        return http.Response('{"status":"ok"}', 200);
      }));
      await ApiService.logout();
      expect(sentAuth, 'Bearer abc');
      expect(sentPath, endsWith('/api/v1/users/logout'));
      expect(ApiService.authToken, isNull);
    });
  });
}
