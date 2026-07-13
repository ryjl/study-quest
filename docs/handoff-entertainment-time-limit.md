# 交接文档：路径迁移脚本 + 娱乐限时功能

> 本文档记录 v1 发布后待做的两个功能块。v1 的数据库 schema 已为这两项预留了所有必要的表和字段，实现它们**不需要任何 schema 变更**。

---

## 一、路径迁移脚本（Phase 6 / C3）

### 背景

当存储后端从 AList 切换到 WebDAV（或反向），DB 里 `episodes.video_relative_path` 存的是旧后端视角的路径，新后端下会 404。需要一个一次性运维脚本，把旧路径批量重映射为新后端上的正确路径。

v1 已废除 hash 机制，容灾/迁移改为 **basename + filesize 匹配**。

### 不需要改 schema

`episodes.video_relative_path` 和 `reading_books.file_relative_path` 都是可更新的 `text` 列。脚本只改值，不改结构。

### 实现规格

**位置**：`backend/cmd/migrate-paths/main.go`（独立 CLI 工具，不进 server 二进制）

**流程**：

```
1. 连接新后端（从 settings 表读 storage_type/url/credentials，复用 storage.StorageProvider）
2. 递归 ListDir 扫描新后端，构建 map[basename+filesize] → newPath 的完整索引
3. 遍历 DB 里所有 episodes（和 reading_books）：
   - basename = filepath.Base(episode.OriginalRelativePath)
   - 在索引里查 basename + *episode.FileSize
   - 匹配上 → UPDATE video_relative_path = newPath
   - 匹配不上 → 记入"未找到"列表
4. 输出报告：X 条已迁移，Y 条未找到（附 basename 列表）
```

**关键设计点**：

- 匹配键是 `basename + filesize`（不是 hash）。basename 碰撞靠 filesize 区分，双碰撞概率极低。
- `OriginalRelativePath` 优先于 `VideoRelativePath` 作为 basename 来源（前者是导入时的原始路径，更稳定）。
- 脚本应在**停机状态**下跑（或至少停止播放），避免迁移过程中有新的进度写入。
- 运行前务必备份数据库：`cp data/studyquest.db data/studyquest.db.bak`

**接口参考**：

- `storage.StorageProvider`（`backend/internal/storage/provider.go`）— `ListDir(path)` 返回 `[]FileInfo{Name, Path, Size, IsDir}`
- `config.LoadConfig()`（`backend/internal/config/config.go`）— 读 `DB_PATH`
- `repository.NewEpisodeRepository(db)` — `FindByBasenameAndSize(basename, size)` 已有

**CLI 用法**（建议）：

```bash
# 先在 admin 设置页改好新后端地址，测试连接通过后：
cd backend && go run cmd/migrate-paths/main.go
# 输出：Migrated 58 episodes, 0 not found (backup at data/studyquest.db.bak)
```

### 风险

- 同名同大小不同文件 → 误匹配。概率极低（同一课程目录下不会有），但脚本应在报告里列出匹配结果供人工确认。
- 新后端目录树很大 → ListDir 扫描慢。可加 `--path` 参数限定扫描根目录。

---

## 二、娱乐限时功能

### 背景

家长希望控制小朋友的娱乐视频观看时间：每日/每周累计不超过 N 分钟（时长上限），以及只在特定时段开放（时段限制）。

### 不需要改 schema

v1 已预留：
- `entertainment_progresses.watch_seconds` — 每用户每集的累计观看秒数（原子累加，和学习的 `user_progresses.watch_seconds` 同模式）
- `entertainment_progresses.updated_at` — 用于按日/周求和
- `EntertainmentRepository` 已有两个聚合方法预留：
  - `GetTodayWatchSeconds(userID) → int64`（按业务日求和）
  - `GetWeekWatchSeconds(userID) → int64`（最近 7 天求和）

### 功能拆分

#### 2a. 时长上限（每日/每周 N 分钟）

**后端逻辑**（`progress_service.go` 或新建 `entertainment_service.go`）：

```go
// 在 ReportProgress 的娱乐分支里，UpsertProgress 之后加一个上限检查：
func (s *progressService) ReportProgress(...) {
    if s.isEntertainmentCourse(ep.CourseID) {
        entProg, _ := s.entertainmentRepo.UpsertProgress(...)

        // 新增：检查今日是否超限
        dailyLimit := s.getEntertainmentDailyLimit()  // 从 settings 读
        todaySecs, _ := s.entertainmentRepo.GetTodayWatchSeconds(userID)
        if dailyLimit > 0 && todaySecs >= dailyLimit {
            // 超限：返回一个特殊标记，前端据此显示"今日娱乐时间已用完"
            return entProg, ErrEntertainmentDailyLimitReached
        }
        return entProg, nil
    }
    ...
}
```

**需要的 settings 新增**（存 `settings` 表，admin 管理，不改 schema）：

| key | 默认值 | 说明 |
|---|---|---|
| `entertainment_daily_limit_minutes` | `0`（不限制） | 每日娱乐时长上限（分钟），0=不限 |
| `entertainment_weekly_limit_minutes` | `0`（不限制） | 每周娱乐时长上限（分钟），0=不限 |

**前端（学生端）**：
- 播放器收到 `ErrEntertainmentDailyLimitReached` 后，暂停播放并弹窗"今日娱乐时间已达上限"
- 娱乐 Tab 顶部显示今日已看 X 分钟 / 上限 Y 分钟（调用新 API `GET /api/v1/entertainment/usage`）

**前端（管理端）**：
- 设置页加"娱乐限时"卡片：两个数字输入框（日上限、周上限），保存到 settings

#### 2b. 时段开放（只在特定时间可看）

**后端逻辑**：

```go
// 在 ReportProgress 的娱乐分支里，UpsertProgress 之前加一个时段检查：
if !s.isEntertainmentAllowedNow() {
    return nil, ErrEntertainmentTimeWindowClosed
}
```

**需要的 settings 新增**：

| key | 格式 | 说明 |
|---|---|---|
| `entertainment_allowed_windows` | JSON `[{"weekday":0,"start":"19:00","end":"20:30"},...]` | 允许观看的时段。空=不限制。weekday 0=周日..6=周六。时间按业务时区（appclock）解释 |

**前端（学生端）**：
- 娱乐 Tab 在非开放时段显示"娱乐时间未开放，下次开放：周日 19:00"
- 播放器在非开放时段拒绝播放（后端 gate + 前端提示）

**前端（管理端）**：
- 设置页加"娱乐开放时段"编辑器（复用 unlock 的 weekly_times UI 模式，加 start/end）

### 实现顺序建议

1. **先做时长上限**（2a）—— 价值最高，逻辑最简单（一个数字 + 一个比较）
2. **再做时段开放**（2b）—— UI 复杂度较高（时段编辑器），但后端逻辑简单

### 涉及的已有代码

| 文件 | 改什么 |
|---|---|
| `backend/internal/repository/entertainment_repo.go` | `GetTodayWatchSeconds` / `GetWeekWatchSeconds` 已有，可能需要加 `GetAllUserUsage()`（批量） |
| `backend/internal/service/progress_service.go` | `ReportProgress` 娱乐分支加上限+时段 gate |
| `backend/internal/handler/episode_handler.go` 或新 handler | 暴露 `GET /api/v1/entertainment/usage` |
| `backend/internal/handler/admin_auth.go` | settings GET/PUT 加新 key |
| `frontend/lib/ui/screen/entertainment_screen.dart` | 顶部加用量显示 + 时段提示 |
| `frontend-admin/src/pages/Settings.tsx` | 加娱乐限时配置卡片 |

### 测试要点

- 日上限触发后，学习视频不受影响（entertainment_progress 独立于 user_progresses）
- 时段切换（跨过 00:00 业务日界线）后，日计数归零
- 上限设为 0 时 = 不限制（不是"禁止"）
- 多设备同时看娱乐视频时，watch_seconds 的原子累加不会丢（UpsertProgress 用 ON CONFLICT DO UPDATE）
