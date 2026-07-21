import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:markdown/markdown.dart' as md;
import 'package:flutter_svg/flutter_svg.dart';

import '../../theme.dart';

/// MarkdownView —— AI 文本统一渲染入口。
///
/// ## 用途
/// 把后端 / 模型返回的 markdown 文本渲染成 Flutter widget 树,覆盖:
///   - 普通 markdown:加粗 / 列表 / GFM 表格 / 代码块 / 引用块
///   - 字号缩放:[textScale] 会乘到所有段落、列表、表头、表格单元格的 fontSize
///     上(AI 页传入全局的 [textScale],用户可在设置里调大调小)。
///   - 允许覆盖基础文字颜色(卡片背景不同时,深底卡可以用 [baseTextColor])。
///
/// ## SVG 渲染策略
/// 模型生成的 markdown 里可能包含形如:
///   ```svg
///   <svg>...</svg>
///   ```
/// 的围栏代码块。我们在 [MarkdownBody.builders] 里注册一个针对 `'pre'` 元素的
/// [MarkdownElementBuilder]([_SvgCodeInterceptor]):
///   - 当 `pre > code` 的 class 为 `language-svg` 时,取出 SVG 源码,用
///     [SvgPicture.string] 渲染成图(带 padding、居中、宽度撑满父容器)。
///   - 其它语言的代码块(```bash / ```dart / …)以及无语言标记的代码块,
///     一律返回 null,交给 flutter_markdown 默认渲染(等宽字体 + 灰底圆角框)。
///
/// ## 降级方案
/// 模型有时输出的 SVG 带语法错误(标签未闭合、用了非 SVG 的元素等)。本组件用
/// flutter_svg 2.x 的 [SvgPicture.errorBuilder] 回调做优雅降级:
///   - 解析失败时,在等宽字体的代码块里直接显示原始 SVG 源码,
///   - 并在上方加一行小灰字「(图表渲染失败,显示源码)」,
///   这样用户至少能看到内容、且布局不会塌。我们不会做额外的预校验(同步 parse
///   两次成本高、收益小),onError 回调足够覆盖真实失败场景。
class MarkdownView extends StatelessWidget {
  /// markdown 文本。
  final String data;

  /// 字号缩放系数,默认 1.0。AI 页一般传 [UiPrefs.instance.aiTextScale]。
  final double textScale;

  /// 基础正文颜色,默认 slate-700 (#334155)。卡片背景不同时调用方可覆盖。
  final Color? baseTextColor;

  const MarkdownView({
    super.key,
    required this.data,
    this.textScale = 1.0,
    this.baseTextColor,
  });

  // 设计 token。提到 widget 顶层 const 是为了 styleSheet 闭包里方便引用。
  static const Color _defaultTextColor = Color(0xFF334155); // Slate-700
  static const Color _tableHeadBg = AppTheme.slate100; // Slate-100
  static const Color _tableBorder = Color(0xFFCBD5E1); // Slate-300
  static const Color _codeBlockBg = AppTheme.slate100; // Slate-100
  static const Color _codeBlockText = AppTheme.slate900; // Slate-900
  static const Color _mutedText = AppTheme.textMuted; // Slate-500
  static const Color _inlineCodeBg = AppTheme.borderMuted; // Slate-200

  @override
  Widget build(BuildContext context) {
    final Color textColor = baseTextColor ?? _defaultTextColor;
    final MarkdownStyleSheet sheet = _buildStyleSheet(context, textColor);

    return MarkdownBody(
      data: _normalizeMarkdown(data),
      extensionSet: md.ExtensionSet.gitHubFlavored,
      softLineBreak: true,
      styleSheet: sheet,
      builders: {
        // pre 元素是围栏代码块的块级容器。在此拦截 ```svg 块;
        // 非语言的代码块返回 null 走默认渲染。
        'pre': _SvgCodeInterceptor(textColor: textColor),
      },
    );
  }

  /// 预处理模型返回的 markdown,修两类常见脏数据(已确认在生产 DB 里出现):
  ///
  /// 1. **字面量 `\n` 没转成真实换行**:LLM 在 JSON 字符串值里输出表格时,
  ///    有时会把 `\n` 当成 2 字符的字面量(backslash + n)而不是换行转义。
  ///    后端 json.Unmarshal 不会"再解一次"——它把 `\n` 当普通字符留在字符串里。
  ///    结果:GFM 表格语法 `| a |\n|---|` 在客户端是一行,markdown 不识别为表格。
  ///    修法:把字面量 `\n`(以及 `\r`)替换成真实换行。只针对**未被围栏代码块包裹**
  ///    的区域——代码块里的 `\n` 应该保留(虽然 SVG 里通常也没字面量 \n)。
  ///
  /// 2. **裸 `<svg>...</svg>` 没加围栏**:prompt 要求模型把 SVG 放在 ``` ```svg ```
  ///    围栏里,但模型有时直接吐裸 SVG。markdown 把裸 SVG 当内联 HTML,绝大多数
  ///    markdown 解析器(含 GFM)会转义掉 `<` `>`,渲染成 `<svg ...>` 文本。
  ///    修法:检测裸 SVG(没被 ``` ```svg ``` 包裹的 `<svg ... </svg>`),自动补上
  ///    围栏,让 _SvgCodeInterceptor 能正常拦截渲染。
  static String _normalizeMarkdown(String input) {
    if (input.isEmpty) return input;
    String s = input;

    // 先把已有的 ``` ... ``` 围栏代码块整体保护起来(不修改里面的字面量 \n)。
    // 简化处理:按 ``` 分段,奇数段(代码块内容)跳过,偶数段(普通 markdown)做 normalize。
    final parts = s.split('```');
    final buf = StringBuffer();
    for (var i = 0; i < parts.length; i++) {
      if (i.isOdd) {
        // 代码块内容:原样保留(前后补回 ```)。注意 SVG 围栏块本身就在这里,
        // 不需要再处理。
        buf.write('```');
        buf.write(parts[i]);
        buf.write('```');
      } else {
        // 普通 markdown 区段:做两步 normalize。
        String section = parts[i];
        // (a) 字面量 \n / \r / \t → 真实字符
        section = section.replaceAll('\\n', '\n').replaceAll('\\r', '').replaceAll('\\t', '\t');
        // (b) 裸 <svg ...>...</svg> 补围栏。非贪婪匹配跨行 SVG。
        //     注意:Dart RegExp(ES 风格)不支持 (?s) 内联 flag——会抛 FormatException。
        //     要用命名参数 dotAll: true。这个 bug 之前的版本让整页 markdown 渲染失败
        //     (build() 抛异常被框架吞,UI 显示空白)。
        final svgPattern = RegExp(r'<svg\b.*?</svg>', dotAll: true);
        section = section.replaceAllMapped(svgPattern, (m) {
          final svg = m.group(0)!;
          // 已经在围栏里的(理论上不会到这里,因为我们跳过了代码块段)不重复包。
          return '\n```svg\n$svg\n```\n';
        });
        buf.write(section);
      }
    }
    return buf.toString();
  }

  /// 构造 [MarkdownStyleSheet]。以当前 Theme 为底,叠加项目设计 token 与
  /// [textScale] 缩放。表格用 [IntrinsicColumnWidth](每列按内容自适应宽度),
  /// 配合 build() 里注册的 _TableScrollWrapper——超宽表格会横向滚动,而不是
  /// 溢出盖到相邻文字。
  MarkdownStyleSheet _buildStyleSheet(BuildContext context, Color textColor) {
    final double scale = textScale <= 0 ? 1.0 : textScale;
    final base = MarkdownStyleSheet.fromTheme(Theme.of(context));

    return base.copyWith(
      p: base.p?.copyWith(
        fontSize: (base.p?.fontSize ?? 16) * scale,
        height: 1.55,
        color: textColor,
      ),
      pPadding: const EdgeInsets.symmetric(vertical: 4),
      h1: base.h1?.copyWith(
        fontSize: (base.h1?.fontSize ?? 26) * scale,
        color: textColor,
        fontWeight: FontWeight.w700,
      ),
      h2: base.h2?.copyWith(
        fontSize: (base.h2?.fontSize ?? 22) * scale,
        color: textColor,
        fontWeight: FontWeight.w700,
      ),
      h3: base.h3?.copyWith(
        fontSize: (base.h3?.fontSize ?? 20) * scale,
        color: textColor,
        fontWeight: FontWeight.w600,
      ),
      h4: base.h4?.copyWith(
        fontSize: (base.h4?.fontSize ?? 18) * scale,
        color: textColor,
        fontWeight: FontWeight.w600,
      ),
      h5: base.h5?.copyWith(
        fontSize: (base.h5?.fontSize ?? 16) * scale,
        color: textColor,
        fontWeight: FontWeight.w600,
      ),
      h6: base.h6?.copyWith(
        fontSize: (base.h6?.fontSize ?? 14) * scale,
        color: _mutedText,
        fontWeight: FontWeight.w600,
      ),
      strong: base.strong?.copyWith(
        color: textColor,
        fontWeight: FontWeight.w700,
      ),
      em: base.em?.copyWith(color: textColor),
      a: const TextStyle(color: AppTheme.blue600), // Blue-600
      listBullet: TextStyle(
        fontSize: 16 * scale,
        color: _mutedText,
      ),
      blockquote: TextStyle(
        color: _mutedText,
        fontSize: (base.blockquote?.fontSize ?? 15) * scale,
      ),
      blockquoteDecoration: BoxDecoration(
        color: _tableHeadBg,
        borderRadius: const BorderRadius.all(Radius.circular(6)),
        border: Border(
          left: BorderSide(color: _tableBorder, width: 3),
        ),
      ),
      blockquotePadding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
      code: TextStyle(
        fontFamily: 'monospace',
        fontFamilyFallback: const ['RobotoMono', 'Courier New'],
        fontSize: (base.code?.fontSize ?? 14) * scale,
        color: _codeBlockText,
        backgroundColor: _inlineCodeBg,
      ),
      codeblockDecoration: BoxDecoration(
        color: _codeBlockBg,
        borderRadius: const BorderRadius.all(Radius.circular(8)),
        border: Border.all(color: _tableBorder, width: 1),
      ),
      codeblockPadding: const EdgeInsets.all(12),
      // —— 表格 ——
      tableHead: TextStyle(
        fontWeight: FontWeight.w600,
        color: textColor,
        fontSize: 14 * scale,
      ),
      tableHeadAlign: TextAlign.left,
      tableBody: TextStyle(
        color: textColor,
        fontSize: 14 * scale,
      ),
      tableCellsPadding: const EdgeInsets.fromLTRB(8, 6, 8, 6),
      // IntrinsicColumnWidth:每列按内容自适应宽度。flutter_markdown 0.7 会对
      // IntrinsicColumnWidth 的表格自动套一层横向 SingleChildScrollView(见
      // builder.dart 515-535),所以理论上不该盖字。如果仍盖字,问题在调用方给的
      // maxWidth 约束——见 _PointItem 的分流修复。
      tableColumnWidth: const IntrinsicColumnWidth(),
      // TableBorder.all 没有 borderRadius 参数(Flutter 的 Table 边线本身不
      // 支持圆角),这里按默认 markdown 表格风格,留细灰边框即可。
      tableBorder: TableBorder.all(color: _tableBorder, width: 1),
      tablePadding: const EdgeInsets.only(top: 4, bottom: 4),
    );
  }
}

/// 拦截 `'pre'` 元素:仅当内部 code 块的语言是 svg 时把它渲染成图,
/// 其它代码块自己渲染成带 codeblockDecoration 样式的代码块。
///
/// 注意:不能对非 svg 返回 null 期望"走默认渲染"。flutter_markdown 0.7 的
/// builder.dart 在 visitElementAfterWithContext 返回 null 时,只用 defaultChild()
/// 把已渲染的 inline code 子节点塞进一个裸 Column —— 它**不会**重新套上
/// styleSheet 的 codeblockDecoration(背景/边框/padding),因为注册了 'pre' builder
/// 后,默认的 pre 装饰逻辑被绕过了。结果是非 svg 代码块会丢掉背景色和边框。
/// 所以这里非 svg 时自己构造一个和 styleSheet 一致的装饰容器。
class _SvgCodeInterceptor extends MarkdownElementBuilder {
  _SvgCodeInterceptor({required this.textColor});

  /// 解析失败降级时显示源码用的文字颜色。
  final Color textColor;

  @override
  bool isBlockElement() => true;

  @override
  Widget? visitElementAfterWithContext(
    BuildContext context,
    md.Element element,
    TextStyle? preferredStyle,
    TextStyle? parentStyle,
  ) {
    final svgSource = _extractSvg(element);
    if (svgSource != null) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: _SvgView(
          svgSource: svgSource,
          textColor: textColor,
        ),
      );
    }

    // 非 svg 代码块:自己渲染成带样式的代码块(复用 MarkdownView 的设计 token)。
    // 取 code 元素的纯文本作为代码内容。
    final code = _extractCodeText(element);
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(vertical: 8),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: MarkdownView._codeBlockBg,
        borderRadius: const BorderRadius.all(Radius.circular(8)),
        border: Border.all(color: MarkdownView._tableBorder, width: 1),
      ),
      child: SelectableText(
        code,
        style: TextStyle(
          fontFamily: 'monospace',
          fontFamilyFallback: const ['RobotoMono', 'Courier New'],
          fontSize: 14,
          color: MarkdownView._codeBlockText,
          height: 1.4,
        ),
      ),
    );
  }

  /// 取 pre > code 的纯文本内容(用于非 svg 代码块渲染)。
  String _extractCodeText(md.Element element) {
    md.Element? codeEl;
    if (element.tag == 'code') {
      codeEl = element;
    } else {
      for (final child in element.children ?? const <md.Node>[]) {
        if (child is md.Element && child.tag == 'code') {
          codeEl = child;
          break;
        }
      }
    }
    return codeEl?.textContent ?? '';
  }

  /// 判断 [element](应为 `pre > code`)是否是 svg 代码块,是则返回其文本内容。
  /// markdown 包把围栏语言写到 code 元素的 `class="language-svg"` 上(见
  /// markdown-7.x fenced_code_block_syntax.dart:50)。返回 null 表示非 svg。
  String? _extractSvg(md.Element element) {
    // pre 直接子级应为单个 code 元素。容错:遍历找第一个 code 元素。
    md.Element? codeEl;
    if (element.tag == 'code') {
      codeEl = element;
    } else {
      for (final child in element.children ?? const <md.Node>[]) {
        if (child is md.Element && child.tag == 'code') {
          codeEl = child;
          break;
        }
      }
    }
    if (codeEl == null) return null;

    final langClass = codeEl.attributes['class'] ?? '';
    // 形如 "language-svg";也兼容手写的 "language-svg " / 大小写差异。
    final lang = langClass
        .split(' ')
        .map((s) => s.trim())
        .firstWhere(
          (s) => s.isNotEmpty,
          orElse: () => '',
        );
    final normalized =
        lang.toLowerCase().replaceAll('language-', '').trim();
    if (normalized != 'svg' && lang.toLowerCase() != 'language-svg') {
      return null;
    }

    final src = codeEl.textContent;
    // 简单校验一下确实像 svg(模型偶尔会把空代码块或纯文本标成 svg)。
    if (!src.contains('<svg') && !src.contains('<SVG')) {
      return null;
    }
    return src;
  }
}

/// 渲染单张 SVG 图,带错误降级。
///
/// flutter_svg 2.x 的 [SvgPicture.errorBuilder] 在解析失败时被调用——我们用它
/// 返回一个等宽源码块 + 一行小灰字提示。同时这层包成 StatefulWidget,方便在
/// 解析异常(异步解析阶段被 swallow 的特殊情况)时也能 setState 切到降级态。
class _SvgView extends StatefulWidget {
  const _SvgView({required this.svgSource, required this.textColor});

  final String svgSource;
  final Color textColor;

  @override
  State<_SvgView> createState() => _SvgViewState();
}

class _SvgViewState extends State<_SvgView> {
  bool _failed = false;

  @override
  Widget build(BuildContext context) {
    if (_failed) {
      return _fallback();
    }

    return ClipRect(
      child: SvgPicture.string(
        widget.svgSource,
        // 保持无 width/height 约束 —— SVG 自身必须带 width/height/viewBox 属性
        // (prompt 已强制),让 flutter_svg 按 intrinsic size 测量。曾经试过加
        // width: double.infinity,被 markdown 表格的 IntrinsicColumnWidth 测出
        // 无限大 → 布局 NaN → 子节点被 ListView 丢弃甚至 C++ 崩溃(详见
        // docs/frontend_render_fix_summary.md),绝不能回退。
        // BoxFit.fitWidth:按父容器宽度等比缩放(比 contain 更适合"全宽图表"),
        // 仍依赖 SVG 自身的 width/viewBox 做 intrinsic 测量。
        fit: BoxFit.fitWidth,
        alignment: Alignment.center,
        errorBuilder: (context, error, stackTrace) {
          // 渲染失败:切到降级态(下一帧重建)。
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (mounted && !_failed) {
              setState(() => _failed = true);
            }
          });
          return _fallback();
        },
      ),
    );
  }

  /// 降级视图:一行小灰字提示 + 等宽源码块。
  Widget _fallback() {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppTheme.slate100, // Slate-100
        borderRadius: const BorderRadius.all(Radius.circular(8)),
        border: Border.all(color: AppTheme.borderMuted, width: 1),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            '(图表渲染失败,显示源码)',
            style: TextStyle(
              fontSize: 12,
              color: AppTheme.textMuted, // Slate-500
            ),
          ),
          const SizedBox(height: 6),
          SelectableText(
            widget.svgSource,
            style: TextStyle(
              fontFamily: 'monospace',
              fontFamilyFallback: const ['RobotoMono', 'Courier New'],
              fontSize: 12,
              color: AppTheme.slate900, // Slate-900
              height: 1.4,
            ),
          ),
        ],
      ),
    );
  }
}
