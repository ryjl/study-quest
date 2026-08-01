// Grade tag system: grade 是开放 tag 体系(不硬编码 1-9 年级)。PRESET_GRADES 是
// admin 表单默认显示的 tag + 中文 label;历史 DB 里的 legacy 值("1"-"9"、"adult")
// 不在预设里,作为普通自定义 chip 回显,admin 可在 Grade 管理页 CRUD/合并/看用量。
// 自定义 tag(考研 / 职场 / 幼小衔接 / ...)任意加。
//
// Split out of types.ts so that file is pure TS interfaces (no constants /
// runtime helpers).

export const PRESET_GRADES: { key: string; name: string }[] = [
  { key: 'primary', name: '小学' },
  { key: 'junior', name: '初中' },
  { key: 'senior', name: '高中' },
  { key: 'college', name: '大学' },
  { key: 'other', name: '其它' },
  { key: 'universal', name: '通用' },
];

// gradeLabel 把单个 grade key 渲染成展示文字。
//   - 预设值(primary/junior/senior/college/other/universal) → 中文 label
//   - 纯数字(legacy "1"-"9") → "<n>年级"(向后兼容老 DB 数据)
//   - 其它(自定义 tag 如"考研") → 原样
export function gradeLabel(key: string): string {
  const preset = PRESET_GRADES.find((g) => g.key === key);
  if (preset) return preset.name;
  if (/^\d+$/.test(key)) return `${key}年级`;
  return key;
}

// gradeDisplay 把后端 grade 字段(逗号分隔的 key 列表)渲染成展示文字。
// universal 单独显示为"全学段通用";多个值用逗号连接各自的 gradeLabel。
export function gradeDisplay(grade: string): string {
  if (!grade) return '';
  const parts = grade.split(',').map((g) => g.trim()).filter((g) => g.length > 0);
  if (parts.length === 1 && parts[0] === 'universal') return '全学段通用';
  return parts.map(gradeLabel).join(', ');
}
