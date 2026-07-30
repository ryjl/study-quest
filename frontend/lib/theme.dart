import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';
import 'theme/app_colors.dart';

export 'theme/app_colors.dart';

/// 全局主题门面:ThemeData 构建 + 亮度无关常量 + 焦点 BoxDecoration helper。
///
/// 语义颜色 token 已迁到 [AppColors](ThemeExtension,亮/暗两套),调用点用
/// `context.colors.xxx` 取色(见 [AppColorsX])。这里保留:
///   - 旧静态 Color 常量(= 亮色默认值):给 `const` 默认参数 / 渐变 / 工厂构造
///     等必须在 const 上下文里用的地方。业务 build 里应优先用 `context.colors`。
///   - 渐变 token(品牌渐变两端一致,与亮度无关)。
///   - 几何常量、[switchDecoration] / [getSubjectGradientFromColor] /
///     [colorFromHex] 等亮度无关或已参数化的 helper。
class AppTheme {
  // ---- 旧语义 Color 常量(= 亮色默认值,供 const 上下文用)----
  // 注:业务 build 里应改用 `context.colors.xxx`(亮/暗感知)。这些常量保留是
  // 因为渐变、工厂构造 const 默认值、switchDecoration 形参默认值必须在 const
  // 上下文求值,读不了 context。值与 AppColors.light 一致(亮色默认)。
  static const Color backgroundColor = Color(0xFFF8FAFC);
  static const Color cardColor = Color(0xFFFFFFFF);
  static const Color primaryColor = Color(0xFF3B82F6);
  static const Color accentGreen = Color(0xFF10B981);
  static const Color accentOrange = Color(0xFFF97316);
  static const Color textWhite = Color(0xFF1E293B);
  static const Color textMuted = Color(0xFF64748B);
  static const Color borderMuted = Color(0xFFE2E8F0);

  // Extended Slate ramp — promoted from inline literals so files don't
  // keep re-typing the same hex. backgroundColor above is slate50.
  static const Color slate50 = Color(0xFFF8FAFC);
  static const Color slate100 = Color(0xFFF1F5F9);
  static const Color slate200 = Color(0xFFE2E8F0); // alias of borderMuted
  static const Color slate300 = Color(0xFFCBD5E1);
  static const Color slate400 = Color(0xFF94A3B8);
  static const Color slate500 = Color(0xFF64748B); // alias of textMuted
  static const Color slate600 = Color(0xFF475569);
  static const Color slate700 = Color(0xFF334155);
  static const Color slate900 = Color(0xFF0F172A);

  // Brand accent ramp.
  static const Color indigo500 = Color(0xFF6366F1);
  static const Color violet500 = Color(0xFF8B5CF6);
  static const Color blue100 = Color(0xFFEFF6FF);
  static const Color blue600 = Color(0xFF2563EB);

  // Semantic / status accents.
  static const Color emerald100 = Color(0xFFECFDF5);
  static const Color amber50 = Color(0xFFFFFBEB);
  static const Color orange400 = Color(0xFFFB923C);
  static const Color yellow400 = Color(0xFFFACC15);

  // Reusable gradient tokens. Brand drives primary CTAs/headers; levelBadge
  // powers the XP/grade pill; avatarRing is the circular halo behind user
  // avatars in the profile drawer. 渐变两端一致(品牌识别度),与亮度无关。
  static const Gradient brandGradient = LinearGradient(
    colors: [Color(0xFF3B82F6), Color(0xFF6366F1)],
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
  );
  static const Gradient levelBadgeGradient = LinearGradient(
    colors: [Color(0xFFFB923C), Color(0xFFFACC15)],
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
  );
  static const Gradient avatarRingGradient = LinearGradient(
    colors: [Color(0xFF60A5FA), Color(0xFFC084FC)],
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
  );

  // Switch Style Constants
  static const double borderRadiusValue = 20.0;
  static const double borderWidthValue = 3.0;

  // Gradients for subject cards
  static const Gradient blueGradient = LinearGradient(
    colors: [Color(0xFF60A5FA), Color(0xFF3B82F6)],
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
  );

  static const Gradient indigoGradient = LinearGradient(
    colors: [Color(0xFF818CF8), Color(0xFF6366F1)],
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
  );

  static const Gradient skyGradient = LinearGradient(
    colors: [Color(0xFF38BDF8), Color(0xFF0EA5E9)],
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
  );

  static const Gradient emeraldGradient = LinearGradient(
    colors: [Color(0xFF34D399), Color(0xFF10B981)],
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
  );

  /// Builds a diagonal gradient from a hex color (e.g. "#f59e0b"). Used so the
  /// course card banner matches the admin-configured subject color instead of
  /// a hardcoded per-name switch. Falls back to the primary gradient.
  static Gradient getSubjectGradientFromColor(String hexColor) {
    final base = _parseHex(hexColor) ?? primaryColor;
    return LinearGradient(
      colors: [
        Color.alphaBlend(base.withValues(alpha: 0.55), Colors.white),
        base,
      ],
      begin: Alignment.topLeft,
      end: Alignment.bottomRight,
    );
  }

  static Color? _parseHex(String hex) {
    var h = hex.trim();
    if (h.isEmpty) return null;
    if (h.startsWith('#')) h = h.substring(1);
    if (h.length == 3) {
      h = h.split('').map((c) => '$c$c').join();
    }
    if (h.length == 6) h = 'FF$h';
    final value = int.tryParse(h, radix: 16);
    return value == null ? null : Color(value);
  }

  /// Parses a hex color string (e.g. "#f59e0b") into a [Color]. Falls back to
  /// [primaryColor] when the string is empty/invalid. Used for tinting subject
  /// icons on gradient covers where the cover already uses the subject color.
  static Color colorFromHex(String hexColor) {
    return _parseHex(hexColor) ?? primaryColor;
  }

  // ---- 亮色文字主题(亮/暗共用结构,颜色随 ThemeData.colorScheme 取)----
  static TextTheme _quicksandTextTheme(Color display, Color title, Color body, Color bodyMuted) {
    return GoogleFonts.quicksandTextTheme(
      TextTheme(
        displayLarge: TextStyle(color: display, fontWeight: FontWeight.bold, fontSize: 32),
        titleLarge: TextStyle(color: title, fontWeight: FontWeight.w600, fontSize: 20),
        bodyLarge: TextStyle(color: body, fontSize: 18, fontWeight: FontWeight.w500),
        bodyMedium: TextStyle(color: bodyMuted, fontSize: 16, fontWeight: FontWeight.w500),
      ),
    );
  }

  static ThemeData get lightTheme => _buildTheme(AppColors.light);
  static ThemeData get darkTheme => _buildTheme(AppColors.dark);

  static ThemeData _buildTheme(AppColors c) {
    final isDark = c.backgroundColor == AppColors.dark.backgroundColor;
    return ThemeData(
      brightness: isDark ? Brightness.dark : Brightness.light,
      scaffoldBackgroundColor: c.backgroundColor,
      primaryColor: c.primaryColor,
      cardColor: c.cardColor,
      textTheme: _quicksandTextTheme(c.textWhite, c.textWhite, c.textWhite, c.textMuted),
      colorScheme: (isDark ? const ColorScheme.dark() : const ColorScheme.light()).copyWith(
        primary: c.primaryColor,
        secondary: c.accentGreen,
        surface: c.cardColor,
        onSurface: c.textWhite,
      ),
      extensions: [c],
    );
  }

  /// Bouncy card border helper —— 焦点态 BoxDecoration。
  /// bg/border/shadowColor 已参数化,调用方可传 `context.colors.xxx` 让暗色生效。
  /// 非焦点阴影默认浅灰(slate900 alpha),暗色下应传更亮的边框/阴影色。
  static BoxDecoration switchDecoration({
    required bool hasFocus,
    Color? bg,
    Color? border,
    Color? unfocusedShadowColor,
  }) {
    final bgCol = bg ?? cardColor;
    final borderCol = border ?? borderMuted;
    return BoxDecoration(
      color: bgCol,
      borderRadius: BorderRadius.circular(borderRadiusValue),
      border: Border.all(
        color: hasFocus ? primaryColor : borderCol,
        width: hasFocus ? borderWidthValue : 2.0,
      ),
      boxShadow: hasFocus
          ? [
              BoxShadow(
                color: primaryColor.withValues(alpha: 0.15),
                blurRadius: 16,
                offset: const Offset(0, 0),
              )
            ]
          : [
              BoxShadow(
                color: (unfocusedShadowColor ?? const Color(0xFF0F172A)).withValues(alpha: 0.03),
                blurRadius: 20,
                offset: const Offset(0, 4),
              )
            ],
    );
  }
}
