# tools/ 踩坑集（Python whisper worker 等）

> `tools/video-pipeline`（Python whisper worker）相关。worker 协议设计见
> `docs/modules/ai/subtitle-queue.md`。

## ffmpeg 自编译

### apt 装的 ffmpeg 在 Alpine/musl 上 segfault

容器基础镜像用 Alpine（musl libc），通过 apt 装的 ffmpeg 二进制在调用某些 codec 时
segfault（具体是 libass 字幕滤镜链）。**解法**：静态编译 ffmpeg 7.1.1，自带所有
codec，不依赖系统 shared lib。

副作用：镜像从 665MB 瘦身到 370MB（apt 装 ffmpeg 会拖一堆依赖）。

### ffprobe 必须和 ffmpeg 同源编译

`probeMedia`（`backend/internal/media/probe.go`）依赖 ffprobe JSON 输出。ffprobe
版本和 ffmpeg 必须同源编译，否则 stream index / codec name 字段可能不一致。两者
都从同一个 `tools/video-pipeline` 的 build 脚本产出。

## whisper worker

### `faster-whisper`（CTranslate2 后端）比 `openai/whisper` 快 4x

显存占用一半。中文场景推荐 `small` 或 `medium` 模型（large-v3 显存吃紧、慢，small
已经够准）。

### 音频抽取必须用 ffmpeg `-vn -ac 1 -ar 16000`

whisper 要求 **16 kHz 单声道**。命令：

```bash
ffmpeg -i input.mp4 -vn -ac 1 -ar 16000 -f wav output.wav
```

不转格式 whisper 会拒收或慢得离谱（它内部还要重采样一次）。

### `X-Ingest-Key` 是预共享密钥，不是 admin session

worker 是机器，不是浏览器 session，不走 admin auth。`/subtitle-jobs/*` 路由组挂在
`IngestKeyMiddleware` 上，要求 `X-Ingest-Key` header 等于环境变量 `INGEST_KEY`。
admin 前端用的 cookie session 在 worker 这条路径上无效。

## claim 协议

### 一次 round-trip 一 job

`POST /subtitle-jobs/claim` → 返 `{job: {...}}` 表示认领到一个，返 `{job: null}`
表示空队列，worker 应该退避后重试（不要 busy-loop）。

每个 job 独立一次 claim/complete round-trip，不要批量认领——批量会让"worker 崩溃
后 job 永远 processing"的问题更严重。

### 心跳 `/subtitle-jobs/:id/heartbeat` 防止僵尸 job

worker 认领后每隔 30s 发心跳。如果 backend 60s 没收到心跳（`ReapStaleJobs`），
job 被回滚为 queued 让别的 worker 认领。长视频转录（可能 5-10 分钟）必须持续心跳。

### 完成必 `/complete`，失败必 `/fail`

worker 崩溃后没发 `/complete` 是可以接受的（reaper 兜底），但正常路径必须发。
`/fail` 带错误消息（截断 2000 字），admin UI 才能看到失败原因。**不要静默退出**——
admin 看不到失败的 job 会以为是 backend bug。

## SRT 上行

### worker 上传 SRT 格式不变，后端转 VTT

后端字幕存储已统一为 VTT（`vtt_content` 字段），但 **worker 仍然上传 SRT**——
后端 `/complete` 接口自动 SRT→VTT 转换。worker 代码不需要随 VTT 迁移改。

这条是 VTT 改造（`docs/modules/ai/subtitle-queue.md §12` 提炼）的明确决策：把转换集中在后端，
worker 保持简单。**未来若要改字幕格式，只动后端**。
