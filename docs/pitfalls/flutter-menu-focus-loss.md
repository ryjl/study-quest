# 调试指南:Flutter 播放器菜单确定后焦点丢失

> **现象**(用户反馈):TV 模式下,播放器的字幕 / 音轨 / 速度菜单,**选中某项确认后**,
> 焦点找不到 —— D-pad 按键无响应或飘到不确定的位置,要重新唤出控件才能恢复。
>
> **ESC 关闭**菜单焦点正常,只有**「确认选中」**这条路径有问题。
>
> 本文件是给下个 session 的操作指南:根因已定位 + 验证步骤 + 修复方案。

---

## 根因(已定位)

文件:`frontend/lib/ui/widget/player_menu.dart` 的 `_openMenu`(行 66-86)+ 各菜单的 `onSelected` 回调(player_screen.dart 行 1381/1398/1417)。

**时序竞争 + 未 await 的 async 回调**。

### 当前代码流程

```dart
// player_menu.dart _openMenu
Future<void> _openMenu(BuildContext context) async {
  final result = await showDialog<T>(...);   // ① 等 pop(value)
  if (result != null) {
    widget.onSelected(result);               // ② 回调,未 await!
  }
  if (mounted && _triggerFocus.canRequestFocus) {
    _triggerFocus.requestFocus();            // ③ 归位
  }
}
```

```dart
// player_screen.dart 字幕菜单 onSelected(行 1398)
onSelected: (idx) async {                    // ← async!返回 Future
  final opt = subtitleOptions[idx];
  await _applySubtitleOption(opt, idx);      // ← 内部有 await _player.setSubtitleTrack
  setState(() {});                            // ← 重建
  _scheduleAutoHide();
},
```

### 时序错乱

```
②  widget.onSelected(result)   ← 没 await,返回 Future 立即往下
③  _triggerFocus.requestFocus() ← 焦点归位到触发按钮 ✓
    ...
    (此时 onSelected 的 Future 还在跑)
    await _player.setSubtitleTrack(...)  ← 毫秒~几十毫秒
    setState(() {})                      ← 触发 PlayerScreen 整树重建
    setState(() => _selectedSubtitle=index) ← _applySubtitleOption 内又一次重建
```

焦点丢在两次 `setState` 触发的重建里。叠加 Dialog ModalRoute 卸载时的**焦点恢复**(Flutter 默认会恢复"打开 dialog 前的 primary focus",但这个恢复和上面的 requestFocus 交错),焦点最终落点不确定。

### 为什么 ESC 关闭没事

ESC 走 `DismissIntent` → `Navigator.pop()`(无返回值)→ `result == null` → **不走 ② onSelected 分支**,直接 ③ 归位。没有 `setState` 干扰,所以 ESC 路径焦点正常。这正好印证了根因:**是 onSelected 的 setState 时序在捣乱**。

---

## 验证步骤(下个 session 先做这个确认根因)

### 步骤 1:加日志确认时序

在 `player_menu.dart` 的 `_openMenu` 和 `player_screen.dart` 的 `onSelected` 加打印:

```dart
// player_menu.dart _openMenu
final result = await showDialog<T>(...);
debugPrint('[MENU] dialog closed, result=$result, t=${DateTime.now().millisecondsSinceEpoch}');
if (result != null) {
  widget.onSelected(result);
  debugPrint('[MENU] onSelected returned (future pending), t=...');
}
if (mounted && _triggerFocus.canRequestFocus) {
  _triggerFocus.requestFocus();
  debugPrint('[MENU] requestFocus on trigger, hasFocus=${_triggerFocus.hasFocus}, t=...');
}
```

```dart
// player_screen.dart 字幕 onSelected
onSelected: (idx) async {
  debugPrint('[SUB] onSelected start, t=...');
  await _applySubtitleOption(opt, idx);
  debugPrint('[SUB] after apply, t=...');
  setState(() {});
  debugPrint('[SUB] after setState, hasFocus of trigger=???');
},
```

跑 TV 模式,选中字幕,看 logcat / console。**预期会看到**:`requestFocus` 的时间戳早于 `after setState`,且 `hasFocus` 在 setState 后变成 false。

### 步骤 2:确认是不是 setState 冲掉的

临时把字幕的 `onSelected` 改成**不 setState**:

```dart
onSelected: (idx) async {
  await _applySubtitleOption(opt, idx);
  // setState(() {});  ← 临时注释掉
},
```

如果注释掉 setState 后焦点**不丢了**,根因确认。`_applySubtitleOption` 内部的 `setState(() => _selectedSubtitle = index)` 还在,所以视觉上选中态仍会更新(只是字幕大小菜单那种需要刷新的不更新 —— 够验证用了)。

---

## 修复方案

### 方案 A(推荐,最小改动):requestFocus 延迟到下一帧

`requestFocus()` 放进 `WidgetsBinding.instance.addPostFrameCallback`,确保在 onSelected 的 setState 重建**之后**执行。

```dart
// player_menu.dart _openMenu
Future<void> _openMenu(BuildContext context) async {
  final result = await showDialog<T>(...);
  if (result != null) {
    widget.onSelected(result);
  }
  // 关键:延迟到下一帧。onSelected 里若有 setState 触发重建,
  // 同步 requestFocus 会被那次重建的焦点恢复覆盖;postFrame 确保在重建之后归位。
  if (mounted && _triggerFocus.canRequestFocus) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && _triggerFocus.canRequestFocus) {
        _triggerFocus.requestFocus();
      }
    });
  }
}
```

**为什么 postFrame 能解决**:Flutter 的 rebuild → focus restore 都在同一帧的 build phase,`addPostFrameCallback` 在帧末尾执行,此时重建已完成、焦点树稳定,`requestFocus` 是最后一个生效的。

如果 postFrame 一帧不够(onSelected 的 await 跨多帧),再加一帧延迟或改用方案 B。

### 方案 B(更彻底):await onSelected 后再归位

让 `_openMenu` await onSelected 的 Future,等它(含内部 setState)全跑完再归位:

```dart
Future<void> _openMenu(BuildContext context) async {
  final result = await showDialog<T>(...);
  if (result != null) {
    await widget.onSelected(result);   // ← 加 await,等回调全跑完
  }
  // 此时 onSelected 的 setState 已完成,焦点树稳定,归位可靠
  if (mounted && _triggerFocus.canRequestFocus) {
    _triggerFocus.requestFocus();
  }
}
```

**前提**:`ValueChanged` 要改成 `Future<void> Function(T)`(或保留 `ValueChanged` 但调用方返回 Future)。需要改 `PlayerSettingsMenu` 的 `onSelected` 字段类型 + 三个调用点都确保是 async。

```dart
// player_menu.dart 类定义
final Future<void> Function(T) onSelected;   // ← 从 ValueChanged<T> 改
```

这是最干净的方案 —— 归位严格发生在"所有副作用之后"。

### 方案 C(防御性兜底):给触发按钮加 focus listener 重新抢焦点

如果 A/B 都偶发失效(极端时序),给 `_triggerFocus` 加监听,发现焦点在归位后又被夺走就抢回来:

```dart
// _PlayerSettingsMenuState
bool _menuJustClosed = false;

// _openMenu 里
_menuJustClosed = true;
final result = await showDialog<T>(...);
// ... onSelected ...
WidgetsBinding.instance.addPostFrameCallback((_) {
  _triggerFocus.requestFocus();
  // 给一个短暂窗口(如 200ms)内,焦点若被夺走就抢回
  Future.delayed(const Duration(milliseconds: 200), () {
    _menuJustClosed = false;
  });
});

// initState 加
_triggerFocus.addListener(() {
  if (_menuJustClosed && !_triggerFocus.hasFocus && _triggerFocus.canRequestFocus) {
    _triggerFocus.requestFocus();  // 焦点被夺,抢回
  }
});
```

方案 C 有点 hack,优先用 A 或 B。

---

## 推荐落地顺序

1. **先做验证步骤**(加日志),确认是不是这个时序问题 —— 别假设,先证实。
2. **用方案 A(postFrame)**试,90% 情况这一步就够了。
3. 若 A 仍偶发失效(字幕 backend 那条路径 `await _player.setSubtitleTrack` 比较慢),升级到**方案 B(await onSelected)**。
4. B 是终态正确写法,值得直接做。

---

## 顺带检查:其他菜单是否同病

三个菜单的 `onSelected` 都要看(同样可能中招):

| 菜单 | 位置 | onSelected 是否 async + setState | 风险 |
|---|---|---|---|
| 速度 | player_screen.dart:1381 | `_setRate(r)` → 非 async,但内部可能 setState | 低(同步快) |
| 字幕 | player_screen.dart:1398 | **async + await setSubtitleTrack + setState** | **高(已确认)** |
| 字幕大小 | player_screen.dart:1417 | async + `await UiPrefs.set...` + setState | 中 |
| 音轨 | grep `_applyAudioOption` | async + `await _player.setAudioTrack` | 高 |

修复在 `player_menu.dart` 的 `_openMenu`(方案 A/B)会**一次性覆盖所有菜单**,不用逐个改。

---

## 相关踩坑(已记录)

- `docs/pitfalls/flutter-navigation.md` 坑 3(MenuAnchor 焦点归位 → showDialog)、坑 8(await 后用 context 的 mounted 守卫)—— 本坑是那两个的延伸:Dialog 方案解决了关闭归位,但**确认选中**这条路径的时序漏洞是当时没覆盖到的。
- 修复后建议在 `flutter-navigation.md` 坑 3 补一句"确认选中后归位要用 postFrame / await onSelected"。
