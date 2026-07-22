# 后端踩坑集（backend pitfalls）

> 每条都是真实撞过的坑。CLAUDE.md 的 "Hard-won rules" 是这里最关键几条的提炼。
> 模块专属坑（字幕系统）见 `docs/modules/ai/subtitle-queue.md §13`。

## 跨层契约

### 改 model 字段名前必 grep 全调用点

跨层改动（Go struct json: tag ↔ TS interface ↔ Dart class 三模块隐式契约，无 codegen）
撞一次就 4 个 bug。`Subtitle.SrtContent → VttContent` 案例里，改之前没全 grep，handler
和 repository 各漏一处，运行时才崩。

**规则**：改字段前先 `grep -rn "OldName\|old_name"` 把所有调用点列出来，逐个改完。

### model 改动后立即跑全量测试

`go build` 绿 **不等于** 行为绿。GORM 的 struct tag、binding 标签、json 序列化都不会
在 build 阶段暴露问题。**model-layer change → `make test` every time, no exceptions。**

### 跨层改动不能并行

`CLAUDE.md` 硬规则：Never parallelize model-layer changes with their callers。改 model
的 commit 必须先把所有 caller 改完跑通，再开始下一项工作。上一轮 prompt 重构就是
agent 并行改了 DTO 契约层同时改 caller，4 个 bug 进了主干。

## 时区

### 业务时区必须走 `appclock`

"今天 / 昨天 / 深夜小时 / 连续天数"必须走 `internal/appclock`（固定 Asia/Shanghai，
可注入测试），**禁止直接 `time.Now()` 或 SQLite `'localtime'` 修饰符**。

**真实事故**：容器内 SQLite `'localtime'` 与 UTC 偏离了 8 小时，streak 计算静默清零，
用户连续学习记录消失。存储保持 UTC，只有人类日期语义在 appclock 层转换。

涉及调用点：`badge_repo.go`、`admin_handler.go`（today cutoff）、`episode_repo.go` /
`progress_repo.go`（recent-N-day 窗口）。

### 存储时间戳必须 UTC：三层防护（DSN + NowFunc + 显式 `.UTC()`）

业务时区（上一节）之外，**存储时间戳**也有独立的时区陷阱：同一张表里
`CURRENT_TIMESTAMP`（UTC）写的列和 Go `time.Time` 写的列如果用了不同时区源，
`claimed_at - completed_at` 这类耗时计算会静默差 8 小时。

**真实事故（reaper）**：`ReapStaleJobs` 用 `time.Now().Add(-30min)`（本地时区）
算 cutoff，和 SQLite `CURRENT_TIMESTAMP`（UTC）写的 `claimed_at` 比较。+08 生产
下 cutoff 比 claimed_at 大 8 小时，导致**刚 claim 的 job 每 5 分钟被误 reap**，
polish（2-7 分钟）永远跑不完——症状是"polish 没跑"，实际是 reaper 反复杀掉。
定位极难（无可观测性，job 静默回到 queued）。

**三层防护**（都已落地，缺一不可）：
1. **DSN `_loc=UTC`** —— `cmd/server/main.go` + 所有测试 DB（经
   `testutil.GormConfig()`）。让 go-sqlite3 用 UTC 解释 bare-text 时间戳。
2. **GORM `NowFunc` 返回 `time.Now().UTC()`** —— auto-managed `CreatedAt`/
   `UpdatedAt` 由此走 UTC，和 `CURRENT_TIMESTAMP` 一致。
3. **repository/service 每一处写库 `time.Now()` 显式 `.UTC()`** —— 即使前两层
   失效，代码层仍正确。**这些 `.UTC()` 不是冗余，删任何一处都要审。**

**回归保护**：
- `repository/reaper_timezone_test.go` —— 业务层（reaper cutoff vs UTC
  claimed_at），在 +08 机器上能抓本地 cutoff 回退（已验证有效）。
- `repository/timezone_storage_test.go` —— 存储层 round-trip（auto-managed
  CreatedAt 读回 UTC window + Location）+ 同行两种写法一致（CURRENT_TIMESTAMP
  claimed_at vs Go `.UTC()` completed_at 秒级一致）。

**为什么业务时区走 appclock、存储走 UTC 是两套机制**：appclock 负责"今天/几点"
这种人类日期语义（读出 UTC instant 后转 Asia/Shanghai 再算 Weekday/Hour）；
存储 UTC 负责"两个时间戳之间的物理耗时"。不要混用——存储时间戳绝不走 appclock，
业务日期语义绝不裸 `time.Now()`。

## Worker / goroutine

### `NewAIService` 启 worker 必须带 context cancellation

**根因**：`NewAIService` 每次调用 `go s.runWorker()` 启一个**永不退出**的 goroutine。
`go test ./...` 并行跑多包时，几十个泄露的 worker 争抢 file-DB 锁，间歇性
`no such table: ai_jobs` —— flaky test 根源（5 次约 1 次 fail）。

**修法**（已落地）：`aiService` 加 `cancel context.CancelFunc` 字段，`NewAIService`
内部 `ctx, cancel := context.WithCancel(...)`，`runWorker(ctx)` 用 `select` 监听
`ctx.Done()`。加 `Stop()` 方法。3 个 test helper 注册 `t.Cleanup(svc.Stop)`。

生产代码（`cmd/server/main.go`）不需要调 Stop（进程退出时 OS 回收）；测试必须调。

### panic recover 必须包住 processOneJob

worker 里一个 job 的 panic（nil deref、bug）**不能杀整个 worker goroutine**——那会
静默 halt 所有后台 AI 处理直到重启。`runWorker` 里包了 `defer func(){ recover() }()`
保护，job panic 时标失败、log stack，worker 继续轮询。

### 并发可见字段：中间写单调守卫，终态钉死（别靠 nil）

`UpdateJobStatus`（`repository/ai_job_repo.go`）的 `progress` 字段栽过两个相连
的 bug，都是把"并发可见、有中间态又有终态"的字段当普通列写：

**(a) 并发进度倒退。** polish 的 chunk 回调是 3-way 并发，每个 goroutine
`atomic.Add(1)` 后算出的 `done` 值本身单调递增，但随后的 `UPDATE ... SET progress=?`
提交顺序不定——B(done=0.8) 可能先 commit、A(done=0.6) 后 commit，DB 停在 0.6，
进度条倒退/抖。**修法**：非终态路径加 `WHERE progress IS NULL OR progress < ?`
单调守卫，只有更大的值能写入。

**(b) 失败残留高进度。** `failJob` 之前写 `status=failed` 时 `progress=nil`（map
里不带 progress 键 = 不更新该列），导致 failed job 残留 processing 末期写入的
`progress≈0.9`，前端同时读 `status=failed` + `progress=0.9` 自相矛盾，失败页闪
一下满进度条。**修法**：终态钉死——`done` 写 1.0，`failed`/`skipped` 写 NULL。

**规则**：任何"中间态可被并发写、终态要收尾"的字段（progress、attempts、计数器），
中间写加单调守卫，终态写明确归零/钉死，**绝不靠 nil（不更新列）**——nil 等于
"保留旧值"，而旧值在终态下几乎一定是错的。

**回归保护**：`repository/ai_job_status_test.go`，含
`TestUpdateJobStatus_ConcurrentProgressNoRegression`（真 20-goroutine 并发，
跑 `-race`）+ `TestUpdateJobStatus_FailedNullsProgress` / `_DonePinsProgressOne`。

## HTTP handler

### `ShouldBindJSON` 失败时禁返 `err.Error()`

binding 错误的 `err.Error()` 会泄露 Gin/validator 内部细节给客户端（违反"不泄露 SQL/
driver 内部"红线）。本轮重构前有 14 处 `"invalid request body: " + err.Error()`
直接吐错。

**修法**（已落地）：统一走 `internal/handler/httperr.go` 的 `bindJSON(c, v) bool`
helper，失败时返 generic `"Invalid payload format"`，不携带 err 细节。

### 4xx/5xx 错误必走 `respondError`

service/repo 层错误走 `respondError(c, err)` —— 它是单一 funnel，把 sentinel 错误
map 到对应 HTTP status（`ErrSystemProtected`→403、`ErrSubjectInUse`→409、
`gorm.ErrRecordNotFound`→404、其他→500 + 服务端 log 但客户端只看 generic 消息）。

新 domain 错误通过 `registerAppError` 注册到 `httpErrRegistry`，不要在 handler 里
手写 `if errors.Is(...) { c.JSON(...) }` 链。

## ffmpeg / ffprobe（`internal/media/`）

### 天翼云 OBS CDN 间歇 RST TLS 多 socket 读

ffmpeg 默认开多个 socket 读 mp4 moov+mdat，天翼云 OBS（`obs-ynkmfy-suzhou-home.obs.cn-jssz1.ctyun.cn`，约 80% 课程视频）间歇性 RST 这些 TLS 连接，表现为
`"IO error: End of file"` / `"moov atom not found"`。

**对抗**：`runFFmpegWithRetry` 重试 3 次（A/B 测试：单次 ~40% 成功，3 次达 ~95%）。
命令参数必带 `-reconnect 1 -reconnect_streamed 1 -reconnect_at_eof 1 -reconnect_delay_max 2`。
只重试 transient 网络错误（`IsTransientFFmpegError`），真实 codec/格式错快速失败。

### `IsBitmapSubtitleCodec`: PGS/VOBSUB/DVB 不能转 VTT

`media.IsBitmapSubtitleCodec` 判定图形字幕格式（PGS/VOBSUB/DVB/teletext）。ffmpeg
拒绝把它们转 WebVTT（"Subtitle encoding currently only possible from text to text or
bitmap to bitmap"）。admin UI 据此禁掉"抽取字幕"按钮，引导用户走 Whisper OCR。

## SQLite

### in-memory `:memory:` 每连接独立私有 DB

GORM 连接池可能给同一查询不同的物理连接。in-memory SQLite 每个连接是**独立的私有
空 DB**——worker goroutine 拿到第二个连接时，`AutoMigrate` 建的表它看不到，报
`no such table`。

**测试规则**：凡是 `NewAIService`（启 worker）的测试，必须用
`testutil.NewFileDB(t)` 而不是 `testutil.NewDB(t)`（in-memory）。file-backed
SQLite 跨连接共享 DB。

### 删库重建前必备份

用户已批准开发期删库重建（不做迁移脚本），但每次改 schema 前必备份：

```bash
cp backend/data/studyquest.db backend/data/studyquest.db.bak.$(date +%Y%m%d)
```

### GORM AutoMigrate 会静默跳过建表（FK 关系字段 + uniqueIndex 组合）

GORM 的 SQLite migrator 在某些 struct 配置下**建表失败不报错**，`AutoMigrate`
返回 `nil` 但表根本没建。已知触发组合：**FK 关系字段**（`X Parent
\`gorm:"foreignKey:...;constraint:OnDelete:CASCADE"\``）**叠加该 FK 列上的
`uniqueIndex`**。

`AIPolishChunk` 就栽在这——`Job AIJob` FK 字段 + `JobID` 列的
`uniqueIndex:idx_polish_chunk_job_idx`，AutoMigrate 静默跳过，
`sqlite_master` 查无此表，后续 `ON CONFLICT DO NOTHING` 找不到唯一约束失效，
断点续润在生产完全不工作（重试重烧全部 chunk）。而全量测试全绿——因为测试
用 in-memory DB 且只 seed 一次，重复 seed 的 bug 没被覆盖，是 code review
抓出来的不是测试抓出来的。

**规则**：
- 加任何新 model 后，**验证表真建出来**：`db.Migrator().HasTable("新表")`
  或 `SELECT count(*) FROM sqlite_master WHERE name='新表'`，不能只信
  `AutoMigrate` 的 `nil` error。
- 能用裸 FK 列（`ParentID uint`）就**不要** FK 关系字段——`GlossaryCandidate`、
  `LogEntry`、`AIPolishChunk`（修复后）都这么做。CASCADE 清理在代码层做（删父行
  时手动删子行），不依赖 DB FK constraint。

**回归保护**：service 的 `TestRunPolishJob_Resume_SeedIsIdempotent`（同一个
job 跑两次，断言 chunk 行数不翻倍）能直接抓到建表/unique index 失效——前提是
测试用 file-backed DB 且真跑两次 seed。

## Unlock / Progress

### 娱乐课不能触发 badge

`EvaluateRules` 唯一调用点在 `progress_service.go:260`，娱乐分支在 `:169` 早 return
走不到。**娱乐进度仍物理分表**（`entertainment_progresses`），学习数据零污染。

未来若做"错题本"（聚合 `answers` 表），查询必须 JOIN courses 按
`content_type='learning'` 过滤，否则娱乐答题混入。
