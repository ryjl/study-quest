import 'dart:async';
import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../model/quiz.dart';
import '../../service/api_service.dart';
import '../../service/quiz_draft_store.dart';
import '../../service/ui_prefs.dart';
import '../../service/tv_mode.dart';
import '../../theme.dart';
import '../widget/markdown_view.dart';
import 'player_screen.dart';

// AiStudyScreen — the Phase C AI learning page. Three sections:
//   1. AI summary (headline / sections / concepts / takeaway) — read from /ai-summary.
//   2. 学习建议 (advice) — read from /ai-advice (advice agent, lazily generated +
//      polled while generating). The sole source for study advice; no fallback.
//   3. Practice tab — fetch the quiz (lazily generated, polls while generating),
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

  // Advice(学习建议,agent 驱动)状态。和 quiz 同一套 lazy 生成 + 轮询模式:
  // 首次访问触发后端入队 advice job,返回 generating;ready 时带 advice 文本。
  // advice 是学习建议的唯一来源(不做降级)——unavailable/空时卡片直接隐藏。
  StudyAdvice? _advice;
  AdviceStatus? _adviceStatus;
  Timer? _adviceTimer;

  // 本地已交卷标志。quiz.submitted(来自后端)或本地 _submitted=true 都表示"已交卷"。
  // 本地标志的存在是为了:用户点完"提交全部"、submit-all 成功后立即切到 submitted 态,
  // 不用等下次 fetch。
  bool _submitted = false;

  // Per-question answer state (local until submit; holds the user's selection
  // or text input, and the result once submitted)。
  final Map<int, int> _choicePicks = {}; // questionId → selected option index
  // multi_choice:questionId → 选中的选项索引集合(Set 支持快速 toggle;交卷时转 List)。
  final Map<int, Set<int>> _multiPicks = {};
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
    _loadAdvice();
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    _adviceTimer?.cancel();
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
          // 多选题回填学生当时选的索引集合(重进已交卷卷子时高亮错项)。
          if (q.isMultiChoice && q.userAnswerIndices.isNotEmpty) {
            _multiPicks[q.id] = q.userAnswerIndices.toSet();
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
      // multi_picks 恢复:同样按当前卷子 qid 过滤防脏数据(regen 后旧 qid 作废)。
      draft.multiPicks.forEach((qidStr, indices) {
        final qid = int.tryParse(qidStr);
        if (qid != null && qids.contains(qid) && indices.isNotEmpty) {
          _multiPicks[qid] = indices.toSet();
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
    QuizDraftStore.saveDraft(
      widget.activeUserId,
      widget.episode.id,
      _choicePicks,
      texts,
      _multiPicks,
    );
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

  // _loadAdvice 拉取 episode 级学习建议(agent 驱动,跨知识点分析)。和 _loadQuiz 同构:
  // 首次访问后端 lazy 入队 advice job,返回 generating;ready 时带 advice 文本。
  // generating → 轮询直到 ready。best-effort:失败不阻断页面,建议卡片保持隐藏。
  Future<void> _loadAdvice() async {
    try {
      final resp = await ApiService.fetchEpisodeAdvice(widget.activeUserId, widget.episode.id);
      if (!mounted) return;
      setState(() {
        _adviceStatus = resp.status;
        _advice = resp.advice;
      });
      if (resp.status == AdviceStatus.generating) {
        _startAdvicePolling();
      }
    } catch (_) {
      // best-effort:失败保持建议隐藏,不阻断页面。
    }
  }

  // _startAdvicePolling:generating 时每 3s 拉一次,直到 ready(advice agent 跨知识点
  // 分析可能比出题慢一些)。和 quiz 的 _startPolling 同模式。
  void _startAdvicePolling() {
    _adviceTimer?.cancel();
    _adviceTimer = Timer.periodic(const Duration(seconds: 3), (_) async {
      try {
        final resp = await ApiService.fetchEpisodeAdvice(widget.activeUserId, widget.episode.id);
        if (!mounted) return;
        if (resp.status == AdviceStatus.ready) {
          _adviceTimer?.cancel();
          setState(() {
            _adviceStatus = resp.status;
            _advice = resp.advice;
          });
        }
      } catch (_) {
        // 瞬时错误继续轮询
      }
    });
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
    // 组装 answers:每个题给 {question_id, answer_index? | answer_text? | answer_indices?}。
    // 单选 answer_index,填空 answer_text,多选 answer_indices(排序便于后端/人读对照)。
    // 即便没作答也给题 id,后端按对应字段缺失视为漏答。
    final answers = <Map<String, dynamic>>[];
    for (final q in quiz.questions) {
      final Map<String, dynamic> a = {'question_id': q.id};
      if (q.isMultiChoice) {
        final picks = (_multiPicks[q.id] ?? <int>{}).toList()..sort();
        a['answer_indices'] = picks;
      } else if (q.isFill) {
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
      // 交卷成功后后端链式 enqueue 了 episode 级 advice job(学生刚交完卷 memory 最新,
      // 这时跑 advice 最准)。重新拉 advice 触发轮询,让学生交卷后能很快看到复习建议。
      _loadAdvice();
      // 提示错题已加入错题本(发现性:让学生知道有错题要复习)。
      final wrongCount = results.where((r) => !r.correct).length;
      if (wrongCount > 0 && mounted) {
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
        _multiPicks.clear();
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
    // TV 模式强制最大档(1.4)——TV 上靠远距离观看,且 TV 不渲染字号调整按钮,
    // 用一个 effectiveScale 统一覆盖所有 MarkdownView / Text 的缩放,避免读 UiPrefs。
    final double effectiveScale =
        TvMode.instance.isActive ? 1.4 : UiPrefs.instance.aiTextScale;
    return Scaffold(
      backgroundColor: const Color(0xFFF8FAFC),
      appBar: AppBar(
        backgroundColor: Colors.white,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_rounded, color: AppTheme.textMuted),
          onPressed: () => Navigator.of(context).pop(),
        ),
        title: Text(
          widget.episode.title,
          style: const TextStyle(color: AppTheme.slate900, fontSize: 16, fontWeight: FontWeight.w600),
        ),
        // 字号调整按钮(需求 #6)。TV 模式下隐藏——TV 永远走最大档,调了也没用。
        actions: [
          if (!TvMode.instance.isActive)
            IconButton(
              icon: const Icon(Icons.format_size_rounded, color: AppTheme.textMuted),
              tooltip: '字号',
              onPressed: () => _showTextScaleMenu(context),
            ),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          _buildSummarySection(effectiveScale),
          const SizedBox(height: 16),
          _buildAdviceCard(effectiveScale),
          const SizedBox(height: 16),
          _buildQuizSection(effectiveScale),
          const SizedBox(height: 16),
          _buildHistorySection(effectiveScale),
        ],
      ),
    );
  }

  // 字号菜单(需求 #6):ModalBottomSheet 里 4 档 ListTile,当前档打勾。
  // 选中后写 SharedPreferences 持久化 + setState 触发整页重建,所有 MarkdownView
  // 的 textScale 跟着更新。沿用 _showFilterBottomSheet 的风格(圆角顶部 + 白底)。
  void _showTextScaleMenu(BuildContext context) {
    showModalBottomSheet(
      context: context,
      backgroundColor: Colors.transparent,
      builder: (sheetCtx) {
        return Container(
          decoration: const BoxDecoration(
            color: Colors.white,
            borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
          ),
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 16, 20, 8),
                child: Row(children: [
                  const Icon(Icons.format_size_rounded, size: 18, color: AppTheme.slate900),
                  const SizedBox(width: 8),
                  const Text('字号', style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: AppTheme.slate900)),
                ]),
              ),
              for (int i = 0; i < UiPrefs.aiScaleLabels.length; i++)
                ListTile(
                  leading: Icon(
                    // 4 档依次给个大小递增的字号图标,直观表达档位语义。
                    [Icons.text_fields_rounded, Icons.text_fields_rounded, Icons.text_fields_rounded, Icons.text_fields_rounded][i],
                    size: [16, 20, 24, 28][i].toDouble(),
                    color: UiPrefs.instance.aiTextScaleIndex == i ? AppTheme.violet500 : AppTheme.slate400,
                  ),
                  title: Text(
                    UiPrefs.aiScaleLabels[i],
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: UiPrefs.instance.aiTextScaleIndex == i ? FontWeight.w700 : FontWeight.w500,
                      color: UiPrefs.instance.aiTextScaleIndex == i ? AppTheme.violet500 : const Color(0xFF334155),
                    ),
                  ),
                  trailing: UiPrefs.instance.aiTextScaleIndex == i
                      ? const Icon(Icons.check_rounded, color: AppTheme.violet500)
                      : null,
                  onTap: () async {
                    await UiPrefs.instance.setAiTextScaleIndex(i);
                    if (!mounted) return;
                    setState(() {}); // 触发重建,MarkdownView 的 textScale 更新
                    if (sheetCtx.mounted) Navigator.pop(sheetCtx);
                  },
                ),
              const SizedBox(height: 8),
            ],
          ),
        );
      },
    );
  }

  // --- History section (Phase 3: archived quizzes, read-only) ---
  //
  // Collapsible. Hidden entirely while there's no history and none loading
  // (the common case before the first regenerate), so the panel never shows an
  // empty "history" block on a fresh lesson.
  Widget _buildHistorySection(double textScale) {
    // TV 模式不做题,历史练习也是练习相关,一并隐藏。
    if (TvMode.instance.isActive) return const SizedBox.shrink();
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
          iconColor: AppTheme.textMuted,
          collapsedIconColor: AppTheme.textMuted,
          title: Row(children: [
            const Icon(Icons.history_rounded, size: 16, color: AppTheme.textMuted),
            const SizedBox(width: 6),
            const Text('历史练习', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w700, color: AppTheme.slate900)),
            const SizedBox(width: 8),
            if (_historyLoading)
              const SizedBox(width: 12, height: 12, child: CircularProgressIndicator(strokeWidth: 2))
            else
              Text('${_history.length} 套', style: const TextStyle(fontSize: 12, color: AppTheme.slate400)),
          ]),
          children: [
            for (final h in _history) _HistoryQuizCard(
              quiz: h,
              expanded: _historyExpanded[h.quizId] ?? false,
              onToggle: () => setState(() => _historyExpanded[h.quizId] = !(_historyExpanded[h.quizId] ?? false)),
              onJump: (seconds) => _jumpTo(seconds),
              textScale: textScale,
            ),
          ],
        ),
      ),
    );
  }

  // --- Summary card ---
  Widget _buildSummarySection(double textScale) {
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
          Icon(Icons.auto_awesome_rounded, size: 16, color: AppTheme.violet500),
          SizedBox(width: 6),
          Text('AI 重点总结', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700, color: AppTheme.slate900)),
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
                  // 每个知识点要点可能含 markdown 表格/SVG 图,用 _PointItem 渲染:
                  // 纯文本走 bullet+Expanded,含 block(表格/图)走整行宽度避免盖到相邻文字。
                  ...sec.points.map((p) => _PointItem(
                        data: p,
                        textScale: textScale,
                        textColor: const Color(0xFF334155),
                      )),
                ]),
              )),
        ] else if (s.keyPoints.isNotEmpty) ...[
          const SizedBox(height: 10),
          ...s.keyPoints.map((p) => _PointItem(
                data: p,
                textScale: textScale,
                textColor: const Color(0xFF334155),
              )),
        ],
        // 方法/技巧/公式(Phase F):单独拎出来便于速查。
        if (s.methods.isNotEmpty) ...[
          const SizedBox(height: 10),
          Container(
            padding: const EdgeInsets.only(left: 12, right: 4, top: 2, bottom: 2),
            decoration: const BoxDecoration(
              border: Border(left: BorderSide(color: AppTheme.accentGreen, width: 3)),
            ),
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              const Row(children: [
                Icon(Icons.flag_outlined, size: 14, color: Color(0xFF16A34A)),
                SizedBox(width: 4),
                Text('方法技巧', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: Color(0xFF15803D))),
              ]),
              const SizedBox(height: 4),
              // 方法技巧每条含公式/表格时 markdown 渲染;绿色保留。
              ...s.methods.map((m) => Padding(
                    padding: const EdgeInsets.only(bottom: 2),
                    child: MarkdownView(
                      data: m,
                      textScale: textScale,
                      baseTextColor: const Color(0xFF166534),
                    ),
                  )),
            ]),
          ),
        ],
        // 易错点(Phase F):帮学生避坑。
        if (s.commonMistakes.isNotEmpty) ...[
          const SizedBox(height: 8),
          Container(
            padding: const EdgeInsets.only(left: 12, right: 4, top: 2, bottom: 2),
            decoration: const BoxDecoration(
              border: Border(left: BorderSide(color: Color(0xFFEF4444), width: 3)),
            ),
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              const Row(children: [
                Icon(Icons.warning_amber_rounded, size: 14, color: Color(0xFFDC2626)),
                SizedBox(width: 4),
                Text('易错点', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: Color(0xFFB91C1C))),
              ]),
              const SizedBox(height: 4),
              // 易错点可能含对比表格,markdown 渲染;红色保留。
              ...s.commonMistakes.map((m) => Padding(
                    padding: const EdgeInsets.only(bottom: 2),
                    child: MarkdownView(
                      data: m,
                      textScale: textScale,
                      baseTextColor: const Color(0xFF991B1B),
                    ),
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
                      decoration: BoxDecoration(color: AppTheme.blue100, borderRadius: BorderRadius.circular(6)),
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
            // 总结 takeaway 可能用 markdown 加粗关键词;琥珀色保留。
            child: MarkdownView(
              data: s.takeaway,
              textScale: textScale,
              baseTextColor: const Color(0xFF92400E),
            ),
          ),
        ],
      ]),
    );
  }

  // --- 学习建议区(advice agent 驱动) ---
  //
  // 唯一来源是 advice agent(跨知识点读 mastery 后的综合分析)。advice 不可用或为空
  // 时直接隐藏卡片——不做任何降级(quiz 的 agent_feedback 副产品不再在这里展示,
  // 避免和 advice 语义重复)。
  Widget _buildAdviceCard(double textScale) {
    // generating 态:advice 正在生成,展示占位卡片(比直接隐藏更连贯)。
    if (_adviceStatus == AdviceStatus.generating) {
      return const _Card(
        child: Padding(
          padding: EdgeInsets.all(16),
          child: Row(children: [
            SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2)),
            SizedBox(width: 10),
            Text('正在分析你的学习情况…', style: TextStyle(fontSize: 13, color: AppTheme.textMuted)),
          ]),
        ),
      );
    }
    // advice ready 且非空才展示;unavailable 或空 → 隐藏(AI 未配置 / 尚无 mastery 时正常)。
    final advice = (_adviceStatus == AdviceStatus.ready && _advice != null && !_advice!.isEmpty)
        ? _advice!.adviceText
        : '';
    if (advice.isEmpty) return const SizedBox.shrink();
    return _Card(
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        const Row(children: [
          Icon(Icons.lightbulb_outline_rounded, size: 16, color: Color(0xFFF59E0B)),
          SizedBox(width: 6),
          Text('AI 学习建议', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w700, color: AppTheme.slate900)),
        ]),
        const SizedBox(height: 8),
        // 学习建议正文是 agent 跨知识点综合分析,鼓励输出 markdown 列表/加粗/对比表格。
        // 用 MarkdownView 渲染,字号跟随 AI 页全局缩放(textScale)。
        MarkdownView(
          data: advice,
          textScale: textScale,
          baseTextColor: const Color(0xFF334155),
        ),
      ]),
    );
  }

  // --- Quiz section ---
  Widget _buildQuizSection(double textScale) {
    // TV 模式不做题(需求 #10):练习 section 直接返回空,TV 用户只看 summary/advice。
    if (TvMode.instance.isActive) return const SizedBox.shrink();
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
            Text('正在为你生成练习…', style: TextStyle(fontSize: 14, color: AppTheme.textMuted)),
            SizedBox(height: 4),
            Text('AI 正在分析这节课内容并针对你的学习情况出题', style: TextStyle(fontSize: 11, color: AppTheme.slate400)),
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
          const Text('练习', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700, color: AppTheme.slate900)),
          const SizedBox(width: 8),
          if (submitted)
            const Text('已交卷', style: TextStyle(fontSize: 12, color: AppTheme.accentGreen, fontWeight: FontWeight.w600))
          else
            Text('${_answeredCount()}/${questions.length} 已答', style: const TextStyle(fontSize: 12, color: AppTheme.slate400)),
          const Spacer(),
          TextButton.icon(
            onPressed: _regenerate,
            icon: const Icon(Icons.refresh_rounded, size: 14),
            label: const Text('换题', style: TextStyle(fontSize: 12)),
            style: TextButton.styleFrom(foregroundColor: AppTheme.violet500),
          ),
        ]),
      ),
      ...questions.asMap().entries.map((e) {
            // 交卷后的逐题结果:优先用 submit-all 返回的(刚交卷),没有就从
            // question 自身的回填字段合成(重进已交卷页面,_results 为空但后端在
            // QuizView 的 questions 里回填了 correct/correctIndex/explanation)。
            // 多选题额外合成 correctIndices/partial,让重进也能高亮正确项 + 部分对态。
            final result = _results[e.value.id] ??
                (submitted
                    ? QuizAnswerResult(
                        correct: e.value.correct,
                        correctIndex: e.value.correctIndex,
                        correctText: e.value.correctText,
                        explanation: e.value.explanation,
                        chunkStartTime: e.value.chunkStartTime,
                        correctIndices: e.value.correctIndices,
                        partial: e.value.partial,
                        missedCount: e.value.missedCount,
                        extraCount: e.value.extraCount,
                      )
                    : null);
            return Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: _QuestionCard(
              index: e.key,
              question: e.value,
              pick: _choicePicks[e.value.id],
              multiPicks: _multiPicks[e.value.id],
              fillController: e.value.isFill ? (_fillControllers[e.value.id] ??= TextEditingController()) : null,
              result: result,
              submitted: submitted,
              textScale: textScale,
              onPick: (i) {
                setState(() => _choicePicks[e.value.id] = i);
                _persistDraft();
              },
              onMultiToggle: (i) {
                // 多选切换:已选则移除,未选则加入。空集也保留 key(便于落盘语义一致)。
                setState(() {
                  final set = _multiPicks[e.value.id] ?? <int>{};
                  if (set.contains(i)) {
                    set.remove(i);
                  } else {
                    set.add(i);
                  }
                  _multiPicks[e.value.id] = set;
                });
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
                backgroundColor: AppTheme.violet500,
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
      if (q.isMultiChoice) {
        if ((_multiPicks[q.id]?.isNotEmpty ?? false)) n++;
      } else if (q.isFill) {
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
        border: Border.all(color: AppTheme.borderMuted),
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

  const _QuestionCard({
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
            _partialHint(r.missedCount, r.extraCount),
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
String _partialHint(int missed, int extra) {
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
  // AI 页全局文字缩放,透传给历史题的 MarkdownView(stem / explanation)。
  final double textScale;

  const _HistoryQuizCard({
    required this.quiz,
    required this.expanded,
    required this.onToggle,
    required this.onJump,
    required this.textScale,
  });

  @override
  Widget build(BuildContext context) {
    // Header is always visible; questions only when expanded.
    return Container(
      margin: const EdgeInsets.only(top: 8),
      decoration: BoxDecoration(
        color: const Color(0xFFF8FAFC),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.borderMuted),
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
                  size: 18, color: AppTheme.textMuted),
              const SizedBox(width: 4),
              Expanded(
                child: Text(
                  // ArchivedAt is when it was superseded; fall back to generatedAt.
                  quiz.archivedAt.isNotEmpty ? quiz.archivedAt : quiz.generatedAt,
                  style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: Color(0xFF334155)),
                ),
              ),
              Text('${quiz.questionCount} 题', style: const TextStyle(fontSize: 11, color: AppTheme.textMuted)),
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
                  _HistoryQuestionTile(index: i, q: quiz.questions[i], onJump: onJump, textScale: textScale),
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
  // AI 页全局文字缩放,透传给 stem / explanation 的 MarkdownView。
  final double textScale;

  const _HistoryQuestionTile({required this.index, required this.q, required this.onJump, required this.textScale});

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(8),
        border: Border(
          left: BorderSide(width: 3, color: q.wrong ? const Color(0xFFEF4444) : AppTheme.borderMuted),
        ),
      ),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Row(children: [
          Text('第${index + 1}题', style: const TextStyle(fontSize: 10, color: AppTheme.textMuted)),
          const SizedBox(width: 6),
          if (q.wrong)
            const Text('错', style: TextStyle(fontSize: 10, color: Color(0xFFEF4444), fontWeight: FontWeight.w700))
          else
            const Text('已掌握', style: TextStyle(fontSize: 10, color: Color(0xFF059669))),
        ]),
        const SizedBox(height: 6),
        // 历史题干含 markdown(表格题/加粗关键词),用 MarkdownView 渲染;深色保留。
        MarkdownView(
          data: q.stem,
          textScale: textScale,
          baseTextColor: AppTheme.slate900,
        ),
        const SizedBox(height: 8),
        // 按题型分派:填空 → 文本回放;多选 → correctIndices 高亮所有正确项 + 学生错选项标红;
        // 单选 → correctIndex 高亮单个正确项。
        if (q.isFill)
          _buildFillAnswer()
        else if (q.isMultiChoice)
          _buildMultiOptions()
        else
          _buildChoiceOptions(),
        if (q.explanation.isNotEmpty) ...[
          const SizedBox(height: 6),
          // 历史解析含 markdown,用 MarkdownView 渲染;灰色保留。
          MarkdownView(
            data: q.explanation,
            textScale: textScale,
            baseTextColor: AppTheme.textMuted,
          ),
        ],
        if (q.chunkStartTime != null)
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              onPressed: () => onJump(q.chunkStartTime!),
              icon: const Icon(Icons.play_circle_outline_rounded, size: 14, color: AppTheme.blue600),
              label: Text('跳转视频 ${_fmtJump(q.chunkStartTime!)}',
                  style: const TextStyle(fontSize: 11, color: AppTheme.blue600)),
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
            border: Border.all(color: q.correctIndex == i ? AppTheme.accentGreen : AppTheme.borderMuted),
          ),
          child: Row(children: [
            Expanded(child: Text(q.options[i], style: const TextStyle(fontSize: 12, color: Color(0xFF334155)))),
            if (q.correctIndex == i) const Icon(Icons.check_circle, size: 14, color: AppTheme.accentGreen),
          ]),
        ),
    ]);
  }

  // 多选题历史回放:正确项绿(correctIndices),学生错选的红(userIndices 里但不在 correctIndices)。
  // 学生选对的(交集)保持正确项的绿即可;漏选的正确项也按绿展示——视觉上正确项始终是绿。
  Widget _buildMultiOptions() {
    final correctSet = q.correctIndices.toSet();
    final userSet = q.userIndices.toSet();
    return Column(children: [
      for (int i = 0; i < q.options.length; i++)
        Container(
          margin: const EdgeInsets.only(bottom: 4),
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 7),
          decoration: BoxDecoration(
            color: correctSet.contains(i)
                ? const Color(0xFFECFDF5)
                : userSet.contains(i)
                    ? const Color(0xFFFEF2F2)
                    : Colors.white,
            borderRadius: BorderRadius.circular(6),
            border: Border.all(
              color: correctSet.contains(i)
                  ? AppTheme.accentGreen
                  : userSet.contains(i)
                      ? const Color(0xFFEF4444)
                      : AppTheme.borderMuted,
            ),
          ),
          child: Row(children: [
            Expanded(child: Text(q.options[i], style: const TextStyle(fontSize: 12, color: Color(0xFF334155)))),
            if (correctSet.contains(i))
              const Icon(Icons.check_circle, size: 14, color: AppTheme.accentGreen)
            else if (userSet.contains(i))
              const Icon(Icons.cancel, size: 14, color: Color(0xFFEF4444)),
          ]),
        ),
    ]);
  }

  Widget _buildFillAnswer() {
    // 填空题历史回放:展示学生当时填的原文(q.userText,后端 Answer.UserAnswerText 回填)
    // + 正确答案。漏答(userText 空)只显示正确答案,不显示空行。
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if (q.userText.isNotEmpty) ...[
        Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 7),
          decoration: BoxDecoration(
            color: q.wrong ? const Color(0xFFFEF2F2) : const Color(0xFFECFDF5),
            borderRadius: BorderRadius.circular(6),
            border: Border.all(color: q.wrong ? const Color(0xFFFECACA) : const Color(0xFFBBF7D0)),
          ),
          child: Text('你填的: ${q.userText}',
              style: TextStyle(fontSize: 12, color: q.wrong ? const Color(0xFFB91C1C) : const Color(0xFF15803D))),
        ),
        const SizedBox(height: 4),
      ],
      Container(
        width: double.infinity,
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 7),
        decoration: BoxDecoration(
          color: const Color(0xFFECFDF5),
          borderRadius: BorderRadius.circular(6),
          border: Border.all(color: AppTheme.accentGreen),
        ),
        child: Text('正确答案: ${q.correctText}', style: const TextStyle(fontSize: 12, color: Color(0xFF059669))),
      ),
    ]);
  }
}

/// 渲染单个"要点"字符串:文本就走 bullet + Expanded 的紧凑布局;
/// 含 block 元素(GFM 表格 / SVG 图 / 代码块)就走整行宽度,避免表格在
/// Row+Expanded 的窄约束里溢出盖到相邻文字。
///
/// 问题根因:`IntrinsicColumnWidth` 表格在 Row+Expanded(给子节点 tight maxWidth
/// 约束、但 Table 需要 unbounded 宽度去量每列 intrinsic 宽度)里,flutter_markdown
/// 的横向 SingleChildScrollView 仍可能算出错误的外部尺寸,Android pad 上尤其明显
/// (用户实测:表格直接盖在文字上)。把表格放到 Row 外、占满父容器宽度就好。
class _PointItem extends StatelessWidget {
  final String data;
  final double textScale;
  final Color textColor;

  const _PointItem({
    required this.data,
    required this.textScale,
    required this.textColor,
  });

  /// GFM 表格至少两行:数据行 `| ... |` 和分隔行 `| --- |`。检测分隔行即可
  /// 覆盖所有表格(分隔行是表格语法的强标志)。
  bool get _hasBlockElement {
    for (final line in data.split('\n')) {
      final t = line.trim();
      // 表格分隔行:| --- | :---: | ---: 之类
      if (t.startsWith('|') && RegExp(r'^\|[\s:|-]+\|?\s*$').hasMatch(t) && t.contains('-')) {
        return true;
      }
      // SVG 围栏代码块开头
      if (t.startsWith('```')) return true;
    }
    return false;
  }

  @override
  Widget build(BuildContext context) {
    if (_hasBlockElement) {
      // 整行宽度渲染,带左缩进让视觉上仍在 bullet 列里。
      return Padding(
        padding: const EdgeInsets.only(bottom: 3, left: 10),
        child: MarkdownView(
          data: data,
          textScale: textScale,
          baseTextColor: textColor,
        ),
      );
    }
    // 纯文本/内联 markdown:紧凑布局,bullet + Expanded。
    return Padding(
      padding: const EdgeInsets.only(bottom: 3, left: 10),
      child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
        const Text('· ', style: TextStyle(color: AppTheme.violet500, fontWeight: FontWeight.bold)),
        Expanded(
          child: MarkdownView(
            data: data,
            textScale: textScale,
            baseTextColor: textColor,
          ),
        ),
      ]),
    );
  }
}
