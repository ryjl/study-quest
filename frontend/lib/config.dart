import 'package:shared_preferences/shared_preferences.dart';

class AppConfig {
  static const String _keyBaseUrl = 'backend_base_url';
  // Default to emulator localhost loopback, but can be updated by user in settings.
  static const String defaultUrl = 'http://10.0.2.2:8080';

  static String _currentUrl = defaultUrl;

  static String get baseUrl => _currentUrl;

  /// Same as [baseUrl] but guaranteed to be usable for joining with absolute
  /// API paths like "/api/v1/...". Always returns a value with no trailing slash.
  static String get baseUrlRef {
    final u = _currentUrl;
    return u.endsWith('/') ? u.substring(0, u.length - 1) : u;
  }

  static Future<void> init() async {
    final prefs = await SharedPreferences.getInstance();
    _currentUrl = prefs.getString(_keyBaseUrl) ?? defaultUrl;
  }

  static Future<void> setBaseUrl(String url) async {
    final cleanUrl = url.trim().endsWith('/') ? url.trim().substring(0, url.trim().length - 1) : url.trim();
    _currentUrl = cleanUrl;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_keyBaseUrl, cleanUrl);
  }
}
