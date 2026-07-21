import 'dart:io' show Platform;
import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:device_info_plus/device_info_plus.dart';

/// Android TV 模式检测。
///
/// 两种来源 OR 起来即「TV 模式生效」(isActive):
///   1. 自动检测:运行在真机 TV 上 —— device_info_plus 的 systemFeatures 含
///      "android.software.leanback",或 host/id 包含 "tv"/"_tv"/"googletv"。
///   2. 调试强制:设置页的「预览 TV 模式」开关(debug_force_tv)。
///      给 MuMu 模拟器(PAD 形态)开发用 —— 打开后强制走 TV 分支布局,
///      这样开发者不用真机 TV 也能验证 TV 页面。
///
/// 非 Android 平台(iOS/desktop)永远是 false。
class TvMode {
  TvMode._();
  static final TvMode instance = TvMode._();

  static const _kDebugForceTv = 'debug_force_tv';

  bool _autoDetected = false; // 真机 TV 自动检测结果
  bool _forceEnabled = false; // 调试开关
  bool _loaded = false;

  /// TV 模式是否生效。UI 各处用这个判断走 PAD 分支还是 TV 分支。
  bool get isActive => _autoDetected || _forceEnabled;
  bool get forceEnabled => _forceEnabled;

  /// 启动时调一次。幂等。
  Future<void> load() async {
    if (_loaded) return;
    _autoDetected = await _detectAndroidTv();
    try {
      final prefs = await SharedPreferences.getInstance();
      _forceEnabled = prefs.getBool(_kDebugForceTv) ?? false;
    } catch (e) {
      if (kDebugMode) debugPrint('TvMode.load force flag failed: $e');
    }
    _loaded = true;
  }

  /// 设置页调试开关回调。
  Future<void> setForceEnabled(bool enabled) async {
    _forceEnabled = enabled;
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setBool(_kDebugForceTv, enabled);
    } catch (e) {
      if (kDebugMode) debugPrint('TvMode.setForceEnabled failed: $e');
    }
  }

  Future<bool> _detectAndroidTv() async {
    if (!Platform.isAndroid) return false;
    try {
      final info = await DeviceInfoPlugin().androidInfo;
      // leanback feature 是 Android TV 的官方标识。
      if (info.systemFeatures.contains('android.software.leanback')) return true;
      // uimode 也可能包含 UI_MODE_TYPE_TELEVISION 信息,但 device_info_plus 10.x
      // 没直接暴露 uiMode,用 host/id 名字兜底(很多 TV 盒子 host 含 "tv")。
      final host = info.host.toLowerCase();
      final model = info.model.toLowerCase();
      final device = info.device.toLowerCase();
      if (host.contains('tv') || model.contains('tv') || device.contains('tv')) return true;
      return false;
    } catch (e) {
      if (kDebugMode) debugPrint('TvMode._detectAndroidTv failed: $e');
      return false;
    }
  }
}
