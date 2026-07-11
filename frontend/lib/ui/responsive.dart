import 'package:flutter/material.dart';

/// Responsive layout helpers driven by orientation, not raw width.
///
/// The app's two layouts map to orientation rather than a pixel breakpoint:
///   - **Landscape** (width ≥ height): the tablet layout — 280px sidebar,
///     multi-column rows, generous paddings. Tablets are landscape by default
///     and their landscape width is always ample.
///   - **Portrait** (width < height): the mobile layout — bottom nav bar,
///     stacked rows, reduced paddings. This fires on a tablet rotated to
///     portrait (whose width, e.g. ~768–900dp, would *not* trip a <600
///     handset breakpoint), as well as on handsets.
///
/// Orientation is used instead of a fixed width threshold because the target
/// is real tablets: a tablet in portrait still has a large logical width that
/// exceeds Material's 600dp compact breakpoint, so width-based switching would
/// never activate. The user's intent ("rotate to portrait → give me the mobile
/// layout") is captured directly by the aspect ratio.

/// True when the screen is in portrait (width < height). Triggers the mobile
/// layout: bottom nav, stacked columns, compact paddings.
bool isPortrait(BuildContext context) {
  final size = MediaQuery.sizeOf(context);
  return size.width < size.height;
}

/// Edge insets that shrink in portrait. Use for page-level content paddings
/// that are generous in landscape but must not crowd a portrait screen.
EdgeInsets portraitAwarePadding(BuildContext context,
    {double landscape = 40, double portrait = 16}) {
  return EdgeInsets.all(isPortrait(context) ? portrait : landscape);
}
