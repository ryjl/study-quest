import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:study_quest/model/exam.dart';
import 'package:study_quest/service/api_service.dart';

// 课程考试 model + ApiService 测试。对齐 wrong_book_test.dart 的 MockClient 范式
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

  group('ExamStatus.fromJson', () {
    test('parses available status', () {
      final s = ExamStatus.fromJson({'available': true});
      expect(s.available, true);
      expect(s.reason, '');
    });

    test('parses unavailable with reason', () {
      final s = ExamStatus.fromJson({
        'available': false,
        'reason': '课程题库不足,学完更多课后解锁考试',
      });
      expect(s.available, false);
      expect(s.reason, contains('题库不足'));
    });
  });

  group('ExamQuestion.fromJson', () {
    test('parses choice question with options list', () {
      final q = ExamQuestion.fromJson({
        'id': 5,
        'type': 'choice',
        'stem': '1+1=?',
        'options': ['1', '2', '3'],
        'has_jump': true,
      });
      expect(q.id, 5);
      expect(q.type, 'choice');
      expect(q.stem, '1+1=?');
      expect(q.options, ['1', '2', '3']);
      expect(q.hasJump, true);
      expect(q.isFill, false);
      expect(q.isMultiChoice, false);
    });

    test('parses multi_choice type', () {
      final q = ExamQuestion.fromJson({'id': 1, 'type': 'multi_choice', 'stem': 's', 'options': const []});
      expect(q.isMultiChoice, true);
    });

    test('decodes options given as JSON string', () {
      final q = ExamQuestion.fromJson({
        'id': 2, 'type': 'choice', 'stem': 's', 'options': '["a","b"]',
      });
      expect(q.options, ['a', 'b']);
    });

    test('defaults type to choice when missing', () {
      final q = ExamQuestion.fromJson({'id': 3, 'stem': 's'});
      expect(q.type, 'choice');
    });
  });

  group('ExamView.fromJson', () {
    test('parses exam with questions', () {
      final v = ExamView.fromJson({
        'exam_id': 42,
        'course_id': 7,
        'submitted': false,
        'questions': [
          {'id': 1, 'type': 'choice', 'stem': 'q1', 'options': const ['a', 'b']},
          {'id': 2, 'type': 'fill', 'stem': 'q2', 'options': const []},
        ],
      });
      expect(v.examId, 42);
      expect(v.courseId, 7);
      expect(v.submitted, false);
      expect(v.questions.length, 2);
      expect(v.questions[0].id, 1);
      expect(v.questions[1].isFill, true);
    });

    test('submitted defaults to false', () {
      final v = ExamView.fromJson({'exam_id': 1, 'course_id': 1, 'questions': const []});
      expect(v.submitted, false);
    });
  });

  group('ExamSubmitResult.fromJson', () {
    test('parses choice result with correct_index', () {
      final r = ExamSubmitResult.fromJson({
        'question_id': 9,
        'correct': true,
        'correct_index': 1,
        'explanation': '因为...',
        'source': 'pool',
      });
      expect(r.questionId, 9);
      expect(r.correct, true);
      expect(r.correctIndex, 1);
      expect(r.explanation, '因为...');
      expect(r.source, 'pool');
    });

    test('parses multi_choice result with correct_indices', () {
      final r = ExamSubmitResult.fromJson({
        'question_id': 3,
        'correct': false,
        'partial': true,
        'correct_indices': [0, 2],
        'source': 'generated',
      });
      expect(r.correct, false);
      expect(r.partial, true);
      expect(r.correctIndices, [0, 2]);
      expect(r.source, 'generated');
    });

    test('source defaults to pool', () {
      final r = ExamSubmitResult.fromJson({'question_id': 1, 'correct': true});
      expect(r.source, 'pool');
    });
  });

  group('ExamSubmitReport.fromJson', () {
    test('parses score + results', () {
      final rep = ExamSubmitReport.fromJson({
        'exam_id': 42,
        'score': 0.5,
        'results': [
          {'question_id': 1, 'correct': true, 'source': 'pool'},
          {'question_id': 2, 'correct': false, 'source': 'generated'},
        ],
      });
      expect(rep.examId, 42);
      expect(rep.score, 0.5);
      expect(rep.results.length, 2);
      expect(rep.results[0].correct, true);
      expect(rep.results[1].source, 'generated');
    });
  });

  // ── ApiService 端点 ──
  group('ApiService exam endpoints', () {
    test('fetchExamStatus parses available', () async {
      String? capturedUrl;
      ApiService.bindTestClient(MockClient((request) async {
        capturedUrl = request.url.path;
        return http.Response('{"available":true}', 200);
      }));
      final st = await ApiService.fetchExamStatus(1, 7);
      expect(capturedUrl, '/api/v1/courses/7/exam/status');
      expect(st.available, true);
    });

    test('fetchExamStatus 404 → unavailable 考试功能未启用', () async {
      ApiService.bindTestClient(MockClient((request) async {
        return http.Response('', 404);
      }));
      final st = await ApiService.fetchExamStatus(1, 7);
      expect(st.available, false);
      expect(st.reason, contains('未启用'));
    });

    test('startExam returns ExamView', () async {
      String? capturedUrl;
      ApiService.bindTestClient(MockClient((request) async {
        capturedUrl = request.url.path;
        return http.Response(
          '{"exam_id":5,"course_id":7,"questions":['
          '{"id":1,"type":"choice","stem":"q","options":["a","b"]}]}',
          200,
        );
      }));
      final v = await ApiService.startExam(1, 7);
      expect(capturedUrl, '/api/v1/courses/7/exam/start');
      expect(v.examId, 5);
      expect(v.questions.length, 1);
    });

    test('startExam 409 throws 题库不足 message', () async {
      ApiService.bindTestClient(MockClient((request) async {
        return http.Response('{"error":"x"}', 409);
      }));
      expect(() => ApiService.startExam(1, 7),
          throwsA(predicate((Object? e) => e.toString().contains('题库不足'))));
    });

    test('fetchActiveExam 200 with status:none → null', () async {
      ApiService.bindTestClient(MockClient((request) async {
        return http.Response('{"status":"none"}', 200);
      }));
      final v = await ApiService.fetchActiveExam(1, 7);
      expect(v, isNull);
    });

    test('fetchActiveExam 200 with exam payload → ExamView', () async {
      ApiService.bindTestClient(MockClient((request) async {
        return http.Response(
          '{"exam_id":5,"course_id":7,"submitted":false,"questions":[]}',
          200,
        );
      }));
      final v = await ApiService.fetchActiveExam(1, 7);
      expect(v, isNotNull);
      expect(v!.examId, 5);
    });

    test('submitExam returns report', () async {
      String? capturedUrl;
      String? capturedBody;
      ApiService.bindTestClient(MockClient((request) async {
        capturedUrl = request.url.path;
        capturedBody = request.body;
        return http.Response(
          '{"exam_id":5,"score":1.0,"results":['
          '{"question_id":1,"correct":true,"source":"pool"}]}',
          200,
        );
      }));
      final rep = await ApiService.submitExam(
        activeUserId: 1,
        examId: 5,
        answers: const [
          {'question_id': 1, 'answer_index': 1},
        ],
      );
      expect(capturedUrl, '/api/v1/exams/5/submit');
      expect(capturedBody, contains('"answers"'));
      expect(rep.score, 1.0);
      expect(rep.results.first.correct, true);
    });

    test('submitExam 409 throws 已交卷 message', () async {
      ApiService.bindTestClient(MockClient((request) async {
        return http.Response('{"error":"x"}', 409);
      }));
      expect(() => ApiService.submitExam(activeUserId: 1, examId: 5, answers: const []),
          throwsA(predicate((Object? e) => e.toString().contains('已交卷'))));
    });
  });
}
