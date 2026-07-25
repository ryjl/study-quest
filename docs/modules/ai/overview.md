# AI Agent 模块（Learning Agent：总结 / 出题 / 建议 / 报告 / Chat）

> 技术文档，进 git。对标 `docs/modules/ai/subtitle-queue.md` 的深度，作为本模块的权威架构参考。
> 本文档是本模块的权威架构参考。
> 本文档记录**已落地**的设计；标注 ⏳ 的部分尚未实现。

## 1. 背景与定位

StudyQuest 的视频字幕（Step 1/2 的产物）落库后，AI Agent 模块把它们转化为**结构化的学习辅助**：episode 总结、自适应出题、跨课程学习建议、课程级总结、admin 用户报告，（未来）互动问答。这是整个 AI 模块的核心价值——不是"调 LLM 出题/写建议"的脚本，而是基于 memory + RAG **自主决策**、有反馈循环的 agent。

### 能力清单，价值递进

两轮体验打磨后，能力从最初的"总结 + 出题"两件扩展到下面六件。其中后三件（建议/课程总结/用户报告）是**第二轮的核心**——它们不是单次 prompt engineering，而是复用同一套 ReAct loop（agent.go 的 `Run`），每个 agent 配自己的工具集 + system prompt，自己查数据自己分析（详见 §16）。

| 能力 | 状态 | 落地阶段 | 用到的 agent 机制 |
|---|---|---|---|
| ① Episode 总结 | ✅ | Phase B（Phase F 丰富化） | 单次 LLM 调用（无 tool calling）；Phase F 扩展输出结构（sections/methods/common_mistakes/pre_adventure，见 §9） |
| ② 出题 | ✅ | Phase C（第二轮做题流程重构） | **ReAct loop + tool calling + self-check + memory 反馈循环**（agent 核心）。第二轮新增统一交卷/历史/has_jump（见 §14、§17） |
| ③ 学习建议（advice） | ✅ | Phase C 第二轮 | ReAct loop + advice 工具集（episode/course/subject 三级，自然语言输出） |
| ④ 课程总结（course summary） | ✅ | Phase D | ReAct loop + course_summary 工具集（course-unique 纯内容总结，所有学生共享） |
| ⑤ Admin 用户报告（user report） | ✅ | Phase E | ReAct loop + user_study 工具集（admin 视角跨课程画像报告） |
| ⑥ chat | ⏳ | Phase D 原计划 | 多轮对话 + RAG（复用 Phase C 的检索/memory）。advice/course_summary/user_report 已部分占用了原 Phase D 的"agent 驱动"版图 |

### 第一性约束：纯附加层

> 没配 provider / 课程没开 AI → 系统行为与之前完全一致（纯视频观看）。

这条原则决定了所有设计：核心包（model/repository/service/handler）**不感知 AI**；AI 包只**读**核心数据 + 维护自己的私有表。AI 接口在没有总结时返回 404，客户端隐藏 AI 卡片——缺席是正常态，不是错误。

## 2. 整体拓扑

```
┌──────────────────────────────────────────────────────────────┐
│  Go Backend (2c2g 服务器)                                      │
│                                                                │
│  internal/ai/          ← 独立包，与核心业务隔离                  │
│    provider.go           LLMProvider/Embedder 接口 + DTO        │
│    openai_compat.go      chat 实现（→ 中转站 HTTP）              │
│    onnx_embedder.go      embedding 实现（本地 ONNX，无网络）     │
│    resolver.go           按 ai_providers 配置选择 + 缓存         │
│    segmenter.go          SRT → content_chunks 切片              │
│    agent/                ★ 决策逻辑（summarizer / quizzer /     │
│                           advice / course_summary / user_study   │
│                           —— 全部复用 agent.Run ReAct loop）       │
│                                                                │
│  service/ai_service.go   编排：job worker + 字幕完成 hook       │
│  handler/                admin 配置/观测 + 客户端读取            │
│                                                                │
│  SQLite: ai_providers / content_chunks / ai_summaries /         │
│          knowledge_memory / quizzes / questions / answers /     │
│          ai_jobs / ai_runs / chat_sessions / chat_messages /    │
│          study_advices / ai_course_summaries / user_study_reports │
└───────────────┬──────────────────────────┬─────────────────────┘
                │                          │
        chat HTTPS                  embedding 本地推理
                ▼                          ▼
   ┌──────────────────────┐   ┌──────────────────────────────┐
   │ 中转站 (OpenAI 兼容)  │   │ libonnxruntime + BGE-small-zh │
   │ hi-code.cc           │   │ (data/ai-models/, ~23MB)      │
   │ gpt-5.x 系列         │   │ 进程内, ~67MB 常驻, ~1.6ms/次  │
   └──────────────────────┘   └──────────────────────────────┘
```

**关键解耦**：chat 走远程中转站、embedding 走本地 ONNX，两者完全独立。换 chat 供应商不影响 embedding，反之亦然。这解决了"中转站没有 embedding 能力"的现实约束。

## 3. 三能力独立 Provider 设计

三个能力（chat / embedding / rerank）各有独立的 provider 配置（`ai_providers` 表，每行一个）。ProviderResolver 按 capability 选择实现，缓存构造结果。

### 为什么三能力分开配（而不是一个 provider 配一切）
- chat 中转站没有 embedding 能力 → embedding 必须另寻出路（本地 ONNX）
- 用户后期可能换中转站 → chat 配置可独立改，不动 embedding
- rerank 暂不实现但预留 → 接口已定义，将来加一个 case 即可

### Round-3 Provider UX 定档（admin 只配 chat）

第三轮把 provider 配置从"列表 + 可加多个"收敛为**admin 只配 chat 单例**：

- **embedding 不进 DB，对 admin 不可见**：embedding 模型（BGE-small-zh）打进 docker image，`resolveEmbed`（`resolver.go`）**直接用 `AIModelsDir` + 内置常量 `DefaultEmbeddingModel` 构造本地 ONNX embedder，不查 `ai_providers` 表**。embedding 子系统完全脱离 DB——没有行、没有 seed、没有 admin 配置、没有"配了又找不到"的混乱。`buildLocalEmbedder` 带文件健全检查（`.so`/`.onnx`/`vocab` 缺任一给清晰中文报错）。admin UI 完全隐藏 embedding/rerank。
- **外部 embedding 扩展钩子（未来）**：`resolveEmbed` 先查 DB 有没有 enabled 的**外部** embedding 行（`provider_type != onnx_local`），有就用外部（`buildEmbedExternal`，目前未实现返回 not implemented，是预留钩子），没有才 fallback 到本地内置。将来要换外部 embedding API = 加一行 `ai_providers`(capability=embedding, provider_type=外部类型) + 在 `buildEmbedExternal` 加一个 case，本地模型自动退为 fallback。**不破坏 DB 定档**（加行 = 安全）。
- **chat 是单例固定表单**：去掉"新增 Provider"按钮和列表，chat 只有一份配置（无则初次配置/创建，有则编辑）。resolver 本来就按"每 capability 一个 active provider"工作（`enabledRow` 取最低 ID 那条），UI 现在和这个语义对齐，不再让 admin 误以为可以堆多个。
- **模型从下拉选，不手填**：`OpenAICompatProvider.ListModels`（`openai_compat.go`）打中转站 `GET /v1/models`，admin 填完 base_url + api_key 后点"拉取可用模型"→ 下拉选 model_name（替代之前手填、容易填错）。新路由 `POST /admin/api/ai/providers/models` 用**临时的** base_url+key 探测（不需要先保存），所以新建 provider 时也能先看有哪些模型再选。端点若不支持 `/v1/models`（返回空），回退成手填输入框。
- **测试连通性保留**：`POST /admin/api/ai/providers/:id/test`（chat 发一条 ping 消息）。

> 注意：拉取模型用的是 admin **当场填的 key**（已存的 key 永不回显，无法复用）。编辑已存 chat provider 时，要拉模型必须重新输 key——这是偶发诊断动作，可接受。

### ⚠️ 部署路径陷阱（docker 卷遮蔽，已修）

`make deploy` 用主机卷 `-v ~/data/studyquest-data:/app/data` 持久化 SQLite + 字幕。早期 Dockerfile 把 embedding 模型 COPY 到 `/app/data/ai-models/`——**这会被主机卷遮蔽**（bind mount 盖掉镜像对应路径），容器看到空目录 → embedding 报"data 目录下没有"，与 DB 配不配无关。

**修法**：Dockerfile 把模型 COPY 到 `/app/ai-models/`（**在持久化卷之外**），`make deploy` 的 `docker run` 设 `-e AI_MODELS_DIR=/app/ai-models` 指过去。本地开发不受影响（`config.go` 默认值仍是 `./data/ai-models`，`make fetch-ai-models` 下载到这里，`make run` 用默认值）。**规则：任何 COPY 进镜像的文件，都不能放在被 `-v` 挂载的路径下，否则会被主机卷遮蔽。**

### LLMProvider / Embedder 接口（provider.go）

```go
type LLMProvider interface {
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    Ping(ctx context.Context) error
    ProviderType() string
}

type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dim() int
    Ping(ctx context.Context) error
    ProviderType() string
}
```

**为什么是接口不是具体 struct**：agent 逻辑只依赖契约。换供应商 = 加一个实现 + resolver 的 switch 加一个 case，不动 agent 代码。这套接口+resolver 模式照搬自 `internal/storage`（StorageProvider + StorageProviderResolver）。

### 已实现的 provider 类型

| provider_type | 能力 | 实现 | 网络依赖 |
|---|---|---|---|
| `openai_compat` | chat | `openai_compat.go`（OpenAI 兼容 `/v1/chat/completions` + `/v1/models` 模型列表） | 是（中转站） |
| `onnx_local` | embedding | `onnx_embedder.go`（BGE-small-zh int8，docker image 内置，代码直接构造不进 DB） | 否（本地） |

`openai_compat` 覆盖所有 OpenAI 兼容端点（DeepSeek / Moonshot / vLLM / 中转站），只需改 base_url + api_key + model_name。

### 为什么手搓不引框架
代码手写 OpenAI 兼容 HTTP 请求（不引 SDK）、手写 ONNX 推理、手写 ReAct loop。这是有意为之：① agent 决策流在代码里可读，不藏在库后面；② 减少依赖；③ 这是用来学 agent 的项目，理解原理比省代码重要。

## 4. 数据模型

全部新表（核心表只加字段），集中在 `internal/model/<domain>.go (拆分后,见 CLAUDE.md Code layout)` 注册 AutoMigrate。

### Course 新增字段（核心表改动，最小）
```go
AISummaryEnabled bool `gorm:"default:false"`  // 课程级 AI 总结开关（默认关）
AIQuizEnabled    bool `gorm:"default:false"`  // 课程级 AI 出题开关（默认关）
```
课程级开关是 AI 的入口闸门：只有 admin 在课程上勾选启用，该课程的课时才进入 agent 工作范围。这比全局开关更直觉（"不是所有课都需要 AI"），也强化了附加层定位。

### Course.AIConfig：单 JSON 列承载全部 AI 配置（质量优化轮次）

原来单个 `Course.AIHint` 同时喂给两个消费者：Whisper（字幕转录时作 initial_prompt 术语提示）和 LLM agent（出题/总结时作偏好+术语字典）。两者需要的内容完全不同——Whisper 要术语列表/口音提示（≤240 字符），LLM 要出题偏好 + 术语纠错字典 + 难度倾向。单字段塞两份语义混在一起，admin 不好维护。

质量优化轮次把 AI 配置统一收进**单个 JSON 列** `AIConfigJSON`，解析成 `AIConfig` 结构。**设计动机和 `Question.Scoring` 一致——前向兼容：以后加新配置项（难度系数、题型配比、语言偏好…）只需扩 `AIConfig` struct + admin 表单，不必 ALTER TABLE。**

```go
// 存储：单一 JSON 列（text），shape {"whisper_hint":"...", "quiz_hint":"...", ...future}
AIConfigJSON string `gorm:"column:ai_config_json;type:text"`
// DEPRECATED，保留一个迁移周期供 Effective* 回退。删除计划见 TODO.md。
AIHint       string `gorm:"type:text"`

// 解析后的结构（加新配置项就加字段，不改 schema）：
type AIConfig struct {
    WhisperHint string `json:"whisper_hint,omitempty"` // 喂 Whisper：术语/口音，≤240 字符
    QuizHint    string `json:"quiz_hint,omitempty"`    // 喂 summary/quiz/advice LLM：出题偏好 + 术语纠错字典
    // Future: DifficultyBias / QuestionTypeMix / Language ... 加字段即可
}
// 读写方法：Course.AIConfig() 解析、Course.SetAIConfig(cfg) 序列化。
```

- `WhisperHint`：示例 `"象棋术语：车马炮兵卒将帅士仕相象，屏风马，中炮，巡河炮。老师带南方口音。"`。transcriber.py 截断到 240 字符（Whisper prompt 预算 ~244 token）。
- `QuizHint`：除了出题偏好/难度，还承载**术语纠错字典**——Whisper 同音错字（"车"被转成"居"、"和棋"被转成"合棋"），LLM 输出时要按字典纠正（只改 LLM 输出，不改已落库的字幕）。
- **Effective\* 回退方法**（`EffectiveWhisperHint()` / `EffectiveQuizHint()`）：从 `AIConfig()` 解析新字段；旧课程 `AIHint` 有值、JSON 里对应字段空时回退到旧列，保证迁移前创建的课程继续工作到 admin 重新保存。
- **消费方零感知**：service/handler/agent 都调 `Effective*` 方法，不知道底层是独立列还是 JSON。admin DTO 对外仍暴露 `whisper_hint`/`quiz_hint` 双字段（前端友好），service 层组装成 `AIConfig` 再 `SetAIConfig`。
- admin 前端按科目提供默认模板（数学/象棋/语文/英语/物理/化学），admin 在课程表单点"套用科目模板"即填入，可继续微调。模板常量在 `frontend-admin/src/lib/aiHintTemplates.ts`。

> ✅ **第一轮打通了开关三层断链**。早期 `AISummaryEnabled/AIQuizEnabled` 只在 model 上声明，admin 改不了——handler struct、service 签名、create/update DTO 都没绑这两个字段，开关实际一直是 false。第一轮把三层都接上：`admin_content.go` 的 create/update DTO 加 `ai_summary_enabled/ai_quiz_enabled`，`CourseService.CreateCourse/UpdateCourse` 签名补两个 bool 参数，`UpdateCourse` 还会 diff 旧值——`off→on` 时触发 `EnqueueSegmentForCourse` 回填历史字幕的 segment job（开关关着时落地的字幕没产生任何 AI 工作，开关打开那一刻补一次）。客户端 DTO（`client_dto.go`）也回显这两个开关，前端据此决定是否渲染 AI 入口。

### AI 私有表

**`ai_providers`** — provider 配置（参照 storage_sources）
```
id, capability(chat|embedding|rerank), name, provider_type(openai_compat|onnx_local),
base_url, api_key, model_name, extra_json, is_enabled
```
API key 明文存储（与 storage_sources 同级；at-rest 加密是独立的跨模块 PR）。

**`content_chunks`** — RAG 语料层（多源，来源无关）
```
id, episode_id, course_id, source_type(subtitle|attachment), source_ref,
chunk_index, start_time, end_time, text, embedding(JSON []float32)
```
source_type 为附件（attachment）预留：视频配套 PDF/练习册未来提取后也进这张表，与字幕统一检索。start_time/end_time 让题/答案能"跳转视频时间点"——NULL 表示附件类型（无时间索引）。

**`ai_summaries`** — 总结产物（每 episode 一行）
```
id, episode_id(unique), course_id, summary_json, model_used
```
summary_json 是结构化 JSON：`{headline, key_points[], concepts[], takeaway}`。concepts 供后续出题检索用。

**`knowledge_memory`** — ✅ 学习状态（Phase C 核心，反馈循环载体）
```
id, user_id, episode_id, course_id, chunk_id(知识点单元),
mastery(0.0-1.0), correct_count, wrong_count, last_reviewed
uniqueIndex(user_id, chunk_id)
```
mastery 是"这个学生对这个知识点掌握到什么程度"。答题更新它，下次出题读它——这是让系统成为 agent（状态驱动、自适应）而非无状态出题脚本的关键。衰减曲线留 Phase D。

> ✅ **第二轮加了跨课程聚合查询**。原来的 `Masteries(userID, episodeID)` 只能查一节课。advice/course_summary/user_report 三级 agent 要看更宽的视野，所以 repo（`ai_content_repo.go`）新增：
> - `GetCourseMasteries(userID, courseID)` — 取该学生在某课程下所有课时的 mastery 行（`KnowledgeMemory` 已冗余 `course_id`，一次 WHERE 即可，无需 join）。
> - `GetSubjectMasteries(userID, subjectID)` — 科目级聚合，JOIN `courses(courses.subject_id = ?)` 取该科目下所有课程的 mastery，用于"整个数学学得怎么样"。
>
> 两者的结果都按 mastery ASC 排序（弱点优先），让 agent 先看到最需要加强的知识点。这是 advice/user_study agent 的核心数据源。

**`quizzes` / `questions` / `answers`** — ✅ 出题表（Phase C，第二轮扩展；质量优化轮次加 Scoring 列 + multi_choice 题型）
```
quizzes:   id, episode_id, user_id, course_id, difficulty, agent_feedback,
           status(active|archived), archived_at, submitted_at, created_at
questions: id, quiz_id, chunk_id, type(choice|multi_choice|fill), stem, options(JSON),
           answer(choice index, DEPRECATED), answer_text(fill JSON[], DEPRECATED),
           scoring(JSON, 判分元数据), explanation, has_jump, created_at
answers:   id, question_id, quiz_id(denormalized), user_id, user_answer, user_answer_text, correct, answered_at
```
question.chunk_id → content_chunk.start_time 实现题目跳转视频时间点。

质量优化轮次的 schema 变更：
- **`questions.scoring` 列（JSON，判分元数据）** — 按题型解析判分元数据，前向兼容设计，**加新题型只扩 Scoring schema，不必改表结构**：
  - `choice`：`{"correct_index":N}`
  - `multi_choice`（新题型）：`{"correct_indices":[0,2,3],"partial_credit":true,"min_correct_for_half":1}`
  - `fill`：`{"accept":["12","十二"]}`
  - 未来 `judge`/`order`/`short_answer` 也走 Scoring
- **`questions.type` 扩展枚举值**：`choice | multi_choice | fill`（未来可加 `judge | order | short_answer`）。
- **`questions.answer` / `questions.answer_text` DEPRECATED**：新数据改走 `Scoring.correct_index` / `Scoring.accept`。保留兼容老数据/老 prompt——grading 优先读 Scoring，空则回退老字段。
- **`answers.user_answer_text`**：填空题学生原文持久化（第三轮加），`multi_choice` 题型的多选答案走 `user_answer` 编码（见 §17）。

第二轮新增/变化的字段：
- **`quizzes.status` / `archived_at`** — quiz 历史保留。`regenerate`（换题）不再删旧 quiz，而是把当前 quiz 标 `archived`（设 `archived_at`）+ 插新的 `active` 行。旧卷子只读保留，学生可在历史面板 review。单 active 不变量靠**部分唯一索引**强制（`WHERE status='active'`，GORM 表达不了 partial index，AutoMigrate 后用 raw SQL 建，见 `migrateQuizActiveUniqueIndex`）。
- **`quizzes.submitted_at`** — 统一交卷标记。第二轮做题流程改成"全部做完一次提交 = 一次考试"，点"提交全部"后填这个时间戳，quiz 锁定不可再改。用专门字段（而不是"是否存在 answer 行"）判断交卷状态（历史上单题 submit 端点也产生 answer 行会干扰判断，该端点已在第四轮数据清零重整中删除，见 §18）。
- **`questions.has_jump`** — agent 出题时判断每题是否对应明确视频片段。能锚定到具体 chunk 的题 `has_jump=true`（答错可跳视频复习）；贯穿全文/综合性的题 `has_jump=false`（无单一跳转锚点，不出跳转按钮）。默认 false 兼容老数据。前端据此决定渲染不渲染跳转按钮。
- **`answers.quiz_id`** — denormalized snapshot，让历史答题列表在 regen 删旧 question 后仍能展示（详见 §14）。

**`ai_jobs`** — 异步 job 队列
```
id, job_type(segment|summary|quiz|advice|course_summary|user_report),
episode_id, course_id, user_id(nullable), payload_json, priority,
status(queued|processing|done|failed|skipped), attempt, claimed_at, completed_at, error, progress
```
第二轮新增/变化的字段：
- **6 种 job type**：原 segment/summary/quiz 三种 + 新增 advice/course_summary/user_report 三种（详见 §5）。
- **`payload_json`** — job 类型特定参数。advice job 存 `scope`/`scope_id`/`subject_id`（因为 AIJob 表是 episode-centric 的，subject 级 advice 没有专门列）；其它 job 留空。宽松解析，容忍缺字段。
- **`priority`** — 真正按优先级排队了（详见 §5）：quiz=10（学生正等着）、segment=2、summary/advice/course_summary/user_report=1。`ClaimNextQueuedJob` 按 priority DESC 取最高优先级的 queued job。
- **`user_id`** — quiz/advice/user_report job 绑定具体用户（per-user 自适应）；segment/summary/course_summary 留 NULL。

**`ai_runs`** — agent 决策痕迹（可观测性核心）
```
id, job_id, capability(summary|quiz|chat), input_json,
prompt_tokens, completion_tokens, model_used, response_text,
self_check_result(pass|fail|skipped), self_check_note, duration_ms
```
每次 LLM 调用都写一条。admin 观测页可回放 agent 怎么决策的——既是排查工具（题出得烂→看那次 run 的 prompt 和召回），也是学习素材（看 agent 的思考过程）。

**`chat_sessions` / `chat_messages`** — ⏳ Phase D 预留

**`study_advices`** — ✅ 学习建议（Phase C 第二轮，advice agent 产出）
```
id, user_id, scope(episode|course|subject), scope_id, advice_text(text),
mastery_snapshot_json, model_used, generated_at
uniqueIndex(user_id, scope, scope_id)
```
按 `(user, scope, scope_id)` 唯一存储：episode 级是某节课交卷后的复习建议，course 级是某门课的整体弱点分析，subject 级是某科目跨多门课的弱点分析。重新生成替换旧记录（同 quiz 的 upsert 语义，但 advice 不保留历史——建议是"当前快照"）。`advice_text` 是 agent 的自然语言 `FinalText`（不是结构化 JSON，advice 是开放文本）。`mastery_snapshot_json` 存生成时的 mastery 快照，供后续对比"上次建议后进步多少"。

**`ai_course_summaries`** — ✅ 课程级总结（Phase D，course-unique）
```
id, course_id(unique), summary_text(text), model_used, generated_at
```
和 `study_advices` 的关键差异：**按 course 唯一**（不含 user_id），是纯内容总结——课程整体脉络 + 学习路径建议，与具体学生无关。所有学生共享同一条总结，admin 生成一次即可，不必按 user 重复跑（不同学生的"针对建议"走 advice，那是 per-user 的）。`summary_text` 是 agent 的自然语言 `FinalText`（课程导览，不是结构化题库）。重新生成替换旧记录。

**`user_study_reports`** — ✅ Admin 用户学习报告（Phase E，user-unique）
```
id, user_id(unique), report_text(text), model_used, generated_at
```
和 advice 的差异：advice 是给学生本人看的复习建议（单 scope），user_study_report 是给 admin（老师/家长）看的"这个学生跨所有课程学得怎么样"的画像报告。每用户一份最新报告（unique on user_id），重新生成替换。`report_text` 是 agent 的自然语言 `FinalText`。agent 走和 advice 同一套 ReAct loop，但工具集是 user_study 专用（按参数 course_id 查任意课程，见 §16）。

**`wrong_book_items`** — ✅ 错题本 curation 状态（TODO.md P0）
```
id, user_id, question_id (unique on user_id+question_id),
chunk_id, course_id, episode_id, subject_id (冗余,聚合省 join),
first_wrong_at, last_attempted_at, attempt_count, correct_streak, mastered, mastered_at
```
错题本只存学生侧的 curation 状态（掌握标记 / 重做次数 / 连对次数），**题面永远现查 `questions` 表**（通过 `question_id` join），不冗余拷贝题面——这样题目被 regenerate 替换后错题本仍指向真实题面，题被删时 FK CASCADE 自动清孤儿行（`OnDelete:CASCADE` 到 question）。
冗余 `course_id/episode_id/subject_id/chunk_id` 遵循 `answers.quiz_id`、`content_chunks.course_id` 的既定模式：错题本列表要按科目/课程/知识点过滤聚合，冗余这些 ID 让查询一次 WHERE 就够，不必多表 JOIN。值在交卷 hook 时从 `answer→quiz→course→subject` 的 join 链快照下来（course 改科目不回溯，可接受：错题本是学生练习流水，不是课程元数据从表）。

**维护点**：交卷时（`SubmitAllQuizAnswers`）对每道 `correct=false` 的题 upsert 本表（新建则 `first_wrong_at=now, attempt_count=1`；已存在则 `attempt_count++` + `correct_streak` 清零——答错打断连对）。**漏选（multi_choice 部分对）按"错"处理**，和 mastery 同口径——2026-07-23 改的一致判定，避免旧版"漏选既算错进错题本又不算错不扣 mastery"的自相矛盾。重做流（`SubmitWrongBookRedo`）答对→`correct_streak++`（**连对 3 次**才 `mastered=true` + streak 归 0，见 `IncrementCorrectStreak`/`wrongBookMasteredThreshold`，避免一次蒙对就清除）；答错→`attempt_count++` + streak 清零。手动标记掌握（`MarkMastered`）也清零 streak（重新累计）。重做**不**落 `answers` 行、**不**改 quiz-side mastery（和正式 quiz 交卷隔离，避免污染答题流水统计）。
**nil-safe**：`wrongBookRepo` 未注入时所有错题本方法返回空/无操作（守纯附加层铁律 #6，老测试降级）。

**`exams` / `exam_questions` / `exam_answers`** — ✅ 课程考试（TODO.md P0，阶段综合测评）
```
exams:        id, user_id, course_id (partial unique on user_id+course_id WHERE status='active'),
              status (active|archived), archived_at, submitted_at (交卷锁), score (0-1 得分率)
exam_questions: id, exam_id, question_id, chunk_id (冗余), source (pool|generated), order_idx
exam_answers:   id, exam_id, exam_question_id, user_id, question_id, chunk_id (冗余),
                user_answer, user_answer_text, correct, answered_at
```
和 Quiz 完全平行，但 scope 是 (user, course) 而非 (user, episode)：一张卷综合某课程多个 episode 的知识点。**题源当前是纯题库抽**（`SelectExamQuestions`，按学生 mastery 弱点加权 + 覆盖度约束，从已有题库跨 episode 抽，不跑 LLM），`source` 字段预留 `'generated'` 给 quizzer agent 后续出迁移题。

**答案写独立 `exam_answers` 表，不污染 `answers`**（错题本聚合 / quiz 答题流水统计）。mastery feedback 走同一套 `KnowledgeMemory.RecordAnswer`（考试交卷也更新掌握度，让 agent 下次出题反映阶段考试弱点）。**漏选按"错"处理**，和 quiz / 错题本同口径（2026-07-23 统一判定）。**交卷锁**复用 `TryMarkExamSubmitted` 条件 UPDATE（消除 TOCTOU）。
**题被删兜底**：`ExamQuestion` 指向 `questions` 但不带 FK CASCADE（刻意：考试历史该保留题面快照，不被 regenerate 删题连带清）。若题真被删，取卷视图塞 `(本题已删除)` 占位（不静默跳过让卷子少题）；交卷时该题判不了分，给 `correct=false + (本题已删除,不计分)` 占位结果，且**不计入得分率分母**（用实际判了分的题数作分母，避免被删的题虚降学生得分），也不写 `exam_answers` / 不污染 SourceQuality 统计。
**nil-safe**：`examRepo` 未注入时 `GetExamStatus` 返回 unavailable、`StartExam/SubmitExam` 报"考试功能未启用"、admin 观测返回零值（守纯附加层铁律 #6）。

## 5. 状态机（ai_jobs）

```
                    enqueue
        ┌──────────────────────────┐
        ▼                           │
     queued ──ClaimNextQueuedJob──▶ processing ──成功──▶ done
        │   (按 priority DESC        │
        │    选最高优先级)            │
        │                              ├──失败──▶ failed ──retry──▶ queued
        │                              ├──跳过──▶ skipped
        │                              └──claimed_at > 30min──▶ reaper 复位 ──▶ queued
        └──────────────────────────────┘
```

**与字幕队列的区别**：AI job 是**进程内 worker**（`ai_service.go runWorker`，单 goroutine 轮询 3s），不是外部机器认领。一个 goroutine 拿到 job 就独占处理到结束。原子 claim（`ClaimNextQueuedJob`，单条 `UPDATE...RETURNING`）仍然保留，为将来并行化 worker 留余地。

### 6 种 job type

worker（`runWorker` → `processOneJob`）认领并分发以下 6 种 job（详见各 `run*Job` 方法）：

| job_type | 触发 | 优先级 | 跑什么 |
|---|---|---|---|
| `segment` | 字幕完成 hook / admin 开关 off→on 回填 / admin 手动 | 2 | SRT → content_chunks 切片 + embedding |
| `summary` | segment job done 链式 / admin 手动 | 1 | summarizer 单次 LLM 调用 → ai_summaries |
| `quiz` | 客户端 `GET /ai-quiz` 懒生成 | **10** | quizzer agent loop → quizzes/questions |
| `advice` | 客户端 `GET /ai-advice` 懒生成 / submit-all 后链式 | 1 | advice agent loop → study_advices |
| `course_summary` | admin 手动触发 | 1 | course summary agent loop → ai_course_summaries |
| `user_report` | admin 手动触发 | 1 | user_study agent loop → user_study_reports |

### Priority 排队（quiz 高优先级插队）

`ClaimNextQueuedJob` 不再"先进先出"，而是按 `priority DESC` 选最高优先级的 queued job。设计意图（`ai_service.go` 的 priority 常量）：
- **quiz=10**：学生正盯着屏幕等出题，响应延迟最刺眼，必须最先跑。
- **segment=2**：summary/quiz 的上游，但属于后台批量，不抢在 quiz 前。
- **summary/advice/course_summary/user_report=1**：纯派生/admin 触发，无学生在屏幕前干等（页面轮询显示 generating），低优先级不饿死 quiz。

### Reaper（复位卡住的 processing job）

第二轮加了 reaper（`ReapStaleJobs`，委托 repo，固定 **30 分钟阈值**）。一个 LLM 调用最多 ~30s，加上 ReAct 多轮也就几分钟；`claimed_at` 超过半小时还停在 processing 几乎可以肯定是 worker 挂了（进程被杀、panic 未恢复），重置回 queued 让下一轮 poll 重新认领。admin 也可单条手动复位（`ResetJob`，校验当前必须 processing，否则 409）。reaper 补上了早期文档里"没有 reaper"的缺口——进程内 worker 虽然"拿到 job 就独占到结束"，但进程崩溃会留下孤儿 processing job，必须有人复位。

复用了字幕队列的原子 claim 模式（参照 `subtitle_job_repo.go:145`）：单条 `UPDATE ... WHERE id = (SELECT ... LIMIT 1) RETURNING *`，保证并发安全。

## 6. 后端 API

### Admin 端（`/admin/api/ai/*`，AdminAuthMiddleware cookie 鉴权）

**Provider 配置**
```
GET    /providers            列出（api_key 不回显；admin UI 只展示 chat 行,embedding 不进 DB 对 admin 不可见）
POST   /providers            新建（api_key 必填）— chat 单例,UI 不再暴露"新增"
PUT    /providers/:id        更新（api_key 空 = 不修改）
DELETE /providers/:id
POST   /providers/:id/test   测连通（chat 发测试消息；embedding 加载模型 embed 一句）
POST   /providers/models     用临时 {base_url, api_key} 探测中转站 /v1/models → {ok, models[]}
                            （不需先保存,新建时也能先看有哪些模型再选）
GET    /status               就绪状态 {chat, embedding, rerank, configured}
```

**生成 job + 观测**
```
POST /jobs              {job_type, episode_ids} → {enqueued, skipped}
GET  /jobs?job_type=&status=   → {jobs[], stats{queued,processing,done,failed,skipped}}
GET  /jobs/:id           → {job, runs[]}（决策痕迹回放）
GET  /runs?limit=        → [runs]（最近决策痕迹）
GET  /runs/:id           → 单条决策详情（完整 prompt/response/usage）
```

### 客户端端（`/api/v1/*`，UserAuthMiddleware Bearer token）
```
GET /episodes/:id/ai-summary   读 episode 总结（无总结→404，客户端隐藏卡片）
GET /courses/:id/ai-summary    读课程级总结（course-unique，无总结→404）
```
quiz（受 `IsEpisodeVisible` 访问控制）：
```
GET  /episodes/:id/ai-quiz            拉题（无题→202 generating 懒生成；ready 返回题,不下发答案）
                                       已交卷(submitted_at!=nil)时回填逐题结果(correct/correct_index/
                                       explanation/user_answer_index),重进能 review
POST /episodes/:id/ai-quiz/submit     单题即时判分(兼容保留)→更新 memory→返回结果+解析+跳转时间
POST /episodes/:id/ai-quiz/submit-all 统一交卷(一次考试):一次性判分全部题→逐题返回结果→锁定 quiz
                                       body:{answers:[{question_id, answer_index? | answer_text?}]}
                                       已交卷→409 ErrQuizAlreadySubmitted
POST /episodes/:id/ai-quiz/regenerate 换题(标 archived 只读保留旧 quiz→基于最新 memory 重新生成→202 generating)
GET  /episodes/:id/ai-quiz/history    历史卷子(archived quiz 只读 review,含正确答案+逐题对错)
```
submit/submit-all body：选择题发 `answer_index`，填空题发 `answer_text`。

advice（学习建议，agent 驱动，受 `canAccessEpisode` / `canAccessCourse` 访问控制）：
```
GET /episodes/:id/ai-advice   episode 级建议(无→202 generating 懒生成;ready 返回 advice_text)
GET /courses/:id/ai-advice    course 级建议(跨课时聚合 mastery)
GET /subjects/:id/ai-advice   subject 级建议(跨多门课聚合 mastery)
```
三个 advice 端点都是 lazy 生成 + 轮询（同 quiz 的 202/generating/ready 模式），`advice_text` 是 agent 自然语言输出。

错题本（数据按 `user_id` 键存，只需登录，不需 course access gate——学生查无权课程只会拿到自己的空错题，绝不泄露跨用户）：
```
GET  /wrong-book                  列错题本(?course_id=&mastered= 过滤;0/缺省=全局/全部)
                                  响应{items,unmastered_count}。items 每条带正确答案
                                  (correct_index/correct_text/correct_indices)+解析,
                                  列表卡片点开即可复习,无需进重做流。unmastered_count
                                  是该用户未掌握总数(独立于 items 过滤,给 tab 角标)。
POST /wrong-book/:id/master       手动标记掌握(清零 streak)
POST /wrong-book/:id/unmaster     取消掌握(streak 归 0,重新累计)
GET  /wrong-book/redo             取一批未掌握题做重做卷(?course_id=&limit=,默认10)
POST /wrong-book/redo/submit      重做交卷(body:{answers:[...]})→逐题判分+更新curation
                                  (对→correct_streak++,连对3次才mastered;错→attempt++/streak清零)
```
重做卷不下发正确答案（防作弊）；交卷后逐题 reveal + 解析。重做**不**落 `answers` 行、**不**改 quiz-side mastery（和正式 quiz 交卷隔离）。注意路由顺序：`/wrong-book/redo` 和 `/wrong-book/redo/submit` 必须在 `/wrong-book/:id` 前注册（否则 gin 把 "redo" 当 `:id`）。

课程考试（阶段综合测评，start/submit 受 `canAccessCourse` 访问控制；status 只需登录）：
```
GET  /courses/:id/exam/status   是否可考(gate:题库 ≥3 道才开考)→{available,reason?}
                                题库不足/AI 未配置→available=false
POST /courses/:id/exam/start    开考(组卷:从题库按 mastery 弱点抽题)→返回考试卷(不下发答案)
                                题库不足→409 ErrExamInsufficientPool
GET  /courses/:id/exam          取已开考的 active exam(无→{status:none});已交卷回填结果
POST /exams/:id/submit          交卷(body:{answers:[...]})→逐题判分+写exam_answers+更新mastery→报告
                                已交卷→409 ErrExamAlreadySubmitted
```
题目不下发正确答案（防作弊）；交卷后逐题 reveal。答案写独立 `exam_answers` 表（不污染 `answers`）。注意路由顺序：`/courses/:id/exam/status`、`/start` 在 `/courses/:id/exam` 前注册（显式排前更清晰，虽然 gin 静态段优先）。

### Admin 观测端（Phase C 新增）
```
GET /admin/api/ai/summaries/:episodeID   读已生成的总结内容（admin 回放）
GET /admin/api/ai/users/:userID/quizzes  列出某用户所有题库（用户视图入口）
GET /admin/api/ai/quizzes/:quizID        题库详情：题+答案+答题历史+memory+agent_feedback+ai_runs(trace)
GET /admin/api/wrong-book/stats          错题本全局统计(TODO.md P0):总数/未掌握/本周新增/
                                        高频错题榜(top_frequent)+科目弱点分布(by_subject)。
                                        每聚合独立降级(对齐 DashboardStats),AI 未配置返回零值。
GET /admin/api/exam/stats                课程考试全局统计(TODO.md P0):考试卷总数/已交卷/平均得分率/
                                        本周新开考 + 题源质量对比(source_quality: pool vs generated
                                        正确率,验证迁移题难度)。AI 未配置返回零值。
```

### Admin 生成端（第二轮新增，admin 触发 agent 跑）
```
POST /admin/api/ai/courses/:id/course-summary  触发某课程的课程级总结(course-unique,admin 生成一次所有学生共享)
GET  /admin/api/ai/courses/:id/course-summary  读已生成的课程总结(无→generating/404)
POST /admin/api/ai/users/:userID/study-report  触发某用户的跨课程学习报告(user-unique,admin 视角)
GET  /admin/api/ai/users/:userID/study-report  读已生成的用户报告(无→generating/404)
```
这四个端点都是 admin 手动触发（入队 course_summary/user_report job）+ 轮询读。POST 返回 generating 状态，前端轮询 GET 直到 ready。和客户端 advice 端点的差异：advice 是学生自己打开页面 lazy 触发；course_summary/user_report 是 admin 主动为某课程/某学生生成（admin 可观测 + 可读性是第二轮的重点，admin 能看到 agent 怎么遍历数据）。

### API key 安全约定
- GET **永不回显** api_key（DTO 里置空）
- PUT **空 api_key = 保留旧值**（admin 编辑其他字段无需重输 key）
- 这套约定参照 Settings.tsx 的 admin 密码"留空则不修改"

## 7. 代码组织

### 分层职责
```
internal/ai/           纯 AI 逻辑，不依赖 service/handler（可独立测试）
  provider.go            接口 + DTO（tool calling 字段已定义，Phase C 用）
  openai_compat.go       chat 实现
  onnx_embedder.go       embedding 实现
  resolver.go            provider 选择 + 缓存
  segmenter.go           切片（纯函数，无 DB 依赖）
  agent/                 决策逻辑（summarizer 已有；quizzer/tools/agent 待 Phase C）
internal/repository/
  ai_repo.go             AIProvider CRUD
  ai_content_repo.go     chunks/summaries/jobs/runs CRUD + ClaimNextQueuedJob
internal/service/
  ai_service.go          编排：job worker + 字幕完成 hook + 读接口
internal/handler/
  admin_ai.go            admin 配置 + 观测
  ai_handler.go          客户端读取
```

### 依赖方向
`handler → service → repository → model`，`ai/` 被 service 引用但不反向。`ai/` 包不 import service/handler（保持可独立测试）。

### main.go 接入点（4 处）
1. config 加 `AIModelsDir`
2. AutoMigrate 自动覆盖（model 注册即可）
3. 构造 repo + resolver + aiService（`ai_service.go` 内部启动 worker goroutine）
4. handler 构造 + router 注册

### 字幕完成 hook（Step 2 → Step 3 衔接）
```go
// subtitle_service.go
subtitleJobService.SetOnSubtitleCompleted(aiService.OnSubtitleCompleted)
```
字幕落库 → 回调 → aiService 检查课程是否开了 AI → 入队 segment job。用 callback 而非直接 import，保持字幕服务对 AI 无感知（解耦）。

## 8. 切片设计（segmenter.go）

### 策略：时间窗口 + 边界对齐
- 按 ~90s 目标窗口累积字幕 cue，到目标后在下个 cue 边界关闭 chunk
- **从不在句子中间切**（保留 cue 边界）——一句话不跨 chunk
- 硬上限 180s（防单个超长 cue 拖垮）+ 800 字符上限（防快语速密度爆炸）

### 为什么这个粒度
- 90s 足够覆盖一个子主题（连贯性）
- 又足够小，让检索命中指向具体位置而非"整个前半段"（检索粒度）
- 实测：31min 课 → 27 chunks，每 chunk 几百字，质量良好

### 流程
```
SRT → ParseSRT(cue 列表) → SegmentChunks(累积+边界对齐) → chunks
                                                    ↓
                                         Embedder.Embed(批量) → 存 content_chunks
```

## 9. 总结 agent（Phase B → Phase F 丰富化）

### 为什么总结不做 tool calling
总结是**单次抽取**（读全文 → 提炼要点），不需要多步推理或查外部信息。所以 summarizer 是直接的 Chat 调用（temperature=0，结构化 JSON 输出），不走 ReAct loop。tool calling 留给出题（Phase C）和第二轮的 advice/course_summary/user_report（§16）——那些需要查 memory、检索弱点、跨课程遍历，才是 agent loop 的用武之地。

### 输出结构（Phase F 丰富化）

Phase F 把 summary 从"一串平铺要点"升级到接近真实学习笔记的结构。所有新增字段都是**同一次 LLM 调用**产出——零额外 token 成本，只是 prompt 让模型多输出几个结构。

```json
{
  "headline": "一句话概括（20字内）",
  "key_points": ["3-6个要点"],
  "concepts": ["关键名词，供出题检索"],
  "sections": [
    { "title": "知识点名", "points": ["该知识点的一句话要点"] }
  ],
  "methods": ["这节课讲到的具体方法/技巧/公式，便于速查"],
  "common_mistakes": ["这节课相关的常见错误/易错点，帮学生避坑"],
  "pre_adventure": [
    { "prompt": "开放式思考题（激发好奇心）", "hint": "不剧透答案的思考方向提示" }
  ],
  "takeaway": "给学生的启发（可选）"
}
```

各字段的定位：
- **`sections`** — 按知识点分的小节（`{title, points[]}`），让总结有结构而非平铺。一节课通常 2-5 节，前端可渲染成"知识点卡片"。
- **`methods`** — 单独拎出来的方法/技巧/公式列表，便于速查（区别于 key_points 的"要点"，methods 是可操作的"怎么做"）。
- **`common_mistakes`** — 易错点列表，帮学生避坑。
- **`pre_adventure`** — 3 个课前探险问题（`{prompt, hint}`），开放式思考题 + 一句思考方向提示。**和 summary 同一次 LLM 调用产出**，前端在播放前展示，引导孩子带着问题进入视频。这取代了早期单独的"课前问题"管线。

> 老的 summary（Phase F 之前生成的）没有 sections/methods/common_mistakes/pre_adventure 字段，`json.Unmarshal` 会留 nil。`SummaryResult.normalize()` 统一把所有切片字段初始化成空切片——避免 nil 切片 Marshal 成 `null` 让前端 `.map` 炸。

### 实测质量
31min 小学数学计算课 → 总结准确提炼了"位值原理/对应思想/实境制（凑整）/加减互逆"四个算理 + 9 个 concepts。Phase F 丰富化后 sections/methods/common_mistakes/pre_adventure 都正常产出。

### 输出前纠正 Whisper 同音错字（质量优化轮次）

字幕经 Whisper 转录，同音字经常被转错（象棋课里"车"被转成"居"、"和棋"被转成"合棋"）。这些错字会污染 summary 概念名、concepts 检索词、takeaway 文本。质量优化轮次在 summarizer 输出前加一步**术语纠错**（结合 `Course.QuizHint` 的术语字典）：LLM 输出的 summary 文本按字典做替换纠正，**只改 LLM 输出、不改已落库的字幕**（字幕是历史事实，保留原样）。这让 summary 读起来不再被同音错字打断，也为 quiz/advice 的术语一致性打好基础。

## 10. 并发与正确性设计

### 原子 claim（ClaimNextQueuedJob）
单条 `UPDATE ai_jobs SET status='processing' ... WHERE id = (SELECT id WHERE status='queued' ... LIMIT 1) RETURNING *`。两个并发 worker 不会抢同一个 job：第一个 UPDATE 改了 status，第二个的子查询就找不到它了。RETURNING 把确切行交回，避免 UPDATE 后再 SELECT 的歧义。

### Resolver 缓存
provider 构造结果缓存在内存（chat/embedder 各一个 slot）。admin 改配置后调 `Invalidate(capability)` 清缓存，下次 resolve 重建。**信任 Invalidate 信号**（单进程、所有写路径都经过 admin handler 调它），只在缓存空时查 DB——不在每次 resolve 都查。

### ONNX 全局初始化
`ort.InitializeEnvironment()` 是进程全局 C 状态。用包级 `sync.Once` 保证只初始化一次、永不销毁——不能让每个 embedder 实例各自管理（会踩踏/重复报错）。

## 11. 本地 ONNX Embedding（踩坑记录）

### 技术栈
- `github.com/yalue/onnxruntime_go v1.31.0`（proxy 可直拉，活跃维护）
- 模型：`Xenova/bge-small-zh-v1.5` int8 量化版，23MB，512 维
- tokenizer：手写中文 WordPiece（~150 行，无第三方依赖——常见 Go tokenizer 库都拉不到）

### 硬约束
- **必须配 libonnxruntime 1.26.0 的 .so**：onnxruntime_go header 锁 `ORT_API_VERSION 26`，版本不匹配初始化失败。Makefile `fetch-ai-models` 下载。resolver **运行时发现 `.so` 版本**（`findOnnxRuntimeLib` 扫 `libonnxruntime.so.*`），不硬编码——升级只改 Makefile 一处。
- **不是新 CGO 依赖**：项目本就用 `mattn/go-sqlite3`（cgo 包），backend 一直需要 CGO_ENABLED=1。

### 实测
- 加载 + 推理：106ms（首次），warm 后 1.6ms/次
- 常驻内存：~67MB（模型 23MB + onnxruntime + Go runtime），2c2g 够用

### 资源
- 文件分发：`backend/data/ai-models/`（gitignore）
  - `libonnxruntime.so.1.26.0`（22MB）
  - `bge-small-zh-v1.5/model_quantized.onnx`（23MB）
  - `bge-small-zh-v1.5/vocab.txt`（107KB）
- 获取：`make fetch-ai-models`（幂等，已存在跳过）

## 12. 中转站（chat LLM）

### 实测确认
- 端点：`https://www.hi-code.cc`，OpenAI 兼容协议（newapi/oneapi 搭建）
- 可用模型：7 个（gpt-5.4 / 5.4-mini / 5.5 / 5.6-luna/sol/terra / codex-auto-review）
- **支持 function calling**（tool_calls）：实测返回标准 tool_calls，finish_reason="tool_calls"——Phase C 走标准 function calling，不需要退化到 ReAct 文本协议
- **没有 embedding 能力**：4 个常见 embedding 模型全 503，模型列表无 embedding 模型

### 协议约定
- client 不写死任何供应商/模型名，base_url + api_key + model 全走 `ai_providers` 配置
- 用户后期可能换中转站，协议不变

## 13. Admin 前端

### 已有
- **`AiProvidersSection.tsx`**（Settings 页区块）— provider 配置 CRUD + 测连接（照搬 StorageSourcesSection 模式）
- **`AIWorkflow.tsx`**（独立页）— 观测：job 状态统计+轮询、job 列表、决策痕迹回放（点 run 展开 response_text/input_json）
- **`CourseModal.tsx`** — AI 总结/出题开关（默认关，符合附加层原则）+ **AI 提示双 textarea**（质量优化轮次）：WhisperHint（喂字幕转录，术语/口音，≤240 字）+ QuizHint（喂 summary/quiz/advice LLM，出题偏好 + 术语纠错字典）。两字段在表单里分开编辑，service 层组装成 `AIConfig` 存进 `AIConfigJSON` 单列（详见 §4）。带"套用科目模板"按钮：按当前科目一键填入默认模板（`aiHintTemplates.ts`，覆盖数学/象棋/围棋/语文/英语/物理/化学/生物/历史/地理/政治 11 科 + alias 机制），admin 可在模板基础上微调。

### 信息架构（产品视角重构）

admin 后台左侧导航从"扁平 14 项功能清单"重构为**按运营者任务分组的 5 组可折叠导航**（不再像程序员的模块清单）：

```
概览          控制台（三段式：待办/异常 + 数据概览 + 最近活动流）
内容运营      课程库管理 · 字幕队列 · 阅读室
用户与授权    用户与授权 · 观看历史
AI 运营       AI Workflow · AI 用户视图
系统配置      分类与标签（科目+标签合并页）· 荣誉徽章 · 版本发布 · 系统设置
```

每组带组标题（小字 muted）+ 可折叠（当前组有 active 项自动展开）。科目管理 + 标签管理合并为「分类与标签」单页双 Tab（`Classification.tsx`，URL `?tab=` 保深链），后端两套表不动。

> **第 4 轮（视觉 + 交互重做）变更**：
> - 导航/功能图标全站从 emoji 换成 **lucide-react 线性 SVG**（`LayoutGrid`/`Library`/`Captions`/`Users`/`Bot`/`Tags`/`Medal`/`Package`/`Settings`…），科目/勋章 emoji 同步下线（见下文「图标系统」）。
> - **「文件导入」导航项删除**——它本质是给课程库批量加课时的工具，迁进课程库 PageHeader 的「从文件夹导入」按钮 + Dialog（`courses/ImportDialog.tsx`，复用原 3 步向导逻辑）。
> - 视觉语言整体重做为 **Linear/Notion 风**：中性灰 + 近黑强调（light primary = slate-900、dark primary = 白）、Inter 字体（tabular numerals）、小圆角（10/12px）、border-defined 卡片（去 shadow）、扁平无渐变按钮。详见 §13 末尾「第 4 轮视觉重做」。

### 设计系统（全量重设计的地基）

为消除"程序员感"，建立了一套产品化 UI 原语（`components/PageHeader.tsx` + `components/ui.tsx`）：

- **`PageHeader`** — 粘性顶栏（sticky + backdrop-blur，Linear 标志性交互），标题 + 描述 + 面包屑组名 + 右对齐主操作。所有页面顶部统一使用，替代各页散落的 `<h1>`。
- **`Section`** — 可折叠内容区块（标题 + icon + 计数 badge + 右侧操作 + ChevronRight 折叠箭头），把长页面/抽屉分段。
- **`StatusCard`** — 状态色卡片（ok 绿/warn 琥珀/danger 红/info 中性），控制台待办/异常区用。
- **`TodoItem`** — 可点跳转的待办条目（lucide icon + 标题 + tabular-nums 数字 + chevron）。
- **`ActivityFeed`** — 时间线（最近动态流，新用户/AI 任务/新增课时合并排序，AI 任务带 episode/course 标题）。
- **`Tabs`** — 下划线 Tab 切换（active 用文字色下划线而非彩色）。
- **`DropdownMenu`** — 轻量 ⋯ 下拉（lucide MoreHorizontal 触发），点击外部/Esc 关闭。
- **`SubjectIcon`** — 科目图标组件（见下文「图标系统」）。

> 第 4 轮起，所有原语的 `icon` prop 类型从 `string`（emoji）改为 `ReactNode`（接受 `<LucideIcon size={14}/>`）。视觉皮整体换 Linear/Notion 风：去紫、去渐变、去 glow、圆角调小、信息密度收紧。

### 图标系统（科目/勋章图标，第 4 轮建立）

科目和勋章的图标**不再存 DB**——两端各自维护「标识符 → 图标」映射，渲染时查表，跨平台一致：

- **科目**：前端用 subject **key**（`math`/`english`/`physics`…）映射。admin 端 `lib/subjectIcon.tsx`（`resolveSubjectIcon(key) → lucide 组件`，math→Calculator、english→Languages、physics→Atom…，自定义/未识别 key → `BookOpen` fallback）；Flutter 端 `lib/ui/widget/subject_icon.dart`（`subjectIconData(key) → IconData`，math→`calculate_rounded`…，fallback `book_rounded`）。科目编辑器（`Subjects.tsx`）有「图标预览」实时显示当前 key 对应的图标，颜色取表单里的 color。
- **勋章**：前端用 badge **code**（`first_blood`/`streak`/`subject_math`…）映射。admin 端 `lib/badgeIcon.tsx`（所有勋章共享一个 lucide `Award` 图标，靠 `badgeColor(code)` 算出的颜色环区分类型）；Flutter 端 `main_navigation.dart` 的 `_badgeIcon(code, ruleType)` 映射到 Material IconData。
- **DB 字段已删**：`Subject.Emoji`、`Badge.IconName` 在数据清零重整中删除（见 §18）。科目/勋章的视觉表达完全在前端映射表里，DB 不存显示用元数据。

### 关键交互改造

- **课程库卡片**：整卡头部可点展开（hover 反馈，左侧 chevron 旋转）+ **右侧 3 个直接可见的图标按钮**（解锁节奏/编辑/删除，质量优化轮次从原 ⋯ 三点菜单里拎出来）。"展开/折叠"不再单独占一项——点卡片头部或左侧 chevron 就能展开，菜单里那份是冗余。封面占位用科目图标（lucide/Material Icon，按 subject key 映射）。卡片头信息分主-辅两层（标题 + 点分隔的元信息行）。
- **课程库课时树**（`CourseTree`）：+章节/+课时/**探测时长** 都为常驻按钮（质量优化轮次把只剩 1 项的 ⋯ 菜单降级成普通按钮，整个课程库不再有任何三点菜单）。EpisodeRow 的操作按钮 hover 才出现（`opacity-0 group-hover:opacity-100`），行间视觉更安静。批量工具栏选中态出现。
- **文件导入**：原独立页迁进课程库 PageHeader 的「从文件夹导入」按钮 + Dialog（`courses/ImportDialog.tsx`），3 步向导逻辑不变。
- **用户授权抽屉**（去程序员感核心）：顶加用户概要卡；课程授权**按科目分组**（每组用科目图标 + 全选/清空 + 搜索）；**解锁节奏/积分徽章默认折叠**；**staged 保存**（本地暂存 diff，顶部 sticky「有 N 项未保存」+ 保存/放弃，保存时并行发 per-item grant/revoke，后端无批量端点用 Promise.allSettled + 部分失败提示）。
- **控制台**：三段式（待办/异常置顶 → 数据概览降级 → 最近活动流），全正常时显示绿色「一切正常」。活动流的 AI 任务条目带 episode/course 标题（不只是 model_used）。
- **观看历史**：月历热力图加 max-w 约束，单元格不再撑满全屏。
- **Modal/Drawer 点外关闭**：智能区分「点击遮罩」和「拖拽选区」——只有 mousedown 起点在遮罩层才触发关闭。拖选文字滑出框外不会误关（Linear/Notion 同款行为）。实现见 `ui.tsx` 的 `clickOutsideOnly`（Radix/Headless UI 模式）。

### 框架约定
React 18 + TS + Vite + TanStack Query + Tailwind。无 UI 库（原生 input + 全局 class `.card`/`.input`/`.btn-primary` + 上述设计系统原语）。**每个 mutation 必须 invalidate**（CLAUDE.md 硬规则）。颜色走 tailwind config token，不硬编码。

## 14. ✅ Phase C 实现（出题 agent，已落地）

这是 agent 价值核心。下面记录**已落地**的设计与代码位置。

### 已建的文件
- `agent/agent.go` — ReAct loop（observe→think→act 循环，带详尽注释，★学习重点）
- `agent/tools.go` — tool 定义 + 执行（search_subtitles / get_user_mastery / get_episode_info / get_related_chunks）
- `agent/quizzer.go` — 出题（走 agent loop + LLM self-check 自我修正 + agent_feedback 评价）
- `agent/memory.go` — memory 读写 + mastery 更新（+0.1/-0.2 线性，衰减留 Phase D）
- `agent/prompts.go` — QuizzerSystemPrompt / QuizSelfCheckPrompt / buildQuizUserPrompt
- `agent/grading.go` — GradeChoice / GradeMultiChoice / GradeFill / NormalizeText（填空题归一化判题；Scoring 列解析在内部）
- `vector.go`（ai 根包）— CosineSim / TopK / ParseEmbedding / NormalizeText（纯函数）
- `service/ai_service_quiz.go` — 出题编排：worker runQuizJob + 懒生成 + 答题 + 观测读
- `handler/ai_handler.go`（客户端 quiz 3 方法）+ `handler/admin_ai_*.go (拆分后)`（观测 3 方法）
- 前端：`ai_study_screen.dart`（独立 AI 学习页）+ admin `AIUserView.tsx`（用户视图）

### ReAct loop（agent.go）
```
循环直到 LLM 给最终答案或达步数上限（maxSteps=6）：
  observe: 把上一步工具结果（RoleTool 消息）喂回 LLM
  think:   LLM 推理（function calling 下，工具选择即推理的外化）
  act:     LLM 选调 tool（ToolCalls）或给最终答案（FinishReason != tool_calls）
  执行 tool → 结果作下一步 observation
达上限 → 强制 ToolChoice="none" 逼出最终答案
每步记录 TraceStep{step,thought,action,observation} 进 trace
```
全程聚合 usage 写**一条** ai_runs（capability=quiz），trace 序列化进 `trace_json` 字段。

### 关键设计决策（落地时确定）

**1. 题库模型：单套 active + 可重做/换题/历史保留**
- 「一个学生一节课**始终只有一套 active 题**」靠部分唯一索引强制（`WHERE status='active'`），不再是早期文档里的"删旧插新"——见下
- 「重做」=同一套题再答（`Answer` append-only，每次答题加新行；mastery 累积更新）
- 「换题」=`CreateQuiz` 事务内把当前 quiz 标 `archived`（设 `archived_at`）+ 插新的 active quiz（基于最新 memory 重新生成）。**旧卷子不删，只读保留**，学生可在历史面板 review（`GET /ai-quiz/history`）。这是第二轮 quiz 历史功能的基础（详见 §17）
- `Answer` 和 `KnowledgeMemory` 在换题时**不删**——mastery 代表长期学习状态

**2. 按用户独立 + 懒生成**
- 出题绑定具体用户（`AIJob.UserID`），agent 通过 `get_user_mastery` 工具读 per-user memory → 真·自适应
- 触发：客户端首次 `GET /ai-quiz` 发现无 quiz → 入队 per-user quiz job → 返回 `202 generating` → 客户端轮询（3s）
- admin 批量预热：结构预留（`EnqueueQuiz`），本次不接路由（懒加载够用，省钱符合纯附加层）

**3. 题型：选择 + 多选 + 填空混搭**
- `Question.Type` = `choice | multi_choice | fill`（质量优化轮次新增 `multi_choice`，未来可加 `judge | order | short_answer`，全部走 `Scoring` 列）
- **选择题（choice）**：`Options` JSON 数组 + `Scoring.correct_index`（老数据用 `Answer` 索引，grading 优先读 Scoring）
- **多选题（multi_choice，质量优化轮次新增）**：`Options` JSON 数组 + `Scoring.correct_indices:[0,2,3]` + `partial_credit`/`min_correct_for_half`。支持"以下哪些是 XX 的特征"这类多选正确项的题。grading 三态判分（全对/部分对/错），详见 §17
- **填空题（fill）**：`Scoring.accept` JSON 数组（多等价答案，如 `["12","十二"]`；老数据用 `AnswerText`，grading 优先读 Scoring）
- **填空题仅限唯一答案的知识点**（数学计算/事实），prompt 强约束；主观/辨析一律选择/多选
- 判题：选择题比索引；多选题三态（全对 +0.1 / 部分对按"基本掌握"处理、传 true 不扣分 / 错 -0.2，详见 §17）；填空题 `NormalizeText` 归一化（全角→半角、去标点空格、小写）后与可接受答案精确匹配——**不做模糊匹配**（数学题 11≠12）。注：RecordAnswer 当前只支持 correct=true/false，部分对的"中性态"是 TODO（按 Score 加权 +0.05 之类），现阶段 partial 视为掌握传 true，避免漏选 1 个就扣 0.2 系统性压低 mastery。
- `GradeChoice` / `GradeMultiChoice` / `GradeFill` 纯函数，单测覆盖；`Scoring` 解析在 grading.go 内部，对调用方透明

> **Scoring 列设计动机**：质量优化轮次前，题型判分元数据散落在 `Answer`（choice）+ `AnswerText`（fill）两个列里，每加一个新题型就要改表加列（违反 §18 定档承诺）。`Scoring` 把判分元数据收敛到一个 JSON 列、按 type 解析——加新题型只扩 Scoring schema（向前兼容设计）。老 `Answer`/`AnswerText` 字段保留兼容老数据/老 prompt，grading 优先读 Scoring、空则回退。

**4. memory 两表分工**
- `Answer`（append-only 做题流水）+ `KnowledgeMemory`（汇总掌握度，agent 读）
- submit 时两表都写：先写 Answer 流水，再 `UpsertMemoryOnAnswer`（原子 `INSERT...ON CONFLICT DO UPDATE`，mastery +0.1/-0.2 clamp，count++）
- 不对称增量：答对 +0.1，答错 -0.2——错误信号更强，弱点快速浮现

**5. meta 充分利用**
- `get_episode_info` 工具返回富信息包：标题、**文件名**（从 VideoRelativePath/OriginalRelativePath 提取，常带章节信息如"第3讲_分数加减法.mp4"）、时长、科目、标签、年级、**QuizHint**（喂 LLM 的出题偏好+术语字典；质量优化轮次前是 AIHint）、已生成 summary 的 concepts/key_points
- 文件名帮 agent 快速锁定主题，比纯靠字幕召回更准更省 token
- QuizHint 进 prompt：让 agent 按科目题型倾向出题（数学偏填空）、按术语字典纠正 Whisper 同音错字

**6. 可观测性（学习载体，非附属）**
- `AIRun.TraceJSON`：quiz run 携带 `[{step,thought,action:{tool,args},observation}]`，admin "思考时间线"展开成步骤列表——学 agent 决策流的核心
- `Quiz.AgentFeedback`：LLM 出题副产品（基于 memory 生成弱点分析+学习建议），不额外调 LLM；展示给学生 + admin
- 两个观测视图：AIWorkflow（job 视图，run 详情含 trace 时间线）+ AIUserView（用户视图，选用户→题库→详情：题+答题历史+memory+评价+思考时间线）
- admin 能看 AI 生成的总结内容（`GET /admin/api/ai/summaries/:episodeID`）

### Prompt 质量优化（质量优化轮次，核心改动）

第二轮之后出题跑通，但实测题目质量有几个稳定问题：题干依赖视频上下文（"老师说的这个概念"离开视频就不知所云）、干扰项太弱（一眼排除）、正确答案位置不均衡（总是 B/C）、多选/填空题型几乎不用。质量优化轮次系统性强化了 quizzer 的 system prompt（`agent/prompts.go` 的 `QuizzerSystemPrompt` / `QuizSelfCheckPrompt`），方向如下：

- **上下文自足（硬规则）**：题干必须脱离视频独立成立，禁止"这里"/"老师说的"/"刚才那个"等指代词。题干不写自足的题直接判废重出。
- **反蒙题四原则**：① 三同（错误项与正确项在语法/长度/结构上同形）② plausible（每个错误项都得是合理误答，不能是常识性荒谬项）③ 位置均衡（一份卷子里 A/B/C/D 出现频次接近，不集中在某位置）④ 需要看课（不学这节课也能蒙对的常识题，不算有效检测）。
- **Whisper 同音错字纠正**：输出题干/选项/解析时，按 `QuizHint` 的术语字典纠正（"居"→"车"、"合棋"→"和棋"），**不改字幕原文**——字幕是历史事实保留原样，只改 LLM 输出的文本。
- **题型倾向由 QuizHint 驱动**：不同科目默认题型比例不同（数学偏填空考计算，文科偏选择考辨析），agent 读 QuizHint 决定。
- **multi_choice 题型支持**：新增"以下哪些是 XX 的特征"这类多选正确项的题（详见上面"题型"小节 + §17 的 grading/UI）。

### self-check 新增维度（质量优化轮次）

quizzer 的 self-check（`QuizSelfCheckPrompt`）原本审"答案对不对、题干清晰不清晰"。质量优化轮次加 4 个维度对齐 prompt 的硬规则：

- **题干自足性**：题干是否脱离视频独立成立（"老师说的"这类指代词出现 → fail）
- **蒙题测试**：四个选项里蒙中率是否合理（错误项太弱/常识项太多 → fail）
- **多选题答案合理性**：`correct_indices` 长度是否合理（1 个正确项等于退化为单选、3+ 个正确项要审是否真的多选语义）
- **答案位置均衡**：一份卷子里正确答案位置分布（4 个题里有 3 个 C → fail）

self-check 仍走"判废→重出"循环（agent.go 的 ReAct loop），不通过时让 LLM 重新生成，达步数上限用最后一次结果（记 `self_check_result=fail` + note，admin 观测可见）。

### 砍掉 / 调整的设计
- **80% 弹题已废弃**——改为独立 AI 学习页。播放器加一个 AI 学习入口图标
- **讨论 tab（chat）**仍留未来；advice/course_summary/user_report 已部分占用原 Phase D 的 agent 版图
- **streaming + memory 衰减曲线**仍留未来
- 旧 in-player quiz overlay（post_review_json mock）已清理（死代码清理，第二轮）
- 第二轮做题流程从"单题即时判分"改为"统一提交（一次考试）"，详见 §17。客户端主流程走 submit-all；单题 submit 端点曾在第二轮兼容保留，第四轮数据清零重整时作为死代码删除（见 §18）

### Phase C 验收路径
1. admin 在某课程勾选 AIQuizEnabled，该课程 episode 有切片+总结（Phase A/B）
2. 客户端首次 GET /ai-quiz → 202 generating → 轮询 → ready（含填空题，数学课验证）
3. admin AIWorkflow 看 quiz ai_runs（self-check 列、决策回放展开 trace 时间线、可见工具调用 + memory 数值）
4. 客户端做题 → submit → 反馈 + 解析 + [跳转 xx:xx]（选择比索引、填空归一化）
5. 换题 → agent 读更新后 memory → ai_runs 见 mastery 变化（自适应闭环）

## 15. 演进方向

两轮打磨后，原"演进方向"里很多项已经落地。下面拆成"已实现"和"仍未来"两块，避免重复记录。

### ✅ 已实现（从原演进方向 / 砍掉清单里清掉的）
- AI 开关三层断链打通（admin 真的能开/关 `AISummaryEnabled/AIQuizEnabled`，off→on 回填历史字幕）—— 原本只是 model 字段
- 自动 summary（segment job done 链式触发 summary job，admin 不用手动点）—— 原本 admin UI 没接，summary 永远没人触发
- 课前探险问题（summary 的 `pre_adventure`，和 summary 同一次 LLM 调用产出）
- 做题流程重构（统一提交 + quiz 历史 + has_jump 跳转控制）—— 原本是单题即时判分 + 换题删旧
- quiz 历史保留（regen 标 archived 只读保留，不再删旧 quiz）
- 学习建议 / 课程总结 / 用户报告 agent（advice/course_summary/user_report，全 ReAct agent 驱动）
- summary 丰富化（Phase F：sections/methods/common_mistakes/pre_adventure）
- admin 可读性（admin 能读 AI 生成的总结内容 + 触发课程总结/用户报告）
- 死代码清理（旧 in-player quiz overlay 等）
- **填空题用户原文持久化**：`Answer` 加 `UserAnswerText` 列，交卷后/历史 review 能回放"你当时填的什么"（之前判完就丢，只能看对错）—— 原 §15"仍未来"项，第三轮补上
- **advice 前端展示**：客户端 `AiStudyScreen` 接 `/ai-advice` 端点，展示 advice agent 的自然语言建议（advice 优先，空则回退 quiz 的 `agent_feedback`）—— handoff 列的头号待办，第三轮补上
- advice/course_summary/user_report 三表 Upsert 改 `ON CONFLICT`（原 delete-then-insert），无并发窗口

### ✅ 已实现（质量优化轮次）
- **AIHint → AIConfigJSON（单 JSON 列）**：原 §15"仍未来"里"AIHint 语义过载"的方向落地——不是拆成两列，而是收进单个 JSON 列 `AIConfigJSON`，解析成 `AIConfig` struct。设计动机同 `Question.Scoring`：前向兼容，加新配置项只扩 struct 不改 schema。`Effective*` 方法做老列回退兼容。详见 §4
- **多选题（multi_choice）题型**：原 §18 forward-safe 表里"更多题型（多选/连线/排序）= 加枚举值"的第一项落地。grading 三态判分（全对/部分对/错），前端 checkbox 多选 UI + partial credit。详见 §14、§17
- **advice 门控（无答题记录不生成）**：episode/course/subject 三级 advice，该 scope 下学生没有答题记录时后端直接返回 `status=unavailable`（不入队 LLM job、不轮询、前端隐藏卡片），而不是首次进 AI 页就白烧 token 生成"新学生建议先做题"。只有交卷（submit-all）后才链式触发 advice 重算。详见 §16
- **进 AI tab 暂停视频**：从视频页跳到 AI 学习页前调 `_player.pause()`，避免视频/音频在后台继续播
- **prompt 质量优化**：上下文自足硬规则、反蒙题四原则、Whisper 术语纠正、题型倾向由 QuizHint 驱动、self-check 加 4 个维度。详见 §14
- **科目默认模板**：admin CourseModal 的"套用科目模板"按钮，按科目一键填 WhisperHint/QuizHint（11 科 + alias 机制），admin 在模板基础上微调。详见 §13、`frontend-admin/src/lib/aiHintTemplates.ts`
- **Whisper 协议去兼容债**：`EpisodeInfo.ai_hint` → `whisper_hint`（Python worker + backend 同步改造，handler 只发 `whisper_hint`，不再双发 `ai_hint` 兼容老 worker）
- **submit-all 并发安全（TOCTOU 修复）**：新增 `TryMarkQuizSubmitted`（条件 `UPDATE ... WHERE submitted_at IS NULL`），在落任何 answer/memory 之前抢占交卷锁，消除"并发双交卷重复落 answer + 重复扣 mastery"窗口。详见 §17
- **多选题已交卷回填补全**：`GetQuizForClient`/`SubmitQuizAnswer`/`buildQuizHistoryView` 三处对 multi_choice 的正确答案揭示 + 作答回填统一走三题型 switch（原把 `q.Answer=0` 当多选正确答案、把 `[0,2,3]` JSON 当文本回传的 bug 修复）
- **多选题部分对 mastery 不扣分**：`RecordAnswer` 传 `verdict.Correct || verdict.Partial`（部分对视为掌握），避免漏选 1 个就 -0.2 系统性压低 mastery 误导 advice
- **课程库去三点菜单**：课程卡片 + 课时树顶部的 ⋯ 菜单全部去掉，操作直接露出为图标按钮（编辑/解锁/删除/探测时长）。整个课程库不再有三点菜单
- **Modal/Drawer hook 顺序 bug 修复**：`clickOutsideOnly`（含 `useRef`）从 `if (!open) return null` 之后移到之前，消除 React #310（open 切换时 hook 数变化）

### ⏳ 仍未来
- **chat（原 Phase D 原计划）**：advice/course_summary/user_report 已占了原 Phase D 的"agent 驱动"版图，chat 作为"多轮对话"能力仍未来（复用 RAG + memory，答案带视频时间戳跳转）
- **memory 衰减曲线**（艾宾浩斯，复用 knowledge_memory；目前 mastery 是单调累积，不随时间衰减）
- **streaming 输出（SSE）**：改善等待体验，目前 agent 跑完一次性返回
- **知识点命名标准化**：目前 agent 靠 LLM 推理关联 `chunk.text` 描述知识点（advice 之所以能说"通分掌握不好"，是因为工具把 chunk.text 注入了 observation）。未来可考虑给 chunk 打标签 / 建本体库，让知识点有稳定 ID 而非靠文本相似
- admin 批量预热出题（`EnqueueQuiz` 结构已预留，接路由即可）
- 附件提取入 content_chunks（PDF/练习册，source_type=attachment，schema 已预留）
- rerank（数据量增大后上 rerank API，接口已预留）

## 16. Agent 驱动的学习建议 / 课程总结 / 用户报告（第二轮核心）

这是第二轮体验打磨的核心。三个新能力（advice / course_summary / user_report）都**不是单次 prompt engineering**（不是"把所有数据塞进一个 prompt 让 LLM 写"），而是**全 agent 驱动**——每个都走 `agent.go` 的 ReAct `Run` loop，agent 自己用工具集查数据、自己决定分析深度、自己综合成自然语言输出。

### 关键设计：全 agent 驱动（复用 agent.Run + Toolbox）

三个新 agent 都复用同一套引擎，只换两样东西：

| 维度 | 共享 | 各自不同 |
|---|---|---|
| ReAct loop | ✅ `agent.Run`（observe→think→act 循环，maxSteps 上限，达上限强制 ToolChoice=none 逼出答案） | — |
| Toolbox 结构 | ✅ 同一个 `Toolbox`（register/Execute/Specs） | 工具集不同（每个 agent 注册自己的工具） |
| LLM provider | ✅ 同一个 chat provider（resolver 解析） | — |
| trace + usage | ✅ 同样的 `AgentResult{FinalText, Trace, Usage, Turns}`，写同一条 ai_runs | — |
| system prompt | — | 每个agent自己的 system prompt（`AdviceSystemPrompt` / `CourseSummarySystemPrompt` / `UserStudySystemPrompt`） |
| 工具集 | — | `NewAdviceToolbox` / `NewCourseSummaryToolbox` / `NewUserStudyToolbox` |
| 输出 | — | 都是自然语言（`agentRes.FinalText` 直接用，不解析 JSON） |

为什么不一次性 prompt engineering？因为课程大小、学生数据量差异巨大——小课程几节、大课程几十节；新学生无答题记录、老学生几十条 mastery。一次性塞进 prompt 要么撑爆 context，要么信息不全。agent 模式让 agent 自己按需调工具，大课程/数据多的学生多调几次，小课程少调几次，自适应。

三个 agent 都**无 self-check**（quiz 需要"审题"保证答案正确性；advice/summary/report 是开放文本，无客观正误，只做轻量的非空 + 长度合理性检查）。

### Advice 门控：无答题记录不生成（质量优化轮次）

`StudyAdvice` 的 prompt 本质是"基于学生的 mastery 弱点给建议"。如果一个 scope 下学生**一条答题记录都没有**（mastery 全空），advice agent 只能产出"建议先做几道题"这类空话——白烧一次 LLM token，学生看着也尴尬。质量优化轮次在 service 层（`ai_service_advice.go` 的 `GetOrEnqueueAdvice`）加门控：

- **该 scope 下学生没有 answer 记录 → 直接返回 `status=unavailable`**：不入队 advice job、客户端不轮询、前端隐藏 advice 卡片。
  - episode 级：查该 episode 下该 user 的 answer 行
  - course 级：查该 user 在该 course 所有 episode 下的 answer 行
  - subject 级：查该 user 在该 subject 所有课程下的 answer 行
- **只有交卷（submit-all）后才链式触发 advice 重算**：学生做了一节课的题交卷后，`EnqueueAdviceForEpisode` 异步入队 episode 级 advice job；course/subject 级目前仍走客户端 lazy 生成（已有答题记录后才会入队，门控保证不会白跑）。
- 这套门控和"AI off → unavailable"叠加（前者是"没数据"，后者是"没配置"），客户端只看到一个 `unavailable`，不区分原因（都隐藏卡片）。

> 门控补上了早期 advice agent 的一个浪费点：第一次进 AI 页就触发生成"新学生建议先做题"，token 烧了、学生看不到价值。现在改成"做了题才有建议"，advice 的边际成本花在真有数据可分析的学生身上。

### AdviceAgent（episode / course / subject 三级学习建议）

给学生本人看的"哪里薄弱 + 怎么复习"。三级 scope：

| scope | scope_id | 看什么 | pre-seed 来源 |
|---|---|---|---|
| `episode` | episode_id | 某节课交卷后的复习建议 | `Masteries(userID, episodeID)` |
| `course` | course_id | 某门课的整体弱点分析（跨课时） | `CourseMasteries(userID, courseID)` |
| `subject` | subject_id | 某科目跨多门课的弱点分析 | `SubjectMasteries(userID, subjectID)` |

- **工具集**（`advice_tools.go` 的 `NewAdviceToolbox`，MaxSteps=10）：
  - `get_user_mastery`（**增强版**，返回 mastery + `chunk.text`，让 agent 说"通分掌握不好"而非"chunk#37"）
  - `get_course_mastery`（跨课时聚合，弱点优先）
  - `get_subject_mastery`（科目级聚合）
  - `list_user_courses`（该学生的课程列表）
  - `get_episode_summary`（读已生成的 episode summary，引用知识点名）
- **触发**：客户端 `GET /episodes|courses|subjects/:id/ai-advice` lazy 生成（202 generating → 轮询）；episode 级还在 `submit-all`（交卷）后链式触发（`EnqueueAdviceForEpisode`），学生交完卷就能看到复习建议。
- **pre-seed**：按 scope 预先读对应 mastery 塞进 prompt（`buildAdviceSeed`），省一轮工具调用 + 同时收集 `MasterySnapshot`（存进 `StudyAdvice.MasterySnapshotJSON`，供后续对比"上次建议后进步多少"）。pre-seed 只列 mastery<0.8 的（已掌握的不值得列）。

### CourseSummaryAgent（课程级纯内容总结，course-unique）

给**所有学生**看的课程导览（与具体学生无关，按 course 唯一存储，admin 生成一次即可）。

- **工具集**（`course_summary_tools.go` 的 `NewCourseSummaryToolbox`，MaxSteps=8）：
  - `get_course_episodes`（列课程所有 episode：id + 标题）
  - `get_episode_summary`（**带 episode_id 参数**，按需深入某节课的概念；和 advice 的同名工具区别——advice 版无参数读 toolbox 绑定的 episode）
  - 工具按 courseID 限定，`get_episode_summary` 校验 episode_id 属于本课程，防 agent 越权查别的课程
  - **无 mastery 类工具**（课程总结是纯内容，与具体学生无关）
- **pre-seed**：遍历课程所有 episode + 读每集 AISummary 的 headline，拼成 episode 列表塞进 prompt（`buildCourseSummarySeed`），省掉 agent 逐个调 `get_course_episodes` + N 次 `get_episode_summary` 的初始轮次。agent 一上来就有完整课程结构 + 每集一句话概括，可以直接开写或选择性深入。
- **触发**：admin 手动 `POST /admin/api/ai/courses/:id/course-summary`（admin 生成，前端轮询 GET）。

### UserStudyAgent（admin 用户学习报告，user-unique）

给 **admin（老师/家长）**看的"这个学生跨所有课程学得怎么样"的画像报告。和 advice 的关键差异——advice 绑定单个 scope，user_study 跨一个学生的所有课程，工具必须按参数查任意课程。

- **工具集**（`user_study_tools.go` 的 `NewUserStudyToolbox`，MaxSteps=10）：
  - `list_user_courses`（复用 advice 的实现，绑定 user_study 的 userID）
  - `get_course_mastery`（**按参数 course_id**，不是读 toolbox 绑定的 courseID——agent 自己遍历课程）
  - `get_course_summary`（按参数 course_id 读课程级总结；Phase D 未 merge 时降级提示，agent 据此改用 `get_course_mastery` 的 chunk.text）
  - `get_user_advice`（读该用户已有的 StudyAdvice，复用 episode/course 级分析）
- **pre-seed**：service 层（`runUserReportJob`）预算好"该学生每门课程的概要"（每课程平均 mastery + 最弱知识点，含 chunk.text 线索），塞进 `req.Courses`。`buildUserStudySeed` 按平均 mastery 升序排列（弱项课程在前），让 agent 优先关注需要加强的课程。避免 agent 第一轮就要调 `list_user_courses` + N 次 `get_course_mastery`。
- **触发**：admin 手动 `POST /admin/api/ai/users/:userID/study-report`。

### "说人话"的关键：formatMasteriesWithText

三个 agent 能用自然语言描述知识点弱点（"通分掌握不好"而非"chunk#37 mastery=0.2"），关键在 `formatMasteriesWithText`（`advice_tools.go`）。这个函数把 mastery 行渲染成文本 observation 时，**把 `chunk.text` 片段一起注入**——通过 `chunkID → ContentChunk.Text` join（调用方 `loadChunksForMasteries` 先建全局 map）。

```
- mastery=0.20 (★弱点) | 对1 错4 | 最近复习:3天前
  知识点线索: 通分就是把异分母分数分别化成和原来相等的同分母分数...(截断到120字)
```

agent 据此 observation 就能写出人话建议。这是"目前靠 LLM 推理关联 chunk.text"的体现（未来若给 chunk 打标签/建本体库，知识点会有稳定 ID，见 §15 的"知识点命名标准化"）。

### 代码位置

- agent 决策：`agent/advice.go` + `agent/advice_tools.go`、`agent/course_summary.go` + `agent/course_summary_tools.go`、`agent/user_study.go` + `agent/user_study_tools.go`
- service 编排（GLUE：解析 job → 构造 agent → 跑 → 存产物 → 记 ai_run）：`service/ai_service_advice.go`、`service/ai_service_course_summary.go`、`service/ai_service_user_report.go`
- system prompts：`agent/prompts.go`（`AdviceSystemPrompt` / `CourseSummarySystemPrompt` / `UserStudySystemPrompt`）
- 复用的引擎：`agent/agent.go` 的 `Run` + `agent/tools.go` 的 `Toolbox`

## 17. 做题流程 + 前端体验（第二轮重构）

第二轮把做题从"单题即时判分"重构为"统一提交 = 一次考试"的体验，并配套前端缓存、跳转、历史、播放器入口等一整套打磨。

### 统一提交（一次考试模型）

- **流程**：单题即时判分 → 全部做完**统一提交**。提交前所有题可改，提交后锁定（`Quiz.SubmittedAt` 标记，不可再改答案）。
- **`POST /episodes/:id/ai-quiz/submit-all`**：一次性判分整张卷子，逐题返回结果（correct/正确答案/解析/跳转时间戳），并为每题落 answer 行 + 更新 memory。已交卷 → 409 `ErrQuizAlreadySubmitted`（防重复提交）。
- **并发安全（TOCTOU 修复）**：旧实现"读 `SubmittedAt` 判空 → 落 N 个 answer + memory → 盖戳"非事务，两个并发交卷请求都能过判空、各落一份 answer、各扣一次 mastery。修复：在 GetQuiz 之后、**落任何副作用之前**调 `TryMarkQuizSubmitted(quizID, now)`——条件 `UPDATE quizzes SET submitted_at=? WHERE id=? AND submitted_at IS NULL`，SQLite 行锁串行化，只有一个请求 `RowsAffected>0`（赢家继续），败者直接 409。这把"抢占交卷锁"提前到所有副作用之前，彻底消除窗口。
- **多选题部分对的 mastery 处理**：`RecordAnswer` 当前只支持 correct=true/false（无中性态）。多选题部分对（漏选但没多选错项）传 true（视为"基本掌握"，不扣分）——避免漏选 1 个就 -0.2 系统性压低 mastery 误导 advice。"按 Score 加权"的精确方案记在 TODO。
- **`GET /episodes/:id/ai-quiz` 回填**：quiz 已交卷（`submitted_at != nil`）时，拉题响应按题型回填逐题结果——choice：`correct`/`correct_index`/`user_answer_index`；fill：`correct_text`/`user_answer_text`；multi_choice：`correct_indices`/`user_answer_indices`/`partial`/`missed_count`/`extra_count`（partial 不存在 Answer 表，回填时按 user indices 重算 `GradeMultiChoice` 得到）。学生重进 AI 学习页直接进入只读 review 态，三题型都能看到自己当时选了什么 + 正确答案。
- 客户端状态机：`loading → generating/ready/unavailable → (answering | submitted)`。

### has_jump（跳转按钮控制）

agent 出题时判断每题是否对应明确视频片段（`Question.HasJump`）。能锚定到具体 chunk 的题 `has_jump=true`（答错可跳视频复习）；贯穿全文/综合性的题 `has_jump=false`（无单一跳转锚点）。前端据此决定渲染不渲染跳转按钮——避免给"综合性题目"出一个误导性的"跳到 12:38"。

### 跳转 push 播放器 + disableAiTab（防栈加深）

第二轮把跳转从"pop JumpRequest 回原播放器 seek"改为**push 一个标准 `PlayerScreen`**（带 `initialPosition` = 目标时间戳）：
- `PlayerScreen` 加 `disableAiTab` 参数：跳转 push 出来的播放器**不渲染 AI 入口**，否则学生能在跳转播放器里再进 AI 页、再跳转、再 push……栈无限加深。
- `initialPosition` 优先级最高，进入跳转播放器直接 seek 到目标时间。
- 本页（AI 学习页）留在栈里，交卷结果状态保留；看完视频返回回到 AI 页。

### 本地缓存未提交草稿（QuizDraftStore）

统一提交后，学生在点"提交全部"之前的选择/填空都只活在内存里——APP 切后台被杀、误触返回，做了一半的卷子就没了。`quiz_draft_store.dart`（shared_preferences）按 `(userID, episodeID)` 存未提交草稿：
- key 形如 `quiz_draft.<uid>.<eid>`，多用户共用一台 PAD 时每人每节课草稿隔离。
- 格式：`{choice_picks: {qid: idx}, fill_texts: {qid: text}}`。
- 生命周期：改答案 → `saveDraft`（覆盖写）；提交成功 / 换题 → `clearDraft`；打开页面 quiz 未交卷且有草稿 → `loadDraft` 自动恢复填入。
- **multi_choice 草稿扩展（质量优化轮次）**：多选题的勾选集合走新字段 `multi_picks: {qid: [0,2,3]}`（与 `choice_picks` 分开，避免单选/多选编码混淆），生命周期同上。

### multi_choice 题型 UI + partial credit（质量优化轮次）

多选题是质量优化轮次的新题型，配套前端 + grading 一整套：

- **前端 UI**：multi_choice 题渲染成 **checkbox 多选**（区别于单选 radio）。学生可勾任意数量选项；提交前可改。
- **草稿**：勾选状态走 `multi_picks` 字段（见上），切后台/误返回恢复。
- **submit-all**：客户端把勾选的索引数组 `answer_indices:[0,2,3]` 一起发（区别于单选的 `answer_index`）。
- **grading 三态**（`GradeMultiChoice`）：与 `Scoring.correct_indices` 对比：
  - **全对**：勾选集合 = 正确集合 → `correct=true`，mastery +0.1
  - **部分对**：勾选集合与正确集合有交集但不全等（且 `partial_credit=true`、勾中数 ≥ `min_correct_for_half`）→ 算部分对，mastery +0.0（不奖不罚）
  - **错**：勾选集合与正确集合无交集，或漏选/多选超过阈值 → `correct=false`，mastery -0.2
- **前端反馈**：交卷 review 时每个选项标对/错（正确项打勾、错选项打叉、漏选项提示），让学生看清"我漏了哪个 / 多选了哪个"。

> mastery 加权当前是固定数值（全对 +0.1 / 部分对 +0.0 / 错 -0.2），未来可改成按 Score 加权（如部分对按勾中比例给 +0.03~+0.07），见 TODO.md。

### 进 AI tab 暂停视频（质量优化轮次）

从视频页跳到 AI 学习页（`PlayerScreen` → push `AiStudyScreen`）之前调 `_player.pause()`。早期实现没暂停，学生进 AI 页后视频/音频还在后台播——既浪费带宽，也在学生看 advice/做题时分心。pause 后保留播放位置，从 AI 页返回时学生回到原处继续看。

### quiz 历史（regen 不删旧）

第二轮 quiz 历史功能：regenerate（换题）不再删旧 quiz，而是标 `archived`（设 `archived_at`）+ 插新的 active quiz。旧卷子只读保留，学生可在历史面板 review。
- **`GET /episodes/:id/ai-quiz/history`**：返回 archived quiz 列表，每个含完整题 + 正确答案 + 逐题对错（只读）。总是 200（可能空数组）——没有历史是 regen 前的正常态，不是错误。
- 单 active 不变量靠部分唯一索引强制（`WHERE status='active'`，见 §4）。
- `Answer.QuizID`（denormalized snapshot）让历史答题列表在 regen 删旧 question 后仍能展示。

### 播放器常驻 AI 入口

第二轮把播放器的 AI 入口从"顶栏图标（随顶栏 4s 自动隐藏）"调整为：
- **helper panel 常驻 AI 学习卡片 + 探索任务**：播放器右侧 helper panel 常驻"本节探索任务"（`_buildPreAdventureSection`，数据源是 summary 的 `pre_adventure`）+ AI 学习入口，不再随顶栏 4s 自动隐藏。学生随时能点开。
- **课前探索任务不再前置弹窗**：`course_detail_screen` 的课前 modal（`_showPreAdventureModal`）移除，探索任务直接在播放器 helper panel 常驻显示——不再前置弹窗打断进入视频。

### 代码位置
- 客户端：`frontend/lib/ui/screen/ai_study_screen.dart`（AI 学习页重构：统一提交流 + 状态机 + 草稿恢复 + 跳转 push）、`frontend/lib/service/quiz_draft_store.dart`（草稿持久化）、`frontend/lib/ui/screen/player_screen.dart`（`disableAiTab`/`initialPosition`/helper panel 常驻 AI 卡片 + 探索任务）、`frontend/lib/ui/screen/course_detail_screen.dart`（课前 modal 移除）。
- 后端：`handler/ai_handler.go`（`SubmitAllQuizAnswers` / `GetEpisodeQuizHistory`）、`service/ai_service_quiz.go`（`SubmitAllQuizAnswers` 统一交卷 + `ListQuizHistory` + quiz 拉题时回填逐题结果）、model 层 `Quiz.SubmittedAt` / `Question.HasJump` / quiz active/archived 状态。

## 18. 数据库定档声明（第三轮 schema freeze ／ 第四轮数据清零重整）

> **⚠️ 第四轮（数据清零重整）后，第三轮的「定档」约束已解除。** 用户决定清空线上 DB 重新开始（fresh start），这意味着「不能改列类型/删列/改唯一索引」的约束不再适用——可以直接用全新干净的 schema，不用写 forward-safe 迁移脚本。第四轮借这次机会清理了第三轮累积的 forward-compat 包袱（删列、改列类型、删契约、删死迁移代码）。下文先记录第四轮的重整内容，再保留第三轮定档声明作为历史背景。

### 第四轮重整内容（fresh start，2026-07）

趁线上数据清零，一次性清理了以下 schema 和契约债务。**这些变更假定 DB 是空的**——不写迁移脚本、不做 forward-compat、AutoMigrate 直接生成全新 schema。

**删列**（图标改由前端按 key/code 映射，不存 DB）：
- `Subject.Emoji` —— 科目图标改由前端 `subjectIcon.tsx`（admin）/ `subject_icon.dart`（Flutter）按 subject key 映射，详见 §13「图标系统」
- `Badge.IconName` —— 与 `Badge.Code` 重复且 seed 里大部分是假值；勋章图标改由前端按 code/ruleType 映射

**改列类型**：
- `UserProgress.IsCompleted`：`int`（0/1）→ `bool`。Go 侧 GORM 存 SQLite 仍是 INTEGER 0/1，但 JSON 序列化从 `1`/`0` 变成 `true`/`false`，Flutter `progress.dart` 加 `_parseBool` 兼容三种形态（bool/int/string）

**删契约**（forward-compat 包袱）：
- **单题 `/ai-quiz/submit` 端点**：Flutter 实际只用 `submit-all`，单题 submit 是死代码。删路由 + handler，service 层 `SubmitQuizAnswer` 方法保留（接口惯性，`gradeOneAnswer` helper 被 submit-all 复用）
- **`tags` 逗号字符串契约**：`TagsList()`/`TagsJoined()` 模型方法删除，DTO 改为只发 `tags_list`（数组）+ `tag_ids`。Flutter `course.dart` 的 `tags` 字段从 `String` 改成 `tagsList: List<String>`
- **`watch_minutes` DTO**：`admin_dto.go` 删 `WatchMinutes`（= watch_seconds/60 的兼容字段），admin 改用 `WatchSeconds`
- **`X-User-ID` / `Bearer<integer>` 遗留鉴权拒绝逻辑**：全新安装从未用过这些方案，删 middleware 拒绝代码 + 回归测试
- **`nil SourceID` 全局 fallback 路径**：导入现在必填源，删 episode/reading repo 的 nil-source 查询分支 + import 的 nil-source 分支

**删死代码/死迁移**：
- `migrateAIProvidersTableName`（a_iproviders→ai_providers 重命名）+ `tableExists` helper + `migrate_table_rename_test.go` 整个文件——fresh install 直接生成 `ai_providers`，无需重命名。现网 DB 已在数据清零重整时统一为 `ai_providers`，`AIProvider.TableName()` 固定返回该名，不再需要迁移函数（历史曾有，已删；`a_iproviders` 老表名如仍残留在极老部署，属废弃数据，不自动迁移）。
- `migrateQuizActiveUniqueIndex` 的「DROP legacy idx_quiz_user_ep」步骤（保留 CREATE partial unique index）
- `RemoveDeprecatedDefaults` 空存根
- `ReasonParentGrant` 未实现常量 + Flutter 对应 case
- `Rule*`/`Reason*` 常量在 service 里真正用上（替换硬编码 `"watch_duration"` 等字符串）

**理顺**：
- `PointsLedger.Description`：`size:1024` → `size:255`（实际值都是短句）

> **定档约束恢复时机**：第四轮重整后，新 schema 再次进入「定档」状态——未来除非再次清零数据，否则恢复「只能加列/加表/加枚举值」的约束。第四轮删掉的那些 forward-compat 包袱（TagsJoined、单题 submit、nil SourceID 等）不要再以兼容名义重新引入。

### 历史背景：第三轮定档声明（已被第四轮取代，保留作记录）

> **第三轮把 AI 模块的全部表结构定档。** 之后除非出现无法用"加列/加表/加枚举值"覆盖的需求，不再动现有表的列定义、列类型、唯一索引。目的是让线上升级永远走 GORM `AutoMigrate` 的零风险路径（SQLite 加列不丢数据、不停机），避免"改列类型/删列/改唯一索引"这类需要停机 + 迁移脚本的升级困难。
>
> **注**：第四轮数据清零后，上述约束一度解除并做了列删除/类型变更（见上文「第四轮重整内容」）。下面的 forward-safe 分析是第三轮定档时的判断，作为历史背景保留。

### 当前 schema 已 forward-safe

下表把"未来可能的需求"逐一对照"它需要什么 schema 变更"，确认全部落在安全变更类别：

| 未来需求 | 需要的变更 | 类别 | 风险 |
|---|---|---|---|
| memory 衰减曲线（艾宾浩斯） | `KnowledgeMemory` 加 `decay_*` 列 + 读时算 | 加列 | 零风险 |
| chat 多轮对话 | 用已预留的 `ChatSession`/`ChatMessage`（见下） | 加表内容 | 零风险 |
| 更多题型（多选/连线/排序） | `Question.Type` 加枚举值 + `Scoring` JSON 加 schema | 加枚举值 + 加 JSON 字段 | 零风险（multi_choice 已落地；judge/order/short_answer 同路径，见 TODO.md） |
| 附件提取入 content_chunks（PDF/练习册） | `ContentChunk.SourceType` 加 `"attachment"` 值 | 加枚举值 | 零风险 |
| rerank | `AIProvider.Capability` 加 `"rerank"` 值 + resolver 一个 case | 加枚举值 | 零风险 |
| 多 provider 轮询/failover | resolver 逻辑改，不动表 | 无 schema 变更 | 零风险 |

**"升级困难"只发生在**：改列类型、删列、改/删唯一索引、改主键。当前 schema 里没有这些隐患的设计——所有唯一性都靠建表时定好的 unique index（advice 三列、course_summary on course_id、user_report on user_id、memory on user+chunk、quiz partial active），未来不需要动它们。

### ChatSession / ChatMessage 为什么留空不乱预留

`ChatSession`/`ChatMessage` 表已建（AutoMigrate 注册），但字段是**最小骨架**——只够建表，不够承载真实 chat。这是刻意的：chat 的字段需求未定（可能纯 RAG 检索、可能 agent 多轮、可能带工具调用 trace），现在按猜测加一堆列，真做 chat 时大概率加错，反而要改/删列（触发"升级困难"）。

所以策略是：**留空表骨架，字段等真做 chat 时按当时确定的需求加**。加字段 = 加列 = 零风险，不破坏"定档"承诺。`AIRun`/`TraceJSON` 已经能承载 chat 的可观测性，`ContentChunk` 已经能承载 chat 的 RAG 语料，chat 真正缺的只是会话状态表的具体列——那是"加列"，安全。

### 第三轮的具体 DB 变更（本次定档前的最后一次调整）

1. **`Answer` 加 `UserAnswerText` 列**（text）—— 填补唯一真实结构缺口：填空题用户原文之前判完就丢（`Answer.UserAnswer` 只有 int），交卷后/历史 review 无法回放"你当时填了什么"。加列后，写路径（`SubmitQuizAnswer`/`SubmitAllQuizAnswers`）落原文，读路径（`QuizViewQuestion`/`QuizHistoryQuestion`/admin `QuizDetailAnswer`）回放。
2. **三表 Upsert 改 `ON CONFLICT`** —— `UpsertAdvice`/`UpsertCourseSummary`/`UpsertUserStudyReport` 从 delete-then-insert 改为 GORM `clause.OnConflict`（和 `UpsertMemoryOnAnswer` 同语义）。功能等价、无并发窗口、语义更清晰。纯实现层，schema 不变。
3. **`AIJob.JobType` 注释更正** —— 注释从 `segment|summary|quiz|advice` 更新为 6 种 job type（漏了 course_summary/user_report）。纯注释，列定义（`size:20`）不变，老数据兼容。

这三项之后，AI 模块的表结构进入"定档"状态。

### Admin 重设计轮的 DB 操作（定档后的两处 schema 相关改动）

全量 admin 重设计 + bug 修复轮做了两处和 schema 相关的操作，这里显式留痕（它们是定档后**仅有的**两处，且都是**零风险/一次性的安全变更**）：

1. **FK 约束真正生效**（`cmd/server/main.go`）：DSN 加 `_foreign_keys=on`。这不是 schema 变更（约束一直在 CREATE TABLE DDL 里，只是之前 PRAGMA per-connection 导致池里大部分连接 FK 关闭、约束没触发）。修好后 GORM 声明的 20+ 个 `OnDelete:CASCADE/RESTRICT` 在所有连接生效。**升级安全**：已用真实 dev DB 副本验证 `PRAGMA foreign_key_check` 为空（零违规），AutoMigrate 不会因孤儿行失败。手动 cascade 仍保留作双保险（AI 相关表如 ai_summaries/content_chunks/quizzes 没有 GORM foreignKey 声明，只靠手动 cascade）。

2. **`a_iproviders` → `ai_providers` 表重命名**（历史操作，现已完成）：GORM 默认 snake-case 把 `AIProvider` 解析成 `a_iproviders`（每个大写字母前加下划线再 trim → 难看的名字）。给 struct 加 `TableName()` 返回 `ai_providers` 固定名字。重命名当初由 `migrateAIProvidersTableName`（在 AutoMigrate 之前用 SQLite 原生 `ALTER TABLE ... RENAME TO` 原地重命名）完成；后来做过一次数据清零重整，现网 DB 已全部统一为 `ai_providers`，**该迁移函数已删除**（代码里不再保留，`AIProvider.TableName()` 固定返回 `ai_providers` 即可）。极老部署若仍残留 `a_iproviders` 表，视为废弃数据，不自动迁移。

> **关于"定档"承诺的诚实说明**：这两处是定档后发生的 schema 相关操作。#1 不动 schema（只是让既有约束生效）。#2 是表重命名，技术上属于"定档"想避免的类别，当初用 `ALTER TABLE RENAME` 实现（SQLite 原生、保留数据、幂等、AutoMigrate 前跑），实际升级路径是 `make deploy` 零手动干预；现网已迁完，迁移代码已清理。记录在此提醒未来：表名/列名一旦定，再改就要走这种"AutoMigrate 前 raw SQL 迁移"的路径——能做但要谨慎，且迁完后应及时清理迁移代码、更新本文档。

> **注**：第三轮还有一块 provider UX 改造（embedding 干净化不进 DB、chat 单例表单、模型下拉拉取、docker 卷遮蔽修复）—— 这块**不动 ai_providers 表结构**（embedding 现在完全不用这张表，直接从镜像文件构造），只是改了 admin UI 怎么配 + resolver 怎么找 embedding + Dockerfile/Makefile 的模型路径。详见 §3「Round-3 Provider UX 定档」。表结构定档不受影响。

### 质量优化轮次的 schema 变更（AI workflow 质量优化，2026-07）

全部落在"加列/加枚举值"的安全类别，AutoMigrate 零风险升级，不动既有列定义/列类型/唯一索引：

**加列**：
- **`courses.AIConfigJSON`**（text，JSON）—— 课程级 AI 配置的单 JSON 列，shape `{"whisper_hint":"...", "quiz_hint":"...", ...}`。解析成 `AIConfig` struct（`Course.AIConfig()` / `SetAIConfig()`）。**设计动机同 `questions.Scoring`：前向兼容，加新配置项（难度/题型配比/语言…）只扩 struct + 表单，不必 ALTER TABLE。** 详见 §4。
- **`questions.Scoring`**（text，JSON）—— 各题型判分元数据，按 type 解析。前向兼容设计，加新题型只扩 Scoring schema 不改表。详见 §4。

**Deprecated（保留一个迁移周期，删除计划见 TODO.md）**：
- `courses.AIHint` —— 收进 `AIConfigJSON` 后语义过载。`EffectiveWhisperHint()` / `EffectiveQuizHint()` 方法从 JSON 解析，JSON 对应字段空时回退到老 AIHint 列，保证旧课程继续工作到 admin 重新保存。
- `questions.Answer` —— choice 单选正确索引。新数据走 `Scoring.correct_index`。grading 优先读 Scoring，空回退 Answer。
- `questions.AnswerText` —— fill 可接受答案 JSON。新数据走 `Scoring.accept`。grading 优先读 Scoring，空回退 AnswerText。

**加枚举值**：
- **`questions.Type`**：`choice | fill` → `choice | multi_choice | fill`。`multi_choice` 是质量优化轮次的新题型（详见 §14、§17）。未来 `judge | order | short_answer` 也是加枚举值（判分元数据全部走 Scoring）。

> 本次 schema 变更延续"定档"承诺：只加列 / 加枚举值 / 加 deprecated 注释，不改列类型、不删列、不动唯一索引。deprecated 字段的删除要等下一轮数据清零（或显式迁移脚本），不能在 AutoMigrate 里直接 DROP COLUMN（SQLite DROP COLUMN 有版本/约束限制，且会触发"升级困难"）。

## 19. 部署架构（第三轮：alpine → debian-slim + 三个踩坑）

第三轮实机部署暴露了三个**本地 `make run` 永远测不到**的 docker 特有 bug，叠在一起让 embedding 在服务器上完全跑不通。这里记录定档的修法和教训，避免下次再踩。

### 运行时镜像：alpine → debian:bookworm-slim

**决定**：运行时镜像从 `alpine:3.19` 换成 `debian:bookworm-slim`。builder 阶段也一起换成 `golang:1.23-bookworm`（保证 CGO 编译链接的是 glibc，和运行时一致——避免 musl 编译的 go-sqlite3 在 glibc 运行时 ABI 不匹配）。

**唯一原因**：`libonnxruntime.so.1.26.0`（微软官方 release）是 glibc 版，依赖 `ld-linux-x86-64.so.2`（glibc 动态链接器）+ `__vsnprintf_chk` 等 glibc 符号。Alpine 用 musl libc，没有这些 → ONNX 库加载失败。debian 是原生 glibc，一劳永逸消除 musl/glibc 兼容类问题。

**权衡**：debian-slim (~30MB) 比 alpine (~5MB) 大 25MB，但换来的是"以后任何 glibc 的 .so / cgo 依赖都不会再踩坑"。曾考虑 gcompat（alpine 的 glibc 兼容层），但是补丁方案、不彻底——弃用。

### 三个踩坑（按定位顺序）

**踩坑一：docker 卷遮蔽（embedding 模型找不到）**

`make deploy` 用 `-v ~/data/studyquest-data:/app/data` 持久化 SQLite+字幕。旧 Dockerfile 把 embedding 模型 COPY 到 `/app/data/ai-models/`——**bind-mount 会遮蔽镜像里该路径的内容**（主机目录初始为空），容器看到空目录。**修法**：模型 COPY 到 `/app/ai-models/`（在持久化卷之外），`Makefile` deploy 设 `-e AI_MODELS_DIR=/app/ai-models`。

> 规则：**任何 COPY 进镜像的文件，都不能放在被 `-v` 挂载的路径下**，否则会被主机卷遮蔽。

**踩坑二：Dockerfile spa 阶段 WORKDIR 错位（SPA 永远是旧的）**

spa 阶段 `WORKDIR /build` + vite outDir `../backend/...`（相对当前目录）→ 在 docker 里解析成 `/backend/...`（跳出 /build），而 `COPY --from=spa /build/backend/...` 拿空 → builder 用 `COPY backend/` 带进来的**本地脏 dist** 凑数 → embed 旧 SPA。症状是"代码改了、build 成功、deploy 成功，但页面还是旧的"。

**修法**：spa 阶段 `WORKDIR /build/frontend-admin`，outDir 解析成 `/build/backend/...`（和 COPY --from 一致）。

> 教训：vite outDir 相对路径 + docker 多阶段 WORKDIR 是组合陷阱。本地 `make run` 时 vite 在 `frontend-admin/` 里跑、解析正确，**docker 多阶段的 WORKDIR 和本地 cwd 不一致才暴露**——本地测试覆盖不了 docker 的路径正确性。

**踩坑三：ONNX glibc vs Alpine musl（库加载失败）**

见上面"运行时镜像"——`libonnxruntime.so` 要 glibc，Alpine 是 musl。本地 WSL2 是 glibc 直接能跑，docker 用 alpine 才暴露。

> 共同教训（三个坑都是）：**本地 `make run` 和 `docker build/deploy` 是两套完全不同的构建/运行路径**。前者从不触发 docker 的卷遮蔽、多阶段路径错位、libc 兼容问题。本地测过的 docker 不一定能跑——这是真实的测试盲区。要彻底消除，得让本地也用 docker 跑（加 `make run-docker` 目标，未来工作）。

### 镜像大小现状（已优化）

镜像大小演进：**~665MB（apt ffmpeg）→ ~370MB（ffbinaries 静态，segfault）→ ~241MB（自编译最小 ffmpeg）**。

**当前方案：自编译最小白名单 ffmpeg 7.1.1**（`make fetch-ffmpeg`，`scripts/build-ffmpeg.sh`）。

为什么不是预编译静态版：
- **ffbinaries / johnvansickle 全系列静态构建在新内核上对任何网络协议输入（http/https URL）都 segfault**（exit 139）——probeMedia 走云盘 HTTPS URL 必崩。实测 v6.1 和 v7.0.2 都崩，是 gcc 8 工具链 + 静态 glibc 在新内核 socket 路径的 ABI 不兼容，不是版本问题。这是 ffbinaries 方案被废弃的直接原因。
- BtbN 预编译虽不崩，但是"全量 codec"配置，ffmpeg+ffprobe 两文件 222MB——而本项目只用 ffprobe 读 metadata + ffmpeg 截一帧/提取封面，95% codec 是死重。

自编译用 `--disable-everything` + 白名单开启现实可能遇到的所有格式：
- 容器：mp4/mkv/avi/wmv/flv/webm/ts/rm
- 视频解码：h264/hevc/vp9/vp8/av1/mpeg4/mpeg2/theora/wmv3/vc1/msmpeg4v3/rv30/rv40/flv1/mjpeg
- 音频解码：aac/mp3/ac3/eac3/flac/opus/vorbis/pcm
- 编码：mjpeg（截帧输出）
- 协议：file/pipe/http/https（OpenSSL 后端）

产物 ~17MB（两个二进制），比 ffbinaries 152MB 省 135MB，比 BtbN 全量 222MB 省 205MB。

**编译缓存策略**（关键设计）：不在 Dockerfile 里编译（否则每次 `docker build --no-cache` 或换机器都要等 8 分钟），而是 `make fetch-ffmpeg` 在本地用 docker 编译到 `backend/data/ffmpeg-bin/`（gitignored），Dockerfile 只 COPY。模式照搬已有的 `fetch-ai-models`。改 configure 选项时：`make clean-ffmpeg && make fetch-ffmpeg`。

代码只用到三个 ffmpeg 调用（`episode_service.go`），白名单全覆盖：
- `ffprobe` 探测 metadata（时长/编码/分辨率）
- `ffmpeg -vframes 1` 截一帧做封面（`extractScreenshot`）
- `ffmpeg -c copy` 提内嵌封面（`extractEmbeddedCover`）
不转码，所以不需要全套编码器依赖。

`docker history` 各层占比（自编译 ffmpeg 后）：
- debian-slim 基础 ~75MB
- ONNX 模型 ~47MB（必要，baked-in）
- Go 二进制 ~14MB
- 自编译 ffmpeg+ffprobe ~17MB
- ca-certs/tzdata/curl ~15MB

**已知遗留（不影响主线）**：天翼云 OBS host（`obs-ynkmfy-suzhou-home.obs.cn-jssz1.ctyun.cn`，约占 lesson 视频 80%）对 ffmpeg/ffprobe 的 TLS 连接有偶发 RST 现象。`probeMedia`/`extractScreenshot`/`extractEmbeddedCover` 都已加 `-reconnect` 参数 + 3 次重试（见 `episode_service.go` 的 `runFFmpegWithRetry`），实测成功率约 75-85%。剩余失败的多扫几轮「扫描缺失时长」会逐步补齐——这是云盘 CDN 网络抖动，不是 ffmpeg 问题。
