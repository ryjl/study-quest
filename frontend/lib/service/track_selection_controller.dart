import 'package:media_kit/media_kit.dart';

import '../model/course.dart';
import 'api_service.dart';

/// Pure track-selection logic for the player.
///
/// Extracted from `PlayerScreenState` so the dedup/labeling rules for subtitle
/// and audio options live in one place. The State still owns `_applyXxxOption`
/// because those mutate `_selectedSubtitle` and call `setState` — keeping the
/// glue in the widget avoids plumbing a callback for a 1-line mutation.
class TrackSelectionController {
  TrackSelectionController._();

  /// Builds the subtitle option list shown in the inline subtitle menu.
  ///
  ///修复字幕按钮重复 bug(需求 #4):native(libmpv 内置)轨和 backend 轨可能
  /// 同名(都叫「中文」),直接拼接会让菜单里出现两个「中文」按钮。用 seenLabels
  /// 在最终列表的 label 层做去重:同一个 label 只保留一个来源,优先保留 native
  /// (切换更可靠,直接走 libmpv 的内置轨),backend 的同名 label 加「(校对版)」
  /// 后缀保留为可选项。「关闭字幕」label 唯一,自然不会被去重掉。
  static List<Map<String, dynamic>> subtitleOptions({
    required Player player,
    required Set<String> nativeSubtitleIds,
    required List<EpisodeSubtitle> backendSubtitles,
  }) {
    final list = <Map<String, dynamic>>[];
    final seenLabels = <String>{};

    list.add({'label': '关闭字幕', 'type': 'off'});
    seenLabels.add('关闭字幕');

    final cleanNativeSubs = player.state.tracks.subtitle
        .where((t) => nativeSubtitleIds.contains(t.id))
        .toList();
    for (var track in cleanNativeSubs) {
      final label = track.title ?? track.language ?? '内置字幕 ${track.id}';
      if (!seenLabels.contains(label)) {
        seenLabels.add(label);
        list.add({
          'label': label,
          'type': 'native',
          'track': track,
        });
      }
    }

    // Backend 字幕(Whisper 转录/校对版)。和 native 重名时不直接跳过 —— backend
    // 版本可能是经过术语纠错的优质翻译,直接去重会让用户选不到。改成给重名的加
    // 「(校对版)」后缀,既避免菜单出现两个一模一样的「中文」,又保留可选项。
    // 不重名的正常加入。
    for (var sub in backendSubtitles) {
      final label = seenLabels.contains(sub.label)
          ? '${sub.label}(校对版)'
          : sub.label;
      // 后缀后的 label 理论上仍可能撞名(极端情况:已有 native 叫「中文(校对版)」),
      // 用 while 兜底直到不重名。正常场景一次就够。
      var finalLabel = label;
      var n = 2;
      while (seenLabels.contains(finalLabel)) {
        finalLabel = '${sub.label}(校对版$n)';
        n++;
      }
      seenLabels.add(finalLabel);
      list.add({
        'label': finalLabel,
        'type': 'backend',
        'track': sub,
      });
    }
    return list;
  }

  /// Resolves the streaming URL for a backend subtitle option.
  static String backendSubtitleUrl(EpisodeSubtitle sub) =>
      ApiService.absoluteUrl(sub.url);

  /// Builds the audio option list shown in the inline audio menu.
  static List<Map<String, dynamic>> audioOptions(Player player) {
    final list = <Map<String, dynamic>>[];
    final cleanAudioTracks = player.state.tracks.audio
        .where((t) => t.id != 'no' && t.id != 'auto')
        .toList();
    for (var track in cleanAudioTracks) {
      final label = track.title ?? track.language ?? '音轨 ${track.id}';
      list.add({
        'label': label,
        'track': track,
      });
    }
    return list;
  }
}
