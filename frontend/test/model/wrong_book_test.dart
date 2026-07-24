import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:study_quest/model/wrong_book.dart';
import 'package:study_quest/service/api_service.dart';

// 错题本 model + ApiService 测试。对齐 api_service_test.dart 的 MockClient 范式
// (ApiService 持静态可变状态,setUp/tearDown 必须重置)。

void main() {
  setUp(() {
    ApiService.authToken = 'test-token';
    ApiService.onUnauthorized = null;
    ApiService.resetTestClient();
  });
  tearDown(() {
    ApiService.resetTestClient();
  });

  group('WrongBookItem.fromJson', () {
    test('parses a full choice row', () {
      final item = WrongBookItem.fromJson({
        'question_id': 7,
        'stem': '通分怎么做',
        'type': 'choice',
        'options': ['A', 'B', 'C'],
        'explanation': '找公分母',
        'has_jump': true,
        'chunk_id': 100,
        'course_id': 200,
        'episode_id': 300,
        'subject_id': 5,
        'first_wrong_at': '2026-07-23T00:00:00Z',
        'last_attempted_at': '2026-07-23T01:00:00Z',
        'attempt_count': 3,
        'mastered': false,
      });
      expect(item.questionId, 7);
      expect(item.stem, '通分怎么做');
      expect(item.type, 'choice');
      expect(item.options, ['A', 'B', 'C']);
      expect(item.attemptCount, 3);
      expect(item.mastered, isFalse);
      expect(item.hasJump, isTrue);
    });

    test('parses options from JSON string (backend may send raw string)', () {
      // 后端 options 字段在某些路径下可能是 JSON 字符串而非数组;fromJson 要兜底解析。
      final item = WrongBookItem.fromJson({
        'question_id': 1,
        'stem': 'q',
        'type': 'choice',
        'options': '["甲","乙"]',
        'explanation': '',
        'has_jump': false,
        'chunk_id': 0,
        'course_id': 1,
        'episode_id': 1,
        'subject_id': 0,
        'first_wrong_at': '',
        'last_attempted_at': '',
        'attempt_count': 1,
        'mastered': false,
      });
      expect(item.options, ['甲', '乙']);
    });

    test('missing fields degrade to safe defaults (no null)', () {
      final item = WrongBookItem.fromJson({});
      expect(item.questionId, 0);
      expect(item.stem, '');
      expect(item.options, isEmpty);
      expect(item.mastered, isFalse);
      expect(item.attemptCount, 0);
    });

    test('parses correct answer fields + streak', () {
      final item = WrongBookItem.fromJson({
        'question_id': 5, 'stem': 's', 'type': 'multi_choice',
        'correct_indices': [0, 2], 'correct_streak': 2, 'attempt_count': 1,
      });
      expect(item.correctIndices, [0, 2]);
      expect(item.correctStreak, 2);
      expect(item.correctIndex, isNull);
      expect(item.correctText, '');
    });

    test('choice question parses correct_index', () {
      final item = WrongBookItem.fromJson({
        'question_id': 6, 'stem': 's', 'type': 'choice', 'correct_index': 1,
      });
      expect(item.correctIndex, 1);
    });

    test('fill question parses correct_text', () {
      final item = WrongBookItem.fromJson({
        'question_id': 7, 'stem': 's', 'type': 'fill', 'correct_text': '答案',
      });
      expect(item.correctText, '答案');
    });
  });

  group('WrongBookList.fromJson', () {
    test('parses items + unmastered_count', () {
      final list = WrongBookList.fromJson({
        'items': [
          {'question_id': 1, 'stem': 'q1', 'type': 'choice'},
          {'question_id': 2, 'stem': 'q2', 'type': 'choice', 'mastered': true},
        ],
        'unmastered_count': 1,
      });
      expect(list.items.length, 2);
      expect(list.unmasteredCount, 1);
    });

    test('empty items + missing count degrade safely', () {
      final list = WrongBookList.fromJson({});
      expect(list.items, isEmpty);
      expect(list.unmasteredCount, 0);
    });
  });

  group('WrongBookRedoResult.fromJson', () {
    test('parses multi_choice result with correct_indices', () {
      final r = WrongBookRedoResult.fromJson({
        'question_id': 9,
        'correct': false,
        'partial': true,
        'correct_indices': [0, 2],
        'explanation': '注意漏选',
      });
      expect(r.questionId, 9);
      expect(r.correct, isFalse);
      expect(r.partial, isTrue);
      expect(r.correctIndices, [0, 2]);
      expect(r.explanation, '注意漏选');
    });

    test('parses choice result with correct_index', () {
      final r = WrongBookRedoResult.fromJson({
        'question_id': 1,
        'correct': true,
        'correct_index': 2,
      });
      expect(r.correct, isTrue);
      expect(r.correctIndex, 2);
      expect(r.correctIndices, isEmpty);
    });
  });

  group('ApiService wrong-book endpoints', () {
    test('fetchWrongBook parses items array + unmastered_count', () async {
      ApiService.bindTestClient(MockClient((request) async {
        expect(request.url.path, endsWith('/api/v1/wrong-book'));
        return http.Response(jsonEncode({
          'items': [
            {'question_id': 1, 'stem': 'q1', 'type': 'choice', 'options': [], 'first_wrong_at': '', 'attempt_count': 1, 'mastered': false, 'course_id': 0, 'episode_id': 0, 'subject_id': 0, 'has_jump': false, 'explanation': ''},
            {'question_id': 2, 'stem': 'q2', 'type': 'fill', 'options': [], 'first_wrong_at': '', 'attempt_count': 2, 'mastered': true, 'course_id': 0, 'episode_id': 0, 'subject_id': 0, 'has_jump': false, 'explanation': ''},
          ],
          'unmastered_count': 1,
        }), 200);
      }));
      final list = await ApiService.fetchWrongBook(1);
      expect(list.items.length, 2);
      expect(list.items[0].stem, 'q1');
      expect(list.items[1].mastered, isTrue);
      expect(list.unmasteredCount, 1);
    });

    test('fetchWrongBook appends mastered=false query when filtering', () async {
      String? capturedPath;
      ApiService.bindTestClient(MockClient((request) async {
        capturedPath = request.url.path + (request.url.query.isEmpty ? '' : '?${request.url.query}');
        return http.Response(jsonEncode({'items': [], 'unmastered_count': 0}), 200);
      }));
      await ApiService.fetchWrongBook(1, mastered: false);
      expect(capturedPath, contains('mastered=false'));
    });

    test('fetchWrongBook returns empty on 404 (AI off, graceful)', () async {
      ApiService.bindTestClient(MockClient((request) async {
        return http.Response('', 404);
      }));
      final list = await ApiService.fetchWrongBook(1);
      expect(list.items, isEmpty);
      expect(list.unmasteredCount, 0);
    });

    test('markWrongBookMastered hits /master suffix when true', () async {
      String? capturedPath;
      ApiService.bindTestClient(MockClient((request) async {
        capturedPath = request.url.path;
        return http.Response(jsonEncode({'ok': true}), 200);
      }));
      final result = await ApiService.markWrongBookMastered(1, 42, true);
      expect(capturedPath, endsWith('/wrong-book/42/master'));
      expect(result, isTrue);
    });

    test('markWrongBookMastered hits /unmaster suffix when false', () async {
      String? capturedPath;
      ApiService.bindTestClient(MockClient((request) async {
        capturedPath = request.url.path;
        return http.Response(jsonEncode({'ok': true}), 200);
      }));
      await ApiService.markWrongBookMastered(1, 42, false);
      expect(capturedPath, endsWith('/wrong-book/42/unmaster'));
    });

    test('fetchWrongBookRedo parses questions array', () async {
      ApiService.bindTestClient(MockClient((request) async {
        return http.Response(jsonEncode({
          'questions': [
            {'id': 10, 'type': 'choice', 'stem': 'redo-q', 'options': ['A', 'B'], 'has_jump': true},
          ],
        }), 200);
      }));
      final qs = await ApiService.fetchWrongBookRedo(1);
      expect(qs.length, 1);
      expect(qs[0].id, 10);
      expect(qs[0].options, ['A', 'B']);
    });

    test('submitWrongBookRedo posts answers and parses results', () async {
      Map<String, dynamic>? capturedBody;
      ApiService.bindTestClient(MockClient((request) async {
        capturedBody = jsonDecode(request.body) as Map<String, dynamic>;
        return http.Response(jsonEncode({
          'results': [
            {'question_id': 10, 'correct': true, 'correct_index': 0},
          ],
        }), 200);
      }));
      final results = await ApiService.submitWrongBookRedo(
        activeUserId: 1,
        answers: [
          {'question_id': 10, 'answer_index': 0},
        ],
      );
      expect(capturedBody!['answers'], isA<List>());
      expect(results.length, 1);
      expect(results[0].correct, isTrue);
    });
  });
}
