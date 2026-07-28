import 'package:flutter/foundation.dart';
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
  /// 单一字幕策略:每语言只展示一个选项。backend 字幕(Whisper 转录 + LLM
  /// polish)优先用原名展示;若 backend 已覆盖某语言/label,native(libmpv 从
  /// 视频容器抽出的内嵌轨)的同名同语言条目直接跳过 —— 否则菜单会冒出「中文」
  /// 和「中文(校对版)」两个按钮,而它们实际是同一条字幕的两份来源,既混乱、
  /// 切换时还会因 native/backend 索引时序错位导致「点了没字幕」。
  ///
  /// 历史:之前为了"保留可选项"给重名 backend 加「(校对版)」后缀,但后端每个
  /// (episode, language) 只存一行 Subtitle,LLM 校对是 in-place 覆盖 VttContent,
  /// 根本不存在"原始/校对"两份数据 —— 所谓"校对版"是前端拼出来的假选项。改为
  /// backend 优先、native 兜底(无 backend 时才展示 native)。
  static List<Map<String, dynamic>> subtitleOptions({
    required Player player,
    required Set<String> nativeSubtitleIds,
    required List<EpisodeSubtitle> backendSubtitles,
  }) {
    // 委托给纯函数,让合并逻辑可单测(不需要 mock Player)。
    final cleanNativeSubs = player.state.tracks.subtitle
        .where((t) => nativeSubtitleIds.contains(t.id))
        .toList();
    return mergeSubtitleOptions(
      backendSubtitles: backendSubtitles,
      nativeSubs: cleanNativeSubs,
    );
  }

  /// 字幕合并的纯逻辑(无 Player 依赖,可单测)。
  ///
  /// 策略:backend 优先用原名,native 若与 backend 同 label 或同 language
  /// 则跳过。详见 [subtitleOptions] 的完整注释。
  @visibleForTesting
  static List<Map<String, dynamic>> mergeSubtitleOptions({
    required List<EpisodeSubtitle> backendSubtitles,
    required List<SubtitleTrack> nativeSubs,
  }) {
    final list = <Map<String, dynamic>>[];
    final seenLabels = <String>{};
    final seenLanguages = <String>{};

    void add(Map<String, dynamic> opt) {
      list.add(opt);
      seenLabels.add(opt['label'] as String);
    }

    add({'label': '无', 'type': 'off'});

    // 1) Backend 字幕优先:Whisper 转录 + LLM polish 后的内容,质量更高。
    //    用原名展示,同时登记 label 和 language,供下面 native 去重。
    for (final sub in backendSubtitles) {
      if (!seenLabels.add(sub.label)) continue;
      seenLanguages.add(sub.language);
      list.add({'label': sub.label, 'type': 'backend', 'track': sub});
    }

    // 2) Native 兜底:仅当 backend 未覆盖该 label 或 language 时才展示。
    //    netdisk 流的容器内嵌轨经常拉取失败,留着主要给"backend 缺失"的剧集兜底。
    for (final track in nativeSubs) {
      final label = track.title ?? track.language ?? '内置字幕 ${track.id}';
      final clashByLabel = seenLabels.contains(label);
      final clashByLang =
          track.language != null && seenLanguages.contains(track.language);
      if (clashByLabel || clashByLang) continue;
      seenLabels.add(label);
      list.add({'label': label, 'type': 'native', 'track': track});
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
