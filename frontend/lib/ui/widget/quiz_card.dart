import 'package:flutter/material.dart';

import '../../model/quiz.dart';
import '../../theme.dart';
import '../widget/markdown_view.dart';

/// One question card in the AI study practice tab.
///
/// Extracted verbatim from `ai_study_screen.dart`'s `_QuestionCard` (+ its
/// private helpers `_optionTile` / `_multiOptionTile` / `_buildFillInput` /
/// `_buildVerdict`). Pure presentational consumer of [QuizQuestion] data and
/// user-interaction callbacks.
class QuestionCard extends StatelessWidget {
  final int index;
  final QuizQuestion question;
  final int? pick;
  // multi_choice:当前选中索引集合(从父级 _multiPicks[qid] 透传,null 时视为空集)。
  final Set<int>? multiPicks;
  final TextEditingController? fillController;
  final QuizAnswerResult? result;
  final ValueChanged<int> onPick;
  // 多选切换回调(点击某选项 toggle 选中态)。仅多选题用。
  final ValueChanged<int>? onMultiToggle;
  // 填空文本变化回调:触发 rebuild + 落盘草稿(选择用 onPick,填空用这个)。
  final VoidCallback onFillChanged;
  final VoidCallback? onJump;
  // 统一提交后整张卷子锁定。submitted=true 时所有题只读,
  // 逐题展示 result(若 result 还没到——比如重进已交卷的卷子而 fetch 没带结果——则不展示判分)。
  final bool submitted;
  // AI 页全局文字缩放因子,传给 MarkdownView(stem / explanation)。
  final double textScale;

  const QuestionCard({
    super.key,
    required this.index,
    required this.question,
    required this.pick,
    required this.multiPicks,
    required this.fillController,
    required this.result,
    required this.onPick,
    required this.onMultiToggle,
    required this.onFillChanged,
    required this.onJump,
    required this.submitted,
    required this.textScale,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppTheme.borderMuted),
      ),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Row(children: [
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
            decoration: BoxDecoration(color: AppTheme.slate100, borderRadius: BorderRadius.circular(5)),
            child: Text('第${index + 1}题', style: const TextStyle(fontSize: 10, color: AppTheme.textMuted)),
          ),
          const SizedBox(width: 6),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
            decoration: BoxDecoration(
              // 填空琥珀、多选紫(和 selected 主色一致)、单选蓝。
              color: question.isFill
                  ? const Color(0xFFFFFBEB)
                  : question.isMultiChoice
                      ? const Color(0xFFF5F3FF)
                      : AppTheme.blue100,
              borderRadius: BorderRadius.circular(5),
            ),
            child: Text(
              question.isFill ? '填空' : question.isMultiChoice ? '多选' : '选择',
              style: TextStyle(
                fontSize: 10,
                color: question.isFill
                    ? const Color(0xFF92400E)
                    : question.isMultiChoice
                        ? const Color(0xFF6D28D9)
                        : const Color(0xFF1D4ED8),
              ),
            ),
          ),
        ]),
        const SizedBox(height: 8),
        // 题干可能含 markdown(表格题、加粗关键词、代码块/SVG 图),用 MarkdownView 渲染。
        // 原本 w600 的加粗语义交给模型用 ** 控制;MarkdownView 文字色保持深 slate-900。
        MarkdownView(
          data: question.stem,
          textScale: textScale,
          baseTextColor: AppTheme.slate900,
        ),
        const SizedBox(height: 10),
        // 按题型分派选项渲染:填空 → 输入框;多选 → checkbox 风(可多选);单选 → radio 风。
        if (question.isFill)
          _buildFillInput(submitted)
        else if (question.isMultiChoice)
          _buildMultiOptions(submitted)
        else
          _buildChoiceOptions(submitted),
        // 判分展示:整卷已交卷,且这道题的 result 已就绪(submit-all 成功后填的)。
        // 重进已交卷的卷子时若 result 还没 fetch 回来,这里就不展示,保持中性只读态。
        if (submitted && result != null) _buildVerdict(),
        // 跳转视频:仅 hasJump=true 的题渲染(onJump 已被 caller 门控),
        // 且仅在交卷态展示(做错题看解析时跳回去复习)。
        if (onJump != null && submitted)
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              onPressed: onJump,
              icon: const Icon(Icons.play_circle_outline_rounded, size: 16, color: AppTheme.blue600),
              label: Text('跳转视频 ${_fmtJump(question.chunkStartTime!)}', style: const TextStyle(fontSize: 12, color: AppTheme.blue600)),
            ),
          ),
      ]),
    );
  }

  Widget _buildChoiceOptions(bool submitted) {
    return Column(children: [
      for (int i = 0; i < question.options.length; i++)
        _optionTile(i, submitted),
    ]);
  }

  // 多选渲染:方框 checkbox 风格(区别于单选的圆形 radio),点击 toggle 选中态。
  // 交卷后高亮:正确项绿、学生错选的红(correctIndices 之外但 multiPicks 里的)。
  // 漏选的正确项只靠 correctIndices 高亮(学生没选的不额外标红——解析里说明漏选了哪些)。
  Widget _buildMultiOptions(bool submitted) {
    final picks = multiPicks ?? <int>{};
    final hasVerdict = submitted && result != null;
    // correctSet:交卷后揭示的正确答案集合(为空表示还没结果,保持中性只读)。
    final correctSet = hasVerdict ? result!.correctIndices.toSet() : <int>{};
    return Column(children: [
      for (int i = 0; i < question.options.length; i++)
        _multiOptionTile(i, picks, correctSet, hasVerdict, submitted),
    ]);
  }

  Widget _multiOptionTile(int i, Set<int> picks, Set<int> correctSet, bool hasVerdict, bool submitted) {
    final isSelected = picks.contains(i);
    final isCorrect = hasVerdict && correctSet.contains(i);
    // 学生错选:已选 + 交卷有结果 + 不在正确集合里。
    final isWrongPick = hasVerdict && isSelected && !isCorrect;
    Color bg = Colors.white;
    Color border = AppTheme.borderMuted;
    if (submitted && hasVerdict) {
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
      onTap: submitted ? null : () => onMultiToggle?.call(i),
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 9),
        decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(8), border: Border.all(color: border)),
        child: Row(children: [
          // 方框 checkbox(区别于单选的圆形 radio),选中填紫色 + 白勾。
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
          Expanded(child: Text(question.options[i], style: const TextStyle(fontSize: 13, color: Color(0xFF334155)))),
          if (isCorrect) const Icon(Icons.check_circle, size: 16, color: AppTheme.accentGreen),
          if (isWrongPick) const Icon(Icons.cancel, size: 16, color: Color(0xFFEF4444)),
        ]),
      ),
    );
  }

  Widget _optionTile(int i, bool submitted) {
    final isSelected = pick == i;
    // result 可能为 null:整卷已交卷(submitted=true)但 result 还没回来
    // (重进一张已交卷的卷子时 GET 不带结果)。此时不渲染对错高亮,保持中性只读。
    final hasVerdict = submitted && result != null;
    final isCorrect = hasVerdict && result!.correctIndex == i;
    final isWrongPick = hasVerdict && isSelected && !isCorrect;
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
      onTap: submitted ? null : () => onPick(i),
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 9),
        decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(8), border: Border.all(color: border)),
        child: Row(children: [
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
          Expanded(child: Text(question.options[i], style: const TextStyle(fontSize: 13, color: Color(0xFF334155)))),
          if (isCorrect) const Icon(Icons.check_circle, size: 16, color: AppTheme.accentGreen),
          if (isWrongPick) const Icon(Icons.cancel, size: 16, color: Color(0xFFEF4444)),
        ]),
      ),
    );
  }

  Widget _buildFillInput(bool submitted) {
    return TextField(
      controller: fillController,
      enabled: !submitted,
      onChanged: (_) => onFillChanged(), // 触发 rebuild(已答计数) + 落盘草稿
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

  Widget _buildVerdict() {
    final r = result!;
    // 三态:correct 全对(绿)、partial 部分对(琥珀黄,多选漏选但没多错)、都 false 错(红)。
    final isPartial = !r.correct && r.partial;
    final Color bannerBg = r.correct
        ? const Color(0xFFECFDF5)
        : isPartial
            ? const Color(0xFFFFFBEB)
            : const Color(0xFFFEF2F2);
    final Color iconColor = r.correct
        ? AppTheme.accentGreen
        : isPartial
            ? const Color(0xFFF59E0B)
            : const Color(0xFFEF4444);
    final Color textColor = r.correct
        ? const Color(0xFF059669)
        : isPartial
            ? const Color(0xFF92400E)
            : const Color(0xFFDC2626);
    final String title = r.correct ? '回答正确!' : isPartial ? '部分正确' : '答案不正确';
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(color: bannerBg, borderRadius: BorderRadius.circular(8)),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Row(children: [
          Icon(
            r.correct ? Icons.check_circle : isPartial ? Icons.error_outline : Icons.cancel,
            size: 16,
            color: iconColor,
          ),
          const SizedBox(width: 6),
          Text(title, style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: textColor)),
        ]),
        // 填空题回放学生当时填的原文(优先后端回填的 user_answer_text,回退 controller 里的
        // 当前文本——刚交卷时后端回填还没 fetch 回来,controller 仍持有输入值)。答对了也展示,
        // 让学生确认自己填对了什么;漏答(空串)不展示这行。
        if (question.isFill) ...[
          const SizedBox(height: 4),
          Text('你填的: ${question.userAnswerText.isNotEmpty ? question.userAnswerText : (fillController?.text ?? '')}',
              style: const TextStyle(fontSize: 12, color: AppTheme.textMuted)),
        ],
        // 多选题:partial 时提示漏选/多选的具体数量(选项已用绿/红高亮,这里给一句文字总结)。
        if (question.isMultiChoice && isPartial) ...[
          const SizedBox(height: 4),
          Text(
            partialHint(r.missedCount, r.extraCount),
            style: const TextStyle(fontSize: 12, color: Color(0xFF92400E)),
          ),
        ],
        if (!r.correct && r.correctText.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text('正确答案: ${r.correctText}', style: const TextStyle(fontSize: 12, color: Color(0xFF334155))),
        ],
        if (r.explanation.isNotEmpty) ...[
          const SizedBox(height: 4),
          // 解析正文含 markdown(对比表格/公式),用 MarkdownView 渲染;灰色保留。
          MarkdownView(
            data: r.explanation,
            textScale: textScale,
            baseTextColor: AppTheme.textMuted,
          ),
        ],
      ]),
    );
  }
}

String _fmtJump(int seconds) {
  final m = seconds ~/ 60;
  final s = seconds % 60;
  return '$m:${s.toString().padLeft(2, '0')}';
}

// 多选题部分对的提示文案:根据漏选/多选数量给具体反馈。
// missed>0 只有漏选(没多选错项,partial 的定义);extra>0 理论上 partial 不会出现
// (有多选错项判错不判部分对),但兜底也处理,避免数字异常时空泛。
String partialHint(int missed, int extra) {
  if (missed > 0 && extra > 0) {
    return '你选对了部分,但漏选了 $missed 项、多选了 $extra 项。';
  }
  if (missed > 0) {
    return '你已经选对了部分,但漏选了 $missed 个正确选项。';
  }
  if (extra > 0) {
    return '你选对了部分,但多选了 $extra 个错误选项。';
  }
  return '你已经选对了部分答案。';
}
