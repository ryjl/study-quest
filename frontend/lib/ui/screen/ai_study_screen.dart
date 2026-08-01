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
import '../widget/quiz_card.dart';
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
//
// 交卷即归档:submit-all 成功后后端把该 quiz 翻成 archived,它立即进入
// 「历史练习」面板(可点开 review 逐题结果)。重进页面时 GetOrEnqueueQuiz 因「无 active
// 但有历史」返回 done——当前练习区渲染「已完成、点重新生成」入口,不自动出新题;只有
// 首次(从未做过)才自动 enqueue 生成。学生点「重新生成」才生成新一套。已交卷卷子的
// 复习视图只在历史面板里(交卷态下当前区仍展示本次阅卷结果,返回再进则进历史)。
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
  // summary 加载失败标志:区分"真没总结"(null + 无误)和"网络挂了"(error)。
  // 原来失败时直接隐藏卡片,学生分不清是这节没总结还是断网,也没有重试入口。
  bool _summaryError = false;

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
    if (mounted) setState(() => _summaryError = false);
    try {
      final s = await ApiService.fetchEpisodeSummary(widget.activeUserId, widget.episode.id);
      if (mounted) setState(() => _summary = s);
    } catch (_) {
      // 失败不静默隐藏:设置错误态,_buildSummarySection 会渲染"加载失败·重试"卡片,
      // 让学生能区分"这节没总结"和"网络挂了",并给重试入口。
      if (mounted) setState(() => _summaryError = true);
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
      // 清掉残留草稿防错乱。交卷即归档后,重进页面通常返回 done(无 active quiz),
      // 这条分支是防御性兜底——若后端因故仍回 active+submitted 的卷子(比如刚交完卷
      // 还没切走、本地 _quiz 仍在内存,但后续若重 fetch),也能正确回填学生当时的选择
      // 让错项红框高亮。未交卷:尝试恢复本地草稿(防切后台丢答案)。
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
        // generating 之外的任何状态都该停轮询:
        //   ready → 题好了,渲染;cooling → 熔断了,后端不会再入队,继续轮询无意义
        //     (否则会每 3s 打一次 DB 永不停止);unavailable/done → 同理。
        // 继续轮询的唯一理由是「还在 generating」,其它状态都有终态含义。
        if (resp.status != QuizStatus.generating) {
          _pollTimer?.cancel();
          setState(() {
            _quizStatus = resp.status;
            _quiz = resp.quiz;
          });
          if (resp.status == QuizStatus.ready) {
            // A new active quiz just landed, which means the prior one was
            // archived server-side. Refresh history so the panel reflects it.
            _loadHistory();
          }
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
        // 同 quiz 的 _startPolling:generating 之外的状态(ready/cooling/unavailable)
        // 都停轮询。cooling 时后端已熔断不会再生效,继续轮询只会空打 DB。
        if (resp.status != AdviceStatus.generating) {
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
      // 交卷即归档:后端已把这套卷子翻成 archived。刷新历史面板,让它立即出现在
      // 「历史练习」里(返回再进页面也能从历史点开 review)。best-effort,不阻断。
      _loadHistory();
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
    final colors = context.colors;
    // TV 模式强制最大档(1.4)——TV 上靠远距离观看,且 TV 不渲染字号调整按钮,
    // 用一个 effectiveScale 统一覆盖所有 MarkdownView / Text 的缩放,避免读 UiPrefs。
    final double effectiveScale =
        TvMode.instance.isActive ? 1.4 : UiPrefs.instance.aiTextScale;
    return Scaffold(
      backgroundColor: colors.backgroundColor,
      appBar: AppBar(
        backgroundColor: colors.cardColor,
        elevation: 0,
        leading: IconButton(
          icon: Icon(Icons.arrow_back_rounded, color: colors.textMuted),
          onPressed: () => Navigator.of(context).pop(),
        ),
        title: Text(
          widget.episode.title,
          style: TextStyle(color: colors.textWhite, fontSize: 16, fontWeight: FontWeight.w600),
        ),
        // 字号调整按钮(需求 #6)。TV 模式下隐藏——TV 永远走最大档,调了也没用。
        actions: [
          if (!TvMode.instance.isActive)
            IconButton(
              icon: Icon(Icons.format_size_rounded, color: colors.textMuted),
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
          // 【TV 只读模式】TV 下只保留"看总结 + 学习建议",练习(quiz)和历史
          // (历史也是 quiz)整 section 不渲染 —— TV 做题体验差,且 MainNavigation
          // 已在 tab 层裁掉错题本,这里和那条策略一致。PAD/手机不受影响。
          if (!TvMode.instance.isActive) ...[
            const SizedBox(height: 16),
            // 历史练习在上(默认折叠):交卷即归档后,已交卷的卷子进这里,重进页面先看到
            // 历史可点开 review,当前练习区在下面才是「做新题」入口。
            _buildHistorySection(effectiveScale),
            const SizedBox(height: 16),
            _buildQuizSection(effectiveScale),
          ],
        ],
      ),
    );
  }

  // 字号菜单(需求 #6):ModalBottomSheet 里 4 档 ListTile,当前档打勾。
  // 选中后写 SharedPreferences 持久化 + setState 触发整页重建,所有 MarkdownView
  // 的 textScale 跟着更新。沿用 _showFilterBottomSheet 的风格(圆角顶部 + 白底)。
  void _showTextScaleMenu(BuildContext context) {
    final colors = context.colors;
    showModalBottomSheet(
      context: context,
      backgroundColor: Colors.transparent,
      builder: (sheetCtx) {
        return Container(
          decoration: BoxDecoration(
            color: colors.cardColor,
            borderRadius: const BorderRadius.vertical(top: Radius.circular(24)),
          ),
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 16, 20, 8),
                child: Row(children: [
                  Icon(Icons.format_size_rounded, size: 18, color: colors.textWhite),
                  const SizedBox(width: 8),
                  Text('字号', style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: colors.textWhite)),
                ]),
              ),
              for (int i = 0; i < UiPrefs.aiScaleLabels.length; i++)
                ListTile(
                  leading: Icon(
                    // 4 档依次给个大小递增的字号图标,直观表达档位语义。
                    [Icons.text_fields_rounded, Icons.text_fields_rounded, Icons.text_fields_rounded, Icons.text_fields_rounded][i],
                    size: [16, 20, 24, 28][i].toDouble(),
                    color: UiPrefs.instance.aiTextScaleIndex == i ? AppTheme.violet500 : colors.textMuted,
                  ),
                  title: Text(
                    UiPrefs.aiScaleLabels[i],
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: UiPrefs.instance.aiTextScaleIndex == i ? FontWeight.w700 : FontWeight.w500,
                      color: UiPrefs.instance.aiTextScaleIndex == i ? AppTheme.violet500 : colors.textWhite,
                    ),
                  ),
                  trailing: UiPrefs.instance.aiTextScaleIndex == i
                      ? Icon(Icons.check_rounded, color: AppTheme.violet500)
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
    final colors = context.colors;
    return _Card(
      child: Theme(
        // Keep the per-quiz ExpansionTile visuals consistent with the card.
        data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
        child: ExpansionTile(
          initiallyExpanded: false,
          tilePadding: EdgeInsets.zero,
          iconColor: colors.textMuted,
          collapsedIconColor: colors.textMuted,
          title: Row(children: [
            Icon(Icons.history_rounded, size: 16, color: colors.textMuted),
            const SizedBox(width: 6),
            Text('历史练习', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w700, color: colors.textWhite)),
            const SizedBox(width: 8),
            if (_historyLoading)
              const SizedBox(width: 12, height: 12, child: CircularProgressIndicator(strokeWidth: 2))
            else
              Text('${_history.length} 套', style: TextStyle(fontSize: 12, color: colors.textMuted)),
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
    // 加载失败:给明确的"加载失败·重试"卡片,而非静默消失。
    // (区分:summary==null && !_summaryError → 这节真没总结,AI 没开/没素材属正常,隐藏。)
    if (_summaryError) {
      final colors = context.colors;
      return _Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Row(children: [
            Icon(Icons.cloud_off_rounded, size: 18, color: colors.textMuted),
            const SizedBox(width: 8),
            Expanded(child: Text('总结加载失败,请检查网络后重试',
                style: TextStyle(fontSize: 13, color: colors.textMuted))),
            TextButton.icon(
              onPressed: () {
                setState(() => _summaryLoading = true);
                _loadSummary();
              },
              icon: const Icon(Icons.refresh_rounded, size: 16),
              label: const Text('重试'),
            ),
          ]),
        ),
      );
    }
    final s = _summary;
    if (s == null || s.isEmpty) {
      return const SizedBox.shrink(); // no summary → hide, AI add-on absence is normal
    }
    final colors = context.colors;
    return _Card(
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Row(children: [
          Icon(Icons.auto_awesome_rounded, size: 16, color: AppTheme.violet500),
          const SizedBox(width: 6),
          Text('AI 重点总结', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700, color: colors.textWhite)),
        ]),
        if (s.headline.isNotEmpty) ...[
          const SizedBox(height: 10),
          Text(s.headline, style: TextStyle(fontSize: 14, fontWeight: FontWeight.w600, color: colors.textWhite)),
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
                        textColor: colors.textWhite,
                      )),
                ]),
              )),
        ] else if (s.keyPoints.isNotEmpty) ...[
          const SizedBox(height: 10),
          ...s.keyPoints.map((p) => _PointItem(
                data: p,
                textScale: textScale,
                textColor: colors.textWhite,
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
                      decoration: BoxDecoration(color: colors.blue100, borderRadius: BorderRadius.circular(6)),
                      child: Text(c, style: TextStyle(fontSize: 11, color: colors.blue600)),
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
    final colors = context.colors;
    // generating 态:advice 正在生成,展示占位卡片(比直接隐藏更连贯)。
    if (_adviceStatus == AdviceStatus.generating) {
      return _Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Row(children: [
            const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2)),
            const SizedBox(width: 10),
            Text('正在分析你的学习情况…', style: TextStyle(fontSize: 13, color: colors.textMuted)),
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
        Row(children: [
          Icon(Icons.lightbulb_outline_rounded, size: 16, color: const Color(0xFFF59E0B)),
          const SizedBox(width: 6),
          Text('AI 学习建议', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w700, color: colors.textWhite)),
        ]),
        const SizedBox(height: 8),
        // 学习建议正文是 agent 跨知识点综合分析,鼓励输出 markdown 列表/加粗/对比表格。
        // 用 MarkdownView 渲染,字号跟随 AI 页全局缩放(textScale)。
        MarkdownView(
          data: advice,
          textScale: textScale,
          baseTextColor: colors.textWhite,
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
      final colors = context.colors;
      return _Card(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(children: [
            const CircularProgressIndicator(),
            const SizedBox(height: 12),
            Text('正在为你生成练习…', style: TextStyle(fontSize: 14, color: colors.textMuted)),
            const SizedBox(height: 4),
            Text('AI 正在分析这节课内容并针对你的学习情况出题', style: TextStyle(fontSize: 11, color: colors.textMuted)),
          ]),
        ),
      );
    }
    if (_quizStatus == QuizStatus.done) {
      // 已做过(有历史归档)但无 active quiz:交卷即归档后或换题归档后的状态。
      // 不自动出新题——渲染「已完成」入口,学生点「重新生成」才出新一套。
      return _buildDoneCard();
    }
    if (_quizStatus == QuizStatus.cooling) {
      // 连续多次生成失败已熔断:后端拒绝自动重试(避免反复入队烧 token)。
      // 不静默隐藏——明确告诉学生「AI 出题卡住了」,并给手动重试入口(点重试走
      // RegenerateQuiz,绕过冷却)。这是 cooling 区别于 unavailable 的意义:
      // unavailable 是「AI 没开/没字幕」,静默隐藏即可;cooling 是「试过但失败了」,
      // 需要让学生知道并给恢复手段。
      return _buildCoolingCard();
    }
    if (_quizStatus != QuizStatus.ready || _quiz == null) {
      // unavailable — AI off or no source material. Hide quietly (add-on layer).
      return const SizedBox.shrink();
    }
    final questions = _quiz!.questions;
    // 交卷态:后端 quiz.submitted(重进已交卷的卷子)或本地 _submitted(刚点完提交全部)。
    final submitted = _quiz!.submitted || _submitted;
    final colors = context.colors;
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Padding(
        padding: const EdgeInsets.only(left: 4, bottom: 8),
        child: Row(children: [
          Text('练习', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700, color: colors.textWhite)),
          const SizedBox(width: 8),
          if (submitted)
            Text('已交卷', style: TextStyle(fontSize: 12, color: AppTheme.accentGreen, fontWeight: FontWeight.w600))
          else
            Text('${_answeredCount()}/${questions.length} 已答', style: TextStyle(fontSize: 12, color: colors.textMuted)),
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
            // 作答态:choice 用 _choicePicks,multi 用 _multiPicks,fill 用 controller。
            // 交卷态:优先用回填的 userAnswerIndex/userAnswerIndices/userAnswerText。
            final userChoice = _choicePicks[e.value.id] ?? e.value.userAnswerIndex;
            final userMulti = e.value.isMultiChoice
                ? (_multiPicks[e.value.id] ?? e.value.userAnswerIndices.toSet())
                : <int>{};
            final userFill = e.value.isFill
                ? (e.value.userAnswerText.isNotEmpty
                    ? e.value.userAnswerText
                    : (_fillControllers[e.value.id]?.text ?? ''))
                : '';
            return Padding(
              padding: const EdgeInsets.only(bottom: 12),
              child: QuizReviewCard(
                stem: QuizStemData(
                  index: e.key,
                  type: e.value.type,
                  stem: e.value.stem,
                  options: e.value.options,
                  chunkStartTime: e.value.chunkStartTime,
                  hasJump: e.value.hasJump,
                ),
                verdict: QuizVerdictData(
                  submitted: submitted,
                  userChoiceIndex: userChoice,
                  userMultiIndices: userMulti,
                  userFillText: userFill,
                  correctIndex: result?.correctIndex ?? e.value.correctIndex,
                  correctIndices: (result?.correctIndices ?? e.value.correctIndices).toSet(),
                  correctText: result?.correctText ?? e.value.correctText,
                  explanation: result?.explanation ?? e.value.explanation,
                  correct: result?.correct ?? (submitted ? e.value.correct : null),
                  partial: result?.partial ?? e.value.partial,
                ),
                mode: submitted ? QuizReviewMode.submitted : QuizReviewMode.interactive,
                textScale: textScale,
                onPickChoice: (i) {
                  setState(() => _choicePicks[e.value.id] = i);
                  _persistDraft();
                },
                onToggleMulti: (i) {
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
                fillController: e.value.isFill ? (_fillControllers[e.value.id] ??= TextEditingController()) : null,
                onFillChanged: () {
                  // 只触发 rebuild 让按钮状态更新;实际落盘由 onChanged 里 debounce 调用。
                  setState(() {});
                  _persistDraft();
                },
                // 跳转按钮:仅 hasJump + 有 chunkStartTime + 交卷态时给(组件内已 gate onJump!=null)。
                onJump: (e.value.hasJump && e.value.chunkStartTime != null && submitted)
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

  // done 态入口卡:已做过本节练习(上次交卷已归档),但当前没有 active quiz。
  // 不自动出新题——引导学生点「重新生成」主动做新一套,或去上方历史练习 review。
  Widget _buildDoneCard() {
    final colors = context.colors;
    return _Card(
      child: Padding(
        padding: const EdgeInsets.all(20),
        child: Column(
          children: [
            const Icon(Icons.task_alt_rounded, size: 40, color: AppTheme.accentGreen),
            const SizedBox(height: 12),
            Text('本节练习已做完一套',
                style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: colors.textWhite)),
            const SizedBox(height: 6),
            Text(
              '上次的成绩已存进上方「历史练习」,点开可查看对错。\n想做一套新题?点下面按钮重新生成。',
              textAlign: TextAlign.center,
              style: TextStyle(fontSize: 12, color: colors.textMuted, height: 1.5),
            ),
            const SizedBox(height: 16),
            SizedBox(
              width: double.infinity,
              child: ElevatedButton.icon(
                onPressed: _regenerate,
                icon: const Icon(Icons.refresh_rounded, size: 18),
                label: const Text('重新生成一套题', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w700)),
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppTheme.violet500,
                  foregroundColor: Colors.white,
                  elevation: 0,
                  padding: const EdgeInsets.symmetric(vertical: 12),
                  shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  // cooling 卡片:quiz 连续失败 ≥3 次被后端熔断。和 done 卡片的关键区别——
  // done 是「正常结束、想做了再生成」的正面状态;cooling 是「出错了、暂停了」的
  // 异常状态,语气要承认问题、给重试出口,不静默隐藏(否则学生以为 AI 没开)。
  // 点「重试生成」走 _regenerate → RegenerateQuiz,这条路径后端故意绕过冷却
  // (admin/用户主动行为 = escape hatch),所以冷却中学生仍能手动恢复。
  Widget _buildCoolingCard() {
    final colors = context.colors;
    return _Card(
      child: Padding(
        padding: const EdgeInsets.all(20),
        child: Column(
          children: [
            Icon(Icons.sentiment_neutral_rounded, size: 40, color: colors.textMuted),
            const SizedBox(height: 12),
            Text('AI 出题暂时卡住了',
                style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: colors.textWhite)),
            const SizedBox(height: 6),
            Text(
              '这节课的内容比较长,AI 多次尝试生成练习都没成功,已暂停自动重试。\n'
              '可以点下面按钮手动重试一次,或联系老师检查 AI 配置。',
              textAlign: TextAlign.center,
              style: TextStyle(fontSize: 12, color: colors.textMuted, height: 1.5),
            ),
            const SizedBox(height: 16),
            SizedBox(
              width: double.infinity,
              child: ElevatedButton.icon(
                onPressed: _regenerate,
                icon: const Icon(Icons.refresh_rounded, size: 18),
                label: const Text('重试生成', style: TextStyle(fontSize: 14, fontWeight: FontWeight.w700)),
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppTheme.violet500,
                  foregroundColor: Colors.white,
                  elevation: 0,
                  padding: const EdgeInsets.symmetric(vertical: 12),
                  shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// --- Reusable card container ---
class _Card extends StatelessWidget {
  final Widget child;
  const _Card({required this.child});
  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.cardColor,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: colors.borderMuted),
      ),
      child: child,
    );
  }
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
    final colors = context.colors;
    // Header is always visible; questions only when expanded.
    return Container(
      margin: const EdgeInsets.only(top: 8),
      decoration: BoxDecoration(
        color: colors.cardColor,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: colors.borderMuted),
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
                  size: 18, color: colors.textMuted),
              const SizedBox(width: 4),
              Expanded(
                child: Text(
                  // ArchivedAt is when it was superseded; fall back to generatedAt.
                  quiz.archivedAt.isNotEmpty ? quiz.archivedAt : quiz.generatedAt,
                  style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: colors.textWhite),
                ),
              ),
              Text('${quiz.questionCount} 题', style: TextStyle(fontSize: 11, color: colors.textMuted)),
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
                  _historyReviewCard(quiz.questions[i], i),
              ],
            ),
          ),
      ]),
    );
  }

  // 历史题用统一的 QuizReviewCard 渲染(submitted 态,全揭示)。适配 ArchivedQuizQuestion:
  // correct = !wrong(归档模型用反向 wrong 字段),userChoiceIndex = userIndex,
  // userMultiIndices = userIndices。视觉和当前练习交卷态/错题本/考试完全一致。
  Widget _historyReviewCard(ArchivedQuizQuestion q, int i) {
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: QuizReviewCard(
        stem: QuizStemData(
          index: i,
          type: q.type,
          stem: q.stem,
          options: q.options,
          chunkStartTime: q.chunkStartTime,
          hasJump: q.hasJump,
        ),
        verdict: QuizVerdictData(
          submitted: true,
          userChoiceIndex: q.userIndex,
          userMultiIndices: q.userIndices.toSet(),
          userFillText: q.userText,
          correctIndex: q.correctIndex,
          correctIndices: q.correctIndices.toSet(),
          correctText: q.correctText,
          explanation: q.explanation,
          // ArchivedQuizQuestion 用 wrong(反向);correct = !wrong。未判分时(null 不可能,
          // 归档题一定有对错结果)按 wrong 兜底为 false。
          correct: !q.wrong,
        ),
        mode: QuizReviewMode.submitted,
        textScale: textScale,
        onJump: (q.hasJump && q.chunkStartTime != null) ? () => onJump(q.chunkStartTime!) : null,
      ),
    );
  }
}

/// 渲染单个"要点"字符串:文本就走 bullet + Expanded 的紧凑布局;
/// 含 block 元素(GFM 表格 / SVG 图 / 代码块)就走整行宽度,避免表格在
/// Row+Expanded 的窄约束里溢出盖到相邻文字。
///
/// 问题根因:`IntrinsicColumnWidth` 表格在 Row+Expanded(给子节点 tight maxWidth
/// 约束、但 Table 需要 unbounded 宽度去量每列 intrinsic 宽度)里,flutter_markdown
/// 的横向 SingleChildScrollView 仍可能算出错误的外部尺寸,Android pad 上尤其明显
/// (表格会直接盖在文字上)。把表格放到 Row 外、占满父容器宽度就好。
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
