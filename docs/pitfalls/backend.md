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

## Unlock / Progress

### 娱乐课不能触发 badge

`EvaluateRules` 唯一调用点在 `progress_service.go:260`，娱乐分支在 `:169` 早 return
走不到。**娱乐进度仍物理分表**（`entertainment_progresses`），学习数据零污染。

未来若做"错题本"（聚合 `answers` 表），查询必须 JOIN courses 按
`content_type='learning'` 过滤，否则娱乐答题混入。
