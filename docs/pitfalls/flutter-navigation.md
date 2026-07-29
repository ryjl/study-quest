# Flutter 导航 / 路由 / TV 焦点踩坑

> 本文件只收**真实踩过的坑**(每条都有代码注释背书,不是泛泛的 Flutter 知识)。
> 配套的泛用 pitfalls 见 `docs/pitfalls/frontend.md`。
>
> 代码位置基于 `frontend/lib/`,行号会随重构漂移 —— 找最新实现以函数名/类名为准。

---

## 0. 架构前提:全命令式 push,无命名路由表

StudyQuest 的 Flutter 端**不用** `Navigator.pushNamed` / `routes:` / `onGenerateRoute`(`grep pushNamed` 全项目 0 结果)。所有跳转都是命令式 `Navigator.push(context, MaterialPageRoute(...))`,登录态守卫写在 `MaterialApp.home`:

```dart
// main.dart:72
home: auth.isAuthenticated ? const MainNavigation() : const LoginScreen(),
```

`AuthService` 是 `ChangeNotifierProvider`,登录态一变 → `MaterialApp.home` 整个重建,在 `LoginScreen` / `MainNavigation` 间切换。页面间跳转(登录成功、登出)用 `pushReplacement` 换栈顶。

**含义**:这意味着没有深链、没有 web URL、没有嵌套 `Navigator`。加新屏 = `push(context, MaterialPageRoute)` 一行。**好处是简单可预测,代价是栈管理全靠人工**(见坑 9 的无限栈)。

---

## 1. TextField D-pad 焦点陷阱(被 4 个屏反复踩)

**最重灾区**。搜索框 / IP 输入框一旦获得焦点,方向键全被吃掉,D-pad 永远跳不到旁边的「筛选」「保存」按钮。

### 根因

TextField(底层 `EditableText`)**自己消费方向键**做光标移动和多行导航,事件**不会冒泡到祖先 Focus widget**。所以用外层 `Focus` widget 包裹是拦不住的 —— TextField 先消费了。

### 解法

抽了一个公共 helper `dpadEscapeFocusNode()`(`ui/widget/tv_focus.dart:17-51`):给 TextField **自己的** `focusNode` 装一个 `onKeyEvent`,在方向键到达 `EditableText` **之前**返回 `handled` 并手动 `nextFocus` / `previousFocus`;字母数字 / 回车放行。

```dart
// 关键:这个 onKeyEvent 跑在 EditableText 的默认按键处理之前
// 必须用「TextField 自己的 FocusNode」才拦得住,外层 Focus 包裹没用
TextField(focusNode: dpadEscapeFocusNode(), ...)
```

### 踩坑位置(每屏都重述了根因,各自 dispose)

| 屏 | 位置 | 备注 |
|---|---|---|
| 课程大厅搜索框 | `course_list_screen.dart:51-59, 284-299` | 注释最详细("【Android TV 焦点陷阱修复】") |
| 阅读室 | `reading_room_screen.dart:27-29` | |
| 设置页 IP 输入框 | `settings_screen.dart:20-22, 44-47` | 为此 SettingsScreen **必须从 StatelessWidget 改成 StatefulWidget**(节点要 dispose) |
| 登录页 dialog | `login_screen.dart:44-47, 140` | dialog 场景用 `.whenComplete(ipFocusNode.dispose)` 兜底释放 |

---

## 2. FutureBuilder 引用不稳 → 输入框焦点 / 输入被打断

`course_list_screen.dart:41-45, 100-103`。

### 现象

在搜索框打字时,每次 `setState` 触发,`FutureBuilder` 重新订阅,整个子树重建,TextField **焦点丢失、输入中断**。

### 根因

把 `Future.wait([...])` 写在 `build` 里,每次 build 都新建一个 Future → FutureBuilder 认为是"新 future" → 重订阅 → `ConnectionState.waiting` → 子树重建。

### 解法

缓存成 `late final _combinedFuture`,只在 `_loadData()` 里重建一次:

```dart
late final Future _combinedFuture;   // 顶层字段
@override
void initState() { super.initState(); _combinedFuture = _loadAll(); }
// build 里:FutureBuilder(future: _combinedFuture, ...)
```

---

## 3. `MenuAnchor` 焦点归位丢失 → 改用 `showDialog`

`ui/widget/player_menu.dart:6-19`(2026-07-27 重构记录)。

### 现象

播放器设置菜单(速度 / 字幕 / 音轨)用 `MenuAnchor` 时:菜单关闭后焦点丢失到 `ModalScope`、ESC 后焦点消失再也点不回来、系统返回键直接退出页面。修了多轮没根治。

### 根因

`MenuAnchor` 的菜单 overlay 在 `Navigator` 顶层渲染,焦点归位靠 `MenuController` 状态机 + `FocusManager` 全局监听,**跟手动焦点管理冲突严重**。

### 解法

改用 `showDialog` —— Flutter 最成熟的焦点隔离机制:自动 `FocusScope`、自动 ESC 关闭、自动系统返回键关闭、`await` 线性流程 + 显式 `requestFocus` 归位。归位处还做防御性检查(`player_menu.dart:83-85`):

```dart
// 防御性检查(node 可能已被 dispose)
if (mounted && _triggerFocus.canRequestFocus) { _triggerFocus.requestFocus(); }
```

---

## 4. 控件 auto-hide 卸载 FocusNode → TV 焦点丢失

`player_screen.dart:580-582, 638-667, 962-966`。

### 现象

TV 模式下播放器控件(seek bar + 控制行)自动隐藏后,焦点凭空消失,D-pad 失灵。

### 根因

`Focus` 节点随控件子树一起被 `if (_controlsVisible)` **整树卸载**,不在焦点树里。隐藏态按方向键,没人接键。

### 解法(双管齐下)

1. **TV 下禁用 auto-hide**(`player_screen.dart:962-966`):auto-hide 是 PAD/触屏的传统交互,TV 用户没这个概念。TV 下控件常驻可见,seek bar 的 FocusNode 永远在焦点树里,几何算法总能找到。
2. **顶层 Focus 唤出兜底**(`player_screen.dart:638-667` `_onWakeControls`):万一控件隐藏了,顶层 `Focus(autofocus: true, onKeyEvent: _onWakeControls)` 是唯一能可靠在隐藏态接键的位置,任意方向键 / 激活键唤出控件 + 吞键(避免唤出那次按键同时触发 seek)。

---

## 5. 不要自己重写方向键分发 —— 别覆盖 framework 的 2D 几何导航

`player_screen.dart:41-46, 550-555`。

### 根因教训

Flutter framework 已经把 ◄▲▼► 默认绑到 `DirectionalFocusIntent` → `FocusNode.focusInDirection` → **2D 几何算法自动找空间最近的可聚焦节点**(由 MaterialApp 顶层默认 `Shortcuts` 提供)。

应用层再给方向键绑 `Shortcuts` 就是**覆盖**这个默认行为,破坏几何导航。

### 正确做法

- 方向键:**不绑** Shortcuts,留给 framework 的 2D 几何算法。
- 激活键(Enter / Select / 遥控器 OK):绑 `ActivateIntent` → 播放/暂停。
- 只有 "seek ±30s" 这种**非遍历语义**才手写 `onKeyEvent`,且只针对 seek bar **一个节点**(见 `player_screen.dart:550-555` Shortcuts 定义处注释:"不绑方向键!方向键留给 MaterialApp 顶层默认绑定的 DirectionalFocusIntent")。

### 反面案例

旧代码 `_onRemoteKey` 拦全部方向键转线性 `nextFocus`(`player_screen.dart:142-147`),破坏了几何导航,已废弃。

---

## 6. 几何算法跨 traversal scope 跳错 → 用 FocusScope 隔离

`player_screen.dart:142-147` + `ui/widget/helper_panel.dart:59-69`。

### 现象

D-pad 在 helper panel(AI 学习侧栏)跳到边界时,焦点"跳走丢失",跑到背后视频区的控制按钮。

### 根因

两个区在**同一个 traversal scope**,几何算法在选候选时不分边界,跨区选了空间最近的节点。

### 解法(helper_panel.dart:59-69,两个改动)

1. `FocusScope`(不是 `FocusTraversalGroup`)建立**独立 FocusScopeNode**,`directionalTraversalEdgeBehavior` 默认 `stop`,panel 内 ▲▼ 到边界停住不溢出。
2. `ClampingScrollPhysics`(不是 `BouncingScrollPhysics`):iOS 弹性滚动会**吃掉** D-pad 垂直键去滚动,而不是在 FocusButton 间跳。ClampingScrollPhysics 让方向键优先给焦点遍历。

---

## 7. TV 遥控器"返回"键键值不一致

`player_screen.dart:561-567`。

### 现象

某些 Android TV 遥控器在菜单里按"返回"键,菜单不关、直接退出整个页面。

### 根因

`MenuAnchor` 源码(`menu_anchor.dart:71-79`)**只绑了 `escape`**。但真机返回键发的是 `browserBack` / `goBack`,不是 `escape`。

### 解法

把三种退出键值(`escape` / `browserBack` / `goBack`)都显式绑 `DismissIntent`。并做**分层 Dismiss**(`player_screen.dart:669-679` `_handleDismiss`):有菜单先收菜单、有控件先收控件、否则才 `Navigator.pop`。修复了"按 ESC 直接退出整个播放页"的旧 bug(旧代码在根层 `_onRemoteKey` 无条件 `Navigator.pop`)。

---

## 8. 异步 await 后用 context —— 必须 `mounted` 守卫

全项目最普遍的坑。`await` 期间 widget 可能已被卸载,再用 context / setState 报错。

### 两种守卫(场景不同)

| 守卫 | 用在哪 | 示例 |
|---|---|---|
| `State.mounted`(`if (mounted)`) | `State` 方法里 | `course_list_screen.dart:107/111/116`(`.then((list) { if (mounted) setState(...) })`)、`main_navigation.dart:117-125`(`_onLogout`) |
| `context.mounted` | **没有 State 的闭包**(典型:`onPressed: () async {...}`) | `settings_screen.dart:223,229`(await 文件 IO 后 `if (context.mounted) ScaffoldMessenger.of(context)...`) |

### 典型用例:await Navigator 返回后取值

```dart
// player_screen.dart:1504-1517 _enterAiStudy
final result = await Navigator.of(context).push(...);
if (result is JumpRequest && mounted) {   // ← mounted 守卫
  _seekTo(result.target);
}
```

```dart
// login_screen.dart:158-169 _onSubmitPin
final success = await authService.login(...);
if (success) {
  if (!mounted) return;                    // ← 守卫
  Navigator.pushReplacement(context, ...);
}
```

---

## 9. AI 页 ↔ 播放器互跳成无限栈

`player_screen.dart:55-58`(`disableAiTab` 参数文档)。

### 现象

AI 学习页有"跳转视频 12:38"按钮 push 播放器,播放器又有 AI 入口,反复互跳 → 栈无限增长。

### 根因

命令式 push 没有环路检测。

### 解法

从 AI 页 push 进来的播放器传 `disableAiTab: true`,UI 层**不渲染**回跳入口(helper panel 的 AI 学习入口 + 顶栏 AI 图标都不渲染),从根上断环。配合 `JumpRequest` 作为 `pop` 返回值(`player_screen.dart:1514`),让 AI 页接住后定位 seek。

---

## 10. TV 路由裁剪靠过滤 enum(路由表即功能定义)

`service/app_features.dart:1-48`。

### 模式

单 APK 同时跑 PAD 和 TV,运行时按 `TvMode` 裁剪功能域:

```dart
// app_features.dart:45-48
List<AppFeature> visibleFeaturesFor({bool? tv}) {
  final isTv = tv ?? TvMode.instance.isActive;
  return AppFeature.values.where((f) => !isTv || f.supportsTv).toList();
}
```

错题本(`wrongBook`)标 `supportsTv: false`(`app_features.dart:27-28`),TV 模式下整 tab 不出现。

### 教训

**路由表即功能定义**。加新功能 = 加一个 enum 值 + 决定它的 `supportsTv`,导航表 / 路由自动跟随。**不要**在每屏里散写 `if (TvMode...)`,否则导航 tab 和实际可用功能会不一致。`main_navigation.dart:563-578, 699-706` 的 tab 列表和屏分发都走 `visibleFeaturesFor()`,自动跟随裁剪。

---

## 11. 响应式布局不能用 600dp 断点

`ui/responsive.dart:5-18`。

### 根因

目标设备是真实平板。平板**竖屏**宽度(~768–900dp)会**超过** Material 的 600dp compact 断点。如果用 `width < 600` 做手机布局切换,**平板竖屏永远切不过去**。

### 解法

用**屏幕方向**(`width < height`)而非像素断点,直接捕捉用户"旋转一下要竖排布局"的意图:

```dart
bool isPortrait(BuildContext context) {
  final size = MediaQuery.sizeOf(context);
  return size.width < size.height;
}
```

导航差异:横屏 = 侧边栏多列(`main_navigation.dart:163-180`),竖屏 = 底部导航栏堆叠(`main_navigation.dart:141-160`)。

---

## 12. FocusButton 节点生命周期:外部传入的不 dispose

`ui/widget/focus_button.dart:38-50`。

### 规则

```dart
// initState
_focusNode = widget.focusNode ?? FocusNode();
// dispose
if (widget.focusNode == null) _focusNode.dispose();   // 只 dispose 自己创建的
```

外部传入的节点归调用方管理。这是 `player_menu.dart:56-64` 能把 `_triggerFocus` 传进 `FocusButton` 又自己 dispose 的前提。违反这个规则 → 双重 dispose 崩溃,或节点泄漏。

---

## 13. TvFocus 不能省 GestureDetector

`ui/widget/tv_focus.dart:110-116`。

### 坑

给一个已有点击的卡片加 D-pad 支持时,容易只加 `Focus` 包裹忘了 `GestureDetector`,导致**触屏点击失效**(只有键盘 / D-pad 能用)。

### 解法

`TvFocus` = `Focus` + `GestureDetector` 双层。GestureDetector 不能省,否则触屏路径失效。

---

## 14. PIN 蒙层焦点引导

`login_screen.dart:262-273` + `ui/widget/num_pad.dart:122-131`。

### 现象

登录页选用户后弹 PIN 蒙层,D-pad 焦点会飘到背后模糊的用户卡上。

### 解法

蒙层打开用 `FocusScope(autofocus: true)` 把焦点**引进 NumPad**,NumPad 内「1」键再 `autofocus`,形成明确的焦点落点链。

---

## 快速参考:FocusTraversalGroup vs FocusScope

这两个容易混,项目里踩过坑(坑 6):

| | `FocusTraversalGroup` | `FocusScope` |
|---|---|---|
| 作用 | 共享一条 reading-order 遍历链(Tab / D-pad 顺序) | **独立焦点域**(FocusScopeNode) |
| 边界行为 | 方向键到边界仍会溢出到同 scope 的其它节点 | `directionalTraversalEdgeBehavior` 默认 `stop`,边界停住 |
| 适用 | 整屏 / 整区想让 D-pad 在按钮间自然移动 | 两区**互不想让焦点串**(如视频区 vs 侧栏 panel) |

**记忆点**:想让 D-pad 在一组按钮里来回跳 → `FocusTraversalGroup`;想**隔离**两个区互不串焦点 → `FocusScope`(helper_panel 的用法)。

---

## 快速参考:哪些坑是 TV 专属 vs 通用

| 坑 | TV 专属 | 通用(PAD 也中招) |
|---|---|---|
| 1 TextField 焦点陷阱 | ✓(PAD 触屏点一下就出来) | |
| 2 FutureBuilder 引用不稳 | | ✓ |
| 3 MenuAnchor 焦点归位 | ✓ | |
| 4 控件 auto-hide 卸载 FocusNode | ✓ | |
| 5 覆盖 framework 几何导航 | ✓ | |
| 6 几何算法跨 scope 跳错 | ✓ | |
| 7 返回键键值不一致 | ✓ | |
| 8 await 后用 context | | ✓ |
| 9 互跳无限栈 | | ✓ |
| 10 TV 路由裁剪 | ✓(PAD 不裁剪) | |
| 11 响应式断点 | | ✓ |
| 12 FocusButton 节点生命周期 | | ✓ |
| 13 TvFocus 不能省 GestureDetector | ✓(触屏 + D-pad 双模式) | |
| 14 PIN 蒙层焦点引导 | ✓ | |

> 后续若回归 Flutter 单端(放弃 TV 原生 Kotlin),坑 1/3/4/5/6/7/10/13/14 这些 TV 焦点相关的会重新全部生效 —— 这份文档就是那时的参考。
