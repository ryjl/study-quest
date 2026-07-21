import 'package:flutter/material.dart';

import '../../theme.dart';

/// A [SliderTrackShape] that paints a three-segment seek bar:
///   played (activeColor) | buffered (bufferedColor) | unbuffered (trackColor).
///
/// Standard Material Slider only shows played vs. unplayed; for a video player
/// we also want to show how far ahead the demuxer has buffered, so the user
/// knows whether a seek target is ready to play instantly.
class BufferedSeekBarTrackShape extends RoundedRectSliderTrackShape {
  BufferedSeekBarTrackShape({
    required this.bufferedFraction,
    required this.bufferedColor,
  });

  /// Fraction of total duration already buffered, in [0, 1].
  final double bufferedFraction;
  final Color bufferedColor;

  @override
  void paint(
    PaintingContext context,
    Offset offset, {
    required RenderBox parentBox,
    required SliderThemeData sliderTheme,
    required Animation<double> enableAnimation,
    required TextDirection textDirection,
    required Offset thumbCenter,
    Offset? secondaryOffset,
    bool isDiscrete = false,
    bool isEnabled = false,
    double additionalActiveTrackHeight = 0,
  }) {
    if (sliderTheme.trackHeight == null) return;
    final trackHeight = sliderTheme.trackHeight!;
    final radius = Radius.circular(trackHeight / 2);

    // Compute the track rect manually. The Slider leaves horizontal padding
    // for the thumb; we mirror the default Material layout (thumb radius
    // ≈ trackHeight to keep things simple).
    final thumbGap = trackHeight;
    final trackLeft = offset.dx + thumbGap;
    final trackRight = offset.dx + parentBox.size.width - thumbGap;
    final trackTop = offset.dy + (parentBox.size.height - trackHeight) / 2;
    final trackRect = Rect.fromLTRB(
        trackLeft, trackTop, trackRight, trackTop + trackHeight);

    // Layer 1 (bottom): full base track = unbuffered segment.
    context.canvas.drawRRect(
      RRect.fromRectAndCorners(
        Rect.fromLTRB(
            trackRect.left, trackRect.top, trackRect.right, trackRect.bottom),
        topLeft: radius,
        topRight: radius,
        bottomLeft: radius,
        bottomRight: radius,
      ),
      Paint()..color = sliderTheme.inactiveTrackColor ?? Colors.white12,
    );

    // Layer 2: buffered segment [0 .. bufferedFraction].
    final bufferEnd =
        trackRect.left + trackRect.width * bufferedFraction.clamp(0.0, 1.0);
    if (bufferEnd > trackRect.left) {
      context.canvas.drawRRect(
        RRect.fromRectAndCorners(
          Rect.fromLTRB(
              trackRect.left, trackRect.top, bufferEnd, trackRect.bottom),
          topLeft: radius,
          topRight: radius,
          bottomLeft: radius,
          bottomRight: radius,
        ),
        Paint()..color = bufferedColor,
      );
    }

    // Layer 3 (top): played segment [0 .. thumbCenter] in active color.
    context.canvas.drawRRect(
      RRect.fromRectAndCorners(
        Rect.fromLTRB(
            trackRect.left, trackRect.top, thumbCenter.dx, trackRect.bottom),
        topLeft: radius,
        topRight: radius,
        bottomLeft: radius,
        bottomRight: radius,
      ),
      Paint()..color = sliderTheme.activeTrackColor ?? AppTheme.primaryColor,
    );
  }
}
