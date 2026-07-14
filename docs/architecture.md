# StudyQuest (学途奇旅) — 架构设计文档

> 文档版本：v1.0 | 日期：2026-07-01
>
> 本文档记录了项目的核心架构选型、关键技术决策及其背后的理由。所有决策均经过讨论确认，作为后续开发的权威参考。

---

## 1. 项目定位

StudyQuest 是一个**长期个人学习资源管理平台**。初期面向小学 3-6 年级，但架构设计为全年龄段可扩展（初中、高中及以上）。本质上是家长为孩子搭建的**私有学习资料统一入口**，将散落在各处网盘中的课外视频资源转化为有结构、有进度追踪、有 AI 互动的学习体验。

### 关于"控制感"的产品思考

随着孩子年龄增长，平台应逐步从"家长管控模式"向"自主学习助手模式"过渡：

- 用户角色系统支持权限分级：`student` → `teen` → `self`
- 高年级用户可自主选课、自定学习进度
- 积分系统从"奖惩驱动"转向"成就展示"
- 以上转变均通过 Admin 面板灵活配置，不需要改动底层架构

---

## 2. 整体架构

三端分离、极度解耦，适应 2C2G（2 核 2G 内存）轻量服务器的硬件约束。

```
┌────────────────┐     ┌────────────────┐     ┌────────────────┐
│  Flutter PAD   │     │   Go Backend   │     │  本地工具链     │
│  (客户端)       │────▶│   (2C2G 服务器) │◀────│  (台式机)       │
│                │     │                │     │                │
│  Android PAD   │     │  Gin + SQLite  │     │  Python 脚本    │
│  Android TV    │     │  Admin 面板     │     │  Whisper/FFmpeg │
└────────────────┘     │  DeepSeek API  │     └────────────────┘
                       └───────┬────────┘
                               ▼
                       ┌────────────────┐
                       │  AList/OpenList │
                       │  (同一台服务器)  │
                       │                │
                       │  ├── /115/     │
                       │  ├── /tianyi/  │
                       │  └── /quark/   │
                       └────────────────┘
```

### Monorepo 目录结构

```
study-quest/
├── backend/             # Go 后端服务 + Admin Web 面板
├── frontend/            # Flutter PAD/TV 客户端
├── tools/               # 本地工具链（Python 流水线 + 各类辅助脚本）
│   ├── video-pipeline/  # Whisper 本地转录批处理
│   ├── import-scripts/  # 批量导入辅助
│   └── maintenance/     # 运维/备份/对账
├── docs/                # 项目文档
├── Makefile             # 顶层构建命令
├── .gitignore
└── README.md
```

**为什么用 `tools/` 而不是 `ai-worker-python/`？**
最初白皮书命名为 `ai-worker-python`，但该目录未来会放所有 helper 脚本（不限于 AI 和 Python），所以选了更通用的名字，内部按功能分子目录。

---

## 3. 技术栈选型及理由

### 3.1 Go 后端：Gin + GORM + SQLite

| 选型 | 备选 | 选择理由 |
|------|------|---------|
| **Gin** | Fiber | Gin 生态最成熟、中间件最丰富、社区文档最全、与 GORM 集成最顺滑。Fiber 虽然性能更高，但底层用 fasthttp 而非标准库 `net/http`，会限制中间件兼容性。2C2G 场景下 Gin 性能完全够用。 |
| **GORM v2** | sqlx / 原生 SQL | AutoMigrate 对 SQLite 支持好，开发速度快。与"骨架优先、能用就行"的 MVP 节奏匹配。后期如需精细控制可叠加 `golang-migrate`。 |
| **SQLite** | PostgreSQL / MySQL | 2C2G 服务器内存极其有限（整个后端常驻需控制在 50MB 以内）。SQLite 零进程、零端口、单文件、零配置，完美匹配家用轻量场景。 |

### 3.2 Flutter 客户端

一套 Dart 代码同时编译为安卓 PAD 端与安卓 TV 端 APK。UI 风格采用类"任天堂 Switch / 多邻国"的极简游戏化大圆角风格。全面使用 `ThemeData` 全局主题，严禁硬编码颜色值。

### 3.3 本地工具链

Python 脚本，主要运行在本地高性能台式机（有 GPU）。负责 FFmpeg 音频提取、本地 Whisper 字幕生成等重计算任务。生成的字幕会通过 API 或 `rsync`/`scp` 等方式上传到服务器端供后续流程使用。

---

## 4. 存储层架构（核心决策）

### 4.1 问题背景

视频资源分散在多种网盘（115、天翼云、夸克等），通过 AList/OpenList 统一挂载。需要解决：

1. **视频流分发**：2C2G 服务器不能中转视频流量
2. **文件指纹（Hash）获取**：白皮书的"双保险容灾检索"需要文件 SHA1
3. **多存储源兼容**：自己用有 AList，但如果给别人用，别人可能只有 WebDAV
4. **未来扩展性**：多源聚合的可能性

### 4.2 AList REST API vs WebDAV 调研结论

| 能力 | AList REST API | WebDAV 协议 |
|------|---------------|-------------|
| 获取文件 Hash (SHA1) | ✅ `/api/fs/get` → `hash_info.sha1`（115 原生支持） | ❌ 无标准属性（RFC 4918 未定义） |
| 获取下载 URL | ✅ `/api/fs/link` | ✅ 构造 URL 即可 |
| 302 直链 | ✅ AList 根据驱动自动决定 | ✅ AList WebDAV 实现内部做了 302 重定向 |
| 目录浏览 | ✅ `/api/fs/list` | ✅ `PROPFIND` |
| 文件元数据 | ✅ (size, modified, hash) | ⚠️ (size, modified，无 hash) |

**关键发现：WebDAV 也能 302 直链**。经过在 PotPlayer 中实际验证，AList 暴露的 WebDAV 端点在客户端请求文件下载时，会自动 302 重定向到网盘直链，视频流速度是直连速度，未经过服务器中转。

**但是 WebDAV 拿不到 Hash**。RFC 4918（WebDAV 协议标准）没有定义文件哈希属性。虽然部分实现（如 Nextcloud）有私有扩展，但这不是通用标准，AList WebDAV 也不暴露 hash。

### 4.3 最终方案：接口抽象 + 双实现

用 Go interface 定义统一的 `StorageProvider` 接口，提供两种实现：

```
                    ┌─────────────────────┐
                    │  StorageProvider    │  ← Go interface
                    │  (统一抽象接口)      │
                    └──────┬──────┬───────┘
                           │      │
              ┌────────────▼┐  ┌──▼────────────┐
              │ AList REST  │  │   WebDAV       │
              │ API 实现     │  │   通用实现      │
              ├─────────────┤  ├───────────────┤
              │ ✅ 文件浏览  │  │ ✅ 文件浏览     │
              │ ✅ 下载URL   │  │ ✅ 下载URL     │
              │ ✅ 302直链   │  │ ✅ 302直链     │
              │ ★ 文件Hash  │  │ ❌ 无Hash      │
              ├─────────────┤  ├───────────────┤
              │容灾：Hash    │  │容灾：路径+大小 │
              │优先+路径兜底 │  │  兜底          │
              └─────────────┘  └───────────────┘
```

**为什么不只用 AList REST API？**
自用场景下 AList 是首选（有 hash 加持）。但如果未来项目给别人用，别人不一定有 AList，可能只有通用 WebDAV 服务。WebDAV 虽然没有 hash，但白皮书的"第二优先级：路径 + 文件大小兜底"仍然能工作。接口抽象的成本很低（就是一个 interface），但扩展性收益巨大。

**为什么不需要原生 115 API？**
AList/OpenList 已经做了云存储抽象。115 的 SHA1、天翼云的直链等特性，AList 的 REST API 都已透传。直接对接 115 原生 API 增加巨大复杂度但无额外收益。

### 4.4 115 网盘的"强制代理"现实

115 是 AList 中的"强制代理"驱动——115 原始下载 URL 有 IP/Cookie 绑定限制，客户端无法从网盘服务器直接下载。无论走 REST API 还是 WebDAV，**视频流量都必须经过 AList 代理**。

但这对家用场景影响不大：PAD 和服务器在同一局域网内，视频流走的是内网带宽（通常 1Gbps 起步），而非公网带宽。其他网盘（天翼云等）如果支持直链，AList 会自动返回直连 URL，此时 302 直连方案天然生效。

### 4.5 多源扩展策略

**MVP 阶段**：单源，`settings` 表存当前激活的存储源配置（类型 + 连接参数），Admin 面板二选一。

**多源（已实现，见 §10.1）**：
- `storage_sources` 表：每行一个存储源（id, type, name, url, username, password, token, is_default）。Admin 全局配 N 个。
- `episodes.source_id` / `reading_books.source_id`（nullable）指向其来源；导入时选定。
- `StorageProviderResolver` 按 `source_id` 解析出 `StorageProvider`（进程内缓存）；`source_id` 为 NULL 时回退到全局 `settings` 兼容期，两者都无则报错。
- 用户存储源白名单 `user_storage_sources`（防呆）：授权时拦截 + 访问时兜底，防止 admin 误把受限源的内容授权给受限用户。空集合 = 不限制（向后兼容）。
- backfill 脚本 `cmd/backfill_sources`：建一个 `default` source（从旧 `settings` 读）并把 NULL `source_id` 回填（一次性，不写 migrate code）。

**未来扩展**（仍未实现）：
- 虚拟聚合层：跨源浏览和检索（多 source 拼成一棵树）。

---

## 5. 视频流分发方案

### 核心原则：Go 后端不碰视频字节流

```
PAD 请求 → Go 后端                    Go → StorageProvider              PAD → 存储
GET /episodes/:id/stream   →   provider.GetDownloadURL(path)   →   302 Redirect
                                       │                                  │
                                AList: /api/fs/link → URL          PAD 拿到最终 URL
                                WebDAV: 构造 WebDAV URL            直接从网盘/AList拉流
```

Go 后端只做一件事：查库拿到 `video_relative_path` → 问 StorageProvider 拿下载 URL → 302 重定向。视频字节流完全不经过 Go 进程。

### 容灾检索算法

当正常路径失败时（文件被移动/重命名），启动双保险检索：

```
1. 检查 provider.SupportsHash()
   ├─ true (AList) → 调 GetFileInfo 获取云端 hash
   │                 → WHERE file_hash = ?  （指纹匹配）
   │                 → 命中：更新路径，返回
   │                 → 未命中：降级到步骤 2
   │
   └─ false (WebDAV) → 直接进入步骤 2

2. 路径+大小兜底
   → WHERE original_relative_path = ? AND file_size = ?
   → 命中：更新当前路径，返回
   → 未命中：报错"资源不可用"
```

---

## 6. 数据库设计决策

### 6.1 表结构总览（14 张表）

| 表 | 用途 | 关键设计点 |
|---|------|----------|
| `settings` | KV 系统配置 | 存储源连接信息、Admin 密码等，由 Admin 面板管理 |
| `users` | 多用户 | PIN 码 bcrypt 哈希；`role` 支持 student/teen/parent/admin |
| `user_course_access` | 用户-课程权限 | 不同用户可访问不同课程库 |
| `courses` | 课程总表 | `grade`(TEXT) + `subject` 二维分类；`content_type` 区分 learning/entertainment |
| `episodes` | 课时明细 | 双保险索引：`file_hash`(indexed) + `original_relative_path` + `file_size`；`source_id` 指向其存储源（NULL = 走全局 settings 兼容回退，见 §4.5） |
| `subtitles` | 字幕直存 | SRT 纯文本存 DB，零延迟分发 |
| `ai_lesson_contents` | AI 数据 | 前置探险卡 + 后期复习测试，JSON 格式 |
| `user_points` | 积分总账 | 当前可用 + 历史总积分 |
| `points_ledger` | 积分流水 | 防刷分，支持审计 |
| `user_progress` / `entertainment_progress` | 学习/娱乐进度 | 断点续播 + 累计播放秒（aggregate，见 6.4）+ 80% 完成判定；娱乐单独成表避免污染学习统计 |
| `sessions` | 用户登录会话 | opaque token（32 字节 hex），一设备一行，固定 TTL，可 admin 撤销（见 §7） |
| `watch_events` | 观看事件流 | append-only，每行 = 一次"连续观看会话"（窗口合并后），回答"今天/某天学了多久、几点学"（见 §6.4） |
| `storage_sources` | 存储源配置 | 多源：admin 全局配 N 个网盘后端（alist/webdav），内容通过 source_id 指向其一（见 §4.5） |
| `user_storage_sources` | 用户存储源白名单 | 防呆：空集合 = 不限制（向后兼容）；非空时授权 + 访问两道拦截 |

### 6.2 相对于白皮书 DDL 的变更

| 变更 | 理由 |
|------|------|
| 新增 `settings` 表 | 业务配置（AList 连接等）存数据库，由 Admin 面板管理，不走环境变量 |
| 新增 `users` 表 | 白皮书引用了 `user_id` 但缺少 users 表 |
| 新增 `user_course_access` 表 | 不同用户可访问不同课程库 |
| `grade` 类型 `INTEGER` → `TEXT` | 支持 `"3"`, `"7"`, `"10"`, `"all"` 等灵活分类 |
| `pdf_relative_path` → `attachment_json` | JSON 数组，支持多类型附件（PDF、练习册、音频等），不限于 PDF |
| `file_size` / `duration_seconds` 改为可空 | Python 流水线分阶段填充，初次导入可能只有路径 |
| `user_progress` 增加 `watch_seconds` | 记录累计播放秒数，配合服务端 80% 完成校验 |
| 新增 `storage_sources` / `user_storage_sources` 表 + `episodes`/`reading_books` 加 `source_id` | 多源：admin 全局配 N 个网盘后端，内容跟源走；用户白名单防呆。NULL `source_id` 回退到全局 `settings` 兼容期（见 §4.5） |

### 6.3 配置管理策略

**环境变量**（仅启动必需，极简）：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `SERVER_ADDR` | `0.0.0.0:8080` | 监听地址 |
| `DB_PATH` | `./data/studyquest.db` | SQLite 文件路径 |
| `SESSION_TTL_HOURS` | `720`（30 天）| 用户登录会话有效期（固定，不滑动续期）。过期后客户端被踢回登录页重输 PIN |
| `INGEST_KEY` | 空（不强制）| Python 工具链灌库端点（`/api/v1/ingest/*`）的预共享密钥。空=端点公开（仅适合内网）；公网部署**必须**设置，否则任何人可 POST 篡改片库 |
| `TRUSTED_PROXIES` | `127.0.0.1,::1` | 逗号分隔的可信代理 CIDR/主机列表，Gin 据此读 `X-Forwarded-For` 解析真实客户端 IP（用于登录限速）。反代部署时设为反代所在网段 |
| `WATCH_MERGE_WINDOW` | `60` | 观看事件窗口合并阈值（秒）。客户端每 5~30s 发一次心跳，两次心跳间隔 ≤ 此值则合并成一行（一次"连续观看会话"）。0 = 禁用合并（每次心跳单独成行）。调大→行数少但单行跨度含更多暂停；调小→边界更准但弱网易切碎 |

> **部署提醒**：上述三项在纯局域网部署下均可保持默认。一旦把后端暴露到公网（即便前面套了 caddy/nginx），**必须设置 `INGEST_KEY`**，并按你的反代位置调整 `TRUSTED_PROXIES`，否则登录限速会按反代的回环 IP 统计而失效。

**`settings` 表**（所有业务配置，Admin 面板管理）：

| Key | 说明 |
|-----|------|
| `storage_type` | `"alist"` 或 `"webdav"` |
| `storage_url` | AList 地址 或 WebDAV 端点 URL |
| `storage_username` | 用户名 |
| `storage_password` | 密码 |
| `storage_token` | AList Token（可选） |
| `admin_password_hash` | Admin 面板密码（bcrypt） |

**为什么业务配置不走环境变量？**
因为这些配置需要在运行时通过 Admin 面板修改（比如换一个 AList 地址、更改 Token），重启服务不可接受。存数据库 + Admin 面板是最自然的方案。

### 6.4 学习时长：累计字段 + 事件表（双写）

学习时长有两套互补的存储，**双写**（同一次进度上报两边都更新）：

| 存储 | 形态 | 用途 | 局限 |
|------|------|------|------|
| `user_progresses.watch_seconds` / `entertainment_progresses.watch_seconds` | 每个 (用户, 课时) 一行的**累计总数** | dashboard 总时长、排行榜、用户列表"学习时长"列 —— 都是 O(1) 读 | 丢了时间维度：回答不了"今天/某天学了多久"、"几点学的" |
| `watch_events`（每行 = 一次"连续观看会话"） | append-only **事件流** | Admin "观看历史"页面：月历热力图 + 按日明细 | 按天聚合需 SUM（O(n)，但家庭量级无压力） |

**为什么双写而不是只存事件表？** 累计字段是"快照"，O(1) 读总数，dashboard/排行榜依赖它；事件表是"真相"，回答明细/时间维度。两者职责不同，双写成本几乎为零（多一次 insert），各自走最擅长的查询路径。

**窗口合并**：客户端每 5~30s 发一次心跳，后端把连续心跳合并成一行 `watch_events`（默认 60s 阈值，`WATCH_MERGE_WINDOW` 可配）。`duration_seconds` 只累加真实 delta（暂停不算）；`started_at ~ ended_at` 是墙钟跨度（含合并进来的暂停间隙）。Admin 明细页两个都显示，让操作者判断差值。

**预存 bug 修复**：此 PR 同时修掉了 `RecentDailyWatchSeconds` / `CountActiveUsersSince` / entertainment `GetTodayWatchSeconds` 的"按 updated_at 分桶累计字段"缺陷——这些查询现在改读事件表，按天/按时长聚合才正确。

---

## 7. 用户认证方案

### 7.1 PAD 端：头像选择 + PIN 码

家用场景，面向小学生，认证流程需要极简：

```
PAD 启动 → 显示所有用户头像 → 点击选择 → 输入 4-6 位 PIN 码 → 进入主界面
```

- PIN 码使用 bcrypt 哈希存储，绝不明文
- 登录成功后服务端签发**不透明 session token**（32 字节随机 hex，存 `sessions` 表），客户端在 `Authorization: Bearer <token>` 中携带。token **不是**用户 ID——历史上一度把用户 ID 当 token 用，那条路径已被显式拒绝（中间件对纯数字 token 返回 401）
- 一个用户可同时持有多条 session（一设备一条），互不影响——登录第二台不会踢第一台
- session 固定 TTL（`SESSION_TTL_HOURS`，默认 30 天），不滑动续期；过期需重登
- 登录端点按源 IP 限速（15 分钟内 5 次尝试后 429），防 PIN 暴力破解。限速用 `c.ClientIP()`（读 `X-Forwarded-For`，受 `TRUSTED_PROXIES` 约束），反代部署时务必正确配置可信代理
- 用户列表接口（GET /users）为公开接口（只返回头像和昵称，不含敏感信息）

**Admin 设备管理**：每个用户的活跃 session（设备）在 Admin 面板可见，可单独"踢下线"、全部下线，或为设备加备注（如"客厅 iPad"）。设备主标识是客户端登录时上报的 OS 设备名（`device_info_plus`），缺失时回退到 User-Agent。

### 7.2 Admin 面板：独立密码

Admin 面板有独立的登录密码（更强，存 `settings` 表），与 PAD 用户 PIN 码完全隔离。所有存储源配置、用户管理、课程分配等操作只能通过 Admin 面板完成。

> Admin 面板用独立的 `admin_session` cookie 鉴权（bcrypt 密码哈希作为 cookie 值），与 PAD 端的 Bearer token 体系完全分开。两套机制互不影响。

### 7.3 扩展路径

当前框架预留了升级空间：
- PIN → 更长密码（`role` 越高，密码要求越严格）
- 设备绑定 / 多因素
- HTTPS（由前置 caddy/nginx 终结 TLS，后端本身只跑明文 HTTP）

---

## 8. Admin 面板设计

### 8.1 MVP：Go 模板渲染

Admin 面板作为 Go 后端的一部分，使用 `html/template` 渲染页面，放在 `backend/internal/admin/templates/` 下。优点是零额外前端构建、与后端同进程部署、开发速度最快。

### 8.2 升级路径：SPA

未来切换到现代 SPA（React/Vue）时：
1. 新建 `admin-web/` 顶层目录（独立前端项目）
2. 打包后 `embed` 到 Go binary（`go:embed`）
3. Go 端的 API 路由（`/admin/api/*`）完全不需要改
4. 只需把模板渲染换成静态文件 serving

### 8.3 Admin 核心功能

| 功能 | 说明 |
|------|------|
| 用户管理 | 新建/编辑/删除用户，设置角色和 PIN 码 |
| 用户设备管理 | 每个用户可见其活跃登录设备（OS 设备名 + admin 备注），可单独/全部下线 |
| 观看历史 | 顶层页面，月历热力图（颜色=当日学习时长）+ 点某天看当日明细（学习/娱乐、时间段、有效时长、含暂停标注、当日合计） |
| 课程管理 | 创建课程，设置年级 × 科目分类 |
| 存储浏览 + 导入 | 通过 StorageProvider 浏览网盘目录，选择内容导入为课时 |
| 用户权限分配 | 设定每个用户可访问哪些课程 |
| 系统设置 | 配置存储源类型和连接参数，测试连接 |

---

## 9. 安全考量

| 安全规则 | 状态 | 说明 |
|---------|------|------|
| SQL 注入防护 | ✅ | GORM 全 ORM，零 SQL 拼接 |
| 密钥管理 | ✅ | 存储凭据存 DB，启动仅需 DB_PATH；PIN/Admin 密码 bcrypt 哈希 |
| 输入校验 | ✅ | 所有 ID 参数整数校验 |
| 用户认证 | ✅ | opaque session token（非 user ID），固定 TTL，可撤销，多设备并发；legacy `X-User-ID`/`Bearer<userID>` 已显式拒绝（见 §7） |
| 登录限速 | ✅ | 按 `ClientIP` 滑窗 15min/5 次 → 429；防 PIN 暴力破解。反代部署需配 `TRUSTED_PROXIES` 才能拿到真实 IP |
| 灌库端点鉴权 | ✅ | `/api/v1/ingest/*` 可由 `INGEST_KEY` 加 `X-Ingest-Key` header 校验（空=公开，仅内网） |
| CORS | ✅ | 严格来源限制，不使用 `*` 通配符；已移除 legacy `X-User-ID` 允许头 |
| 错误信息 | ✅ | 用户侧返回通用错误，详细日志在服务端；登录失败统一 401（不区分"用户不存在"/"PIN 错"，避免枚举） |
| 服务绑定 | ⚠️ | `0.0.0.0`（家用内网场景，非公网暴露） |
| HTTPS | ⚠️ | 后端只跑明文 HTTP，TLS 由前置 caddy/nginx 终结（纯局域网可不接） |
| 存储凭据加密 | TODO | MVP 明文存 DB，正式版需加密 |
| Admin CSRF | TODO | Admin 面板 POST 走 cookie，目前靠 SameSite + 同源，未加 CSRF token |
| mTLS 数据库 | N/A | SQLite 本地文件，无网络连接 |

---

## 10. 分阶段路线图

```
Phase 1 (MVP)                     Phase 2                      Phase 3
──────────────────────           ──────────────────────       ──────────────────────
Monorepo 骨架                     Flutter PAD 骨架             AI 前置探险卡阻断
Go 后端 + SQLite                  用户选择 + PIN 登录界面      复习测试弹窗
GORM AutoMigrate (10 表)          课程列表 GridView            积分系统
StorageProvider 接口               视频播放 + 302 流            附件查看/打印
AList + WebDAV 双实现              遥控器焦点系统               Python Whisper 流水线
302 视频重定向                     播放进度上报                 DeepSeek AI 生成
Admin 面板骨架                     断点续播                     主题换肤
Admin 导入 + 设置                                              Admin SPA 升级
用户 + PIN 认证                                                多源虚拟聚合
Makefile
```

### 10.1 已落地：存储源抽象 + 用户存储源白名单（branch 3）

> 状态：**已实现**。设计意图的真相源现在收敛到 §4.5 + §6.1；本节保留为落地摘要。

**动机**：多用户场景下分离不同的用户场景（例如家长追剧与小朋友资料分离）。此前存储配置全局唯一（`settings` 表 5 个 `storage_*` 键），所有用户共用一个网盘后端。

**方案 D（已落地）**：把"存储源"提成一等公民，存储源跟**内容**走，而不是跟用户走。

- 新表 `storage_sources`（type/url/username/password/token/is_default），admin 全局配 N 个；`user_storage_sources` 是用户白名单。
- `episodes.source_id` / `reading_books.source_id` 指向其来源；导入时选定（NULL = 走全局 `settings` 兼容回退）。
- 现有 `user_course_access` / `user_reading_*_access` 仍控制"谁能看哪些内容"。
- 用户存储源白名单（防呆）：授权时拦截（GrantAccess / readingGrant）+ 访问时兜底（GetPlayInfo / StreamBook），防止 admin 误把别的源的内容授权给受限用户。默认空 = 不限制（向后兼容）；staff（admin/parent）bypass；reading 文章无存储维度不拦。
- `StorageProviderResolver` 统一构造 provider（替换 4 处 `getActiveProvider()` 的 copy-paste），按 `source_id` 解析；NULL 回退全局 `settings`；凭据改了走 `Invalidate` 失效缓存。
- 灾难恢复（basename+size 兜底）按 `source_id` 限定作用域（`FindByBasenameAndSizeScoped`），避免 A 源自愈成 B 源路径；NULL `source_id` 退化为全局（兼容旧数据）。
- backfill 脚本 `cmd/backfill_sources`：建一个 `default` source（从旧 `settings` 读）并把 NULL `source_id` 回填（一次性，带 `-dry-run`，不写 migrate code）。
- Admin SPA：Settings 页"存储源管理"区块（多 source CRUD + 测连接，旧全局配置降级为兼容回退）；用户详情抽屉"允许的存储源"多选；Import 页存储源选择器。

**依赖**：branch 1（session 鉴权）是前提——白名单的隔离效果依赖真鉴权（否则 `X-User-ID` 可伪造）。已满足。

**测试**：repo 单元测试覆盖 CRUD + 白名单空/非空语义（空=不限制是向后兼容核心断言）；cmd/server 集成测试覆盖多源隔离、授权拦截、访问兜底、staff bypass、白名单经 user DTO 往返。

**仍不在范围**：存储凭据加密（独立 PR）、虚拟聚合（多 source 拼树）、删除全局 `storage_*` settings（留兼容期）。

**详见**：`docs/handoff-storage-sources.md`（branch 3 交接文档，含决策记录）。

---
