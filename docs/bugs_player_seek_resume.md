# 播放器进度条 / 续播 / 完成判断 Bug 复盘

> 日期：2026-07-05
>
> 本文记录了一次围绕视频播放进度条的集中修复过程。核心教训是**日志驱动、不猜测**——一个本质上是"调整播放位置"的常见需求，因为前期靠假设反复试错，绕了三大圈才定位根因。

---

## 背景

学员端 Flutter app（`media_kit` / libmpv 后端）通过 AList 拉取天翼云盘视频。修复前存在一组相互关联的症状：

1. **进度条拖动后整体坏掉**：时长显示 `0:00`、Slider 拖不动
2. **续播失效**：下次打开从 0 开始，不回到上次位置
3. **完成状态误判**：视频没看完却被标记 `isCompleted`
4. **倍速按钮无响应**、**选课页 overflow**、**课程目录时长显示 `--:--`**

## 根因与修复

### Bug 1：控件 overlay 重建导致 StreamBuilder 丢状态

**症状**：进度条前 1 秒能拖动，控件自动隐藏后再唤出就坏了（时长变 0、Slider 禁用）。

**根因**：控件用 `if (_controlsVisible)` 控制显隐。隐藏时整个 `StreamBuilder` 子树被销毁，再显示时重建——重建瞬间 `position` / `duration` / `buffer` stream 都还没吐出新事件，`snapshot.data` 为 null，fallback 成 `Duration.zero`。后果：`totalMs = 0` → Slider 的 `onChanged: null`（禁用）。

**修复**：三个 StreamBuilder 都加 `initialData: _player.state.xxx`，重建时立刻用 player 当前同步状态，不回 0。

**教训**：UI 子树的 mount/unmount 会让 StreamBuilder 重新订阅，必须提供同步的 initialData 兜底。

### Bug 2：Slider 拖动期间逐帧 seek 撕裂 demuxer

**症状**：拖动进度条时 demuxer 反复重建（`VideoOutput.Resize` 每次 handle 不同）。

**根因**：Slider 的 `onChanged` 在拖动**每一像素**都调 `_player.seek()`。libmpv 收到密集 seek，每次拆掉 demuxer 重建。

**修复**：标准播放器模式——`onChangeStart` 进入预览、`onChanged` 只更新本地显示数字、`onChangeEnd` 松手时只 seek 一次。

### Bug 3：续播 seek 被 CDN 网络重连 reset（最难的一个）

**症状**：续播 seek 成功（日志显示 `pos=405`），但 1～17 秒后 position 被打回 0。用户看到"跳到续播点 → 闪一下 → 回到开头"。

**根因**：天翼云 CDN 的连接会在播放过程中周期性 drop，libmpv 检测到断流后**重新从头 open**，position 重置为 0。这是网络/CDN 层面的 reset，**不是 seek 时机问题**。

**走过的弯路**（前 3 次都基于错误假设"找对时机就能一次成"）：

| 尝试 | 为什么失败 |
|---|---|
| 监听 `duration` / `playing` / `buffering` 各种事件触发单次 seek | 全被 CDN 的周期性 reset 打回 |
| `Media(start: offset)` 让 demuxer 直接从 offset 打开 | 初始 position 对了，但仍被 reset |
| `open(play:false) → seek → play()` 官方推荐模式 | `play()` 在这个流上本身也会触发 reset |

**最终修复**：`Media.start`（让初始 position 接近 target，减少可见闪烁）+ **持久 watchdog**（监测 position，一旦被异常打回 0 且远离 target，就重新 seek，最多重试 8 次，直到用户播放越过 target + 10 秒视为稳定）。

**教训**：
- 抓完整 position 时间序列，**让数据说话**，不要凭症状猜。
- 看到 `pos=22 → pos=0` 的周期性 reset 模式，应该立刻判断是周期性 reset，直接上对抗式 watchdog，而不是反复试"换事件触发时机"。

### Bug 4：完成判断用了累计观看时长（逻辑错误）

**症状**：视频没看完却被标记 `isCompleted=1`。

**根因**：后端判断逻辑是 `WatchSeconds >= duration * 0.8`。`WatchSeconds` 是**累计观看秒数**（每次上报 delta 之和）。用户反复看前半段（每次看几分钟退出再进），累计起来达到 80% 就被标完成——但实际播放位置从没到过 80%。

**修复**：改成 **`LastPositionSeconds >= duration * 0.9`**（位置到 90% 才算完成）。

**配套设计原则**：`isCompleted` 与续播**解耦**。
- `isCompleted` 只影响：发奖励、触发 quiz、列表显示"已完成"标记
- **续播永远回到 `LastPositionSeconds`**，不管是否完成
- 这样"看到 90% → 标完成 → 下次打开" 仍然续播到 90%，不会强制从头（用户直觉预期）

### Bug 5：倍速按钮 / overflow / 时长 `--:--` / 转圈延迟

| 问题 | 根因 | 修复 |
|---|---|---|
| 倍速按钮无效 | `PopupMenuButton` 的 child 被包在 `FocusButton(onPressed:(){})` 里，吞了手势 | 直接用 Container chip 作 child |
| 选课页 `bottom overflowed` | 根 Column 固定高度 + GridView 用 `Expanded` 在窗口小时溢出 | 包 `SingleChildScrollView`，GridView 改 `shrinkWrap: true` |
| 课程目录时长全 `--:--` | AList `/api/fs/get` 不返回媒体信息；DB `duration_seconds` 全空 | 后端加 ffprobe worker（见下） |
| seek 后转圈延迟消失 | 直接读 `state.buffering` 不触发 rebuild | 改 StreamBuilder + 只在 `playing && buffering` 时显示 |

## 媒体信息 probe（ffprobe 后台 worker）

AList 只返回文件系统级元数据（大小、修改时间、hash），**不解析容器**。要拿到时长/编码/分辨率必须 ffprobe（实测对天翼直链 0.75s 出结果，只读头部）。

**设计**（带限流，避免网盘 API 限流）：
- 单 goroutine 串行队列，每文件间隔 **3 秒**
- 触发：导入完成自动排队 + admin 手动按钮
- DB 新增 `episodes.media_meta_json`（存 `MediaMeta`：duration/codecs/resolution/bitrate/全部 streams）
- admin 设置页加"扫描缺失时长"卡片 + 进度轮询

实测 48 集 4K MKV 全部 probe 成功，0 失败。

## 改动清单

**后端**：
- `model`：新增 `MediaMeta` / `MediaStream` 类型 + `Episode.MediaMetaJSON` 字段
- `service/episode_service.go`：`probeMedia()` ffprobe shell out（30s 超时）
- `service/probe_worker.go`：单例串行队列 + 3s 限流 + 进度统计（新建）
- `service/progress_service.go`：完成判断改为 position-based，阈值 90%
- `service/import_service.go` + `handler/ingest_handler.go`：接 `enqueueProbe` 回调，导入后自动排队
- `handler/admin_handler.go`：`ScanMissingDurations` + `ProbeProgress` 端点
- `repository/episode_repo.go`：`ListByNullDuration()`

**前端**：
- `player_screen.dart`：
  - StreamBuilder 加 `initialData`（Bug 1）
  - Slider `onChangeStart/End` 模式（Bug 2）
  - `Media.start` + watchdog 续播（Bug 3）
  - 进度条三层 trackShape（播放/缓冲/未缓冲）
  - buffering 转圈 StreamBuilder 化
  - 倍速 PopupMenuButton 修手势
  - mpv 调优（`demuxer-lavf-probe-info=no` 避开天翼 HEAD 403）
- `course_detail_screen.dart`：episode 行显示续播进度条
- `course_list_screen.dart`：包 SingleChildScrollView 修 overflow
- `android/app/build.gradle.kts`：`packaging.jniLibs.excludes` 只打 x86_64

## 核心教训

1. **日志驱动，不猜测**。一个"调整位置"的需求折腾一整天，主因是前期靠症状假设反复试错。应该一开始就抓完整 position 时间序列，看清 reset 模式。
2. **StreamBuilder 子树重建**必须配 `initialData`，否则 unmount/remount 时回 null。
3. **Slider 拖动 = 每像素一次回调**，密集 IO 操作（seek、网络请求）必须在 `onChangeEnd` 做。
4. **CDN 直链的隐含约束**：网络重连会触发 libmpv 重新 open、reset position。这类问题应用层只能对抗（watchdog），根治需要换稳定的流源或自建代理。
5. **完成判断与续播位置必须解耦**——这是产品直觉，不能因为"已完成"就强制从头。
