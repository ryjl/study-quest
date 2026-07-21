import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 全局 UI 偏好(本地存储,不上后端)。
///
/// 用户在播放器/AI 页调过的字号配置会持久化到 SharedPreferences,下次进 App
/// 自动读回 —— 满足「改一次,下次播放也和之前一样」的需求(需求 #7)。
///
/// 设计:两组独立配置 ——
///   - 字幕字号(subtitleSizeIndex):只在播放器用,因为字幕场景对字号敏感,
///     且和 AI 页阅读体验是两回事。
///   - AI 页文字缩放(aiTextScaleIndex):summary / advice / quiz 所有文字一起缩放。
///
/// 用 index 而非原始数值:档位是离散的(小/中/大/超大),存 index 让 UI 的 segmented
/// control 直接绑定,且未来想调整具体数值只改 [_subtitleSizes]/[_aiScales] 即可。
///
/// 不再 extends ChangeNotifier(原 Step 7 fallback 决策):全 App 通过
/// [instance] 单例直读,~25 处调用点里大量位于 `onTap` 等非 build 上下文,
/// `context.watch` 在那里是非法的;迁移到 MultiProvider 会逼着重写那些回调并
/// 影响 rebuild 语义。没有任何外部代码调用 addListener / notifyListeners,
/// 所以去掉 mixin 后唯一行为变化是 setter 不再广播 —— 而调用方本来就需要
/// 主动 setState 拉新值,等价于现在的契约。
class UiPrefs {
  UiPrefs._();
  static final UiPrefs instance = UiPrefs._();

  static const _kSubtitleSize = 'ui_subtitle_size_index';
  static const _kAiTextScale = 'ui_ai_text_scale_index';

  // 字幕字号档位(dp)。播放器里字幕背景半透明,字号要够大才看得清,所以最小档也
  // 不太小。需求 #5 反馈「最大也小」,这里把最大档从原来的 26 提到 36。
  static const List<double> _subtitleSizes = [18.0, 24.0, 30.0, 38.0];
  // AI 页文字缩放因子,作用于所有 Text 的 fontSize。
  // 0.85 紧凑(信息密度高)、1.0 默认、1.2 大、1.4 超大(老花/远距离 TV 场景)。
  static const List<double> _aiScales = [0.85, 1.0, 1.2, 1.4];

  int _subtitleSizeIndex = 1; // 默认「中」
  int _aiTextScaleIndex = 1; // 默认「中」(1.0)
  bool _loaded = false;

  /// 字幕字号档位的中文标签(供 UI segmented control 用)。
  static const List<String> subtitleSizeLabels = ['小', '中', '大', '超大'];
  static const List<String> aiScaleLabels = ['紧凑', '标准', '大', '超大'];

  double get subtitleSize => _subtitleSizes[_subtitleSizeIndex.clamp(0, _subtitleSizes.length - 1)];
  int get subtitleSizeIndex => _subtitleSizeIndex;

  double get aiTextScale => _aiScales[_aiTextScaleIndex.clamp(0, _aiScales.length - 1)];
  int get aiTextScaleIndex => _aiTextScaleIndex;

  /// 启动时调用一次,把 prefs 读进内存。调用方应 await 完成后再读字段。
  /// 重复调用幂等(已加载直接返回)。
  Future<void> load() async {
    if (_loaded) return;
    try {
      final prefs = await SharedPreferences.getInstance();
      _subtitleSizeIndex = (prefs.getInt(_kSubtitleSize) ?? 1).clamp(0, _subtitleSizes.length - 1);
      _aiTextScaleIndex = (prefs.getInt(_kAiTextScale) ?? 1).clamp(0, _aiScales.length - 1);
    } catch (e) {
      if (kDebugMode) debugPrint('UiPrefs.load failed: $e');
    }
    _loaded = true;
  }

  Future<void> setSubtitleSizeIndex(int index) async {
    final clamped = index.clamp(0, _subtitleSizes.length - 1);
    if (clamped == _subtitleSizeIndex) return;
    _subtitleSizeIndex = clamped;
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setInt(_kSubtitleSize, clamped);
    } catch (e) {
      if (kDebugMode) debugPrint('UiPrefs.setSubtitleSizeIndex failed: $e');
    }
  }

  Future<void> setAiTextScaleIndex(int index) async {
    final clamped = index.clamp(0, _aiScales.length - 1);
    if (clamped == _aiTextScaleIndex) return;
    _aiTextScaleIndex = clamped;
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setInt(_kAiTextScale, clamped);
    } catch (e) {
      if (kDebugMode) debugPrint('UiPrefs.setAiTextScaleIndex failed: $e');
    }
  }
}
