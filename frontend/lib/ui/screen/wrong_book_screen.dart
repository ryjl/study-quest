import 'package:flutter/material.dart';

import '../../model/course.dart';
import '../../model/wrong_book.dart';
import '../../service/api_service.dart';
import '../../service/tv_mode.dart';
import '../../theme.dart';
import '../widget/button_3d.dart';
import '../widget/quiz_card.dart';
import '../widget/state_widgets.dart';
import '../responsive.dart';

/// 错题本屏(TODO.md P0)。学生做错的题自动归集于此。体验闭环:
///  - 列表浏览(默认「全部」:已掌握的灰显不消失;可按课程/掌握状态过滤)
///  - 卡片就地自测(收起态点选项回忆,展开看正确答案 + 解析 + 多选明细对比)
///  - 整批重做(顶部「重做一批」,像一份复习卷,连对 3 次才掌握)
///  - 手动标记掌握 / 取消(明确按钮,不再用隐晦圆圈)
///
/// PAD/TV 友好:
///  - 响应式 GridView(PAD 横屏多列)
///  - TV 模式:做题交互隐藏 + 提示去平板/手机(TV 复习错题场景不成立)
class WrongBookScreen extends StatefulWidget {
  final int activeUserId;
  /// 错题本内部状态变化(掌握切换/重做交卷)后通知父级刷新 tab 角标。
  /// 不传则不通知(默认)。main_navigation 传入 _loadUnmasteredCount。
  final VoidCallback? onWrongBookChanged;
  const WrongBookScreen({
    super.key,
    required this.activeUserId,
    this.onWrongBookChanged,
  });

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
  // 就地自测的选择(问题#3):纯本地状态,不改后端。让学生在收起态先点选项回忆,
  // 展开看答案时和正确答案对比。_choiceSelf[questionId]=选中的索引;_multiSelf 是多选集合。
  // 自测≠重做:重做走单独的重做卷屏(交卷判分、改 mastery);自测只是轻量回忆。
  final Map<int, int> _choiceSelf = {};
  final Map<int, Set<int>> _multiSelf = {};
  final Map<int, TextEditingController> _fillSelfControllers = {};
  // 乐观更新层:覆盖 snapshot.data 的"实时"列表。toggle 掌握状态后立即生效,
  // 不用等重新拉取。每次 _loadData 清空(新过滤 = 以服务端为准),toggle 时就地改写。
  // 之前乐观更新写到 _lastList 但 build 没用它,导致「标记掌握」看着没反应(问题#6)。
  WrongBookList? _liveList;

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
    _choiceSelf.clear();
    _multiSelf.clear();
    for (final c in _fillSelfControllers.values) {
      c.dispose();
    }
    _fillSelfControllers.clear();
    _liveList = null; // 新过滤:丢弃乐观层,以服务端结果为准
    final f = ApiService.fetchWrongBook(
      widget.activeUserId,
      courseId: _courseFilter,
      mastered: _masteredFilter,
    );
    // 服务端结果回来后,把它设为乐观层的基底。注意:必须用 .then 在 future 完成时赋值,
    // 而不是在 build() 里赋值——否则 toggle 触发的 rebuild 会用同一个旧 future 的 snapshot
    // 把 _liveList 覆盖回旧数据,乐观更新瞬间失效(问题#6 修复的回归 bug)。
    _future = f.then((list) {
      if (mounted) {
        setState(() => _liveList = list);
      }
      return list;
    });
  }

  @override
  void dispose() {
    for (final c in _fillSelfControllers.values) {
      c.dispose();
    }
    super.dispose();
  }

  // 就地自测:点选项标记你的选择(单选)。纯本地,不改后端。
  void _selfPickChoice(int qid, int idx) {
    setState(() => _choiceSelf[qid] = idx);
  }

  // 就地自测:多选 toggle。
  void _selfToggleMulti(int qid, int idx) {
    setState(() {
      final set = _multiSelf[qid] ?? <int>{};
      if (set.contains(idx)) {
        set.remove(idx);
      } else {
        set.add(idx);
      }
      _multiSelf[qid] = set;
    });
  }

  Future<void> _refresh() async {
    setState(_loadData);
  }

  Future<void> _toggleMastered(WrongBookItem item) async {
    final newMastered = !item.mastered;
    // 乐观更新:就地改写 _liveList(真正驱动 build 的数据源),立即看到状态翻转。
    // 之前写到 _lastList 但 build 没读它 → 「标记掌握」点了像没反应(问题#6)。
    setState(() {
      final list = _liveList;
      if (list != null) {
        final i = list.items.indexWhere((x) => x.questionId == item.questionId);
        if (i >= 0) {
          _liveList = WrongBookList(
            items: List.of(list.items)..[i] = _copyWithMastered(list.items[i], newMastered),
            unmasteredCount: list.unmasteredCount + (newMastered ? -1 : 1),
          );
        }
      }
    });
    try {
      await ApiService.markWrongBookMastered(widget.activeUserId, item.questionId, newMastered);
      // 通知父级(main_navigation)刷新 tab 角标——未掌握数变了。
      widget.onWrongBookChanged?.call();
    } catch (_) {
      // 失败回滚:重拉保证和后端一致。
      if (mounted) _refresh();
    }
  }

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
    widget.onWrongBookChanged?.call(); // 重做交卷可能改了掌握状态,角标可能变
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.transparent,
      body: FutureBuilder<WrongBookList>(
        future: _future,
        builder: (context, snapshot) {
          if (snapshot.connectionState != ConnectionState.done && _liveList == null) {
            return loadingSpinner();
          }
          if (snapshot.hasError && _liveList == null) {
            return errorStateBox(
              snapshot.error.toString(), _refresh,
              message: '加载错题本失败',
            );
          }
          // 数据源:乐观层(_liveList)优先——它在 _loadData 的 future.then 里被设为服务端结果,
          // 之后 toggle 就地改写它。build 里不再触碰它的赋值,避免覆盖乐观更新(问题#6)。
          final list = _liveList ?? snapshot.data;
          final items = list?.items ?? const <WrongBookItem>[];
          // 空态分两种:真没有错题(全局首次)vs 当前过滤无结果。后者保留过滤行,
          // 让学生能切回「全部」/换个课程,而不是整页变成空状态丢失所有 tab(问题#2)。
          // 判断依据:unmasteredCount>0 或课程过滤开启 → 学生其实有错题,只是当前视图为空。
          final unmastered = list?.unmasteredCount ?? 0;
          final hasFilter = _masteredFilter != null || _courseFilter != 0;
          if (items.isEmpty) {
            if (hasFilter || unmastered > 0) {
              // 当前筛选下没有题:保留 header+过滤行 + 提示切回全部。
              return _buildFilterEmpty(unmastered);
            }
            return _buildEmpty();
          }
          return _buildList(items, unmastered);
        },
      ),
    );
  }

  // 真正的空状态(学生从没错过题):引导去学习。
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

  // 过滤空状态:学生有错题,但当前筛选(未掌握/某课程)下没有。保留 header + 过滤行,
  // 避免整页变空让用户以为页面坏了、要切大 tab 才恢复(问题#2)。
  Widget _buildFilterEmpty(int unmastered) {
    return Padding(
      padding: portraitAwarePadding(context),
      child: CustomScrollView(
        slivers: [
          SliverToBoxAdapter(child: _buildHeader(0, 0, unmastered)),
          SliverFillRemaining(
            hasScrollBody: false,
            child: Center(
              child: Padding(
                padding: const EdgeInsets.only(top: 48),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.filter_list_off_rounded, size: 48, color: AppTheme.textMuted),
                    const SizedBox(height: 12),
                    const Text('当前筛选下没有错题',
                      style: TextStyle(fontSize: 16, fontWeight: FontWeight.bold, color: AppTheme.slate900)),
                    const SizedBox(height: 6),
                    const Text('换个课程,或切回「全部」看看全部错题。',
                      style: TextStyle(color: AppTheme.textMuted, fontSize: 13)),
                  ],
                ),
              ),
            ),
          ),
        ],
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
    final courseName = _coursesTitle(item.courseId);
    final hasOptions = item.type == 'choice' || item.type == 'multi_choice';
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
          // 来源 chip + 错次/streak 徽标。课程名一定显示(问题#1):题干常写「根据本课程」
          // 却不点名是哪门课,来源 chip 是学生唯一能对上号的线索,丢了就完全不知道在问哪门。
          Row(
            children: [
              Flexible(
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                  decoration: BoxDecoration(
                    color: AppTheme.blue100, borderRadius: BorderRadius.circular(8),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.book_outlined, size: 12, color: AppTheme.blue600),
                      const SizedBox(width: 4),
                      Flexible(
                        child: Text(
                          courseName ?? '未知课程',
                          maxLines: 1, overflow: TextOverflow.ellipsis,
                          style: const TextStyle(fontSize: 11, color: AppTheme.blue600, fontWeight: FontWeight.w600),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              const Spacer(),
              _buildStatusBadge(item),
            ],
          ),
          const SizedBox(height: 8),
          // 题面 + 选项/答案:复用全站统一的 QuizReviewCard(代码/视觉与 quiz/考试/历史一致)。
          // 收起态(expanded=false):interactive,学生先自测点选项回忆,不揭示答案;
          // 展开态(expanded=true):submitted,高亮正确项 + 明细 + 解析,和自测选择对比。
          // 之前这里各写了一份中性选项/揭示选项/多选明细/填空回放,现统一到组件里。
          _wrongBookReviewCard(item, expanded),
          const SizedBox(height: 8),
          // 「查看答案 / 收起」切换(选择/多选/有正确文本的填空都给)。
          if (hasOptions || item.correctText.isNotEmpty || item.correctIndex != null)
            GestureDetector(
              onTap: () => _toggleExpand(item.questionId),
              child: Container(
                color: Colors.transparent,
                padding: const EdgeInsets.symmetric(vertical: 4),
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
            ),
          const SizedBox(height: 12),
          // 底部操作行:只有掌握切换(卡片已支持就地自测,单题「重做本题」入口移除)。
          Row(
            children: [
              const Spacer(),
              // 掌握切换:未掌握→「标记掌握」(灰);已掌握→「取消掌握」(绿,点一下退回未掌握)。
              // 文案明确双向(问题#5:之前已掌握态只显示「已掌握」像不可点的状态标签,没有"改回未掌握"的入口)。
              // 乐观更新走 _liveList,点击即时翻转(问题#6)。
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
                        item.mastered ? '已掌握 · 点取消' : '标记掌握',
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

  // 错题本卡片的题面 + 选项/答案区:复用全站统一的 QuizReviewCard。
  // 收起态(reveal=false)→ interactive:学生先自测点选项回忆,不揭示答案;
  // 展开态(reveal=true)→ submitted:高亮正确项 + 多选明细 + 填空对比 + 解析。
  // 自测选择(_choiceSelf/_multiSelf/_fillSelfControllers)作为 user 作答传给组件,
  // 揭示时和正确答案对比。自测≠重做:不改后端,只是轻量回忆。
  Widget _wrongBookReviewCard(WrongBookItem item, bool reveal) {
    return QuizReviewCard(
      stem: QuizStemData(
        index: 0, // 错题本是单题卡,无题序
        type: item.type,
        stem: item.stem,
        options: item.options,
        chunkStartTime: null,
        hasJump: false,
      ),
      verdict: QuizVerdictData(
        submitted: reveal,
        userChoiceIndex: _choiceSelf[item.questionId],
        userMultiIndices: _multiSelf[item.questionId] ?? const {},
        userFillText: _fillSelfControllers[item.questionId]?.text ?? '',
        correctIndex: item.correctIndex,
        correctIndices: item.correctIndices.toSet(),
        correctText: item.correctText,
        explanation: item.explanation,
        // 揭示态:若学生自测过,按自测结果判对错(横幅显示「回答正确/不正确」);
        // 没自测过则 correct=null(不显示对错横幅,只展示正确答案 + 解析)。
        correct: reveal ? _selfTestCorrect(item) : null,
      ),
      mode: reveal ? QuizReviewMode.submitted : QuizReviewMode.interactive,
      // 错题本卡片有自己的外壳(圆角卡 + 课程 chip + 掌握切换),内层用 flat 避免双层边框。
      flat: true,
      onPickChoice: (i) => _selfPickChoice(item.questionId, i),
      onToggleMulti: (i) => _selfToggleMulti(item.questionId, i),
      fillController: item.type == 'fill'
          ? (_fillSelfControllers.putIfAbsent(item.questionId, () => TextEditingController()))
          : null,
      onFillChanged: () => setState(() {}),
    );
  }

  // 自测对错(揭示态用):学生点过选项/填过答案才算分,没自测过返回 null(不显示对错横幅)。
  // choice:选中索引 == 正确索引;multi:自测集合 == 正确集合;fill:文本 trim 相等(忽略大小写)。
  bool? _selfTestCorrect(WrongBookItem item) {
    final qid = item.questionId;
    if (item.type == 'multi_choice') {
      final picks = _multiSelf[qid];
      if (picks == null) return null;
      return _setEquals(picks, item.correctIndices.toSet());
    }
    if (item.type == 'fill') {
      final text = _fillSelfControllers[qid]?.text ?? '';
      if (text.trim().isEmpty) return null;
      return text.trim().toLowerCase() == item.correctText.trim().toLowerCase();
    }
    // choice
    final pick = _choiceSelf[qid];
    if (pick == null) return null;
    return item.correctIndex != null && pick == item.correctIndex;
  }

  bool _setEquals(Set<int> a, Set<int> b) =>
      a.length == b.length && a.containsAll(b);


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
  // 填空题用 controller 持有输入(QuizReviewCard 的 fill 走 controller),onChanged 同步到
  // _answers['answer_text'] 供交卷读取。和 ai_study_screen 的 _fillControllers 同模式。
  final Map<int, TextEditingController> _fillControllers = {};
  Map<int, WrongBookRedoResult>? _results;
  bool _submitting = false;

  @override
  void dispose() {
    for (final c in _fillControllers.values) {
      c.dispose();
    }
    super.dispose();
  }

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
        // 标题带题量(问题#7):让学生一眼看到这张复习卷有几题,而不是只看到「重做错题」
        // 误以为只有 1 题。配合后端 RedoWrongBookQuiz 的 log,题量异常时可定位。
        title: Text('重做错题 · 共 ${widget.questions.length} 题'),
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
              return _redoCard(q, index, submitted);
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

  // 重做卷的逐题卡:复用全站统一的 QuizReviewCard(代码/视觉与 quiz/考试/历史一致)。
  // 适配 WrongBookRedoQuestion + WrongBookRedoResult + 本地 _answers:
  //   - _answers[q.id] 是 Map:choice→answer_index, multi→answer_indices, fill→answer_text。
  //   - 重做不下发正确答案(防作弊),result 只在交卷后揭示。
  Widget _redoCard(WrongBookRedoQuestion q, int index, bool submitted) {
    final result = _results?[q.id];
    final userAnswer = _answers[q.id];
    final userChoice = userAnswer?['answer_index'] as int?;
    final userMulti = (userAnswer?['answer_indices'] as List?)?.cast<int>().toSet() ?? <int>{};
    final userFill = (userAnswer?['answer_text'] as String?) ?? '';
    return Padding(
      padding: const EdgeInsets.only(bottom: 14),
      child: QuizReviewCard(
        stem: QuizStemData(
          index: index,
          type: q.type,
          stem: q.stem,
          options: q.options,
          chunkStartTime: null, // 重做题不下发 chunk 锚点,无跳转
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
          partial: result?.partial ?? false,
        ),
        mode: submitted ? QuizReviewMode.submitted : QuizReviewMode.interactive,
        onPickChoice: (i) => _setChoice(q.id, i),
        onToggleMulti: (i) => _toggleMulti(q.id, i),
        // 填空题:lazy 建 controller,onChange 同步到 _answers 供交卷读取。
        fillController: q.type == 'fill' ? (_fillControllers[q.id] ??= TextEditingController()) : null,
        onFillChanged: () {
          setState(() {});
          _answers[q.id] = {'answer_text': _fillControllers[q.id]?.text ?? ''};
        },
      ),
    );
  }

}
