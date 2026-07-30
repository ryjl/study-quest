import 'package:flutter/material.dart';

/// 语义颜色 token —— 亮 / 暗两套,通过 [ThemeExtension] 注入主题,调用点用
/// `context.colors.xxx` 取值(见 [AppColorsX] 扩展)。
///
/// 与 `docs/design-tokens.md` 对齐。品牌主色 / 渐变终点 / 语义色两端一致;
/// 底色取向不同:亮色用 slate50 底,暗色用 slate900 底(对齐 TV 深色分工,
/// 业界 TV/暗色惯例:不刺眼、OLED 防烧、远距离可读)。
///
/// slate ramp 字段名(textWhite/textMuted/borderMuted 等)沿用旧 AppTheme
/// 静态常量名,避免调用点迁移时语义漂移 —— 它们只是"主文字/静音文字/边框"
/// 的别名,亮暗取值不同,但名字不变。
class AppColors extends ThemeExtension<AppColors> {
  const AppColors({
    required this.backgroundColor,
    required this.cardColor,
    required this.primaryColor,
    required this.accentGreen,
    required this.accentOrange,
    required this.textWhite,
    required this.textMuted,
    required this.borderMuted,
    required this.slate50,
    required this.slate100,
    required this.slate200,
    required this.slate300,
    required this.slate400,
    required this.slate500,
    required this.slate600,
    required this.slate700,
    required this.slate800,
    required this.slate900,
    required this.indigo500,
    required this.violet500,
    required this.blue100,
    required this.blue600,
    required this.emerald100,
    required this.amber50,
    required this.orange400,
    required this.yellow400,
  });

  // ---- 语义 token(亮度相关,亮暗不同)----
  /// 页面背景。亮=slate50,暗=slate900。
  final Color backgroundColor;

  /// 卡片底色。亮=白,暗=slate800。
  final Color cardColor;

  /// 主文字(深底取浅、浅底取深)。亮=slate800,暗=slate100。
  final Color textWhite;

  /// 静音/辅助文字。亮=slate500,暗=slate400。
  final Color textMuted;

  /// 常规边框。亮=slate200,暗=slate700。
  final Color borderMuted;

  // ---- 品牌主色(两端一致)----
  final Color primaryColor; // Blue-500
  final Color accentGreen; // Emerald-500
  final Color accentOrange; // Orange-500

  // ---- Slate 中性灰阶 ramp(两端共用 ramp,取值本身不变;字段保留供 ramp 引用)----
  final Color slate50;
  final Color slate100;
  final Color slate200;
  final Color slate300;
  final Color slate400;
  final Color slate500;
  final Color slate600;
  final Color slate700;
  final Color slate800;
  final Color slate900;

  // ---- 品牌延伸色(两端一致)----
  final Color indigo500;
  final Color violet500;
  final Color blue100;
  final Color blue600;

  // ---- 语义 / 状态色(两端一致)----
  final Color emerald100;
  final Color amber50;
  final Color orange400;
  final Color yellow400;

  // ---- 亮 / 暗实例 ----
  //
  // 亮色:沿用旧 AppTheme 静态常量值(slate50 底 / slate800 主文字)。
  // 暗色:slate900 底 / slate800 卡片 / slate100 主文字 / slate400 静音文字 /
  //      slate700 边框 —— 与 TV 深色分工(design-tokens.md)对齐,保证客厅/暗光
  //      下不刺眼、对比度足够。

  static const AppColors light = AppColors(
    backgroundColor: Color(0xFFF8FAFC),
    cardColor: Color(0xFFFFFFFF),
    primaryColor: Color(0xFF3B82F6),
    accentGreen: Color(0xFF10B981),
    accentOrange: Color(0xFFF97316),
    textWhite: Color(0xFF1E293B),
    textMuted: Color(0xFF64748B),
    borderMuted: Color(0xFFE2E8F0),
    slate50: Color(0xFFF8FAFC),
    slate100: Color(0xFFF1F5F9),
    slate200: Color(0xFFE2E8F0),
    slate300: Color(0xFFCBD5E1),
    slate400: Color(0xFF94A3B8),
    slate500: Color(0xFF64748B),
    slate600: Color(0xFF475569),
    slate700: Color(0xFF334155),
    slate800: Color(0xFF1E293B),
    slate900: Color(0xFF0F172A),
    indigo500: Color(0xFF6366F1),
    violet500: Color(0xFF8B5CF6),
    blue100: Color(0xFFEFF6FF),
    blue600: Color(0xFF2563EB),
    emerald100: Color(0xFFECFDF5),
    amber50: Color(0xFFFFFBEB),
    orange400: Color(0xFFFB923C),
    yellow400: Color(0xFFFACC15),
  );

  static const AppColors dark = AppColors(
    backgroundColor: Color(0xFF0F172A), // slate900 深底
    cardColor: Color(0xFF1E293B), // slate800 卡片
    primaryColor: Color(0xFF3B82F6),
    accentGreen: Color(0xFF10B981),
    accentOrange: Color(0xFFF97316),
    textWhite: Color(0xFFF1F5F9), // slate100 主文字(深底取浅)
    textMuted: Color(0xFF94A3B8), // slate400 静音文字
    borderMuted: Color(0xFF334155), // slate700 边框
    slate50: Color(0xFFF8FAFC),
    slate100: Color(0xFFF1F5F9),
    slate200: Color(0xFFE2E8F0),
    slate300: Color(0xFFCBD5E1),
    slate400: Color(0xFF94A3B8),
    slate500: Color(0xFF64748B),
    slate600: Color(0xFF475569),
    slate700: Color(0xFF334155),
    slate800: Color(0xFF1E293B),
    slate900: Color(0xFF0F172A),
    indigo500: Color(0xFF6366F1),
    violet500: Color(0xFF8B5CF6),
    blue100: Color(0xFFEFF6FF),
    blue600: Color(0xFF2563EB),
    emerald100: Color(0xFFECFDF5),
    amber50: Color(0xFFFFFBEB),
    orange400: Color(0xFFFB923C),
    yellow400: Color(0xFFFACC15),
  );

  @override
  AppColors copyWith({
    Color? backgroundColor,
    Color? cardColor,
    Color? primaryColor,
    Color? accentGreen,
    Color? accentOrange,
    Color? textWhite,
    Color? textMuted,
    Color? borderMuted,
    Color? slate50,
    Color? slate100,
    Color? slate200,
    Color? slate300,
    Color? slate400,
    Color? slate500,
    Color? slate600,
    Color? slate700,
    Color? slate800,
    Color? slate900,
    Color? indigo500,
    Color? violet500,
    Color? blue100,
    Color? blue600,
    Color? emerald100,
    Color? amber50,
    Color? orange400,
    Color? yellow400,
  }) {
    return AppColors(
      backgroundColor: backgroundColor ?? this.backgroundColor,
      cardColor: cardColor ?? this.cardColor,
      primaryColor: primaryColor ?? this.primaryColor,
      accentGreen: accentGreen ?? this.accentGreen,
      accentOrange: accentOrange ?? this.accentOrange,
      textWhite: textWhite ?? this.textWhite,
      textMuted: textMuted ?? this.textMuted,
      borderMuted: borderMuted ?? this.borderMuted,
      slate50: slate50 ?? this.slate50,
      slate100: slate100 ?? this.slate100,
      slate200: slate200 ?? this.slate200,
      slate300: slate300 ?? this.slate300,
      slate400: slate400 ?? this.slate400,
      slate500: slate500 ?? this.slate500,
      slate600: slate600 ?? this.slate600,
      slate700: slate700 ?? this.slate700,
      slate800: slate800 ?? this.slate800,
      slate900: slate900 ?? this.slate900,
      indigo500: indigo500 ?? this.indigo500,
      violet500: violet500 ?? this.violet500,
      blue100: blue100 ?? this.blue100,
      blue600: blue600 ?? this.blue600,
      emerald100: emerald100 ?? this.emerald100,
      amber50: amber50 ?? this.amber50,
      orange400: orange400 ?? this.orange400,
      yellow400: yellow400 ?? this.yellow400,
    );
  }

  @override
  AppColors lerp(AppColors? other, double t) {
    if (other == null) return this;
    return AppColors(
      backgroundColor: Color.lerp(backgroundColor, other.backgroundColor, t)!,
      cardColor: Color.lerp(cardColor, other.cardColor, t)!,
      primaryColor: Color.lerp(primaryColor, other.primaryColor, t)!,
      accentGreen: Color.lerp(accentGreen, other.accentGreen, t)!,
      accentOrange: Color.lerp(accentOrange, other.accentOrange, t)!,
      textWhite: Color.lerp(textWhite, other.textWhite, t)!,
      textMuted: Color.lerp(textMuted, other.textMuted, t)!,
      borderMuted: Color.lerp(borderMuted, other.borderMuted, t)!,
      slate50: Color.lerp(slate50, other.slate50, t)!,
      slate100: Color.lerp(slate100, other.slate100, t)!,
      slate200: Color.lerp(slate200, other.slate200, t)!,
      slate300: Color.lerp(slate300, other.slate300, t)!,
      slate400: Color.lerp(slate400, other.slate400, t)!,
      slate500: Color.lerp(slate500, other.slate500, t)!,
      slate600: Color.lerp(slate600, other.slate600, t)!,
      slate700: Color.lerp(slate700, other.slate700, t)!,
      slate800: Color.lerp(slate800, other.slate800, t)!,
      slate900: Color.lerp(slate900, other.slate900, t)!,
      indigo500: Color.lerp(indigo500, other.indigo500, t)!,
      violet500: Color.lerp(violet500, other.violet500, t)!,
      blue100: Color.lerp(blue100, other.blue100, t)!,
      blue600: Color.lerp(blue600, other.blue600, t)!,
      emerald100: Color.lerp(emerald100, other.emerald100, t)!,
      amber50: Color.lerp(amber50, other.amber50, t)!,
      orange400: Color.lerp(orange400, other.orange400, t)!,
      yellow400: Color.lerp(yellow400, other.yellow400, t)!,
    );
  }
}

/// context 感知取色扩展。调用点:`context.colors.primaryColor`。
///
/// 从当前主题读 [AppColors] ThemeExtension;若未注入(如纯 widget 测试)兜底
/// 返回亮色,避免 null 崩溃。
extension AppColorsX on BuildContext {
  AppColors get colors => Theme.of(this).extension<AppColors>() ?? AppColors.light;
}
