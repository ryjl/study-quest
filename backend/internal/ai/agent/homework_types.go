package agent

// homework_types.go 定义作业卷(Homework)生成产物的纯数据结构。这些是 LLM 生成
// JSON 解析后的草稿,由 homework_parse.go 的 ParseHomeworkGeneration 产出,主 session
// 集成时把它们落库成 model.HomeworkSection / model.HomeworkQuestion(以及处理 SectionSeq
// → SectionID 的翻译)。
//
// 设计意图:解析层只做"逐题校验 + 残题丢弃",不碰任何 service / repo / model 写入。
// 这样 service 层拿到的就是一个干净的 HomeworkDraft,直接走 persist 路径即可。

// HomeworkDraft 是一次作业生成的完整结果:大题(section)和题(question)分开存放
// (题通过 SectionSeq 引用回 section)。主 session 集成时:
//   - 先按 Sections 顺序插入 section 行,拿到 SectionID;
//   - 再把每题的 SectionSeq 翻译成 SectionID,插入 question 行。
type HomeworkDraft struct {
	Sections  []HomeworkDraftSection
	Questions []HomeworkDraftQuestion
}

// HomeworkDraftSection 是一道大题(如"一、选择题""二、阅读理解")。阅读理解类大题会
// 带 passage_title + passage_content(一段和本课主题相关的阅读材料),其余大题这两字段
// 为 nil。
type HomeworkDraftSection struct {
	Seq            int
	Title          string
	PassageTitle   *string
	PassageContent *string
}

// HomeworkDraftQuestion 是一道小题。SectionSeq 引用所属 section 的 seq(在 section 内
// 通过 seq 排序)。Scoring 是按 type 校验后重新序列化的规范 JSON 字符串(已剔除 LLM
// 可能多写的冗余字段),由 service 层原样落库、grading 层按 type 解析。
type HomeworkDraftQuestion struct {
	SectionSeq  int   // 引用 section 的 seq;主 session 集成时翻译成 SectionID
	Seq         int   // section 内的题号
	Type        string // choice | multi_choice | fill | short_answer | calculation | copy_word | dictation | translation
	Stem        string
	Options     []string // choice/multi_choice 用,其他题型为空
	Scoring     string   // 原样存(JSON 字符串),由各题型 schema 校验合法性;校验后已规范化
	Explanation string
}
