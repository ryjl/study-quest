import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';

import 'package:study_quest/model/course.dart';
import 'package:study_quest/service/track_selection_controller.dart';

/// 字幕合并逻辑测试。
///
/// 核心策略:backend 字幕(LLM polish 后的优质字幕)优先用原名展示;
/// native(libmpv 内嵌轨)若与 backend 同 label 或同 language 则跳过。
/// 这修复了"中文"+"中文(校对版)"两个按钮的混乱,顺带修了点校对版无字幕
/// 的索引错位 bug —— 详见 track_selection_controller.dart 的策略注释。
///
/// 直接测纯函数 mergeSubtitleOptions(无 Player 依赖),避免 mock media_kit
/// PlayerState 的复杂构造。

EpisodeSubtitle _backendSub({
  required int id,
  String language = 'zh-CN',
  String label = '中文',
}) =>
    EpisodeSubtitle(
      id: id,
      language: language,
      label: label,
      url: '/api/v1/subtitles/$id.vtt',
    );

SubtitleTrack _nativeTrack({
  required String id,
  String? title,
  String? language,
}) =>
    SubtitleTrack(id, title, language);

List<String> _labels(List<Map<String, dynamic>> options) =>
    options.map((o) => o['label'] as String).toList();

void main() {
  group('mergeSubtitleOptions — backend 优先合并', () {
    test('backend 与 native 同 label:只保留一个"中文"(指向 backend)', () {
      // 这就是原 bug 场景:libmpv 抽出内嵌"中文",backend 也有"中文"。
      // 之前会变成"中文"+"中文(校对版)"两个,且点校对版无字幕。
      final options = TrackSelectionController.mergeSubtitleOptions(
        backendSubtitles: [_backendSub(id: 10, label: '中文')],
        nativeSubs: [_nativeTrack(id: '1', title: '中文', language: 'zh-CN')],
      );
      // 无 + 唯一一个"中文"(backend),native 被跳过。
      expect(_labels(options), ['无', '中文']);
      expect(options.last['type'], 'backend');
    });

    test('backend 与 native 同 language 不同 label:native 也跳过', () {
      // language 撞名也算覆盖 —— 防止"中文"(backend) + "Chinese"(native,
      // language=zh-CN) 同时出现。
      final options = TrackSelectionController.mergeSubtitleOptions(
        backendSubtitles: [_backendSub(id: 10, label: '中文')],
        nativeSubs: [_nativeTrack(id: '2', title: 'Chinese', language: 'zh-CN')],
      );
      expect(_labels(options), ['无', '中文']);
      expect(options.last['type'], 'backend');
    });

    test('无 backend:native 兜底展示', () {
      // backend 缺失的剧集(无 Whisper 转录),native 内嵌轨保留。
      final options = TrackSelectionController.mergeSubtitleOptions(
        backendSubtitles: const [],
        nativeSubs: [_nativeTrack(id: '3', title: '中文', language: 'zh-CN')],
      );
      expect(_labels(options), ['无', '中文']);
      expect(options.last['type'], 'native');
    });

    test('backend 多语言 + native 不撞名:都保留', () {
      final options = TrackSelectionController.mergeSubtitleOptions(
        backendSubtitles: [_backendSub(id: 10, label: '中文', language: 'zh-CN')],
        nativeSubs: [_nativeTrack(id: '4', title: 'English', language: 'en')],
      );
      // 关闭 + 中文(backend) + English(native,不撞名)。
      expect(_labels(options), ['无', '中文', 'English']);
    });

    test('无任何字幕:只剩"无"', () {
      final options = TrackSelectionController.mergeSubtitleOptions(
        backendSubtitles: const [],
        nativeSubs: const [],
      );
      expect(_labels(options), ['无']);
      expect(options.single['type'], 'off');
    });

    test('不再出现"(校对版)"后缀', () {
      // 回归保护:旧逻辑会给重名 backend 加"(校对版)"后缀,新逻辑不该再有。
      final options = TrackSelectionController.mergeSubtitleOptions(
        backendSubtitles: [_backendSub(id: 11, label: '中文')],
        nativeSubs: [_nativeTrack(id: '5', title: '中文', language: 'zh-CN')],
      );
      final hasProofreadSuffix =
          options.any((o) => (o['label'] as String).contains('校对'));
      expect(hasProofreadSuffix, isFalse);
    });

    test('native title 为 null:用 language 兜底当 label', () {
      // libmpv 的内嵌轨有时 title 为空,应回退到 language 而不是崩。
      final options = TrackSelectionController.mergeSubtitleOptions(
        backendSubtitles: const [],
        nativeSubs: [_nativeTrack(id: '6', title: null, language: 'ja')],
      );
      expect(_labels(options), ['无', 'ja']);
    });
  });

  group('backendSubtitleUrl', () {
    test('拼出包含原 path 的绝对 URL', () {
      // absoluteUrl 基于 AppConfig.baseUrl 拼,测试环境默认是 emulator loopback。
      // 只验证 path 段正确,不绑死 host。
      final url = TrackSelectionController.backendSubtitleUrl(
          _backendSub(id: 42));
      expect(url, contains('/api/v1/subtitles/42.vtt'));
    });
  });
}
