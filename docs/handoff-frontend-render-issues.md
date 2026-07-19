# Handoff:前端渲染问题 [已解决 DONE]

> 给下一个会话的起步文档。3 个前端渲染 bug,根因都疑似"某个卡片渲染异常让后续兄弟节点不显示"。
> 本文档由 2026-07-19 会话结束时整理,所有现状数据已核实。前端崩溃及样式冲突已被 Antigravity 彻底修复。

## 一句话需求

修 3 个让用户能看见但渲染不对的 bug:
1. **table 盖文字**(数学课 `common_mistakes` 里的对比表格盖到旁边文字)
2. **数学课 quiz 卡片不显示**(后端返回 ready,前端 logcat 也进 ready 分支,但视觉上看不到)
3. **课程总览卡片让下方章节列表看不见**(用户新发现:点进课程确实显示了课程总览内容,但下面的章节列表不见了)

3 个 bug 疑似同一类问题:**Flutter 在 ListView/Column 里渲染某个卡片时抛了异常,框架把后续兄弟节点也吞了**。

---

## 已确认的根因和现状(本 session 排查结论)

### 已修(不要重改)

- **AI 页空白**:`MarkdownView._normalizeMarkdown` 用了 `RegExp(r'(?s)...')` → Dart RegExp(ES 风格)**不支持 `(?s)` 内联 flag** → 抛 FormatException → build() 失败 → 整页空白。已改 `dotAll: true`。Go 后端的 `(?s)` 没问题(RE2 支持),不要动。
- **summary 里的 `\n` 字面量 + 裸 SVG**:LLM 输出 JSON 字符串值时把 `\n` 当 2 字符字面量、把 SVG 不加围栏直接吐裸的。后端 `parseSummaryJSON.normalizeMarkdownInFields` + 客户端 `MarkdownView._normalizeMarkdown` 双向 normalize 已修。

### 未修的 3 个 bug 的现状

#### Bug 1:table 盖文字

**位置**:`frontend/lib/ui/screen/ai_study_screen.dart` 的 `common_mistakes`(line ~633)和 `methods`(line ~621)块。

```dart
// methods 块(line 620-629)
...s.methods.map((m) => Padding(
  padding: const EdgeInsets.only(bottom: 2),
  child: MarkdownView(data: m, ...),  // ← 在 Container(padding: all(10)) 里
)),
```

外层是 `Container(padding: EdgeInsets.all(10)) + Column`(带背景色装饰),给 `MarkdownView` 一个 tight maxWidth 约束。`MarkdownView` 用 `IntrinsicColumnWidth()` 表格,flutter_markdown 0.7 内部会自动给表格套横向 `SingleChildScrollView`(见 `flutter_markdown/lib/src/builder.dart:515-535`)——但**在这个约束下没生效**,表格溢出盖到旁边文字。

**实测数学课 ep=25 的 `common_mistakes[5]`** 是 GFM 表格 `| 易混淆点 | 正确理解 |` 4 行,渲染后盖字。

**试过但不行的方案**(已回退):
- 把 `MarkdownView.build()` 外层套 `LayoutBuilder` + `SingleChildScrollView(horizontal)` + `ConstrainedBox(minWidth: parentWidth)`:让 quiz 卡片**完全消失**了(可能 ListView 子项 width=double.infinity 的连锁影响),已回退。
- 把 `tableColumnWidth` 改成 `FlexColumnWidth(1.0)`:让表格**完全消失**(走 flutter_markdown builder.dart:534 的 else 分支,不包 scrollview),已回退。

**没试过但应该有效的方向**:
- 给 `common_mistakes`/`methods` 块也用 `_PointItem` 模式分流(含 block 时跳出带 padding 的 Container,走整行宽度)——但会丢背景色装饰,需要重新设计样式。
- 注册自定义 `table` builder,把表格替换成 `SingleChildScrollView(horizontal) + ConstrainedBox(maxWidth: 父宽度)`——需要重建 Table widget,复杂。
- 给 `MarkdownView` 加一个 `parentMaxWidth` 参数,内部把表格手动套横向 scrollview。

#### Bug 2:数学课 quiz 卡片不显示

**最诡异的一个**。`logcat` 抓到的日志(commit `99b408f` 已加诊断 print,release 也输出):

```
[ai-study] _buildQuizSection ep=25 loading=true status=null quizNull=true       ← 初次
[ai-study] _loadQuiz ep=25 → status=QuizStatus.ready quiz=10q                    ← 后端 ready
[ai-study] _buildQuizSection ep=25 loading=false status=QuizStatus.ready quizNull=false  ← 进 ready 分支
```

也就是说 `_buildQuizSection` **正确走到了 ready 分支**,开始渲染题目(`Column(crossAxisAlignment: start, children: [...questions...])`)。但用户**视觉上看不到 quiz 卡片**。

logcat 里**没有任何 Flutter 异常**——说明要么是 silent exception(被框架吞了),要么是布局问题(渲染了但宽度/高度为 0)。

**怀疑方向**:
- `multi_choice` 题型渲染(`_multiOptionTile` line ~1030)在某种状态下抛 silent exception。象棋课 quiz 含 `multi_choice`,但数学课只有 `fill` 和 `choice`——所以**问题不在 multi_choice**,在更通用的渲染路径。
- `_optionTile`(line ~1077)或 `_buildFillInput` 在某种数据下抛异常。
- 题目卡片渲染了但 ListView 滚动条没显示,用户没看到下方——但用户说"看不到",且 summary 在上面就能看到,quiz 应该就在下方一屏内。
- 整页 ListView 渲染过程中,某个上层 widget 抛异常让 ListView 子节点截断。

**重现条件**:数学课 ep=25 一定能复现。

#### Bug 3:课程总览卡片让下方章节列表看不见(2026-07-19 新发现)

**位置**:`frontend/lib/ui/screen/course_detail_screen.dart` 的 `_buildCourseSummaryCard`(本 session 新加,line ~347 附近)。

```dart
// course_detail_screen.dart 的 ListView body(line ~198)
Column(children: [
  // Hero header
  Container(...child: _buildHeroContent(...)),
  const SizedBox(height: 40),
  _buildCourseSummaryCard(context),  // ← 本 session 加的课程总览卡片
  const SizedBox(height: 40),
  // Chapter Directory Panel
  Container(...child: Column(...)),  // ← 闯关目录,用户说看不见
])
```

**用户描述**:"生成课程总结后,pad 点进课程,上面确实显示了课程总结内容,但是下面的课程列表看不见了"。

`_buildCourseSummaryCard` 内部用 `FutureBuilder<CourseSummary?>`:

```dart
return FutureBuilder<CourseSummary?>(
  future: _courseSummaryFuture,
  builder: (context, snapshot) {
    if (snapshot.connectionState == ConnectionState.waiting) return const SizedBox.shrink();
    if (snapshot.hasError) return const SizedBox.shrink();
    final summary = snapshot.data;
    if (summary == null || !summary.isReady || ...) return const SizedBox.shrink();
    return Container(width: double.infinity, ...);  // ← 卡片
  },
);
```

**怀疑方向**:课程总览卡片渲染时抛 silent exception(可能跟 Bug 2 同类),让外层 Column 渲染中断,后续的"闯关目录"也被吞。
- 内部用了 `MarkdownView`(渲染 summary_text)——如果 summary_text 里有表格/SVG,跟 Bug 1 同源。
- `width: double.infinity` 可能在某些约束下炸。

---

## 怎么开始(下 session 推荐流程)

### Step 0:环境就绪

```bash
# MuMu 已装最新版(versionCode 4001,commit 99b408f),adb 连接:
adb connect 172.24.240.1:16384   # 或 :7555
# app 包名:com.revin.study_quest
# 已加 4 处 print 诊断日志,release 模式下也输出
adb -s 172.24.240.1:16384 logcat -c
adb -s 172.24.240.1:16384 logcat -v time | grep ai-study
```

服务器后端:`ssh -p 30901 ry@192.168.8.4`,DB 在 `/home/ry/data/studyquest-data/studyquest.db`。

### Step 1:确认 3 个 bug 还能复现

打开 MuMu,登录,进数学课 ep=01 第一节(episode_id=25):
- [ ] summary 有内容但 table 盖字(Bug 1)
- [ ] quiz 卡片不显示(Bug 2)

进象棋课某一节(有课程总览的):
- [ ] 课程总览显示了,但下方章节列表不见(Bug 3)

抓 logcat 看 `[ai-study]` 输出。

### Step 2:找 silent exception

Flutter release 模式下默认不打印 stack trace,但 **`FlutterError.onError` 可以重写** 让它打印。在 `main.dart` 加:

```dart
FlutterError.onError = (details) {
  FlutterError.presentError(details);
  debugPrint('🔥 FlutterError: ${details.exception}');
  debugPrint(details.stack.toString());
};
```

但 release 模式 `debugPrint` 被吞——用 `print`。这样能看到 build()/layout 阶段抛的异常。

或者用 `adb logcat *:E` 只看 error 级别,但 Flutter 异常默认是 `I/flutter` 级别……

**最可靠的办法**:加一个全局 zone 包住 `runApp`,捕获所有未处理异常并 print。

### Step 3:逐个修

修完一个就 `make build-apk` + 装到 MuMu + 实测验证。

### Step 4:清理

- 删 `ai_study_screen.dart` 的 4 处 print 诊断日志(commit message 标记是临时)
- 删本 handoff 文档(或标 done)

---

## 关键文件速查

| 问题 | 文件 | 关键行 |
|------|------|--------|
| Bug 1 table 盖字 | `frontend/lib/ui/widget/markdown_view.dart` | `_buildStyleSheet` line ~220(`tableColumnWidth: IntrinsicColumnWidth()`) |
| Bug 1 调用方约束 | `frontend/lib/ui/screen/ai_study_screen.dart` | `methods` line ~621 / `commonMistakes` line ~647(Container + Column) |
| Bug 2 quiz 消失 | `frontend/lib/ui/screen/ai_study_screen.dart` | `_buildQuizSection` line ~729 / `_QuestionCard` line ~915 / `_optionTile` line ~1077 |
| Bug 3 列表消失 | `frontend/lib/ui/screen/course_detail_screen.dart` | `_buildCourseSummaryCard` line ~347 / 调用点 line ~230 |
| 已修的 RegExp bug | `frontend/lib/ui/widget/markdown_view.dart` | `_normalizeMarkdown` line ~99(`dotAll: true`) |
| 已修的 normalize | `backend/internal/ai/agent/summarizer.go` | `normalizeMarkdownInFields` line ~310 |

## 关键约束

- **不要碰 Go 后端的 `(?s)` 正则**——Go RE2 支持,运行正常。
- **MuMu 是 x86_64 模拟器**,但实际通过 `libhoudini.so` 跑 ARM 翻译。装 x86_64 apk 即可。
- **`make build-apk`** 一次出全部 ABI(arm64/arm/x86_64),`make build-apk-x64` 单 x64。
- **诊断日志用 `print` 不是 `debugPrint`**——release 模式 debugPrint 被吞。
- **课程总览的内容来源**:admin 内容管理 tab 触发生成,所有学生共享。客户端只读(`/api/v1/courses/:id/ai-summary`)。
- **客户端课程总览 model**:`frontend/lib/model/course_summary.dart`,字段含 `episodeCountAtGen`/`currentEpisodeCount` + `isStale` getter。

## 上一 session 留下的诊断日志(print)

`ai_study_screen.dart` 里 4 处(调完清掉):

```dart
// line 120 _loadQuiz
print('[ai-study] _loadQuiz ep=${widget.episode.id} → status=${resp.status} quiz=${resp.quiz == null ? "null" : "${resp.quiz!.questions.length}q"}');

// line 562 _buildSummarySection
print('[ai-study] _buildSummarySection ep=${widget.episode.id} loading=$_summaryLoading summaryNull=${_summary == null} isEmpty=${_summary?.isEmpty}');

// line 734 _buildQuizSection 入口
print('[ai-study] _buildQuizSection ep=${widget.episode.id} loading=$_quizLoading status=$_quizStatus quizNull=${_quiz == null}');

// line 754 hiding 分支
print('[ai-study] ⚠️ quiz section HIDING: status=$_quizStatus quizNull=${_quiz == null}');
```

`grep "ai-study" /tmp/adb.log` 可以看状态机走的分支。

---

## 跟本轮其他工作的关系

本轮(commit `c9e1f04` + `fc6a730` + `99b408f`)的主体改动都稳定可用:

- ✅ cache 流程重构 + WSL 9P 兜底(MuMu 上 wav cache 已能直接命中,不再每次扫描)
- ✅ UpsertSummary / parseSummaryJSON normalize(后端,修重新生成报错 + 新生成的内容干净)
- ✅ 课程总览全链路(admin 触发 + 客户端卡片,数据流通正常)
- ✅ AI 控制台内容管理 tab(删除按钮 gate + 陈旧提示)
- ✅ 决策痕迹显示课程标题

只有这 3 个前端渲染 bug 没修,跟主体改动解耦——可以独立修不影响其他部分。
