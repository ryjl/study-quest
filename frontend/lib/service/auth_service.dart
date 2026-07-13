import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../model/user.dart';
import 'api_service.dart';
import 'device_info_service.dart';

class AuthService extends ChangeNotifier {
  static const String _keyCurrentUser = 'current_authenticated_user';
  static const String _keyAuthToken = 'auth_token';

  User? _currentUser;
  String? _authToken;

  User? get currentUser => _currentUser;
  // Authenticated requires BOTH a cached user and a live token. A user without
  // a token is the upgrade-from-old-client state and is treated as logged out.
  bool get isAuthenticated => _currentUser != null && _authToken != null;

  AuthService() {
    // Register the global 401 hook so any authenticated API call that comes
    // back 401 (expired / admin-revoked) clears the local session and triggers
    // a return to the login screen via notifyListeners.
    ApiService.onUnauthorized = () async => await logout();
  }

  // Initialize and check if a user is cached locally.
  Future<void> init() async {
    final prefs = await SharedPreferences.getInstance();
    final tokenStr = prefs.getString(_keyAuthToken);
    final jsonStr = prefs.getString(_keyCurrentUser);

    // Upgrade compatibility: an old client may have a cached user but no token
    // (the pre-session scheme stored only the user id). Treat that as logged
    // out — clear the stale user so the next request doesn't loop on a 401.
    if ((tokenStr == null || tokenStr.isEmpty) && jsonStr != null) {
      await prefs.remove(_keyCurrentUser);
      _currentUser = null;
      _authToken = null;
      ApiService.authToken = null;
      return;
    }

    if (jsonStr != null && jsonStr.isNotEmpty && tokenStr != null && tokenStr.isNotEmpty) {
      try {
        _currentUser = User.fromJson(jsonDecode(jsonStr));
        _authToken = tokenStr;
        ApiService.authToken = tokenStr;
      } catch (_) {
        _currentUser = null;
        _authToken = null;
        ApiService.authToken = null;
      }
    }
  }

  // Attempt login with PIN. On success, persists the user + token and returns
  // true; on failure returns false and changes nothing.
  Future<bool> login(User user, String pin) async {
    // Best-effort device label so the admin device list shows "iPad" / "客厅
    // 电视" rather than a raw user-agent. Failures here must not block login —
    // we send no device_name and the backend falls back to the UA.
    String? deviceName;
    try {
      deviceName = await DeviceInfoService.getDeviceName();
    } catch (_) {
      deviceName = null;
    }

    final token = await ApiService.loginUser(user.id, pin, deviceName: deviceName);
    if (token != null) {
      _currentUser = user;
      _authToken = token;
      ApiService.authToken = token;
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_keyCurrentUser, jsonEncode({
        'ID': user.id,
        'Nickname': user.nickname,
        'AvatarURL': user.avatarUrl,
        'Role': user.role,
      }));
      await prefs.setString(_keyAuthToken, token);
      notifyListeners();
      return true;
    }
    return false;
  }

  // Logout current session. Asks the server to revoke the token (so it can't
  // be replayed even if captured), then clears local state regardless of the
  // server response.
  Future<void> logout() async {
    await ApiService.logout();
    _currentUser = null;
    _authToken = null;
    ApiService.authToken = null;
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_keyCurrentUser);
    await prefs.remove(_keyAuthToken);
    notifyListeners();
  }
}
