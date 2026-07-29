# 调试指南:Flutter 播放器菜单确定后焦点丢失

> **现象**(用户反馈):TV 模式下,播放器的字幕 / 音轨 / 速度菜单,**选中某项确认后**,
> 焦点找不到 —— D-pad 按键无响应或飘到不确定的位置,要重新唤出控件才能恢复。
>
> **ESC 关闭**菜单焦点正常,只有**「确认选中」**这条路径有问题。
>
> 本文件是踩了**两轮坑**才彻底修好的完整记录。第一轮(2026-07 初)定位到 onSelected
> 时序问题,用方案 B 修好了"选中后焦点归位";但暴露出第二轮(2026-07-29)**更深层的
> 几何导航 bug**,才是"归位后按方向键丢焦"的真正根因。**如果你只看一节,看「真正的根因
> (第二轮)」**。

---

## TL;DR(给赶时间的人)

这个 bug 有**两层**,必须都修:

1. **第一层(已修,方案 B)**:`_openMenu` 不 await `onSelected`,导致 setState 重建
   把归位焦点冲掉。修复:`await widget.onSelected(result)` + 归位改用 `postFrameCallback`。
   见 `frontend/lib/ui/widget/player_menu.dart`。

2. **第二层(真正的终态修复)**:播放器根层有个**全屏 `Focus(autofocus: true)` 节点**
   (`_onWakeControls` 用),它默认 `canRequestFocus: true`,**参与了几何导航**。它覆盖全屏
   `(0,0)~(w,h)`,在 D-pad 方向键的几何算法里被当作"正对方向的大邻居"选中,把焦点从控制行
   按钮吸走(视觉上 = 丢焦)。修复:`canRequestFocus: !_controlsVisible` —— 控件可见时不参与
   遍历,隐藏时才可聚焦接键。见 `frontend/lib/ui/screen/player_screen.dart:587`。

**为什么只有「确认」触发、ESC 不触发**:ESC 走 Flutter 内置 DismissIntent 焦点恢复,焦点
状态干净;「确认」走 `await onSelected`(含 setState 重建),重建扰动了几何导航的候选优先级,
这时全屏节点才被选中。是**全屏节点参与导航** + **onSelected 重建扰动**两个因素叠加。

---

## 真正的根因(第二轮,2026-07-29)

### 现象(比第一轮更精确)

方案 B 修好"选中归位"后,暴露出新现象:

- 打开字幕菜单 → **切换某项 + 确定选中** → 按 D-pad 方向键(如 ◄)→ **焦点消失**
- 打开字幕菜单 → **ESC 关闭** → 按方向键 → **焦点正常**

归位本身成功(`_triggerFocus.hasFocus=true / hasPrimaryFocus=true`),但随后按方向键,焦点
不是移到相邻按钮,而是跑到了一个 **`(0,0)`、覆盖全屏、debugLabel 为 null 的匿名 Focus 节点**上。
视觉上就是"没焦点",D-pad 再按无响应。

### 这个 (0,0) 全屏节点是什么

`frontend/lib/ui/screen/player_screen.dart` 的播放器根层(约 578 行):

```dart
child: Focus(
  autofocus: true,                 // ← 启动时接管键入口
  onKeyEvent: _onWakeControls,     // ← 控件隐藏态接方向键唤出控件
  child: FocusTraversalGroup(
    child: Stack(... 整个播放器内容 ...),  // ← 这个 Focus 覆盖全屏
  ),
)
```

它的作用是:控件隐藏态(控制行子树卸载、没人接键)时,靠这个 Focus 节点**自己持有焦点** +
`onKeyEvent` 接方向键,唤出控件(`_onWakeControls` 只在 TV + 隐藏态拦键)。

问题:`Focus` 默认 `canRequestFocus: true`,这个全屏节点**参与了几何导航**。当焦点在控制行
按钮(如字幕按钮 `(584,571)`)上按 ◄ 时,framework 的几何算法找左邻居,候选要满足
`x < 586`。倍速按钮 `(524,571)` 和全屏节点 `(0~1138, 0~640)` 都满足,但全屏节点的 y 区间
完全覆盖字幕按钮的 y,**几何评分上全屏节点是更"正对"的邻居**,于是被选中。

### 验证方法(真机日志)

在 `_PlayerSettingsMenuState` 里加全局焦点监听,追踪每次 `primaryFocus` 变化(带屏幕坐标):

```dart
FocusManager.instance.addListener(() {
  final pf = FocusManager.instance.primaryFocus;
  // 读 pf 的 rect(getTransformTo + size)打印
});
```

复现"字幕确定 + ◄",日志会显示焦点从 `(584,571)` 跳到 `null@(0,0)`——证实全屏节点吸走了焦点。
对比 ESC 路径,焦点会正常落在相邻按钮(如 `(524,571)` 倍速),不会跳 `(0,0)`。

### 修复

```dart
// player_screen.dart 播放器根层 Focus
child: Focus(
  autofocus: true,
  // 控件可见时,全屏 Focus 不参与焦点遍历(canRequestFocus=false):
  // 否则它覆盖全屏 (0,0)~(w,h),在 D-pad 几何导航里被当作"正对方向的大邻居"选中,
  // 把焦点从控制行按钮吸走 → 看似丢焦。
  // 控件隐藏时设回 true:此时控制行子树已卸载,需要这个节点自己持有焦点,
  // _onWakeControls 才能在隐藏态接到方向键唤出控件。
  canRequestFocus: !_controlsVisible,
  onKeyEvent: _onWakeControls,
  child: FocusTraversalGroup(...),
)
```

**为什么这样安全**:
- 控件可见时(正常操作态):`canRequestFocus=false`,全屏节点不进遍历,◄► 在控制行按钮间
  正常移动;`onKeyEvent` 仍能收到事件(因为 `onKeyEvent` 是从 primaryFocus 向祖先冒泡的,
  全屏节点作为祖先仍在冒泡路径上,但它此时只返回 ignored 不拦键,无副作用)。
- 控件隐藏时:`canRequestFocus=true`,全屏节点自己持焦,`_onWakeControls` 正常接键唤出。

**注意**:`onKeyEvent` 在祖先节点上**即使该节点不可聚焦,只要在 primaryFocus 的祖先链上就会被
调用**(事件冒泡机制)。所以 `canRequestFocus=false` 不影响可见态的事件传递——这点是这个修复
成立的关键,改之前务必理解。

---

## 第一层根因(已修,方案 B,历史记录)

> 这一节是 2026-07 初第一轮排查的记录,根因是 onSelected 时序,方案 B 修好了"选中归位"。
> 它本身仍有效,只是没覆盖到第二层几何导航问题。

### 时序竞争 + 未 await 的 async 回调

文件:`frontend/lib/ui/widget/player_menu.dart` 的 `_openMenu` + 各菜单的 `onSelected` 回调。

**当前代码流程(修复后)**:

```dart
// player_menu.dart _openMenu
Future<void> _openMenu(BuildContext context) async {
  final result = await showDialog<T>(...);   // ① 等 pop(value)
  if (result != null) {
    await widget.onSelected(result);         // ② 回调,await!(方案 B)
  }
  // ③ 归位:延迟到下一帧(postFrame),等焦点树稳定
  if (mounted && _triggerFocus.canRequestFocus) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && _triggerFocus.canRequestFocus) {
        _triggerFocus.requestFocus();
      }
    });
  }
}
```

**修复前(出问题的写法)**:`② widget.onSelected(result)` 没 await,返回 Future 立即往下走,
③ requestFocus 先执行,随后 onSelected 里的 `await setSubtitleTrack + setState` 触发重建,
把刚归位的焦点冲掉。

### 为什么 ESC 关闭没事(第一层)

ESC 走 `DismissIntent` → `Navigator.pop()`(无返回值)→ `result == null` → 不走 ② onSelected
分支,直接 ③ 归位。没有 setState 干扰。

---

## 修复方案(已落地)

### 方案 B(已落地):await onSelected + postFrame 归位

`onSelected` 字段类型从 `ValueChanged<T>` 改成 `Future<void> Function(T)`,`_openMenu` 里
`await widget.onSelected(result)`,归位用 `addPostFrameCallback`。

调用点(`player_screen.dart` 的三个菜单 onSelected)都要返回 Future:
- 速度菜单 `(r) => _setRate(r)` → `(r) async => _setRate(r)`
- 字幕 / 字幕大小 / 音轨菜单本就是 async lambda,无需改

### 第二层修复(已落地):全屏 Focus 的 canRequestFocus

见上方「真正的根因 → 修复」。这是方案 B 之外、必须同时做的修复。

---

## 踩坑教训(为什么修了一天)

这次排查极其低效,记录教训避免重复:

1. **抽象复现够不到真机 bug**。我写了多个 widget 测试模拟"多菜单连续操作 + 横移",**全部
   通过**——因为最小复现里没有那个全屏 Focus 节点,也没有真实播放器的 StreamBuilder 持续重建。
   真机的额外复杂度(全屏 Focus + position stream 每秒重建 + 多个 ModalScope 叠加)是触发
   条件。**教训:焦点几何导航 bug,抽象复现往往无效,必须上真机抓焦点树。**

2. **凭 `debugLabel` 判断焦点落点会被骗**。多个菜单触发按钮都叫 `playerMenuTrigger`,日志里
   看不出焦点落在了哪个。**教训:追踪焦点要用屏幕坐标 + hashCode,不能只看 debugLabel。**

3. **中途误判根因(幽灵 scope)走了弯路**。我曾根据 `enclosingScope=_ModalScopeState` 误判
   为"Dialog FocusScope 残留",但 `_ModalScopeState` 其实是 PlayerScreen 自己 push 出来的
   正常 ModalRoute scope,不是残留。**教训:`_ModalScopeState` 不一定是 Dialog 残留,任何
   `Navigator.push` 的页面都叫这个名字。**

4. **关键的认知突破来自用户**。我反复让用户测,进展缓慢;直到用户问"为什么只有确定才触发、
   ESC 不触发"——这个对比把思路从"焦点归位时序"校正到"几何导航候选 + 重建扰动"。**教训:
   ESC vs 确定的行为差异是定位这类 bug 的关键线索,要主动利用对比,而不是只盯着一条路径。**

5. **应该早点上真机抓焦点树**。纯看代码 + 抽象测试浪费了大半天。一旦加了全局 `FocusManager`
   监听打印 primaryFocus 坐标,一次复现就看到了焦点跳 `(0,0)`。**教训:焦点 bug 第一时间
   上真机 + 焦点坐标日志,别相信抽象测试。**

### 快速定位 checklist(下次遇到焦点丢失)

1. 加全局监听打印每次 `primaryFocus` 变化的**屏幕坐标 + hashCode**:
   ```dart
   FocusManager.instance.addListener(() {
     final pf = FocusManager.instance.primaryFocus;
     // 用 pf.context.findRenderObject().getTransformTo(null) 读坐标打印
   });
   ```
2. 复现,看焦点跳到了**哪个节点的坐标**。`null@(0,0)` 这种 = 全屏匿名节点吸焦。
3. 找那个全屏 Focus 节点(grep `autofocus: true` / 全屏 Stack 的祖先),加 `canRequestFocus`
   控制它只在需要时参与遍历。
4. 对比 ESC vs 确定、空操作 vs 有副作用的路径,定位是什么扰动了几何候选。

---

## 相关踩坑(已记录)

- `docs/pitfalls/flutter-navigation.md` 坑 3(MenuAnchor 焦点归位 → showDialog)、坑 8
  (await 后用 context 的 mounted 守卫)——本坑是那两个的延伸。第二层(全屏 Focus 几何导航)
  是当时完全没覆盖到的新维度。

## 相关文件

- `frontend/lib/ui/widget/player_menu.dart`:`_openMenu`(方案 B + postFrame 归位)
- `frontend/lib/ui/screen/player_screen.dart:587`:全屏 Focus 的 `canRequestFocus: !_controlsVisible`
- `frontend/lib/ui/screen/player_screen.dart` 的 `_onWakeControls`:隐藏态接键唤出控件逻辑
