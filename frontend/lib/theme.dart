import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

class AppTheme {
  // Theme Color Tokens
  static const Color backgroundColor = Color(0xFF0B0F19);
  static const Color cardColor = Color(0xFF111827);
  static const Color primaryColor = Color(0xFF8B5CF6);
  static const Color accentGreen = Color(0xFF10B981);
  static const Color accentOrange = Color(0xFFF59E0B);
  static const Color textWhite = Color(0xFFF1F5F9);
  static const Color textMuted = Color(0xFF9CA3AF);
  static const Color borderMuted = Color(0xFF1F2937);

  // Switch Style Constants
  static const double borderRadiusValue = 18.0;
  static const double borderWidthValue = 3.0;

  static ThemeData get darkTheme {
    return ThemeData(
      brightness: Brightness.dark,
      scaffoldBackgroundColor: backgroundColor,
      primaryColor: primaryColor,
      cardColor: cardColor,
      textTheme: GoogleFonts.outfitTextTheme(
        const TextTheme(
          displayLarge: TextStyle(color: textWhite, fontWeight: FontWeight.bold, fontSize: 32),
          titleLarge: TextStyle(color: textWhite, fontWeight: FontWeight.w600, fontSize: 20),
          bodyLarge: TextStyle(color: textWhite, fontSize: 16),
          bodyMedium: TextStyle(color: textMuted, fontSize: 14),
        ),
      ),
      colorScheme: const ColorScheme.dark(
        primary: primaryColor,
        secondary: accentGreen,
        background: backgroundColor,
        surface: cardColor,
      ),
    );
  }

  // Switch-styled Box Decoration helper for both Pad & TV
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
        width: borderWidthValue,
      ),
      boxShadow: hasFocus
          ? [
              BoxShadow(
                color: primaryColor.withOpacity(0.4),
                blurRadius: 12,
                offset: const Offset(0, 0),
              )
            ]
          : [],
    );
  }
}
