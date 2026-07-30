import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 主题偏好(浅色 / 深色 / 跟随系统)——本地持久化,默认跟随系统。
///
/// **必须** extends ChangeNotifier(与 UiPrefs 的有意偏离):因为它要驱动
/// `MaterialApp.themeMode`,主题切换需要整个 app rebuild,只有通过 provider
/// + notifyListeners 才能让 MaterialApp 监听到变化。UiPrefs 不需要 rebuild
/// (调用方主动 setState),所以不加 mixin;这里相反。
///
/// 默认 [ThemePref.system]:跟随系统暗色设置(Android 设置 → 显示 → 暗色主题)。
/// 用户可在设置页手动覆盖为浅色/深色。
class ThemePrefs extends ChangeNotifier {
  ThemePrefs._();
  static final ThemePrefs instance = ThemePrefs._();

  static const _kKey = 'ui_theme_pref';

  ThemePref _value = ThemePref.system;
  bool _loaded = false;

  ThemePref get value => _value;

  /// 启动时调一次,幂等。把 prefs 读进内存。
  Future<void> load() async {
    if (_loaded) return;
    try {
      final prefs = await SharedPreferences.getInstance();
      final idx = prefs.getInt(_kKey);
      if (idx != null && idx >= 0 && idx < ThemePref.values.length) {
        _value = ThemePref.values[idx];
      }
    } catch (e) {
      debugPrint('ThemePrefs.load failed: $e');
    }
    _loaded = true;
  }

  /// 设置页切换回调。持久化 + 广播(触发 MaterialApp rebuild)。
  Future<void> set(ThemePref pref) async {
    if (pref == _value) return;
    _value = pref;
    notifyListeners();
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setInt(_kKey, pref.index);
    } catch (e) {
      debugPrint('ThemePrefs.set failed: $e');
    }
  }

  /// 供 [MaterialApp.themeMode] 直接消费的值。
  ThemeMode get themeMode {
    switch (_value) {
      case ThemePref.light:
        return ThemeMode.light;
      case ThemePref.dark:
        return ThemeMode.dark;
      case ThemePref.system:
        return ThemeMode.system;
    }
  }
}

/// 三态主题选项。index 用于持久化(存 int),顺序固定勿改。
enum ThemePref {
  light,
  dark,
  system;

  String get label {
    switch (this) {
      case ThemePref.light:
        return '浅色';
      case ThemePref.dark:
        return '深色';
      case ThemePref.system:
        return '跟随系统';
    }
  }
}
