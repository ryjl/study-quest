package model

import "time"

// WrongBookItem 是错题本里的一条「错题状态」记录。
//
// 设计要点(见 docs/modules/ai/ 与 TODO.md「错题本」):
//   - 题面永远现查(Question 表),不冗余拷贝——本表只存 curation 状态。这样题目
//     被 regenerate 替换后,错题本引用的 QuestionID 仍指向真实题面;而如果题面真
//     的被删(Question 跟随 Quiz 生命周期),错题本行也该清理(见 FK CASCADE)。
//   - 冗余 CourseID/EpisodeID/SubjectID/ChunkID 是刻意的,遵循 Answer.QuizID、
//     ContentChunk.CourseID 的既定模式:错题本列表要按科目/课程/知识点过滤聚合,
//     冗余这些 ID 让查询一次 WHERE 就够,不必 JOIN 多表。这些值在 upsert 时从
//     Answer→Quiz→Course→Subject 的 join 链快照下来,后续 course 改科目不会回溯
//     更新(可接受:错题本是学生的练习流水,不是课程元数据的从表)。
//   - FirstWrongAt 记首次做错时间,AttemptCount 记重做次数——前端能展示"这道题
//     你错了 N 次、多久了",帮学生识别顽固错题。重做正确后 Mastered 置 true。
//
// 维护点:交卷时(SubmitAllQuizAnswers)对每道 correct=false 的题 upsert 本表
// (新建则 FirstWrongAt/AttemptCount=1;已存在则 AttemptCount++)。重做流
// (WrongBook Redo)答对→Mastered=true;答错→AttemptCount++。
type WrongBookItem struct {
	ID              uint `gorm:"primaryKey;autoIncrement"`
	UserID          uint `gorm:"uniqueIndex:idx_wb_user_q;index;not null"`
	QuestionID      uint `gorm:"uniqueIndex:idx_wb_user_q;index;not null"`
	// 冗余上下文(见上文说明)。
	ChunkID         uint `gorm:"index"`
	CourseID        uint `gorm:"index"`
	EpisodeID       uint `gorm:"index"`
	SubjectID       uint `gorm:"index"`
	FirstWrongAt    time.Time
	LastAttemptedAt *time.Time
	AttemptCount    int `gorm:"default:1"`
	// CorrectStreak 是重做时的连续答对次数。重做答对 → streak++(达阈值 mastered);
	// 答错 → streak 清零。阈值见 wrongBookMasteredThreshold。让"掌握"不是对一次就清,
	// 而是连对几次才算真掌握,避免蒙对就清除。
	CorrectStreak   int  `gorm:"default:0"`
	Mastered        bool `gorm:"default:false"`
	MasteredAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// FK 关系(AI 附加层,单向):删 user/question 时 DB CASCADE 清本表。Question 是
	// quiz 的从表(OnDelete:CASCADE),所以 regenerate 删 quiz 会级联删 question 会再
	// 级联清这里——错题本自动不留指向已删题的孤儿行。
	User    User     `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	Question Question `gorm:"foreignKey:QuestionID;constraint:OnDelete:CASCADE" json:"-"`
}
