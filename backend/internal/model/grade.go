package model

import "strings"

// Code split from models.go for navigability. See models.go for the
// package overview.

// Grade 是课程/阅读资源的适用人群 tag。历史上是 1-9 年级硬编码 enum,2026-07-20
// 改成开放 tag 体系:GradeValid 不再校验具体值,admin 可以填任意自定义 tag
// (如 "小学""初中""成人""考研""职场")。
//
// 推荐使用语义化预设常量 GradePrimary/Junior/Senior/Adult/Universal,admin
// 表单默认显示这 5 个 + 允许自定义补充。历史 Grade1-9 常量已删除,DB 中遗留
// 的 "1"-"9" 字面值会作为普通自定义 tag 回显(Grade 本身是 string,不受影响)。
type Grade string

const (
	// 2026-07-20 新预设(推荐值)。这 5 个 + admin 自定义组成实际可用 tag 集。
	GradePrimary   Grade = "primary"   // 小学
	GradeJunior    Grade = "junior"    // 初中
	GradeSenior    Grade = "senior"    // 高中
	GradeAdult     Grade = "adult"     // 成人
	GradeUniversal Grade = "universal" // 通用(匹配任何过滤)
)

// PresetGrades 是 admin 表单默认显示的预设 tag(顺序即展示顺序)。
// 历史 Grade1-9 常量已删除:DB 里若有 "1"-"9" 字面值会以"自定义 tag"形式
// 回显在 admin 表单上,可删可改(Grade 类型本身是 string,存储/读取不受影响)。
var PresetGrades = []Grade{GradePrimary, GradeJunior, GradeSenior, GradeAdult, GradeUniversal}

// PresetGradeLabel 返回预设 grade 的中文 label。非预设值(自定义 tag)返回空串
// —— 调用方应该 fallback 到原样展示。
func PresetGradeLabel(g Grade) string {
	switch g {
	case GradePrimary:
		return "小学"
	case GradeJunior:
		return "初中"
	case GradeSenior:
		return "高中"
	case GradeAdult:
		return "成人"
	case GradeUniversal:
		return "通用"
	}
	return ""
}

// Valid 报告 grade 值是否合法。2026-07-20 改成开放 tag 后,任何非空 trim 字符串
// 都合法 —— 不再限制具体枚举值。空串返回 false(调用方 parseGrades 会把空串
// 当作"未指定" → 默认 Universal)。
func (g Grade) Valid() bool {
	return strings.TrimSpace(string(g)) != ""
}

// ContentType distinguishes learning content (counts towards watch time,
// points, badges) from entertainment content (pure playback, no learning
// stats). Stored on Course so the progress service can branch at the choke
// point without touching the 11+ learning-stat queries.
type ContentType string

const (
	ContentLearning      ContentType = "learning"
	ContentEntertainment ContentType = "entertainment"
)

func (c ContentType) Valid() bool {
	return c == ContentLearning || c == ContentEntertainment
}

// SubjectCategory 标记 Subject 是学术学科还是娱乐子类。
//   - academic: 语数英象棋物理化学等(配合 ContentType=learning)
//   - entertainment: 动画片/电影/纪录片/综艺等(配合 ContentType=entertainment)
//
// 注意:Category 只是 UI 分组/过滤标签,真正的"是否计时长/badge/AI"开关仍是
// Course.ContentType。Category 和 ContentType 应该一致(学术 subject 配 learning
// content,娱乐 subject 配 entertainment content),这个一致性由 admin UI 和
// import_service 维持,不在 DB 层强制。
type SubjectCategory string

const (
	SubjectCategoryAcademic      SubjectCategory = "academic"
	SubjectCategoryEntertainment SubjectCategory = "entertainment"
)

// Subject represents a user-editable course subject (科目), e.g. 语文/数学/英语.
// Stored as its own table so it can be renamed or deleted independently of
// courses. `Key` is the stable identifier referenced by badge rules AND the
// lookup key the frontend uses to resolve a display icon (admin: key→lucide;
// Flutter: key→Material IconData — see subjectIcon.tsx / subject_icon.dart).
// Label / Color carry the remaining display metadata. The icon is NOT stored
// here: both clients map the key to a line icon at render time, which keeps
// the visual language consistent and cross-platform.
//
// IsSystem marks rows seeded by SeedDefaultSubjects: they can be renamed or
// recolored but never deleted (so the catalog always retains the canonical
// core subjects). User-created rows have IsSystem=false and are freely
