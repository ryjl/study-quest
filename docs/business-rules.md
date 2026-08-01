# 跨端业务规则契约

> **目的**：StudyQuest 有两个前端（PAD 用 Flutter / Dart，TV 用 Kotlin），两端语言不通，无法共享代码。
> 但业务规则必须一致。本文档是这些规则的**唯一事实源**，两端实现都要对照它，并由测试保证语义一致。
>
> **TV 端实现位置**：`tv-android/app/src/main/java/app/studyquest/tv/`
> **PAD 端实现位置**：`frontend/lib/`（Flutter，现有）
>
> 改任一规则 → 改本文档 → 两端同步 → 两端测试同步更新。

---

## 1. 字幕选项合并（Subtitle Merge）

播放器的字幕菜单要展示一个去重列表。字幕有两个来源，合并规则如下。

### 来源

| 来源 | 说明 | 质量 | 怎么拿 |
|---|---|---|---|
| **backend 字幕** | 后端 `/episodes/:id/play-info` 返回的 `subtitles[]`，Whisper 转录 + LLM 校对生成的 VTT 文件（URL） | 高（校对过） | play-info 响应的 `subtitles` 字段 |
| **native 字幕** | 视频容器（MKV/MP4）内嵌的字幕轨道 | 原始 | 播放器从容器抽出（ExoPlayer `tracks.text` / media_kit `player.state.tracks.subtitle`） |

### 合并规则

1. 菜单第一项永远是 **「无」**（关闭字幕），`type: 'off'`。
2. **backend 字幕优先**：逐条展示，用其原始 label。同时登记它的 label 和 language。
3. **native 兜底去重**：逐条检查 native 字幕，如果它的 **label 或 language** 跟某个 backend 字幕重复 → 跳过。不重复的才展示。
4. native 的 label 取值优先级：`track.title` → `track.language` → `"内置字幕 ${track.id}"`。

### 为什么这么做

同一条「中文」字幕，backend 有一份（校对过），native 容器里也有一份（原始）。不合并的话菜单出现「中文」+「中文(校对版)」两个按钮——点 native 那个会被 backend 那份覆盖，出现「点了没字幕」的 bug。backend 优先是因为 LLM 校对过，质量更高。

### 伪代码

```
list = []
seenLabels = {}      # 已展示的 label 集合
seenLanguages = {}   # 已展示的 language 集合

list.add({label: "无", type: "off"})

for sub in backendSubtitles:              # 1) backend 全展示,登记去重键
    if sub.label in seenLabels: continue
    seenLabels.add(sub.label)
    seenLanguages.add(sub.language)
    list.add({label: sub.label, type: "backend", track: sub})

for track in nativeSubs:                  # 2) native 去重兜底
    label = track.title ?: track.language ?: "内置字幕 ${track.id}"
    if label in seenLabels: continue
    if track.language != null && track.language in seenLanguages: continue
    seenLabels.add(label)
    list.add({label: label, type: "native", track: track})

return list
```

### backend 字幕 URL

backend 字幕项要解析成绝对 URL 才能给播放器。play-info 返回的 `sub.url` 是相对路径（如 `/api/v1/subtitles/3.vtt`），需拼成 `baseUrl + sub.url`。

### 参考

- PAD 实现：`frontend/lib/service/track_selection_controller.dart` 的 `mergeSubtitleOptions`（纯函数，行 48）
- 测试：`frontend/test/service/track_selection_controller_test.dart`

---

## 2. 音轨选项（Audio Options）

播放器音轨菜单（多音轨剧集才显示）。

### 规则

1. 从播放器抽出的音轨里过滤掉 `id == "no"` 和 `id == "auto"`（media 容器的"无/自动"占位）。
2. 剩下的逐条展示，label 取值优先级：`track.title` → `track.language` → `"音轨 ${track.id}"`。
3. **菜单只在音轨数 > 1 时显示**。单音轨（绝大多数剧集）不显示音轨菜单。

### 参考

- PAD 实现：`frontend/lib/service/track_selection_controller.dart` 的 `audioOptions`（行 90）

---

## 3. 章节分组（Chapter Grouper）

课程详情页把平铺的 episode 列表按 chapter 分组展示。

### 规则

输入：`episodes[]`（平铺列表）+ `chapters[]`（章节目录）。

1. **chapters 排序**：按 `sortOrder` 升序，`sortOrder` 相同则按 `id` 升序。
2. **episode 分桶**：逐个 episode 检查它的 `chapterId`：
   - `chapterId > 0` 且该 id 在 chapters 列表里 → 归入对应 chapter 桶。
   - 否则（`chapterId == 0` 或指向不存在的 chapter）→ 归入「未分组」桶。
3. **输出顺序**：按排序后的 chapters 顺序，每个 chapter（只要桶非空）输出一个分组；最后如果有未分组 episode，追加一个未分组桶。
4. **未分组桶标题**：
   - chapters 列表为空 → 标题「全部课时」
   - chapters 列表非空 → 标题「其他课时」
5. **兜底**：如果没有任何 chapter 也没有任何 episode → 返回空列表。如果有 episode 但没生成任何分组（防御性）→ 返回单个「全部课时」分组。

### 数据结构

```
GroupedChapter {
  title: String          // chapter 标题 或「其他课时」/「全部课时」
  episodes: List<Episode>
  isUngrouped: boolean   // 标记未分组桶(影响 UI 样式)
}
```

### 参考

- PAD 实现：`frontend/lib/service/chapter_grouper.dart` 的 `groupEpisodesByChapter`（纯函数，行 32）

---

## 4. 进度上报防作弊（Progress Report Watchdog）

播放时定期上报观看进度，但要防作弊（不能 seek 一下就把中间时长算进去）+ 应对 CDN 异常。

### 规则

每 **5 秒** 一个 tick，仅在**正在播放**（`playing == true`）时处理：

```
delta = currentPos - lastLoggedPos   # 本次位置 - 上次上报位置(秒)

if !playing:            return       # 不播放不上报
if delta > 0 && delta <= 30:         # 正常前进:上报
    report(episodeId, currentPos, delta)
    lastLoggedPos = currentPos
elif delta < 0:                      # 位置倒退(CDN 重连回零):跳过本 tick,保持基线
    return                           # 不改 lastLoggedPos(下次仍与真实前进位置比)
else:                                # delta == 0(卡住)或 delta > 30(seek 跳跃):重置基线
    lastLoggedPos = currentPos       # 不上报,避免把 seek 算成观看时长
```

### 关键点

- **delta 上限 30 秒**（5s tick 的 6 倍裕量）：应对缓冲/GC/resume 重 seek 导致的偶发延迟，避免误丢合法的 6-10s delta。曾经用 10 秒上限静默丢掉了观看时长，admin 的"学习时长"列卡在 0。
- **负 delta 不重置基线**：CDN 重连把位置重置到 0 时，如果此时把基线锚在 0，后续 seek 回断点会被算成巨大虚假 delta。所以跳过本 tick，基线保持在上次真实前进位置。
- **大 delta（seek）只重置基线不上报**：用户 seek 跳过中间部分，那段不算观看时长。
- **后端兜底**：server-side 每次上报 clamp 到 600s，且只计单调前进。即使前端算错，大跳跃也无法膨胀总量。

### 请求

`POST /api/v1/progress/report`
```json
{
  "episode_id": 123,
  "position_seconds": 456,
  "delta_watch_seconds": 5
}
```

### 参考

- PAD 实现：`frontend/lib/ui/screen/player_screen.dart` 的 `_startProgressTimer`（行 407）

---

## 5. 断点续播（Resume Playback）

进播放器时从上次看的位置继续。

### 规则

1. play-info 返回 `progress.last_position_seconds`。
2. 仅当 `last_position_seconds > 5`（跳过开头几秒的噪音）才 seek 到该位置。
3. **CDN 回零 watchdog**：网盘流重连时 CDN 可能把位置重置到 0。需要监听播放器位置变化，检测异常回零并重 seek 回断点。最多重试若干次（PAD 端是 8 次，1 秒节流），避免无限循环。

### 为什么需要 watchdog

`seekTo(resumeMs)` 在播放器 ready 后调用一次，但网盘 CDN（尤其 115）断连重连会重置到 0，单次 seek 不够。watchdog 反复检测 + 重 seek，直到位置稳定在断点附近。

### 参考

- PAD 实现：`frontend/lib/ui/screen/player_screen.dart` 的 `_setupResumeSeek`（行 361）

---

## 6. 字幕字号档位（Subtitle Size）

播放器字幕字号，用户可调，本地持久化（不上后端）。

### 档位

| index | 标签 | 字号(dp) |
|---|---|---|
| 0 | 小 | 18.0 |
| 1 | 中（默认） | 24.0 |
| 2 | 大 | 30.0 |
| 3 | 超大 | 38.0 |

### 持久化

- key: `ui_subtitle_size_index`（int，存 index 不存原始数值）
- PAD 用 `SharedPreferences`，TV 用 `EncryptedSharedPreferences` 或 `DataStore<Preferences>`。
- 默认 index = 1（中）。

### 参考

- PAD 实现：`frontend/lib/service/ui_prefs.dart` 的 `UiPrefs`（`_subtitleSizes` 行 32）

---

## 7. 网盘直链鉴权头注入

play-info 返回的 `url` 是 AList 网盘代理地址，实际播放时后端 302 重定向到云盘 CDN 直链。某些网盘（尤其 115）的直链需要鉴权头。

### 规则

1. play-info 返回 `headers`（`Map<String, String>`），如 `{Referer: "...", User-Agent: "..."}`。
2. 播放器 HTTP 数据源工厂必须把这些头设为**默认请求头**，对所有请求（含 302 跳转后的 CDN 直链）生效。
3. 关键头：`Referer`（115 网盘鉴权）、`User-Agent`（部分网盘要求）。
4. **不做 HEAD probe**：某些云盘对 HEAD/probe 请求返 403。ExoPlayer 默认不做 probe（对应 media_kit 的 `demuxer-lavf-probe-info=no`）。

### ExoPlayer 实现

`OkHttpDataSource.Factory` 设置 `defaultRequestProperties.headers(headers)`，注入到 `DefaultMediaSourceFactory`。

### 参考

- PAD 实现：`frontend/lib/ui/screen/player_screen.dart` 的 `Media(playInfo.url, httpHeaders: headers)` + `_tuneMpvForNetdisk`（行 206）
