# Pitfall:Flutter 播放器菜单确定后焦点丢失

> **症状**:TV 模式下,播放器菜单(字幕/音轨/速度)**选中某项确认后**,D-pad 方向键
> 无响应或焦点飘到不确定位置,要重新唤出控件才能恢复。**ESC 关闭菜单焦点正常**,
> 只有「确认选中」这条路径有问题。
>
> 这个 bug 有**两层**,必须都修。

## 第一层:`_openMenu` 未 await `onSelected`

`player_menu.dart` 的 `_openMenu` 里 `widget.onSelected(result)` 没 await,返回 Future
立即往下执行 requestFocus,随后 onSelected 里的 `await setSubtitleTrack + setState` 触发
重建,把刚归位的焦点冲掉。

**修复**:onSelected 字段类型从 `ValueChanged<T>` 改成 `Future<void> Function(T)`,
`_openMenu` 里 `await widget.onSelected(result)`,归位改用 `addPostFrameCallback`(等焦点树
稳定后再 requestFocus)。见 `frontend/lib/ui/widget/player_menu.dart`。

**为什么 ESC 不触发**:ESC 走 `DismissIntent` → `Navigator.pop()`(无返回值)→ result 为
null → 不走 onSelected 分支,没有 setState 干扰。

## 第二层(真正的终态修复):全屏 Focus 参与了几何导航

第一层修好后暴露出真正根因:播放器根层有个**全屏 `Focus(autofocus: true)` 节点**
(`_onWakeControls` 用,控件隐藏态接方向键唤出控件)。它默认 `canRequestFocus: true`,
**参与了几何导航**——覆盖全屏 `(0,0)~(w,h)`,在 D-pad 几何算法里被当作"正对方向的大邻居"
选中,把焦点从控制行按钮吸走(视觉上 = 丢焦)。

确认路径触发、ESC 不触发:onSelected 的 setState 重建扰动了几何导航的候选优先级,这时
全屏节点才被选中。是**全屏节点参与导航** + **onSelected 重建扰动**两因素叠加。

**修复**(`player_screen.dart` 根层 Focus):

```dart
child: Focus(
  autofocus: true,
  // 控件可见时不参与焦点遍历:否则它覆盖全屏,在 D-pad 几何导航里被当作"正对方向的大邻居"
  // 选中,把焦点从控制行按钮吸走 → 看似丢焦。隐藏时设回 true:控制行子树已卸载,需要这个
  // 节点自己持焦,_onWakeControls 才能在隐藏态接到方向键唤出控件。
  canRequestFocus: !_controlsVisible,
  onKeyEvent: _onWakeControls,
  child: FocusTraversalGroup(...),
)
```

**关键认知**:`onKeyEvent` 在祖先节点上**即使该节点不可聚焦,只要在 primaryFocus 的祖先链
上就会被调用**(事件冒泡)。所以 `canRequestFocus=false` 不影响可见态的事件传递——这是这个
修复成立的要点。

## 快速定位 checklist(下次遇到焦点丢失)

1. 加全局监听打印每次 `primaryFocus` 变化的**屏幕坐标 + hashCode**(不要只看 debugLabel,
   多个触发按钮可能同名)。
2. 复现,看焦点跳到了哪个节点的坐标。`null@(0,0)` 这种 = 全屏匿名节点吸焦。
3. 找那个全屏 Focus 节点(grep `autofocus: true` / 全屏 Stack 的祖先),用 `canRequestFocus`
   控制它只在需要时参与遍历。
4. **焦点几何导航 bug,抽象 widget 测试往往无效**(最小复现里没有真实播放器的全屏 Focus +
   position stream 每秒重建)——必须上真机抓焦点树。

## 相关文件

- `frontend/lib/ui/widget/player_menu.dart`:`_openMenu`(await onSelected + postFrame 归位)
- `frontend/lib/ui/screen/player_screen.dart`:根层 Focus 的 `canRequestFocus: !_controlsVisible`
  + `_onWakeControls`(隐藏态接键唤出控件)
- 相关:`docs/pitfalls/flutter-navigation.md` 坑 3(MenuAnchor 焦点归位 → showDialog)、坑 8
  (await 后用 context 的 mounted 守卫)——本坑是那两个的延伸。
