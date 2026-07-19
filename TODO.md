# TODO — StudyQuest 待办清单

> 本文件记录所有"已识别但未实现"的 feature idea，按优先级分组。每次迭代从这里挑选。
> 最后更新：2026-07-18（体验优化轮次：AI 富文本 + 字号配置 + Android TV 适配）

每条尽量写清四样东西：**场景**（解决什么用户问题）、**价值**（为什么值得做）、**工作量预估**（小/中/大）、**依赖**（前置条件 / 阻塞项）。

---

## 已完成（2026-07-18 体验优化轮次）

本轮交付（8 条用户反馈），代码已落地、`make test` + `flutter analyze` 全绿：

- **AI 富文本（表格 + SVG 图）**：前端 `MarkdownView`（`flutter_markdown` + `flutter_svg`），
  后端 `prompts.go` 三处 prompt 鼓励约束式 SVG（只允许简单流程图/柱状图）+ GFM 表格。
  AI 页 summary / advice / quiz 的 stem / explanation 全部支持 markdown 渲染。
- **字幕按钮重复 bug**：`_nativeSubtitleIds` 改 Set 去重 + `_getSubtitleOptions` label 层去重。
- **全局字号配置**：`UiPrefs`（SharedPreferences）—— 字幕字号（小中大超大）和 AI 页字号
  （紧凑/标准/大/超大）各自独立，改一次全局沿用。AI 页右上角加字号调整按钮。
- **跳转 AI 未暂停**：helper panel 常驻 AI 入口补 `_player.pause()`。
- **Android TV 适配**：`TvMode` 检测 + 设置页「预览 TV 模式」调试开关（MuMu 可验证）；
  搜索框 D-pad 焦点陷阱修复；播放器 TV 下 seek ±30s + helper panel 默认展开；
  AI 页 TV 下隐藏 quiz、字号强制大。

---

## P0 — 高价值，建议下一轮做

### 错题本

学生做错的题自动归集到一个"错题本"，按科目 / 课程 / 知识点（chunk）分类，支持重做、标记"已掌握"。

- **场景**：当前学生交卷后能看到本次 quiz 的逐题对错，但错题做完就散落在各次 quiz 历史里，没有"我一直在哪里犯错"的聚合视图。错题本是中小学教辅的标准闭环，孩子和家长都习惯这种复习方式。
- **价值**：补上"复习闭环"。AI 出题已经积累了 per-user 的 `Answer` 流水 + `KnowledgeMemory` 弱点 mastery，错题本是这些数据最自然的消费端——让 AI 数据从"展示"变成"驱动行动"。
- **工作量预估**：中。
  - 后端：新表 `WrongBookEntry`（或纯视图聚合 `Answer WHERE correct=false`），按 `(user_id, question_id)` 去重，加 `mastered` 标记 + `last_attempted_at`；几个查询 API（按科目/课程/知识点列错题、重做提交、标记掌握）。
  - 前端：新列表页（错题本入口）+ 重做流（复用现有 quiz 渲染组件）+ "标记掌握"操作。
- **依赖**：`questions` / `answers` 表已有数据，可直接聚合（无需新基建）。`KnowledgeMemory` 已有 chunk_id 可做知识点维度聚合。建议和"课程考试按钮"一起规划，两者共用一部分题库检索逻辑。

### 课程考试按钮

课程页一个"考试"按钮：把这门课全部学过的知识点综合出一张**有针对性**的试卷（基于 mastery 弱点抽题，不重新生成而是从已有题库抽）。

- **场景**：当前每个 episode 独立出题、独立交卷，没有"阶段性综合测评"。学生学完一门课（或一个章节）后，想检验整体掌握程度，只能靠记忆里的散落印象。课程级 exam 提供"阶段性考试"体验。
- **价值**：补上"阶段测评"。复用已有题库（每个 episode 多次 quiz 后积累的 questions）做抽题，边际成本极低（不出新题、不跑 LLM），但给家长/学生一个清晰的"这门课学到什么程度"信号。
- **工作量预估**：中偏大。
  - 后端：抽题算法（按 mastery 弱点优先抽 + 覆盖该 course 下多个 episode/chunk + 题型均衡 + 题量控制），新表 `Exam`（或复用 `Quiz` 加 scope 字段），交卷报告（跨 episode 的 mastery 变化 + 弱点分布）。
  - 前端：课程页加"考试"按钮 + 试卷渲染（复用 quiz 组件）+ 交卷报告页（图表化展示）。
- **依赖**：题库数据量要足够（每个知识点有 2-3 道可抽的题），即学生在多个 episode 做过 quiz 后才有意义。新课程/新课时不适合开考试。和错题本共用题库检索层。

---

## P1 — 中等价值

### chat 多轮对话能力（原 Phase D）

学生在 AI tab 里和 agent 多轮对话讨论一节课，答案带视频时间戳跳转（"你说的通分在 12:38 讲过"）。

- **场景**：当前 AI 能力是单向的——出题、总结、建议，没有"学生提问"的入口。chat 补上"问答"维度。
- **价值**：从"被动接收 AI 内容"升级到"主动提问"。复用 Phase C 的 RAG（content_chunks）+ memory。
- **工作量预估**：大。`ChatSession`/`ChatMessage` 表骨架已建（字段未定，按当时需求加列，零风险）；agent 逻辑、流式 UX、上下文窗口管理都要从头做。
- **依赖**：无硬依赖，但建议 streaming（见下）先做以改善等待体验。

### memory 衰减曲线（艾宾浩斯）

`KnowledgeMemory.mastery` 当前是单调累积（答对 +0.1 / 答错 -0.2），不随时间衰减。加艾宾浩斯遗忘曲线，让长时间没复习的知识点 mastery 自然回落，触发 agent 重新出题巩固。

- **场景**：学生一个月前答对的"通分"现在可能已经忘了，但 mastery 还是 0.9，agent 不会再出这题。衰减让"过时掌握"自动浮现。
- **价值**：让系统更接近真实学习规律，自适应更准。
- **工作量预估**：小到中。`KnowledgeMemory` 加 `decay_*` 列（加列，零风险），读时算衰减、不需要后台 cron。
- **依赖**：无。属于 §18 forward-safe 表里的"加列"项。

### streaming 输出（SSE）

agent 跑完一次性返回 → 改成 SSE 流式输出，改善等待体验。

- **场景**：quiz/advice 生成几十秒，学生盯着"generating..."转圈。
- **价值**：体验提升，尤其是 chat（每条消息等几秒太长）。
- **工作量预估**：中。OpenAI 兼容协议支持 stream，后端 SSE pipe + 前端 StreamBuilder。
- **依赖**：建议和 chat 一起做。

### 知识点命名标准化（chunk 打标签 / 本体库）

目前 agent 靠 LLM 推理关联 `chunk.text` 描述知识点（advice 之所以能说"通分掌握不好"，是因为工具把 chunk.text 注入了 observation）。未来给 chunk 打标签 / 建本体库，让知识点有稳定 ID 而非靠文本相似。

- **场景**：当前 chunk 是文本切片，"通分"在不同课里是不同 chunk_id，agent 靠文本语义关联。本体库让"通分"有一个稳定全局 ID，跨课程聚合更准。
- **价值**：跨课程的知识点 mastery 聚合、错题本的知识点维度、课程考试的抽题覆盖都能更准。
- **工作量预估**：中偏大。本体库 schema + admin 标注工具 + chunk 自动匹配逻辑。
- **依赖**：无硬依赖，但建议错题本/课程考试上线后再做（有了应用场景才好定本体粒度）。

### admin 批量预热出题

`EnqueueQuiz` 结构已预留，本次不接路由（懒加载够用，省钱符合纯附加层）。未来 admin 可一键给某课程所有 episode 批量预热出题，学生进 AI 页直接 ready 不用等。

- **场景**：新课程上线时，第一批学生进 AI 页都要等几十秒生成。批量预热让首屏 ready。
- **价值**：体验提升，但和"省钱"原则有张力——预热会跑很多可能没人看的题。建议做成 admin 显式触发，不默认开。
- **工作量预估**：小。结构已预留，接路由 + admin UI 按钮即可。
- **依赖**：无。

### 附件提取入 content_chunks（PDF / 练习册）

视频配套 PDF/练习册提取后也进 `content_chunks` 表（`source_type=attachment`），与字幕统一检索。

- **场景**：当前 RAG 语料只有字幕，没覆盖课程附件。题目/建议只能基于"老师说的"，不能基于"练习册上的题"。
- **价值**：扩展 RAG 语料，尤其理科（练习册题型丰富）。
- **工作量预估**：中。PDF 文本提取（已有附件字段 `AttachmentJSON`，加提取 worker）+ chunk 切分（复用 segmenter 策略，无时间索引）。schema 已预留（`source_type` 加 `attachment` 值，加枚举值零风险）。
- **依赖**：附件上传链路已就绪。

### rerank（数据量增大后）

`AIProvider.Capability` 加 `rerank` 值（接口已预留），数据量增大后上 rerank API 提升检索精度。

- **场景**：当前 embedding 检索是 brute-force cosine（per-episode 几百 chunk 够用）。课程数/附件多了之后召回噪声变大。
- **价值**：检索精度提升，间接提升 quiz/advice 质量。
- **工作量预估**：小到中。resolver 加一个 case + 调用点加 rerank pass。
- **依赖**：数据量足够大才值得（当前规模无收益）。

---

## P2 — 远期 / 探索性

### 判断题（judge 题型）

对/错二选一，必须配解析。Scoring 列已支持（`{"correct":true}`），`Question.Type` 加 `judge` 枚举值即可。

- **场景**：快速概念抽查，"对还是错"的一问一答。
- **价值**：题型丰富度。但 50% 蒙中率，不适合做核心检测，适合做课前的快速 warm-up 或课后的概念校准。
- **工作量预估**：小。数据结构已就位（Scoring），主要是 prompt 让 agent 出 judge 题 + 前端加对/错按钮 UI。
- **依赖**：无。

### 排序 / 配对题（order / match 题型）

知识点序列化考察，如"把以下步骤按正确顺序排列"、"把朝代和事件配对"。Scoring 列承载 `{"correct_order":[3,1,2,4]}` 之类判分元数据。

- **场景**：考"过程类"知识（历史顺序、实验步骤、算法流程），比选择/填空更能检验结构性理解。
- **价值**：题型丰富度，覆盖一类当前题型考不好的知识点。
- **工作量预估**：中。prompt + grading + 前端拖拽 UI（拖拽 UX 在 PAD 上要打磨）。
- **依赖**：无。

### 简答题（short_answer 题型）

LLM 判分，质量最高但成本/延迟/不确定性大。Scoring 列承载 `{"rubric":"...","keywords":[...]}`，需要 rubric + 半对评分 + 重判逻辑。

- **场景**：考"表述类"知识（作文思路、解题说理），选择/填空考不好。
- **价值**：题型丰富度的天花板，但 LLM 判分的不确定性（同一答案两次判分可能不同）是真实风险。
- **工作量预估**：大。LLM 判分 pipeline + rubric 设计 + 半对评分规则 + 重判逻辑（学生申诉时） + 前端主观题输入 UX。
- **依赖**：建议先有足够的 quiz 历史数据评估 LLM 判分稳定性再上线。streaming 输出先做以改善等待体验（简答题判分慢）。

---

## 已识别的技术债

### AIHint 旧字段清理

`Course.AIHint` 在质量优化轮次收进单 JSON 列 `AIConfigJSON` 后已 deprecated，`EffectiveWhisperHint()` / `EffectiveQuizHint()` 方法从 JSON 解析、JSON 字段空时回退到老 `AIHint` 列。同理 `Question.Answer` / `Question.AnswerText` 被 `Scoring` 取代后也 deprecated。

- **要做什么**：deprecated 一轮（确认所有线上课程都重新保存过、所有 question 都有 Scoring）后，删除 `Course.AIHint` 列 + `Question.Answer`/`Question.AnswerText` 列 + `Effective*` 回退逻辑（回退去掉后 `Effective*` 直接返回 `AIConfig()` 的字段即可）。
- **阻塞项**：删列属于"升级困难"类（SQLite DROP COLUMN 有版本/约束限制），需要等下次数据清零或写显式迁移脚本。不能在 AutoMigrate 里直接删。
- **触发条件**：admin 端观测无 course 还在用 AIHint（Effective* 回退路径连续一段时间无命中）。

### AIConfig 扩展新配置项（JSON 化的收益兑现）

`Course.AIConfigJSON` 单 JSON 列设计成前向兼容——加新配置项不必改 schema。当出现新需求时（举例如下），只需：扩 `model.AIConfig` struct 加字段 → admin 表单加输入 → service 层 `SetAIConfig` 自然带上 → 消费方按需读。

- **难度系数**：`DifficultyBias string`（easy/medium/hard），出题 prompt 读它调难度配比。
- **题型配比**：`QuestionTypeMix map[string]float64`（如 `{"fill":0.5,"choice":0.3,"multi_choice":0.2}`），覆盖 prompt 默认题型倾向。
- **语言偏好**：`Language string`，summary/quiz 输出语言（双语课程可能要英文输出）。
- **禁用术语纠错**：`DisableTermCorrection bool`，某些课程字幕准确不需要纠错时关掉。

这些都是**代码-only 改动**，零 schema 迁移——这就是 JSON 化的核心收益。

### 多选题 mastery 加权数值微调

当前 multi_choice grading 三态的 mastery 增量是固定数值：全对 +0.1 / 部分对 +0.1（质量优化轮次改成视为掌握、不扣分，避免压低 mastery 误导 advice）/ 错 -0.2。

- **要做什么**：`RecordAnswer` 当前只支持 correct=true/false，部分对借用 true 是粗糙折中。可加 partial 参数或 Score 参数，让部分对按"勾中正确项数 / 正确项总数"给比例分（如勾中 2/3 给 +0.07），全对才 +0.1。
- **价值**：mastery 更精确反映掌握程度。当前部分对一律 +0.1，让"勾中 1 个"和"勾中 3 个里的 2 个"同等对待，且和全等同权。
- **工作量预估**：小。`RecordAnswer` 签名扩 partial/Score + UpsertMemoryOnAnswer 的增量公式调 + 单测调阈值。
- **触发条件**：多选题上线后观察一段时间，确认加权不会让 mastery 抖动过大。

### advice 重算策略细化

当前 `UpsertAdvice` 每次交卷都覆盖（episode 级 advice 在每次 submit-all 后链式触发重算）。如果学生一节课做了 5 次 quiz，advice 会被重算 5 次——每次都烧 token，但内容可能没实质变化（mastery 没动多少）。

- **要做什么**：加节流——"上次建议后答了 N 题才重算"或"mastery 变化超过阈值才重算"。
- **价值**：省 token（advice 是低优先级 job，但积少成多）。
- **工作量预估**：小。service 层加重算前检查（对比 `MasterySnapshotJSON` 和当前 mastery 的 diff）。
- **触发条件**：观察到 advice 重算频率过高 / token 账单上涨。
