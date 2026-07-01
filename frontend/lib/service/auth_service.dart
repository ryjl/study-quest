import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../model/user.dart';
import 'api_service.dart';

class AuthService extends ChangeNotifier {
  static const String _keyCurrentUser = 'current_authenticated_user';

  User? _currentUser;

  User? get currentUser => _currentUser;
  bool get isAuthenticated => _currentUser != null;

  // Initialize and check if a user is cached locally
  Future<void> init() async {
    final prefs = await SharedPreferences.getInstance();
    final jsonStr = prefs.getString(_keyCurrentUser);
    if (jsonStr != null && jsonStr.isNotEmpty) {
      try {
        _currentUser = User.fromJson(jsonDecode(jsonStr));
      } catch (_) {
        _currentUser = null;
      }
    }
  }

  // Attempt login with PIN
  Future<bool> login(User user, String pin) async {
    final success = await ApiService.loginUser(user.id, pin);
    if (success) {
      _currentUser = user;
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_keyCurrentUser, jsonEncode({
        'ID': user.id,
        'Nickname': user.nickname,
        'AvatarURL': user.avatarUrl,
        'Role': user.role,
      }));
      notifyListeners();
      return true;
    }
    return false;
  }

  // Logout current session
  Future<void> logout() async {
    _currentUser = null;
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_keyCurrentUser);
    notifyListeners();
  }
}
