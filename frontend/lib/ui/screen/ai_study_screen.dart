import 'dart:async';
import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../model/quiz.dart';
import '../../service/api_service.dart';
import '../../service/quiz_draft_store.dart';
import 'player_screen.dart';

// AiStudyScreen — the Phase C AI learning page. Two sections:
//   1. AI summary (headline / key points / concepts / takeaway) — read from
//      /ai-summary. The LLM's agent_feedback (study advice) shows here too.
//   2. Practice tab — fetch the quiz (lazily generated, polls while generating),
//      then the Phase B "统一提交" flow:
//        a) answering(作答中):所有题展示,选择/填空可改,底部"提交全部"。不立即判分。
//           草稿(未提交的选择/填空)按 (uid, eid) 缓存到本地,防止切后台丢失。
//        b) submitted(已交卷):点"提交全部" → 调 submit-all 一次性判分 → 锁定所有题,
//           逐题显示对错 + 正确答案 + 解析 + 跳转按钮(仅 has_jump=true 的题)。
//        已交卷的 quiz 重新进入本页时直接进入 submitted 只读态(quiz.submitted)。
//
// Navigation: pushed from course_detail_screen's "AI 重点总结" button and from
// the player. 跳转按钮改为 push 一个标准 PlayerScreen(带 disableAiTab:true +
// initialPosition),不再 pop JumpRequest——这样跳转出的播放器不能再进 AI 页,
// 防止栈无限加深,且本页留在栈里、交卷结果状态保留。

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

  // Quiz state machine: loading → generating/ready/unavailable → (answering | submitted)。
  QuizStatus? _quizStatus;
  QuizView? _quiz;
  bool _quizLoading = true;
  Timer? _pollTimer;

  // 本地已交卷标志。quiz.submitted(来自后端)或本地 _submitted=true 都表示"已交卷"。
  // 本地标志的存在是为了:用户点完"提交全部"、submit-all 成功后立即切到 submitted 态,
  // 不用等下次 fetch。
  bool _submitted = false;

  // Per-question answer state (local until submit; holds the user's selection
  // or text input, and the result once submitted)。
  final Map<int, int> _choicePicks = {}; // questionId → selected option index
  final Map<int, TextEditingController> _fillControllers = {};
  final Map<int, QuizAnswerResult> _results = {}; // questionId → verdict(交卷后逐题结果)
  // 统一提交中(按钮置灰防重复点)。
  bool _submittingAll = false;

  // Phase 3: archived (superseded) quiz history, shown read-only. Collapsed by
  // default — most students rarely reopen old attempts, but the data is
  // preserved so a curious/parent can review past generations + mistakes.
  List<ArchivedQuizView> _history = const [];
  bool _historyLoading = false;
  // Per-history-quiz expanded state (quizId → expanded). Default collapsed.
  final Map<int, bool> _historyExpanded = {};

  @override
  void initState() {
    super.initState();
    _loadSummary();
    _loadQuiz();
    _loadHistory();
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    _disposeFillControllers();
    super.dispose();
  }

  // 集中清理填空 controller。dispose()(页面销毁)和 _regenerate()(换卷子)都用。
  void _disposeFillControllers() {
    for (final c in _fillControllers.values) {
      c.dispose();
    }
    _fillControllers.clear();
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
      // 已交卷(后端 quiz.SubmittedAt 非零):直接进入只读态,本页不需要草稿,
      // 清掉残留草稿防错乱(例如学生交卷后没返回就切走,草稿可能还在)。
      // 同时从后端回填的 user_answer_index 恢复 _choicePicks,让重进时错项红框
      // 能高亮(否则 _choicePicks 空,只显示正确项绿框,看不出"我选错了哪个")。
      // 未交卷:尝试恢复本地草稿(防切后台丢答案)。
      if (resp.quiz != null && resp.quiz!.submitted) {
        QuizDraftStore.clearDraft(widget.activeUserId, widget.episode.id);
        for (final q in resp.quiz!.questions) {
          if (q.userAnswerIndex != null) {
            _choicePicks[q.id] = q.userAnswerIndex!;
          }
        }
      } else if (resp.status == QuizStatus.ready && resp.quiz != null) {
        await _restoreDraft();
      }
      // If generating, poll until ready (the backend enqueued a per-user job on
      // first GET; it takes a few seconds for the ReAct loop to finish).
      if (resp.status == QuizStatus.generating) {
        _startPolling();
      }
    } catch (_) {
      if (mounted) setState(() => _quizLoading = false);
    }
  }

  // _restoreDraft 从本地草稿恢复选择/填空到 _choicePicks / _fillControllers。
  // 只在 quiz 未交卷、且本地 _choicePicks/_fillControllers 为空(刚进页面)时调用。
  // 草稿按 qid 存,但当前卷子的题 id 可能换过(regen 后),所以恢复时按 id 精确匹配:
  // 卷子里没有的 qid 视为脏数据丢弃(宁可丢一题也别错位)。
  Future<void> _restoreDraft() async {
    final quiz = _quiz;
    if (quiz == null) return;
    final draft = await QuizDraftStore.loadDraft(widget.activeUserId, widget.episode.id);
    if (!mounted || draft.isEmpty) return;
    final qids = {for (final q in quiz.questions) q.id};
    setState(() {
      draft.choicePicks.forEach((qidStr, idx) {
        final qid = int.tryParse(qidStr);
        if (qid != null && qids.contains(qid)) {
          _choicePicks[qid] = idx;
        }
      });
      draft.fillTexts.forEach((qidStr, text) {
        final qid = int.tryParse(qidStr);
        if (qid != null && qids.contains(qid)) {
          _fillControllers[qid] ??= TextEditingController();
          _fillControllers[qid]!.text = text;
        }
      });
    });
  }

  // _persistDraft 把当前选择/填空写到本地草稿(覆盖写)。调用方在 onPick/onChanged
  // 里先 setState 改内存,再调本方法异步落盘。SharedPreferences 本地 IO 廉价,不抖。
  void _persistDraft() {
    final texts = <int, String>{};
    _fillControllers.forEach((qid, c) {
      if (c.text.isNotEmpty) texts[qid] = c.text;
    });
    QuizDraftStore.saveDraft(widget.activeUserId, widget.episode.id, _choicePicks, texts);
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
          // A new active quiz just landed, which means the prior one was
          // archived server-side. Refresh history so the panel reflects it.
          _loadHistory();
        }
      } catch (_) {
        // keep polling on transient errors
      }
    });
  }

  // _loadHistory fetches the archived (superseded) quiz generations for the
  // history panel. Best-effort: failures just leave the previous list (or
  // empty) rather than blocking the page.
  Future<void> _loadHistory() async {
    if (mounted) setState(() => _historyLoading = true);
    try {
      final h = await ApiService.fetchQuizHistory(widget.activeUserId, widget.episode.id);
      if (mounted) setState(() => _history = h);
    } catch (_) {
      // best-effort
    } finally {
      if (mounted) setState(() => _historyLoading = false);
    }
  }

  // _submitAll 是 Phase B 的统一交卷:把整张卷子的选择/填空一次性提交给后端判分。
  // 后端返回的 results 列表与 _quiz.questions 顺序一一对应(位置映射,不是 id),
  // 这里把 results[i] 填到 _results[questions[i].id]。成功后:本地 _submitted=true
  // (立即切只读态,不等下次 fetch)、清草稿(交卷了草稿作废)。
  Future<void> _submitAll() async {
    final quiz = _quiz;
    if (quiz == null || _submittingAll) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('交卷?'),
        content: const Text('提交后不能再修改答案,将一次性判分整张卷子。'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(c, false), child: const Text('再想想')),
          TextButton(onPressed: () => Navigator.pop(c, true), child: const Text('交卷')),
        ],
      ),
    );
    if (confirmed != true) return;
    // 组装 answers:每个题给 {question_id, answer_index?, answer_text?}。
    // 选择题给 answer_index(即便没选也给题 id,后端视为漏答),填空题给 answer_text。
    final answers = <Map<String, dynamic>>[];
    for (final q in quiz.questions) {
      final Map<String, dynamic> a = {'question_id': q.id};
      if (q.isFill) {
        a['answer_text'] = _fillControllers[q.id]?.text ?? '';
      } else {
        final idx = _choicePicks[q.id];
        if (idx != null) a['answer_index'] = idx;
      }
      answers.add(a);
    }
    setState(() => _submittingAll = true);
    try {
      final results = await ApiService.submitAllQuizAnswers(
        activeUserId: widget.activeUserId,
        episodeId: widget.episode.id,
        answers: answers,
      );
      if (!mounted) return;
      // 按 question_id 映射结果到题目(后端每个 result 带 question_id),不依赖
      // 返回顺序与题序一致——位置映射在并发删题/DB 排序漂移时会错位。
      // 带 id 的走 id 映射;万一某条没带 id(理论不应发生),回退到位置映射兜底。
      setState(() {
        final hasIds = results.any((r) => r.questionId != null);
        if (hasIds) {
          for (final r in results) {
            if (r.questionId != null) _results[r.questionId!] = r;
          }
        } else {
          for (int i = 0; i < quiz.questions.length && i < results.length; i++) {
            _results[quiz.questions[i].id] = results[i];
          }
        }
        _submitted = true;
        _submittingAll = false;
      });
      // 交卷成功,草稿作废。
      QuizDraftStore.clearDraft(widget.activeUserId, widget.episode.id);
    } catch (e) {
      if (mounted) {
        setState(() => _submittingAll = false);
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
      // 新卷子:旧的草稿/已选/已判分全部作废。本地 _submitted 也要重置
      // (换题前若已交卷,新卷子是未交卷的)。
      setState(() {
        _quizStatus = QuizStatus.generating;
        _quiz = null;
        _results.clear();
        _choicePicks.clear();
        _submitted = false;
      });
      _disposeFillControllers();
      // 新卷子 id 全新,旧草稿(按旧 qid 存的)是脏数据,清掉。
      QuizDraftStore.clearDraft(widget.activeUserId, widget.episode.id);
      _startPolling();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('换题失败: $e')));
      }
    }
  }

  // _jumpTo: push 一个标准 PlayerScreen,带 initialPosition = 目标时间戳。
  // disableAiTab:true 让跳转出的播放器不能再进 AI 页(否则栈会无限加深:
  // AI 页 → 跳转 → 播放器 → AI 入口 → AI 页 → 跳转 → …)。
  // push 而非 pop:本页(AiStudyScreen)留在栈里,返回键回到本页时 _submitted /
  // _results 状态都保留。
  void _jumpTo(int seconds) {
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (context) => PlayerScreen(
          activeUserId: widget.activeUserId,
          episode: widget.episode,
          disableAiTab: true,
          initialPosition: Duration(seconds: seconds),
        ),
      ),
    );
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
          const SizedBox(height: 16),
          _buildHistorySection(),
        ],
      ),
    );
  }

  // --- History section (Phase 3: archived quizzes, read-only) ---
  //
  // Collapsible. Hidden entirely while there's no history and none loading
  // (the common case before the first regenerate), so the panel never shows an
  // empty "history" block on a fresh lesson.
  Widget _buildHistorySection() {
    if (!_historyLoading && _history.isEmpty) {
      return const SizedBox.shrink();
    }
    return _Card(
      child: Theme(
        // Keep the per-quiz ExpansionTile visuals consistent with the card.
        data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
        child: ExpansionTile(
          initiallyExpanded: false,
          tilePadding: EdgeInsets.zero,
          iconColor: const Color(0xFF64748B),
          collapsedIconColor: const Color(0xFF64748B),
          title: Row(children: [
            const Icon(Icons.history_rounded, size: 16, color: Color(0xFF64748B)),
            const SizedBox(width: 6),
            const Text('历史练习', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w700, color: Color(0xFF0F172A))),
            const SizedBox(width: 8),
            if (_historyLoading)
              const SizedBox(width: 12, height: 12, child: CircularProgressIndicator(strokeWidth: 2))
            else
              Text('${_history.length} 套', style: const TextStyle(fontSize: 12, color: Color(0xFF94A3B8))),
          ]),
          children: [
            for (final h in _history) _HistoryQuizCard(
              quiz: h,
              expanded: _historyExpanded[h.quizId] ?? false,
              onToggle: () => setState(() => _historyExpanded[h.quizId] = !(_historyExpanded[h.quizId] ?? false)),
              onJump: (seconds) => _jumpTo(seconds),
            ),
          ],
        ),
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
        // 知识点小节(Phase F):按知识点分组的结构化要点,比平铺更像学习笔记。
        // 优先展示 sections;如果老数据没有 sections 才回退到平铺 keyPoints。
        if (s.sections.isNotEmpty) ...[
          const SizedBox(height: 12),
          ...s.sections.map((sec) => Padding(
                padding: const EdgeInsets.only(bottom: 10),
                child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  Text(sec.title, style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w700, color: Color(0xFF6D28D9))),
                  const SizedBox(height: 4),
                  ...sec.points.map((p) => Padding(
                        padding: const EdgeInsets.only(bottom: 3, left: 10),
                        child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
                          const Text('· ', style: TextStyle(color: Color(0xFF8B5CF6), fontWeight: FontWeight.bold)),
                          Expanded(child: Text(p, style: const TextStyle(fontSize: 12.5, color: Color(0xFF334155)))),
                        ]),
                      )),
                ]),
              )),
        ] else if (s.keyPoints.isNotEmpty) ...[
          const SizedBox(height: 10),
          ...s.keyPoints.map((p) => Padding(
                padding: const EdgeInsets.only(bottom: 4),
                child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  const Text('· ', style: TextStyle(color: Color(0xFF8B5CF6), fontWeight: FontWeight.bold)),
                  Expanded(child: Text(p, style: const TextStyle(fontSize: 13, color: Color(0xFF334155)))),
                ]),
              )),
        ],
        // 方法/技巧/公式(Phase F):单独拎出来便于速查。
        if (s.methods.isNotEmpty) ...[
          const SizedBox(height: 10),
          Container(
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(color: const Color(0xFFF0FDF4), borderRadius: BorderRadius.circular(8)),
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              const Row(children: [
                Icon(Icons.flag_outlined, size: 14, color: Color(0xFF16A34A)),
                SizedBox(width: 4),
                Text('方法技巧', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: Color(0xFF15803D))),
              ]),
              const SizedBox(height: 4),
              ...s.methods.map((m) => Padding(
                    padding: const EdgeInsets.only(bottom: 2),
                    child: Text(m, style: const TextStyle(fontSize: 12, color: Color(0xFF166534))),
                  )),
            ]),
          ),
        ],
        // 易错点(Phase F):帮学生避坑。
        if (s.commonMistakes.isNotEmpty) ...[
          const SizedBox(height: 8),
          Container(
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(color: const Color(0xFFFEF2F2), borderRadius: BorderRadius.circular(8)),
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              const Row(children: [
                Icon(Icons.warning_amber_rounded, size: 14, color: Color(0xFFDC2626)),
                SizedBox(width: 4),
                Text('易错点', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: Color(0xFFB91C1C))),
              ]),
              const SizedBox(height: 4),
              ...s.commonMistakes.map((m) => Padding(
                    padding: const EdgeInsets.only(bottom: 2),
                    child: Text(m, style: const TextStyle(fontSize: 12, color: Color(0xFF991B1B))),
                  )),
            ]),
          ),
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
    // 交卷态:后端 quiz.submitted(重进已交卷的卷子)或本地 _submitted(刚点完提交全部)。
    final submitted = _quiz!.submitted || _submitted;
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Padding(
        padding: const EdgeInsets.only(left: 4, bottom: 8),
        child: Row(children: [
          const Text('练习', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700, color: Color(0xFF0F172A))),
          const SizedBox(width: 8),
          if (submitted)
            const Text('已交卷', style: TextStyle(fontSize: 12, color: Color(0xFF10B981), fontWeight: FontWeight.w600))
          else
            Text('${_answeredCount()}/${questions.length} 已答', style: const TextStyle(fontSize: 12, color: Color(0xFF94A3B8))),
          const Spacer(),
          TextButton.icon(
            onPressed: _regenerate,
            icon: const Icon(Icons.refresh_rounded, size: 14),
            label: const Text('换题', style: TextStyle(fontSize: 12)),
            style: TextButton.styleFrom(foregroundColor: const Color(0xFF8B5CF6)),
          ),
        ]),
      ),
      ...questions.asMap().entries.map((e) {
            // 交卷后的逐题结果:优先用 submit-all 返回的(刚交卷),没有就从
            // question 自身的回填字段合成(重进已交卷页面,_results 为空但后端在
            // QuizView 的 questions 里回填了 correct/correctIndex/explanation)。
            final result = _results[e.value.id] ??
                (submitted
                    ? QuizAnswerResult(
                        correct: e.value.correct,
                        correctIndex: e.value.correctIndex,
                        correctText: e.value.correctText,
                        explanation: e.value.explanation,
                        chunkStartTime: e.value.chunkStartTime,
                      )
                    : null);
            return Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: _QuestionCard(
              index: e.key,
              question: e.value,
              pick: _choicePicks[e.value.id],
              fillController: e.value.isFill ? (_fillControllers[e.value.id] ??= TextEditingController()) : null,
              result: result,
              submitted: submitted,
              onPick: (i) {
                setState(() => _choicePicks[e.value.id] = i);
                _persistDraft();
              },
              onFillChanged: () {
                // 只触发 rebuild 让按钮状态更新;实际落盘由 onChanged 里 debounce 调用。
                setState(() {});
                _persistDraft();
              },
              // 跳转按钮:只对 hasJump=true 的题渲染,且仅交卷态展示(看错题时才有用)。
              onJump: (e.value.hasJump && e.value.chunkStartTime == null)
                  ? null
                  : (e.value.hasJump && e.value.chunkStartTime != null)
                      ? () => _jumpTo(e.value.chunkStartTime!)
                      : null,
            ),
          );
          }),
      // 未交卷:底部醒目的"提交全部"按钮(交卷后不再渲染)。
      if (!submitted)
        Padding(
          padding: const EdgeInsets.only(top: 4),
          child: SizedBox(
            width: double.infinity,
            child: ElevatedButton(
              onPressed: _submittingAll ? null : _submitAll,
              style: ElevatedButton.styleFrom(
                backgroundColor: const Color(0xFF8B5CF6),
                foregroundColor: Colors.white,
                elevation: 0,
                padding: const EdgeInsets.symmetric(vertical: 14),
                shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
              ),
              child: _submittingAll
                  ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
                  : const Text('提交全部', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700)),
            ),
          ),
        ),
    ]);
  }

  // _answeredCount:本地统计已作答题数(选择有 pick 或填空非空)。后端 answeredCount
  // 只在 fetch 时更新,做题过程中的实时计数用本地状态更准。
  int _answeredCount() {
    final quiz = _quiz;
    if (quiz == null) return 0;
    int n = 0;
    for (final q in quiz.questions) {
      if (q.isFill) {
        if ((_fillControllers[q.id]?.text ?? '').isNotEmpty) n++;
      } else {
        if (_choicePicks[q.id] != null) n++;
      }
    }
    return n;
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
  // 填空文本变化回调:触发 rebuild + 落盘草稿(选择用 onPick,填空用这个)。
  final VoidCallback onFillChanged;
  final VoidCallback? onJump;
  // 统一提交后整张卷子锁定。submitted=true 时所有题只读,
  // 逐题展示 result(若 result 还没到——比如重进已交卷的卷子而 fetch 没带结果——则不展示判分)。
  final bool submitted;

  const _QuestionCard({
    required this.index,
    required this.question,
    required this.pick,
    required this.fillController,
    required this.result,
    required this.onPick,
    required this.onFillChanged,
    required this.onJump,
    required this.submitted,
  });

  @override
  Widget build(BuildContext context) {
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
    // result 可能为 null:整卷已交卷(submitted=true)但 result 还没回来
    // (重进一张已交卷的卷子时 GET 不带结果)。此时不渲染对错高亮,保持中性只读。
    final hasVerdict = submitted && result != null;
    final isCorrect = hasVerdict && result!.correctIndex == i;
    final isWrongPick = hasVerdict && isSelected && !isCorrect;
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
      onChanged: (_) => onFillChanged(), // 触发 rebuild(已答计数) + 落盘草稿
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

// --- History quiz card (read-only, fully revealed) ---
//
// One archived quiz in the history panel. The header shows the generated time,
// question count, and wrong count; tapping expands to reveal every question
// WITH the correct answer + explanation (read-only — no submit). Wrong
// questions get a red left border + a small "错" tag so mistakes stand out.
class _HistoryQuizCard extends StatelessWidget {
  final ArchivedQuizView quiz;
  final bool expanded;
  final VoidCallback onToggle;
  final ValueChanged<int> onJump; // seconds → seek the player

  const _HistoryQuizCard({
    required this.quiz,
    required this.expanded,
    required this.onToggle,
    required this.onJump,
  });

  @override
  Widget build(BuildContext context) {
    // Header is always visible; questions only when expanded.
    return Container(
      margin: const EdgeInsets.only(top: 8),
      decoration: BoxDecoration(
        color: const Color(0xFFF8FAFC),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: const Color(0xFFE2E8F0)),
      ),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        // Header row (tap to toggle).
        InkWell(
          onTap: onToggle,
          borderRadius: BorderRadius.circular(10),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 9),
            child: Row(children: [
              Icon(expanded ? Icons.expand_less_rounded : Icons.chevron_right_rounded,
                  size: 18, color: const Color(0xFF64748B)),
              const SizedBox(width: 4),
              Expanded(
                child: Text(
                  // ArchivedAt is when it was superseded; fall back to generatedAt.
                  quiz.archivedAt.isNotEmpty ? quiz.archivedAt : quiz.generatedAt,
                  style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: Color(0xFF334155)),
                ),
              ),
              Text('${quiz.questionCount} 题', style: const TextStyle(fontSize: 11, color: Color(0xFF64748B))),
              const SizedBox(width: 8),
              if (quiz.wrongCount > 0)
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                  decoration: BoxDecoration(color: const Color(0xFFFEF2F2), borderRadius: BorderRadius.circular(5)),
                  child: Text('错 ${quiz.wrongCount}', style: const TextStyle(fontSize: 10, color: Color(0xFFEF4444))),
                )
              else
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                  decoration: BoxDecoration(color: const Color(0xFFECFDF5), borderRadius: BorderRadius.circular(5)),
                  child: const Text('全对', style: TextStyle(fontSize: 10, color: Color(0xFF059669))),
                ),
            ]),
          ),
        ),
        if (expanded)
          Padding(
            padding: const EdgeInsets.fromLTRB(10, 4, 10, 10),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                for (int i = 0; i < quiz.questions.length; i++)
                  _HistoryQuestionTile(index: i, q: quiz.questions[i], onJump: onJump),
              ],
            ),
          ),
      ]),
    );
  }
}

// One read-only question in a history quiz. Reveals the correct answer + a
// per-option correctness highlight. Wrong questions get a red accent.
class _HistoryQuestionTile extends StatelessWidget {
  final int index;
  final ArchivedQuizQuestion q;
  final ValueChanged<int> onJump;

  const _HistoryQuestionTile({required this.index, required this.q, required this.onJump});

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(8),
        border: Border(
          left: BorderSide(width: 3, color: q.wrong ? const Color(0xFFEF4444) : const Color(0xFFE2E8F0)),
        ),
      ),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Row(children: [
          Text('第${index + 1}题', style: const TextStyle(fontSize: 10, color: Color(0xFF64748B))),
          const SizedBox(width: 6),
          if (q.wrong)
            const Text('错', style: TextStyle(fontSize: 10, color: Color(0xFFEF4444), fontWeight: FontWeight.w700))
          else
            const Text('已掌握', style: TextStyle(fontSize: 10, color: Color(0xFF059669))),
        ]),
        const SizedBox(height: 6),
        Text(q.stem, style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: Color(0xFF0F172A))),
        const SizedBox(height: 8),
        if (q.isFill) _buildFillAnswer() else _buildChoiceOptions(),
        if (q.explanation.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(q.explanation, style: const TextStyle(fontSize: 11, color: Color(0xFF64748B), height: 1.4)),
        ],
        if (q.chunkStartTime != null)
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              onPressed: () => onJump(q.chunkStartTime!),
              icon: const Icon(Icons.play_circle_outline_rounded, size: 14, color: Color(0xFF2563EB)),
              label: Text('跳转视频 ${_fmtJump(q.chunkStartTime!)}',
                  style: const TextStyle(fontSize: 11, color: Color(0xFF2563EB))),
              style: TextButton.styleFrom(
                padding: const EdgeInsets.symmetric(horizontal: 4),
                minimumSize: const Size(0, 24),
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
            ),
          ),
      ]),
    );
  }

  Widget _buildChoiceOptions() {
    return Column(children: [
      for (int i = 0; i < q.options.length; i++)
        Container(
          margin: const EdgeInsets.only(bottom: 4),
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 7),
          decoration: BoxDecoration(
            color: q.correctIndex == i ? const Color(0xFFECFDF5) : Colors.white,
            borderRadius: BorderRadius.circular(6),
            border: Border.all(color: q.correctIndex == i ? const Color(0xFF10B981) : const Color(0xFFE2E8F0)),
          ),
          child: Row(children: [
            Expanded(child: Text(q.options[i], style: const TextStyle(fontSize: 12, color: Color(0xFF334155)))),
            if (q.correctIndex == i) const Icon(Icons.check_circle, size: 14, color: Color(0xFF10B981)),
          ]),
        ),
    ]);
  }

  Widget _buildFillAnswer() {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 7),
      decoration: BoxDecoration(
        color: const Color(0xFFECFDF5),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: const Color(0xFF10B981)),
      ),
      child: Text('正确答案: ${q.correctText}', style: const TextStyle(fontSize: 12, color: Color(0xFF059669))),
    );
  }
}
