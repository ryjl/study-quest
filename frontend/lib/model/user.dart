import '../config.dart';

class User {
  final int id;
  final String nickname;
  final String avatarUrl;
  final String role;
  // Free-text grade label set by admin (e.g. "四年级"/"初二"). Shown in the
  // sidebar/profile badge in preference to the role label when non-empty.
  final String grade;

  User({
    required this.id,
    required this.nickname,
    required this.avatarUrl,
    required this.role,
    this.grade = '',
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
      grade: json['Grade'] ?? json['grade'] ?? '',
    );
  }

  static String _resolveAvatar(String raw) {
    if (raw.isEmpty) return raw;
    if (raw.startsWith('http://') || raw.startsWith('https://')) return raw;
    if (raw.startsWith('/')) return AppConfig.baseUrl + raw;
    return '${AppConfig.baseUrl}/$raw';
  }
}

/// Returns the badge label shown beside a user's name in the sidebar/profile
/// header: the admin-set grade label (e.g. "四年级") when present, otherwise a
/// localized role label (student→"学生", admin→"管理员"). Replaces the old
/// hardcoded `'四年级' : '家长'` placeholder which was always fake data.
String gradeOrRoleLabel(User user) {
  if (user.grade.isNotEmpty) return user.grade;
  switch (user.role) {
    case 'admin':
      return '管理员';
    case 'student':
    default:
      return '学生';
  }
}
