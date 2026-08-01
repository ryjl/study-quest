# AI 字幕队列系统（Subtitle Generation Queue）

> 字幕自动生成的基础设施：admin 选哪些 episode 要字幕 → 任务排队 → GPU 机器上的
> whisper worker 认领并转录 → 字幕自动落库 → 播放器立即可用。
>
> 后端 VPS（2c2g）只做协调；whisper 重计算跑在带 GPU 的用户台式机上。本文档描述
> 后端这一侧的队列、协议、状态机与并发设计。字幕系统的**改造决策**（VTT 统一、
> 字幕润色分块+diff、find/replace 输出、断点续润、词汇表挖矿、轻量 log 层）见 §12；
> 跨模块踩坑见 `docs/pitfalls/`。

---

## 1. 背景与拓扑

学习视频存放在 alist/webdav 网盘上，后端通过 `StorageProvider` 能为任意 episode 签出
临时下载直链（带网盘 quirk header，如 115 的 Referer）。字幕一旦进入 `subtitles` 表，
播放器的字幕通道立刻可用（`GetPlayInfo` 已返回字幕列表，`GetSubtitleVTT` 把 SRT 转
WebVTT）——所以这套队列的终点就是 `subtitles` 表，无需新接播放器。

三段式拓扑：

```
admin 面板（浏览器）          后端 VPS（2c2g，纯协调）        GPU 台式机（whisper worker）
──────────────────           ─────────────────────           ──────────────────────────
勾选 episode ──→ subtitle_jobs(queued)
加进队列                        priority/status
                              ↓ claim（原子，现签直链）  ←── GET /claim
                              status=processing
                                                              下载（用返回的直链+header）
                                                              ffmpeg 抽音频
                                                              faster-whisper → SRT
                              收到 SRT ←── POST /complete ──┘
                              SaveSubtitle 落库 + job=done
                              （播放器立刻能用了）
```

VPS 一个比特的视频流量都不背——下载直链指向网盘 CDN，不过 VPS。这正好匹配 2c2g 的
能力边界：它只发任务、收小 SRT。

---

## 2. 数据模型

`backend/internal/model/content.go` — `SubtitleJob`：

| 字段 | 类型 | 说明 |
| :--- | :--- | :--- |
| `ID` | uint PK | |
| `EpisodeID` | uint | 关联 episode（FK CASCADE） |
| `Status` | string | `queued` \| `processing` \| `done` \| `failed` \| `skipped` |
| `Priority` | int | 大的先被认领（同 priority 按 created_at 升序） |
| `Attempt` | int | 每次 ClaimNext 自增；retry 不清零，能看到试过几次 |
| `ClaimedAt` | *time.Time | 最后认领时间；reaper 超时回收的判据 |
| `ClaimedBy` | string | worker 自报 id（`X-Worker-ID` header），可观测用，不做鉴权 |
| `CompletedAt` | *time.Time | |
| `Error` | text | worker 报失败的错误串（截断到 2000 字） |
| `Language` | string | 目标字幕语言，默认 `zh-CN` |

**去重策略**：同一 episode 同时只允许一个非终结（queued/processing）任务，但允许
多个终结任务（done/failed 历史）保留——所以去重**不在 DB 上做唯一约束**，而是在
service 层 `Enqueue` 时 check-then-act（见 §5 的边界说明）。

状态常量（`SubtitleJobQueued` / `SubtitleJobProcessing` / `SubtitleJobDone` /
`SubtitleJobFailed` / `SubtitleJobSkipped`）在 `content.go` 顶部 const 块。

`AutoMigrate` 已注册（`model/migrate.go`），部署时自动建表，无需手动迁移。

---

## 3. 状态机

```
        ┌───────── Enqueue ─────────► queued
        │                               │
        │                          ClaimNext（原子）
        │                               ▼
        │                          processing
        │                          │  │  │
        │             Complete ◄───┘  │  └──► Fail ──► failed
        │                 │            │                   │
        │               done           │              Retry │ Skip
        │                              │                   │
        └──────── ReapStale ◄──────────┘                   ▼
              （超时回收）                              queued / skipped
```

- `queued → processing`：只有 `ClaimNext` 一条路径，原子。
- `processing → done`：`Complete`，**状态守卫**（见 §6）。
- `processing → failed`：worker 主动 `Fail`。
- `processing → queued`：`ReapStale`（认领超 30 分钟无心跳）或 admin `Retry`。
- `failed → queued`：admin `Retry`（attempt 不清零）。
- `failed/skipped` 终结。

**无自动重试**：worker 失败一般是数据问题（无音轨、纯噪音），重试也没用，由 admin
手动决定 Retry 或 Skip。

---

## 4. 后端 API

### 4.1 Admin 端（`/admin/api/*`，AdminAuthMiddleware cookie 鉴权）

挂在 `adminHandler` 上（fluent builder 加了 `WithSubtitleJobService`）。

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| POST | `/admin/api/subtitle-jobs` | 批量入队。Body `{episode_ids, priority?}`，返回 `{enqueued, skipped, reasons}` |
| GET | `/admin/api/subtitle-jobs` | 列队列。Query `status?` `limit?`，join episode 取 title |
| POST | `/admin/api/subtitle-jobs/:id/skip` | admin 手动跳过（终结） |
| POST | `/admin/api/subtitle-jobs/:id/retry` | failed → queued |
| GET | `/admin/api/subtitle-jobs/stats` | 队列统计 + 当前 processing 任务，供面板轮询 |

入队返回的 `{enqueued, skipped, reasons}` 是关键 UX：reasons 是 `{episode_id: code}`
的 map，code 取值 `has_subtitle` / `already_queued` / `entertainment` / `not_found`，
前端据此显示"已加入 3 个；跳过 2 个（已有字幕）"。

### 4.2 Worker 协议端（`/api/v1/*`，IngestKeyMiddleware，`X-Ingest-Key` header）

挂在 `v1Ingest` 组，与现有 ingest 端点并列。**worker 是机器不是浏览器 session**，
所以复用 `X-Ingest-Key`（Python toolchain 共享密钥），不另造中间件。

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| POST | `/api/v1/subtitle-jobs/claim` | 认领下一个任务，返回现签直链 + episode 信息 |
| POST | `/api/v1/subtitle-jobs/:id/complete` | 回传 SRT，落库 + job done |
| POST | `/api/v1/subtitle-jobs/:id/heartbeat` | 刷新 claimed_at，防被回收 |
| POST | `/api/v1/subtitle-jobs/:id/fail` | 报失败 |

`claim` 响应示例：

```json
{
  "job": {"id":1,"episode_id":5,"language":"zh-CN","attempt":1,"claimed_by":"my-desktop"},
  "download_url": "https://...",
  "download_header": {"Referer":"..."},
  "episode": {"id":5,"title":"...","duration_seconds":1200}
}
```

队列空时返回 `{"job": null}`，worker 应 sleep 后再轮询。

`complete` 在以下情况返回非 200：
- `404`：job 不存在（`ErrSubtitleJobNotFound`）。
- `409`：**stale completion**——job 已不在 processing（被 reaper 回收或被 retry 后另一
  worker 已完成），worker 必须丢弃这份 SRT（`ErrSubtitleJobStaleComplete`）。见 §6。

---

## 5. 代码组织

```
backend/internal/
├── model/content.go + model/migrate.go                      SubtitleJob model + 状态常量 + AutoMigrate
├── repository/subtitle_job_repo.go      SubtitleJobRepository（含原子 ClaimNext）
├── service/subtitle_service.go          SubtitleJobService（业务守门 + 状态机）
├── handler/
│   ├── subtitle_job_handler.go          worker 协议（claim/complete/heartbeat/fail）
│   └── admin_subtitle_jobs.go           admin 端（enqueue/list/skip/retry/stats）
├── repository/episode_repo.go           新增 CountSubtitlesByEpisodes / HasSubtitle
│                                        / FindCourseContentType（gate 检查 + DTO 计数）
├── handler/admin_dto.go                 episodeDTO 加 subtitle_count 字段
├── handler/admin_{course,episode,chapter,subtitle}.go (拆分后)             ListEpisodesByCourse / GetCourseDetail 批量填计数
└── cmd/server/
    ├── main.go                          接线 + reaper goroutine + busy_timeout DSN
    └── subtitle_jobs_integration_test.go 7 个集成测试
```

### 分层职责

- **Repo**（`subtitle_job_repo.go`）：纯持久化 + 一个关键的原子操作 `ClaimNext`。
  所有状态翻转方法（MarkDone/MarkFailed/MarkSkipped/MarkQueued）都带状态守卫或清空
  关联字段（见 §6）。
- **Service**（`subtitle_service.go`）：业务规则守门（entertainment 拒绝、已有字幕跳过、
  去重）+ 状态机编排 + 直链现签（`ClaimNext` 调 `episodeService.GetStreamURL`）。
- **Handler**：HTTP 协议适配，把 service 错误映射成 HTTP 状态码。

---

## 6. 并发与正确性设计（重点）

这是这套队列最容易出错的部分，每条都来自 review 中抓到的真实问题。

### 6.1 原子认领（`ClaimNext`）

```go
UPDATE subtitle_jobs
SET status='processing', claimed_at=NOW(), attempt=attempt+1, claimed_by=?
WHERE id = (SELECT id FROM subtitle_jobs
            WHERE status='queued'
            ORDER BY priority DESC, created_at ASC LIMIT 1)
RETURNING *
```

- **单条原子语句**：SQLite 串行化 UPDATE，两个并发 claim 不可能拿到同一行。
- **`RETURNING *` 是关键**：精确返回被翻转的那一行。
  - 早期版本是 UPDATE 后用"最近 processing 任务"回查，但那个回查在并发下是歧义的
    （两个 winner 都读到最新一行，或读到别人 claim 的行）——原子 UPDATE 白做了。
  - `RETURNING` 把结果钉死在本语句触碰的那一行。
  - SQLite 3.35+（2021）支持 RETURNING；`mattn/go-sqlite3 v1.14.22` 满足。

### 6.2 Complete 的状态守卫（防迟到 Complete 写重复字幕）

```go
// repo.MarkDone 带 WHERE status='processing' 守卫
UPDATE subtitle_jobs SET status='done', ... WHERE id=? AND status='processing'
// RowsAffected==0 → ErrSubtitleJobNotClaimed
```

`service.Complete` 的调用顺序（**顺序很重要**）：
1. 先 `repo.MarkDone`（状态守卫翻转 processing→done）。
2. **只有翻转成功**才 `SaveSubtitle` 写字幕。

为什么是这个顺序——防 reaper/retry 竞态：

```
worker A 认领 → 转录超时（心跳丢失）→ reaper 把任务退回 queued
                                    → worker B 认领 → B 完成
worker A 终于转完，发 Complete（迟到的 SRT）
```

如果先写字幕再标 done，worker A 的迟到 SRT 会覆盖/重复 worker B 的结果。先标 done
（守卫），A 的翻转失败（status 已不是 processing）→ 返回 `ErrSubtitleJobStaleComplete`
→ handler 映射 409 → worker A 丢弃 SRT。

**次要的边界**：字幕写入失败但 job 已标 done 的不一致状态——`Complete` 返回错误但不
回滚 done（job done 但无字幕）。admin 可通过现有字幕 UI 手动补。这是权衡：宁可这种
罕见不一致（可人工补），也不要把字幕写早导致重复（不可恢复）。

### 6.3 reaper（崩溃 worker 兜底）

`main.go` 起 5 分钟 ticker 的 goroutine，调 `service.ReapStale`：

```go
UPDATE subtitle_jobs SET status='queued', claimed_at=NULL, claimed_by=''
WHERE status='processing' AND claimed_at IS NOT NULL AND claimed_at < (NOW - 30min)
```

把认领超 30 分钟没心跳的 processing 任务退回 queued。worker 每 ~30s 打 heartbeat 刷
`claimed_at`，所以 30 分钟静默基本等于"worker 挂了"。

reaper 用**独立 context**（`reaperCtx`，不是 probe worker 的 ctx），两个后台循环解耦。

### 6.4 SQLite busy_timeout

CLAUDE.md 早标注"生产没设 busy_timeout"。这套队列引入了并发写（claim 与进度上报
并发），暴露了这个问题——`SQLITE_BUSY` 直接报错而非排队。

修法是在 **DSN** 加 `?_busy_timeout=5000`（per-connection），不是 `PRAGMA Exec`（那个
只设当前一个连接，GORM 连接池里别的连接不生效）。生产 `main.go` 和测试
`testhelper_test.go` 都加了。

### 6.5 Enqueue 去重的非原子性（已知边界，可接受）

`Enqueue` 的去重是 check-then-act（`FindActiveByEpisode` 然后 `Create`），不是原子的。
两个真正并发的同 episode 入队请求理论上能都通过检查、都创建。但：
- 唯一调用方是 admin（单个人类），并发概率极低。
- 重复是自愈的——两个都转录，第二个 Complete 被 §6.2 守卫拒绝为 stale，episode 最终
  只有一个字幕。
- DB 唯一约束无法表达"一个活跃 + 多个历史"，所以不在 DB 层强约束。

代码里有注释说明这个权衡。

---

## 7. Admin 前端

`frontend-admin/`：

- **`pages/courses/CourseTree.tsx`**：课程详情页的批量工具栏加"加入字幕队列..."下拉
  （选优先级），复用现成的多选基建（`Set<number>` + 批量工具栏）。入队成功 toast 显示
  加入/跳过数 + 跳过原因。入队后 invalidate `['episodes', course.id]`。
- **`pages/SubtitleQueue.tsx`**（新增）：队列管理页。状态统计条 + 任务表（状态/Worker/
  优先级/尝试/更新时间/错误 + 重试/跳过操作）+ 状态过滤。running 时 3s 轮询，idle 停。
- **`lib/api/` 域聚合 (拆分后)** + **`lib/types.ts`**：`SubtitleJob` / `SubtitleJobStats` /
  `SubtitleJobEnqueueResult` 类型 + 5 个 api 方法。
- **`components/Layout.tsx`** + **`App.tsx`**：导航"字幕队列" + 路由。

### episodeDTO 的 `subtitle_count`

前端 `Episode.subtitle_count` 字段早就在 type 里预留但一直是死的（后端不返回）。
本系统让后端真正填上它：`episode_repo.CountSubtitlesByEpisodes`（一条 GROUP BY，避免
N+1），`ListEpisodesByCourse` / `GetCourseDetail` 批量填进 DTO。这样课程详情页能显示
哪些 episode 已有字幕（可据此禁用/提示，呼应"已有字幕就跳过"的产品诉求）。

---

## 8. 测试

`backend/cmd/server/subtitle_jobs_integration_test.go`，10 个测试：

1. `TestSubtitleJobEnqueueGate` — gate 规则（entertainment 拒绝、已有字幕跳过、去重）。
2. `TestSubtitleJobEnqueueBatchReturnsSkippedReasons` — 批量入队的 skipped/reasons。
3. `TestSubtitleJobCompletePersistsSubtitle` — Complete 落库 + claimed_by 盖章。
4. `TestSubtitleJobReapStale` — 超时回收。
5. `TestSubtitleJobClaimAtomicity` — 并发 claim 不超过一个 winner（容忍 SQLITE_BUSY）。
6. `TestSubtitleJobClaimDistinctJobsSequentially` — 每次 claim 返回不同且正确的任务
   （抓 §6.1 的 RETURNING 回归）。
7. `TestSubtitleJobCompleteRejectsStaleCompletion` — 防迟到 Complete 写重复字幕。
8. `TestSubtitleJobClaimPriority` — 高优先级先被认领。
9. `TestSubtitleJobAdminHTTPEndpoints` — admin 端 enqueue/list/stats/skip/retry 全链。
10. `TestSubtitleJobWorkerClaimRequiresIngestKey` — worker 端点受 X-Ingest-Key 保护。

**关于并发测试的诚实性**：测试没试图用真 goroutine 并发去测原子性的"行为"
（内存 SQLite + GORM 连接池下会 flaky 报 "database is locked"，那是 SQLite 自身的锁
机制，不是我的代码的保证）。原子性保证来自单条 SQL 语句本身，所以测的是"语句的属性"
（每次返回正确且不同的行），而非"并发执行的行为"。这点在测试注释里写明了。

`docs/dev-setup.md (合并后)` 是 curl 端到端冒烟测，无需 Python worker 即可验证整条
协议链路。

---

## 9. Worker 实现（Step 2，已完成）

Python whisper worker 在 `tools/video-pipeline/`，消费上面的队列协议。

### 9.1 架构

```
tools/video-pipeline/
├── run.sh              启动器：在 shell 层 export LD_LIBRARY_PATH（cublas），再 exec python
├── worker.py           主循环：GPU 预检 → 懒加载模型 → claim/转录/complete 循环
├── api_client.py       后端 4 端点 HTTP 封装 + X-Ingest-Key/X-Worker-ID 鉴权
├── cache.py            本地视频缓存匹配（filename + file_size 双匹配，多目录）
├── audio.py            ffmpeg 抽音频（含 wav/mp4 双层缓存 + 断点续传下载）
├── transcriber.py      faster-whisper 封装（懒加载 + initial_prompt 拼装 + 进度回调）
├── config.py           YAML 配置 + 环境变量覆盖
├── config.example.yaml 配置模板（进 git）
├── pyproject.toml      uv 依赖（含 nvidia-*-cu12 CUDA 库）
└── tests/              cache 匹配 + prompt 拼装的单测（14 个，无需 GPU）
```

模型**只加载一次常驻**：`Transcriber` 在 worker 主循环外创建，首次 `transcribe`
时 `_ensure_model()` 加载，之后所有 job 复用同一实例。不会每个视频重新加载。

### 9.2 initial_prompt 三段拼装

Whisper 的 `initial_prompt` 不是自由指令，是解码器上下文（~244 token 预算），用来引导
风格 + 注入热词。worker 从 claim 响应自动拼三段：

1. **base_prompt**（config）——风格引导，如 `"以下是普通话的句子。"`
2. **课程元数据**（claim 响应自动带）——subject/course_title/chapter_title，学科术语密集
   的标题能压制 Whisper 把专有名词转错。
3. **whisper_hint**（admin 在课程编辑页手填，存 `Course.AIConfigJSON`）——针对性提示，如"老师口音重""重点听 ε-δ 定义"。
   按课程设一次，该课所有 episode 共用。出题/总结 agent 用同一 JSON 列里的 `quiz_hint` 字段（见 `docs/modules/ai/overview.md` §4）。

截断到 240 字符（CJK 保守估计 1 字符 ≈ 1 token）。不需要每个视频手写 prompt。

### 9.3 进度上报

heartbeat 端点请求体加了可选 `progress_ratio`（0.0–1.0）。faster-whisper 的 segment
generator 是惰性的，每产出一个 segment 就回调更新进度。后台心跳线程每 30s 带上当前
ratio 发一次 heartbeat，admin 队列页 processing 行显示百分比。旧 worker 发空 body 照常
工作（向前兼容）。

### 9.4 缓存设计（两层）

**视频缓存**（`cache.py`，配置的 `cache.dirs`）：worker 启动时扫描，按 `filename +
file_size` 双匹配。命中→直接用本地文件，跳过网盘下载（直链 sign 时效问题完全消失）。

**音频缓存**（`audio.py`，`~/.cache/sq-whisper/wav/`）：ffmpeg 抽出的 16kHz mono WAV
按 `(filename, file_size)` 的 hash 存盘。重试时复用，不重新下载/抽音频。下载的 mp4
也缓存在同目录（同 key）。启动时自动清理 7 天以上的旧文件。

### 9.5 GPU 策略（硬约束，不 fallback）

GPU 是硬前提，不降级到 CPU：
- 启动 `gpu_preflight` 检查 `ctranslate2.get_cuda_device_count()`，为 0 直接 `sys.exit(1)`。
- `device` 配置成非 `cuda` 直接报错。
- 模型加载失败（含 OOM）异常冒泡退出。

理由：这是开发机上的批处理 worker，失败时人工介入（关占显存的程序、改 config）比静默
降级到"CPU 转录几小时"更有用——后者只会让任务卡在队列里假装在跑。

### 9.6 claim 响应扩展（Step 2 新增字段）

claim 响应在 Step 1 基础上加了缓存键 + prompt 上下文（全部向前兼容，旧 worker 不读）：

```json
"episode": {
  "id": 5, "title": "...", "duration_seconds": 1200,
  "filename": "lesson01.mp4",       // basename，缓存匹配键
  "file_size": 524288000,            // 字节，缓存匹配键（可能为 null）
  "subject": "math",                 // 科目 key，prompt 用
  "course_title": "高等数学",         // prompt 用
  "chapter_title": "第一章 极限",     // prompt 用
  "whisper_hint": "重点听 ε-δ 定义"   // admin 手填（存 Course.AIConfigJSON），prompt 用
}
```

后端 `ClaimNext` 顺路 join episode→course→chapter→subject 填上。`whisper_hint` 来自
`Course.EffectiveWhisperHint()`（读 `AIConfigJSON` JSON 列，回退老 `AIHint` 列）。
admin 在课程编辑页填，详见 `docs/modules/ai/overview.md` §4。

---

## 10. Worker 实测踩坑记录（WSL2 + RTX 4060）

以下每个都是真实踩到、花了时间排查的坑。换环境大概率会再踩，记录于此。

### 10.1 `Invalid compute type: int8_fp16`

ctranslate2 的合法 compute type 是 `int8_float16`（int8 量化权重 + float16 计算），
**不是** `int8_fp16`。`int8_fp16` 会在模型加载时报 `ValueError`。

合法值（`ctranslate2.get_supported_compute_types("cuda")`）：
`float16, int8, int8_float16, int8_float32, int8_bfloat16, bfloat16, float32`。

4060 8GB 用 `int8_float16`（~3G 显存），给 Windows 桌面占用留余量。

### 10.2 `libcublas.so.12 is not found`（CUDA 库缺失）

**现象**：模型能加载，但 `model.encode()` 时报 `RuntimeError: Library libcublas.so.12
is not found or cannot be loaded`。

**根因**：ctranslate2 在运行时 dlopen `libcublas.so.12` / `libcudart.so.12` 等 CUDA 库，
但不把它们声明为 pip 依赖（它们是"系统级"可选依赖）。WSL2 只装了驱动（`libcuda.so`），
没装 CUDA toolkit（`libcublas` 等）。

**解法**：装 `nvidia-cublas-cu12` / `nvidia-cuda-runtime-cu12` / `nvidia-cufft-cu12` /
`nvidia-cuda-nvrtc-cu12` / `nvidia-nvjitlink-cu12` pip 包（已写入 pyproject.toml）。
它们把 `.so` 放进 `site-packages/nvidia/*/lib/`。

**关键坑**：必须用 `run.sh` 启动 worker——它在 shell 层 `export LD_LIBRARY_PATH` 指向
那些 lib 目录。glibc 的 dlopen 在**进程启动时**读 `LD_LIBRARY_PATH`，Python 进程内
`os.environ` 设置**不可靠**（实测 worker 里设了还是找不到）。`run.sh` 在 python 启动前
export，才能让动态链接器看到。

### 10.3 ffmpeg 读网盘 https 流失败（tls End of file + moov atom not found）

**现象**：ffmpeg `-i <alist直链>` 抽音频报 `[tls] IO error: End of file`，然后
`moov atom not found`。

**根因**：alist 直链 302 重定向到天翼云 OBS（https）。两个问题叠加：
1. ffmpeg 默认不自动重连，TLS 流断了就报 `End of file`。
2. MP4 的 `moov` atom 在文件尾部，ffmpeg 读 https 流要 HTTP range seek 到尾部读 moov，
   但 TLS 流已断 → `moov atom not found`。

**解法**：对 http(s) 输入，**先全量下载 mp4 到本地再 ffmpeg 抽音频**（`audio.py` 的
`_download`）。本地文件没有 range/tls 问题，moov 一定能读到。下载的 mp4 也缓存（同 key），
重试不重复下载。

试过 `-reconnect 1 -reconnect_streamed 1`——能连上但 range seek 时流还是断，moov 读不到。
全量下载是唯一可靠方案。

### 10.4 下载中途 TLS 断流（SSL: UNEXPECTED_EOF_WHILE_READING）

**现象**：requests 全量下载 145MB 视频，下到中途报
`SSLError(SSLEOFError(8, '[SSL: UNEXPECTED_EOF_WHILE_READING]'))`。

**根因**：天翼云 OBS 的直链带 `x-obs-traffic-limit=409600`（400KB/s 限速），145MB 要约
6 分钟，长连接 TLS 会中途断。

**解法**：`_download` 加**断点续传 + 重试**——用 HTTP Range header 从已下载的 byte 继续，
最多 5 次，指数退避。partial 文件保存在 `.part` 文件里，跨重试不丢进度。

### 10.5 WSL2 `/mnt/` 路径 Python `os.walk` 失败（9P readdir bug）

**现象**：`os.walk("/mnt/e/BaiduNetdiskDownload/xq")` 返回 0 个文件，但 `ls` 能列出
15 个。`os.listdir` / `os.scandir` / `pathlib.iterdir` 全报
`[Errno 5] Input/output error`。

**根因**：WSL2 访问 Windows 盘走 9P 文件系统，Python 的 `getdents64` 系统调用在某些
9P 目录上失败。`ls`/`find`（coreutils）有重试/容错逻辑能部分工作。

**解法**：`cache.py` 对 `/mnt/` 路径改用 `subprocess` 调 `find` 命令扫描（`find -printf
"%s\t%f"` 一步拿到文件名+大小），不用 `os.walk`。`os.path.getsize` 对已知路径仍可用
（stat 单文件没问题，只有 readdir 有问题）。

**影响**：如果缓存目录在 `/mnt/` 下而不做这个处理，视频缓存永远 MISS，所有视频都走
网盘下载——功能正确但慢。

### 10.6 Gin 路由 `:id.vtt` 参数解析 bug（字幕不显示）

**现象**：播放器字幕选项出现但选中后不显示字幕。`GET /api/v1/subtitles/1.vtt` 返回
400 `invalid subtitle ID format`。

**根因**：Gin v1.10.0 不按 `.` 截断参数名。路由模板 `/subtitles/:id.vtt` 注册的参数名是
`"id.vtt"`（含点号），不是 `"id"`。handler 里 `c.Param("id")` 拿到空串，`ParseUint` 失败。

**解法**：handler 改用 `c.Param("id.vtt")` 拿到 `"1.vtt"`，再 `TrimSuffix(".vtt")` 得
`"1"`。路由模板不用改（play-info 生成的 url 仍带 `.vtt`，前端不用动）。已加回归测试
`TestSubtitleVTTEndpoint` 锁住。

### 10.7 `uv run` 产生孤儿子进程

**现象**：`uv run python worker.py` 后台启动，TaskStop 杀的是 `uv` 外壳，python 子进程
成孤儿继续跑。多个孤儿 worker 互相抢任务、日志丢失。

**解法**：`run.sh` 用 `exec .venv/bin/python` 直接启动 python（不经 uv run），进程死了
就是死了。Makefile 的 `make run` 调 `run.sh`。

---

## 11. 演进方向

- **字幕润色 LLM**：SRT 落库后过一遍 LLM 修 whisper 错别字（象棋等专业领域术语错字
  明显）。已实现——见 §12.2（分块 + diff 输出 + find/replace 格式 + 断点续润）。summary/
  quiz/advice LLM 在输出时按 `quiz_hint` 术语字典纠正（只改输出不改字幕，见
  `docs/modules/ai/overview.md` §9/§14）也已就位。
- **字幕切片 + embedding（RAG）**：为出题 agent 准备语料，Step 3。
- **多语言**：当前默认 zh-CN，`Language` 字段已支持扩展。
- ~~**worker 主动推送进度**~~：已实现（§9.3）。

---

## 12. 设计决策（"为什么"，已落地）

本节提炼自已完成的字幕系统改造。实施时不要重新质疑这些决策，除非遇到具体障碍。

### 12.1 存储统一为 VTT（不再 SRT）

`subtitles` 表 `srt_content` 字段已改名为 `vtt_content`，**只存 VTT**。

理由：
- 播放路径省一步转换（原每次播放都 `srtToVtt`）
- 保留内嵌字幕样式（粗体/位置，对学生有用）
- 避免"双存"（SRT+VTT 两字段）的一致性陷阱

**给 AI 时**：`VttToSrt(sub.VttContent)` 转换后走原有 SRT 解析逻辑。

### 12.2 字幕润色：分块 + diff 输出

全量字幕分块送 LLM，但 LLM **只输出 `{changes: [...]}` diff，不重写全文**。

- 一次性全量不可行——输出 token 物理上限（DeepSeek 8k，最长字幕 9 万字）
- 预过滤（只送疑似错字的 cue）不可行——"选出有问题的句子"本身就是核心问题
- LLM 的价值在"判断该不该改"，需要看上下文
- diff 输出省 70-90% 输出 token

**分块参数**（基于真实数据校准，可调）：
- 块大小 150 cue（≈6900 字 + prompt ≈ 8k input）
- 块重叠 3 cue（前后各 1.5 句上下文）
- 并发 **3 路**（默认，存全局 `settings` 表 `polish_concurrency` 键，admin 可在「系统
  设置 → AI 性能」改 1~10）。瞬时叠加风险来自多 job 交错 / summary 与 polish 同时跑，
  而非单 job 内的 N 路——worker 是单 goroutine 串行处理 job。遇 429 可调低，换不限制
  并发的中继可调高（指数退避兜底见下）
- MaxTokens 12000（中继不一定严格遵守 maxTokens，把它当建议；给足余量避免极端 chunk 被限）
- **job deadline 按 chunk 数自适应**：`max(chunk数 × 5min, 20min)`。固定 20min 会让长字幕
  顶满 deadline、最后几个 chunk 没轮到跑就被取消。自适应后长字幕够覆盖 + 重试余量，
  短字幕走 20min 下限不空等
- 单块 **2 次重试**（网络/parse 失败时）；重试退避为**指数退避**（2s、8s…），给被 429
  限流的中继留排队窗口；仍失败则整 job 标 `failed`（详见下方「验证策略与 partial 语义」），
  不写回字幕、不链式推进 segment

**验证策略与 partial 语义**：

采用**放行 + 信息性提示**（不靠硬规则拦截）：
- 验证只剩**结构校验**（JSON 能 parse、id 在 chunk 范围内）—— 这两个失败才重试
- 长度差 > **5**（`maxLenDelta`）、标点变化、Levenshtein/maxLen > 0.5 的条目**照常应用**，
  只计入 `PolishStats.HighEditDistanceCount` 作为信息性统计（job detail 里显示
  `high_edit_distance=N`，提示 admin 去字幕版本 UI 复核）。硬规则拦截会把正常术语纠错
  （`考算→口算`、`合不变→和不变` 这类短词改 1 字）误杀掉
- 审核职责移到 UI：字幕行的「对比」视图（润色版/原始版 toggle + cue 级 diff 高亮 +
  token 级 +/- 色块）让 admin 一眼看出 LLM 改了什么、改得对不对

**提示词与代码必须对齐**：
提示词描述的行为必须和代码实际执行的一致——提示词写「字符数差距 ≤ 2」「违反则整批
作废」而代码只警告不拦时，模型会按提示词自我审查，把代码允许的 3-5 字修正咽下去不
输出。故提示词标题写成「id 错误会作废，其他违规由 admin 复核」，不承诺代码不执行的
字符数规则，并加入 find/replace 输出格式（见下方「find/replace 输出格式」）。

**partial 语义收窄**：`PartialOptimized=true`（有 chunk 耗尽重试仍失败）现在标 job
**failed**，不再标 done。partial 时**不写回字幕**（保持 source=whisper 原状，避免半成品
污染下游）、**不链式 segment**。admin 可 RetryJob 重跑整条（配合断点续润只重烧失败 chunk，
见下方「断点续润」），或 SkipPolish 跳过走原版。partial 的唯一成因现在是「LLM 没返回有效
结果」（网络/parse 失败），不再包含「返回了但规则不信」（因为不再拦截）。

**重试次数 + 指数退避**：`maxRetries = 2`。第 3 次重试几乎从不成功（relay 垃圾响应原样
重复、unknown-id 漂移原样重复），只是白烧计费 token。配合断点续润，一个耗尽重试的
chunk 会 fail 掉整 job，但它已 done 的兄弟 chunk 会留下——下次 retry 时只重烧这一个
chunk，比内联多打一次近乎无用的第 3 次更便宜。重试退避为指数（2s/8s…）：429 后秒级
重试必再撞墙，指数退避让中继有机会排空在途请求。指数退避是兜底——万一并发调高了或
换了更严的中继。

**llm_optimized 一致性**：`isPolishableSource` 单一真源，入队（EnqueuePolish）和执行
（runPolishJob）共享。允许 whisper（首次润色）和 llm_optimized（re-polish，admin 接受
新 glossary 后重跑）。re-polish 从 `RawVttContent`（不可变快照）重新开始，不会 drift 累积。

**时间戳保证**：后端维护 `id → 时间戳` 映射，LLM 只输出 `id → 修正文本`。
时间戳根本不进 prompt，物理上不会错。

#### 12.2.1 find/replace 输出格式

原来每个 change 只能整句替换：`{"id":147,"text":"修正后的整句"}`。问题是一个 cue 里
只错一个字（最常见情形），却要模型把整句重输出——多出来的 completion token 全花在抄
没改的字上。现在新增 **find/replace 子串替换**格式：

```json
{"id":147,"edits":[{"find":"出军","replace":"出车"}]}
```

- **`edits` 优先**：有 `edits` 就按子串替换处理，忽略 `text`；没有 `edits` 才回退到
  `text`（整句替换）。`text` 字段保留是为了**向后兼容**（老模型 / 格式漂移 / 现有测试
  的返回格式）。
- **唯一性由 apply 层兜底**：`find` 必须在该 cue 内唯一（`strings.Count(final, find) == 1`）。
  不唯一或找不到时，**跳过该 edit**（计入 `PolishStats.SkippedEdits`），**不 retry**——
  retry 模型大概率原样返回同样的 find，只是白烧配额。一条 change 的其余 edits、或 `text`
  fallback，照常应用。
- `SkippedEdits` 是信息性统计（非正确性门控），job detail 里能看到，让 admin 区分「模型
  的 find 不精确」和「模型根本没改这条 cue」。
- 同一 cue 多处错字用数组：`edits:[{find,replace},{find,replace}]`。

统一用 `CueChange.resolveText(orig)` 计算 final text，validate 和 apply 两处共享同一路径，
不会漂移。

#### 12.2.2 断点续润 / checkpoint-resume

polish 是最贵的 AI 能力（单集 7-13 分钟、几万 token）。早期版本一个 chunk 失败 → 整 job
failed → RetryJob 把**所有 chunk** 重烧一遍，哪怕大部分 chunk 上次已成功。本轮加断点续润：
新表 `ai_polish_chunks`（`ai_jobs` 子表，FK OnDelete:CASCADE）记录每个 chunk 的状态。

**表结构**（`model.AIPolishChunk`）：`job_id` + `chunk_index`（0-based，和 chunk 布局对齐）
+ `chunk_first/last_global_idx`（审计用）+ `status`（queued/done/failed）+ 累计 token +
`retries` + `high_edit_distance_count` + `changed_cues` + `first_err`（失败时）+
`polished_chunk_json`（成功时存 `{"changes":[...],"glossary":[...]}`，resume 时反序列化）。

**流程**（`setupPolishCheckpoint` + `polish.Polish` 的 `PriorOutcomes`/`OnChunkDone`）：

1. job 开始时按 `polish.ChunkLayout(numCues)` 算出每个 chunk 的全局 cue 范围，幂等 seed
   一批 `queued` 骨架行（retry 重跑不会覆盖上次已写 done/failed 的行）。
2. 读回上次已 done 的 chunk，反序列化成 `PriorChunkOutcome` 喂给 `polish.Polish`——它对
   这些 chunk **不调 LLM**，只把它们的 changes/glossary/tokens 折进最终 reassembly + stats
   （token 是上次真烧了的，stats 必须反映）。
3. 每个新跑完的 chunk 通过 `OnChunkDone` 回调落库（done 写 JSON + token，failed 写 errStr）。
4. **进度上报**：`onChunkDone` 里 `progress = doneChunks/totalChunks`，写进 job status，
   admin 队列页能看到 polish 的真实进度（而不是 20 分钟一直转圈）。

**设计要点**：
- chunk 边界对给定 VTT 是确定的（step=147 固定），所以 retry 时 chunk index 稳定、能对上。
- `polishChunkRepo` 为 nil（测试 / 关闭断点续润）时退化为普通全量跑，不影响正确性。
- 成功的 job 保留 chunk 行，既是 token 花费记录，也是未来 re-polish retry 的 resume 锚点。
- chunk 落库失败是**非致命**的：best-effort log，polish 本身照常产出正确字幕，只是丢了
  resume 能力。

#### 12.2.3 挖矿开关 SkipGlossaryMining

re-polish（source=llm_optimized）时字幕内容未变（从不可变的 `RawVttContent` 重跑），上次
能挖的术语这次还能挖到，再挖既白烧 completion token 又分散模型注意力。`PolishRequest.
SkipGlossaryMining bool` 让 re-polish 用**精简系统提示词**（无「术语挖矿」段，省约 120
prompt token/chunk），模型不再输出 glossary 字段。

`runPolishJob` 用 `sub.Source == "llm_optimized"` 判定是否开（首次润色 source=whisper
照常挖矿）。`SystemPrompt(skipMining)` 是单一真源：既被 `polishChunk` 选用，也被
`recordPolishRun` 写进 `ai_runs.system_prompt_text`，保证 admin 在「查看回放」里看到的
prompt 就是模型实际收到的版本。

#### 12.2.4 token 统计 + polish 写 ai_runs

**token 累加 bug**：retry 循环里原来用一个 `usage` 变量，每次 retry 时 `usage = resp.usage`
**覆盖**，导致前面 HTTP 成功 attempt 的 token 被丢掉（尤其被 validation 拒掉的 attempt，
token 已经计费但没统计）。改成 per-chunk `promptAccum`/`completionAccum` **累加所有 HTTP
成功 attempt 的 token**（含最终被 validation 拒掉的）。失败的 chunk 也照样加（它们同样
消耗了 token，账单不会因为标 failed 就免单）。这直接影响 `recordPolishRun` 写进 `ai_runs`
的 token 数是否反映真实账单。

**`recordPolishRun`**：polish 之前是**唯一不写 `ai_runs` 表的 AI 能力**（summary/quiz/
advice/course_summary 都写）。本轮补上：`capability="polish"`，带 prompt/completion tokens、
耗时（`duration_ms`）、model、system prompt（按 skipMining 取正确版本）、以及 preview
（changed/total/glossary/high_edit_distance/skipped_edits/failed_chunks）。成功记 `pass`、
partial 记 `fail`（复用 `self_check_result` 列）。这让 AI 控制台的 RunList 和「查看回放」
能看到 polish 的 token 花费和 prompt——运维可见性和其他 AI 能力对齐。

### 12.3 润色用便宜模型

| 任务 | 模型要求 |
|---|---|
| 字幕润色 | 低（指令遵循 + 中文常识） |
| Quiz self-check | 低-中 |
| Summary | 中 |
| Quiz 出题 / Advice | 中-高 |

推荐润色模型：DeepSeek-chat（中文强 + 便宜 + ¥0.3-1/节）。多 provider 落地：
provider 配置里 `tags` 字段（JSON 数组），润色任务优先找 tags 含 `"polish"` 的 provider，
找不到 fallback 到默认 chat。

### 12.4 词汇表自动生成（挖矿工作流）

润色时顺带挖矿，admin 审核入库。LLM 在润色时本来就要判断"这个是不是术语错字"——
让它**显式输出**这个判断（glossary 字段），沉淀下来。**零额外 LLM 成本**（同一次
调用里多吐一个字段）。

两套互补系统：

| 系统 | 位置 | 角色 |
|---|---|---|
| **TermDict** | `Course.AIConfigJSON.TermDict` + `Subject.AIConfigJSON` | admin 手写的权威字典，LLM 每次润色按它纠正 |
| **glossary_candidates** | `glossary_candidates` 表（`model/ai.go`） | LLM 挖出的建议池，status: pending/accepted/rejected，admin 审核后 accepted → 自动追加进 TermDict |

单向流：candidates → TermDict（接受）。TermDict 手改不影响 candidates。

**挖矿门槛**：`confidence≥0.9` 且 `evidence_ids≥2`（出现至少 2 个不同 cue）。两道把关：
prompt 要求模型自律（软）+ `filterGlossary` 硬兜底（`dedupGlossary` 之后过滤）。**为什么
这么严**：孤例（出现 1 次）和低置信度的规则依赖上下文、不可复用——比如象棋
`推一将→退二将`，脱离"绝杀"语境没意义，存进字典反而过拟合到具体词条；门槛低了会一节课
冒出 100+ 候选，admin 审不过来，审核流于形式。收紧后只剩真正高频稳定的规则（如
`车→居` 全课程反复
出现），数量降到个位数，人工审核才有意义。**注意**：接受候选仍需人工判断——门槛只过滤
明显的噪音，不替 admin 决定"这个规则值不值得固化进字典"。

**re-polish 关闭挖矿**：admin 接受新 glossary 后 re-polish（source=llm_optimized）时，
字幕内容未变（从 `RawVttContent` 重跑），能挖的术语上次已挖。此时 `SkipGlossaryMining=true`
跳过挖矿、用精简系统提示词省 token，详见 §12.2.3。

### 12.5 字幕格式全覆盖

统一走 ffmpeg `-c:s webvtt` 转换。文本格式（SRT/ASS/SSA/SUB/SMI/WebVTT）全能转。
图形格式（PGS/VOBSUB/DVB）不能转，admin UI 报错提示走 whisper OCR——判定由
`media.IsBitmapSubtitleCodec`（`backend/internal/media/probe.go`）。

### 12.6 不转 ASS 存储

虽然 ASS 支持复杂样式（卡拉OK/双语分行/说话人颜色），但中小学学习场景不需要，且
AI 管线（segmenter/summarizer/prompt）都深度依赖 SRT 结构，转 ASS 会断链。**保持
VTT 存储**。若未来要"重点高亮"，让润色 LLM 在 VTT 里给术语包 `<b>` 标签（VTT 原生
支持），零额外架构成本。

### 12.7 轻量结构化日志层

之前所有日志走 stderr（`log.Printf`，全仓 ~81 处，前缀不统一）。AI/subtitle worker 的
关键事件（job 失败、reaper 回收、provider 解析失败、worker panic、polish 完工）只在
server 日志里，admin 看不到——出现「AI worker 前面卡住了，不知道为什么」这类问题，只能
SSH 进去看 stderr。本轮加一个**轻量结构化 log 层**，让运维可见性不再依赖 SSH。

**数据模型**（`model.LogEntry`）：`level`（info/warn/error）+ `source`（ai_worker/reaper/
polish/provider/segment…）+ `message` + `fields_json`（可选的结构化上下文，JSON blob，如
token 计数、chunk id、reaped 数量）+ `job_id`/`episode_id`/`course_id`（可选关联）+
`created_at`。`AutoMigrate` 自动建表。

**设计取舍**（对应 TODO.md 原 P1 条目的边界声明）：
- **不引第三方 log 库**：自己写 wrapper（`LogRepository` + service 层 `appendLog`），避免
  go.mod 改动 + 全仓 81 处替换。
- **best-effort + nil-safe**：`appendLog` 在 `logRepo==nil`（测试）时是 no-op；写入失败只
  `log.Printf` 一行，**绝不** derail 业务逻辑。log 是观测层，不是业务路径。
- **不全量替换 81 处 `log.Printf`**：只接 5 个高信号点，其余旧代码渐进迁移。

**5 个接线点**（高信号运维事件）：

| 点 | level | source | 触发 |
|---|---|---|---|
| `failJob` | error | ai_worker | 任何 AI job 走到失败终态（含 provider 解析失败，经 failJob 落 error） |
| `ReapStaleJobs` | warn | reaper | 认领超 30 分钟的任务被回收（worker 崩溃/relay 挂起的信号） |
| `polishStats` | info | polish | polish 完工，带 chunk/llm_calls/retries/token/duration 结构化字段 |
| provider resolve 失败 | error | ai_worker | 润色任务找不到 provider（经 failJob 落 error，带原始 resolve 错误） |
| worker panic | error | ai_worker | AI worker goroutine recover 到 panic（防整条 worker 静默死掉） |

**Admin 可视化**：`GET /admin/api/logs?level=&source=&job_id=&limit=`（`admin_logs.go`）+
`/admin/logs` 前端页（`pages/Logs.tsx`，仿 AIWorkflow 的 useQuery + 轮询）。返回 enriched
view（带 episode/course 标题，避免 N+1）。admin 能直接在控制台按 level/source/job 过滤看
事件流定位故障，而不是 SSH 翻 stderr。

---

## 13. 关键陷阱（字幕系统专属）

跨模块通用坑见 `docs/pitfalls/`，本节只列字幕改造踩过的、对修改字幕代码仍然有效的坑。

### 13.1 跨层契约改动不能并行

`Subtitle.SrtContent` → `VttContent` 涉及多个调用点。**改之前先
`grep -rn "SrtContent\|srt_content"` 把所有调用点列出来**，逐个改完。

主要调用点（重构后路径）：
- `backend/internal/model/content.go` — Subtitle struct 定义
- `backend/internal/handler/episode_handler.go` — GetSubtitleVTT（读 vtt_content）
- `backend/internal/service/ai_service.go` — runSegmentJob
- `backend/internal/repository/episode_repo.go` — SaveSubtitle upsert
- `backend/internal/handler/admin_subtitle.go` — 字幕 CRUD（admin_content 拆分后）
- `backend/internal/handler/admin_dto.go` — subtitleDTO
- `backend/internal/service/import_service.go` — auto-match 字幕
- `tools/video-pipeline/api_client.py` — **不改**，继续上传 SRT，后端转 VTT

### 13.2 model 改动后立即跑全量测试

CLAUDE.md 硬规则：model-layer change → `make test` every time，no exceptions。
`go build` 绿不等于行为绿。

### 13.3 `whisper_hint` 字段不要被 VTT 迁移误伤

字幕 job 的 `whisper_hint` 字段（`subtitle_job_handler.go`）是给 worker 的 prompt
context（**不是字幕**），和字幕存储格式无关。改字幕格式不要动它。

### 13.4 删库重建前先备份

用户已批准删库重建（不做迁移脚本）。但每次改 schema 前先备份：

```bash
cp backend/data/studyquest.db backend/data/studyquest.db.bak.$(date +%Y%m%d)
```
