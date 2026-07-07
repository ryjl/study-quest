/// A course tag (标签) — mirrors the backend `tags` table served at
/// `/api/v1/tags`. Courses carry tag IDs; the UI resolves them to labels via
/// this catalog (fetched once on the course list screen).
class Tag {
  final int id;
  final String key;
  final String label;
  final String color;

  const Tag({
    required this.id,
    required this.key,
    required this.label,
    this.color = '#64748b',
  });

  factory Tag.fromJson(Map<String, dynamic> json) {
    return Tag(
      id: (json['id'] ?? json['ID'] ?? 0) as int,
      key: json['key'] ?? json['Key'] ?? '',
      label: json['label'] ?? json['Label'] ?? '',
      color: json['color'] ?? json['Color'] ?? '#64748b',
    );
  }
}
