// 播放器 TV 焦点导航的最小重现用例测试。
//
// 背景:player_screen.dart 的控件区(seek bar + 控件行)最初用
// `if (_controlsVisible)` 守卫 + auto-hide(4 秒)自动卸载。seek bar 的
// _seekBarFocus 是视频区**唯一**的 FocusNode,卸载后视频区零焦点落点,TV 用户
// 焦点在 helper panel 按 ← 想回视频区时,framework 的 focusInDirection 几何
// 算法找不到目标 → 焦点"丢失",再也移不动。
//
// 修复:TV 模式下 _scheduleAutoHide 不调度(控件常驻可见),seek bar 的
// FocusNode 永远在焦点树里,几何算法总能找到 → 跨区导航可靠。PAD/手机保留
// auto-hide(触屏传统交互)。
//
// 这组测试用最小重现用例(Row[左区, 右区])钉死根因 + 验证修复,参照官方
// flutter/packages/flutter/test/widgets/focus_traversal_test.dart:1668 模板。
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('播放器控件隐藏策略的焦点跨区行为', () {
    /// 搭一个最小播放器布局:左区(视频区,seekBar Focus) + 右区(helper panel
    /// 的 panelButton Focus)。`leftVisible` 控制左区是否渲染(模拟 auto-hide
    /// 卸载 vs 常驻)。
    Future<void> pumpPlayer(
      WidgetTester tester, {
      required bool leftVisible,
      required FocusNode seekBarNode,
      required FocusNode panelNode,
    }) async {
      Widget? leftChild;
      if (leftVisible) {
        // 控件可见:seekBar 在焦点树里
        leftChild = Positioned(
          left: 0,
          right: 0,
          bottom: 0,
          child: Focus(
            focusNode: seekBarNode,
            child: const SizedBox(width: 50, height: 40),
          ),
        );
      }
      // 控件隐藏:leftChild 保持 null,模拟 `if (_controlsVisible)` 卸载子树。
      // (不用 if (false) 避免静态分析 dead_code 警告)

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: FocusTraversalGroup(
              child: Row(
                children: [
                  SizedBox(
                    width: 300,
                    height: 200,
                    child: Stack(children: [if (leftChild != null) leftChild]),
                  ),
                  SizedBox(
                    width: 100,
                    height: 200,
                    child: Center(
                      child: Focus(
                        focusNode: panelNode,
                        child: const SizedBox(
                            width: 80, height: 40, child: ColoredBox(color: Colors.blue)),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      );
      await tester.pump();
    }

    testWidgets('控件可见(修复后 TV 常驻):从右区按 ← 能回到左区 seekBar', (tester) async {
      final seekBarNode = FocusNode(debugLabel: 'seekBar');
      final panelNode = FocusNode(debugLabel: 'panelButton');
      addTearDown(() {
        seekBarNode.dispose();
        panelNode.dispose();
      });

      await pumpPlayer(
        tester,
        leftVisible: true, // 控件可见(TV 修复后常驻就是这个状态)
        seekBarNode: seekBarNode,
        panelNode: panelNode,
      );

      panelNode.requestFocus();
      await tester.pump();
      expect(panelNode.hasFocus, isTrue);

      final moved = panelNode.focusInDirection(TraversalDirection.left);
      await tester.pump();
      expect(moved, isTrue, reason: '控件可见时,跨区必须成功');
      expect(seekBarNode.hasFocus, isTrue, reason: '焦点应落到左区 seekBar');
    });

    testWidgets('控件隐藏(旧 auto-hide bug):左区卸载,按 ← 几何跨区失败 —— 根因保护', (tester) async {
      // 这条测试钉死旧 auto-hide 方案的 bug,防止有人把 TV auto-hide 改回来。
      final seekBarNode = FocusNode(debugLabel: 'seekBar');
      final panelNode = FocusNode(debugLabel: 'panelButton');
      addTearDown(() {
        seekBarNode.dispose();
        panelNode.dispose();
      });

      await pumpPlayer(
        tester,
        leftVisible: false, // 控件隐藏(auto-hide 卸载子树)
        seekBarNode: seekBarNode,
        panelNode: panelNode,
      );

      panelNode.requestFocus();
      await tester.pump();
      expect(panelNode.hasFocus, isTrue);

      final moved = panelNode.focusInDirection(TraversalDirection.left);
      await tester.pump();
      expect(moved, isFalse,
          reason: '控件卸载后左区无 FocusNode,几何跨区必然失败 —— auto-hide 焦点丢失根因');
      expect(panelNode.hasFocus, isTrue, reason: '焦点仍卡在右区,移不动');
    });
  });

  group('_onWakeControls 唤出兜底(纯状态机,不依赖几何)', () {
    /// _onWakeControls 的纯逻辑函数版本(从 player_screen 抽出来便于测):
    /// 仅 TV + 控件隐藏态 + (方向键/激活键) → 唤出 + handled;否则 ignored。
    /// 注意:LogicalKeyboardKey 重写了 == / hashCode,不能放 const set
    /// (跟 button_3d.dart 的 isActivationKey 同理),所以用 || 判断。
    KeyEventResult wakeLogic({
      required bool isTv,
      required bool controlsVisible,
      required LogicalKeyboardKey key,
    }) {
      bool isWakeKey(LogicalKeyboardKey k) =>
          k == LogicalKeyboardKey.arrowLeft ||
          k == LogicalKeyboardKey.arrowRight ||
          k == LogicalKeyboardKey.arrowUp ||
          k == LogicalKeyboardKey.arrowDown ||
          k == LogicalKeyboardKey.enter ||
          k == LogicalKeyboardKey.select ||
          k == LogicalKeyboardKey.space;
      if (!isTv) return KeyEventResult.ignored;
      if (controlsVisible) return KeyEventResult.ignored;
      if (isWakeKey(key)) return KeyEventResult.handled;
      return KeyEventResult.ignored;
    }

    testWidgets('TV 隐藏态:方向键/激活键被吞(handled)', (tester) async {
      expect(
          wakeLogic(isTv: true, controlsVisible: false, key: LogicalKeyboardKey.arrowRight),
          KeyEventResult.handled);
      expect(
          wakeLogic(isTv: true, controlsVisible: false, key: LogicalKeyboardKey.arrowLeft),
          KeyEventResult.handled);
      expect(
          wakeLogic(isTv: true, controlsVisible: false, key: LogicalKeyboardKey.enter),
          KeyEventResult.handled);
      expect(
          wakeLogic(isTv: true, controlsVisible: false, key: LogicalKeyboardKey.select),
          KeyEventResult.handled);
      expect(
          wakeLogic(isTv: true, controlsVisible: false, key: LogicalKeyboardKey.space),
          KeyEventResult.handled);
    });

    test('TV 隐藏态:escape/browserBack 透传(交给 DismissIntent 处理)', () {
      // escape 等退出键不在 wakeKeys 里,由顶层 Shortcuts 的 DismissIntent 处理,
      // 不走 _onWakeControls 唤出逻辑。
      expect(
          wakeLogic(isTv: true, controlsVisible: false, key: LogicalKeyboardKey.escape),
          KeyEventResult.ignored);
      expect(
          wakeLogic(isTv: true, controlsVisible: false, key: LogicalKeyboardKey.browserBack),
          KeyEventResult.ignored);
    });

    test('TV 显示态:所有键透传(交给 framework 几何算法)', () {
      expect(
          wakeLogic(isTv: true, controlsVisible: true, key: LogicalKeyboardKey.arrowRight),
          KeyEventResult.ignored);
      expect(
          wakeLogic(isTv: true, controlsVisible: true, key: LogicalKeyboardKey.enter),
          KeyEventResult.ignored);
    });

    test('非 TV:所有键透传(PAD/手机走旧 seek±10s/音量逻辑,由 seek bar onKeyEvent 处理)', () {
      expect(
          wakeLogic(isTv: false, controlsVisible: false, key: LogicalKeyboardKey.arrowRight),
          KeyEventResult.ignored);
      expect(
          wakeLogic(isTv: false, controlsVisible: true, key: LogicalKeyboardKey.enter),
          KeyEventResult.ignored);
    });
  });
}
