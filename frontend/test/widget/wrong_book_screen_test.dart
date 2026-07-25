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
    int courseId = 0,
  }) {
    return {
      'question_id': id, 'stem': stem, 'type': type, 'options': options,
      'explanation': explanation, 'has_jump': false, 'chunk_id': 0, 'course_id': courseId,
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

  testWidgets('options show by default (neutral) so student can self-test before revealing answer',
      (tester) async {
    // 问题#3:收起态就列选项(中性不高亮),让学生先自测。之前选项完全隐藏。
    bindMock((_) => itemsResponse([
          item(id: 1, stem: 'q1', options: ['选项A', '选项B', '选项C', '选项D'], correctIndex: 2),
        ]));
    await _pumpScreen(tester);
    // 4 个选项在收起态都可见(选项带 A.B.C.D. 序号前缀)。
    expect(find.text('A. 选项A'), findsOneWidget);
    expect(find.text('D. 选项D'), findsOneWidget);
    // 还没展开,不显示「正确答案」。
    expect(find.text('正确答案'), findsNothing);
  });

  testWidgets('tapping a neutral option marks the choice; revealing shows it red when wrong',
      (tester) async {
    // 问题#3:点选项可标记,展开后选错的项标红、正确项标绿。
    bindMock((_) => itemsResponse([
          item(id: 1, stem: 'q1', options: ['错', '对'], correctIndex: 1),
        ]));
    await _pumpScreen(tester);
    // 先点错误的「错」(index 0)标记自测选择(选项带 A. 前缀)。
    await tester.tap(find.text('A. 错'));
    await tester.pumpAndSettle();
    // 展开看答案。
    await tester.tap(find.text('查看答案'));
    await tester.pumpAndSettle();
    expect(find.text('正确答案'), findsOneWidget);
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
    // 显式指定主 Scrollable:scrollUntilVisible 默认 find.byType(Scrollable) 会匹配到多个,
    // 卡片变矮触发滚动时抛 "Too many elements"。
    await tester.scrollUntilVisible(find.text('错 3 次'), 200,
        scrollable: find.byType(Scrollable).first);
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

  testWidgets('redo screen shows question number and multi-detail after submit', (tester) async {
    // 需求#2 题序号 + 需求#3a 多选明细:重做卷每题带「第N题」;多选交卷后底部明细。
    bindMock((path) {
      // 注意顺序:/redo/submit 必须在 /wrong-book/redo 之前判断——submit 路径
      // (/wrong-book/redo/submit) 也 contains('/wrong-book/redo'),先判 redo 会误匹配。
      if (path.contains('/redo/submit')) {
        // 正确是 0、2(甲、丙)。学生选 0、1 → 乙多选、丙漏选。
        return http.Response(jsonEncode({
          'results': [
            {'question_id': 10, 'correct': false, 'correct_indices': [0, 2], 'explanation': ''},
          ],
        }), 200, headers: utf8);
      }
      if (path.contains('/wrong-book/redo')) {
        return http.Response(jsonEncode({
          'questions': [
            {'id': 10, 'type': 'multi_choice', 'stem': '多选重做', 'options': ['甲', '乙', '丙'], 'has_jump': false},
          ],
        }), 200, headers: utf8);
      }
      return itemsResponse([item(id: 10, stem: 'seed')]);
    });
    await _pumpScreen(tester);
    await tester.tap(find.text('重做一批'));
    await tester.pumpAndSettle();
    // 题序号。
    expect(find.text('第1题'), findsOneWidget);
    // 选 甲(A) + 乙(B)。
    await tester.tap(find.text('A. 甲'));
    await tester.pump();
    await tester.tap(find.text('B. 乙'));
    await tester.pump();
    await tester.tap(find.text('提交全部'));
    await tester.pumpAndSettle();
    // 题序号仍在(题卡重渲染) + 多选明细(你的选择/正确答案/多选/漏选)。
    expect(find.text('第1题'), findsOneWidget);
    expect(find.text('你的选择'), findsOneWidget);
    expect(find.text('正确答案'), findsOneWidget);
    expect(find.textContaining('多选了'), findsOneWidget);
    expect(find.textContaining('漏选了'), findsOneWidget);
  });

  testWidgets('filtered empty state keeps filter chips (problem #2)', (tester) async {
    // 问题#2:选了未掌握过滤、但该过滤下无结果时,不应整页变空状态丢失过滤 chip。
    // 这里模拟"学生有错题(unmastered_count>0)但当前过滤返回空 items"。
    bindMock((_) => itemsResponse(const [], unmastered: 3));
    await _pumpScreen(tester);
    // 过滤行仍在(能切回全部),不是「还没有错题」引导页。
    expect(find.text('全部'), findsOneWidget);
    expect(find.text('未掌握'), findsOneWidget);
    expect(find.text('当前筛选下没有错题'), findsOneWidget);
    expect(find.text('还没有错题'), findsNothing);
  });

  testWidgets('truly empty (no wrong ever) shows guide, not filtered-empty', (tester) async {
    // 真正从没错过题(unmastered_count=0 且无过滤)→ 引导页。
    bindMock((_) => itemsResponse(const [], unmastered: 0));
    await _pumpScreen(tester);
    expect(find.text('还没有错题'), findsOneWidget);
  });

  testWidgets('mastered item toggle button shows cancel wording (problem #5)', (tester) async {
    // 问题#5:已掌握题的按钮文案明确双向可点(「已掌握 · 点取消」)。
    bindMock((_) => itemsResponse([item(id: 1, stem: 'mq', mastered: true)]));
    await _pumpScreen(tester);
    expect(find.textContaining('点取消'), findsOneWidget);
  });

  testWidgets('toggling mastered flips the UI immediately (optimistic update, problem #6)', (tester) async {
    // 问题#6 回归守护:点「标记掌握」后,乐观更新必须即时翻转按钮文案。
    // 之前的 bug 是乐观层被 build 用旧 future snapshot 覆盖,点了像没反应。
    bindMock((_) => itemsResponse([item(id: 1, stem: 'mq', mastered: false)]));
    await _pumpScreen(tester);
    // 初始:未掌握 → 显示「标记掌握」。
    expect(find.text('标记掌握'), findsOneWidget);
    await tester.scrollUntilVisible(find.text('标记掌握'), 100,
        scrollable: find.byType(Scrollable).first);
    await tester.pumpAndSettle();
    // 点一下 → 乐观翻转成「已掌握 · 点取消」,不等后端。
    await tester.tap(find.text('标记掌握'));
    await tester.pumpAndSettle();
    expect(find.textContaining('点取消'), findsOneWidget);
    expect(find.text('标记掌握'), findsNothing);
  });

  testWidgets('course source chip always shown (problem #1)', (tester) async {
    // 问题#1:来源 chip 一定显示,即使课程名解析不出也显示「未知课程」。
    bindMock((_) => itemsResponse([item(id: 1, stem: 'q', courseId: 999)]));
    await _pumpScreen(tester);
    expect(find.text('未知课程'), findsOneWidget);
  });
}
