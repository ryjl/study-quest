/// App-level features that can be toggled on/off by device class.
///
/// 单 APK 同时跑 PAD 和 TV,用运行时 [TvMode] 裁剪功能域。功能定义集中在这
/// 一个枚举里:加新功能 = 加一个 enum 值 + 决定它的 supportsTv,导航表/路由
/// 自动跟随,不散落 if (tv) 判断。这是"路由表即功能定义"的优雅写法 ——
/// 业界主流(Flutter 官方 adaptive / Kotlin sealed route / Web RBAC)的套路。
library;

import 'package:flutter/material.dart';

import 'tv_mode.dart';

/// A top-level feature surfaced as a navigation tab (and its screen).
///
/// icon 用 IconData(运行时常量)。enum 构造不是 const,但 Dart 允许非 const
/// enum 字段,这里接受 —— enum 实例本身仍是单例,只是构造时跑一次 IconData。
enum AppFeature {
  /// 学习大厅:课程列表 + 进入播放。
  courseHall(Icons.school_rounded, '学习大厅'),

  /// 阅读室:书架 + PDF/文章阅读。
  readingRoom(Icons.menu_book_rounded, '阅读室'),

  /// 成长足迹:积分/时长/通关 dashboard(纯展示)。
  footprint(Icons.explore_rounded, '成长足迹'),

  /// 错题本:复习做错的题(PAD only —— TV 做题体验差,且"重做一批"已禁)。
  wrongBook(Icons.spellcheck_rounded, '错题本', supportsTv: false),

  /// 系统设置:服务器地址、播放偏好、TV 模式开关、登出。
  settings(Icons.settings_rounded, '系统设置');

  const AppFeature(this.icon, this.label, {this.supportsTv = true});

  final IconData icon;
  final String label;
  final bool supportsTv;
}

/// The ordered list of features visible under the current device class.
///
/// Filters out TV-incompatible features (e.g. [AppFeature.wrongBook]) when
/// [TvMode] is active. Order is preserved from [AppFeature.values] so the
/// visible-tab indices stay stable.
List<AppFeature> visibleFeaturesFor({bool? tv}) {
  final isTv = tv ?? TvMode.instance.isActive;
  return AppFeature.values.where((f) => !isTv || f.supportsTv).toList(growable: false);
}
