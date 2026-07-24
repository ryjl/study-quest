# TODO — StudyQuest 待办清单

> 本文件记录所有"已识别但未实现"的 feature idea 和技术债，按优先级分组。
> 已完成的功能不在此记录（git log 是完成历史）；本文件只面向未来工作。
>
> 每条尽量写清四样东西：**场景**（解决什么用户问题）、**价值**（为什么值得做）、
> **工作量预估**（小/中/大）、**依赖**（前置条件 / 阻塞项）。
>
> 最后更新：2026-07-23（错题本 + 阶段 0 共享抽题层 + 课程考试均已落地）

## P0 — 高价值，建议下一轮做

### ✓ 错题本 —— 已完成（2026-07-23）

已落地：新表 `wrong_book_items`（curation 状态：mastered/attempt_count/first_wrong_at + 冗余 course/episode/subject/chunk id 省聚合 join）+ 交卷 hook（`SubmitAllQuizAnswers` 对 `correct=false` 的题 upsert，部分对不算错）+ `WrongBookRepository`（upsert/master/list + admin 聚合 Stats/TopFrequent/Distribution）+ `wrong_book_service.go`（列表题面+curation 合并、重做流、mastered）+ 5 个 client 端点（list/master/unmaster/redo/redo-submit）+ admin 观测端点 `/admin/api/wrong-book/stats` + `/admin/wrong-book` 页（StatCard + 高频错题榜 + 科目弱点分布）+ Flutter 错题本屏（响应式 GridView、PAD maxWidth、TV gate）+ 重做屏（复用 quiz 渲染、ConstrainedBox 800 可读性）。

题面永远现查 `questions` 表（不冗余拷贝）；重做**不**落 `answers` 行、**不**改 quiz-side mastery（隔离性，见 `docs/modules/ai/overview.md` §4/§6）。阶段 0 抽出的共享题库检索层（`question_pool_repo.go` + `exam_selector.go`）为课程考试铺好了路。UT 覆盖：repo 14 + selector 11 + service/hook 10 + 集成 5 + admin api 3 + flutter model/api 12 + flutter 屏幕级 widget 6。

**附带改的一致判定**：multi_choice 漏选（部分对）从"视为掌握"改成"按错处理"，mastery / 错题本 / 显示对错三处统一为"漏选就是错"（见下方技术债章节的 2026-07-23 说明）。

**体验优化（2026-07-23 同日）**：把错题本从"功能存在"打磨成完整学习闭环。后端：① 列表响应带正确答案 + 解析（`correct_index`/`correct_text`/`correct_indices`，从 `scoring` 派生）+ `unmastered_count`（tab 角标）；② 掌握机制从"重做对 1 次就清除"改成**连对 3 次**才掌握（`correct_streak` 字段 + `IncrementCorrectStreak`，答错清零）。前端：③ Tab 角标显示未掌握数 + 图标改 `spellcheck` 区分阅读室；④ 列表默认「全部」+ 课程过滤，已掌握灰显不消失；⑤ 卡片「查看答案」收起展开（正确答案绿色高亮 + 解析），不再只有裸题面；⑥ 掌握切换改成明确文字按钮（不再用隐晦圆圈误触）；⑦ 单题重做（每卡「重做本题」）+ 整批重做共存；⑧ 顶部进度「共 N 题 · 已掌握 M」；⑨ 空状态引导去学习；⑩ quiz/考试交卷后提示「N 道错题已加入，去复习」。UT 覆盖：repo +3（streak）、service +1（连对3次）、flutter model +4（答案/streak/list）、widget +3（默认全部/答案展开/单题重做）。

<details>
<summary>原条目（保留供参考）</summary>

学生做错的题自动归集到一个"错题本"，按科目 / 课程 / 知识点（chunk）分类，支持重做、标记"已掌握"。

- **场景**：当前学生交卷后能看到本次 quiz 的逐题对错，但错题做完就散落在各次 quiz 历史里，没有"我一直在哪里犯错"的聚合视图。错题本是中小学教辅的标准闭环，孩子和家长都习惯这种复习方式。
- **价值**：补上"复习闭环"。AI 出题已经积累了 per-user 的 `Answer` 流水 + `KnowledgeMemory` 弱点 mastery，错题本是这些数据最自然的消费端——让 AI 数据从"展示"变成"驱动行动"。
- **工作量预估**：中。
  - 后端：新表 `WrongBookEntry`（或纯视图聚合 `Answer WHERE correct=false`），按 `(user_id, question_id)` 去重，加 `mastered` 标记 + `last_attempted_at`；几个查询 API（按科目/课程/知识点列错题、重做提交、标记掌握）。
  - 前端：新列表页（错题本入口）+ 重做流（复用现有 quiz 渲染组件）+ "标记掌握"操作。
- **依赖**：`questions` / `answers` 表已有数据，可直接聚合（无需新基建）。`KnowledgeMemory` 已有 chunk_id 可做知识点维度聚合。建议和"课程考试按钮"一起规划，两者共用一部分题库检索逻辑。

</details>

### ✓ 课程考试 —— 已完成（2026-07-23）

已落地：课程详情页 hero 加"课程考试"入口 → 课程考试屏（status gate / 开考 / 答题 / 交卷 / 阅卷报告）。后端三表 `exams` / `exam_questions` / `exam_answers`（和 Quiz 平行，scope 是 `(user, course)`，partial unique index 保 (user,course) 同时只有一个 active）+ `exam_repo.go`（archive-then-insert 组卷、`TryMarkExamSubmitted` 交卷锁、`ExamStats`/`ExamSourceQuality` 观测聚合）+ `exam_service.go`（`StartExam` gate→抽题→组卷、`SubmitExam` 交卷锁→逐题判分→写 ExamAnswer→更新 mastery→算 Score、4 个 DTO）+ `exam_handler.go`（4 client 端点）+ admin 观测端点 `/admin/api/exam/stats`（考试总数/已交卷/平均得分率/题源质量对比）+ `/admin/exam` 页（StatCard + pool vs generated 正确率横条）+ Flutter 考试屏（PAD maxWidth 800、复用 quiz 渲染、TV gate 隐藏做题体）。

题源当前是**纯题库抽**（`SelectExamQuestions`，阶段 0 的 `question_pool_repo.go` + `exam_selector.go`：按 mastery 弱点加权 + 覆盖度约束 + 题型轮转 + 降级，从已有题库跨 episode 抽，不跑 LLM）。`source` 字段预留 `'generated'`，quizzer agent 出迁移题作为后续增强。答案写独立 `exam_answers` 表（不污染 `answers`），mastery 走同一套 `KnowledgeMemory.RecordAnswer`（考试交卷也更新掌握度）。漏选按"错"处理，和 quiz / 错题本同口径。交卷锁复用条件 UPDATE 范式（消除 TOCTOU）。UT 覆盖：repo 7 + selector 11 + service 8 + 集成 4 + admin api 3 + flutter model/api 20 + flutter 屏幕级 widget 6。

---

### （历史：课程考试按钮原需求，已完成，见上方 ✓）

课程页一个"考试"按钮：把这门课全部学过的知识点综合出一张**有针对性**的试卷（基于 mastery 弱点抽题，不重新生成而是从已有题库抽）。

- **场景**：当前每个 episode 独立出题、独立交卷，没有"阶段性综合测评"。学生学完一门课（或一个章节）后，想检验整体掌握程度，只能靠记忆里的散落印象。课程级 exam 提供"阶段性考试"体验。
- **价值**：补上"阶段测评"。复用已有题库（每个 episode 多次 quiz 后积累的 questions）做抽题，边际成本极低（不出新题、不跑 LLM），但给家长/学生一个清晰的"这门课学到什么程度"信号。
- **工作量预估**：中偏大。
  - 后端：抽题算法（按 mastery 弱点优先抽 + 覆盖该 course 下多个 episode/chunk + 题型均衡 + 题量控制），新表 `Exam`（或复用 `Quiz` 加 scope 字段），交卷报告（跨 episode 的 mastery 变化 + 弱点分布）。
  - 前端：课程页加"考试"按钮 + 试卷渲染（复用 quiz 组件）+ 交卷报告页（图表化展示）。
- **依赖**：题库数据量要足够（每个知识点有 2-3 道可抽的题），即学生在多个 episode 做过 quiz 后才有意义。新课程/新课时不适合开考试。和错题本共用题库检索层。

---

## P1 — 中等价值

### ✓ 轻量 log 系统（作业级事件落库 + admin 可视化）—— 已完成（2026-07-22）

已落地：新表 `log_entries`（level/source/message/fields_json/job_id/episode_id/course_id/
created_at）+ `LogRepository` + service 层 `appendLog`（nil-safe + best-effort，失败只
`log.Printf`、绝不 derail 业务）+ 5 点接线（`failJob`/error、`ReapStaleJobs`/warn、
`polishStats`/info、provider resolve 失败/error、worker panic/error）+ `GET /admin/api/logs`
端点 + `/admin/logs` 前端页（仿 AIWorkflow 的 useQuery + 轮询）。不引第三方 log 库、不全量
替换 81 处 `log.Printf`（渐进迁移），边界声明全部兑现。详见 `docs/modules/ai/subtitle-queue.md`
§12.7。

<details>
<summary>原条目（保留供参考）</summary>

当前所有日志走 stderr（`log.Printf`，81 处，前缀不统一）。AI/subtitle worker 的关键事件（job 开始/结束/失败、relay 调用、reaper）只在 server 日志里，admin 看不到。加一个轻量结构化 log 层让运维可见性不再依赖 SSH 看日志。

- **场景**：polish 卡住、relay 挂了、worker 死锁等"看不见的故障"，admin 只能从 job 状态间接推断。2026-07-21 这轮调研时确认 polish partial 的诊断盲点主要靠 job detail 字符串 + 字幕 diff UI 已经覆盖，但通用事件流（reaper 触发、provider 切换、heartbeat 丢失）仍无 admin 视图。
- **价值**：运维可见性。下次出现"AI worker 前面卡住了，不知道为什么"这类问题，admin 能直接在控制台看事件流定位，而不是等你 SSH 进去看日志。
- **工作量预估**：中（一个中等 PR）。MVP = `log_entries` 表（仿 `AIRun`：id/level/source/message/fields_json/job_id/created_at）+ 仿 AIRun 的 repo/handler（约 200 行后端）+ `/admin/logs` 页（仿 AIWorkflow 的 useQuery + pollWhen，约 200 行前端）+ 改 5 个集中点（failJob/httperr/两个 reaper/polishStats）。**不引第三方 log 库**（自己写 wrapper，避免 go.mod 改动 + 全仓 81 处替换）；**不做 SSE 实时流**（项目无 SSE 基建，轮询够用）；**不全量替换 81 处 log.Printf**（新代码用新 wrapper，旧代码渐进迁移）。
- **依赖**：无。可独立 PR。

</details>

### streaming 输出（SSE）

agent 跑完一次性返回 → 改成 SSE 流式输出，改善等待体验。

- **场景**：当前 AI 能力是同步等结果（quiz/advice 生成几十秒，学生盯着"generating..."转圈）。
- **价值**：体验提升，尤其是 chat（每条消息等几秒太长）。**是 chat 多轮对话的硬前置**——没有 SSE，多轮每条等 5-10s 不可用。
- **工作量预估**：中。`LLMProvider` 加 `ChatStream(ctx, req) (<-chan ChatChunk, error)`；`OpenAICompatProvider` 改 SSE reader；SSE handler + Flutter `StreamBuilder` UI。波及所有 `Chat()` 调用点。
- **依赖**：无硬依赖，但建议作为 chat 的前置。

### chat 多轮对话能力（原 Phase D）

学生在 AI tab 里和 agent 多轮对话讨论一节课，答案带视频时间戳跳转（"你说的通分在 12:38 讲过"）。

- **场景**：当前 AI 能力是单向的——出题、总结、建议，没有"学生提问"的入口。chat 补上"问答"维度。
- **价值**：从"被动接收 AI 内容"升级到"主动提问"。复用 Phase C 的 RAG（content_chunks）+ memory。
- **工作量预估**：大。`ChatSession`/`ChatMessage` 表骨架已建（字段未定，按当时需求加列，零风险）；agent 逻辑、流式 UX、上下文窗口管理都要从头做。
- **依赖**：建议 streaming（见上）先做以改善等待体验。

### memory 衰减曲线（艾宾浩斯）

`KnowledgeMemory.mastery` 当前是单调累积（答对 +0.1 / 答错 -0.2），不随时间衰减。加艾宾浩斯遗忘曲线，让长时间没复习的知识点 mastery 自然回落，触发 agent 重新出题巩固。

- **场景**：学生一个月前答对的"通分"现在可能已经忘了，但 mastery 还是 0.9，agent 不会再出这题。衰减让"过时掌握"自动浮现。
- **价值**：让系统更接近真实学习规律，自适应更准。
- **工作量预估**：小到中。`KnowledgeMemory` 加 `decay_*` 列（加列，零风险），读时算衰减、不需要后台 cron。`LastReviewed` 字段已存在可直接用。
- **依赖**：无。属于 §18 forward-safe 表里的"加列"项。

### AI 输出质量仪表盘

`QuizSelfCheck` 已经在跑（出题后第二轮 LLM 审题，pass/fail/regenerated 写 `AIRun.SelfCheckResult`），但没有聚合视图。

- **场景**：admin 想发现"某个学科的 quiz fail-rate 突然升高"或"换模型后质量下降"，现在只能逐条 run 看。
- **价值**：质量治理。能及时发现 prompt/模型/prompt 版本的输出质量回归。
- **工作量预估**：中。数据全在 `ai_runs` 表（self_check_result + capability + model_used + system_prompt_text），缺聚合 UI：按学科/题型/self-check 结果分组的 fail-rate 趋势图。杠杆在 UI 不在数据采集。（2026-07-22 补：polish 现在也写 `ai_runs` 了——`recordPolishRun` capability="polish"——所以仪表盘能覆盖全部 AI 能力，不再漏 polish。）
- **依赖**：无。

### 知识点命名标准化（chunk 打标签 / 本体库）

目前 agent 靠 LLM 推理关联 `chunk.text` 描述知识点。未来给 chunk 打标签 / 建本体库，让知识点有稳定 ID 而非靠文本相似。

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

### 简答题（short_answer 题型）

开放式问答，LLM 判分。

- **场景**：考论述/表达/推理过程，选择填空考不了的维度。
- **价值**：题型丰富度，最接近真实考试。但判分难（要 LLM），且 PAD 输入长文本体验差。
- **工作量预估**：中偏大。LLM 判分 prompt + 边界 case 处理（学生答得"接近但不够准"怎么判）。

---

## 已识别的技术债

### AIHint 旧字段清理

`Course.AIHint` 在质量优化轮次收进单 JSON 列 `AIConfigJSON` 后已 deprecated，`EffectiveWhisperHint()` / `EffectiveQuizHint()` 方法从 JSON 解析、JSON 字段空时回退到老 `AIHint` 列。同理 `Question.Answer` / `Question.AnswerText` 被 `Scoring` 取代后也 deprecated。

- **要做什么**：deprecated 一轮（确认所有线上课程都重新保存过、所有 question 都有 Scoring）后，删除 `Course.AIHint` 列 + `Question.Answer`/`Question.AnswerText` 列 + `Effective*` 回退逻辑（回退去掉后 `Effective*` 直接返回 `AIConfig()` 的字段即可）。
- **阻塞项**：删列属于"升级困难"类（SQLite DROP COLUMN 有版本/约束限制），需要等下次数据清零或写显式迁移脚本。不能在 AutoMigrate 里直接删。
- **触发条件**：admin 端观测无 course 还在用 AIHint（Effective* 回退路径连续一段时间无命中）。

### AIConfig 扩展新配置项（JSON 化的收益兑现）

`Course.AIConfigJSON` / `Subject.AIConfigJSON` 单 JSON 列设计成前向兼容——加新配置项不必改 schema。当出现新需求时（举例如下），只需：扩 `model.AIConfig` struct 加字段 → admin 表单加输入 → service 层 `SetAIConfig` 自然带上 → 消费方按需读。

- **难度系数**：`DifficultyBias string`（easy/medium/hard），出题 prompt 读它调难度配比。
- **题型配比**：`QuestionTypeMix map[string]float64`（如 `{"fill":0.5,"choice":0.3,"multi_choice":0.2}`），覆盖 prompt 默认题型倾向。
- **语言偏好**：`Language string`，summary/quiz 输出语言（双语课程可能要英文输出）。
- **禁用术语纠错**：`DisableTermCorrection bool`，某些课程字幕准确不需要纠错时关掉。

这些都是**代码-only 改动**，零 schema 迁移——这就是 JSON 化的核心收益。

### 多选题 mastery 加权数值微调

当前 multi_choice grading 三态的 mastery 增量是固定数值：全对 +0.1 / 部分对（漏选）按"错"处理扣 -0.2 / 错 -0.2。

> 2026-07-23 改动：部分对（漏选）从"视为掌握、不扣分"改成"按错扣分"。原口径（部分对不扣 mastery）是质量优化轮次为避免压低 mastery 误导 advice 而设，但 2026-07-23 错题本上线时暴露了它的自相矛盾——同一道漏选题，错题本判定和 mastery 判定相反，语义混乱。现统一为"漏选就是错"，mastery / 错题本 / 显示对错三处一致。`verdict.Partial` 字段保留用于 UI 展示"漏选X/多选Y"明细，但不再改变 mastery/错题本判定。

- **要做什么**：`RecordAnswer` 当前只支持 correct=true/false，部分对按错处理是粗糙折中（漏 1 个和全错同等扣分）。可加 partial 参数或 Score 参数，让部分对按"勾中正确项数 / 正确项总数"给比例分（如勾中 2/3 给 -0.07 而非 -0.2），全错才 -0.2。
- **价值**：mastery 更精确反映掌握程度。当前部分对一律 -0.2，让"勾中 1 个"和"勾中 3 个里的 2 个"同等对待。
- **工作量预估**：小。`RecordAnswer` 签名扩 partial/Score + UpsertMemoryOnAnswer 的增量公式调 + 单测调阈值。
- **触发条件**：多选题上线后观察一段时间，确认加权不会让 mastery 抖动过大。

### advice 重算策略细化

当前 `UpsertAdvice` 每次交卷都覆盖（episode 级 advice 在每次 submit-all 后链式触发重算）。如果学生一节课做了 5 次 quiz，advice 会被重算 5 次——每次都烧 token，但内容可能没实质变化（mastery 没动多少）。

- **要做什么**：加节流——"上次建议后答了 N 题才重算"或"mastery 变化超过阈值才重算"。
- **价值**：省 token（advice 是低优先级 job，但积少成多）。
- **工作量预估**：小。service 层加重算前检查（对比 `MasterySnapshotJSON` 和当前 mastery 的 diff）。
- **触发条件**：观察到 advice 重算频率过高 / token 账单上涨。

