import 'package:flutter/material.dart';

import '../../theme.dart';
import 'markdown_view.dart';

// ─────────────────────────────────────────────────────────────────────────────
// 全站统一的「逐题阅卷讲解」组件。
//
// 历史:这套渲染逻辑(题卡外壳 + 选项 + 多选明细 + 填空回放 + 对错横幅 + 解析)原本
// 在 4 个文件里各写了一遍(wrong_book_screen / ai_study_screen / course_exam_screen
// / 历史 quiz 卡),改一处要同步多处、且视觉逐渐漂移(历史多选是红绿混显无明细、重做/
// 考试交卷只有裸解析块无对错横幅)。本组件把它们合并成一份,代码和视觉都强制统一。
//
// 适配 5 个 model:调用方各自 toStem()/toVerdict() 转成下面的标准化值对象,抹平字段名
// 差异(QuizQuestion.userAnswerIndex / ArchivedQuizQuestion.userIndex / WrongBookRedo
// 的 _answers['answer_index'] / ExamQuestion 的同构 / WrongBookItem 自测态)。
// ─────────────────────────────────────────────────────────────────────────────

/// 选项序号前缀:0→"A. "、1→"B. "... 让选项像试卷一样带字母序号,多选明细里的
/// 「A. xxx」「⚠ B 多选了」才能和选项列表对上号。一致性不变量:前缀只依赖索引、
/// 不进库、各页面天然一致。收进本组件,删掉 wrong_book/ai_study/exam 三处副本。
String _optionTag(int i) => '${String.fromCharCode(65 + i)}. ';

/// 标准化题面(纯展示数据,不携带作答/答案)。
///
/// 调用方把各自的题面 model 转成这个,抹平字段差异。verdict 为 null 时表示纯作答态
/// (interactive),不揭示答案。
class QuizStemData {
  final int index; // 题序 0-based(展示时 +1)
  final String type; // 'choice' | 'multi_choice' | 'fill'
  final String stem; // markdown(可能含表格/加粗关键词)
  final List<String> options; // choice / multi_choice 的选项
  final int? chunkStartTime; // 跳视频用;null 不显示跳转按钮
  final bool hasJump; // 是否对应明确视频片段(仅 hasJump 才给跳转按钮)

  const QuizStemData({
    required this.index,
    required this.type,
    required this.stem,
    required this.options,
    required this.chunkStartTime,
    required this.hasJump,
  });

  bool get isFill => type == 'fill';
  bool get isMultiChoice => type == 'multi_choice';
}

/// 标准化作答 + 判分。null 字段表示未作答/未揭示。
///
/// - choice:userChoiceIndex 是学生选的(0-based);correctIndex 是正确项。
/// - multi_choice:userMultiIndices 是学生选的集合;correctIndices 是正确集合。
/// - fill:userFillText 是学生填的;correctText 是标准答案。
/// - correct=null 表示未判分(interactive 态);true/false 是判定结果。partial 仅多选
///   「漏选但没多错」时为 true(展示「部分正确」横幅)。
class QuizVerdictData {
  final bool submitted; // 是否锁定只读(交卷/揭示后)
  final int? userChoiceIndex;
  final Set<int> userMultiIndices;
  final String userFillText;
  final int? correctIndex;
  final Set<int> correctIndices;
  final String correctText;
  final String explanation;
  final bool? correct;
  final bool partial;

  const QuizVerdictData({
    required this.submitted,
    this.userChoiceIndex,
    this.userMultiIndices = const {},
    this.userFillText = '',
    this.correctIndex,
    this.correctIndices = const {},
    this.correctText = '',
    this.explanation = '',
    this.correct,
    this.partial = false,
  });
}

/// 渲染模式。interactive(作答中,可点,无判分高亮) / submitted(只读,对错横幅 +
/// 多选明细 + 填空回放 + 解析)。
enum QuizReviewMode { interactive, submitted }

/// 全站统一的逐题阅卷讲解卡片。
///
/// 用法:调用方把 model 转成 [QuizStemData] + [QuizVerdictData?],按场景选 mode,
/// 透传作答回调(interactive 态)。详见本文件顶部注释。
class QuizReviewCard extends StatelessWidget {
  final QuizStemData stem;
  final QuizVerdictData? verdict; // null → interactive(纯作答,无判分)
  final QuizReviewMode mode;
  // AI 页全局文字缩放因子,传给 MarkdownView(stem / explanation)。
  final double textScale;

  // interactive 态作答回调(submitted 态忽略):
  // choice 点选项;multi toggle 选项;fill 文本变化。
  final ValueChanged<int>? onPickChoice;
  final ValueChanged<int>? onToggleMulti;
  // fill 用 controller(interactive 态持有输入;submitted 态不用,改用 verdict.userFillText)。
  final TextEditingController? fillController;
  final VoidCallback? onFillChanged;
  // 跳转视频(仅 hasJump 且 submitted 态展示——看错题时跳回去复习)。
  final VoidCallback? onJump;
  // flat=true 时渲染成「无外框」的纯内容(题序/题面/选项/横幅/解析),不套自己的 Container
  // 边框+底色。供已有自己卡片外壳的容器嵌套用(如错题本卡片:外层有自己的圆角卡 + 课程
  // chip + 掌握切换,内层用 flat 避免双层边框视觉冗余)。默认 false=自带卡片外壳。
  final bool flat;

  const QuizReviewCard({
    super.key,
    required this.stem,
    this.verdict,
    required this.mode,
    this.textScale = 1.0,
    this.onPickChoice,
    this.onToggleMulti,
    this.fillController,
    this.onFillChanged,
    this.onJump,
    this.flat = false,
  });

  bool get _submitted => mode == QuizReviewMode.submitted;

  @override
  Widget build(BuildContext context) {
    final v = verdict;
    // submitted 态下若有判分,展示对错横幅(含明细/正确答案/解析)。
    final hasVerdict = _submitted && v != null && v.correct != null;
    final content = Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        // 题序号 + 题型标签:让卷子像正式试卷。填空琥珀、多选紫、单选蓝。
        Row(children: [
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
            decoration: BoxDecoration(color: AppTheme.slate100, borderRadius: BorderRadius.circular(5)),
            child: Text('第${stem.index + 1}题', style: const TextStyle(fontSize: 10, color: AppTheme.textMuted)),
          ),
          const SizedBox(width: 6),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
            decoration: BoxDecoration(
              color: stem.isFill
                  ? const Color(0xFFFFFBEB)
                  : stem.isMultiChoice
                      ? const Color(0xFFF5F3FF)
                      : AppTheme.blue100,
              borderRadius: BorderRadius.circular(5),
            ),
            child: Text(
              stem.isFill ? '填空' : stem.isMultiChoice ? '多选' : '选择',
              style: TextStyle(
                fontSize: 10,
                color: stem.isFill
                    ? const Color(0xFF92400E)
                    : stem.isMultiChoice
                        ? const Color(0xFF6D28D9)
                        : const Color(0xFF1D4ED8),
              ),
            ),
          ),
        ]),
        const SizedBox(height: 8),
        // 题干含 markdown(表格题、加粗关键词、代码块/SVG 图),用 MarkdownView 渲染。
        MarkdownView(data: stem.stem, textScale: textScale, baseTextColor: AppTheme.slate900),
        const SizedBox(height: 10),
        // 按题型分派选项渲染:填空 → 输入框/回放;多选 → checkbox 风;单选 → radio 风。
        if (stem.isFill)
          _buildFill(_submitted, v)
        else if (stem.isMultiChoice)
          _buildMultiOptions(_submitted, v)
        else
          _buildChoiceOptions(_submitted, v),
        // 对错横幅:仅交卷/揭示态且有判分(correct != null)时显示三态横幅。
        if (hasVerdict) _buildVerdictBanner(v),
        // 复习脚注(交卷/揭示态,不依赖判分):正确答案 + multi 明细 + 解析。
        // 错题本「查看答案」时无判分(correct=null)也要展示正确答案和解析。
        if (_submitted && v != null) _buildReviewFooter(v),
        // 跳转视频:仅 hasJump=true 的题渲染,且仅在交卷态展示(看错题解析时跳回去复习)。
        if (stem.hasJump && stem.chunkStartTime != null && _submitted && onJump != null)
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              onPressed: onJump,
              icon: const Icon(Icons.play_circle_outline_rounded, size: 16, color: AppTheme.blue600),
              label: Text('跳转视频 ${_fmtJump(stem.chunkStartTime!)}',
                  style: const TextStyle(fontSize: 12, color: AppTheme.blue600)),
              style: TextButton.styleFrom(
                padding: const EdgeInsets.symmetric(horizontal: 4),
                minimumSize: const Size(0, 24),
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
            ),
          ),
      ],
    );
    // flat 模式只返回纯内容(无外框),让外层卡片壳统一管视觉;否则套自带卡片外壳。
    if (flat) return content;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppTheme.borderMuted),
      ),
      child: content,
    );
  }

  // ── 单选选项 ──
  Widget _buildChoiceOptions(bool submitted, QuizVerdictData? v) {
    return Column(children: [
      for (int i = 0; i < stem.options.length; i++) _choiceTile(i, submitted, v),
    ]);
  }

  Widget _choiceTile(int i, bool submitted, QuizVerdictData? v) {
    final isSelected = v?.userChoiceIndex == i;
    // 揭示态(submitted)就高亮正确项(绿)——不依赖 correct 判分:错题本「查看答案」
    // 时学生可能没自测,这时也要看到正确答案。
    final isCorrect = submitted && v?.correctIndex == i;
    // 学生错选:已选 + 已揭示 + 不是正确项。仅在交卷/揭示态标红。
    final isWrongPick = submitted && isSelected && !isCorrect;
    Color bg = Colors.white;
    Color border = AppTheme.borderMuted;
    if (submitted) {
      if (isCorrect) {
        bg = const Color(0xFFECFDF5);
        border = AppTheme.accentGreen;
      } else if (isWrongPick) {
        bg = const Color(0xFFFEF2F2);
        border = const Color(0xFFEF4444);
      }
    } else if (isSelected) {
      bg = const Color(0xFFF5F3FF);
      border = AppTheme.violet500;
    }
    return GestureDetector(
      onTap: submitted ? null : () => onPickChoice?.call(i),
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 9),
        decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(8), border: Border.all(color: border)),
        child: Row(children: [
          // 圆形 radio(选中填紫色 + 白勾)。
          Container(
            width: 20,
            height: 20,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              border: Border.all(color: isSelected ? AppTheme.violet500 : const Color(0xFFCBD5E1)),
              color: isSelected ? AppTheme.violet500 : Colors.transparent,
            ),
            alignment: Alignment.center,
            child: isSelected ? const Icon(Icons.check, size: 12, color: Colors.white) : null,
          ),
          const SizedBox(width: 8),
          Expanded(child: Text('${_optionTag(i)}${stem.options[i]}', style: const TextStyle(fontSize: 13, color: Color(0xFF334155)))),
          if (isCorrect) const Icon(Icons.check_circle, size: 16, color: AppTheme.accentGreen),
          if (isWrongPick) const Icon(Icons.cancel, size: 16, color: Color(0xFFEF4444)),
        ]),
      ),
    );
  }

  // ── 多选选项 ──
  // 选项区只标绿(正确项),不标红——红绿混在选项里分不清「我选的」和「对的」。
  // 学生作答的对错归属放底部 _buildMultiDetail 明细里用带颜色文字说清楚。
  Widget _buildMultiOptions(bool submitted, QuizVerdictData? v) {
    final picks = v?.userMultiIndices ?? <int>{};
    // 揭示态(submitted)就高亮正确项(绿)——不依赖 correct 判分(同 choice 逻辑)。
    final correctSet = submitted ? (v?.correctIndices ?? <int>{}) : <int>{};
    return Column(children: [
      for (int i = 0; i < stem.options.length; i++) _multiTile(i, picks, correctSet, submitted),
    ]);
  }

  Widget _multiTile(int i, Set<int> picks, Set<int> correctSet, bool submitted) {
    final isSelected = picks.contains(i);
    final isCorrect = submitted && correctSet.contains(i);
    Color bg = Colors.white;
    Color border = AppTheme.borderMuted;
    if (submitted) {
      if (isCorrect) {
        bg = const Color(0xFFECFDF5);
        border = AppTheme.accentGreen;
      }
    } else if (isSelected) {
      bg = const Color(0xFFF5F3FF);
      border = AppTheme.violet500;
    }
    return GestureDetector(
      onTap: submitted ? null : () => onToggleMulti?.call(i),
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 9),
        decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(8), border: Border.all(color: border)),
        child: Row(children: [
          // 方框 checkbox(选中填紫色 + 白勾)。
          Container(
            width: 20,
            height: 20,
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(4),
              border: Border.all(color: isSelected ? AppTheme.violet500 : const Color(0xFFCBD5E1)),
              color: isSelected ? AppTheme.violet500 : Colors.transparent,
            ),
            alignment: Alignment.center,
            child: isSelected ? const Icon(Icons.check, size: 12, color: Colors.white) : null,
          ),
          const SizedBox(width: 8),
          Expanded(child: Text('${_optionTag(i)}${stem.options[i]}', style: const TextStyle(fontSize: 13, color: Color(0xFF334155)))),
          if (isCorrect) const Icon(Icons.check_circle, size: 16, color: AppTheme.accentGreen),
        ]),
      ),
    );
  }

  // ── 填空 ──
  // interactive: TextField;submitted: Container 模拟输入框(对绿错红边)留内容只读。
  // 用 Container 而非 readOnly TextField:避免在 build 里 new TextEditingController 泄漏。
  Widget _buildFill(bool submitted, QuizVerdictData? v) {
    if (submitted) {
      final userText = v?.userFillText ?? '';
      final isCorrect = v?.correct ?? false;
      return Container(
        width: double.infinity,
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 14),
        decoration: BoxDecoration(
          color: isCorrect ? const Color(0xFFECFDF5) : const Color(0xFFFEF2F2),
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: isCorrect ? const Color(0xFF10B981) : const Color(0xFFEF4444), width: 1.5),
        ),
        child: Text(
          userText.isEmpty ? '(未作答)' : userText,
          style: TextStyle(
            fontSize: 14,
            color: isCorrect ? const Color(0xFF059669) : const Color(0xFFDC2626),
            fontWeight: FontWeight.bold,
          ),
        ),
      );
    }
    return TextField(
      controller: fillController,
      onChanged: (_) => onFillChanged?.call(),
      decoration: InputDecoration(
        hintText: '输入你的答案',
        hintStyle: const TextStyle(color: AppTheme.slate400, fontSize: 13),
        filled: true,
        fillColor: const Color(0xFFF8FAFC),
        border: OutlineInputBorder(borderRadius: BorderRadius.circular(8), borderSide: const BorderSide(color: AppTheme.borderMuted)),
        enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(8), borderSide: const BorderSide(color: AppTheme.borderMuted)),
        contentPadding: const EdgeInsets.symmetric(horizontal: 10, vertical: 10),
      ),
      style: const TextStyle(fontSize: 14, color: AppTheme.slate900),
    );
  }

  // ── 对错横幅(仅交卷/揭示态且有判分)──
  // 三态:correct 全对(绿)、partial 部分对(琥珀黄,多选漏选但没多错)、都 false 错(红)。
  // 只展示标题行(回答正确!/部分正确/答案不正确),正确答案/明细/解析放 _buildReviewFooter
  // (那里不依赖判分,错题本「查看答案」无判分时也能展示)。
  Widget _buildVerdictBanner(QuizVerdictData v) {
    final isPartial = !(v.correct ?? false) && v.partial;
    final Color bannerBg = (v.correct ?? false)
        ? const Color(0xFFECFDF5)
        : isPartial
            ? const Color(0xFFFFFBEB)
            : const Color(0xFFFEF2F2);
    final Color iconColor = (v.correct ?? false)
        ? AppTheme.accentGreen
        : isPartial
            ? const Color(0xFFF59E0B)
            : const Color(0xFFEF4444);
    final Color textColor = (v.correct ?? false)
        ? const Color(0xFF059669)
        : isPartial
            ? const Color(0xFF92400E)
            : const Color(0xFFDC2626);
    final String title = (v.correct ?? false) ? '回答正确!' : isPartial ? '部分正确' : '答案不正确';
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(color: bannerBg, borderRadius: BorderRadius.circular(8)),
      child: Row(children: [
        Icon(
          (v.correct ?? false) ? Icons.check_circle : isPartial ? Icons.error_outline : Icons.cancel,
          size: 16,
          color: iconColor,
        ),
        const SizedBox(width: 6),
        Text(title, style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: textColor)),
      ]),
    );
  }

  // ── 复习脚注(交卷/揭示态,不依赖判分)──
  // 含:multi 明细「你的选择/正确答案/多选了/漏选了」、错时正确答案、解析。
  // 单独抽出:错题本「查看答案」时无判分(correct=null),也要展示正确答案和解析;
  // 交卷态(correct 非 null)时和横幅一起出现,信息不重复(横幅只给标题,这里给明细)。
  Widget _buildReviewFooter(QuizVerdictData v) {
    final children = <Widget>[];
    // 多选题:选项区只标正确项,这里用底部明细把「你的选择/正确答案/多选/漏选」用
    // 带颜色的选项文字列清楚。
    if (stem.isMultiChoice) {
      children.add(Container(
        margin: const EdgeInsets.only(top: 8),
        padding: const EdgeInsets.all(10),
        decoration: BoxDecoration(color: const Color(0xFFF8FAFC), borderRadius: BorderRadius.circular(8)),
        child: _buildMultiDetail(v),
      ));
    }
    // 错时(或填空题)给正确答案。fill 由 _buildFill 回放了「你填的」,这里补正确答案。
    if ((!(v.correct ?? false)) && v.correctText.isNotEmpty) {
      children.add(Padding(
        padding: const EdgeInsets.only(top: 4, left: 2),
        child: Text('正确答案: ${v.correctText}',
            style: const TextStyle(fontSize: 12, color: Color(0xFF059669), fontWeight: FontWeight.bold)),
      ));
    }
    if (v.explanation.isNotEmpty) {
      children.add(Padding(
        padding: const EdgeInsets.only(top: 6),
        child: MarkdownView(data: v.explanation, textScale: textScale, baseTextColor: AppTheme.textMuted),
      ));
    }
    if (children.isEmpty) return const SizedBox.shrink();
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: children);
  }

  // 多选题底部明细:选项区干净(只标正确项),这里用带颜色的选项文字把对错归属说清楚。
  // 你选的:选对的项绿、选错的项红;正确答案:全绿;多选/漏选项橙字提示。
  Widget _buildMultiDetail(QuizVerdictData v) {
    final picks = v.userMultiIndices;
    final correctIdxs = v.correctIndices;
    final pickedSet = picks.toSet();
    final correctSet = correctIdxs.toSet();
    final wrongPicks = picks.where((i) => !correctSet.contains(i)).toList(); // 多选的
    final missed = correctIdxs.where((i) => !pickedSet.contains(i)).toList(); // 漏选的
    String label(int i) => String.fromCharCode(65 + i); // 0→A,1→B...

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (picks.isNotEmpty) ...[
          const Text('你的选择', style: TextStyle(fontSize: 11, color: AppTheme.textMuted, fontWeight: FontWeight.bold)),
          const SizedBox(height: 4),
          // 一行一项:选对绿、选错红。
          for (final i in picks)
            Text('${label(i)}. ${stem.options[i]}',
                style: TextStyle(
                    fontSize: 12,
                    color: correctSet.contains(i) ? const Color(0xFF059669) : const Color(0xFFDC2626),
                    fontWeight: FontWeight.bold)),
        ],
        const SizedBox(height: 6),
        const Text('正确答案', style: TextStyle(fontSize: 11, color: AppTheme.textMuted, fontWeight: FontWeight.bold)),
        const SizedBox(height: 4),
        for (final i in correctIdxs)
          Text('${label(i)}. ${stem.options[i]}',
              style: const TextStyle(fontSize: 12, color: Color(0xFF059669), fontWeight: FontWeight.bold)),
        if (wrongPicks.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text('⚠ ${wrongPicks.map(label).join("、")} 多选了',
              style: const TextStyle(fontSize: 11, color: Color(0xFF92400E), fontWeight: FontWeight.bold)),
        ],
        if (missed.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text('⚠ ${missed.map(label).join("、")} 漏选了',
              style: const TextStyle(fontSize: 11, color: Color(0xFF92400E), fontWeight: FontWeight.bold)),
        ],
      ],
    );
  }
}

String _fmtJump(int seconds) {
  final m = seconds ~/ 60;
  final s = seconds % 60;
  return '$m:${s.toString().padLeft(2, '0')}';
}
