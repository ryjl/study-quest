# 字幕队列冒烟测试

验证字幕生成队列的后端协议完整、端到端跑通（无需 Python whisper worker）。
真正的 whisper worker（运行在带 GPU 的台式机上）只是按这个协议实现下载 → 转录 → 回传。

## 前置

- 后端已 build 并运行（`make run` 或 `./backend/bin/server`）
- 已设置 `INGEST_KEY` 环境变量（worker 协议端点受 `X-Ingest-Key` 保护）
- admin 已设置登录密码（首次访问 `/admin` 会引导设置）
- 至少有一个 learning 课程的 episode（通过 admin 导入或手动创建）

下面的变量按你的环境替换：

```bash
export BASE=http://127.0.0.1:8080
export INGEST_KEY=你的ingest_key
export ADMIN_PW=你的admin密码
export EP_ID=1   # 要加字幕的 episode id
```

## 1. admin 登录拿 cookie

```bash
curl -s -c /tmp/cookie.txt -X POST "$BASE/admin/api/login" \
  -H "Content-Type: application/json" \
  -d "{\"password\":\"$ADMIN_PW\"}"
# 期望: {"role":"admin",...}
```

## 2. 把 episode 加入字幕队列

```bash
curl -s -b /tmp/cookie.txt -X POST "$BASE/admin/api/subtitle-jobs" \
  -H "Content-Type: application/json" \
  -d "{\"episode_ids\":[$EP_ID],\"priority\":0}"
# 期望: {"status":"ok","enqueued":[1],"skipped":[],"reasons":{}}
# 若 episode 是娱乐内容或已有字幕，会出现在 skipped 里并带 reason
```

## 3. 看队列

```bash
curl -s -b /tmp/cookie.txt "$BASE/admin/api/subtitle-jobs" | head -c 400
# 期望: 看到 status="queued" 的任务行
curl -s -b /tmp/cookie.txt "$BASE/admin/api/subtitle-jobs/stats"
# 期望: {"running":true,"queued":1,...}
```

## 4. worker 认领任务（关键：返回现签的下载直链）

```bash
curl -s -X POST "$BASE/api/v1/subtitle-jobs/claim" \
  -H "X-Ingest-Key: $INGEST_KEY" \
  -H "X-Worker-ID: my-desktop"
# 期望:
# {
#   "job":{"id":N,"episode_id":1,"language":"zh-CN","attempt":1,"claimed_by":"my-desktop"},
#   "download_url":"https://...",          ← 网盘现签直链，会过期，要立刻用
#   "download_header":{"Referer":"..."},    ← 网盘 quirk header，下载时要带上
#   "episode":{"id":1,"title":"...","duration_seconds":...}
# }
#
# 注意: 没有 INGEST_KEY 会返回 401。队列空时返回 {"job":null}。
# X-Worker-ID 可选（不传记为 "anonymous"），仅用于 admin 面板显示哪个 worker 在干。

# （可选）长视频转录中，worker 每 ~30s 打一次心跳防止被回收：
JOB_ID=<上一步的 id>
curl -s -X POST "$BASE/api/v1/subtitle-jobs/$JOB_ID/heartbeat" \
  -H "X-Ingest-Key: $INGEST_KEY"
```

## 5. worker 回传 SRT（用一段假字幕模拟 whisper 输出）

```bash
SRT='1
00:00:01,000 --> 00:00:03,000
你好，这是冒烟测试字幕。

2
00:00:04,000 --> 00:00:06,000
whisper worker 实际会传真实转录结果。'

curl -s -X POST "$BASE/api/v1/subtitle-jobs/$JOB_ID/complete" \
  -H "X-Ingest-Key: $INGEST_KEY" \
  -H "Content-Type: application/json" \
  -d "$(jq -n --arg s "$SRT" '{srt_content:$s}')"
# 期望: {"status":"done"}
# 这一步会: ① 把 SRT 存进 subtitles 表  ② job 置 done
```

## 6. 验证播放器能消费（这是端到端打通的标志）

字幕一落库，播放器的字幕通道立刻就能用——`GetPlayInfo` 已经会返回字幕列表。

```bash
# 先拿到 subtitle id（admin 列表）
SUB_ID=$(curl -s -b /tmp/cookie.txt "$BASE/admin/api/episodes/$EP_ID/subtitles" | jq '.[0].id')

# 直接取 VTT（播放器消费的就是这个端点，已现成）
curl -s "$BASE/api/v1/subtitles/$SUB_ID.vtt"
# 期望:
# WEBVTT
#
# 1
# 00:00:01.000 --> 00:00:03.000
# 你好，这是冒烟测试字幕。
# ...
```

## 7. （可选）失败 + 重试路径

```bash
# 再入一个任务，让 worker 报失败
curl -s -b /tmp/cookie.txt -X POST "$BASE/admin/api/subtitle-jobs" \
  -H "Content-Type: application/json" -d "{\"episode_ids\":[$EP_ID]}"   # 会被跳过(already_queued? 此时上一个已 done，会重新入队)
JOB2=$(curl -s -b /tmp/cookie.txt "$BASE/admin/api/subtitle-jobs?status=queued" | jq '.[0].id')

# worker 认领后报失败
curl -s -X POST "$BASE/api/v1/subtitle-jobs/$JOB2/claim" -H "X-Ingest-Key: $INGEST_KEY" >/dev/null
curl -s -X POST "$BASE/api/v1/subtitle-jobs/$JOB2/fail" \
  -H "X-Ingest-Key: $INGEST_KEY" -H "Content-Type: application/json" \
  -d '{"error":"whisper crashed"}'
# 期望: {"status":"failed"}

# admin 重试
curl -s -b /tmp/cookie.txt -X POST "$BASE/admin/api/subtitle-jobs/$JOB2/retry"
# 期望: {"status":"queued"}  (attempt 不清零，能看到试过)
```

## 协议小结（给 Python worker 实现者）

| 方法 | 路径 | 鉴权 | 作用 |
| :--- | :--- | :--- | :--- |
| POST | /api/v1/subtitle-jobs/claim | X-Ingest-Key（+ X-Worker-ID 可选） | 认领最高优先级任务，返回现签直链 |
| POST | /api/v1/subtitle-jobs/:id/complete | X-Ingest-Key | 回传 SRT，落库 + job done |
| POST | /api/v1/subtitle-jobs/:id/heartbeat | X-Ingest-Key | 刷新心跳，防被回收 |
| POST | /api/v1/subtitle-jobs/:id/fail | X-Ingest-Key | 报失败 |

worker 主循环（伪代码）：

```python
while True:
    job = POST /claim  with X-Ingest-Key + X-Worker-ID: <hostname>
    if job is None: sleep(30); continue
    try:
        srt = whisper_transcribe(job.download_url, headers=job.download_header)
        POST /{job.id}/complete {srt_content: srt}
    except Exception as e:
        POST /{job.id}/fail {error: str(e)}
```

## 安全/边界说明

- **直链会过期**：alist sign 有时效，必须每次 claim 后立刻用，不缓存。
- **网盘 header 要透传**：`download_header`（如 115 的 Referer）下载时必须带上，否则 403。
- **崩溃回收**：worker 崩溃/断电会留下 processing 任务，后端每 5 分钟扫描一次，
  认领超 30 分钟没心跳的退回 queued，可被重新 claim。
- **多 worker**：claim 是原子操作（单条 UPDATE…RETURNING），多台机器/多 worker 进程可同时消费，
  不会抢同一任务。X-Worker-ID 仅用于 admin 面板显示哪个 worker 在干，不做鉴权。
- **去重**：同一 episode 同时只允许一个 queued/processing 任务；已有字幕的 episode 拒绝入队。
- **娱乐内容不入队**：entertainment 课程的 episode 在入队时被业务层拒绝。
