import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:study_quest/ui/widget/bookshelf.dart';

void main() {
  group('BookCoverTile', () {
    // Shared gradient used by all tiles in tests.
    const gradient = LinearGradient(
      colors: [Color(0xFF60A5FA), Color(0xFF3B82F6)],
      begin: Alignment.topLeft,
      end: Alignment.bottomRight,
    );

    testWidgets('renders title and subtitle', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: BookCoverTile(
              coverUrl: '',
              subjectKey: 'math',
              subjectColor: '#3B82F6',
              gradient: gradient,
              badgeIcon: Icons.book,
              title: '我的书',
              subtitle: '作者',
            ),
          ),
        ),
      );

      expect(find.text('我的书'), findsOneWidget);
      expect(find.text('作者'), findsOneWidget);
      expect(find.byIcon(Icons.book), findsOneWidget);
    });

    testWidgets('renders gradient fallback when coverUrl empty', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: BookCoverTile(
              coverUrl: '',
              subjectKey: 'math',
              subjectColor: '#3B82F6',
              gradient: gradient,
              badgeIcon: Icons.book,
              title: '无封面',
              subtitle: '',
            ),
          ),
        ),
      );

      // Cover area is 130×180. Gradient fallback path goes through
      // gradientCover() which builds a Container with a BoxDecoration(gradient).
      // Verify NO Image.network is in the tree (since coverUrl is empty).
      expect(find.byType(Image), findsNothing);
      // The fallback Container decoration carries the gradient.
      final containers = tester
          .widgetList<Container>(find.byType(Container))
          .where((c) =>
              c.decoration is BoxDecoration &&
              (c.decoration as BoxDecoration).gradient == gradient)
          .toList();
      expect(containers.length, greaterThanOrEqualTo(1));
    });

    testWidgets('invokes onTap when tapped', (tester) async {
      var taps = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: BookCoverTile(
              coverUrl: '',
              subjectKey: 'math',
              subjectColor: '#3B82F6',
              gradient: gradient,
              badgeIcon: Icons.book,
              title: '可点击',
              subtitle: '',
              onTap: () => taps++,
            ),
          ),
        ),
      );

      await tester.tap(find.byType(BookCoverTile));
      await tester.pump();
      expect(taps, 1);
    });
  });

  group('sectionTitle', () {
    testWidgets('renders the text with w900 weight', (tester) async {
      await tester.pumpWidget(
        MaterialApp(home: Scaffold(body: Builder(builder: (ctx) => sectionTitle(ctx, 'Hello')))),
      );
      final text = tester.widget<Text>(find.text('Hello'));
      expect(text.style?.fontWeight, FontWeight.w900);
    });
  });

  group('buildShelfBoard', () {
    testWidgets('lays out children in a Wrap', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Builder(builder: (ctx) => buildShelfBoard(
              ctx,
              children: const [SizedBox(width: 50, height: 50)],
            )),
          ),
        ),
      );
      expect(find.byType(Wrap), findsOneWidget);
    });
  });
}
