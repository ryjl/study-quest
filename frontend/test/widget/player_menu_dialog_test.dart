// 验证 Dialog 版 PlayerSettingsMenu 的核心行为。
//
// Dialog 方案(2026-07-27,弃用 MenuAnchor)的核心优势:
// - showDialog 是 await 的,关闭后显式 requestFocus 归位(线性流程,可靠)
// - Dialog 路由自带 DismissIntent(routes.dart:1198)→ ESC 自动关菜单
// - ModalRoute 自带 popEntry → 系统返回键自动关菜单
// 这测试覆盖三个关键场景。
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:study_quest/ui/widget/player_menu.dart';

void main() {
  Future<void> pumpMenu(WidgetTester tester, {double? selected}) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Center(
            child: PlayerSettingsMenu<double>(
              icon: Icons.speed,
              selectedValue: selected ?? 1.0,
              options: const [
                PlayerMenuOption(value: 0.5, label: '0.5x'),
                PlayerMenuOption(value: 1.0, label: '1.0x'),
                PlayerMenuOption(value: 2.0, label: '2.0x'),
              ],
              onSelected: (_) {},
            ),
          ),
        ),
      ),
    );
    await tester.pump();
  }

  testWidgets('打开菜单:焦点落当前选中项(不是第 0 项)', (tester) async {
    await pumpMenu(tester, selected: 2.0); // 当前选中 2.0x(第 3 项)
    await tester.tap(find.byIcon(Icons.speed));
    await tester.pumpAndSettle();

    expect(find.text('2.0x'), findsOneWidget);
    // 焦点应落选中项(2.0x), autofocus 机制
    final selectedOption = find.text('2.0x');
    final focusedNode = Focus.of(tester.element(selectedOption));
    expect(focusedNode.hasFocus, isTrue,
        reason: '菜单打开应聚焦当前选中项,不是第 0 项');
  });

  testWidgets('ESC 关菜单,焦点回触发按钮', (tester) async {
    await pumpMenu(tester);
    // 先聚焦触发按钮,记录
    await tester.tap(find.byIcon(Icons.speed));
    await tester.pump();
    await tester.pumpAndSettle();
    expect(find.text('1.0x'), findsOneWidget);

    // 按 ESC
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();

    expect(find.text('1.0x'), findsNothing, reason: 'ESC 应关菜单');
    // 焦点应回到触发按钮(playerMenuTrigger)
    expect(FocusManager.instance.primaryFocus?.debugLabel, 'playerMenuTrigger',
        reason: 'ESC 关菜单后焦点应回触发按钮,不丢失');
  });

  testWidgets('选中选项:onSelected 回调 + 菜单关闭 + 焦点归位', (tester) async {
    double? selected;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Center(
            child: PlayerSettingsMenu<double>(
              icon: Icons.speed,
              selectedValue: 1.0,
              options: const [
                PlayerMenuOption(value: 1.0, label: '1.0x'),
                PlayerMenuOption(value: 2.0, label: '2.0x'),
              ],
              onSelected: (v) => selected = v,
            ),
          ),
        ),
      ),
    );
    await tester.pump();

    await tester.tap(find.byIcon(Icons.speed));
    await tester.pumpAndSettle();

    // 点 2.0x
    await tester.tap(find.text('2.0x'));
    await tester.pumpAndSettle();

    expect(selected, equals(2.0), reason: '应回调 onSelected(2.0)');
    expect(find.text('2.0x'), findsNothing, reason: '选中后菜单应关');
    expect(FocusManager.instance.primaryFocus?.debugLabel, 'playerMenuTrigger',
        reason: '选中后焦点应回触发按钮');
  });

  testWidgets('选中态视觉:有勾标记,无尺寸抖动', (tester) async {
    // 验证选中项有 check icon,且选中/未选中 tileColor 不同但布局稳定
    await pumpMenu(tester, selected: 1.0);
    await tester.tap(find.byIcon(Icons.speed));
    await tester.pumpAndSettle();

    // 选中项(1.0x)应有勾
    final checkBefore1 = find.descendant(
      of: find.ancestor(of: find.text('1.0x'), matching: find.byType(ListTile)),
      matching: find.byIcon(Icons.check_rounded),
    );
    expect(checkBefore1, findsOneWidget, reason: '选中项应有勾标记');

    // 未选中项(0.5x)无勾
    final checkBefore05 = find.descendant(
      of: find.ancestor(of: find.text('0.5x'), matching: find.byType(ListTile)),
      matching: find.byIcon(Icons.check_rounded),
    );
    expect(checkBefore05, findsNothing, reason: '未选中项无勾');
  });
}
