import 'dart:async';
import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../model/quiz.dart';
import '../../service/api_service.dart';

// AiStudyScreen — the Phase C AI learning page. Two sections:
//   1. AI summary (headline / key points / concepts / takeaway) — read from
//      /ai-summary. The LLM's agent_feedback (study advice) shows here too.
//   2. Practice tab — fetch the quiz (lazily generated, polls while generating),
//      answer choice/fill questions, see correctness + explanation + jump to
//      the video timestamp, redo, or regenerate (换题).
//
// Navigation: pushed from course_detail_screen's "AI 重点总结" button and from
// the player. Pops a JumpRequest when the user taps a "[跳转 12:38]" link so the
// player can seek — the caller (player) handles it via .then().
//
// The generation flow is the observable centerpiece: first GET returns
// status=generating (the backend enqueued a per-user quiz job), so we poll every
// few seconds until ready. This is the "lazy generation" UX decided in planning.

class AiStudyScreen extends StatefulWidget {
  final int activeUserId;
  final Episode episode;

  const AiStudyScreen({super.key, required this.activeUserId, required this.episode});

  @override
  State<AiStudyScreen> createState() => _AiStudyScreenState();
}

class _AiStudyScreenState extends State<AiStudyScreen> {
  // Summary.
  EpisodeSummary? _summary;
  bool _summaryLoading = true;

  // Quiz state machine: loading → generating/ready/unavailable → answered.
  QuizStatus? _quizStatus;
  QuizView? _quiz;
  bool _quizLoading = true;
  Timer? _pollTimer;

  // Per-question answer state (local until submit; holds the user's selection
  // or text input, and the result once submitted).
  final Map<int, int> _choicePicks = {}; // questionId → selected option index
  final Map<int, TextEditingController> _fillControllers = {};
  final Map<int, QuizAnswerResult> _results = {}; // questionId → verdict

  @override
  void initState() {
    super.initState();
    _loadSummary();
    _loadQuiz();
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    for (final c in _fillControllers.values) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _loadSummary() async {
    try {
      final s = await ApiService.fetchEpisodeSummary(widget.activeUserId, widget.episode.id);
      if (mounted) setState(() => _summary = s);
    } catch (_) {
      // best-effort; the card just stays hidden
    } finally {
      if (mounted) setState(() => _summaryLoading = false);
    }
  }

  Future<void> _loadQuiz() async {
    try {
      final resp = await ApiService.fetchEpisodeQuiz(widget.activeUserId, widget.episode.id);
      if (!mounted) return;
      setState(() {
        _quizStatus = resp.status;
        _quiz = resp.quiz;
        _quizLoading = false;
      });
      // If generating, poll until ready (the backend enqueued a per-user job on
      // first GET; it takes a few seconds for the ReAct loop to finish).
      if (resp.status == QuizStatus.generating) {
        _startPolling();
      }
    } catch (_) {
      if (mounted) setState(() => _quizLoading = false);
    }
  }

  void _startPolling() {
    _pollTimer?.cancel();
    _pollTimer = Timer.periodic(const Duration(seconds: 3), (_) async {
      try {
        final resp = await ApiService.fetchEpisodeQuiz(widget.activeUserId, widget.episode.id);
        if (!mounted) return;
        if (resp.status == QuizStatus.ready) {
          _pollTimer?.cancel();
          setState(() {
            _quizStatus = resp.status;
            _quiz = resp.quiz;
          });
        }
      } catch (_) {
        // keep polling on transient errors
      }
    });
  }

  Future<void> _submit(int questionId, QuizQuestion q) async {
    final int? choiceIndex = _choicePicks[questionId];
    final String? fillText = q.isFill ? _fillControllers[questionId]?.text : null;
    try {
      final result = await ApiService.submitQuizAnswer(
        activeUserId: widget.activeUserId,
        episodeId: widget.episode.id,
        questionId: questionId,
        answerIndex: q.isFill ? null : choiceIndex,
        answerText: q.isFill ? fillText : null,
      );
      if (mounted) setState(() => _results[questionId] = result);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('提交失败: $e')));
      }
    }
  }

  Future<void> _regenerate() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('换一套题?'),
        content: const Text('将基于你的最新学习情况重新生成一套题(旧题会被替换)。'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(c, false), child: const Text('取消')),
          TextButton(onPressed: () => Navigator.pop(c, true), child: const Text('换题')),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await ApiService.regenerateQuiz(widget.activeUserId, widget.episode.id);
      if (!mounted) return;
      setState(() {
        _quizStatus = QuizStatus.generating;
        _quiz = null;
        _results.clear();
        _choicePicks.clear();
      });
      _startPolling();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('换题失败: $e')));
      }
    }
  }

  void _jumpTo(int seconds) {
    // Pop a JumpRequest so the player (caller) can seek. The course_detail entry
    // point ignores it (no player open).
    Navigator.of(context).pop(JumpRequest(Duration(seconds: seconds)));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: const Color(0xFFF8FAFC),
      appBar: AppBar(
        backgroundColor: Colors.white,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_rounded, color: Color(0xFF64748B)),
          onPressed: () => Navigator.of(context).pop(),
        ),
        title: Text(
          widget.episode.title,
          style: const TextStyle(color: Color(0xFF0F172A), fontSize: 16, fontWeight: FontWeight.w600),
        ),
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          _buildSummarySection(),
          const SizedBox(height: 16),
          _buildAgentFeedbackCard(),
          const SizedBox(height: 16),
          _buildQuizSection(),
        ],
      ),
    );
  }

  // --- Summary card ---
  Widget _buildSummarySection() {
    if (_summaryLoading) {
      return const _Card(child: Center(child: Padding(padding: EdgeInsets.all(24), child: CircularProgressIndicator())));
    }
    final s = _summary;
    if (s == null || s.isEmpty) {
      return const SizedBox.shrink(); // no summary → hide, AI add-on absence is normal
    }
    return _Card(
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        const Row(children: [
          Icon(Icons.auto_awesome_rounded, size: 16, color: Color(0xFF8B5CF6)),
          SizedBox(width: 6),
          Text('AI 重点总结', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700, color: Color(0xFF0F172A))),
        ]),
        if (s.headline.isNotEmpty) ...[
          const SizedBox(height: 10),
          Text(s.headline, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w600, color: Color(0xFF1E293B))),
        ],
        if (s.keyPoints.isNotEmpty) ...[
          const SizedBox(height: 10),
          ...s.keyPoints.map((p) => Padding(
                padding: const EdgeInsets.only(bottom: 4),
                child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  const Text('· ', style: TextStyle(color: Color(0xFF8B5CF6), fontWeight: FontWeight.bold)),
                  Expanded(child: Text(p, style: const TextStyle(fontSize: 13, color: Color(0xFF334155)))),
                ]),
              )),
        ],
        if (s.concepts.isNotEmpty) ...[
          const SizedBox(height: 10),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: s.concepts
                .map((c) => Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                      decoration: BoxDecoration(color: const Color(0xFFEFF6FF), borderRadius: BorderRadius.circular(6)),
                      child: Text(c, style: const TextStyle(fontSize: 11, color: Color(0xFF1D4ED8))),
                    ))
                .toList(),
          ),
        ],
        if (s.takeaway.isNotEmpty) ...[
          const SizedBox(height: 10),
          Container(
            padding: const EdgeInsets.all(8),
            decoration: BoxDecoration(color: const Color(0xFFFFFBEB), borderRadius: BorderRadius.circular(8)),
            child: Text(s.takeaway, style: const TextStyle(fontSize: 12, color: Color(0xFF92400E))),
          ),
        ],
      ]),
    );
  }

  // --- Agent feedback (study advice) ---
  Widget _buildAgentFeedbackCard() {
    final feedback = _quiz?.agentFeedback ?? '';
    if (feedback.isEmpty) return const SizedBox.shrink();
    return _Card(
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        const Row(children: [
          Icon(Icons.lightbulb_outline_rounded, size: 16, color: Color(0xFFF59E0B)),
          SizedBox(width: 6),
          Text('AI 学习建议', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w700, color: Color(0xFF0F172A))),
        ]),
        const SizedBox(height: 8),
        Text(feedback, style: const TextStyle(fontSize: 13, color: Color(0xFF334155), height: 1.5)),
      ]),
    );
  }

  // --- Quiz section ---
  Widget _buildQuizSection() {
    if (_quizLoading) {
      return const _Card(child: Padding(padding: EdgeInsets.all(24), child: Center(child: CircularProgressIndicator())));
    }
    if (_quizStatus == QuizStatus.generating) {
      return const _Card(
        child: Padding(
          padding: EdgeInsets.all(32),
          child: Column(children: [
            CircularProgressIndicator(),
            SizedBox(height: 12),
            Text('正在为你生成练习…', style: TextStyle(fontSize: 14, color: Color(0xFF64748B))),
            SizedBox(height: 4),
            Text('AI 正在分析这节课内容并针对你的学习情况出题', style: TextStyle(fontSize: 11, color: Color(0xFF94A3B8))),
          ]),
        ),
      );
    }
    if (_quizStatus != QuizStatus.ready || _quiz == null) {
      // unavailable — AI off or no source material. Hide quietly (add-on layer).
      return const SizedBox.shrink();
    }
    final questions = _quiz!.questions;
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Padding(
        padding: const EdgeInsets.only(left: 4, bottom: 8),
        child: Row(children: [
          const Text('练习', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700, color: Color(0xFF0F172A))),
          const SizedBox(width: 8),
          Text('${_quiz!.answeredCount}/${questions.length} 已答', style: const TextStyle(fontSize: 12, color: Color(0xFF94A3B8))),
          const Spacer(),
          TextButton.icon(
            onPressed: _regenerate,
            icon: const Icon(Icons.refresh_rounded, size: 14),
            label: const Text('换题', style: TextStyle(fontSize: 12)),
            style: TextButton.styleFrom(foregroundColor: const Color(0xFF8B5CF6)),
          ),
        ]),
      ),
      ...questions.asMap().entries.map((e) => Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: _QuestionCard(
              index: e.key,
              question: e.value,
              pick: _choicePicks[e.value.id],
              fillController: e.value.isFill ? (_fillControllers[e.value.id] ??= TextEditingController()) : null,
              result: _results[e.value.id],
              onPick: (i) => setState(() => _choicePicks[e.value.id] = i),
              onSubmit: () => _submit(e.value.id, e.value),
              onJump: e.value.chunkStartTime == null ? null : () => _jumpTo(e.value.chunkStartTime!),
            ),
          )),
    ]);
  }
}

// --- Reusable card container ---
class _Card extends StatelessWidget {
  final Widget child;
  const _Card({required this.child});
  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: const Color(0xFFE2E8F0)),
      ),
      child: child,
    );
  }
}

// --- One question card ---
class _QuestionCard extends StatelessWidget {
  final int index;
  final QuizQuestion question;
  final int? pick;
  final TextEditingController? fillController;
  final QuizAnswerResult? result;
  final ValueChanged<int> onPick;
  final VoidCallback onSubmit;
  final VoidCallback? onJump;

  const _QuestionCard({
    required this.index,
    required this.question,
    required this.pick,
    required this.fillController,
    required this.result,
    required this.onPick,
    required this.onSubmit,
    required this.onJump,
  });

  @override
  Widget build(BuildContext context) {
    final submitted = result != null;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: const Color(0xFFE2E8F0)),
      ),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Row(children: [
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
            decoration: BoxDecoration(color: const Color(0xFFF1F5F9), borderRadius: BorderRadius.circular(5)),
            child: Text('第${index + 1}题', style: const TextStyle(fontSize: 10, color: Color(0xFF64748B))),
          ),
          const SizedBox(width: 6),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
            decoration: BoxDecoration(
              color: question.isFill ? const Color(0xFFFFFBEB) : const Color(0xFFEFF6FF),
              borderRadius: BorderRadius.circular(5),
            ),
            child: Text(
              question.isFill ? '填空' : '选择',
              style: TextStyle(fontSize: 10, color: question.isFill ? const Color(0xFF92400E) : const Color(0xFF1D4ED8)),
            ),
          ),
        ]),
        const SizedBox(height: 8),
        Text(question.stem, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w600, color: Color(0xFF0F172A))),
        const SizedBox(height: 10),
        if (question.isFill) _buildFillInput(submitted) else _buildChoiceOptions(submitted),
        if (submitted) _buildVerdict(),
        if (!submitted)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: SizedBox(
              width: double.infinity,
              child: ElevatedButton(
                onPressed: question.isFill ? (fillController == null || fillController!.text.isEmpty ? null : onSubmit) : (pick == null ? null : onSubmit),
                style: ElevatedButton.styleFrom(backgroundColor: const Color(0xFF8B5CF6), foregroundColor: Colors.white, elevation: 0),
                child: const Text('提交', style: TextStyle(fontSize: 13)),
              ),
            ),
          ),
        if (onJump != null && submitted)
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              onPressed: onJump,
              icon: const Icon(Icons.play_circle_outline_rounded, size: 16, color: Color(0xFF2563EB)),
              label: Text('跳转视频 ${_fmtJump(question.chunkStartTime!)}', style: const TextStyle(fontSize: 12, color: Color(0xFF2563EB))),
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

  Widget _optionTile(int i, bool submitted) {
    final isSelected = pick == i;
    final isCorrect = submitted && result!.correctIndex == i;
    final isWrongPick = submitted && isSelected && !isCorrect;
    Color bg = Colors.white;
    Color border = const Color(0xFFE2E8F0);
    if (submitted) {
      if (isCorrect) {
        bg = const Color(0xFFECFDF5);
        border = const Color(0xFF10B981);
      } else if (isWrongPick) {
        bg = const Color(0xFFFEF2F2);
        border = const Color(0xFFEF4444);
      }
    } else if (isSelected) {
      bg = const Color(0xFFF5F3FF);
      border = const Color(0xFF8B5CF6);
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
              border: Border.all(color: isSelected ? const Color(0xFF8B5CF6) : const Color(0xFFCBD5E1)),
              color: isSelected ? const Color(0xFF8B5CF6) : Colors.transparent,
            ),
            alignment: Alignment.center,
            child: isSelected ? const Icon(Icons.check, size: 12, color: Colors.white) : null,
          ),
          const SizedBox(width: 8),
          Expanded(child: Text(question.options[i], style: const TextStyle(fontSize: 13, color: Color(0xFF334155)))),
          if (isCorrect) const Icon(Icons.check_circle, size: 16, color: Color(0xFF10B981)),
          if (isWrongPick) const Icon(Icons.cancel, size: 16, color: Color(0xFFEF4444)),
        ]),
      ),
    );
  }

  Widget _buildFillInput(bool submitted) {
    return TextField(
      controller: fillController,
      enabled: !submitted,
      onChanged: (_) {}, // trigger rebuild for submit-button enable state
      decoration: InputDecoration(
        hintText: '输入你的答案',
        hintStyle: const TextStyle(color: Color(0xFF94A3B8), fontSize: 13),
        filled: true,
        fillColor: const Color(0xFFF8FAFC),
        border: OutlineInputBorder(borderRadius: BorderRadius.circular(8), borderSide: const BorderSide(color: Color(0xFFE2E8F0))),
        enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(8), borderSide: const BorderSide(color: Color(0xFFE2E8F0))),
        contentPadding: const EdgeInsets.symmetric(horizontal: 10, vertical: 10),
      ),
      style: const TextStyle(fontSize: 14, color: Color(0xFF0F172A)),
    );
  }

  Widget _buildVerdict() {
    final r = result!;
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: r.correct ? const Color(0xFFECFDF5) : const Color(0xFFFEF2F2),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Row(children: [
          Icon(r.correct ? Icons.check_circle : Icons.cancel, size: 16, color: r.correct ? const Color(0xFF10B981) : const Color(0xFFEF4444)),
          const SizedBox(width: 6),
          Text(
            r.correct ? '回答正确!' : '答案不正确',
            style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: r.correct ? const Color(0xFF059669) : const Color(0xFFDC2626)),
          ),
        ]),
        if (!r.correct && r.correctText.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text('正确答案: ${r.correctText}', style: const TextStyle(fontSize: 12, color: Color(0xFF334155))),
        ],
        if (r.explanation.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text(r.explanation, style: const TextStyle(fontSize: 12, color: Color(0xFF64748B), height: 1.4)),
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
