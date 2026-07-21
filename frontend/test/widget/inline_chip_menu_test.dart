import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:study_quest/ui/widget/inline_chip_menu.dart';

void main() {
  group('InlineChipMenu', () {
    testWidgets('renders title and each item label', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: InlineChipMenu(
              title: '播放速度：',
              items: [
                InlineChipItem(label: '0.5x', selected: false, onTap: () {}),
                InlineChipItem(label: '1.0x', selected: true, onTap: () {}),
                InlineChipItem(label: '2.0x', selected: false, onTap: () {}),
              ],
            ),
          ),
        ),
      );

      expect(find.text('播放速度：'), findsOneWidget);
      expect(find.text('0.5x'), findsOneWidget);
      expect(find.text('1.0x'), findsOneWidget);
      expect(find.text('2.0x'), findsOneWidget);
    });

    testWidgets('selected chip shows a check icon, unselected does not',
        (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: InlineChipMenu(
              title: '字幕：',
              items: [
                InlineChipItem(
                    label: '关闭',
                    selected: true,
                    onTap: () {}),
                InlineChipItem(
                    label: '中文',
                    selected: false,
                    onTap: () {}),
              ],
            ),
          ),
        ),
      );

      // Exactly one check_rounded icon for the single selected chip.
      expect(find.byIcon(Icons.check_rounded), findsNWidgets(1));
    });

    testWidgets('tapping a chip fires its callback', (tester) async {
      var taps = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: InlineChipMenu(
              title: '音轨：',
              items: [
                InlineChipItem(
                    label: '默认',
                    selected: false,
                    onTap: () => taps++),
              ],
            ),
          ),
        ),
      );

      await tester.tap(find.text('默认'));
      await tester.pump();
      expect(taps, 1);
    });
  });
}
