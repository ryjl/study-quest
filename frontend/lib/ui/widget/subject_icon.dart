import 'package:flutter/material.dart';

/// Resolves a subject key to a Material [IconData] for display. Mirrors the
/// admin SPA's `subjectIcon.tsx` (key → icon).
///
/// Subjects are DB-driven (admin can edit/create them), so we map the known
/// system-seeded keys (see backend `service/subject_service.go` seed list) to a
/// semantically-close Material icon, and fall back to [Icons.book_rounded] for
/// anything custom or unrecognized. Callers tint the icon with the subject's
/// `color` from the DB.
///
/// The DB used to carry a `Subject.emoji` field; that was dropped in favor of
/// deriving the icon from the key on both clients.
///
/// Accepts the key case-insensitively so it stays robust when the same subject
/// is referenced by label ("语文") or alias.
IconData subjectIconData(String key) {
  switch (key.toLowerCase()) {
    case 'math':
    case '数学':
      return Icons.calculate_rounded;
    case 'chinese':
    case '语文':
      return Icons.menu_book_rounded;
    case 'english':
    case '英语':
      return Icons.translate_rounded;
    case 'physics':
    case '物理':
    case '科学':
      return Icons.science_rounded;
    case 'chemistry':
    case '化学':
      return Icons.biotech_rounded;
    case 'biology':
    case '生物':
      return Icons.park_rounded;
    case 'history':
    case '历史':
      return Icons.auto_stories_rounded;
    case 'geography':
    case '地理':
      return Icons.public_rounded;
    case 'politics':
    case '道法':
    case '道德与法治':
      return Icons.balance_rounded;
    case 'extra':
    case '课外':
    case '百科':
      return Icons.explore_rounded;
    case 'entertainment':
    case '娱乐':
      return Icons.movie_rounded;
    // 2026-07-20 新增娱乐子类(配合 Subject.Category=entertainment)。
    case 'animation':
    case '动画':
    case '动画片':
      return Icons.animation_rounded;
    case 'movie':
    case '电影':
      return Icons.movie_filter_rounded;
    case 'documentary':
    case '纪录片':
      return Icons.video_camera_back_rounded;
    case 'variety':
    case '综艺':
      return Icons.live_tv_rounded;
    case 'music':
    case '音乐':
      return Icons.music_note_rounded;
    case 'art':
    case '美术':
      return Icons.palette_rounded;
    case 'pe':
    case 'sport':
    case '体育':
      return Icons.sports_rounded;
    case '兴趣':
      return Icons.extension_rounded;
    case '综合':
      return Icons.map_rounded;
    default:
      return Icons.book_rounded;
  }
}
