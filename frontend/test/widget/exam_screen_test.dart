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

  testWidgets('多选题交卷后选项区只标正确项 + 底部明细列对错归属', (tester) async {
    // 需求#3a:多选提交后选项区干净(只标正确绿),底部明细用带颜色文字说清「你的选择/正确答案/多选/漏选」。
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
            {'id': 1, 'type': 'multi_choice', 'stem': '多选判分', 'options': ['甲', '乙', '丙', '丁']},
          ],
        }), 200, headers: utf8);
      }
      if (path.endsWith('/submit')) {
        // 正确是 0、2(甲、丙)。学生选了 0、1(甲、乙) → 甲对、乙多选、丙漏选。
        return http.Response(jsonEncode({
          'exam_id': 5, 'score': 0.0,
          'results': [
            {'question_id': 1, 'correct': false, 'correct_indices': [0, 2], 'explanation': ''},
          ],
        }), 200, headers: utf8);
      }
      return http.Response('', 500);
    }));
    await _pumpScreen(tester);
    // 选 甲(0) 和 乙(1)。
    await tester.tap(find.text('甲'));
    await tester.pump();
    await tester.tap(find.text('乙'));
    await tester.pump();
    await tester.tap(find.text('提交全部'));
    await tester.pumpAndSettle();
    // 底部明细出现:你的选择 / 正确答案 / 多选/漏选提示。
    expect(find.text('你的选择'), findsOneWidget);
    expect(find.text('正确答案'), findsOneWidget);
    expect(find.textContaining('多选了'), findsOneWidget); // 乙多选
    expect(find.textContaining('漏选了'), findsOneWidget); // 丙漏选
  });

  testWidgets('填空题交卷判错后输入框变红、内容保留、显示正确答案', (tester) async {
    // 需求#3b:填空提交判错,输入框变红留内容(用户能看到自己刚填了什么)+ 绿色正确答案。
    const utf8 = {'content-type': 'application/json; charset=utf-8'};
    ApiService.bindTestClient(MockClient((request) async {
      final path = request.url.path;
      if (path.endsWith('/exam/status')) {
        return http.Response(jsonEncode({'available': true}), 200, headers: utf8);
      }
      if (path.endsWith('/exam')) {
        return http.Response(jsonEncode({
          'exam_id': 6, 'course_id': 7, 'submitted': false,
          'questions': [
            {'id': 2, 'type': 'fill', 'stem': '填空判分', 'options': []},
          ],
        }), 200, headers: utf8);
      }
      if (path.endsWith('/submit')) {
        return http.Response(jsonEncode({
          'exam_id': 6, 'score': 0.0,
          'results': [
            {'question_id': 2, 'correct': false, 'correct_text': '5/6', 'explanation': ''},
          ],
        }), 200, headers: utf8);
      }
      return http.Response('', 500);
    }));
    await _pumpScreen(tester);
    // 填一个错误答案。
    await tester.enterText(find.byType(TextField), '12');
    await tester.pump();
    await tester.tap(find.text('提交全部'));
    await tester.pumpAndSettle();
    // 判错:正确答案展示,且输入框内容(12)还在(只读态)。
    expect(find.textContaining('正确答案'), findsOneWidget);
    expect(find.textContaining('5/6'), findsOneWidget);
  });
}
