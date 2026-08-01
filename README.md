# StudyQuest

家长为孩子搭的**私有学习资源管理平台**:把网盘里散落的视频变成有结构、有进度、
有 AI 互动的学习体验。

- 给学生:在 PAD/TV 上看课程视频、做配套练习、和 AI 一起复盘弱点。
- 给家长:一个后台导入视频、编排课程、配置 AI、授权孩子访问。

> 家庭/私有部署,技术选型偏"好维护、单二进制部署",不追求横向扩展。

---

## 三端一览

| 端 | 路径 | 技术栈 | 干什么 |
|---|---|---|---|
| **后端** | `backend/` | Go + Gin + GORM + SQLite | 单一二进制,`go:embed` 内嵌 admin SPA。同时服务 `/admin`(SPA)、`/admin/api/*`(管理 JSON)、`/api/v1/*`(学生客户端) |
| **家长管理端** | `frontend-admin/` | React 18 + TS + Vite + TanStack Query + Tailwind | 导入视频、编排课程/章节、配 AI prompt、审阅术语、看学习数据。构建产物嵌入后端二进制 |
| **学生端** | `frontend/` | Flutter(Android/iOS/桌面) | 看视频、闯关做题、AI 学习建议/报告、错题本 |
| **TV 端** | `tv-android/` | Kotlin + Compose for TV | Android TV 原生,大屏看课(独立工程,工具链与 Flutter 不同) |
| **字幕 worker** | `tools/video-pipeline/` | Python(faster-whisper) | 离线跑视频转写,产 SRT 字幕回传后端,触发 AI 链(segment→summary→quiz) |

运行时就是一个 Go 进程 + 一个 SQLite 文件,TV/学生端是独立 APK。

---

## 快速开始

```bash
# 1. 首次准备:拉 ffmpeg/ffprobe + AI 模型(ONNX embedding runtime)
make fetch-ffmpeg fetch-ai-models   # 各幂等,已存在则跳过

# 2. 跑后端(首次会自动 AutoMigrate 建库;默认密码 admin,登录后立刻改)
make run            # = 构建并嵌入 admin SPA + go run 后端

# 3. 浏览器打开后台(默认 :8080)
#    http://localhost:8080/admin

# 4.(可选)前端热更新开发:另开终端
make run-admin      # vite dev server,代理到后端 :8080,改前端即时生效
```

构建客户端 APK:

```bash
make build-apk          # Flutter release APK(按 ABI split)
make build-tv-apk       # TV release APK(Kotlin)
```

测试:

```bash
make test               # 后端 Go 测试
make test-admin         # admin SPA 测试(vitest)
cd frontend && flutter test && flutter analyze   # 学生端
```

> 完整开发环境(JDK / Android SDK / Flutter / Python venv)见
> [`docs/dev-setup.md`](docs/dev-setup.md),部署见 [`docs/deployment.md`](docs/deployment.md)。

---

## 架构一瞥

```
                 ┌─────────────────────────────────────────────┐
                 │            单一 Go 二进制(:8080)            │
   家长 ───────► │  /admin        → 内嵌 React SPA(embed)      │
                 │  /admin/api/*  → 管理 REST JSON             │
   学生(PAD) ──► │  /api/v1/*     → 学生 REST JSON             │
                 │                                             │
                 │  SQLite(GORM)  ffmpeg/ffprobe  本地 ONNX    │
   TV(Kotlin) ──►│  embedding(BGE-small-zh)                    │
                 └──────────────────────┬──────────────────────┘
                                        │ 入队转写 job
                          ┌─────────────▼─────────────┐
                          │  Python whisper worker    │
                          │  (tools/video-pipeline)   │
                          └───────────────────────────┘
```

**AI 能力链**(都在后端,可关):视频转写 → 字幕分段 → 课程总结 → 出题(quiz/作业) →
字幕校对(polish)→ 学习建议/报告。无 provider 配置时系统行为等同"无 AI",是纯附加层。

**技术栈速查**:Go 1.23 / Gin / GORM / SQLite · React 18 / Vite / TanStack Query /
Tailwind · Flutter · Kotlin + Compose for TV · Python faster-whisper · ONNX Runtime。

---

## 文档导航

| 想了解 | 看这里 |
|---|---|
| 给 AI 助手/新人的一站式入口(代码布局、硬规则、 pitfalls 索引) | [`CLAUDE.md`](CLAUDE.md) |
| 整体架构与各模块职责 | [`docs/architecture.md`](docs/architecture.md) |
| 开发环境搭建 | [`docs/dev-setup.md`](docs/dev-setup.md) |
| 部署(含 Docker / 一键部署) | [`docs/deployment.md`](docs/deployment.md) |
| 业务规则(学科/年级/解锁/闯关) | [`docs/business-rules.md`](docs/business-rules.md) |
| 视觉 token(色值/字号) | [`docs/design-tokens.md`](docs/design-tokens.md) |
| AI 子系统深入 | [`docs/modules/ai/`](docs/modules/ai/)(overview / quiz-agent / subtitle-queue / llm-params) |
| 踩过的坑(必读防重蹈) | [`docs/pitfalls/`](docs/pitfalls/) |

近期待办见 [`TODO.md`](TODO.md);长远规划与已搁置方向见 [`docs/ROADMAP.md`](docs/ROADMAP.md)。
