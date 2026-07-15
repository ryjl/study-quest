# AI 字幕队列系统（Subtitle Generation Queue）

> 字幕自动生成的基础设施：admin 选哪些 episode 要字幕 → 任务排队 → GPU 机器上的
> whisper worker 认领并转录 → 字幕自动落库 → 播放器立即可用。
>
> 后端 VPS（2c2g）只做协调；whisper 重计算跑在带 GPU 的用户台式机上。本文档描述
> 后端这一侧的队列、协议、状态机与并发设计。

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

`backend/internal/model/models.go` — `SubtitleJob`：

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

状态常量与 `IsTerminalSubtitleJobStatus` 辅助函数也在 `models.go`。

`AutoMigrate` 已注册，部署时自动建表，无需手动迁移。

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
├── model/models.go                      SubtitleJob model + 状态常量 + AutoMigrate
├── repository/subtitle_job_repo.go      SubtitleJobRepository（含原子 ClaimNext）
├── service/subtitle_service.go          SubtitleJobService（业务守门 + 状态机）
├── handler/
│   ├── subtitle_job_handler.go          worker 协议（claim/complete/heartbeat/fail）
│   └── admin_subtitle_jobs.go           admin 端（enqueue/list/skip/retry/stats）
├── repository/episode_repo.go           新增 CountSubtitlesByEpisodes / HasSubtitle
│                                        / FindCourseContentType（gate 检查 + DTO 计数）
├── handler/admin_dto.go                 episodeDTO 加 subtitle_count 字段
├── handler/admin_content.go             ListEpisodesByCourse / GetCourseDetail 批量填计数
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
- **`lib/api.ts`** + **`lib/types.ts`**：`SubtitleJob` / `SubtitleJobStats` /
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

`docs/subtitle-queue-smoke.md` 是 curl 端到端冒烟测，无需 Python worker 即可验证整条
协议链路。

---

## 9. 演进方向（当前不做）

- **字幕润色 LLM**：SRT 落库后可选过一遍 LLM 修 whisper 错别字。留扩展位，当前不做。
- **字幕切片 + embedding（RAG）**：为出题 agent 准备语料，是 AI 模块 Step 3 的事。
- **多语言**：当前默认 zh-CN，`Language` 字段已支持扩展。
- **worker 主动推送进度**（百分比）：当前只有心跳，admin 看不到转录到哪了。
