/// An achievement badge definition (mirrors backend model.Badge).
class Badge {
  final int id;
  final String code;
  final String title;
  final String description;
  final String iconName;
  final String ruleType;
  final String ruleTarget;
  final int threshold;

  const Badge({
    required this.id,
    required this.code,
    required this.title,
    required this.description,
    required this.iconName,
    required this.ruleType,
    this.ruleTarget = '',
    required this.threshold,
  });

  factory Badge.fromJson(Map<String, dynamic> json) {
    return Badge(
      id: json['ID'] ?? json['id'] ?? 0,
      code: json['Code'] ?? json['code'] ?? '',
      title: json['Title'] ?? json['title'] ?? '',
      description: json['Description'] ?? json['description'] ?? '',
      iconName: json['IconName'] ?? json['icon_name'] ?? '',
      ruleType: json['RuleType'] ?? json['rule_type'] ?? '',
      ruleTarget: json['RuleTarget'] ?? json['rule_target'] ?? '',
      threshold: json['Threshold'] ?? json['threshold'] ?? 0,
    );
  }
}

/// A badge paired with its unlocked state for the current user.
///
/// Mirrors the shape returned by `GET /users/:id/badges` — each entry is a
/// badge object with an extra `unlocked` boolean.
class BadgeStatus {
  final Badge badge;
  final bool unlocked;

  const BadgeStatus({required this.badge, required this.unlocked});

  factory BadgeStatus.fromJson(Map<String, dynamic> json) {
    return BadgeStatus(
      badge: Badge.fromJson(json),
      unlocked: json['unlocked'] == true || json['Unlocked'] == true,
    );
  }
}
