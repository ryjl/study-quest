import 'package:flutter/material.dart';
import '../../model/subject.dart';
import '../../theme.dart';
import 'button_3d.dart';

/// Shared loading / error / empty state widgets used across the course-list
/// and course-detail screens. Extracted from per-screen `_buildErrorBox` /
/// `_buildEmptyBox` methods that were near-identical copies.
///
/// The login screen's error/empty boxes are intentionally NOT consolidated
/// here — they use GlassPanel + FocusButton and a two-button layout, so
/// forcing them through this API would add parameters rather than remove code.

/// The standard loading spinner shown while a FutureBuilder is awaiting.
/// Wraps a primary-colored CircularProgressIndicator in a Center.
Widget loadingSpinner() {
  return const Center(
    child: CircularProgressIndicator(color: AppTheme.primaryColor),
  );
}

/// A centered error state with an icon, message, and a retry button.
///
/// [onRetry] is the refresh callback (e.g. the screen's `_loadData`).
/// [message] is the headline (defaults to a generic failure line; callers pass
/// a screen-specific one when appropriate).
Widget errorStateBox(String error, VoidCallback onRetry, {String message = '加载失败，请重试！'}) {
  return Center(
    child: Column(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        const Icon(Icons.error_outline, size: 48, color: Colors.redAccent),
        const SizedBox(height: 16),
        Text(message, style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 16)),
        const SizedBox(height: 8),
        Text(error, style: const TextStyle(color: AppTheme.textMuted), textAlign: TextAlign.center),
        const SizedBox(height: 24),
        Button3D.blue(onPressed: onRetry, child: const Text('重试加载')),
      ],
    ),
  );
}

/// A centered empty state with an icon, headline, hint, and optional refresh.
Widget emptyStateBox({
  required IconData icon,
  required String headline,
  required String hint,
  String refreshLabel = '刷新',
  VoidCallback? onRefresh,
}) {
  return Center(
    child: Column(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        Icon(icon, size: 56, color: AppTheme.textMuted),
        const SizedBox(height: 16),
        Text(headline, style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 16)),
        const SizedBox(height: 8),
        Text(hint, style: const TextStyle(fontSize: 13, color: AppTheme.textMuted), textAlign: TextAlign.center),
        if (onRefresh != null) ...[
          const SizedBox(height: 24),
          Button3D.blue(onPressed: onRefresh, child: Text(refreshLabel)),
        ],
      ],
    ),
  );
}

/// resolveSubject looks up a subject by key in the catalog, falling back to a
/// placeholder Subject (📦 / grey) when the key is missing or the catalog hasn't
/// loaded. Extracted from the per-screen `_subjectMeta` copies which had the
/// same lookup loop but divergent fallback construction (one used copyWith, one
/// built a fresh literal — both produced the same values).
Subject resolveSubject(String key, List<Subject> catalog) {
  for (final s in catalog) {
    if (s.key == key) return s;
  }
  // Subject's defaults (emoji '📦', color '#9ca3af') match the old fallbacks.
  return Subject(key: key, label: key.isEmpty ? '科目' : key);
}
