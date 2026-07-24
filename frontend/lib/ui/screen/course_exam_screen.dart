import 'package:flutter/material.dart';

import '../../model/exam.dart';
import '../../service/api_service.dart';
import '../../service/tv_mode.dart';
import '../../theme.dart';
import '../widget/button_3d.dart';
import '../widget/markdown_view.dart';
import '../widget/state_widgets.dart';
import '../responsive.dart';

/// 课程考试屏(TODO.md P0)。把一门课全部学过的知识点综合出一张有针对性的
/// 试卷(后端基于 mastery 弱点从已有题库抽题,不重新生成)。
///
/// 流程:GET status(gate)→ 题库不足灰显提示 / POST start 组卷 → 逐题作答 →
/// POST submit 交卷 → 阅卷报告(逐题揭示正确答案 + 得分率)。
///
/// PAD/TV 友好(对齐 wrong_book_screen 重做卷范式):
///  - PAD 横屏用 SliverLayoutBuilder 读 constraints,卷体包 ConstrainedBox(maxWidth 800)
///  - TV 模式(TvMode.isActive)隐藏做题体(遥控器做题体验差),只提示用 PAD/手机考
///  - 复用 quiz 渲染范式(选项 Column,交卷后高亮对错)
class CourseExamScreen extends StatefulWidget {
  final int activeUserId;
  final int courseId;
  final String courseTitle;

  const CourseExamScreen({
    super.key,
    required this.activeUserId,
    required this.courseId,
    required this.courseTitle,
  });

  @override
  State<CourseExamScreen> createState() => _CourseExamScreenState();
}

class _CourseExamScreenState extends State<CourseExamScreen> {
  // null = 还没 load status;loading 时 _statusLoading=true。
  ExamStatus? _status;
  bool _statusLoading = true;
  String? _statusError;

  // 开考后的卷子(null = 未开考)。
  ExamView? _exam;
  // questionId → 作答。choice: {answer_index}, multi: {answer_indices}, fill: {answer_text}。
  final Map<int, Map<String, dynamic>> _answers = {};
  // 交卷后整体报告。
  ExamSubmitReport? _report;
  bool _busy = false; // start / submit 进行中

  @override
  void initState() {
    super.initState();
    _loadStatus();
  }

  Future<void> _loadStatus() async {
    setState(() {
      _statusLoading = true;
      _statusError = null;
    });
    try {
      final st = await ApiService.fetchExamStatus(widget.activeUserId, widget.courseId);
      if (!mounted) return;
      // 已有 active exam(可能未交卷)时直接取出来续做/复习。
      if (st.available) {
        final active = await ApiService.fetchActiveExam(widget.activeUserId, widget.courseId);
        if (!mounted) return;
        setState(() {
          _status = st;
          _exam = active;
          _statusLoading = false;
        });
      } else {
        setState(() {
          _status = st;
          _statusLoading = false;
        });
      }
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _statusError = e.toString();
        _statusLoading = false;
      });
    }
  }

  Future<void> _startExam() async {
    setState(() => _busy = true);
    try {
      final view = await ApiService.startExam(widget.activeUserId, widget.courseId);
      if (!mounted) return;
      setState(() {
        _exam = view;
        _answers.clear();
        _report = null;
        _busy = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('$e'.replaceFirst('Exception: ', ''))),
      );
    }
  }

  void _setChoice(int qid, int idx) {
    if (_report != null || _exam?.submitted == true) return; // 已交卷,锁定
    setState(() => _answers[qid] = {'answer_index': idx});
  }

  void _toggleMulti(int qid, int idx) {
    if (_report != null || _exam?.submitted == true) return;
    final cur = List<int>.from(
      (_answers[qid]?['answer_indices'] as List?)?.cast<int>() ?? const [],
    );
    if (cur.contains(idx)) {
      cur.remove(idx);
    } else {
      cur.add(idx);
    }
    setState(() => _answers[qid] = {'answer_indices': cur});
  }

  Future<void> _submit() async {
    final exam = _exam;
    if (exam == null) return;
    setState(() => _busy = true);
    try {
      final answerList = exam.questions.map((q) {
        final a = _answers[q.id] ?? {};
        return {'question_id': q.id, ...a};
      }).toList();
      final report = await ApiService.submitExam(
        activeUserId: widget.activeUserId,
        examId: exam.examId,
        answers: answerList,
      );
      if (!mounted) return;
      setState(() {
        _report = report;
        _busy = false;
      });
      // 提示错题已加入错题本(发现性:考试做错的题也进错题本)。
      final wrongCount = report.results.where((r) => !r.correct).length;
      if (wrongCount > 0) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('$wrongCount 道错题已加入错题本'),
            action: SnackBarAction(
              label: '去复习',
              onPressed: () => Navigator.of(context).maybePop(),
            ),
            duration: const Duration(seconds: 5),
          ),
        );
      }
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('$e'.replaceFirst('Exception: ', ''))),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppTheme.backgroundColor,
      appBar: AppBar(
        title: Text('${widget.courseTitle} · 课程考试'),
        backgroundColor: Colors.white,
        foregroundColor: AppTheme.slate900,
        elevation: 0,
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_statusLoading) return Center(child: loadingSpinner());
    if (_statusError != null) {
      return errorStateBox(_statusError!, _loadStatus, message: '加载考试状态失败');
    }
    // 已交卷(report 或 exam.submitted)→ 阅卷报告。
    final submitted = _report != null || _exam?.submitted == true;
    if (_exam != null) {
      return _buildExam(_exam!, submitted);
    }
    // 未开考:按 status 决定。
    final st = _status;
    if (st == null || !st.available) {
      return _buildUnavailable(st?.reason ?? '考试功能未启用');
    }
    // 可考但还没点开考。
    return _buildReady();
  }

  // ── 题库不足 / 功能未启用 ──
  Widget _buildUnavailable(String reason) {
    return emptyStateBox(
      icon: Icons.lock_outline_rounded,
      headline: '暂时不能考试',
      hint: reason,
      onRefresh: _loadStatus,
    );
  }

  // ── 可考,展示开考入口 ──
  Widget _buildReady() {
    final tv = TvMode.instance.isActive;
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 640),
        child: Padding(
          padding: portraitAwarePadding(context),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              const Icon(Icons.emoji_events_rounded, size: 64, color: AppTheme.blue600),
              const SizedBox(height: 16),
              const Text(
                '准备好考试了吗?',
                style: TextStyle(fontSize: 24, fontWeight: FontWeight.bold),
              ),
              const SizedBox(height: 8),
              const Text(
                '综合这门课的知识点出题,检验整体掌握程度。',
                textAlign: TextAlign.center,
                style: TextStyle(color: AppTheme.textMuted, fontSize: 14),
              ),
              const SizedBox(height: 24),
              if (tv)
                const Text(
                  '请用平板或手机开考(电视做题体验差)',
                  style: TextStyle(color: AppTheme.textMuted, fontSize: 13),
                )
              else
                Button3D.blue(
                  onPressed: _busy ? null : _startExam,
                  child: Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 32, vertical: 12),
                    child: _busy
                        ? const SizedBox(
                            width: 18, height: 18,
                            child: CircularProgressIndicator(color: Colors.white, strokeWidth: 2))
                        : const Text('开始考试',
                            style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold)),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }

  // ── 答题 / 阅卷 ──
  Widget _buildExam(ExamView exam, bool submitted) {
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 800),
        child: ListView.builder(
          padding: const EdgeInsets.all(16),
          itemCount: exam.questions.length + 2, // header(得分) + 题目 + 提交/完成
          itemBuilder: (context, index) {
            if (index == 0) return _buildScoreHeader(exam, submitted);
            if (index == exam.questions.length + 1) return _buildSubmitRow(exam, submitted);
            final q = exam.questions[index - 1];
            return _buildQuestionCard(q, index - 1, submitted);
          },
        ),
      ),
    );
  }

  Widget _buildScoreHeader(ExamView exam, bool submitted) {
    if (!submitted) {
      return Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: Text('共 ${exam.questions.length} 道题',
          style: const TextStyle(fontSize: 15, color: AppTheme.textMuted)),
      );
    }
    // 已交卷但拿不到 report(取 active exam 回来、只有 submitted=true 没带成绩)时,
    // 不能伪造分数——提示用户成绩在交卷时的报告里。
    if (_report == null) {
      return Container(
        width: double.infinity,
        margin: const EdgeInsets.only(bottom: 16),
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: const Color(0xFFF1F5F9),
          borderRadius: BorderRadius.circular(14),
        ),
        child: const Text('本次考试已交卷,成绩见交卷时的报告。',
          textAlign: TextAlign.center,
          style: TextStyle(color: AppTheme.textMuted, fontSize: 14)),
      );
    }
    final score = _report!.score;
    final correct = _report!.results.where((r) => r.correct).length;
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 16),
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        gradient: const LinearGradient(
          colors: [Color(0xFF3B82F6), Color(0xFF6366F1)],
          begin: Alignment.topLeft, end: Alignment.bottomRight,
        ),
        borderRadius: BorderRadius.circular(20),
      ),
      child: Column(
        children: [
          const Text('本次考试', style: TextStyle(color: Colors.white70, fontSize: 13)),
          const SizedBox(height: 4),
          Text('${(score * 100).round()} 分',
            style: const TextStyle(color: Colors.white, fontSize: 40, fontWeight: FontWeight.w900)),
          const SizedBox(height: 4),
          Text('$correct / ${exam.questions.length} 题正确',
            style: const TextStyle(color: Colors.white70, fontSize: 14)),
        ],
      ),
    );
  }

  Widget _buildSubmitRow(ExamView exam, bool submitted) {
    if (submitted) {
      return Padding(
        padding: const EdgeInsets.only(top: 16),
        child: Button3D.blue(
          onPressed: () => Navigator.of(context).pop(),
          child: const Padding(
            padding: EdgeInsets.symmetric(horizontal: 24, vertical: 10),
            child: Text('完成', style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold)),
          ),
        ),
      );
    }
    // TV 模式不做题(理论上进不到这——_buildReady 已 gate;防御性兜底)。
    return Padding(
      padding: const EdgeInsets.only(top: 16),
      child: Button3D.blue(
        onPressed: _busy ? null : _submit,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 10),
          child: _busy
            ? const SizedBox(width: 18, height: 18,
                child: CircularProgressIndicator(color: Colors.white, strokeWidth: 2))
            : const Text('提交全部', style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold)),
        ),
      ),
    );
  }

  Widget _buildQuestionCard(ExamQuestion q, int index, bool submitted) {
    final result = _report?.results.cast<ExamSubmitResult?>().firstWhere(
          (r) => r?.questionId == q.id,
          orElse: () => null,
        );
    final userAnswer = _answers[q.id];
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 14),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppTheme.borderMuted),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // 题序号 + 题型标签(对齐 quiz/重做卷样式)。
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
                color: q.isFill
                    ? const Color(0xFFFFFBEB)
                    : q.isMultiChoice ? const Color(0xFFF5F3FF) : AppTheme.blue100,
                borderRadius: BorderRadius.circular(5),
              ),
              child: Text(
                q.isFill ? '填空' : q.isMultiChoice ? '多选' : '选择',
                style: TextStyle(
                  fontSize: 10,
                  color: q.isFill
                      ? const Color(0xFF92400E)
                      : q.isMultiChoice ? const Color(0xFF6D28D9) : const Color(0xFF1D4ED8),
                ),
              ),
            ),
          ]),
          const SizedBox(height: 8),
          MarkdownView(data: q.stem, baseTextColor: AppTheme.slate900, textScale: 1.0),
          const SizedBox(height: 10),
          if (q.isMultiChoice)
            ..._buildMultiOptions(q, userAnswer, result, submitted)
          else if (q.isFill)
            _buildFillField(q, userAnswer, result, submitted)
          else
            ..._buildChoiceOptions(q, userAnswer, result, submitted),
          // 交卷后揭示解析。
          if (submitted && result != null && result.explanation.isNotEmpty) ...[
            const SizedBox(height: 8),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: const Color(0xFFF8FAFC),
                borderRadius: BorderRadius.circular(8),
              ),
              child: MarkdownView(
                data: result.explanation,
                baseTextColor: AppTheme.slate600,
                textScale: 0.95,
              ),
            ),
          ],
        ],
      ),
    );
  }

  List<Widget> _buildChoiceOptions(
    ExamQuestion q, Map<String, dynamic>? userAnswer,
    ExamSubmitResult? result, bool submitted,
  ) {
    final selectedIdx = userAnswer?['answer_index'] as int?;
    final correctIdx = result?.correctIndex;
    return List.generate(q.options.length, (i) {
      final selected = selectedIdx == i;
      final correct = submitted && correctIdx == i;
      final wrongPick = submitted && selected && !correct;
      return _optionTile(
        q.options[i],
        selected: selected,
        correct: correct,
        wrongPick: wrongPick,
        onTap: () => _setChoice(q.id, i),
      );
    });
  }

  List<Widget> _buildMultiOptions(
    ExamQuestion q, Map<String, dynamic>? userAnswer,
    ExamSubmitResult? result, bool submitted,
  ) {
    final picked = (userAnswer?['answer_indices'] as List?)?.cast<int>() ?? const [];
    final correctIdxs = result?.correctIndices ?? const [];
    // 多选选项区只标正确答案(绿),不标红——红绿混在选项里分不清「我选的」和「对的」(需求#3a)。
    // 用户的选择放底部明细用带颜色文字说清楚。
    final tiles = List.generate(q.options.length, (i) {
      final selected = picked.contains(i);
      final correct = submitted && correctIdxs.contains(i);
      return _optionTile(
        q.options[i],
        selected: selected,
        correct: correct,
        wrongPick: false,
        multi: true,
        onTap: () => _toggleMulti(q.id, i),
      );
    });
    if (submitted && result != null) {
      tiles.add(_buildMultiDetail(q.options, picked, correctIdxs));
    }
    return tiles;
  }

  // 多选题底部明细:选项区干净(只标正确项),这里用带颜色的选项文字把对错归属说清楚。
  // 你选的:选对的项绿、选错的项红;正确答案:全绿;多选/漏选项橙字提示。
  Widget _buildMultiDetail(List<String> options, List<int> picked, List<int> correctIdxs) {
    final pickedSet = picked.toSet();
    final correctSet = correctIdxs.toSet();
    final wrongPicks = picked.where((i) => !correctSet.contains(i)).toList(); // 多选的
    final missed = correctIdxs.where((i) => !pickedSet.contains(i)).toList(); // 漏选的
    String label(int i) => String.fromCharCode(65 + i); // 0→A,1→B...

    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: const Color(0xFFF8FAFC), borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (picked.isNotEmpty) ...[
            const Text('你的选择', style: TextStyle(fontSize: 11, color: AppTheme.textMuted, fontWeight: FontWeight.bold)),
            const SizedBox(height: 4),
            Wrap(
              spacing: 10, runSpacing: 4,
              children: picked.map((i) {
                final ok = correctSet.contains(i);
                return Text('${label(i)}. ${options[i]}',
                  style: TextStyle(fontSize: 12,
                    color: ok ? const Color(0xFF059669) : const Color(0xFFDC2626),
                    fontWeight: FontWeight.bold));
              }).toList(),
            ),
          ],
          const SizedBox(height: 6),
          const Text('正确答案', style: TextStyle(fontSize: 11, color: AppTheme.textMuted, fontWeight: FontWeight.bold)),
          const SizedBox(height: 4),
          Wrap(
            spacing: 10, runSpacing: 4,
            children: correctIdxs.map((i) => Text('${label(i)}. ${options[i]}',
              style: const TextStyle(fontSize: 12, color: Color(0xFF059669), fontWeight: FontWeight.bold))).toList(),
          ),
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
      ),
    );
  }

  Widget _buildFillField(
    ExamQuestion q, Map<String, dynamic>? userAnswer,
    ExamSubmitResult? result, bool submitted,
  ) {
    // 填空题交卷后:输入框变红/绿(留内容只读),下面给正确答案(错时)。之前提交后丢掉
    // 用户填的,只显示正确答案,用户不知道自己刚填了什么(需求#3b)。
    if (submitted) {
      final userText = (userAnswer?['answer_text'] as String?) ?? '';
      final isCorrect = result?.correct ?? false;
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          TextField(
            controller: TextEditingController(text: userText),
            readOnly: true,
            enableInteractiveSelection: false,
            decoration: InputDecoration(
              filled: true,
              fillColor: isCorrect ? const Color(0xFFECFDF5) : const Color(0xFFFEF2F2),
              enabledBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
                borderSide: BorderSide(color: isCorrect ? const Color(0xFF10B981) : const Color(0xFFEF4444)),
              ),
              focusedBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
                borderSide: BorderSide(color: isCorrect ? const Color(0xFF10B981) : const Color(0xFFEF4444)),
              ),
            ),
            style: TextStyle(
              fontSize: 14,
              color: isCorrect ? const Color(0xFF059669) : const Color(0xFFDC2626),
              fontWeight: FontWeight.bold,
            ),
          ),
          if (!isCorrect && result?.correctText.isNotEmpty == true) ...[
            const SizedBox(height: 6),
            Text('正确答案:${result!.correctText}',
              style: const TextStyle(color: Color(0xFF10B981), fontWeight: FontWeight.bold)),
          ],
        ],
      );
    }
    return TextField(
      decoration: const InputDecoration(border: OutlineInputBorder(), hintText: '输入答案'),
      onChanged: (v) => _answers[q.id] = {'answer_text': v},
    );
  }

  Widget _optionTile(String label, {
    required bool selected, required bool correct, required bool wrongPick,
    bool multi = false, required VoidCallback onTap,
  }) {
    Color bg = Colors.white;
    Color border = AppTheme.borderMuted;
    Color iconColor = AppTheme.textMuted;
    IconData icon = multi ? Icons.check_box_outline_blank : Icons.radio_button_unchecked;
    if (selected) {
      bg = const Color(0xFFEFF6FF);
      border = AppTheme.blue600;
      icon = multi ? Icons.check_box : Icons.radio_button_checked;
      iconColor = AppTheme.blue600;
    }
    if (correct) {
      bg = const Color(0xFFD1FAE5);
      border = const Color(0xFF10B981);
      icon = Icons.check_circle;
      iconColor = const Color(0xFF10B981);
    }
    if (wrongPick) {
      bg = const Color(0xFFFEE2E2);
      border = Colors.redAccent;
      icon = Icons.cancel;
      iconColor = Colors.redAccent;
    }
    return GestureDetector(
      onTap: onTap,
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 9),
        decoration: BoxDecoration(
          color: bg, borderRadius: BorderRadius.circular(8),
          border: Border.all(color: border),
        ),
        child: Row(
          children: [
            Icon(icon, size: 18, color: iconColor),
            const SizedBox(width: 8),
            Expanded(child: Text(label, style: const TextStyle(fontSize: 14))),
          ],
        ),
      ),
    );
  }
}
