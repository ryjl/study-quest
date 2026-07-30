import 'package:flutter/material.dart';

import '../../model/exam.dart';
import '../../service/api_service.dart';
import '../../service/tv_mode.dart';
import '../../theme.dart';
import '../widget/button_3d.dart';
import '../widget/quiz_card.dart';
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
  // 填空题用 controller 持有输入(QuizReviewCard 的 fill 走 controller),onChanged 同步到
  // _answers['answer_text'] 供交卷读取。和 ai_study_screen 的 _fillControllers 同模式。
  final Map<int, TextEditingController> _fillControllers = {};
  // 交卷后整体报告。
  ExamSubmitReport? _report;
  bool _busy = false; // start / submit 进行中

  @override
  void initState() {
    super.initState();
    _loadStatus();
  }

  @override
  void dispose() {
    for (final c in _fillControllers.values) {
      c.dispose();
    }
    super.dispose();
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
    final colors = context.colors;
    return Scaffold(
      backgroundColor: colors.backgroundColor,
      appBar: AppBar(
        title: Text('${widget.courseTitle} · 课程考试'),
        backgroundColor: colors.cardColor,
        foregroundColor: colors.slate900,
        elevation: 0,
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_statusLoading) return Center(child: loadingSpinner(context));
    if (_statusError != null) {
      return errorStateBox(context, _statusError!, _loadStatus, message: '加载考试状态失败');
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
      context: context,
      icon: Icons.lock_outline_rounded,
      headline: '暂时不能考试',
      hint: reason,
      onRefresh: _loadStatus,
    );
  }

  // ── 可考,展示开考入口 ──
  Widget _buildReady() {
    final tv = TvMode.instance.isActive;
    final colors = context.colors;
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 640),
        child: Padding(
          padding: portraitAwarePadding(context),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              Icon(Icons.emoji_events_rounded, size: 64, color: colors.blue600),
              const SizedBox(height: 16),
              const Text(
                '准备好考试了吗?',
                style: TextStyle(fontSize: 24, fontWeight: FontWeight.bold),
              ),
              const SizedBox(height: 8),
              Text(
                '综合这门课的知识点出题,检验整体掌握程度。',
                textAlign: TextAlign.center,
                style: TextStyle(color: colors.textMuted, fontSize: 14),
              ),
              const SizedBox(height: 24),
              if (tv)
                Text(
                  '请用平板或手机开考(电视做题体验差)',
                  style: TextStyle(color: colors.textMuted, fontSize: 13),
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
                return Padding(
                  padding: const EdgeInsets.only(bottom: 14),
                  child: _examCard(q, index - 1, submitted),
                );
              },
        ),
      ),
    );
  }

  Widget _buildScoreHeader(ExamView exam, bool submitted) {
    final colors = context.colors;
    if (!submitted) {
      return Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: Text('共 ${exam.questions.length} 道题',
          style: TextStyle(fontSize: 15, color: colors.textMuted)),
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
        child: Text('本次考试已交卷,成绩见交卷时的报告。',
          textAlign: TextAlign.center,
          style: TextStyle(color: colors.textMuted, fontSize: 14)),
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

  // 试卷的逐题卡:复用全站统一的 QuizReviewCard(代码/视觉与 quiz/错题本/历史一致)。
  // 适配 ExamQuestion + ExamSubmitResult + 本地 _answers:
  //   - _answers[q.id] 是 Map:choice→answer_index, multi→answer_indices, fill→answer_text。
  //   - 考试题不下发正确答案(防作弊),result 只在交卷后揭示。
  Widget _examCard(ExamQuestion q, int index, bool submitted) {
    final result = _report?.results.cast<ExamSubmitResult?>().firstWhere(
          (r) => r?.questionId == q.id,
          orElse: () => null,
        );
    final userAnswer = _answers[q.id];
    final userChoice = userAnswer?['answer_index'] as int?;
    final userMulti = (userAnswer?['answer_indices'] as List?)?.cast<int>().toSet() ?? <int>{};
    final userFill = (userAnswer?['answer_text'] as String?) ?? '';
    return QuizReviewCard(
      stem: QuizStemData(
        index: index,
        type: q.type,
        stem: q.stem,
        options: q.options,
        chunkStartTime: null, // 考试题不下发 chunk 锚点,无跳转
        hasJump: false,
      ),
      verdict: QuizVerdictData(
        submitted: submitted,
        userChoiceIndex: userChoice,
        userMultiIndices: userMulti,
        userFillText: userFill,
        correctIndex: result?.correctIndex,
        correctIndices: result?.correctIndices.toSet() ?? const {},
        correctText: result?.correctText ?? '',
        explanation: result?.explanation ?? '',
        correct: result?.correct,
        partial: false,
      ),
      mode: submitted ? QuizReviewMode.submitted : QuizReviewMode.interactive,
      onPickChoice: (i) => _setChoice(q.id, i),
      onToggleMulti: (i) => _toggleMulti(q.id, i),
      // 填空题:lazy 建 controller,onChange 同步到 _answers 供交卷读取。
      fillController: q.isFill ? (_fillControllers[q.id] ??= TextEditingController()) : null,
      onFillChanged: () {
        setState(() {});
        _answers[q.id] = {'answer_text': _fillControllers[q.id]?.text ?? ''};
      },
    );
  }
}
