package model

import "time"

// 课后作业卷(Homework)。和 Quiz/Exam 平行,但定位不同:
//   - Quiz = (user, episode) 级,屏上小测,AI 个性化出题(读 mastery)
//   - Exam = (user, course) 级,屏上综合考,纯题库抽题(不跑 LLM)
//   - Homework = episode 级、不绑 user(通用卷)、AI 单次生成、纯打印纸笔做、家长手批
//
// 关键差异:作业是"通用教具",一份卷子谁打印都一样,所以不绑 user,也不需要 Answer
// 表(纯打印没人提交)。一个 episode 同时只有一份 active 作业(partial unique index,
// 同 quiz/exam 范式),重生成时旧卷 archived 保留历史。
//
// 题源:AI 单次 LLM 调用(不走 ReAct agent loop,作业不读 mastery、不个性化)。素材 =
// 当前 episode 的 ContentChunk 为主 + 当前 course 其他 episode 的 chunk 为辅(复习题),
// 由 service 层代码层 RAG 检索后塞进 prompt(代替 quiz 的 agent 主动检索)。
//
// 题型:8 种。choice/multi_choice/fill 复用现有(格式对齐 Question 表的 Scoring JSON
// 设计);short_answer/calculation/copy_word/dictation/translation 是作业特有(存参考
// 答案 reference 给家长对照,不判分)。judge 并入 choice(两个选项的单选);order/
// board_state 标 future(Type 是开放 string,加新题型不改表)。

// Homework 是一份 episode 级的通用作业卷。一个 episode 同时只有一个 active homework
// (partial unique index,见 migrateHomeworkActiveUniqueIndex)。
type Homework struct {
	ID         uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	EpisodeID  uint   `gorm:"index;not null" json:"episode_id"`
	CourseID   uint   `gorm:"index;not null" json:"course_id"` // 冗余自 Episode.CourseID,聚合/列表省 join
	// Version 每次重生成 +1。无旧卷时首版 = 1;有旧卷(被 archived)时新卷 = 旧 Version + 1。
	// 由 service 层在 CreateHomework 前算好填入(repo 不自增,保持 repo 无状态)。
	Version   int    `gorm:"default:1" json:"version"`
	Status    string `gorm:"size:16;default:'active'" json:"status"` // active | archived
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	// AgentMetaJSON 存生成元数据(题型分布、self-check 结果摘要等),供 admin 观测。
	// 不是契约字段(前端按需读),结构可演进。
	AgentMetaJSON string `gorm:"type:text" json:"agent_meta_json,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"-"`
	// FK(AI 附加层,单向):删 Episode 时 DB CASCADE 清本表(CourseID 冗余,不加 Course FK
	// 避免 episode 改课程时出现孤儿;Course 删会级联清 Episode,再级联清本表,够用)。
	Episode Episode `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE" json:"-"`
}

// HomeworkSection 是作业卷的一个大题分组(一、选择题 / 二、计算题 / …)。
// 保证卷子的整体感:LLM 按 section 组织输出,渲染层按 section 统一排版。
//
// PassageTitle/PassageContent 用于阅读理解类大题:该 section 顶部先出一段材料,
// 再出该 section 下的题。普通大题这两个字段为 NULL。一期一段材料够用;多段材料
// (正式题组结构)留 future。
type HomeworkSection struct {
	ID             uint `gorm:"primaryKey;autoIncrement"`
	HomeworkID     uint `gorm:"index;not null"`
	Seq            int  // 大题序号 1,2,3…,渲染按此排序
	Title          string
	PassageTitle   *string `gorm:"type:text"` // nullable;阅读理解大题的材料标题
	PassageContent *string `gorm:"type:text"` // nullable;阅读理解材料正文
	CreatedAt      time.Time
	// FK:删 Homework 时 CASCADE 清本表。
	Homework Homework `gorm:"foreignKey:HomeworkID;constraint:OnDelete:CASCADE" json:"-"`
}

// HomeworkQuestion 是作业卷里的一道题。字段名/格式对齐 Question 表(Options/Scoring
// 用 type:text 存 JSON),但**不复用** Question 表——作业题和这份作业强绑定,不是
// 跨 quiz/exam 共享的题库题。复用 Question 表会让 Question.QuizID 必填字段语义混乱,
// 也会继承 exam 那种"题被删兜底"的复杂度(见 exam.go ExamQuestion 注释)。
//
// Type 是开放 string,8 种:
//   - choice:        Scoring {"correct_index":2}
//   - multi_choice:  Scoring {"correct_indices":[0,2]}(作业不要 partial_credit,打印用不上)
//   - fill:          Scoring {"accept":["12","十二"]}(作业可多答案,比 quiz 宽松)
//   - short_answer:  Scoring {"reference":"要点:…"}
//   - calculation:   Scoring {"reference":"解:3×4=12"}
//   - copy_word:     Scoring {"content":"beautiful","times":3}
//   - dictation:     Scoring {"reference":"参考文本"}
//   - translation:   Scoring {"reference":"参考译文"}
//
// Explanation 是答案解析,只在答案版(预览页勾选"显示答案")显示。
type HomeworkQuestion struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	HomeworkID uint   `gorm:"index;not null"`
	SectionID  uint   `gorm:"index;not null"`
	Seq        int    // 题号(section 内或全局,由 service 决定),渲染按此排序
	Type       string `gorm:"size:24"` // 开放 string,见上方 8 种
	Stem       string `gorm:"type:text;not null"`
	Options    string `gorm:"type:text"` // JSON []string(choice/multi_choice 用,同 Question.Options)
	Scoring    string `gorm:"type:text"` // 各题型判分/参考 JSON,按 type 解析(同 Question.Scoring)
	Explanation string `gorm:"type:text"`
	CreatedAt  time.Time
	// FK:删 Homework/Section 时 CASCADE 清本表。
	Homework Homework         `gorm:"foreignKey:HomeworkID;constraint:OnDelete:CASCADE" json:"-"`
	Section  HomeworkSection  `gorm:"foreignKey:SectionID;constraint:OnDelete:CASCADE" json:"-"`
}

// HomeworkPromptConfig 存每个 subject 一份**完整 system prompt**(C 方案:admin 可编辑)。
// 和 AIConfig.HomeworkHint(短 hint)语义不同——这里是作业生成的完整系统提示词,
// 含科目配方(题型白/黑名单、题量区间)。首次访问某 subject 时 lazy 创建(repo 的
// GetOrCreatePromptConfig 用 defaultHomeworkPrompt(subject) 灌默认),admin 编辑后
// UPDATE 覆盖;"恢复默认" = UPDATE 回 defaultHomeworkPrompt。
//
// SystemPrompt NOT NULL 避免 NULL 三态(lazy 创建时一次性灌满,不存在"有行无内容")。
type HomeworkPromptConfig struct {
	ID           uint   `gorm:"primaryKey;autoIncrement"`
	SubjectID    uint   `gorm:"uniqueIndex;not null"`
	SystemPrompt string `gorm:"type:text;not null"` // 完整 system prompt,首次 lazy 灌默认
	UpdatedAt    time.Time
	// FK(AI 附加层,单向):删 Subject 时 CASCADE 清本表。
	Subject Subject `gorm:"foreignKey:SubjectID;constraint:OnDelete:CASCADE" json:"-"`
}
