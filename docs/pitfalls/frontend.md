# 前端踩坑集（frontend pitfalls）

> Admin SPA（React + TanStack Query）+ Flutter 客户端共用。AI 模块的坑见
> `docs/pitfalls/backend.md` 和 `docs/modules/ai/*`。
>
> **Flutter 导航 / 路由 / TV 焦点**相关的坑(命令式 push 无路由表、TextField D-pad 焦点陷阱、
> MenuAnchor 焦点归位、几何导航 vs FocusScope 隔离、返回键键值、互跳无限栈等)单独成篇,
> 见 **`docs/pitfalls/flutter-navigation.md`** —— 那里有 14 条真坑 + FocusTraversalGroup vs
> FocusScope 速查表 + TV 专属 vs 通用分类。后续若回归 Flutter 单端会重新全部生效。

## Admin SPA（React + TanStack Query）

### 写操作必须 invalidate 相关 queryKey

React Query 是 UI 的 **immediate source of truth**，不是 server。Login、logout、CRUD、
reorder、probe、settings save——每个写操作必须 `invalidateQueries`（或 `setQueryData`）
对应的 key，否则 UI 读 stale 数据。

最经典的症状是**登录 bounce**：登出 → 跳登录页 → 登录成功但 UI 还显示登录页（因为
`['me']` query 还缓存着 unauthenticated 状态）。规则：所有 mutation 的 `onSuccess`
必须 invalidate 它影响的所有 key。

### `useTypedMutation` 错误消息用 `||` 不用 `??`

`useTypedMutation` helper 的错误消息：

```ts
const msg = (e as { message?: string }).message || opts.errorMsg || '操作失败';
```

**用 `||`（逻辑或）而不是 `??`（空合并）**。原因：`??` 只在 `null`/`undefined` 时
fallthrough，**空字符串 `""` 会通过**。`throw new Error('')` 用 `??` 会渲染成空白
toast；用 `||` 正确 fallback 到 `errorMsg` 或通用文案。

### `api/` 域文件聚合时方法名必须跨域唯一

`frontend-admin/src/lib/api/` 拆成 25 个域文件（auth/courses/users/ai/...），每个
导出一个 sub-object，`lib/api/` 域聚合 (拆分后) 用 `{ ...auth, ...courses, ... }` spread 成 flat
`api`。**方法名跨域冲突时，后 spread 的会静默 shadow 前面的**。

加新方法前先 `grep -rn "async <name>(" src/lib/api/` 验证无冲突。本轮拆分时已用 Python
脚本独立验证 133 个方法名零冲突。

## Flutter 布局

### 无穷大约束导致静默布局异常（最严重的一类）

**真实事故**（2026-07-19）：AI 吐的 Markdown 文本含 `<svg>` 图表，`_SvgView` 用
`width: double.infinity` 撑满父容器。当无限大宽度的 SVG 被包在 markdown 表格里时，
`flutter_markdown` 的 `IntrinsicColumnWidth` 尝试获取内部 intrinsic size——无限大无法
测量，触发 `NaN`/`Infinity` 错误。

**两个升级症状**：
1. **组件离奇消失**：`ListView`/`Sliver` 遇到子组件布局失败时，为了保证不崩溃会
   把该子节点（甚至后续兄弟节点）直接丢弃。后台返回了 quiz 数据、logcat 也进 ready
   分支，但 UI 看不到。
2. **C++ 渲染崩溃**：外层加 `clipBehavior: Clip.hardEdge` 防溢出，但 clip 框根据
   NaN 尺寸生成 Transform Matrix 时直接宕机（Impeller/Skia），抛
   `TransformLayer is constructed with an invalid matrix`，全屏白屏。

**规则（红线）**：
- SVG 必须带 `width`/`height`/`viewBox`，让客户端稳定测量 intrinsic size
- 不在外层加 `width: double.infinity` / `double.infinity` 高度
- 用 `BoxFit.contain → fitWidth` 让 SVG 自适应，**不加 width/height/clip 硬约束**
- 让 prompt 强制 AI 吐带尺寸的 SVG，而不是客户端兜底

### `AnimatedSize` 过渡帧会触发同类渲染崩溃

为避免同类 NaN bug，折叠/展开 UI 用**纯布尔显隐**（`if (_expanded) <Widget/>`），
**不用 `AnimatedSize`**。它的过渡帧会触发同类布局异常。

`_HistoryQuizCard` 和课程总览卡片都用这个模式。

## Flutter 媒体（media_kit / libmpv）

### 播放器进度条 5 个子坑（2026-07-05 复盘）

来自 `bugs_player_seek_resume.md`（已删原文）。**核心教训：日志驱动，不猜测**——
一个本质是"调整位置"的需求折腾一整天，主因是前期靠症状假设反复试错。应该一开始就
抓完整 position 时间序列，看清 reset 模式。

5 个具体子坑：

1. **StreamBuilder 子树重建必须配 `initialData`**：UI 子树的 mount/unmount 会让
   StreamBuilder 重新订阅，回 null 必崩。提供同步的 initialData 兜底。
2. **Slider 拖动 = 每像素一次回调**：密集 IO 操作（seek、网络请求）必须在
   `onChangeEnd` 做，不能 `onChange`（撕裂 demuxer）。
3. **CDN 直链续播 seek 被 RST**：天翼云等 CDN 网络重连触发 libmpv 重新 open、reset
   position。应用层只能对抗（resume-seek watchdog），根治需要换稳定流源或自建代理。
4. **完成判断不能用累计观看时长**：这是逻辑错误。"已完成"不等于从头看过 N 分钟，
   进度条跳到 99% 一次也可能完成。
5. **完成判断与续播位置必须解耦**：不能因为"已完成"就强制从头播。产品直觉，不要
   违背。

## Flutter `ChangeNotifier`

### 没人 listen 时 `extends ChangeNotifier` 是死代码

`UiPrefs` 和 `TvMode` 之前都 `extends ChangeNotifier`，但调用方都是 `setState` 手动
触发 rebuild，没人 `addListener`/`notifyListeners` 外部消费。这是"最坏的两头"——
多写了代码，没拿到响应式 rebuild 的好处。

**两条路任选**：
- 接进 `MultiProvider`，screens 用 `context.watch`/`context.select` 自动 rebuild
- 删 `extends ChangeNotifier`，承认是普通单例（**本轮选这条**，最小改动）

注意：很多 call site 在 `onTap` / 非 build context 里，`context.watch` 在那里是
非法的，不能无脑接 MultiProvider。

## Flutter HTTP

### OTA `update_service` 和 `pdf_reader` 故意 bypass `ApiService`

这两个文件直接 `http.get`/`http.Request`，不走 `ApiService` 统一封装。**不是疏忽，
是设计**：

- `update_service.dart`：OTA 在登录前运行（pre-auth，没有 session token），只需要
  bare `Accept` header
- `pdf_reader_screen.dart`：流式下载任意 book URL，手动 byte count 做进度条

改之前先理解为什么 bypass。**别无脑统一**——把它们塞进 `ApiService` 会破坏 auth-free
设计和流式语义。两处已加 `// NOTE: bypasses ApiService because...` 注释。

## 颜色 token

### 散落 `Color(0xFF...)` 字面量是技术债

`AppTheme` 定义了 `textMuted`/`borderMuted`/`accentGreen` 等 token，但 screens
长期直接写 `Color(0xFF64748B)` 字面量——同一个颜色在十几个文件里重复写死，改主题
要全局 grep。

本轮重构扫出 ~370 处字面量，替换了高频 10 文件。**新代码规则**：颜色一律走
`AppTheme.xxx`，没有合适的 token 就先加 token（`lib/theme.dart`）再用。

## Deprecated API 警告不能拖

Flutter 升级会 deprecated 一些 API（`withOpacity`→`withValues`、
`activeColor`→`activeThumbColor` 等）。`flutter analyze` 的 info 警告拖久就成
error（下一版 Flutter）。本轮清掉了全部 50 个 info 警告。新代码注意：
- 用 `.withValues(alpha: x)` 不用 `.withOpacity(x)`
- `SwitchListTile` 用 `activeThumbColor` 不用 `activeColor`
