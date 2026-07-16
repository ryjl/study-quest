# AI 出题 Agent（Phase C：memory + ReAct + 出题）

> 技术文档，进 git。记录 Phase C 出题 agent 的**已落地设计**，对标
> `docs/ai-subtitle-queue.md` 的深度。整体模块背景见 `docs/ai-agent-module.md`；
> 实现交接笔记见 `docs/handoff-ai-step3-phaseC.md`（不进 git）。
>
> 本文聚焦**出题 agent 这一条能力线**（总结见 §Phase B，chat 留 Phase D）。

## 1. 一句话

学生打开一节课的「AI 学习」页 → agent 读这节课的字幕切片（RAG）+
**该学生这节课的掌握度 memory** → 用 ReAct loop 自主决定考哪些知识点、
出几题、什么难度 → self-check 自我校验 → 落库。学生答题 → 更新 memory →
**下次换题时 agent 读到新弱点，自适应出题**。

这是让系统成为 agent（状态驱动、自适应）而非无状态出题脚本的核心。

## 2. 关键设计决策（落地时确定，记录理由）

### 决策 1：单套题库（一个用户一节课只有一套当前题）

`Quiz` 对 `(user_id, episode_id)` 加唯一复合索引。一个学生一节课**始终只有一套
当前题**，不存在"题库 1/题库 2"的列表。

- **重做** = 同一套题再答一遍（`Answer` 表 append-only，每次答题加新行；
  `KnowledgeMemory` 的 mastery 累积更新）
- **换题** = `CreateQuiz` 事务内删旧 quiz+questions、插新（基于**最新 memory**
  重新生成）

为什么不做多套：反馈循环的语义是"用户做了题 → mastery 更新 → 下次出题更准"，
这要求"下次出题"是**覆盖式**的。多套题会让"哪套代表用户状态"变模糊。家用场景
题库膨胀也没意义。

### 决策 2：按用户独立 + 懒生成

出题绑定具体用户（`AIJob.UserID`），agent 通过 `get_user_mastery` 工具读
per-user memory → 真·自适应。

触发：客户端首次 `GET /ai-quiz` 发现无 quiz → 后端入队 per-user quiz job →
返回 `202 generating` → 客户端轮询（3s）直到 ready。

admin 批量预热：**结构预留**（`service.EnqueueQuiz` + worker 支持 quiz job），
但本次不接路由。懒加载够用且省钱（只有真正用 AI 出题的用户才花 LLM token，
符合纯附加层哲学）。

### 决策 3：选择 + 填空混搭，填空仅限唯一答案

`Question.Type` = `choice | fill`。

- **选择**：`Options` JSON 数组 + `Answer` 索引（0-based）
- **填空**：`AnswerText` JSON 数组（多等价答案，如 `["12","十二"]`）

**填空题仅限有唯一确定答案的知识点**（数学计算、事实回忆）。prompt 强约束；
self-check 额外校验填空答案唯一性。主观/辨析/多解题一律选择。

判题（`agent/grading.go`，纯函数，单测覆盖）：
- 选择：比索引
- 填空：`NormalizeText` 归一化（全角→半角、去标点空格、小写）后与可接受答案
  **精确匹配**——不做模糊匹配（数学题 `11 ≠ 12`）。归一化只保留字母/数字/CJK/
  小数点/负号/分数线。

### 决策 4：memory 两表分工

| 表 | 作用 | 换题时 |
|---|---|---|
| `Answer` | 做题流水（append-only）+ 冗余 `QuizID` 快照 | 保留（换题不删答题历史）|
| `KnowledgeMemory` | 汇总掌握度（mastery 0-1，agent 读它）| 保留（长期学习状态）|

mastery 增量**不对称**：答对 +0.1，答错 -0.2（clamp 0~1）。错误信号更强，
弱点快速浮现。原子 upsert：`INSERT ... ON CONFLICT(user_id,chunk_id) DO UPDATE`
（参照 progress 的 watch-seconds 原子累积规则，避免并发答题丢增量）。

**`Answer.QuizID` 冗余字段**（Review 修复）：换题删旧 question 后，answer 的
`question_id` 会悬空。加 `QuizID` 快照 + `ListAnswersForQuiz` 按 episode 维度查
（`quiz_id IN (SELECT id FROM quizzes WHERE episode_id=?)`），换题后历史答题
仍可查，不依赖 JOIN 即将被删的 question。

### 决策 5：meta 充分利用

`get_episode_info` 工具返回**富信息包**：标题、**文件名**（从
`VideoRelativePath`/`OriginalRelativePath` 提取，常带章节信息如
`第3讲_分数加减法.mp4`）、时长、科目、标签、年级、AIHint、已生成 summary 的
concepts/key_points。文件名帮 agent 快速锁定主题，比纯靠字幕召回更准更省 token。

### 决策 6：可观测性是学习载体，非附属

本项目核心价值是**学 agent**。三个可观测性支柱：

1. **`AIRun.TraceJSON`**：quiz run 携带
   `[{step, thought, action:{tool, args}, observation}]`，admin「思考时间线」展开
   成步骤列表——学 agent 决策流的核心。用**平衡括号匹配**提取 JSON（容错模型
   输出截断/包裹），observation 每步截断 ~500 字。
2. **`Quiz.AgentFeedback`**：LLM 出题副产品（基于 memory 生成弱点分析+学习建议），
   **不额外调 LLM**；展示给学生 + admin。
3. **两个观测视图**：AIWorkflow（job 视图，run 详情含 trace 时间线）+ AIUserView
   （用户视图：选用户→题库→详情：题+答题历史+memory+评价+思考时间线）。

## 3. ReAct loop（agent.go 核心，★学习重点）

```
messages = [system, user]
for step in 1..maxSteps(=6):
    resp = llm.Chat(req{messages, tools: toolbox.Specs(), max_tokens: 4000})
    if resp.FinishReason != "tool_calls":
        return final answer  # 终止
    # 执行工具
    for each tool_call:
        observation = toolbox.Execute(name, args)  # 未知工具→返回 error 字符串，不崩
        messages.append(RoleTool{ToolCallID, observation})
达上限 → 强制 ToolChoice="none" 再调一次逼出最终答案
每步记录 TraceStep 进 trace
```

- **每步重发全部消息**：模型无状态，每轮重发历史。token 成本随步数线性增长
  （实测一次出题 ~20000-40000 prompt tokens，是 Phase B 单次总结的 ~5-10 倍）。
  这是 ReAct 的固有成本，maxSteps=6 + 预召回（prompt 预填 memory/episode meta）
  控制轮次。
- **`MaxTokens=4000`**：生成 turn 的最终答案是多题 JSON+解析，1500-2500 tokens。
  不设上限会被中转站默认值（~1197）截断，导致 JSON 解析失败（这是端到端验证发现
  的真实 bug）。

## 4. 工具（tools.go）

| 工具 | 作用 | 数据源 |
|---|---|---|
| `search_subtitles(query)` | 语义检索字幕（余弦相似度 top-5）| content_chunks embedding |
| `get_user_mastery(episode_id)` | 读该用户该 episode 各 chunk 掌握度（弱点优先）| KnowledgeMemory |
| `get_episode_info(episode_id)` | 富 meta 包（标题/文件名/科目/标签/年级/AIHint/summary concepts）| Episode+Course+AISummary |
| `get_related_chunks(chunk_index)` | 读某 chunk 全文（钻取知识点）| content_chunks |

每个工具 scoped 到一个 `(episode, user, course)` 三元组（构造时绑定），模型只能
传 query/chunk_index，**无法跨用户/跨 episode 取数**（服务端强制 scope）。

检索用**暴力余弦相似度**（`vector.go` CosineSim/TopK）：per-episode chunk 数百级，
扫描几百个 512 维向量是微秒级，ANN 索引的构建/内存开销不值得。

## 5. self-check 自我修正（quizzer.go）

出题后第二轮 LLM 调用（ToolChoice=none，不调工具）校验：答案正确性/可推出性/
干扰项合理性/填空唯一性。fail 则**重新生成一次**（bounded，不循环）。

诚实状态：regen 后 `SelfCheckResult = "regenerated"`（不是 "pass"），note 说明
"未对重新生成的题目二次审核"。admin badge 用琥珀色区分 pass/fail/regenerated。

## 6. 数据模型增量（Phase C）

在 Phase A/B 基础上加的字段（全 nullable/默认，AutoMigrate 加列安全）：

```
Quiz:        + agent_feedback (text)
             + UNIQUE(user_id, episode_id)  -- 单套题库约束
Question:    + type (choice|fill, default choice)
             + answer_text (text, fill 的可接受答案 JSON [])
AIJob:       + user_id (*uint, nullable; quiz job 必填)
AIRun:       + trace_json (text, ReAct 步骤轨迹)
             + JSON tags 全字段 snake_case（修复 PascalCase 导致 SPA 渲染失败）
Answer:      + quiz_id (uint, 冗余快照; 换题后历史答题可查)
```

## 7. API（Phase C 新增）

### 客户端（`/api/v1/episodes/:id/ai-quiz*`，受 IsEpisodeVisible 访问控制）
```
GET  /ai-quiz            无题→202 generating 懒生成; ready 返回题(不下发答案)
POST /ai-quiz/submit     {question_id, answer_index? | answer_text?} → 判题+解析+跳转时间
POST /ai-quiz/regenerate 换题(删旧基于最新 memory 重新生成)→202 generating
```

### Admin 观测（`/admin/api/ai/*`）
```
GET /summaries/:episodeID   读已生成总结内容
GET /users/:userID/quizzes  列某用户所有题库
GET /quizzes/:quizID        题库详情(题+答案+答题历史+memory+agent_feedback+ai_runs trace)
```

## 8. 前端

### Admin SPA
- **AIWorkflow.tsx**：run 详情加「思考时间线」（展开 trace_json 为步骤列表）；
  SelfCheckBadge 识别 pass/fail/regenerated/skipped
- **AIUserView.tsx**（新增页）：选用户→题库列表→详情（题/答题/memory 进度条/
  agent_feedback/思考时间线）。侧栏导航「AI 用户视图」
- 所有 quiz 观测端点返回 snake_case（service 层 DTO 转换，修复 model 直接
  marshal 的 PascalCase 问题）

### Flutter（`ai_study_screen.dart`）
- 独立 AI 学习页：顶部 Step3 总结 + agent_feedback（学习建议）+ 练习区
- 练习：拉题（懒生成 202 轮询）→ 做题（选择点选项/填空输文本）→ 反馈
  （对错+解析+[跳转 xx:xx]）→ 重做/换题
- 跳转：`Navigator.pop(JumpRequest(Duration))`，player 接收后 `_seekTo`
- 入口：course_detail「AI 学习」按钮 + player 顶栏图标

## 9. Docker（embedding 模型打进镜像）

Dockerfile 加 `ai-models` build stage：构建时 fetch libonnxruntime(22MB) +
BGE-small-zh 模型(23MB)，COPY 进 runtime 的 `/app/data/ai-models/`。

**为什么打进镜像**：① 45MB 增量小；② 可移植（新机器零配置 `docker run`）；
③ 版本一致（.so 版本与编译期 ORT_API_VERSION 锁定一致，避免 ABI 不匹配）；
④ 消除"挂载点没 fetch 过模型 → embedding 静默失效"的难排查故障。

模型层放 Dockerfile 靠前 stage，layer 缓存命中率高（只在版本升级时重下）。
版本 pin 镜像 Makefile 的 `fetch-ai-models`，需保持同步。

## 10. 端到端验证记录（真实 LLM，episode 25 数学课）

- 中转站 `gpt-5.4-mini` 支持 function calling（返回标准 tool_calls）
- 27 chunks + 结构化总结 → quiz 生成（6-7 题，含填空 `199+99+9=307`）
- trace 显示：get_episode_info → get_user_mastery(读到弱点 chunk3=0.0) →
  多次 search_subtitles → 最终答案
- 答对 mastery +0.1（累积验证），答错 -0.2（clamp 0）
- 换题后 agent trace 明确显示读到 `知识点片段#3: mastery=0.00 ★弱点` →
  新 quiz 针对弱点出题 + agent_feedback 写"位值原理还不够稳"
- self-check fail → 自动 regen（run #9，11 步，tokens 37689/6398）

## 11. 验证中修复的 bug（记录避免重蹈）

| Bug | 修复 |
|---|---|
| 最终答案被 max_tokens 截断，JSON 解析失败 | `extractJSONObject` 改平衡括号匹配 + `MaxTokens=4000` |
| `knowledge_memory` 表名错（GORM 复数成 `knowledge_memories`）| 原生 SQL 用正确表名 |
| 首次答题 INSERT 路径 mastery/count 全 0 | INSERT 也应用 delta |
| 客户端轮询每次创建新 quiz job（堆 10+ 个）| `hasPendingQuizJob` 检查，且优先于 quiz 存在判断 |
| admin DTO PascalCase（SPA 渲染失败）| AIRun 加 JSON tag + quiz detail DTO |
| 换题删 question 导致 answer 孤儿 | Answer 加冗余 QuizID + 按 episode 查历史 |

## 12. 演进方向

- admin 批量预热出题（`EnqueueQuiz` 已预留，接路由即可）
- self-check 对 regen 后的题目二次审核（当前诚实标 regenerated）
- token 成本优化：对话压缩 / 缓存工具结果（ReAct 每步重发全部消息是主成本）
- Phase D：AI chat（复用 RAG + memory，讨论 tab 接入 AI 学习页）
- memory 衰减曲线（艾宾浩斯，复用 KnowledgeMemory）
