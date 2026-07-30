# 调试指南:播放器 ESC / 返回键分层 —— 已修复

> **现象**:播放页按 ESC,**直接退出播放页**(回课程详情或大厅),而不是按分层规约
> 「先关控件、再按才退出」。mumu 模拟器和真机 TV(TCL m7642 / Android 9)都复现。
>
> ✅ **已修复(2026-07-30,commit `9caad50`)**。根因是 escape 绑了框架 `DismissIntent`,
> 被 `MaterialPageRoute` 的 `_DismissModalAction`(maybePop)抢走 → 退出。详见下方。

---

## TL;DR(给赶时间的人)

1. **根因**:`MaterialPageRoute` 给每个 push 进来的 route 自动注册
   `DismissIntent → _DismissModalAction`(`flutter/.../widgets/routes.dart:1198`),它的
   invoke 是 `Navigator.maybePop()`。player_screen 把 escape 绑到框架 `DismissIntent`,
   焦点不在本屏 `Actions` 子树内时,ESC 命中 ModalRoute 的这个 pop action → 播放页被退出。
2. **为什么系统返回键正常、ESC 出问题**:返回键(`KEY_BACK`)走系统 back 通道
   → `onBackPressed` → `PopScope` → `_handlePop`,**不经过 DismissIntent**;ESC(`KEY_ESC`)
   走 Flutter KeyEvent → `escape→DismissIntent`,被 ModalRoute 抢。两条路径不同。
3. **修复(两层,`9caad50`)**:
   - escape 改绑**自定义 `_ToggleControlsIntent`**(不再用框架 `DismissIntent`),从根上
     杜绝被 ModalRoute 抢。
   - 新增 YouTube/B站 式分层 `_applyEscLayering`(ESC 与系统返回键统一走它):
     全屏+隐藏→**唤出控件**;全屏+可见→**退出全屏**;非全屏→退出页面。
4. **验证**:mumu 确认 ESC 不再退出播放页;手机确认系统返回键分层正常。全屏内部分层
   (唤出/退出全屏)未在真机 TV 验证(TCL 装包被封),下次自然使用时确认。

---

## 根因详解

### 为什么 escape 不能绑 DismissIntent

`MaterialPageRoute`(以及它 push 出来的 route)在 `_ModalScope` 里注册了一个 action:

```dart
// flutter/packages/flutter/lib/src/widgets/routes.dart:1198
actions: <Type, Action<Intent>>{DismissIntent: _DismissModalAction(context)},
```

`_DismissModalAction.invoke` 的实现是 `Navigator.of(context).maybePop()`(`routes.dart:987`)
——**会退出当前 route**。

Flutter 的 `WidgetsApp` 默认还把 `escape → DismissIntent` 注册成全局 shortcut
(`app.dart:1272/1321/1351`)。所以一旦某个 widget 用 `SingleActivator(escape): DismissIntent()`
绑 ESC,就同时暴露在两条 action 路径下:

- 焦点在该 widget 自己的 `Actions` 子树内 → 命中 widget 的 action(预期行为)。
- 焦点不在该子树内(飘到别处)→ `Actions.invoke` 沿焦点路径冒泡,**命中 ModalRoute 的
  `_DismissModalAction`** → `maybePop()` → **页面被退出**。

播放页是 `MaterialPageRoute` push 进来的,且 TV 遥控器操作时焦点可能落在 helper panel、
控件层等不同位置,所以 ESC 表现成「有时退出」(命中 ModalRoute)。

### 为什么系统返回键没这个问题

系统返回键(`KEY_BACK`)走的是 Android 标准 back 通道:
`系统 back → onBackPressed → RootBackButtonDispatcher → Navigator.maybePop → 触发 PopScope
→ onPopInvokedWithResult → _handlePop`。**整条链不碰 DismissIntent**,所以返回键的分层
(`PopScope` + `_handlePop`)一直正常,只有 ESC 出问题。这也是诊断初期的迷惑点:返回键
正常会让人误以为「分层逻辑没问题」,从而漏掉 ESC 走的是另一条 action 链。

---

## 修复(已落地,`9caad50`)

### 1. escape 改绑自定义 Intent

`player_screen.dart` 的 Shortcuts 里,escape 从框架 `DismissIntent` 改为**本屏专属**
`_ToggleControlsIntent`:

```dart
SingleActivator(LogicalKeyboardKey.escape): const _ToggleControlsIntent(),
...
_ToggleControlsIntent: CallbackAction<_ToggleControlsIntent>(
  onInvoke: (_) => _handleDismiss(),
),
```

`_ToggleControlsIntent` 是本文件定义的私有 Intent,ModalRoute 不认它 → 不会被抢。
焦点无论在哪,ESC 要么命中本屏的 `_handleDismiss`,要么无人处理(不退出),绝不会
误命中 ModalRoute 的 pop action。

> 注意:Dialog/菜单路由自己注册的 `DismissIntent`(关菜单)不受影响 —— 菜单在更高层的
> route 上,它的 DismissAction 优先级更高,ESC 关菜单仍正常。

### 2. YouTube/B站 式分层 `_applyEscLayering`

ESC 与系统返回键原本各有一个 handler(`_handleDismiss` / `_handlePop`),现在统一转发给
`_applyEscLayering`,做一套明确的状态机(满足「全屏 ESC 不粗暴退出」的需求):

| 状态 | ESC / 返回键行为 |
|------|------------------|
| 全屏 + 控件隐藏 | **唤出控件**(不退出)—— 全屏沉浸态先给操作控件的机会 |
| 全屏 + 控件可见 | **退出全屏**(回带 AI 侧边栏的非全屏态) |
| 非全屏 + 控件可见 | 关控件 |
| 非全屏 + 控件隐藏 | 退出页面(真正 pop) |

菜单(Dialog)打开时不走这里 —— 系统返回键先 pop Dialog 关菜单,ESC 的 DismissIntent
由 Dialog 路由截获关菜单。

---

## 诊断教训(这次踩的坑,别重蹈)

这次定位花了很多时间,根因不在代码本身(就一行 Intent 绑定),而在**诊断被环境层层误导**。
教训:

1. **mumu 模拟器不可靠,按键类问题必须上真机**。mumu 上:`adb input keyevent` 注入的键
   和真实按键**走不同路径**(adb ESC 进 Flutter KeyEvent,真实 ESC 不进,直接 pop);
   logcat 丢 Dart 日志(连 `print` 都不显示);文件日志写不进(scoped storage)。三个观测
   手段全失效,导致「按真实 ESC 退出 / adb 测不退出」的矛盾久拖不决。真机(手机/TV)上
   logcat 正常,同一套日志一抓就有。
2. **TCL 真机 TV 调试被系统封死**:`pm install` 禁(`install_switch_flag:0`,改 prop/
   settings/session 都绕不过)、`getevent` 抓不到、input logcat 没有。要在 TV 上拿代码
   日志只能侧载 debug 包(U盘/当贝市场)。
3. **发现矛盾要立刻怀疑测试方法,而不是反复确认**。这次「你说退出、我测不退出」的矛盾
   是铁证,本应第一时间怀疑 adb 注入 ≠ 真实按键,却反复手测。同理,「手机返回键正常」
   不等于「ESC 正常」—— 手机没 ESC 键,只测了 KEY_BACK 路径,曾因此误判成「mumu 特有、
   非 app bug」,后来真机 TV 复现才推翻。
4. **Flutter 默认 shortcut + route 默认 action 是隐藏陷阱**。`escape→DismissIntent`(全局
   shortcut)+ `DismissIntent→maybePop`(ModalRoute action)是 framework 默认行为,widget
   用 DismissIntent 绑 ESC 就和它们撞车。自定义 Intent 是更稳的写法。

---

## 相关文件

- `frontend/lib/ui/screen/player_screen.dart`:修复都在这。`_ToggleControlsIntent`(自定义
  ESC Intent)、`_applyEscLayering`(统一分层状态机)、Shortcuts 的 escape 绑定、
  `PopScope(canPop:false) + onPopInvokedWithResult→_handlePop`(系统返回键路径,未改)。
- `scripts/test_helper.js`:自动化测试框架(读屏/点击/断言),通用。**不要用它测按键类
  回归** —— `adb input keyevent` 在 mumu 上和真实按键走不同路径(见教训1)。
