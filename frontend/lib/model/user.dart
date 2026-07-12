import '../config.dart';

class User {
  final int id;
  final String nickname;
  final String avatarUrl;
  final String role;

  User({
    required this.id,
    required this.nickname,
    required this.avatarUrl,
    required this.role,
  });

  factory User.fromJson(Map<String, dynamic> json) {
    return User(
      id: json['ID'] ?? json['id'] ?? 0,
      nickname: json['Nickname'] ?? json['nickname'] ?? '',
      // The backend stores avatar as a server-relative path
      // ("/uploads/xxx.jpg"); resolve it to an absolute URL here so every
      // consumer (login screen, sidebar, portrait header) renders correctly.
      // Already-absolute URLs (http/https) pass through unchanged, which also
      // makes re-deserializing a cached user idempotent.
      avatarUrl: _resolveAvatar(json['AvatarURL'] ?? json['avatar_url'] ?? ''),
      role: json['Role'] ?? json['role'] ?? 'student',
    );
  }

  static String _resolveAvatar(String raw) {
    if (raw.isEmpty) return raw;
    if (raw.startsWith('http://') || raw.startsWith('https://')) return raw;
    if (raw.startsWith('/')) return AppConfig.baseUrl + raw;
    return '${AppConfig.baseUrl}/$raw';
  }
}
