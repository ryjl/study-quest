# AI Agent 模块（Learning Agent：总结 / 出题 / Chat）

> 技术文档，进 git。对标 `docs/ai-subtitle-queue.md` 的深度，作为本模块的权威架构参考。
> 设计 seed 见 `docs/ai-step3-seed.md`（不进 git）；交接笔记见 `docs/handoff-ai-step3.md`（不进 git）。
> 本文档记录**已落地**的设计；标注 ⏳ 的部分尚未实现。

## 1. 背景与定位

StudyQuest 的视频字幕（Step 1/2 的产物）落库后，AI Agent 模块把它们转化为**结构化的学习辅助**：课程总结、自适应出题、（未来）互动问答。这是整个 AI 模块的核心价值——不是"调 LLM 出题"的脚本，而是基于 memory + RAG **自主决策**、有反馈循环的 agent。

### 三能力，价值递进

| 能力 | 状态 | 用到的 agent 机制 |
|---|---|---|
| ① 总结 | ✅ Phase B | 单次 LLM 调用（无 tool calling） |
| ② 出题 | ✅ Phase C | **ReAct loop + tool calling + self-check + memory 反馈循环**（agent 核心） |
| ③ chat | ⏳ Phase D | 多轮对话 + RAG（复用 Phase C 的检索/memory） |

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
│    agent/                ★ 决策逻辑（summarizer / quizzer...）   │
│                                                                │
│  service/ai_service.go   编排：job worker + 字幕完成 hook       │
│  handler/                admin 配置/观测 + 客户端读取            │
│                                                                │
│  SQLite: ai_providers / content_chunks / ai_summaries /         │
│          knowledge_memory / quizzes / questions / answers /     │
│          ai_jobs / ai_runs / chat_sessions / chat_messages      │
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
| `openai_compat` | chat | `openai_compat.go`（OpenAI 兼容 `/v1/chat/completions`） | 是（中转站） |
| `onnx_local` | embedding | `onnx_embedder.go`（BGE-small-zh int8） | 否（本地） |

`openai_compat` 覆盖所有 OpenAI 兼容端点（DeepSeek / Moonshot / vLLM / 中转站），只需改 base_url + api_key + model_name。

### 为什么手搓不引框架
代码手写 OpenAI 兼容 HTTP 请求（不引 SDK）、手写 ONNX 推理、手写 ReAct loop。这是有意为之：① agent 决策流在代码里可读，不藏在库后面；② 减少依赖；③ 这是用来学 agent 的项目，理解原理比省代码重要。

## 4. 数据模型

全部新表（核心表只加字段），集中在 `internal/model/models.go` 注册 AutoMigrate。

### Course 新增字段（核心表改动，最小）
```go
AISummaryEnabled bool `gorm:"default:false"`  // 课程级 AI 总结开关（默认关）
AIQuizEnabled    bool `gorm:"default:false"`  // 课程级 AI 出题开关（默认关）
```
课程级开关是 AI 的入口闸门：只有 admin 在课程上勾选启用，该课程的课时才进入 agent 工作范围。这比全局开关更直觉（"不是所有课都需要 AI"），也强化了附加层定位。

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

**`knowledge_memory`** — ⏳ 学习状态（Phase C 核心，反馈循环载体）
```
id, user_id, episode_id, course_id, chunk_id(知识点单元),
mastery(0.0-1.0), correct_count, wrong_count, last_reviewed
uniqueIndex(user_id, chunk_id)
```
mastery 是"这个学生对这个知识点掌握到什么程度"。答题更新它，下次出题读它——这是让系统成为 agent（状态驱动、自适应）而非无状态出题脚本的关键。衰减曲线留 Phase D。

**`quizzes` / `questions` / `answers`** — ⏳ 出题表（Phase C）
```
quizzes:   id, episode_id, user_id, course_id, difficulty
questions: id, quiz_id, chunk_id, type(choice), stem, options(JSON), answer, explanation
answers:   id, question_id, user_id, user_answer, correct, answered_at
```
question.chunk_id → content_chunk.start_time 实现题目跳转视频时间点。

**`ai_jobs`** — 异步 job 队列
```
id, job_type(segment|summary|quiz), episode_id, course_id,
status(queued|processing|done|failed|skipped), priority, attempt, claimed_at, completed_at, error, progress
```

**`ai_runs`** — agent 决策痕迹（可观测性核心）
```
id, job_id, capability(summary|quiz|chat), input_json,
prompt_tokens, completion_tokens, model_used, response_text,
self_check_result(pass|fail|skipped), self_check_note, duration_ms
```
每次 LLM 调用都写一条。admin 观测页可回放 agent 怎么决策的——既是排查工具（题出得烂→看那次 run 的 prompt 和召回），也是学习素材（看 agent 的思考过程）。

**`chat_sessions` / `chat_messages`** — ⏳ Phase D 预留

## 5. 状态机（ai_jobs）

```
                    enqueue
        ┌──────────────────────────┐
        ▼                           │
     queued ──ClaimNextQueuedJob──▶ processing ──成功──▶ done
        │                              │
        │                              ├──失败──▶ failed ──retry──▶ queued
        │                              └──跳过──▶ skipped
        └──────────────────────────────┘
```

**与字幕队列的区别**：AI job 是**进程内 worker**（`ai_service.go runWorker`，单 goroutine 轮询），不是外部机器认领。所以没有 heartbeat / reaper / X-Ingest-Key 协议——一个 goroutine 拿到 job 就独占处理到结束。原子 claim（`ClaimNextQueuedJob`，单条 `UPDATE...RETURNING`）仍然保留，为将来并行化 worker 留余地。

复用了字幕队列的原子 claim 模式（参照 `subtitle_job_repo.go:145`）：单条 `UPDATE ... WHERE id = (SELECT ... LIMIT 1) RETURNING *`，保证并发安全。

## 6. 后端 API

### Admin 端（`/admin/api/ai/*`，AdminAuthMiddleware cookie 鉴权）

**Provider 配置**
```
GET    /providers            列出（api_key 不回显）
POST   /providers            新建（api_key 必填）
PUT    /providers/:id        更新（api_key 空 = 不修改）
DELETE /providers/:id
POST   /providers/:id/test   测连通（chat 发测试消息；embedding 加载模型 embed 一句）
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
GET /episodes/:id/ai-summary   读总结（无总结→404，客户端隐藏卡片）
```
Phase C 新增（quiz，受 IsEpisodeVisible 访问控制）：
```
GET  /episodes/:id/ai-quiz            拉题（无题→202 generating 懒生成；ready 返回题,不下发答案）
POST /episodes/:id/ai-quiz/submit     答题→按题型判对错→更新 memory→返回结果+解析+跳转时间
POST /episodes/:id/ai-quiz/regenerate 换题（删旧基于最新 memory 重新生成→202 generating）
```
submit body：`{question_id, answer_index? | answer_text?}`（选择题发 index，填空题发 text）。

### Admin 观测端（Phase C 新增）
```
GET /admin/api/ai/summaries/:episodeID   读已生成的总结内容（admin 回放）
GET /admin/api/ai/users/:userID/quizzes  列出某用户所有题库（用户视图入口）
GET /admin/api/ai/quizzes/:quizID        题库详情：题+答案+答题历史+memory+agent_feedback+ai_runs(trace)
```

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

## 9. 总结 agent（Phase B，已有）

### 为什么总结不做 tool calling
总结是**单次抽取**（读全文 → 提炼要点），不需要多步推理或查外部信息。所以 summarizer 是直接的 Chat 调用（temperature=0，结构化 JSON 输出），不走 ReAct loop。tool calling 留给出题（Phase C）——出题需要查 memory、检索弱点、决定考什么，那才是 agent loop 的用武之地。

### 输出结构
```json
{
  "headline": "一句话概括（20字内）",
  "key_points": ["3-6个要点"],
  "concepts": ["关键名词，供出题检索"],
  "takeaway": "给学生的启发（可选）"
}
```

### 实测质量
31min 小学数学计算课 → 总结准确提炼了"位值原理/对应思想/实境制（凑整）/加减互逆"四个算理 + 9 个 concepts。

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
- **`CourseModal.tsx`** — AI 总结/出题开关（默认关，符合附加层原则）

### 框架约定
React 18 + TS + Vite + TanStack Query + Tailwind。无 UI 库（原生 input + 全局 class `.card`/`.input`/`.btn-primary`）。**每个 mutation 必须 invalidate**（CLAUDE.md 硬规则）。颜色走 tailwind config token，不硬编码。

## 14. ✅ Phase C 实现（出题 agent，已落地）

这是 agent 价值核心。下面记录**已落地**的设计与代码位置。

### 已建的文件
- `agent/agent.go` — ReAct loop（observe→think→act 循环，带详尽注释，★学习重点）
- `agent/tools.go` — tool 定义 + 执行（search_subtitles / get_user_mastery / get_episode_info / get_related_chunks）
- `agent/quizzer.go` — 出题（走 agent loop + LLM self-check 自我修正 + agent_feedback 评价）
- `agent/memory.go` — memory 读写 + mastery 更新（+0.1/-0.2 线性，衰减留 Phase D）
- `agent/prompts.go` — QuizzerSystemPrompt / QuizSelfCheckPrompt / buildQuizUserPrompt
- `agent/grading.go` — GradeChoice / GradeFill / NormalizeText（填空题归一化判题）
- `vector.go`（ai 根包）— CosineSim / TopK / ParseEmbedding / NormalizeText（纯函数）
- `service/ai_service_quiz.go` — 出题编排：worker runQuizJob + 懒生成 + 答题 + 观测读
- `handler/ai_handler.go`（客户端 quiz 3 方法）+ `handler/admin_ai.go`（观测 3 方法）
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

**1. 题库模型：单套 + 可重做/换题**
- `Quiz` 对 `(user_id, episode_id)` 加唯一复合索引 → 一个学生一节课**始终只有一套题**
- 「重做」=同一套题再答（`Answer` append-only，每次答题加新行；mastery 累积更新）
- 「换题」=`CreateQuiz` 事务内删旧 quiz+questions 插新（基于最新 memory 重新生成）
- `Answer` 和 `KnowledgeMemory` 在换题时**不删**——mastery 代表长期学习状态

**2. 按用户独立 + 懒生成**
- 出题绑定具体用户（`AIJob.UserID`），agent 通过 `get_user_mastery` 工具读 per-user memory → 真·自适应
- 触发：客户端首次 `GET /ai-quiz` 发现无 quiz → 入队 per-user quiz job → 返回 `202 generating` → 客户端轮询（3s）
- admin 批量预热：结构预留（`EnqueueQuiz`），本次不接路由（懒加载够用，省钱符合纯附加层）

**3. 题型：选择 + 填空混搭**
- `Question.Type` = `choice | fill`
- 选择题：`Options` JSON 数组 + `Answer` 索引；填空题：`AnswerText` JSON 数组（多等价答案）
- **填空题仅限唯一答案的知识点**（数学计算/事实），prompt 强约束；主观/辨析一律选择
- 判题：选择题比索引；填空题 `NormalizeText` 归一化（全角→半角、去标点空格、小写）后与可接受答案精确匹配——**不做模糊匹配**（数学题 11≠12）
- `GradeChoice`/`GradeFill` 纯函数，单测覆盖

**4. memory 两表分工**
- `Answer`（append-only 做题流水）+ `KnowledgeMemory`（汇总掌握度，agent 读）
- submit 时两表都写：先写 Answer 流水，再 `UpsertMemoryOnAnswer`（原子 `INSERT...ON CONFLICT DO UPDATE`，mastery +0.1/-0.2 clamp，count++）
- 不对称增量：答对 +0.1，答错 -0.2——错误信号更强，弱点快速浮现

**5. meta 充分利用**
- `get_episode_info` 工具返回富信息包：标题、**文件名**（从 VideoRelativePath/OriginalRelativePath 提取，常带章节信息如"第3讲_分数加减法.mp4"）、时长、科目、标签、年级、AIHint、已生成 summary 的 concepts/key_points
- 文件名帮 agent 快速锁定主题，比纯靠字幕召回更准更省 token

**6. 可观测性（学习载体，非附属）**
- `AIRun.TraceJSON`：quiz run 携带 `[{step,thought,action:{tool,args},observation}]`，admin "思考时间线"展开成步骤列表——学 agent 决策流的核心
- `Quiz.AgentFeedback`：LLM 出题副产品（基于 memory 生成弱点分析+学习建议），不额外调 LLM；展示给学生 + admin
- 两个观测视图：AIWorkflow（job 视图，run 详情含 trace 时间线）+ AIUserView（用户视图，选用户→题库→详情：题+答题历史+memory+评价+思考时间线）
- admin 能看 AI 生成的总结内容（`GET /admin/api/ai/summaries/:episodeID`）

### 砍掉的设计
- **80% 弹题已废弃**——改为独立 AI 学习页。播放器加一个 AI 学习入口图标，跳转后 pop JumpRequest 回播放器 seek
- **讨论 tab（chat）**留 Phase D；本次 AI 学习页只有总结 + 练习 tab
- **streaming + memory 衰减曲线**留 Phase D
- 旧 in-player quiz overlay（post_review_json mock）暂不清理，与新独立页并存

### Phase C 验收路径
1. admin 在某课程勾选 AIQuizEnabled，该课程 episode 有切片+总结（Phase A/B）
2. 客户端首次 GET /ai-quiz → 202 generating → 轮询 → ready（含填空题，数学课验证）
3. admin AIWorkflow 看 quiz ai_runs（self-check 列、决策回放展开 trace 时间线、可见工具调用 + memory 数值）
4. 客户端做题 → submit → 反馈 + 解析 + [跳转 xx:xx]（选择比索引、填空归一化）
5. 换题 → agent 读更新后 memory → ai_runs 见 mastery 变化（自适应闭环）

## 15. 演进方向

- Phase D：AI chat（复用 RAG + memory，多轮对话，答案带视频时间戳跳转）
- 讨论 tab 接入独立 AI 学习页
- memory 衰减曲线（艾宾浩斯，复用 knowledge_memory）
- streaming 输出（SSE，改善等待体验）
- admin 批量预热出题（`EnqueueQuiz` 结构已预留，接路由即可）
- 附件提取入 content_chunks（PDF/练习册，source_type=attachment，schema 已预留）
- rerank（数据量增大后上 rerank API，接口已预留）
- 知识点关联图谱（LLM 给切片贴标签 + 建关联）
