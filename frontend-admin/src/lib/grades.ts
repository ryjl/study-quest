// Grade tag system. 2026-07-20: grade 改开放 tag 体系,不再硬编码 1-9 年级。
// 2026-07-21: "成人" (adult) 替换为 "大学" (college) + "其它" (other)。
// "成人"在中文教育语境联想偏向成人教育/夜校,与产品定位(中小学 + 大学/进修)
// 不搭;新增 "其它" 作为进修/职场/考研等不归类人群的兜底。历史 DB 里的 "adult"
// 值会作为普通自定义 tag 回显,admin 可在 Grade 管理页一次性合并到 college/other。
//
// PRESET_GRADES is the default tags + 中文 labels the admin form shows.
// Historical "1"-"9" and "adult" aren't in the preset — if the DB already has
// those values, they echo back as custom chips. The admin can add any custom
// tag in the form (考研 / 职场 / 幼小衔接 / ...) or manage them centrally on
// the Grades page (CRUD + rename + merge + usage counts).
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

// GRADES 保留为 PRESET_GRADES 的别名,向后兼容老引用(避免大面积改名)。
// 新代码请直接用 PRESET_GRADES。
export const GRADES = PRESET_GRADES;

// gradeLabel 把单个 grade key 渲染成展示文字。
//   - 预设值(primary/junior/senior/adult/universal) → 中文 label
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
