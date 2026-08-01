# Pitfall:播放器 ESC / 返回键分层

> **症状**:播放页按 ESC **直接退出页面**,而不是按分层规约「先关控件、再按才退出」。
>
> **根因**:escape 绑了框架 `DismissIntent`,被 `MaterialPageRoute` 的 `_DismissModalAction`
> (=`Navigator.maybePop()`)抢走。系统返回键不受影响(走另一条通道)。

## 为什么 escape 不能绑框架 `DismissIntent`

`MaterialPageRoute` 在 `_ModalScope` 里注册了:

```dart
// flutter/.../widgets/routes.dart:1198
actions: {DismissIntent: _DismissModalAction(context)},  // invoke = maybePop()
```

`WidgetsApp` 默认又把 `escape → DismissIntent` 注册成全局 shortcut。所以一旦某个 widget
用 `SingleActivator(escape): DismissIntent()` 绑 ESC,就同时暴露在两条 action 路径:

- 焦点在该 widget 自己的 `Actions` 子树内 → 命中 widget 的 action(预期)。
- 焦点飘到子树外 → action 沿焦点路径冒泡,**命中 ModalRoute 的 `_DismissModalAction`**
  → `maybePop()` → 页面被退出。

播放页是 `MaterialPageRoute` push 出来的,TV 遥控器操作时焦点可能落在 helper panel /
控件层等不同位置,所以 ESC 表现成「有时退出」。

## 为什么系统返回键没这个问题

`KEY_BACK` 走 Android 标准 back 通道:`onBackPressed → PopScope → onPopInvokedWithResult`,
**整条链不碰 DismissIntent**。所以返回键的分层一直正常,只有 ESC 出问题——这也是诊断
迷惑点:返回键正常会让人误以为「分层逻辑没问题」,漏掉 ESC 走另一条 action 链。

## 修复(两层)

1. **escape 改绑本屏私有 `_ToggleControlsIntent`**(不用框架 `DismissIntent`)。
   ModalRoute 不认私有 Intent → 从根上杜绝被抢。焦点无论在哪,ESC 要么命中本屏 handler,
   要么无人处理(不退出),绝不误命中 ModalRoute 的 pop。
   > Dialog/菜单路由自己注册的 `DismissIntent`(关菜单)不受影响——菜单在更高层 route,
   > 它的 DismissAction 优先级更高。
2. **YouTube/B站 式分层 `_applyEscLayering`**(ESC 与返回键统一走它):

   | 状态 | ESC / 返回键行为 |
   |------|------------------|
   | 全屏 + 控件隐藏 | 唤出控件(不退出) |
   | 全屏 + 控件可见 | 退出全屏 |
   | 非全屏 + 控件可见 | 关控件 |
   | 非全屏 + 控件隐藏 | 退出页面(真正 pop) |

## 相关文件

- `frontend/lib/ui/screen/player_screen.dart`:`_ToggleControlsIntent`(私有 ESC Intent)、
  `_applyEscLayering`(分层状态机)、Shortcuts 的 escape 绑定、
  `PopScope(canPop:false) + onPopInvokedWithResult`(返回键路径,未改)。

## 测试注意

按键类回归**不要只在模拟器(mumu)上测**:`adb input keyevent` 注入的键和真实按键在 mumu
上走不同路径(注入的 ESC 进 Flutter KeyEvent,真实 ESC 不进、直接 pop),会得到矛盾结果。
按键问题必须上真机。
