import 'package:flutter/material.dart';

import '../../model/course.dart';
import '../../model/wrong_book.dart';
import '../../service/api_service.dart';
import '../../service/tv_mode.dart';
import '../../theme.dart';
import '../widget/button_3d.dart';
import '../widget/markdown_view.dart';
import '../widget/state_widgets.dart';
import '../responsive.dart';

/// 错题本屏(TODO.md P0)。学生做错的题自动归集于此。体验闭环:
///  - 列表浏览(默认「全部」:已掌握的灰显不消失;可按课程/掌握状态过滤)
///  - 卡片点开看正确答案 + 解析(默认收起,展开复习)
///  - 单题重做(「重做本题」)或整批重做(顶部「重做一批」,连对 3 次才掌握)
///  - 手动标记掌握 / 取消(明确按钮,不再用隐晦圆圈)
///
/// PAD/TV 友好:
///  - 响应式 GridView(PAD 横屏多列)
///  - TV 模式:做题交互隐藏 + 提示去平板/手机(TV 复习错题场景不成立)
class WrongBookScreen extends StatefulWidget {
  final int activeUserId;
  const WrongBookScreen({super.key, required this.activeUserId});

  @override
  State<WrongBookScreen> createState() => _WrongBookScreenState();
}

class _WrongBookScreenState extends State<WrongBookScreen> {
  Future<WrongBookList>? _future;
  // null=全部(默认),false=未掌握,true=已掌握。
  bool? _masteredFilter = null;
  // 课程过滤:0=全部课程。
  int _courseFilter = 0;
  List<Course> _courses = const [];
  // 展开看答案的题(questionId 集合)。
  final Set<int> _expanded = {};

  @override
  void initState() {
    super.initState();
    _loadCourses();
    _loadData();
  }

  void _loadCourses() async {
    try {
      final list = await ApiService.fetchCourses(widget.activeUserId);
      if (mounted) setState(() => _courses = list);
    } catch (_) {
      // 课程列表失败不阻塞,课程过滤 chip 用空列表(只显示「全部」)。
    }
  }

  void _loadData() {
    _expanded.clear();
    _future = ApiService.fetchWrongBook(
      widget.activeUserId,
      courseId: _courseFilter,
      mastered: _masteredFilter,
    );
  }

  Future<void> _refresh() async {
    setState(_loadData);
  }

  Future<void> _toggleMastered(WrongBookItem item) async {
    final newMastered = !item.mastered;
    // 乐观更新:先局部改状态(不重拉、不丢展开状态),后台同步。
    setState(() {
      final list = _lastList;
      if (list != null) {
        final i = list.items.indexWhere((x) => x.questionId == item.questionId);
        if (i >= 0) {
          _lastList = WrongBookList(
            items: List.of(list.items)..[i] = _copyWithMastered(list.items[i], newMastered),
            unmasteredCount: list.unmasteredCount + (newMastered ? -1 : 1),
          );
        }
      }
    });
    try {
      await ApiService.markWrongBookMastered(widget.activeUserId, item.questionId, newMastered);
    } catch (_) {
      // 失败回滚:重拉保证和后端一致。
      if (mounted) _refresh();
    }
  }

  // _lastList 缓存最近一次的列表数据,供乐观更新用(FutureBuilder 拿不到当前值)。
  WrongBookList? _lastList;

  WrongBookItem _copyWithMastered(WrongBookItem src, bool mastered) {
    // WrongBookItem 字段全 final,手动 copy(只改 mastered + streak 归 0)。
    return WrongBookItem(
      questionId: src.questionId, stem: src.stem, type: src.type,
      options: src.options, explanation: src.explanation,
      correctIndex: src.correctIndex, correctText: src.correctText,
      correctIndices: src.correctIndices, hasJump: src.hasJump,
      chunkId: src.chunkId, courseId: src.courseId, episodeId: src.episodeId,
      subjectId: src.subjectId, firstWrongAt: src.firstWrongAt,
      lastAttemptedAt: src.lastAttemptedAt, attemptCount: src.attemptCount,
      correctStreak: 0, mastered: mastered,
    );
  }

  void _toggleExpand(int qid) {
    setState(() {
      if (_expanded.contains(qid)) {
        _expanded.remove(qid);
      } else {
        _expanded.add(qid);
      }
    });
  }

  Future<void> _redoSingle(WrongBookItem item) async {
    if (TvMode.instance.isActive) return;
    // 单题重做:取这道题的题面构造重做卷。fetchWrongBookRedo 是随机抽,单题重做用
    // 本地已有的题面构造 WrongBookRedoQuestion,直接进重做屏。
    final q = WrongBookRedoQuestion(
      id: item.questionId,
      type: item.type,
      stem: item.stem,
      options: item.options,
      hasJump: item.hasJump,
    );
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => _WrongBookRedoScreen(
        activeUserId: widget.activeUserId,
        questions: [q],
      ),
    ));
    _refresh(); // 重做可能改了 streak/mastered,刷新列表
  }

  Future<void> _startBatchRedo() async {
    if (TvMode.instance.isActive) return;
    final redoQuestions = await ApiService.fetchWrongBookRedo(
      widget.activeUserId, courseId: _courseFilter, limit: 10,
    );
    if (!mounted) return;
    if (redoQuestions.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('暂无可重做的错题')),
      );
      return;
    }
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => _WrongBookRedoScreen(
        activeUserId: widget.activeUserId,
        questions: redoQuestions,
      ),
    ));
    _refresh();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.transparent,
      body: FutureBuilder<WrongBookList>(
        future: _future,
        builder: (context, snapshot) {
          if (snapshot.connectionState != ConnectionState.done) {
            return loadingSpinner();
          }
          if (snapshot.hasError) {
            return errorStateBox(
              snapshot.error.toString(), _refresh,
              message: '加载错题本失败',
            );
          }
          final list = snapshot.data;
          final items = list?.items ?? const <WrongBookItem>[];
          if (items.isEmpty) {
            return _buildEmpty();
          }
          _lastList = list;
          return _buildList(items, list?.unmasteredCount ?? 0);
        },
      ),
    );
  }

  // 空状态:引导去学习(不再是无用的「刷新」按钮)。
  Widget _buildEmpty() {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.spellcheck_rounded, size: 56, color: AppTheme.textMuted),
            const SizedBox(height: 16),
            const Text('还没有错题', style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold)),
            const SizedBox(height: 8),
            const Text(
              '去「学习大厅」做 AI 练习或课程考试,\n做错的题会自动出现在这里,方便复习。',
              textAlign: TextAlign.center,
              style: TextStyle(color: AppTheme.textMuted, fontSize: 14, height: 1.5),
            ),
            const SizedBox(height: 24),
            Button3D.blue(
              onPressed: _refresh,
              child: const Padding(
                padding: EdgeInsets.symmetric(horizontal: 24, vertical: 10),
                child: Text('刷新', style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold)),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildList(List<WrongBookItem> items, int unmasteredCount) {
    final mastered = items.where((i) => i.mastered).length;
    return Padding(
      padding: portraitAwarePadding(context),
      child: FocusTraversalGroup(
        child: CustomScrollView(
          slivers: [
            SliverToBoxAdapter(child: _buildHeader(items.length, mastered, unmasteredCount)),
            // 用 SliverList 而非 SliverGrid:错题本是阅读复习场景,展开看答案+解析需要
            // 纵向空间,固定 childAspectRatio 的 grid 在「部分展开」时会留白或溢出。
            // 单列每卡自适应高度,PAD 横屏用 maxWidth 限宽居中(可读性)。
            SliverPadding(
              padding: const EdgeInsets.only(top: 8),
              sliver: SliverList(
                delegate: SliverChildBuilderDelegate(
                  (context, index) => _buildCard(items[index]),
                  childCount: items.length,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildHeader(int total, int masteredCount, int unmasteredCount) {
    final tv = TvMode.instance.isActive;
    return Padding(
      padding: const EdgeInsets.only(top: 16, bottom: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              const Text('错题本', style: TextStyle(fontSize: 24, fontWeight: FontWeight.bold)),
              const SizedBox(width: 12),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                decoration: BoxDecoration(
                  color: AppTheme.slate100, borderRadius: BorderRadius.circular(12),
                ),
                child: Text('共 $total 题',
                  style: const TextStyle(fontSize: 13, color: AppTheme.textMuted)),
              ),
              const SizedBox(width: 8),
              // 进度:已掌握 / 未掌握,让掌握进度可见。
              if (masteredCount > 0)
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                  decoration: BoxDecoration(
                    color: const Color(0xFFD1FAE5), borderRadius: BorderRadius.circular(12),
                  ),
                  child: Text('已掌握 $masteredCount',
                    style: const TextStyle(fontSize: 13, color: Color(0xFF065F46), fontWeight: FontWeight.bold)),
                ),
              const Spacer(),
              // 整批重做:TV 模式隐藏(电视做题体验差),给提示。
              if (tv)
                const Padding(
                  padding: EdgeInsets.only(left: 8),
                  child: Text('重做请用平板/手机',
                    style: TextStyle(color: AppTheme.textMuted, fontSize: 12)),
                )
              else
                Button3D.blue(
                  onPressed: _startBatchRedo,
                  child: const Padding(
                    padding: EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(Icons.refresh_rounded, size: 18, color: Colors.white),
                        SizedBox(width: 6),
                        Text('重做一批', style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold)),
                      ],
                    ),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 12),
          // 过滤行:掌握状态三段 + 课程过滤。
          Wrap(
            spacing: 8, runSpacing: 8,
            children: [
              _buildFilterChip(),
              if (_courses.isNotEmpty) _buildCourseChip(),
            ],
          ),
        ],
      ),
    );
  }

  // 掌握状态过滤:未掌握(N) / 已掌握(N) / 全部。带计数(从全部视图算)。
  Widget _buildFilterChip() {
    return Container(
      decoration: BoxDecoration(
        color: AppTheme.slate100, borderRadius: BorderRadius.circular(20),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          _filterSeg('未掌握', false),
          _filterSeg('已掌握', true),
          _filterSeg('全部', null),
        ],
      ),
    );
  }

  Widget _filterSeg(String label, bool? value) {
    final selected = _masteredFilter == value;
    return GestureDetector(
      onTap: () {
        setState(() {
          _masteredFilter = value;
          _loadData();
        });
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        margin: const EdgeInsets.symmetric(vertical: 2),
        decoration: BoxDecoration(
          color: selected ? AppTheme.blue600 : Colors.transparent,
          borderRadius: BorderRadius.circular(18),
        ),
        child: Text(label, style: TextStyle(
          fontSize: 12, color: selected ? Colors.white : AppTheme.textMuted,
          fontWeight: selected ? FontWeight.bold : FontWeight.normal,
        )),
      ),
    );
  }

  // 课程过滤下拉。
  Widget _buildCourseChip() {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12),
      decoration: BoxDecoration(
        color: AppTheme.slate100, borderRadius: BorderRadius.circular(20),
      ),
      child: DropdownButton<int>(
        value: _courseFilter,
        underline: const SizedBox(),
        isDense: true,
        style: const TextStyle(fontSize: 12, color: AppTheme.textMuted),
        items: [
          const DropdownMenuItem(value: 0, child: Text('全部课程')),
          ..._courses.map((c) => DropdownMenuItem(value: c.id, child: Text(c.title))),
        ],
        onChanged: (v) {
          if (v == null) return;
          setState(() {
            _courseFilter = v;
            _loadData();
          });
        },
      ),
    );
  }

  Widget _buildCard(WrongBookItem item) {
    final expanded = _expanded.contains(item.questionId);
    final courseName = _courses.isEmpty ? null : _coursesTitle(item.courseId);
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 720),
        child: Container(
          margin: const EdgeInsets.only(bottom: 14),
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: Colors.white,
            borderRadius: BorderRadius.circular(20),
            border: Border.all(
              color: item.mastered ? const Color(0xFFD1FAE5) : AppTheme.borderMuted,
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
          // 来源 chip + 错次/streak 徽标。
          Row(
            children: [
              if (courseName != null)
                Flexible(
                  child: Container(
                    padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                    decoration: BoxDecoration(
                      color: AppTheme.slate100, borderRadius: BorderRadius.circular(8),
                    ),
                    child: Text(courseName,
                      maxLines: 1, overflow: TextOverflow.ellipsis,
                      style: const TextStyle(fontSize: 11, color: AppTheme.textMuted)),
                  ),
                ),
              const Spacer(),
              _buildStatusBadge(item),
            ],
          ),
          const SizedBox(height: 8),
          // 题面。
          MarkdownView(data: item.stem, baseTextColor: AppTheme.slate900, textScale: 1.0),
          const SizedBox(height: 10),
          // 「查看答案 / 收起」切换。
          if (item.type != 'fill' || item.correctText.isNotEmpty || item.correctIndex != null)
            GestureDetector(
              onTap: () => _toggleExpand(item.questionId),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(expanded ? Icons.expand_less : Icons.lightbulb_outline_rounded,
                      size: 18, color: AppTheme.blue600),
                  const SizedBox(width: 4),
                  Text(expanded ? '收起答案' : '查看答案',
                    style: const TextStyle(color: AppTheme.blue600, fontSize: 13, fontWeight: FontWeight.bold)),
                ],
              ),
            ),
          // 展开内容:正确答案(选项绿色高亮 / 填空正确文本) + 解析。
          if (expanded) ...[
            const SizedBox(height: 8),
            _buildAnswerReveal(item),
          ],
          const SizedBox(height: 12),
          // 底部操作行:重做本题(TV 隐藏) + 掌握切换。
          Row(
            children: [
              if (!TvMode.instance.isActive)
                GestureDetector(
                  onTap: () => _redoSingle(item),
                  child: Container(
                    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                    decoration: BoxDecoration(
                      color: const Color(0xFFEFF6FF), borderRadius: BorderRadius.circular(8),
                    ),
                    child: const Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(Icons.replay_rounded, size: 15, color: AppTheme.blue600),
                        SizedBox(width: 4),
                        Text('重做本题', style: TextStyle(color: AppTheme.blue600, fontSize: 12, fontWeight: FontWeight.bold)),
                      ],
                    ),
                  ),
                ),
              const Spacer(),
              // 掌握切换:明确文字按钮,不再用隐晦圆圈。
              GestureDetector(
                onTap: () => _toggleMastered(item),
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                  decoration: BoxDecoration(
                    color: item.mastered ? const Color(0xFFD1FAE5) : AppTheme.slate100,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(
                        item.mastered ? Icons.check_circle : Icons.check_circle_outline,
                        size: 15,
                        color: item.mastered ? const Color(0xFF10B981) : AppTheme.textMuted,
                      ),
                      const SizedBox(width: 4),
                      Text(
                        item.mastered ? '已掌握' : '标记掌握',
                        style: TextStyle(
                          color: item.mastered ? const Color(0xFF065F46) : AppTheme.textMuted,
                          fontSize: 12, fontWeight: FontWeight.bold,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ],
          ),
        ),
      ),
    );
  }

  // 状态徽标:未掌握显示「错N次」+ 连对进度「再对X次掌握」;已掌握显示「✓已掌握」。
  Widget _buildStatusBadge(WrongBookItem item) {
    if (item.mastered) {
      return Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
        decoration: BoxDecoration(color: const Color(0xFFD1FAE5), borderRadius: BorderRadius.circular(8)),
        child: const Text('✓ 已掌握',
          style: TextStyle(fontSize: 11, color: Color(0xFF065F46), fontWeight: FontWeight.bold)),
      );
    }
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
          decoration: BoxDecoration(
            color: item.attemptCount >= 3 ? const Color(0xFFFEE2E2) : const Color(0xFFFEF3C7),
            borderRadius: BorderRadius.circular(8),
          ),
          child: Text('错 ${item.attemptCount} 次',
            style: TextStyle(
              fontSize: 11,
              color: item.attemptCount >= 3 ? const Color(0xFFB91C1C) : const Color(0xFF92400E),
              fontWeight: FontWeight.bold,
            )),
        ),
        // 连对进度:再对 N 次就掌握(threshold=3)。
        if (item.correctStreak > 0) ...[
          const SizedBox(width: 6),
          Text('连对 ${item.correctStreak} 次',
            style: const TextStyle(fontSize: 10, color: Color(0xFF10B981), fontWeight: FontWeight.bold)),
        ],
      ],
    );
  }

  // 展开的答案 + 解析。choice:选项绿色高亮正确项;multi:正确项绿色;fill:正确文本。
  Widget _buildAnswerReveal(WrongBookItem item) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: const Color(0xFFF8FAFC), borderRadius: BorderRadius.circular(10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text('正确答案',
            style: TextStyle(fontSize: 11, color: AppTheme.textMuted, fontWeight: FontWeight.bold)),
          const SizedBox(height: 6),
          if (item.type == 'multi_choice')
            ..._revealMultiOptions(item)
          else if (item.type == 'fill')
            _revealFill(item)
          else
            ..._revealChoiceOptions(item),
          if (item.explanation.isNotEmpty) ...[
            const SizedBox(height: 10),
            const Text('解析',
              style: TextStyle(fontSize: 11, color: AppTheme.textMuted, fontWeight: FontWeight.bold)),
            const SizedBox(height: 4),
            MarkdownView(data: item.explanation, baseTextColor: AppTheme.slate600, textScale: 0.95),
          ],
        ],
      ),
    );
  }

  List<Widget> _revealChoiceOptions(WrongBookItem item) {
    final correctIdx = item.correctIndex;
    return List.generate(item.options.length, (i) {
      final correct = correctIdx == i;
      return Container(
        margin: const EdgeInsets.only(bottom: 4),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
        decoration: BoxDecoration(
          color: correct ? const Color(0xFFD1FAE5) : Colors.white,
          borderRadius: BorderRadius.circular(6),
          border: Border.all(color: correct ? const Color(0xFF10B981) : AppTheme.borderMuted),
        ),
        child: Row(
          children: [
            Icon(correct ? Icons.check_circle : Icons.radio_button_unchecked,
                size: 16, color: correct ? const Color(0xFF10B981) : AppTheme.textMuted),
            const SizedBox(width: 8),
            Expanded(child: Text(item.options[i], style: const TextStyle(fontSize: 13))),
          ],
        ),
      );
    });
  }

  List<Widget> _revealMultiOptions(WrongBookItem item) {
    final correctIdxs = item.correctIndices.toSet();
    return List.generate(item.options.length, (i) {
      final correct = correctIdxs.contains(i);
      return Container(
        margin: const EdgeInsets.only(bottom: 4),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
        decoration: BoxDecoration(
          color: correct ? const Color(0xFFD1FAE5) : Colors.white,
          borderRadius: BorderRadius.circular(6),
          border: Border.all(color: correct ? const Color(0xFF10B981) : AppTheme.borderMuted),
        ),
        child: Row(
          children: [
            Icon(correct ? Icons.check_box : Icons.check_box_outline_blank,
                size: 16, color: correct ? const Color(0xFF10B981) : AppTheme.textMuted),
            const SizedBox(width: 8),
            Expanded(child: Text(item.options[i], style: const TextStyle(fontSize: 13))),
          ],
        ),
      );
    });
  }

  Widget _revealFill(WrongBookItem item) {
    return Text(item.correctText,
        style: const TextStyle(fontSize: 14, color: Color(0xFF10B981), fontWeight: FontWeight.bold));
  }

  String? _coursesTitle(int courseId) {
    for (final c in _courses) {
      if (c.id == courseId) return c.title;
    }
    return null;
  }
}

/// 错题本重做屏:取一批未掌握题当一份"重做卷",逐题作答 → 交卷 → 阅卷。
/// 复用 quiz 的渲染范式(选项 Column,提交后高亮对错)。PAD 横屏包 maxWidth 800。
/// 交卷后逐题揭示解析(对齐 course_exam_screen,学习为什么错)。
class _WrongBookRedoScreen extends StatefulWidget {
  final int activeUserId;
  final List<WrongBookRedoQuestion> questions;
  const _WrongBookRedoScreen({required this.activeUserId, required this.questions});

  @override
  State<_WrongBookRedoScreen> createState() => _WrongBookRedoScreenState();
}

class _WrongBookRedoScreenState extends State<_WrongBookRedoScreen> {
  final Map<int, Map<String, dynamic>> _answers = {};
  Map<int, WrongBookRedoResult>? _results;
  bool _submitting = false;

  void _setChoice(int qid, int idx) {
    if (_results != null) return;
    setState(() => _answers[qid] = {'answer_index': idx});
  }

  void _toggleMulti(int qid, int idx) {
    if (_results != null) return;
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
    setState(() => _submitting = true);
    try {
      final answerList = widget.questions.map((q) {
        final a = _answers[q.id] ?? {};
        return {'question_id': q.id, ...a};
      }).toList();
      final results = await ApiService.submitWrongBookRedo(
        activeUserId: widget.activeUserId, answers: answerList,
      );
      if (!mounted) return;
      final map = <int, WrongBookRedoResult>{};
      for (final r in results) {
        map[r.questionId] = r;
      }
      setState(() {
        _results = map;
        _submitting = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _submitting = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('提交失败,请重试')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final submitted = _results != null;
    return Scaffold(
      backgroundColor: AppTheme.backgroundColor,
      appBar: AppBar(
        title: const Text('重做错题'),
        backgroundColor: Colors.white,
        foregroundColor: AppTheme.slate900,
        elevation: 0,
      ),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 800),
          child: ListView.builder(
            padding: const EdgeInsets.all(16),
            itemCount: widget.questions.length + 1,
            itemBuilder: (context, index) {
              if (index == widget.questions.length) {
                return _buildSubmitRow(submitted);
              }
              final q = widget.questions[index];
              return _buildQuestionCard(q, submitted);
            },
          ),
        ),
      ),
    );
  }

  Widget _buildSubmitRow(bool submitted) {
    if (submitted) {
      final correct = _results!.values.where((r) => r.correct).length;
      final total = _results!.length;
      return Padding(
        padding: const EdgeInsets.only(top: 16),
        child: Column(
          children: [
            Text('本次重做:$correct / $total 题正确',
              style: const TextStyle(fontSize: 18, fontWeight: FontWeight.bold)),
            const SizedBox(height: 12),
            Button3D.blue(
              onPressed: () => Navigator.of(context).pop(),
              child: const Padding(
                padding: EdgeInsets.symmetric(horizontal: 24, vertical: 10),
                child: Text('完成', style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold)),
              ),
            ),
          ],
        ),
      );
    }
    return Padding(
      padding: const EdgeInsets.only(top: 16),
      child: Button3D.blue(
        onPressed: _submitting ? null : _submit,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 10),
          child: _submitting
            ? const SizedBox(width: 18, height: 18,
                child: CircularProgressIndicator(color: Colors.white, strokeWidth: 2))
            : const Text('提交全部', style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold)),
        ),
      ),
    );
  }

  Widget _buildQuestionCard(WrongBookRedoQuestion q, bool submitted) {
    final result = _results?[q.id];
    final userAnswer = _answers[q.id];
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 14),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: Colors.white, borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppTheme.borderMuted),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          MarkdownView(data: q.stem, baseTextColor: AppTheme.slate900, textScale: 1.0),
          const SizedBox(height: 10),
          if (q.type == 'multi_choice')
            ..._buildMultiOptions(q, userAnswer, result, submitted)
          else if (q.type == 'fill')
            _buildFillField(q, userAnswer, result, submitted)
          else
            ..._buildChoiceOptions(q, userAnswer, result, submitted),
          // 交卷后揭示解析(学习为什么错)。
          if (submitted && result != null && result.explanation.isNotEmpty) ...[
            const SizedBox(height: 8),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: const Color(0xFFF8FAFC), borderRadius: BorderRadius.circular(8),
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
    WrongBookRedoQuestion q, Map<String, dynamic>? userAnswer,
    WrongBookRedoResult? result, bool submitted,
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
    WrongBookRedoQuestion q, Map<String, dynamic>? userAnswer,
    WrongBookRedoResult? result, bool submitted,
  ) {
    final picked = (userAnswer?['answer_indices'] as List?)?.cast<int>() ?? const [];
    final correctIdxs = result?.correctIndices ?? const [];
    return List.generate(q.options.length, (i) {
      final selected = picked.contains(i);
      final correct = submitted && correctIdxs.contains(i);
      final wrongPick = submitted && selected && !correct;
      return _optionTile(
        q.options[i],
        selected: selected,
        correct: correct,
        wrongPick: wrongPick,
        multi: true,
        onTap: () => _toggleMulti(q.id, i),
      );
    });
  }

  Widget _buildFillField(
    WrongBookRedoQuestion q, Map<String, dynamic>? userAnswer,
    WrongBookRedoResult? result, bool submitted,
  ) {
    if (submitted) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (result?.correctText.isNotEmpty == true)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text('正确答案:${result!.correctText}',
                style: const TextStyle(color: Color(0xFF10B981), fontWeight: FontWeight.bold)),
            ),
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
