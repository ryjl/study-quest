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
  // Category 区分学术学科("academic")和娱乐子类("entertainment",如动画片/电影/
  // 纪录片)。让客户端过滤栏只显示学术 subject(避免学习大厅出现"动画片"过滤项)。
  // 后端 Subject.Category 字段,老数据无值时按 'academic' 处理(后端 default)。
  final String category;

  const Subject({
    required this.key,
    required this.label,
    this.color = '#9ca3af',
    this.category = 'academic',
  });

  /// 是否娱乐子类(动画片/电影/纪录片/综艺)。
  bool get isEntertainment => category == 'entertainment';

  factory Subject.fromJson(Map<String, dynamic> json) {
    return Subject(
      key: json['key'] ?? json['Key'] ?? '',
      label: json['label'] ?? json['Label'] ?? '',
      color: json['color'] ?? json['Color'] ?? '#9ca3af',
      category: (json['category'] ?? json['Category'] ?? 'academic').toString(),
    );
  }
}
