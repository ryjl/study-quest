import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

class AppTheme {
  // Theme Color Tokens
  static const Color backgroundColor = Color(0xFFF8FAFC); // Slate-50 base
  static const Color cardColor = Colors.white; // Pure white cards
  static const Color primaryColor = Color(0xFF3B82F6); // Blue-500
  static const Color accentGreen = Color(0xFF10B981); // Emerald-500
  static const Color accentOrange = Color(0xFFF97316); // Orange-500
  static const Color textWhite = Color(0xFF1E293B); // Slate-800
  static const Color textMuted = Color(0xFF64748B); // Slate-500
  static const Color borderMuted = Color(0xFFE2E8F0); // Slate-200 border

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

  static Gradient getSubjectGradient(String subject) {
    switch (subject) {
      case '语文':
        return blueGradient;
      case '数学':
      case '兴趣':
        return indigoGradient;
      case '英语':
      case '综合':
        return skyGradient;
      case '科学':
      default:
        return emeraldGradient;
      }
  }

  static ThemeData get lightTheme {
    return ThemeData(
      brightness: Brightness.light,
      scaffoldBackgroundColor: backgroundColor,
      primaryColor: primaryColor,
      cardColor: cardColor,
      textTheme: GoogleFonts.quicksandTextTheme(
        const TextTheme(
          displayLarge: TextStyle(color: textWhite, fontWeight: FontWeight.bold, fontSize: 32),
          titleLarge: TextStyle(color: textWhite, fontWeight: FontWeight.w600, fontSize: 20),
          bodyLarge: TextStyle(color: textWhite, fontSize: 18, fontWeight: FontWeight.w500),
          bodyMedium: TextStyle(color: textMuted, fontSize: 16, fontWeight: FontWeight.w500),
        ),
      ),
      colorScheme: const ColorScheme.light(
        primary: primaryColor,
        secondary: accentGreen,
        background: backgroundColor,
        surface: cardColor,
        onSurface: textWhite,
      ),
    );
  }

  // Bouncy card border helper
  static BoxDecoration switchDecoration({
    required bool hasFocus,
    Color bg = cardColor,
    Color border = borderMuted,
  }) {
    return BoxDecoration(
      color: bg,
      borderRadius: BorderRadius.circular(borderRadiusValue),
      border: Border.all(
        color: hasFocus ? primaryColor : border,
        width: hasFocus ? borderWidthValue : 2.0,
      ),
      boxShadow: hasFocus
          ? [
              BoxShadow(
                color: primaryColor.withOpacity(0.15),
                blurRadius: 16,
                offset: const Offset(0, 0),
              )
            ]
          : [
              BoxShadow(
                color: const Color(0xFF0F172A).withOpacity(0.03),
                blurRadius: 20,
                offset: const Offset(0, 4),
              )
            ],
    );
  }
}
