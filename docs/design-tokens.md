# 跨端视觉设计 Token

> **目的**：StudyQuest 的 PAD（Flutter）和 TV（Kotlin Compose for TV）两端语言不通，但视觉风格要统一品牌识别度。
> 本文档定义色板、字体、圆角、焦点视觉等 token，两端对照实现。
>
> **核心原则**：品牌色（蓝/橙/绿、渐变、字体）两端一致；**底色不同**——PAD 浅色主题，TV 深色主题（业界 TV APP 惯例：客厅不刺眼、OLED 防烧、远距离可读）。

---

## 色板

### 品牌主色（两端一致）

| Token | HEX | 用途 |
|---|---|---|
| `primaryColor` | `#3B82F6`（Blue-500） | 主色：按钮、链接、焦点高亮、进度条 active |
| `accentGreen` | `#10B981`（Emerald-500） | 次强调：成功、完成态、成长足迹积分 |
| `accentOrange` | `#F97316`（Orange-500） | 强调：徽章、等级、警告 |

### Slate 中性灰阶（两端共用 ramp，底色取向不同）

| Token | HEX | PAD 用途 | TV 用途 |
|---|---|---|---|
| `slate50` | `#F8FAFC` | **背景**（PAD 浅色底） | — |
| `slate100` | `#F1F5F9` | 次背景 | — |
| `slate200` | `#E2E8F0` | 边框 borderMuted | — |
| `slate300` | `#CBD5E1` | — | TV 次要边框 |
| `slate400` | `#94A3B8` | — | TV 辅助文字 |
| `slate500` | `#64748B` | 静音文字 textMuted | TV 正文文字 |
| `slate600` | `#475569` | — | TV 主文字 |
| `slate800` | `#1E293B` | 主文字 textWhite | — |
| `slate900` | `#0F172A` | — | **背景**（TV 深色底） |

### 品牌延伸色

| Token | HEX | 用途 |
|---|---|---|
| `indigo500` | `#6366F1` | 渐变终点、次品牌色 |
| `violet500` | `#8B5CF6` | 头像光环、装饰 |
| `blue100` | `#EFF6FF` | 浅蓝背景（PAD 卡片高亮） |
| `blue600` | `#2563EB` | 深蓝（按下态） |

### 语义/状态色

| Token | HEX | 用途 |
|---|---|---|
| `emerald100` | `#ECFDF5` | 成功浅底 |
| `amber50` | `#FFFBEB` | 警告浅底 |
| `orange400` | `#FB923C` | 渐变中间色 |
| `yellow400` | `#FACC15` | 等级徽章渐变 |

---

## 渐变（两端方向一致：topLeft → bottomRight）

| Token | 色值 | 用途 |
|---|---|---|
| `brandGradient` | `#3B82F6` → `#6366F1` | 品牌主渐变：主 CTA、header |
| `levelBadgeGradient` | `#FB923C` → `#FACC15` | 等级/XP 徽章 |
| `avatarRingGradient` | `#60A5FA` → `#C084FC` | 头像光环 |
| `blueGradient` | `#60A5FA` → `#3B82F6` | 学科卡片（蓝系） |
| `indigoGradient` | `#818CF8` → `#6366F1` | 学科卡片（靛系） |
| `skyGradient` | `#38BDF8` → `#0EA5E9` | 学科卡片（天蓝系） |
| `emeraldGradient` | `#34D399` → `#10B981` | 学科卡片（翠系） |

### 学科卡片渐变动态度生成

学科卡片背景色由后端配置的 subject hex 决定（非硬编码 per-name）。给定一个 hex（如 `#f59e0b`），生成对角渐变：
- 起点 = hex 色 + 0.55 alpha 叠白（变浅）
- 终点 = hex 原色
- 方向 topLeft → bottomRight
- hex 解析失败时 fallback 到 `primaryColor` `#3B82F6`

参考 PAD `getSubjectGradientFromColor` / `colorFromHex`（`frontend/lib/theme.dart` 行 89/116）。

---

## 字体

| 属性 | 值 | 说明 |
|---|---|---|
| 字族 | **Quicksand** | 两端一致。PAD 用 `google_fonts` 包；TV 用本地 `res/font/` 或 Downloadable Fonts |
| 主标题 | bold 32sp | displayLarge |
| 标题 | semibold(600) 20sp | titleLarge |
| 正文 | medium(500) 18sp | bodyLarge |
| 辅助 | medium(500) 16sp | bodyMedium |

> TV 端字号建议在以上基础上 ×1.1~1.2（远距离可读），但 token 基准一致。

---

## 圆角与描边

| Token | 值 | 用途 |
|---|---|---|
| `borderRadiusValue` | `20.0` dp | 卡片、按钮、输入框圆角 |
| `borderWidthValue` | `3.0` dp | 焦点态边框宽度 |
| 普通边框宽度 | `2.0` dp | 非焦点态 |

---

## 焦点视觉（D-pad / 键盘聚焦态）

这是 TV 端的核心交互视觉（PAD 触屏用得少，但 TV 遥控器全靠焦点导航）。

### PAD 版（浅底适配）

聚焦态 BoxDecoration：
- 背景色：卡片原色（白）
- 边框：`primaryColor` `#3B82F6`，宽 `borderWidthValue` 3.0
- 阴影（发光环）：`primaryColor` + **alpha 0.15**，blurRadius 16，offset (0,0)

非聚焦态：
- 边框：`borderMuted` `#E2E8F0`，宽 2.0
- 阴影：`slate900` `#0F172A` + alpha 0.03，blurRadius 20，offset (0,4)

### TV 版（深底适配，需更亮更醒目）

聚焦态（**比 PAD 更亮**，远距离可辨识）：
- 边框：`primaryColor` `#3B82F6`，宽 `borderWidthValue` 3.0（同 PAD）
- 发光环：`primaryColor` + **alpha 0.35**（PAD 的 2 倍多），blurRadius 24（更大柔光），offset (0,0)
- 聚焦背景微提亮：原底色 + `primaryColor` alpha 0.12 叠加

非聚焦态（TV 深底）：
- 边框：`slate700`（`#334155`，PAD 没用，TV 深底中间灰）宽 1.0（更细，弱化非焦点）
- 无阴影（深底加阴影看不见）

### 焦点缩放（可选，TV 增强辨识）

聚焦时 scale 1.0 → 1.05（轻微放大），配合发光环。参考腾讯/网易 TV 的焦点放大效果。

---

## TV 播放器控制层视觉（参考腾讯视频 TV / 网易爆米花 TV）

控制行图标按钮聚焦态：
- 圆形背景：`primaryColor` + alpha 0.2
- 图标：白色 28sp
- 聚焦发光环：`primaryColor` alpha 0.4，blurRadius 16（控制行按钮密集，发光环比卡片小）

seek bar 聚焦态（识别焦点位置）：
- track 高度 4 → 6
- thumb 半径 7 → 10
- overlay 半径 14 → 18
- 时间文字加蓝色 Shadow（`primaryColor` blurRadius 6）

---

## PAD 暗色模式（新增 2026-07）

PAD 端原先只有浅色主题（与 TV 深色分工）。本次新增用户可选的暗色模式，三态切换：浅色 / 深色 / 跟随系统（设置页 → 主题外观）。

### 三端深色分工

| 端 | 浅色 | 深色 | 说明 |
|---|---|---|---|
| PAD（Flutter） | 默认 | 用户可选 | 通过 `ThemeExtension`（`AppColors` light/dark）+ `MaterialApp.themeMode` 实现 |
| TV（Kotlin） | — | 固定 | TV 客厅场景固定深色，无切换 |

### PAD 暗色 token 取值

与 TV 深色分工对齐（同用 slate ramp，底色取向一致）。语义 token 名不变（`textWhite`/`textMuted`/`borderMuted` 等），亮暗取值不同：

| Token | 亮色（默认） | 暗色 | 说明 |
|---|---|---|---|
| `backgroundColor` | `slate50` `#F8FAFC` | `slate900` `#0F172A` | 页面背景 |
| `cardColor` | `#FFFFFF` | `slate800` `#1E293B` | 卡片/容器底 |
| `textWhite`（主文字） | `slate800` `#1E293B` | `slate100` `#F1F5F9` | 深底取浅、浅底取深 |
| `textMuted`（静音文字） | `slate500` `#64748B` | `slate400` `#94A3B8` | |
| `borderMuted`（边框） | `slate200` `#E2E8F0` | `slate700` `#334155` | |
| `primaryColor`/`accentGreen`/`accentOrange` | 两端一致 | 两端一致 | 品牌色不随明暗变 |
| 渐变（brand/levelBadge/学科渐变） | 两端一致 | 两端一致 | 品牌渐变跨端共享 |

### 暗色实现要点

- **不随主题切换的固定色**：状态语义色块（正确绿 `#ECFDF5`、错误红 `#FEF2F2`、选中紫 `#F5F3FF`、警告琥珀 `#FFFBEB`）——亮暗通用，保留浅色块以保证状态辨识；代码块/表格装饰色（slate100 底）同理保留浅色块。
- **视频播放器**：黑底是固有需求（视频本身黑底），亮暗模式下播放器 overlay 控件保持白图标/深底，不跟随主题。
- **context 感知取色**：业务代码用 `context.colors.xxx`（`AppColorsX` 扩展，从 `Theme.of(context).extension<AppColors>()` 读取）；`AppTheme.xxx` 静态常量保留为浅色默认值，供 const 上下文（渐变、工厂构造默认值）使用。

---

## 参考实现

- **PAD（Flutter）**：
  - `frontend/lib/theme/app_colors.dart` —— `AppColors`（ThemeExtension，亮/暗两套语义 token）+ `AppColorsX`（context.colors 扩展）。
  - `frontend/lib/theme.dart` —— `AppTheme` 门面：`lightTheme`/`darkTheme` ThemeData 构建、渐变/圆角/`switchDecoration`/`getSubjectGradientFromColor` 等亮度无关常量与 helper。
  - `frontend/lib/service/theme_prefs.dart` —— `ThemePrefs`（ChangeNotifier，三态持久化 + `themeMode`）。
- **TV（Kotlin）**：`tv-android/app/src/main/java/app/studyquest/tv/ui/theme/`（阶段 1 Agent B 实现 `StudyQuestTheme` + `Color.kt` + `Type.kt` + `Shape.kt`）。

两端 token 改动 → 改本文档 → 两端同步。
