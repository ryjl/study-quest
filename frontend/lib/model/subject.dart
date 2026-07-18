/// A course subject (科目) — mirrors the backend `subjects` table served at
/// `/api/v1/subjects`. Courses carry only the [key] string; the display
/// label / color are looked up from this catalog.
///
/// The Flutter client used to hardcode Chinese display names and switch on
/// them; with subjects now DB-driven we fetch the catalog and resolve by key.
///
/// The DB no longer carries an emoji field — the visual icon is derived from
/// the subject key via `subjectIconData` (see lib/ui/widget/subject_icon.dart).
class Subject {
  final String key;
  final String label;
  final String color;

  const Subject({
    required this.key,
    required this.label,
    this.color = '#9ca3af',
  });

  factory Subject.fromJson(Map<String, dynamic> json) {
    return Subject(
      key: json['key'] ?? json['Key'] ?? '',
      label: json['label'] ?? json['Label'] ?? '',
      color: json['color'] ?? json['Color'] ?? '#9ca3af',
    );
  }

  Subject copyWith({
    String? key,
    String? label,
    String? color,
  }) {
    return Subject(
      key: key ?? this.key,
      label: label ?? this.label,
      color: color ?? this.color,
    );
  }
}
