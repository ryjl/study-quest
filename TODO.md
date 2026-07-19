# TODO — StudyQuest 待办清单

> 本文件记录所有"已识别但未实现"的 feature idea，按优先级分组。每次迭代从这里挑选。
> 最后更新：2026-07-19（用户反馈修复轮次：cache 流程重构 + 课程总览全链路 + AI 内容 normalize + 多个 UX 修复）

每条尽量写清四样东西：**场景**（解决什么用户问题）、**价值**（为什么值得做）、**工作量预估**（小/中/大）、**依赖**（前置条件 / 阻塞项）。

---

## 已完成

### 2026-07-19 用户反馈修复轮次（commit `c9e1f04` + `fc6a730` + `99b408f`）

用户反馈 + 自测发现的 bug 一次性处理。**下个 session 接续未解决的前端渲染 bug，详见 `docs/handoff-frontend-render-issues.md`**。

- **导入文件名保留原字符**：删 `import_service.go` / `reading_import_service.go` 里 `-`/`_` → 空格的替换。`26-7-12【...】` 这类日期/编号不再被破坏。
- **cache MISS log 措辞**：worker.py 原来误导成 "using netdisk URL"，改成 "wav/mp4 cache 仍会尝试"。真正的"走网盘"在 audio.py 里另有准确 log。
- **cache 流程重构**：worker 原来是 video cache 优先，wav cache 在 audio.extract_wav 内部——wav 已命中时还白做 video 扫描（含 WSL 9P）。重排为 wav → video → 网盘。`audio.find_cached_wav()` 抽出为公开函数。
- **WSL2 9P readdir EIO 兜底**：`/mnt/e` 上 find/os.walk 会因 9P EIO 静默漏文件（但 stat 单个路径走不同 9P 消息通常正常）。`cache._scan_find` 加 returncode 告警，`cache.lookup` 索引 MISS 时做 `_probe_flat` stat 兜底（平铺布局下救回 EIO 漏的文件）。测试：12 cache + 5 audio。
- **`UpsertSummary` 改 OnConflict**：旧版 `db.Save(s)` 在 s.ID=0 时走 INSERT，重新生成撞 uniqueIndex 报 "duplicated key not allowed"。用户被迫先手动删才能重生成。
- **AI summary 内容 normalize**（`parseSummaryJSON` + 客户端 `MarkdownView._normalizeMarkdown` 双向兜底）：
  - 字面量 `\n`（backslash+n）→ 真换行（修 GFM 表格被当一行）
  - 裸 `<svg>...</svg>` → 自动补 ` ```svg ` 围栏（修 SVG 被当内联 HTML 转义）
  - 8 个 normalize 单元测试（含"生产 DB 真实样本"用例）
- **Summarizer prompt 富文本**：`"可选/鼓励"` → `"主动使用"`（修 3 门课一副图一表格没生成）。CourseSummary prompt 也加富文本段落，去掉"禁止代码块标记"。
- **课程总览全链路（Phase D 客户端入口）**：
  - `AICourseSummary.EpisodeCountAtGen` 字段 + 陈旧检测（生成时快照的"已总结课时数"vs 当前数）
  - admin 内容管理 tab：status 轮询 + 删除按钮 gate（无 summary 不显示）+ 陈旧橙色提示 + summary_text 预览
  - Flutter `course_detail_screen` 在 Hero header 和"闯关目录"之间插课程总览卡片
  - `GeneratedAt` 统一为 RFC3339（admin 端已是，client 端从"2006-01-02 15:04"改）
- **AI 控制台「内容管理」**：tab 改名（原"重新生成"但既有重生成也有删除，语义更准）。`GET /admin/api/ai/courses/:id/summaries-status` 新端点（返回有 summary 的 episode id 列表，gate 每集删除按钮）。
- **决策痕迹/最近活动显示课程标题**：新增 `AIRunView`（内嵌 AIRun + EpisodeTitle/CourseTitle/UserNickname）+ `enrichRuns`（批量解析 run.job → episode/course/user 标题）。AIWorkflow RunList 表格加"课程/课时"列。
- 验证：`go build` ✓ / `go test ./internal/ai/agent/...` ✓ / `tsc --noEmit` ✓ / `flutter analyze`（无新错误）✓ / cache 单元测试 17/17 ✓ / 生产 DB 级联删除验证（sqlite_sequence vs 实际行数对比，0 孤儿）✓ / MuMu 实测课程总览卡片显示 ✓。

### ⚠️ 本轮未完成（下 session 接续）

3 个前端渲染 bug，根因都疑似"某个卡片渲染异常让后续兄弟节点不显示"。详见 `docs/handoff-frontend-render-issues.md`：

1. **table 盖文字**：`common_mistakes`/`methods` 的 MarkdownView 在带 padding 的 Container 里，约束让 flutter_markdown 内置横向 scrollview 没生效。
2. **数学课 quiz 卡片不显示**：后端返回 ready，`_buildQuizSection` 也进 ready 分支（logcat 已确认），但视觉上看不到——可能渲染中触发 silent error。
3. **课程总览卡片让下方章节列表看不见**（2026-07-19 用户新发现）：症状同上，疑似同类问题。

`ai_study_screen.dart` 已留 4 处 print 诊断日志（`_loadQuiz` / `_buildSummarySection` / `_buildQuizSection` 入口 / hiding 分支），release 模式下也输出到 logcat，调完清掉。

---

### 2026-07-19 AI 控制台 + 重新生成/DELETE 闭环 + FK 级联轮次

本轮（尚未 commit，代码在 working tree）。AI 内容生命周期管理 + AI 配置集中化 + FK 级联：

- **5 类产物 regen + DELETE 闭环**：Episode Summary / Quiz / Advice / Course Summary / User Study Report 每类都齐了两端点 —— 覆盖式重新生成（覆盖旧产物，保留 Upsert 语义）+ 物理 DELETE（idempotent，幂等删，删完再查返回 null）。覆盖式优先，DELETE 作为清理脏数据的兜底。新路由 8 条（`POST .../quizzes/regenerate`、`POST .../advice/regenerate`、`GET .../advice`、5 条 DELETEs）。Service：`RegenerateAdvice`（admin 可强制重算，跳过 mastery gate）、`RegenerateQuizForUser`（与客户端 `RegenerateQuiz` 共享 impl）、`Delete*`（幂等）、`ListUserAdvice`。
- **EnqueueSummary 去重门**：照抄 `hasPendingJob("segment", id)` / `hasPendingQuizJob` 模式 —— 有 queued/processing summary job 直接返回不再入队。修了"admin 连点堆 job"的 bug。
- **AI 表 FK CASCADE（9 张表）**：给 9 个 AI struct（AISummary/ContentChunk/Quiz/Question/Answer/KnowledgeMemory/AICourseSummary/UserStudyReport/AIRun）的 relation 字段加 `gorm:"foreignKey:XxxID;constraint:OnDelete:CASCADE"`。**单向声明**：只在 AI 侧加 FK，core Episode/Course/User 保持零感知（"AI 是 add-on 层"原则）。relation 字段全带 `json:"-"`。**简化 repo 级联**：episode/course/user repo 的 Delete 现在主要靠 CASCADE，只剩 AIJob（无 FK）和 StudyAdvice（polymorphic scope_id）还需要手动 `tx.Delete`。**修了 userRepo.Delete 完全不动 AI 数据的 bug** —— 之前清理 ZERO AI tables，现在补 AIJob + StudyAdvice 手动 + Quiz/Question/Answer/KnowledgeMemory/UserStudyReport 走 CASCADE。
- **AIJob.EpisodeID/CourseID "形式 not null" bug 修复**：原来是 `uint gorm:"not null"`，但 subject-scope advice job 写 0（0 是合法 uint，SQLite 接受了，但语义上指向不存在的 episode）。改 `*uint`（nullable）+ 新增 `model.PtrVal(*uint) uint` helper + 更新 ~9 个 call site。**这是本轮 FK 工作中顺带发现的 bug** —— 加 FK 时立刻暴露（FK 会拒掉 0 行）。
- **集中化：AIConsole 页**：新建 `pages/AIConsole.tsx`（5 tabs：重新生成 / Prompt 配置 / 任务队列 / 学生数据 / Provider）。URL `?tab=` 控 tab，`?course=`/`?subject=` 预选实体（CourseModal/SubjectModal 跳转用）。AIWorkflow 和 AIUserView 嵌进去（`embedded` prop）。**CourseModal 瘦身**：删 125 行 AI hint UI，改成「配置 →」链接到 `/admin/ai-console?tab=prompt&course=:id`；Course 级 AI 开关（`ai_summary_enabled`/`ai_quiz_enabled`）保留；save body 透传 `ai_config: course?.ai_config`（原样 roundtrip，PUT 不覆盖 5 个 hint 字段）。**SubjectModal 同样瘦身**（删 62 行，链接 `?tab=prompt&subject=:id`）。**Settings 移除 AiProvidersSection 渲染**，改成「前往 AI 控制台 →」卡片。抽公共组件 `PromptConfigTab` + `AIHintFields`（5 textarea，从 CourseModal+SubjectModal 的重复代码抽出）。**Layout/App 路由调整**：AI 运营 nav 2→1，老路由 `/admin/ai-workflow`、`/admin/ai-user` 重定向到 `?tab=jobs`/`?tab=users`。删 `lib/aiHintTemplates.ts`（集中化后 `getSubjectTemplate` 零调用方的死代码）。
- **象棋升系统级 + AIConfig seed**：`xiangqi` 从自定义学科升为系统学科（`IsSystem=true`），在 `SeedDefaultSubjects` seed 5 字段 AIConfig（whisper/summary/quiz/advice/term_dict）。模板内容来自原前端 `aiHintTemplates.ts`（本轮删除）。注：math/english 之前就已有 seed，象棋是本轮新增。
- **测试**：新增 `repository/user_repo_test.go`、`repository/ai_content_delete_test.go`；`repository/cleanup_orphans_test.go` 扩展；新增 `service/ai_service_test.go`（EnqueueSummary 去重）、`service/ai_service_advice_test.go`（RegenerateAdvice）、`service/subject_service_test.go`（新增 `TestSeedDefaultSubjects_XiangqiAIConfig` + 更新已有计数）；`lib/api.test.ts` +9 tests。
- 验证：`go build` ✓ / `go test ./internal/service/ ./internal/repository/` ✓ / `tsc --noEmit` ✓ / `npm test` 55 passed ✓ / DB 删后重建 AutoMigrate 从零生成（FK 约束 baked in）+ 手动启动服务器 + sqlite 查询确认象棋 `is_system=1`、`ai_config_json` 489 字节、5 字段全在（含"车→居"纠错）。

### 2026-07-19 prompt 架构重构轮次

代码已 push（commit `bb53dac`）。5 维度配置 + 学科级默认 + 完整可观测性：

- **5 个 AIConfig 维度**：`whisper_hint` / `summary_hint` / `quiz_hint` / `advice_hint` / `term_dict`（前 3 个原有，后 2 个新拆/新增）。存 `Course.AIConfigJSON` + `Subject.AIConfigJSON` 两层。
- **两层解析**：`Course.EffectiveXxxHint(subject)` Course > Subject > legacy AIHint；`term_dict` 特殊——课程级+学科级**合并**（学科在前，课程追加）。
- **system prompt 5 处硬编码学科术语全清**（车→居、通分→同分），改成注入式 TermDict（user prompt 的【术语字典】段）。英语课 prompt 不再带象棋术语。
- **Advice agent 终于拿到 hint**（之前 AdviceRequest 完全没 hint 字段）。
- **TermDict 直传 Request**（summary/advice），不走 ReAct 工具路径，术语纠错每次必生效。Quiz 走 `get_episode_info` 工具返回。
- **可观测性**：`AIRun.SystemPromptText/UserPromptText` 记录每次 LLM 调用的完整 prompt；`agent.Run`（4 个 agent 共同入口）+ `summarizer.recordRun` 写入；admin AI Workflow「查看回放」展示。
- **预览端点** `POST /admin/api/ai/courses/:id/preview-prompt`（不调 LLM 拼出完整 prompt 供调优）；`agent/preview.go` 的 `BuildXxxPromptForPreview` 调同一个 build 函数保证"预览即真相"。
- **学科级默认**：SubjectModal 加 5 字段配置；CourseModal "套用模板"改为优先读 DB 学科 `ai_config`（回退前端 `aiHintTemplates.ts`）；数学/英语两科 seed 在 `SeedDefaultSubjects` 回填。
- **CourseModal/CourseService 签名重构**：从 `(whisperHint, quizHint string, ...)` 改成传 `model.AIConfig` struct。
- **经验教训**：写进 `CLAUDE.md`「Workflow: when to parallelize with subagents」——高耦合重构主会话串行做，跨 agent 契约先定死贴进每个 agent 指令，model 改动后必须跑全量 test 不能只 build。
- 验证：`go build` ✓ / `go test` ✓（全过）/ `tsc --noEmit` ✓ / `npm test` ✓（46 passed）

### 2026-07-18 体验优化轮次

代码已 push（commit `e367503`）。8 条用户反馈一次性交付：

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
- **工作量预估**：中。数据全在 `ai_runs` 表（self_check_result + capability + model_used + system_prompt_text），缺聚合 UI：按学科/题型/self-check 结果分组的 fail-rate 趋势图。杠杆在 UI 不在数据采集。
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

### [已于 2026-07-19 解决] 删 episode/course/user 时 AI 数据无级联

**已解决：FK CASCADE + userRepo 补全。** 9 张 AI 表加了 `foreignKey:XxxID;constraint:OnDelete:CASCADE`（单向 AI 侧声明，core 零感知），episode/course/user repo Delete 现在靠 CASCADE 自动清，仅 AIJob（无 FK）和 StudyAdvice（polymorphic scope_id）保留手动 `tx.Delete`；并修了 `userRepo.Delete` 之前清理 ZERO AI tables 的 bug。详见顶部「2026-07-19 AI 控制台 + 重新生成/DELETE 闭环 + FK 级联轮次」。以下为历史记录，保留备查。

`AISummary/Quiz/Question/Answer/StudyAdvice/AICourseSummary/UserStudyReport/ContentChunk/KnowledgeMemory/AIRun/AIJob` 这些表的 `EpisodeID/CourseID/UserID` 列都只有 `gorm:"index;not null"`，**没有 `gorm:"foreignKey:..."` 声明**。删 episode/course/user 时这些 AI 数据变孤儿残留。

- **要做什么**：要么加 FK 声明 + OnDelete 策略（CASCADE 或 RESTRICT），要么在 service 层的 Delete 方法里显式清理 AI 数据。
- **价值**：避免 DB 长期积累孤儿数据；也让「重新生成闭环」P0 项里的"删除 AI 产物"语义更干净。
- **工作量预估**：小到中。主要是决策（CASCADE 还是 service 层手动清）+ 改 Delete 方法。
- **触发条件**：和「重新生成闭环」一起做最合理（都是 AI 内容生命周期管理）。
