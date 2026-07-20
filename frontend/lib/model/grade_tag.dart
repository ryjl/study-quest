/// A grade tag entry served by `/api/v1/courses/grade-tags`.
///
/// 2026-07-20: grade 改成开放 tag 体系后,客户端过滤栏不再写死 1-9 年级,
/// 改成从这个端点动态拉取可用 tag 列表(5 个预设 + admin 已用的自定义 tag)。
///
/// 后端字段:
///   - `key`: 稳定标识(预设如 "primary"/"junior";自定义如 "考研")。客户端
///     按这个值做过滤匹配(course.grade 字段里存的就是 key)。
///   - `label`: 展示文字(预设已本地化如 "小学";自定义原样输出)。
///   - `preset`: 是否预设值。客户端目前不区分渲染,留作未来扩展(如标记自定义
///     tag 的 chip)。
class GradeTag {
  final String key;
  final String label;
  final bool preset;

  const GradeTag({
    required this.key,
    required this.label,
    required this.preset,
  });

  factory GradeTag.fromJson(Map<String, dynamic> json) {
    return GradeTag(
      key: (json['key'] ?? '').toString(),
      label: (json['label'] ?? '').toString(),
      preset: json['preset'] == true,
    );
  }
}

/// 5 个预设 grade tag 的 fallback 列表。当 /grade-tags 端点请求失败时用这个
/// 兜底,保证过滤栏始终可用。
const List<GradeTag> kPresetGradeTags = [
  GradeTag(key: 'primary', label: '小学', preset: true),
  GradeTag(key: 'junior', label: '初中', preset: true),
  GradeTag(key: 'senior', label: '高中', preset: true),
  GradeTag(key: 'adult', label: '成人', preset: true),
  GradeTag(key: 'universal', label: '通用', preset: true),
];
