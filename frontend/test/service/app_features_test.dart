import 'package:flutter_test/flutter_test.dart';

import 'package:study_quest/service/app_features.dart';

/// 功能域裁剪测试 —— "路由表即功能定义"策略的核心。
///
/// TV 模式下错题本(AppFeature.wrongBook, supportsTv=false)整 tab 不出现。
/// 这保证 MainNavigation 的 tab 列表 / 路由表 / 角标逻辑自动跟随,
/// 而不是散落的 if (tv) 判断。

void main() {
  group('visibleFeaturesFor', () {
    test('非 TV:全部 5 个功能都可见', () {
      final features = visibleFeaturesFor(tv: false);
      expect(features.length, 5);
      expect(features, contains(AppFeature.wrongBook));
      expect(features, AppFeature.values);
    });

    test('TV:错题本被裁掉,只剩 4 个', () {
      final features = visibleFeaturesFor(tv: true);
      expect(features.length, 4);
      expect(features, isNot(contains(AppFeature.wrongBook)));
      // 其它 4 个都在。
      expect(features, containsAll(AppFeature.values.where((f) => f != AppFeature.wrongBook)));
    });

    test('TV 裁剪后顺序保持稳定(学习大厅仍是第 0 个)', () {
      // 索引稳定性:MainNavigation 用 _selectedTab 索引映射到这个列表,
      // 顺序变了会导致 tab 切到错的屏。
      final features = visibleFeaturesFor(tv: true);
      expect(features[0], AppFeature.courseHall);
      expect(features[1], AppFeature.readingRoom);
      expect(features[2], AppFeature.footprint);
      expect(features[3], AppFeature.settings);
    });

    test('默认参数读 TvMode.instance(此处只验证不抛异常)', () {
      // 默认 tv:null 走 TvMode.instance.isActive。测试环境非 Android TV,
      // isActive 应为 false,所以等价于 tv:false。只验证不崩 + 长度对。
      final features = visibleFeaturesFor();
      expect(features.length, greaterThanOrEqualTo(4));
    });
  });

  group('AppFeature — supportsTv 声明', () {
    test('只有 wrongBook 是 TV 不支持', () {
      final tvUnsupported =
          AppFeature.values.where((f) => !f.supportsTv).toList();
      expect(tvUnsupported, [AppFeature.wrongBook]);
    });

    test('每个 feature 都有非空 label 和 icon', () {
      for (final f in AppFeature.values) {
        expect(f.label.isNotEmpty, true, reason: '$f label 空');
        expect(f.icon, isNotNull, reason: '$f icon 空');
      }
    });
  });
}
