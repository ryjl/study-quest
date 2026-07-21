# StudyQuest（学途奇旅）架构设计

> 整体架构权威文档。模块深度细节见 `docs/modules/<module>/`，踩坑见
> `docs/pitfalls/`，开发环境/调试见 `docs/dev-setup.md`。
>
> 文档版本：v2.0 | 最后更新：2026-07-21（对齐代码重构后的目录结构）

---

## 1. 项目定位

StudyQuest 是一个**长期个人学习资源管理平台**。初期面向小学 3-6 年级，但架构设计为
全年龄段可扩展（初中、高中及以上）。本质上是家长为孩子搭建的**私有学习资料统一
入口**，将散落在各处网盘（115 / 天际云 / 夸克，通过 AList/WebDAV 挂载）中的课外视频
资源转化为有结构、有进度追踪、有 AI 互动的学习体验。

### 三端架构

| 端 | 路径 | 技术栈 | 角色 |
|---|---|---|---|
| **Backend** | `backend/` | Go + Gin + GORM + SQLite | 单一二进制,含嵌入的 admin SPA。一个端口同时服务 `/admin`(SPA)、`/admin/api/*`(admin JSON)、`/api/v1/*`(Flutter 客户端) |
| **Admin panel** | `frontend-admin/` | React 18 + TS + Vite + TanStack Query + Tailwind | 家长管理端。导入视频、配置 AI、审核词汇、管理权限 |
| **Student client** | `frontend/` | Flutter(Android PAD/TV/desktop) | 学生端。视频播放、AI 学习、阅读室、积分成长 |

### 设计哲学

- **家长私有部署**：单机 2c2g 服务器跑得动,数据本地不外流
- **AI 纯附加层**：不配 provider / 课程不开 AI → 系统行为与之前完全一致(详见
  `docs/modules/ai/overview.md`)
- **三模块隐式契约**：Go `json:` tag ↔ TS interface ↔ Dart class,无 codegen,
  改任一侧都要同步另外两侧(红线,见 `docs/pitfalls/backend.md`)

---

## 2. 部署拓扑

```
┌────────────────┐     ┌──────────────────┐     ┌────────────────┐
│  Flutter PAD   │     │  Go Backend      │     │  本地工具链     │
│  / TV          │────▶│  (2c2g VPS)      │◀────│  (台式机)       │
│                │     │                  │     │                │
│  Android PAD   │     │  Gin + SQLite    │     │  Python 脚本    │
│  Android TV    │     │  + 嵌入 Admin SPA │     │  Whisper/FFmpeg │
└────────────────┘     │  + DeepSeek API  │     └────────────────┘
                       └──────┬───────────┘
                              ▼
                       ┌──────────────────┐
                       │  AList / WebDAV  │
                       │  (同机或旁机)     │
                       │  ├── /115/       │
                       │  ├── /tianyi/    │
                       │  └── /quark/     │
                       └──────────────────┘
```

### 关键边界

- **VPS 不背视频流量**：所有视频流通过现签的 CDN 直链下发,不过 VPS。VPS 只做协调
  (任务分发、字幕收发、AI 入队)
- **重计算旁路**：whisper 转录、ffmpeg 抽取等重活跑在用户带 GPU 的台式机上,通过
  `X-Ingest-Key` 鉴权的 `/subtitle-jobs/*` 协议和 VPS 通信
- **APK OTA 自洽**：APK 文件存在 `./data/releases/<version_code>/<abi>.apk`,
  `/api/v1/app/latest` 按 `(version_code, abi)` 寻址(**不按 DB id**,DB 重建不破坏
  OTA 契约)

### Monorepo 结构

```
study-quest/
├── backend/             # Go 后端 + 嵌入 admin SPA
├── frontend-admin/      # React admin SPA
├── frontend/            # Flutter 学生端
├── tools/               # Python 工具链(whisper worker 等)
│   └── video-pipeline/  
├── docs/                # 文档(架构/模块/踩坑/开发环境)
├── scripts/             # 杂项脚本
├── Makefile             
├── CLAUDE.md            # AI 助手 + 人类入口
└── TODO.md              # 待办 idea
```

---

## 3. 后端代码结构（`backend/internal/`）

按 Go 包分关注点。**改代码前先看 `CLAUDE.md` 的 Code layout 段。**

```
backend/internal/
├── model/            # GORM model,按域分文件
│   ├── identity.go     Setting/StorageSource/User/Session/UserCourseAccess
│   ├── grade.go        Grade / ContentType / SubjectCategory
│   ├── content.go      Subject/Tag/Course/Chapter/Episode/Media/Subtitle
│   ├── progress.go     UserPoint/PointsLedger/UserProgress/Badge/WeeklyTime
│   ├── unlock.go       CourseUnlockTemplate / UserUnlockOverride
│   ├── reading.go      ReadingSeries/Book/Article + access + progress
│   ├── ai.go           AIProvider/AIJob/AISummary/Quiz/... + AIConfig
│   ├── release.go      AppRelease (OTA)
│   ├── watch.go        WatchEvent
│   ├── migrate.go      AutoMigrate
│   └── models.go       20 行索引/overview
├── handler/          # Gin HTTP handler,按主题拆文件
│   ├── httperr.go      共享 helper: bindJSON/parseUintParam/parseLimit/respondError
│   ├── admin_ai_*.go   admin AI 接口分 4 文件(provider/jobs/results/lifecycle)
│   ├── admin_reading_*.go  5 文件(series/book/article/access/import)
│   ├── admin_{course,episode,chapter,subtitle}.go  admin_content 拆分后
│   └── ...
├── service/          # 业务逻辑
│   ├── ai_service*.go  分 8 文件(core/polish/jobs/naming/quiz/advice/course_summary/user_report)
│   └── ...
├── repository/       # GORM 查询
│   ├── ai_content_repo.go  接口 + 构造器
│   ├── ai_{chunk,summary,job,quiz,memory,advice}_repo.go  分 entity
│   └── ...
├── media/            # ffmpeg/ffprobe wrappers(本轮抽出的新包)
│   ├── ffmpeg.go       ExtractEmbeddedCover/ExtractScreenshot/RunFFmpegWithRetry/IsTransientFFmpegError
│   └── probe.go        ProbeMedia/IsBitmapSubtitleCodec
├── ai/               # LLM provider 抽象 + prompt 构造
│   ├── agent/          ReAct agent + 5 维度 prompt 配置
│   └── polish/         字幕润色 pipeline
├── storage/          # 网盘抽象(AList/WebDAV 统一接口)
├── subtitle/         # 字幕格式转换(SRT↔VTT 等)
├── appclock/         # 业务时区(Asia/Shanghai)注入,测试可替换
├── admin/spa/        # go:embed admin SPA 的入口
│   └── embed.go        //go:embed all:dist
├── config/           # 配置加载
├── middleware/       # Gin 中间件(auth/ingest_key/ratelimit)
├── router/           # 路由注册
└── testutil/         # 测试 helper(NewFileDB/NewDB)
```

### 技术选型理由

| 选型 | 备选 | 选择理由 |
|---|---|---|
| **Gin** | Fiber | Gin 生态最成熟、中间件最丰富。Fiber 用 fasthttp 底层,中间件兼容性受限。2c2g 场景 Gin 性能足够 |
| **GORM v2** | sqlx / 原生 SQL | AutoMigrate 对 SQLite 支持好,开发速度快。后期如需精细控制可叠加 `golang-migrate` |
| **SQLite** | PostgreSQL/MySQL | 2c2g 服务器内存极紧。SQLite 零进程、零端口、单文件、零配置,完美匹配家用场景。GORM 抽象层保留了未来切换可能 |
| **单一二进制 + go:embed** | 分离部署 | 一个 `make build` 出来的 binary 自带 admin SPA,部署 = 复制文件 + 跑 |

---

## 4. 存储层架构（核心决策）

> 详见 `docs/modules/storage/import-mapping.md`(网盘目录→DB 逻辑三层映射)。

### 多存储源抽象（ADR-003）

每个 `StorageSource` 是一个网盘后端配置(AList REST 或 WebDAV)。课程 episode 通过
`SourceID` 指向某个 StorageSource。用户**不持有**存储凭证——这是**导入侧**属性,
不是观看侧属性。

```
StorageSource (admin 配置)
  ├─ Type: alist | webdav
  ├─ URL/credentials
  └─ 多个 Episode 通过 SourceID 指向它
```

### 用户白名单（防误授权）

`UserStorageSource` 表是用户↔StorageSource 的白名单。admin 授权用户访问某个课程前,
后端检查该课程的 StorageSource 是否在该用户的白名单里——防止 admin 把"家长追剧盘"
里的内容误授权给小孩。

### 流分发：现签直链 + quirk header

播放/下载视频时,后端通过 `StorageResolver` 向 AList/WebDAV 签出**临时下载直链**
(带网盘 quirk header,如 115 的 `Referer`)。直链直接指向网盘 CDN,**不过 VPS**。
这正好匹配 2c2g 的能力边界——VPS 只发任务、收小字幕,不背视频流量。

详见 ADR-002(115 网盘 302 直链的协议细节)。

---

## 5. AI 子系统

> 模块深度文档：`docs/modules/ai/overview.md`(整体) + `quiz-agent.md`(出题) +
> `subtitle-queue.md`(字幕)。

### 纯附加层原则

AI 是**附加层**：不配 provider / 课程没开 AI → 系统行为与之前完全一致。客户端把
404 当"无 AI 数据",UI 隐藏 AI 卡片。这是"AI 是加分项不是依赖项"的产品定位。

### 三能力 + 词汇表挖矿

| 能力 | 触发 | 模型要求 |
|---|---|---|
| **Summary** | episode 字幕落库后入队 | 中 |
| **Quiz** | 学生请求时按需生成(ReAct + memory) | 中-高 |
| **Advice** | episode/course/subject 级建议 | 中-高 |
| **字幕润色** | 字幕入库后入队 polish job | 低(DeepSeek-chat 即可) |
| **词汇表挖矿** | 润色副产物 | — |

### Job 队列（in-process worker）

`AIService` 的 worker 是**进程内** goroutine(不是外部 worker——LLM 调用就是 HTTP
请求,不需要独立进程)。job 状态机：
`queued → processing → done | failed | skipped`。

worker **必须 context-cancellable**(`Stop()` 方法 + `t.Cleanup(svc.Stop)` 测试清理),
否则每个 `NewAIService` 泄漏一个永不退出的 goroutine,导致 flaky test。
详见 `docs/pitfalls/backend.md`。

### Provider 抽象

admin 在 UI 配多个 AI provider(OpenAI 兼容接口、中转站、本地模型)。每个 provider 有
`tags` 字段(JSON 数组),按任务路由：润色找 tags 含 `"polish"` 的,找不到 fallback
到默认 chat。多 provider + per-purpose tag 让"便宜的做润色、强的做出题"成为可能。

---

## 6. 字幕子系统

> 详见 `docs/modules/ai/subtitle-queue.md`。

### 三段式拓扑

```
admin 勾选 episode → subtitle_jobs(queued)
                       ↓ claim(原子,现签直链)
                       status=processing
                                          ← worker 下载视频 + ffmpeg 抽 16k 单声道
                                          ← faster-whisper 转录得 SRT
                                          ← POST /complete 上传 SRT
                       收到 SRT → 后端自动转 VTT → subtitles.vtt_content
                       job=done(播放器立刻可用)
```

### VTT 是唯一存储格式

`subtitles` 表只存 VTT(`vtt_content` 字段)。worker 上行仍用 SRT,后端自动转换。
**未来改字幕格式只动后端**,worker 不动。

---

## 7. 解锁与进度

### 三解锁策略

| 策略 | 语义 |
|---|---|
| `all_open` | 全部课时立即可见(默认) |
| `interval` | 每 N 秒解锁一集(学生自驱节奏) |
| `weekly` | 每周固定时刻解锁(按 `WeeklyTime` 配置) |
| `selected` | admin 手动指定哪些可见 |

加上 `UserUnlockOverride`(用户级覆盖) + `UserUnlockAllowedEpisode`(精确到集的白名单)。

### 娱乐课物理分表

`entertainment_progresses` 表独立存储娱乐内容观看进度,和学习数据零污染。娱乐课
**不触发 badge**(`progress_service` 在 entertainment 分支早 return)。

### 时区：`appclock` 注入

"今天 / 连续天数 / 深夜小时"必须走 `internal/appclock`(固定 Asia/Shanghai)。
**禁止** `time.Now()` 或 SQLite `'localtime'`——容器内会和 UTC 偏离,静默清零 streak。
详见 `docs/pitfalls/backend.md`。

---

## 8. 前端契约

### Admin SPA：TanStack Query 是 immediate source of truth

不是 server。所有写操作必须 `invalidateQueries` 对应的 key,否则 UI 读 stale 数据。
登录 bounce bug 就是这么来的。`frontend-admin/src/lib/api/` 拆 21 个域文件聚合为
`api` flat 对象,调用方一律 `api.foo()`,拆分纯为可导航性。

### Flutter：StatefulWidget + FutureBuilder + 单例 ApiService

无 Riverpod/Bloc/GetX。状态重的 screen(player_screen 等)用 StatefulWidget + 手动
`setState`。`ApiService` 是静态方法 + 单例(含 401 hook)。

**两个故意 bypass**(不是疏忽,是设计)：
- `update_service.dart`：OTA 跑在 pre-auth,没 session token
- `pdf_reader_screen.dart`：流式任意 book URL + 手动 byte count

详见 `docs/pitfalls/frontend.md`。

### 三模块隐式契约（红线）

Go struct 的 `json:` tag ↔ `frontend-admin/src/lib/types.ts` interface ↔
`frontend/lib/model/*.dart` class,**无 codegen、无 OpenAPI、无 protobuf**——完全靠手写
对齐。改任一侧的契约字段,必须同步另外两侧。详见 `docs/pitfalls/backend.md`。

---

## 9. 安全考量

### 认证

- **Admin**：session cookie(Gin session middleware)。`/admin/api/login` 设 cookie,
  `/admin/api/me` 验证。所有 `/admin/api/*`(除 login/logout/me)挂 `AdminAuth` 中间件
- **Student client**：用户 PIN 码登录(无密码),session 同样走 cookie。`/api/v1/*` 的
  受保护路由挂 `UserAuth`
- **worker 协议**：预共享密钥 `X-Ingest-Key`(环境变量),不走任何 session

### 速率限制

`/api/v1/users/login` 挂 `RateLimit` 中间件(防 PIN 码爆破)。

### 错误响应不泄露内部细节

`handler/respondError`(`httperr.go`)是单一 funnel：sentinel 错误 map 到对应 HTTP
status + 通用消息。真实错误细节**只 log 服务端**,客户端永远只看 generic 消息。
**handler 禁止直接返回 `err.Error()`**(binding 错误尤其要小心,详见
`docs/pitfalls/backend.md`)。

---

## 10. 关键架构决策（ADR 摘要）

> 完整 ADR 历史在 git log 里。这里只记**仍然有效**的决策摘要。

| ADR | 决策 | 理由摘要 |
|---|---|---|
| 001 | 存储 API 双支持(AList REST + WebDAV) | AList REST 功能强(目录穿透、sign),WebDAV 是开放标准兜底 |
| 002 | 115 网盘用 302 直链 + quirk header | 115 的 Referer 检查让普通 fetch 403,需要带 header 的签名直链 |
| 003 | 多存储源 + 用户白名单 | 多用户场景下分离"导入侧"和"观看侧"属性,防误授权 |
| 004 | 业务配置存 DB(`Setting` 表 KV)而非 env | admin 运行时可改,不需要重启；敏感的仍走 env(密码、ingest key) |
| 005 | 附件通用化 | 一开始 episode.附件 是 PDF 讲义专用,现在扩展为任意文件类型(图片/PDF/...) |
| 006 | Admin 面板用 React + Vite + go:embed | 单一二进制部署,SPA + API 同源(cookie session 顺滑) |
| 007 | 用户 PIN 码认证(无密码) | 小学场景,家长 PIN 码代替密码；admin 才有真密码 |
| 008 | 字幕重计算旁路到 GPU 台式机 | VPS 2c2g 跑不动 whisper,分到用户带 GPU 的台式机；通过 X-Ingest-Key 协议解耦 |

---

## 11. 演进方向（roadmap）

短期/中期可能的演进(未实现,详见 `TODO.md`)：

- **错题本**：聚合 `answers` 表,按 content_type='learning' 过滤,给学生看错题回顾
- **chat agent**：互动问答(Phase D,`docs/modules/ai/overview.md` 有设计)
- **多语言字幕**：`SubtitleJob.Language` 字段已支持扩展
- **PGS/VOBSUB OCR**：图形字幕目前禁掉抽取,未来接 Whisper OCR
- **PostgreSQL 切换**：GORM 抽象层保留可能,目前 SQLite 够用
