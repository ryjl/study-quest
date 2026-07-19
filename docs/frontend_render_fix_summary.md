# 前端渲染问题修复总结 (2026-07-19)

本轮修复彻底解决了 3 个因为布局异常或约束溢出导致的视觉问题，其中 Bug 2 和 Bug 3 的“组件离奇消失”甚至导致了更底层的 C++ 渲染崩溃（一闪白屏）。以下是问题的根因与修复方案汇总。

## 1. 致命崩溃与组件消失 (Bug 2 Quiz消失、Bug 3 课程目录消失)

### 根因分析
这属于典型的“无穷大约束导致的静默布局异常”。
- **起因：** 在 AI 吐出的 Markdown 文本中，包含了表格和 `<svg>` 图表。在 `_SvgView` 渲染时，默认使用了 `width: double.infinity` 以撑满父容器。
- **触发：** 当这些带有无限大宽度的 SVG 被包裹在 Markdown 表格中时，`flutter_markdown` 的 `IntrinsicColumnWidth` 尝试去获取内部元素的内容边界（Intrinsic size）。由于无限大无法被测量，它导致了一个布局阶段的 `NaN`/`Infinity` 错误。
- **现象（消失）：** Flutter 的 `ListView` 或 `Sliver` 在遇到某个子组件布局失败时，会为了保证不崩溃而将该子节点（甚至后续的兄弟节点）直接丢弃。这就导致了明明后台返回了 Quiz 数据，并且进入了 ready 分支，界面上却“离奇消失”。
- **现象升级（崩溃）：** 为了防止表格溢出，我们在外层容器上加上了 `clipBehavior: Clip.hardEdge` 防御。但这个改动引发了更严重的后果：当裁剪框尝试根据子元素的无效尺寸（NaN）生成 Transform Matrix 矩阵时，直接导致底层的 C++ 渲染引擎（Impeller/Skia）宕机，抛出 `TransformLayer is constructed with an invalid matrix` 并引发“一闪后全屏白屏”。

### 修复方案
- **移除 `width: double.infinity`：** 将 `_SvgView` 内部 `SvgPicture.string` 的宽度约束去掉，让其根据 SVG 的 `viewBox` 自适应其真实的 Intrinsic 尺寸。这从根源上阻断了向上抛出无限大尺寸的问题，组件重新正确布局。
- **回退裁剪：** 移除了导致崩溃的所有 `clipBehavior: Clip.hardEdge`。

---

## 2. 表格审美冲突与盖字 (Bug 1 table盖文字)

### 根因分析
- 原先 `methods` 和 `commonMistakes` 使用了纯绿色（`#F0FDF4`）和纯红色（`#FEF2F2`）作为全区域的底色。
- LLM 吐出的表格和 SVG 往往带有自己默认的中性透明/灰色背景。由于底色饱和度较高，带有表格和灰色边框的内容直接被扔进去后，呈现出一种“红绿配灰边”的粗糙拼凑感。

### 修复方案
- **引入左侧彩条设计（Left-Border Accent）：** 彻底放弃大红大绿的全区域底色，转而使用 `border: Border(left: BorderSide(color: Color(0xFF10B981), width: 3))` 的设计。
- 这种设计让 Markdown 组件能够在一个纯白/纯透明的背景上自由渲染。不管 LLM 吐出的图表是什么底色，都能完美、干净地融合到页面中。
- 此外，将全局的 Markdown 表格边框从极浅的 `Slate-200` 微调到 `Slate-300`，进一步增加了它在白底上的清晰度。
