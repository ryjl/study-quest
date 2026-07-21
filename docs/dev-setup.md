# 开发环境与调试

## 标准命令

```bash
# 后端（Go + Gin + GORM + SQLite）
make build              # build backend（先 build-admin 嵌入最新 SPA）
make run                # build + 启动服务（http://localhost:PORT）
make test               # go test ./...

# Admin SPA（React + TS + Vite + TanStack Query）
cd frontend-admin
npm test                # vitest run
npx tsc --noEmit        # 类型检查（不 emit）
npm run build           # vite 生产 build（写到 backend/internal/admin/spa/dist/）

# Flutter 学生端
cd frontend
flutter analyze         # 静态检查
flutter test            # dart 测试

# Flutter APK（交叉编译多 ABI）
make build-apk          # 默认 fat APK
make build-apk-arm64    # 仅 arm64-v8a（现代 Android 设备）
make build-apk-arm      # 仅 armeabi-v7a（老设备）
make build-apk-x64      # 仅 x86_64（模拟器）
```

## 后端

### 首次启动

`make run` 自动执行：
- `AutoMigrate`（`backend/internal/model/migrate.go`）建/补表
- `SeedDefaultSubjects`（学科默认值）
- 如果 admin 密码未设，启动后访问 `/admin` 引导设置

DB 文件在 `backend/data/studyquest.db`（`.gitignore` 覆盖）。

### 改 model 后必须 `make test`

`go build` 绿 **≠** 行为绿。GORM struct tag / binding / json 序列化都不在 build 阶段暴露。
**model-layer change → `make test` every time**。详见 `docs/pitfalls/backend.md`。

### Admin SPA 嵌入机制

`backend/internal/admin/spa/embed.go` 用 `//go:embed all:dist` 把 SPA 构建产物嵌入
Go 二进制。`make build` 先调 `build-admin`（`cd frontend-admin && npm run build`），
输出写到 `backend/internal/admin/spa/dist/`，再 `go build`。

**关键**：backend 的 `go test` **不依赖** SPA build——所有集成测试走 `/admin/api/*`
JSON 端点，从不触发 SPA fallback 路由。`dist/` 空或 stale 都不影响 `go test`。
只有 `make build`/`make run` 需要先 build SPA。

SPA 没 build 时访问 `/admin` 会显示 `notbuilt.html` 的"SPA 尚未构建"提示。

## Flutter（WSL + MuMu 模拟器）

适用场景：开发者用 WSL 跑 Flutter 工具链（编译快、Linux 生态顺），但模拟器/真机接
在 Windows 侧。

### 环境拓扑

```
┌─────────────────────────────────┐     ┌──────────────────────────────┐
│ WSL2 (Linux)                    │     │ Windows 宿主机                │
│                                 │     │                              │
│ Flutter SDK                     │     │ MuMu 模拟器（Android 9/11）  │
│ adb（Linux 版，连 Windows IP）  │ ──→ │ adb（Windows 版）            │
│ Android SDK                     │     │                              │
└─────────────────────────────────┘     └──────────────────────────────┘
```

### Windows 侧：连上 MuMu

MuMu 默认 adb 端口 `7555`（MuMu 12 是 `16384`）。在 Windows cmd：

```cmd
adb connect 127.0.0.1:7555
adb devices   # 应该看到 emulator
```

### Windows 侧：装 APK + 看日志（最常用）

```cmd
adb install -r build\app\outputs\flutter-apk\app-arm64-v8a.apk

# 看日志（过滤 flutter + 应用 tag）
adb logcat | findstr "flutter study_quest"
```

### WSL 侧：直接调 adb（可选，进阶）

在 WSL 里也能连 Windows 的模拟器——把 adb 设为 Windows 主机 IP：

```bash
# WSL 里 ~/.bashrc 加：
export ADB_SERVER_SOCKET=tcp:$(cat /etc/resolv.conf | grep nameserver | awk '{print $2}'):5037

adb devices   # 应该看到 Windows 那边的 emulator
```

这样 `flutter run`、`flutter install` 都能直接在 WSL 里跑。

### Flutter 热重载调试

```bash
flutter run -d <device-id>   # 启动后按 r 热重载、R 热重启
```

### 常见坑

- **MuMu 路径白名单**：MuMu 的 Android 系统对 `/sdcard/` 写入有特殊限制，下载的 APK
  放 `%USERPROFILE%\Downloads\` 再用 MuMu 的"安装 APK"按钮更稳。
- **adb 版本不匹配**：Windows 的 adb 和 WSL 的 adb 版本差距大时，`adb server` 会
  反复 kill 重启。统一用最新 `platform-tools`。
- **网络隔离**：模拟器里的 App 访问 WSL 的 backend，要用宿主机的 LAN IP 而不是
  `localhost`（模拟器有自己的 localhost）。

## 字幕队列冒烟测试

纯后端协议验证，**不需要 Python whisper worker**。手动模拟 worker 上行，验证
claim → 转录 → complete 的完整链路。真正的 worker 按 `tools/video-pipeline/` 实现。

### 前置

- 后端已 `make run`
- 已设 `INGEST_KEY` 环境变量（worker 协议端点受 `X-Ingest-Key` 保护）
- admin 已设密码，至少有一个 learning 课程的 episode

### 1. admin 登录拿 cookie

```bash
curl -c cookies.txt -X POST http://localhost:PORT/admin/api/login \
  -H "Content-Type: application/json" \
  -d '{"password":"<your-password>"}'
```

### 2. 把 episode 加入字幕队列

```bash
curl -b cookies.txt -X POST http://localhost:PORT/admin/api/subtitle-jobs \
  -H "Content-Type: application/json" \
  -d '{"episode_ids":[1]}'
```

### 3. worker 认领任务（关键：返回现签的下载直链）

```bash
curl -X POST http://localhost:PORT/subtitle-jobs/claim \
  -H "X-Ingest-Key: $INGEST_KEY" \
  -H "Content-Type: application/json" \
  -d '{"job_types":["subtitle"]}'
# 返回 {job: {id, episode_id, download_url, download_header, language}, ...}
# job=null 表示队列空
```

### 4. worker 回传 SRT（用假字幕模拟 whisper 输出）

```bash
curl -X POST http://localhost:PORT/subtitle-jobs/<job-id>/complete \
  -H "X-Ingest-Key: $INGEST_KEY" \
  -H "Content-Type: application/json" \
  -d '{"srt":"1\n00:00:01,000 --> 00:00:03,000\n这是测试字幕\n"}'
```

后端自动 SRT→VTT 转换、存 `subtitles.vtt_content`、job 标 done。

### 5. 验证播放器能消费

```bash
# Flutter 端：进入这一课 → 看到"CC"字幕按钮可点 → 字幕显示
# 或直接 curl 验证 API
curl -b cookies.txt http://localhost:PORT/api/v1/episodes/1/play-info
# 应包含 subtitles 数组
```

### 协议小结（给 Python worker 实现者）

一次完整 round-trip：
1. `POST /subtitle-jobs/claim` → 拿到 job + 现签的 download_url
2. 用 download_url 下载视频，ffmpeg 抽 16k 单声道 wav
3. faster-whisper 转录得 SRT
4. `POST /subtitle-jobs/<id>/complete` 上传 SRT
5. 失败走 `POST /subtitle-jobs/<id>/fail`（带 error 消息）

长视频每 30s 发 `POST /subtitle-jobs/<id>/heartbeat` 防止 reaper 回收。

### 安全/边界

- `X-Ingest-Key` 是预共享密钥（环境变量），不是 admin session
- `/subtitle-jobs/*` 路由组挂 `IngestKeyMiddleware`，admin 的 cookie session 在这条
  路径上无效
- worker 是机器，不是浏览器
