# 交接：全 App 竖屏 + 横屏支持

## 背景

当前 App 在 pad 上表现为横屏 only，但文章阅读页（公众号 H5）强制竖屏。
用户希望整个 App 都支持竖屏 + 横屏自由切换，以适配公众号等为手机竖屏设计的内容。

## ⚠️ 关键发现：代码库里没有横屏锁

当前 App 横屏 only 的原因**不在 repo 里**：

| 位置 | 是否锁定方向 |
|---|---|
| `android/app/src/main/AndroidManifest.xml` | ❌ 无 `screenOrientation` 属性 |
| `lib/main.dart` | ❌ 无 `SystemChrome` 调用 |
| `MainActivity.kt` | ❌ 无 `setRequestedOrientation` |
| `ios/Runner/Info.plist` | ✅ 已允许全部方向（含 iPad） |

**唯一的方向锁**是 `article_reader_screen.dart` 里的竖屏锁（initState L73-76 + dispose L114-121）。

→ 横屏 only 的原因可能是：pad 的系统旋转锁定设置、或之前某次 `setPreferredOrientations` 的残留效果。
→ 第一步应该确认：在真机上去掉文章页竖屏锁后，App 是否已经能自由旋转。

## 需要改动的文件

### 1. `article_reader_screen.dart` — 移除竖屏锁
- **initState L73-76**: 删除 `SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp])`
- **dispose L114-121**: 删除恢复 4 方向的 `setPreferredOrientations`
- **import L2**: 删除 `import 'package:flutter/services.dart'`（如无其他引用）
- 删除后文章页跟随系统方向，pad 竖屏时自然竖屏显示

### 2. `main_navigation.dart` — 侧边栏适配竖屏（最大工作量）
- **L140**: 侧边栏硬编码 `width: 280`。竖屏 768px 宽 pad 上，内容区只剩 488px
- **L122-134**: body 是 `Row[_buildSidebar(280px), Expanded(content)]`，无 LayoutBuilder
- **竖屏方案**（需设计决策）：
  - A. 竖屏时侧边栏改为可折叠 Drawer（底部导航或汉堡菜单）
  - B. 竖屏时侧边栏缩窄到 ~72px（只显示图标）
  - C. 竖屏时改为底部 Tab Bar（最接近手机体验）
- 需要 `LayoutBuilder` 包裹，宽度 < 600 时切换布局
- **L1096**: settings 卡片 `BoxConstraints(maxWidth: 800)` — 竖屏不会触发但留意

### 3. `course_list_screen.dart` — 网格列数适配
- **L353-356**: `width > 1200 ? 4 : (width > 800 ? 3 : 2)`
- 竖屏 pad 内容区 ~488px → 永远 2 列，可接受但可能偏空
- 可加一个 `> 400 ? 2 : 1` 的低阈值给手机竖屏
- **L365**: `childAspectRatio: 0.86` — 竖屏 2 列时卡片可能偏高，需视觉验证

### 4. `player_screen.dart` — 视频播放器（建议保持横屏）
- **无方向锁**（不调 SystemChrome），但布局是硬编码横屏分屏：
  - **L544**: `Row[Expanded(flex:7, video), _buildHelperPanel(width:360)]`
  - **L1122**: 辅助面板固定 `width: 360`
- 建议：视频播放器**强制横屏**（视频是宽屏的），进入时锁横屏，退出时恢复
- 在 `initState` 加 `SystemChrome.setPreferredOrientations([landscapeLeft, landscapeRight])`
- 在 `dispose` 恢复全方向

### 5. `reading_room_screen.dart` / `reading_series_detail_screen.dart`
- 书架视图用 `Wrap`（不是固定列数 Grid），自适应宽度，竖屏无需改动
- 系列详情的 SliverAppBar `expandedHeight: 220` 在竖屏可能偏高，留意

### 6. `pdf_reader_screen.dart`
- PDF 阅读页全屏无方向假设，竖屏横屏都能用（PDF 页面自己处理宽高比）
- 无需改动

## 不需要改动的文件
- `main.dart` — 无方向锁
- `AndroidManifest.xml` — 无 `screenOrientation`
- `Info.plist` — 已支持全方向
- `login_screen.dart` — 布局居中，方向无关

## 建议实施顺序
1. 真机验证：去掉 `article_reader_screen.dart` 竖屏锁后 App 是否已能旋转
2. 如果不能 → 查 pad 系统旋转锁 / APK 打包配置
3. 如果能 → 做 `main_navigation.dart` 竖屏布局适配（核心工作）
4. `course_list_screen.dart` 网格阈值微调
5. `player_screen.dart` 加横屏锁（视频应该横屏）

## 分支
当前 main 已包含阅读室全部代码（commit 440e4f4）。
建议新 session 从 main 切 `feat/portrait-support` 分支。
