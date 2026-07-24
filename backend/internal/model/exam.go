package model

import "time"

// 课程考试(TODO.md P0)。和 Quiz 完全平行,但 scope 是 (user, course) 而非 (user, episode):
// 一张考试卷综合某课程下多个 episode 的知识点,做"阶段性综合测评"。
//
// 题源(混合抽题,见 service.exam_selector + exam_service):
//   - 主体从题库抽(跨 episode、跨 quiz generation,按学生 mastery 弱点加权)
//   - 1-2 道由 quizzer agent 在课程级新出(测知识迁移能力)
// ExamQuestion.Source 区分两种来源,让交卷报告能对比"题库题 vs 新题"的正确率,
// 验证迁移题难度是否合理(若新题正确率显著低于题库题,说明太难或 agent 出题质量有问题)。
//
// 考试答案写**独立 ExamAnswer 表**,不污染 Answer(错题本聚合 / quiz 答题流水统计)。
// mastery feedback 走同一套 KnowledgeMemory.RecordAnswer(考试交卷也更新掌握度,
// 让 agent 下次出题能反映"阶段考试暴露的弱点")。

// Exam 是一份课程级考试卷。一个 (user, course) 同时只有一个 active exam
// (partial unique index,见 migrateExamActiveUniqueIndex)。
type Exam struct {
	ID         uint  `gorm:"primaryKey;autoIncrement"`
	UserID     uint  `gorm:"index;not null"`
	CourseID   uint  `gorm:"index;not null"`
	Status     string `gorm:"size:16;default:'active'"` // active | archived
	ArchivedAt *time.Time
	// SubmittedAt 交卷锁。nil = 仍在答题;交卷后盖戳,该 exam 锁定不可再改。
	// 用 TryMarkExamSubmitted 的条件 UPDATE 抢占(复用 quiz 交卷锁范式,消除 TOCTOU)。
	SubmittedAt *time.Time
	Score       float64 // 交卷时算的得分率 0.0-1.0(对题数/总题数)。未交卷为 0。
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// FK 关系(AI 附加层,单向):删 user/course 时 DB CASCADE 清本表。
	// Exam 是 ExamQuestion/ExamAnswer 的父表,各自 FK 指回,CASCADE 级联。
	User   User   `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	Course Course `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE" json:"-"`
}

// ExamQuestion 是考试卷里的一道题。指向被抽中的 Question(题库题或新生成题)。
type ExamQuestion struct {
	ID         uint `gorm:"primaryKey;autoIncrement"`
	ExamID     uint `gorm:"index;not null"`
	QuestionID uint `gorm:"index;not null"` // 指向被抽中的 questions 行
	ChunkID    uint `gorm:"index"`          // 冗余自 Question.ChunkID,聚合省 join
	// Source 区分题源:pool=从题库抽(学生做过的题),generated=quizzer agent 新出(测迁移)。
	// 交卷报告用它对比两类题的正确率,验证迁移题难度是否合理。
	Source   string `gorm:"size:16;default:'pool'"` // pool | generated
	OrderIdx int    // 卷面顺序(组卷时定,前端按此渲染)
	CreatedAt time.Time
	// FK:删 Exam 时 CASCADE 清本表。Question 关系不加 CASCADE——题被删时
	// ExamQuestion 保留(历史考试记录该有题面快照,通过 Service 层兜底"(题已删除)")。
	Exam     Exam     `gorm:"foreignKey:ExamID;constraint:OnDelete:CASCADE" json:"-"`
	Question Question `gorm:"foreignKey:QuestionID" json:"-"`
}

// ExamAnswer 是考试交卷的逐题作答。与 Answer 物理隔离,不污染错题本聚合。
type ExamAnswer struct {
	ID            uint `gorm:"primaryKey;autoIncrement"`
	ExamID        uint `gorm:"index;not null"`
	ExamQuestionID uint `gorm:"index;not null"`
	UserID        uint `gorm:"index;not null"`
	QuestionID    uint `gorm:"index;not null"` // 冗余自 ExamQuestion,聚合省 join
	ChunkID       uint `gorm:"index"`          // 冗余,mastery 更新用
	UserAnswer    int                          // choice: 索引; fill/multi: -1
	UserAnswerText string `gorm:"type:text"`   // fill 原文 / multi_choice: JSON []int 索引
	Correct       bool
	AnsweredAt    time.Time
	// FK:删 Exam 时 CASCADE 清本表。
	Exam        Exam        `gorm:"foreignKey:ExamID;constraint:OnDelete:CASCADE" json:"-"`
	ExamQuestion ExamQuestion `gorm:"foreignKey:ExamQuestionID;constraint:OnDelete:CASCADE" json:"-"`
	User        User        `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}
