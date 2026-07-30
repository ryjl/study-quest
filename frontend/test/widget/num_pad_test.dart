import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:study_quest/ui/widget/num_pad.dart';

// Verifies the externally-controllable clear() the login screen relies on
// after a wrong PIN (auto-submit fills maxDigits; without clear the buffer
// stays full and the user can't retype). Exercises the real NumPad widget so
// the GlobalKey<State> wiring matches production usage.
void main() {
  testWidgets('clear() empties the buffer after digits were entered',
      (tester) async {
    final key = GlobalKey<NumPadState>();
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(body: NumPad(key: key, maxDigits: 6, onSubmit: (_) {}, onCancel: () {})),
    ));
    await tester.pumpAndSettle();

    expect(key.currentState!.isClear, isTrue, reason: 'fresh pad should be clear');

    // Enter a few digits via the real keys.
    for (final d in '123'.split('')) {
      await tester.tap(find.text(d).last);
      await tester.pump();
    }
    expect(key.currentState!.isClear, isFalse, reason: '3 digits should not be clear');

    // The login screen calls this on a failed attempt.
    key.currentState!.clear();
    await tester.pump();
    expect(key.currentState!.isClear, isTrue, reason: 'clear() must empty the buffer');
  });

  testWidgets('submit() fires onSubmit only when maxDigits reached',
      (tester) async {
    int submitted = 0;
    String? submittedPin;
    final key = GlobalKey<NumPadState>();
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(body: NumPad(
        key: key,
        maxDigits: 6,
        onSubmit: (p) { submitted++; submittedPin = p; },
        onCancel: () {},
      )),
    ));
    await tester.pumpAndSettle();

    // Short pin does NOT auto-submit (matches the onKeyPress length check).
    key.currentState!.submit('123');
    await tester.pump();
    expect(submitted, 0);

    // Full pin auto-submits the buffered value.
    key.currentState!.submit('123456');
    await tester.pump();
    expect(submitted, 1);
    expect(submittedPin, '123456');
  });
}
