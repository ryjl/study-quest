import 'package:study_quest/model/course.dart';

/// Display-only grouping of episodes under a chapter title.
///
/// Pulled out of `course_detail_screen.dart` so the grouping logic is unit-
/// testable and reusable. The [isUngrouped] flag marks the synthetic
/// "其他课时"/"全部课时" bucket for episodes whose [Episode.chapterId] is
/// missing or points at a chapter that isn't in the catalog.
class GroupedChapter {
  final String title;
  final List<Episode> episodes;
  final bool isUngrouped;

  const GroupedChapter({
    required this.title,
    required this.episodes,
    required this.isUngrouped,
  });
}

/// Groups [episodes] under their [chapters] in display order.
///
/// Real chapters come first (in [Chapter.sortOrder] then [Chapter.id] order),
/// each populated with the episodes whose [Episode.chapterId] matches. Any
/// episodes left over (chapterId == 0 or pointing at a chapter not in the
/// list) are collected into a trailing bucket whose title is "其他课时" when
/// at least one chapter exists, or "全部课时" when there are none.
///
/// If both lists are empty the result is an empty list. If there are episodes
/// but no chapters (and no leftovers — impossible but defensive), a single
/// "全部课时" group is returned as a fallback.
List<GroupedChapter> groupEpisodesByChapter(
  List<Episode> episodes,
  List<Chapter> chapters,
) {
  final sortedChapters = [...chapters]
    ..sort((a, b) {
      final c = a.sortOrder.compareTo(b.sortOrder);
      return c != 0 ? c : a.id.compareTo(b.id);
    });

  final byChapter = <int, List<Episode>>{};
  final ungrouped = <Episode>[];
  for (final ep in episodes) {
    if (ep.chapterId > 0 && sortedChapters.any((c) => c.id == ep.chapterId)) {
      byChapter.putIfAbsent(ep.chapterId, () => []).add(ep);
    } else {
      ungrouped.add(ep);
    }
  }

  final groups = <GroupedChapter>[];
  for (final ch in sortedChapters) {
    final list = byChapter[ch.id];
    if (list != null && list.isNotEmpty) {
      groups.add(GroupedChapter(title: ch.title, episodes: list, isUngrouped: false));
    }
  }
  if (ungrouped.isNotEmpty) {
    groups.add(GroupedChapter(
      title: sortedChapters.isEmpty ? '全部课时' : '其他课时',
      episodes: ungrouped,
      isUngrouped: true,
    ));
  }
  // If there are no chapters and no episodes somehow, fall back to a single group.
  if (groups.isEmpty && episodes.isNotEmpty) {
    groups.add(GroupedChapter(title: '全部课时', episodes: episodes, isUngrouped: true));
  }
  return groups;
}
