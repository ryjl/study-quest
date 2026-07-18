import 'dart:convert';

/// One tier of a multi-tier badge: T = threshold to clear, R = reward points.
class TierDef {
  final int t;
  final int r;
  const TierDef({required this.t, required this.r});
}

/// An achievement badge definition (mirrors backend model.Badge).
class Badge {
  final int id;
  final String code;
  final String title;
  final String description;
  final String ruleType;
  final String ruleTarget;
  final int threshold;
  // Multi-tier JSON, e.g. '[{"t":3,"r":10},{"t":7,"r":20}]'. Empty = single-tier
  // badge (uses threshold). Parsed lazily via [parsedTiers].
  final String tiers;

  const Badge({
    required this.id,
    required this.code,
    required this.title,
    required this.description,
    required this.ruleType,
    this.ruleTarget = '',
    required this.threshold,
    this.tiers = '',
  });

  factory Badge.fromJson(Map<String, dynamic> json) {
    return Badge(
      id: json['ID'] ?? json['id'] ?? 0,
      code: json['Code'] ?? json['code'] ?? '',
      title: json['Title'] ?? json['title'] ?? '',
      description: json['Description'] ?? json['description'] ?? '',
      ruleType: json['RuleType'] ?? json['rule_type'] ?? '',
      ruleTarget: json['RuleTarget'] ?? json['rule_target'] ?? '',
      threshold: json['Threshold'] ?? json['threshold'] ?? 0,
      tiers: json['Tiers'] ?? json['tiers'] ?? '',
    );
  }

  /// True when this badge has a multi-tier progression with at least one tier.
  /// A Tiers string of "[]" (empty array) is treated as single-tier so the UI
  /// doesn't render a meaningless "maxed with 0 tiers" state.
  bool get isMultiTier => tiers.isNotEmpty && parsedTiers.isNotEmpty;

  /// Parsed tier list (ascending by threshold), or empty for single-tier.
  List<TierDef> get parsedTiers {
    if (tiers.isEmpty) return const [];
    try {
      final list = jsonDecode(tiers) as List;
      return list
          .map((e) => TierDef(
                t: (e['t'] as num?)?.toInt() ?? 0,
                r: (e['r'] as num?)?.toInt() ?? 0,
              ))
          .toList();
    } catch (_) {
      return const [];
    }
  }
}

/// A badge paired with the user's current progress. Mirrors the shape
/// returned by `GET /users/:id/badges` (service.BadgeStatus).
class BadgeStatus {
  final Badge badge;
  final bool unlocked;
  /// Highest cleared tier (0-based). -1 = not unlocked any tier.
  final int tier;
  /// Total number of tiers (1 for single-tier badges).
  final int tierCount;
  /// Raw progress value (minutes/days/count/points) for multi-tier badges.
  final int progress;
  /// Next tier's threshold (0 if maxed or single-tier-unlocked).
  final int nextTier;

  const BadgeStatus({
    required this.badge,
    required this.unlocked,
    this.tier = -1,
    this.tierCount = 1,
    this.progress = 0,
    this.nextTier = 0,
  });

  factory BadgeStatus.fromJson(Map<String, dynamic> json) {
    final badge = Badge.fromJson(json);
    // Prefer the client-parsed tier count (authoritative for multi-tier).
    final parsedCount = badge.isMultiTier ? badge.parsedTiers.length : 1;
    final apiCount = _toInt(json['tier_count'] ?? json['TierCount'] ?? 1);
    return BadgeStatus(
      badge: badge,
      unlocked: json['unlocked'] == true || json['Unlocked'] == true,
      tier: _toInt(json['tier'] ?? json['Tier'] ?? -1),
      tierCount: parsedCount > 0 ? parsedCount : apiCount,
      progress: _toInt(json['progress'] ?? json['Progress'] ?? 0),
      nextTier: _toInt(json['next_tier'] ?? json['NextTier'] ?? 0),
    );
  }
}

int _toInt(Object? v) {
  if (v is num) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}
