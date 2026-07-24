import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:study_quest/service/api_service.dart';
import 'package:study_quest/ui/screen/wrong_book_screen.dart';

// 错题本屏幕级 widget 测试。WrongBookScreen 不依赖 provider,只调 ApiService 静态方法,
// harness 用 MaterialApp(home: Scaffold(body:)) + bindTestClient(MockClient)。
// mock 按 URL 路由(/wrong-book 列表 + /courses 课程过滤)。
//
// 注意:含中文的响应体必须带 utf-8 content-type,否则 http.Response 默认 latin1
// 编码抛 "Contains invalid characters"(对齐 exam_screen_test.dart 范式)。

void main() {
  setUp(() {
    ApiService.authToken = 'test-token';
    ApiService.onUnauthorized = null;
    ApiService.resetTestClient();
  });
  tearDown(() {
    ApiService.resetTestClient();
  });

  const utf8 = {'content-type': 'application/json; charset=utf-8'};

  // 错题本列表响应(items + unmastered_count)。
  http.Response itemsResponse(List<Map<String, dynamic>> items, {int unmastered = 0}) {
    return http.Response(jsonEncode({'items': items, 'unmastered_count': unmastered}), 200, headers: utf8);
  }

  Map<String, dynamic> item({
    required int id,
    required String stem,
    bool mastered = false,
    int attemptCount = 1,
    int streak = 0,
    String type = 'choice',
    int? correctIndex,
    List<String> options = const [],
    String explanation = '',
  }) {
    return {
      'question_id': id, 'stem': stem, 'type': type, 'options': options,
      'explanation': explanation, 'has_jump': false, 'chunk_id': 0, 'course_id': 0,
      'episode_id': 0, 'subject_id': 0, 'first_wrong_at': '',
      'last_attempted_at': '', 'attempt_count': attemptCount,
      'correct_streak': streak, 'mastered': mastered,
      if (correctIndex != null) 'correct_index': correctIndex,
    };
  }

  // 路由 mock:/courses → 空列表(课程过滤 chip 不出现);/wrong-book → items。
  void bindMock(http.Response Function(String path) responder) {
    ApiService.bindTestClient(MockClient((request) async {
      final path = request.url.path;
      if (path.contains('/courses') && !path.contains('grade-tags')) {
        return http.Response(jsonEncode([]), 200, headers: utf8);
      }
      return responder(path);
    }));
  }

  Future<void> _pumpScreen(WidgetTester tester, {int activeUserId = 1}) async {
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(body: WrongBookScreen(activeUserId: activeUserId)),
    ));
    await tester.pumpAndSettle();
  }

  testWidgets('renders items in list with total count', (tester) async {
    bindMock((_) => itemsResponse([item(id: 10, stem: 'stem-A'), item(id: 20, stem: 'stem-B')]));
    await _pumpScreen(tester);
    expect(find.text('stem-A'), findsOneWidget);
    expect(find.text('共 2 题'), findsOneWidget);
  });

  testWidgets('empty state guides to learning', (tester) async {
    bindMock((_) => itemsResponse(const []));
    await _pumpScreen(tester);
    expect(find.text('还没有错题'), findsOneWidget);
    expect(find.textContaining('学习大厅'), findsOneWidget);
  });

  testWidgets('shows error state when API fails', (tester) async {
    ApiService.bindTestClient(MockClient((request) async {
      throw http.ClientException('network down');
    }));
    await _pumpScreen(tester);
    expect(find.text('加载错题本失败'), findsOneWidget);
  });

  testWidgets('answer is collapsed by default; tap 「查看答案」 reveals correct option + explanation',
      (tester) async {
    bindMock((_) => itemsResponse([
          item(id: 1, stem: 'q1', options: ['错', '对'], correctIndex: 1, explanation: '解析文本'),
        ]));
    await _pumpScreen(tester);
    // 默认收起:不显示「正确答案」标签,也不显示解析。
    expect(find.text('正确答案'), findsNothing);
    expect(find.text('解析'), findsNothing);
    // 点「查看答案」展开。
    await tester.tap(find.text('查看答案'));
    await tester.pumpAndSettle();
    expect(find.text('正确答案'), findsOneWidget);
    expect(find.text('解析'), findsOneWidget);
  });

  testWidgets('mastered item shows ✓ badge, unmastered shows attempt count + streak', (tester) async {
    bindMock((_) => itemsResponse([
          item(id: 1, stem: 'mastered-q', mastered: true),
          item(id: 2, stem: 'wrong-q', attemptCount: 3, streak: 2),
        ]));
    await _pumpScreen(tester);
    // 已掌握 badge。
    expect(find.text('✓ 已掌握'), findsOneWidget);
    // 滚到底部看未掌握卡的 "错 3 次" + "连对 2 次"。
    await tester.scrollUntilVisible(find.text('错 3 次'), 200);
    await tester.pumpAndSettle();
    expect(find.text('错 3 次'), findsOneWidget);
    expect(find.text('连对 2 次'), findsOneWidget);
  });

  testWidgets('default filter is 全部 (shows 全部 chip selected with items)', (tester) async {
    // 有数据时才渲染过滤 chip(空状态走引导分支)。默认 _masteredFilter=null=全部。
    bindMock((_) => itemsResponse([item(id: 1, stem: 'q')]));
    await _pumpScreen(tester);
    expect(find.text('全部'), findsOneWidget);
    expect(find.text('未掌握'), findsOneWidget);
    expect(find.text('已掌握'), findsOneWidget);
  });

  testWidgets('batch redo button fetches redo batch; empty batch → snackbar', (tester) async {
    bindMock((path) {
      if (path.contains('/wrong-book/redo')) {
        return http.Response(jsonEncode({'questions': const []}), 200, headers: utf8);
      }
      return itemsResponse([item(id: 1, stem: 'q')]);
    });
    await _pumpScreen(tester);
    expect(find.text('重做一批'), findsOneWidget);
    await tester.tap(find.text('重做一批'));
    await tester.pumpAndSettle();
    expect(find.text('暂无可重做的错题'), findsOneWidget);
  });

  testWidgets('single redo button per card navigates to redo screen', (tester) async {
    bindMock((_) => itemsResponse([item(id: 1, stem: 'q', options: ['a', 'b'], correctIndex: 1)]));
    await _pumpScreen(tester);
    // 重做本题按钮可见可点(确保滚到可见再点)。
    await tester.scrollUntilVisible(find.text('重做本题'), 100);
    await tester.pumpAndSettle();
    await tester.tap(find.text('重做本题'));
    await tester.pumpAndSettle();
    // 进了重做屏:AppBar 标题 + 题面。
    expect(find.text('重做错题'), findsOneWidget);
    expect(find.text('提交全部'), findsOneWidget);
  });
}
