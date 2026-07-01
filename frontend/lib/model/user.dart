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
      avatarUrl: json['AvatarURL'] ?? json['avatar_url'] ?? '',
      role: json['Role'] ?? json['role'] ?? 'student',
    );
  }
}
