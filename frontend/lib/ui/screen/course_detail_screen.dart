import 'dart:math';
import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../model/course_summary.dart';
import '../../model/progress.dart';
import '../../model/quiz.dart';
import '../../model/subject.dart';
import '../../service/api_service.dart';
import '../../service/chapter_grouper.dart';
import '../../theme.dart';
import '../widget/button_3d.dart';
import '../widget/episode_row.dart';
import '../widget/markdown_view.dart';
import '../widget/state_widgets.dart';
import '../widget/subject_icon.dart';
import '../responsive.dart';
import 'player_screen.dart';
import 'course_exam_screen.dart';

class CourseDetailScreen extends StatefulWidget {
  final int activeUserId;
  final Course course;

  const CourseDetailScreen({
    Key? key,
    required this.activeUserId,
    required this.course,
  }) : super(key: key);

  @override
  State<CourseDetailScreen> createState() => _CourseDetailScreenState();
}

class _CourseDetailScreenState extends State<CourseDetailScreen> {
  late Future<List<Episode>> _episodesFuture;
  late Future<List<UserProgress>> _progressFuture;
  late Future<List<Chapter>> _chaptersFuture;
  // 课程总览(跨课时汇总导览)future。单独管理 + 单独 FutureBuilder 渲染,不混进
  // _combinedFuture——后者索引被 episodes/progress/chapters 占用,且总览加载失败
  // 不应拖累整页(无总览 → 隐藏卡片)。
  late Future<CourseSummary?> _courseSummaryFuture;
  // Cached combined future — FutureBuilder must see a STABLE future reference
  // across rebuilds, otherwise each setState (e.g. enrichment prefetch filling
  // the AI/attachment caches) makes FutureBuilder re-subscribe, flip to
  // ConnectionState.waiting, flash the loading spinner, then flip back. On real
  // devices this shows as continuous flicker making the list untappable; MuMu
  // (fast x86) hides it because the waiting→done flip lands within one frame.
  // Built once per load in _refreshData; the FutureBuilder reads this.
  late Future<List<dynamic>> _combinedFuture;

  // summary 缓存。课前探险问题的数据源是 /ai-summary 的 pre_adventure。这里缓存
  // 避免列表行点击弹窗 + 播放器进入时重复请求同一个 summary。失败/老数据存 null,
  // 取不到就降级为"暂无探索任务"。
  final Map<int, EpisodeSummary?> _summaryCache = {};

  // Subject catalog for resolving the course's subject key → label/color.
  List<Subject> _subjectsCatalog = const [];

  // 课程总览卡片展开状态。默认收起——summary 文本可能很长(含表格/SVG),默认
  // 全部展开会把下方"闯关目录"推到很远。用户点标题行展开看全文。无动画,纯布尔
  // 显隐——避免 AnimatedSize 在过渡帧测量异常触发同类的"组件消失"渲染 bug
  // (见 docs/frontend_render_fix_summary.md)。
  bool _summaryExpanded = false;

  @override
  void initState() {
    super.initState();
    _refreshData();
    // Resolve the subject catalog so the header chip shows the configured
    // label and the hero gradient matches the subject color. Non-fatal.
    ApiService.fetchSubjects(widget.activeUserId).then((list) {
      if (mounted) setState(() => _subjectsCatalog = list);
    });
  }

  void _refreshData() {
    _episodesFuture = ApiService.fetchEpisodes(widget.activeUserId, widget.course.id);
    _progressFuture = ApiService.fetchProgressOverview(widget.activeUserId);
    _chaptersFuture = ApiService.fetchChapters(widget.activeUserId, widget.course.id);
    _courseSummaryFuture = ApiService.fetchCourseSummary(widget.activeUserId, widget.course.id);
    // Compose the combined future ONCE here so FutureBuilder sees a stable
    // reference across rebuilds (see _combinedFuture comment above).
    //
    // 容错策略:episodes 是本页核心(课时列表),失败必须进 error 态给重试;
    // progress/chapters 是辅助(完成勾选/续播位置/章节分组),失败应降级为空列表
    // 而非拖垮整页——否则单个辅助接口偶发失败会让课时列表都看不到,体验更差。
    // 用 .catchError((_) => <T>[]) 把辅助 future 包成永不抛错。
    final safeProgress = _progressFuture.catchError((_) => <UserProgress>[]);
    final safeChapters = _chaptersFuture.catchError((_) => <Chapter>[]);
    _combinedFuture = Future.wait([_episodesFuture, safeProgress, safeChapters]);
    setState(() {});
    // After episodes load, prefetch enrichment data in the background.
    _episodesFuture.then((eps) => _prefetchEnrichment(eps)).catchError((_) {});
  }

  Future<void> _prefetchEnrichment(List<Episode> episodes) async {
    // Fetch ALL enrichment before calling setState once. The old code called
    // setState per-episode (N rebuilds), which — combined with the unstable
    // FutureBuilder future — caused N flashes on real devices. Batched into a
    // single setState so the list only rebuilds once after everything lands.
    bool changed = false;
    for (final ep in episodes) {
      // 课前探险问题数据源是 summary.pre_adventure,与列表行的富化并行预取
      // (失败时静默,弹窗/播放器再按需 lazy fetch)。
      try {
        final summary = await ApiService.fetchEpisodeSummary(widget.activeUserId, ep.id);
        _summaryCache[ep.id] = summary;
        changed = true;
      } catch (_) {
        // summary 缺失(老数据/AI 未开/未生成)属正常态,降级即可。
      }
    }
    if (changed && mounted) {
      setState(() {});
    }
  }

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final subjectGradient = AppTheme.getSubjectGradientFromColor(resolveSubject(widget.course.subject, _subjectsCatalog).color);
    // Use the course's real first tag if defined (no more mock tags).
    final tagsList = widget.course.tagsList;
    final firstTag = tagsList.isNotEmpty ? tagsList.first : null;

    return Scaffold(
      body: Container(
        color: colors.backgroundColor, // slate-50 background
        child: FutureBuilder(
          future: _combinedFuture,
          builder: (context, AsyncSnapshot<List<dynamic>> snapshot) {
            if (snapshot.connectionState == ConnectionState.waiting) {
              return loadingSpinner(context);
            }
            if (snapshot.hasError) {
              return errorStateBox(context, snapshot.error.toString(), _refreshData);
            }

            final episodes = snapshot.data![0] as List<Episode>;
            final progressList = snapshot.data![1] as List<UserProgress>;
            final chapters = snapshot.data![2] as List<Chapter>;

            if (episodes.isEmpty) {
              return emptyStateBox(
                context: context,
                icon: Icons.video_library_outlined,
                headline: '该课程库下暂无课时视频',
                hint: '请登录管理后台导入相关的网盘视频资源。',
                refreshLabel: '刷新页面',
                onRefresh: _refreshData,
              );
            }

            // Build quick mapping for completion states + resume positions
            final Map<int, bool> completionMap = {};
            final Map<int, int> resumeMap = {}; // episodeId -> last_position_seconds
            for (var p in progressList) {
              completionMap[p.episodeId] = p.isCompleted;
              if (!p.isCompleted && p.lastPositionSeconds > 5) {
                resumeMap[p.episodeId] = p.lastPositionSeconds;
              }
            }

            // Compute overall course completion progress
            final completedEpisodes = episodes.where((e) => completionMap[e.id] ?? false).length;
            final progressPercent = episodes.isEmpty ? 0 : (completedEpisodes * 100) ~/ episodes.length;

            // Group episodes by real chapter. Episodes whose ChapterID maps to
            // a known chapter are filed under it; everything else (ChapterID 0
            // or orphaned) falls into a trailing "全部课时" bucket.
            final groups = groupEpisodesByChapter(episodes, chapters);

            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                // Sticky Top Bar with White 3D Back button
                Container(
                  color: colors.cardColor.withValues(alpha: 0.7),
                  padding: EdgeInsets.symmetric(
                    horizontal: isPortrait(context) ? 16.0 : 40.0,
                    vertical: 16.0,
                  ),
                  child: Row(
                    children: [
                      Button3D.white(
                        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                        onPressed: () => Navigator.pop(context),
                        child: Row(
                          children: [
                            Icon(Icons.arrow_back_rounded, size: 18, color: colors.textMuted),
                            const SizedBox(width: 8),
                            Text('返回大厅', style: TextStyle(color: colors.textMuted)),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),

                // Scrollable main content
                Expanded(
                  child: RefreshIndicator(
                    // 下拉刷新:正常浏览态也能手动刷新课时/进度,而非只能靠 errorStateBox。
                    onRefresh: () async => _refreshData(),
                    child: SingleChildScrollView(
                    physics: const BouncingScrollPhysics(),
                    padding: EdgeInsets.symmetric(
                      horizontal: isPortrait(context) ? 16.0 : 40.0,
                      vertical: 24.0,
                    ),
                    child: Column(
                      children: [
                        // Hero Header Gradient Card
                        Container(
                          width: double.infinity,
                          decoration: BoxDecoration(
                            gradient: subjectGradient,
                            borderRadius: BorderRadius.circular(36),
                            border: Border.all(color: Colors.white, width: 4.0),
                            boxShadow: [
                              BoxShadow(
                                color: Colors.black.withValues(alpha: 0.08),
                                blurRadius: 30,
                                offset: const Offset(0, 12),
                              )
                            ],
                          ),
                          padding: EdgeInsets.all(isPortrait(context) ? 20.0 : 48.0),
                          child: _buildHeroContent(
                            context,
                            episodes: episodes,
                            firstTag: firstTag,
                            progressPercent: progressPercent,
                          ),
                        ),
                        const SizedBox(height: 40),

                        // 课程总览卡片(跨课时整体导览)。无总结/加载失败 → 不显示,
                        // 避免空白卡片打断章节列表的浏览体验。
                        _buildCourseSummaryCard(context),

                        const SizedBox(height: 40),

                        // Chapter Directory Panel
                        Container(
                          padding: EdgeInsets.all(isPortrait(context) ? 16 : 32),
                          decoration: BoxDecoration(
                            color: colors.cardColor,
                            borderRadius: BorderRadius.circular(36),
                            border: Border.all(color: colors.borderMuted, width: 2.0),
                            boxShadow: [
                              BoxShadow(
                                color: colors.slate900.withValues(alpha: 0.02),
                                blurRadius: 20,
                                offset: const Offset(0, 4),
                              )
                            ],
                          ),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              // Title
                              Row(
                                children: [
                                  Container(
                                    padding: const EdgeInsets.all(8),
                                    decoration: BoxDecoration(
                                      color: colors.blue100,
                                      borderRadius: BorderRadius.circular(12),
                                    ),
                                    child: Icon(Icons.list_alt_rounded, color: colors.blue600, size: 24),
                                  ),
                                  const SizedBox(width: 14),
                                  Text(
                                    '闯关目录',
                                    style: TextStyle(fontSize: 24, fontWeight: FontWeight.w900, color: colors.textWhite),
                                  ),
                                ],
                              ),
                              const SizedBox(height: 40),

                              // Chapter Listing
                              Column(
                                children: groups.asMap().entries.map((entry) {
                                  final group = entry.value;
                                  final isLast = entry.key == groups.length - 1;
                                  // Hide the title accent bar for the ungrouped bucket when there
                                  // are also real chapters, so it reads as a plain list.
                                  final showChapterHeader = !(group.isUngrouped && groups.length > 1);
                                  return Padding(
                                    padding: EdgeInsets.only(bottom: isLast ? 0 : 32.0),
                                    child: Column(
                                      crossAxisAlignment: CrossAxisAlignment.start,
                                      children: [
                                        // Chapter Title Row (hidden for the ungrouped bucket when mixed)
                                        if (showChapterHeader)
                                          Row(
                                            children: [
                                              Container(
                                                width: 6,
                                                height: 24,
                                                decoration: BoxDecoration(
                                                  gradient: LinearGradient(
                                                    colors: [const Color(0xFF60A5FA), colors.blue600],
                                                    begin: Alignment.topCenter,
                                                    end: Alignment.bottomCenter,
                                                  ),
                                                  borderRadius: BorderRadius.circular(3),
                                                ),
                                              ),
                                              const SizedBox(width: 12),
                                              Text(
                                                group.title,
                                                style: TextStyle(fontWeight: FontWeight.w900, fontSize: 18, color: colors.textWhite),
                                              ),
                                            ],
                                          ),
                                        SizedBox(height: showChapterHeader ? 16 : 0),

                                        // Episodes under chapter
                                        Padding(
                                          padding: EdgeInsets.only(left: showChapterHeader ? 18.0 : 0),
                                          child: Column(
                                            children: group.episodes.map((ep) {
                                              final isCompleted = completionMap[ep.id] ?? false;
                                              return EpisodeRow(
                                                ep: ep,
                                                isCompleted: isCompleted,
                                                activeUserId: widget.activeUserId,
                                                resumeSeconds: resumeMap[ep.id] ?? 0,
                                                totalSeconds: ep.durationSeconds,
                                                onPlay: () => _playEpisode(ep),
                                              );
                                            }).toList(),
                                          ),
                                        ),
                                      ],
                                    ),
                                  );
                                }).toList(),
                              ),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
                    ), // RefreshIndicator 闭合
                ),
              ],
            );
          },
        ),
      ),
    );
  }

  /// 课程总览卡片(跨课时整体导览 + 学习路径)。
  ///
  /// 设计取舍:
  ///   - 无总览 / 加载失败 → 返回 SizedBox.shrink()(不占位),让章节列表紧跟
  ///     Hero 后面,避免空白卡片打断浏览。
  ///   - 总览文本里可能有 markdown 表格/SVG(后端 prompt 鼓励富文本),统一用
  ///     MarkdownView 渲染(它内置 GFM 表格 + SVG 围栏块支持)。
  ///   - 陈旧提示:生成后又有新课时补进来,诚实告诉学生"可能未涵盖最新内容"。
  ///     学生端不能触发刷新(课程总览是 admin 手动维护的 course-unique 内容)。
  Widget _buildCourseSummaryCard(BuildContext context) {
    final colors = context.colors;
    return FutureBuilder<CourseSummary?>(
      future: _courseSummaryFuture,
      builder: (context, snapshot) {
        // 加载中 / 失败 / 无数据 都不显示卡片(避免占位空白)。
        if (snapshot.connectionState == ConnectionState.waiting) return const SizedBox.shrink();
        if (snapshot.hasError) return const SizedBox.shrink();
        final summary = snapshot.data;
        if (summary == null || !summary.isReady || (summary.summaryText ?? '').isEmpty) {
          return const SizedBox.shrink();
        }
        return Container(
          width: double.infinity,
          margin: const EdgeInsets.only(bottom: 0),
          padding: EdgeInsets.all(isPortrait(context) ? 16 : 28),
          decoration: BoxDecoration(
            color: colors.cardColor,
            borderRadius: BorderRadius.circular(28),
            border: Border.all(color: colors.borderMuted, width: 2.0),
            boxShadow: [
              BoxShadow(
                color: colors.slate900.withValues(alpha: 0.02),
                blurRadius: 20,
                offset: const Offset(0, 4),
              )
            ],
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // 标题行:整行可点切换展开/收起。InkWell 无圆角(已在卡片外层),
              // 与 _HistoryQuizCard(ai_study_screen.dart) 的 toggle 模式一致。
              InkWell(
                onTap: () => setState(() => _summaryExpanded = !_summaryExpanded),
                child: Row(
                  children: [
                    Container(
                      padding: const EdgeInsets.all(8),
                      decoration: BoxDecoration(
                        color: const Color(0xFFFEF3C7),
                        borderRadius: BorderRadius.circular(12),
                      ),
                      child: const Icon(Icons.auto_stories_outlined, color: Color(0xFFD97706), size: 22),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Text(
                        '课程总览',
                        style: TextStyle(fontSize: 20, fontWeight: FontWeight.w900, color: colors.textWhite),
                      ),
                    ),
                    Icon(
                      _summaryExpanded ? Icons.expand_less_rounded : Icons.chevron_right_rounded,
                      size: 26,
                      color: colors.textMuted,
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 12),
              // 陈旧提示:字幕逐节补全后内容可能未涵盖最新课时。收起态也显示
              // (诚实告诉用户总览可能过期,不展开也能看到)。
              if (summary.isStale)
                Container(
                  margin: const EdgeInsets.only(bottom: 12),
                  padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                  decoration: BoxDecoration(
                    color: const Color(0xFFFFFBEB),
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(color: const Color(0xFFFCD34D), width: 1),
                  ),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const Icon(Icons.info_outline, size: 15, color: Color(0xFFD97706)),
                      const SizedBox(width: 6),
                      Expanded(
                        child: Text(
                          '本总览基于 ${summary.episodeCountAtGen} 节内容生成,目前已有 ${summary.currentEpisodeCount} 节'
                          '${summary.newEpisodesSinceGen > 0 ? '(新增 ${summary.newEpisodesSinceGen} 节)' : ''},'
                          '可能未涵盖最新课时。',
                          style: const TextStyle(fontSize: 12, color: Color(0xFF92400E), height: 1.4),
                        ),
                      ),
                    ],
                  ),
                ),
              // 收起态显示一行小灰字提示,展开时显示完整 summaryText(含表格/SVG)。
              // 纯布尔显隐,不用动画——避免 AnimatedSize 在过渡帧触发测量异常。
              if (!_summaryExpanded)
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Text(
                    '点开看完整总览 →',
                    style: TextStyle(fontSize: 12, color: colors.slate400),
                  ),
                ),
              if (_summaryExpanded) ...[
                const SizedBox(height: 4),
                MarkdownView(
                  data: summary.summaryText!,
                  textScale: 1.0,
                ),
              ],
              const SizedBox(height: 12),
              // 底部元信息:生成时间 + 模型。RFC3339 解析后本地化展示。
              if (summary.generatedAt != null || (summary.modelUsed ?? '').isNotEmpty)
                Wrap(
                  spacing: 12,
                  runSpacing: 4,
                  children: [
                    if (summary.generatedAt != null)
                      Text(
                        '生成于 ${_formatSummaryDate(summary.generatedAt!)}',
                        style: TextStyle(fontSize: 11, color: colors.slate400),
                      ),
                    if ((summary.modelUsed ?? '').isNotEmpty)
                      Text(
                        '模型 ${summary.modelUsed}',
                        style: TextStyle(fontSize: 11, color: colors.slate400),
                      ),
                  ],
                ),
            ],
          ),
        );
      },
    );
  }

  String _formatSummaryDate(DateTime dt) {
    String two(int v) => v.toString().padLeft(2, '0');
    return '${dt.year}-${two(dt.month)}-${two(dt.day)} ${two(dt.hour)}:${two(dt.minute)}';
  }

  /// Hero card content: course details + progress card. Wide: side-by-side
  /// row. Narrow (portrait): stacked vertically, details on top and the
  /// progress card below.
  Widget _buildHeroContent(
    BuildContext context, {
    required List<Episode> episodes,
    required String? firstTag,
    required int progressPercent,
  }) {
    final colors = context.colors;
    final compact = isPortrait(context);

    final details = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Wrap(
          spacing: 10,
          runSpacing: 8,
          children: [
            _buildHeaderChip(
              resolveSubject(widget.course.subject, _subjectsCatalog).label,
              icon: subjectIconData(widget.course.subject),
            ),
            _buildHeaderChip(widget.course.grade == 'universal' ? '通用' : '${widget.course.grade}年级'),
            if (firstTag != null) _buildHeaderChip(firstTag),
          ],
        ),
        const SizedBox(height: 24),
        Text(
          widget.course.title,
          style: TextStyle(
            fontSize: compact ? 26 : 36,
            fontWeight: FontWeight.w900,
            color: Colors.white,
            fontFamily: 'Quicksand',
          ),
        ),
        const SizedBox(height: 16),
        Row(
          children: [
            const Icon(Icons.video_library_rounded, color: Colors.white70, size: 20),
            const SizedBox(width: 8),
            Text(
              '共 ${episodes.length} 讲挑战任务',
              style: const TextStyle(color: Colors.white70, fontSize: 16, fontWeight: FontWeight.bold),
            ),
          ],
        ),
        const SizedBox(height: 20),
        // 课程考试入口(TODO.md P0):综合本课知识点出一张卷子做阶段测评。
        // 题库不足时后端 status gate 会提示,这里直接进屏由其处理灰显。
        Align(
          alignment: Alignment.centerLeft,
          child: Button3D(
            onPressed: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => CourseExamScreen(
                activeUserId: widget.activeUserId,
                courseId: widget.course.id,
                courseTitle: widget.course.title,
              ),
            )),
            backgroundColor: Colors.white.withValues(alpha: 0.2),
            shadowColor: Colors.transparent,
            border: Border.all(color: Colors.white.withValues(alpha: 0.5), width: 1.5),
            padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 10),
            child: const Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.emoji_events_rounded, size: 18, color: Colors.white),
                SizedBox(width: 8),
                Text('课程考试', style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold)),
              ],
            ),
          ),
        ),
      ],
    );

    final progressCard = Transform.rotate(
      angle: 3 * pi / 180, // Rotate slightly (3 degrees)
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
        decoration: BoxDecoration(
          color: colors.cardColor,
          borderRadius: BorderRadius.circular(28),
          boxShadow: const [
            BoxShadow(color: Colors.black12, blurRadius: 10)
          ],
          border: Border.all(color: colors.cardColor, width: 2),
        ),
        child: Column(
          children: [
            Text(
              '学习进度',
              style: TextStyle(color: colors.slate400, fontSize: 11, fontWeight: FontWeight.w900),
            ),
            const SizedBox(height: 4),
            Row(
              crossAxisAlignment: CrossAxisAlignment.baseline,
              textBaseline: TextBaseline.alphabetic,
              children: [
                Text(
                  '$progressPercent',
                  style: TextStyle(fontSize: 48, fontWeight: FontWeight.w900, color: colors.textWhite),
                ),
                Text(
                  '%',
                  style: TextStyle(fontSize: 18, color: colors.slate400, fontWeight: FontWeight.bold),
                ),
              ],
            ),
          ],
        ),
      ),
    );

    if (compact) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          details,
          const SizedBox(height: 20),
          Align(alignment: Alignment.centerLeft, child: progressCard),
        ],
      );
    }
    return Row(
      mainAxisAlignment: MainAxisAlignment.spaceBetween,
      crossAxisAlignment: CrossAxisAlignment.center,
      children: [
        Expanded(child: details),
        const SizedBox(width: 24),
        progressCard,
      ],
    );
  }

  Widget _buildHeaderChip(String text, {IconData? icon}) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 6),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.2),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: Colors.white.withValues(alpha: 0.2)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (icon != null) ...[
            Icon(icon, size: 14, color: Colors.white),
            const SizedBox(width: 5),
          ],
          Text(
            text,
            style: const TextStyle(color: Colors.white, fontSize: 12, fontWeight: FontWeight.w900),
          ),
        ],
      ),
    );
  }

  /// 课前探险问题数据源是 summary.pre_adventure。优先用 _summaryCache 里的缓存
  /// (prefetch 预填或弹窗内 lazy fetch 过)。返回空 List 表示本节暂无任务。
  List<String> _cachedPreAdventureTasks(int episodeId) {
    final summary = _summaryCache[episodeId];
    if (summary == null) return const [];
    return summary.preAdventure.map((p) => p.prompt).toList();
  }

  void _playEpisode(Episode ep) {
    final tasks = _cachedPreAdventureTasks(ep.id);
    Navigator.push(
      context,
      MaterialPageRoute(
        builder: (context) => PlayerScreen(
          activeUserId: widget.activeUserId,
          episode: ep,
          preAdventureTasks: tasks,
        ),
      ),
    ).then((_) => _refreshData());
  }
}
