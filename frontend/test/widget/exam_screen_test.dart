import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:study_quest/service/api_service.dart';
import 'package:study_quest/ui/screen/course_exam_screen.dart';

// 课程考试屏幕级 widget 测试。建立范式:CourseExamScreen 不依赖 provider,只调
// ApiService 静态方法,harness 用 MaterialApp + bindTestClient(MockClient)。
// mock 按 URL 路径路由(status / start / exam / submit),对齐 exam 状态机。
//
// 注意:含中文的响应体必须用 jsonEncode 包(转义成 \uXXXX ASCII),否则
// http.Response 默认按 latin1 编码会抛 "Contains invalid characters"。
// (对齐 wrong_book_screen_test.dart 的 itemsResponse 范式。)

void main() {
  setUp(() {
    ApiService.authToken = 'test-token';
    ApiService.onUnauthorized = null;
    ApiService.resetTestClient();
  });
  tearDown(() {
    ApiService.resetTestClient();
  });

  Future<void> _pumpScreen(WidgetTester tester) async {
    await tester.pumpWidget(MaterialApp(
      home: CourseExamScreen(
        activeUserId: 1,
        courseId: 7,
        courseTitle: 'Exam Course',
      ),
    ));
    await tester.pumpAndSettle();
  }

  // 按路径路由 mock 响应。含中文,必须带 utf-8 content-type,否则
  // http.Response 默认 latin1 编码抛 "Contains invalid characters"。
  void bindRouter(Map<String, dynamic>? Function(String path) responder) {
    ApiService.bindTestClient(MockClient((request) async {
      final path = request.url.path;
      final body = responder(path);
      if (body == null) return http.Response('', 500);
      return http.Response(
        jsonEncode(body), 200,
        headers: const {'content-type': 'application/json; charset=utf-8'},
      );
    }));
  }

  testWidgets('题库不足(status unavailable)→ 显示灰显提示', (tester) async {
    bindRouter((path) {
      if (path.endsWith('/exam/status')) {
        return {'available': false, 'reason': '课程题库不足'};
      }
      return {};
    });
    await _pumpScreen(tester);
    expect(find.text('暂时不能考试'), findsOneWidget);
    expect(find.text('课程题库不足'), findsOneWidget);
  });

  testWidgets('可考且无 active exam → 显示开考入口', (tester) async {
    bindRouter((path) {
      if (path.endsWith('/exam/status')) return {'available': true};
      if (path.endsWith('/exam')) return {'status': 'none'};
      return {};
    });
    await _pumpScreen(tester);
    expect(find.text('准备好考试了吗?'), findsOneWidget);
    expect(find.text('开始考试'), findsOneWidget);
  });

  testWidgets('可考且有未交卷 active exam → 直接显示卷子 + 提交按钮', (tester) async {
    bindRouter((path) {
      if (path.endsWith('/exam/status')) return {'available': true};
      if (path.endsWith('/exam')) {
        return {
          'exam_id': 5, 'course_id': 7, 'submitted': false,
          'questions': [
            {'id': 1, 'type': 'choice', 'stem': '题面A', 'options': ['选项0', '选项1']},
          ],
        };
      }
      return {};
    });
    await _pumpScreen(tester);
    expect(find.text('题面A'), findsOneWidget);
    expect(find.text('选项0'), findsOneWidget);
    expect(find.text('选项1'), findsOneWidget);
    expect(find.text('提交全部'), findsOneWidget);
  });

  testWidgets('点开始考试 → 组卷成功显示卷子', (tester) async {
    var started = false;
    const utf8 = {'content-type': 'application/json; charset=utf-8'};
    ApiService.bindTestClient(MockClient((request) async {
      final path = request.url.path;
      if (path.endsWith('/exam/status')) {
        return http.Response(jsonEncode({'available': true}), 200, headers: utf8);
      }
      if (path.endsWith('/exam')) {
        return http.Response(jsonEncode({'status': 'none'}), 200, headers: utf8);
      }
      if (path.endsWith('/exam/start')) {
        started = true;
        return http.Response(jsonEncode({
          'exam_id': 9, 'course_id': 7,
          'questions': [
            {'id': 1, 'type': 'choice', 'stem': '新卷题', 'options': ['a', 'b']},
          ],
        }), 200, headers: utf8);
      }
      return http.Response('', 500);
    }));
    await _pumpScreen(tester);
    await tester.tap(find.text('开始考试'));
    await tester.pumpAndSettle();
    expect(started, isTrue);
    expect(find.text('新卷题'), findsOneWidget);
  });

  testWidgets('选答案 → 交卷 → 显示得分报告', (tester) async {
    const utf8 = {'content-type': 'application/json; charset=utf-8'};
    ApiService.bindTestClient(MockClient((request) async {
      final path = request.url.path;
      if (path.endsWith('/exam/status')) {
        return http.Response(jsonEncode({'available': true}), 200, headers: utf8);
      }
      if (path.endsWith('/exam')) {
        return http.Response(jsonEncode({
          'exam_id': 5, 'course_id': 7, 'submitted': false,
          'questions': [
            {'id': 1, 'type': 'choice', 'stem': '判分题', 'options': ['错', '对']},
          ],
        }), 200, headers: utf8);
      }
      if (path.endsWith('/submit')) {
        return http.Response(jsonEncode({
          'exam_id': 5, 'score': 1.0,
          'results': [
            {'question_id': 1, 'correct': true, 'correct_index': 1, 'source': 'pool', 'explanation': '解析文'},
          ],
        }), 200, headers: utf8);
      }
      return http.Response('', 500);
    }));
    await _pumpScreen(tester);
    // 选索引 1("对",正确)。
    await tester.tap(find.text('对'));
    await tester.pump();
    // 交卷。
    await tester.tap(find.text('提交全部'));
    await tester.pumpAndSettle();
    // 得分报告:100 分。
    expect(find.text('100 分'), findsOneWidget);
    expect(find.text('完成'), findsOneWidget);
  });

  testWidgets('加载状态失败 → 显示错误态 + 重试', (tester) async {
    ApiService.bindTestClient(MockClient((request) async {
      throw http.ClientException('network down');
    }));
    await _pumpScreen(tester);
    expect(find.text('加载考试状态失败'), findsOneWidget);
    expect(find.text('重试加载'), findsOneWidget);
  });
}
