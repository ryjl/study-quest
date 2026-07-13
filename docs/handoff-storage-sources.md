# Branch 3 交接：存储源抽象 + 用户存储源白名单

> 这份文档供**新会话**接手实现 branch 3 时使用，自包含——不必回读会话历史。
> 它是 `docs/architecture.md` §4.5 / §6.1 / §10.1 的展开版。落地后须同步更新那些章节。

## 背景一句话

多用户场景下分离不同用户场景（家长追剧 vs 小朋友资料）。当前存储配置全局唯一，所有用户共用一个网盘后端；目标是让不同内容来自不同存储源，并防止 admin 误把受限源的内容授权给受限用户。

## 为什么是这个方案（决策记录）

用户最初提"每用户挂自己的存储配置"。我们 brainstorm 后判定这是**逆数据流耦合**——存储是"导入侧"属性，用户是"观看侧"属性。最终选了 **方案 D + 用户白名单**：

| | C（每用户挂凭据） | **D（存储源跟内容走）✅ 选定** |
|---|---|---|
| 多 provider 支持 | ✅ | ✅ 全局配 N 个 `storage_source` |
| 跨用户分享一个剧 | ❌ 有歧义 | ✅ 授权一下就行 |
| 用户需要知道存储配置 | 是 | 否（admin 配，用户无感）|
| 安全模型 | 依赖 auth 强弱 | 靠 course 授权 + source 白名单 + `/stream` 鉴权（已就位）|

**白名单**是用户提的防呆：admin 配置时，若某用户只能访问 source_X，则禁止把 source_Y 的内容授权给他；访问时再兜底校验一次。

**关键依赖已满足**：branch 3 的隔离效果依赖真鉴权（否则 `X-User-ID` 可伪造）。`feat/session-auth` 已合 main，`/stream` 已在鉴权后，`/api/v1/episodes/:id/play-info` 的 unlock 门也在。

## 工作流（用户已定）

- **新 branch `feat/storage-sources`，从 `main` 拉**（branch 1/2 已合 main，无依赖关系）。
- **不写 migrate code**。新表靠 GORM `AutoMigrate` 自动建；老数据回填用一次性脚本。
- **上线前用户会手动删库重建**（`rm data/studyquest.db`），所以回填脚本只在"不删库"的场景才需要跑——但用户偏好删库，所以回填脚本可能根本不用执行。**实现时仍要写脚本**（万一用户改主意不删库），但不必做成 idempotent migrate。
- 测试要够（用户的反复强调）。鉴权那类"通过/拒绝"判断都要有测试守。

## 数据模型（新增，不改老表）

`backend/internal/model/models.go`：

```go
// StorageSource 是一个网盘后端配置（alist 或 webdav）。admin 全局配 N 个。
// 内容（episode/book）通过 source_id 指向它；用户不直接持有存储配置。
type StorageSource struct {
    ID       uint   `gorm:"primaryKey;autoIncrement"`
    Name     string `gorm:"size:100;not null"`            // 显示名，如"家长追剧盘"、"小朋友资料盘"
    Type     string `gorm:"size:20;not null"`             // "alist" | "webdav"
    URL      string `gorm:"size:1024;not null"`
    Username string `gorm:"size:255"`
    Password string `gorm:"size:255"`
    Token    string `gorm:"size:1024"`                    // alist only
    IsDefault bool   `gorm:"default:false"`               // 导入时的默认选项
    CreatedAt time.Time
    UpdatedAt time.Time
}

// UserStorageSource 是用户-存储源白名单（防呆）。空集合 = 不限制（向后兼容）。
type UserStorageSource struct {
    UserID     uint `gorm:"primaryKey"`
    SourceID   uint `gorm:"primaryKey"`
    CreatedAt  time.Time
}
```

**改老表**（加列，不破坏现有数据）：
- `episodes` 加 `SourceID *uint`（nullable，回填成 default source 的 id）。
- `reading_books` 加 `SourceID *uint`（同上）。

都加进 `AutoMigrate` 列表。

## 后端改动点（精确到文件）

### 1. `backend/internal/model/models.go`
- 加 `StorageSource` + `UserStorageSource`。
- `Episode` / `ReadingBook` 加 `SourceID *uint`。
- `AutoMigrate` 末尾加 `&StorageSource{}, &UserStorageSource{}`。

### 2. 新 repo `backend/internal/repository/storage_source_repo.go`
- `Create / Update / Delete / List / FindByID / GetDefault`。
- `WhitelistForUser(userID) ([]uint, error)` —— 返回该用户允许的 source id 列表。
- `SetWhitelist(userID, sourceIDs []uint)` —— 覆盖式写白名单。
- `IsAllowed(userID, sourceID) (bool, error)` —— 白名单为空时返回 true（不限制）；非空时查存在性。

### 3. 关键重构：4 处 `getActiveProvider()` 改成按 source 解析
**这是最大的改动面。** 当前 4 个 service 各自 copy-paste 了一份 `getActiveProvider()`，读全局 `settings` 表的 5 个 `storage_*` 键。改成接受 `sourceID` 参数（或 `episodeID` 反查）：

- `backend/internal/service/episode_service.go:73` — 定义处 + 2 个调用点（L232, L285）
- `backend/internal/service/import_service.go:93` — 定义处 + 5 个调用点（L109, L117, L519, L527 等）
- `backend/internal/service/reading_book_service.go:54` — 定义处 + 1 个调用点（L185）
- `backend/internal/service/reading_import_service.go` — 第 4 处定义

**重构方向**：抽一个 `StorageProviderResolver`（按 sourceID 解析出 `storage.StorageProvider`，带进程内缓存），4 个 service 共用一个实例，不再各自 copy。`storage.StorageProvider` 接口本身**不用改**（`NewAListProvider/NewWebDAVProvider` 已经是无状态、凭构造参数实例化的）。

### 4. `backend/internal/service/progress_service.go` 的 episode 查询
`ReportProgress` 已经查了 `ep`（拿到 `CourseID`）。watch-event 写入已经有 `ep.CourseID`。**这条路径不碰存储源**（进度上报不读网盘），所以不用改。但要确认 `GetStreamURL` 那条路径（episode_service L232）改成按 `ep.SourceID` 解析 provider。

### 5. 白名单拦截（两处）

**授权时**（admin 把某 course 授权给 user）：
- `backend/internal/handler/admin_user.go` 的 `GrantAccess` —— 加一道校验：取该 course 的所有 episode 的 source 集合，检查是否都在该 user 的白名单内。不在则 403 + 红字提示"该用户不被允许访问存储源 X"。
- 同理 reading-access 的 grant 端点。

**访问时兜底**（`/api/v1/episodes/:id/play-info` 的 unlock 门）：
- `backend/internal/handler/episode_handler.go:119` 的 `GetPlayInfo`，unlock 门已经做了 course 级授权检查（L139-160）。在此**再加一道**：user 的白名单非空时，检查 `ep.SourceID` 是否在白名单内。不在则 403。

### 6. admin 端点 + SPA
- `GET/POST/PUT/DELETE /admin/api/storage-sources` —— CRUD。
- `GET/PUT /admin/api/users/:id/storage-whitelist` —— 查/改某用户白名单。
- admin SPA：Settings 页加"存储源管理"区块（多 source CRUD + 测连接）；用户详情抽屉加"允许的存储源"多选。

### 7. backfill 脚本（一次性）
`backend/cmd/backfill_sources/main.go` 或 `tools/` 下：
1. 建一个 `default` source（从现有 `settings` 表的 5 个键读配置）。
2. `UPDATE episodes SET source_id = <default_id> WHERE source_id IS NULL`。
3. `UPDATE reading_books SET source_id = <default_id> WHERE source_id IS NULL`。
4. 把现有 5 个 `storage_*` settings 标记为 deprecated（不删，留作 fallback 兼容期）。

**用户偏好**：上线时会删库重建，所以这个脚本可能根本不跑——但写出来，万一用户不删库也能用。

## 测试（重点）

按 auth PR 的测试标准来（用户强调过"测试一定要够"）：

**单元**
- `storage_source_repo`：CRUD + 白名单的空/非空语义（空=不限制这条**必须测**，是向后兼容的关键）。

**集成**
- **多 source 隔离**：建 source_A（家长）、source_B（小朋友）；episode_X 挂 A、episode_Y 挂 B；user_家长白名单=[A]、user_小朋友白名单=[B]；断言各自 play-info 只能拿到自己 source 的内容。
- **白名单为空=不限制**：白名单空时，任何 source 的内容（已授权的 course 内）都能访问。**这是向后兼容的关键断言**。
- **授权拦截**：admin 把 source_B 的 course 授权给白名单只有 [A] 的 user → 403。
- **访问兜底**：白名单=[A] 的 user 直接请求 source_B 的 episode 的 play-info → 403（即使 course 授权了）。
- **跨 source 不污染**：两个 source 用不同后端（mock），互不影响。
- **backfill 脚本**（如写）：建 default + 回填后，所有现有 episode 的 source_id 非空。

## 数据 / 迁移

- 新表靠 `AutoMigrate` 自动建。
- **不写 migrate code**。
- **用户上线时大概率删库重建**（`rm data/studyquest.db`），所有表从 0 建。回填脚本只在"不删库"时跑。
- branch 1/2 引入的 `sessions` / `watch_events` 表也在删库后从 0 开始。

## 不在本 branch 范围

- 存储凭据加密（settings 表明文存的遗留问题，独立 PR）。
- 虚拟聚合（多 source 拼成一棵树）—— 以后再说。
- 迁移工具（删除全局 `storage_*` settings）—— 留兼容期。

## 已知约束 / 注意事项

- **`/stream` 路由**已在鉴权后（branch 1 改的），但**它本身不读 source**（直接 302 到网盘直链）。play-info 才是解析 source 的地方。stream 的隔离靠"拿不到 play-info 就拿不到直链 URL"间接保证——确认这条仍成立。
- **import_service** 有 5 个 `getActiveProvider` 调用点（最密集），改的时候要逐个确认是按"导入到哪个 source"还是"从哪个 source 读"——两个方向可能需要不同 sourceID 上下文。
- **测试 helper**（`cmd/server/testhelper_test.go`）当前不建 storage source；branch 3 的集成测试要自己建 source fixture。
- **device_info_plus / session** 这些 branch 1/2 的设施都已经合 main，branch 3 直接用。

## 实施顺序建议

1. 模型 + repo + backfill 脚本（不接业务，先能存）。
2. `getActiveProvider` 重构成 `StorageProviderResolver`（4 个 service 一起改，**这是风险最高的步骤，改完跑全量测试确保没回归**）。
3. episode/book 加 source_id + 导入时选定。
4. 白名单表 + 两道拦截（授权 + 访问兜底）。
5. admin 端点 + SPA（CRUD source + 用户白名单多选）。
6. 测试补全 + 文档同步（§4.5 / §6.1 / §10.1 从"未实现"改成"已实现"）。
