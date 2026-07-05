import 'dart:math';
import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../model/progress.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/glass_panel.dart';
import '../widget/button_3d.dart';
import 'player_screen.dart';

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

  /// Per-episode enrichment data (AI content + attachments), keyed by episode
  /// id. Fetched lazily once the episode list is available so the row badges
  /// ("配套讲义" / "AI 重点总结") reflect real data instead of mock toggles.
  final Map<int, AILessonContent?> _aiCache = {};
  final Map<int, List<Attachment>> _attachmentCache = {};

  @override
  void initState() {
    super.initState();
    _refreshData();
  }

  void _refreshData() {
    setState(() {
      _episodesFuture = ApiService.fetchEpisodes(widget.activeUserId, widget.course.id);
      _progressFuture = ApiService.fetchProgressOverview(widget.activeUserId);
      _chaptersFuture = ApiService.fetchChapters(widget.activeUserId, widget.course.id);
    });
    // After episodes load, prefetch enrichment data in the background.
    _episodesFuture.then((eps) => _prefetchEnrichment(eps)).catchError((_) {});
  }

  Future<void> _prefetchEnrichment(List<Episode> episodes) async {
    for (final ep in episodes) {
      try {
        final ai = await ApiService.fetchAILesson(widget.activeUserId, ep.id);
        final atts = await ApiService.fetchAttachments(widget.activeUserId, ep.id);
        if (!mounted) return;
        setState(() {
          _aiCache[ep.id] = ai;
          _attachmentCache[ep.id] = atts;
        });
      } catch (_) {
        // Enrichment is best-effort; rows simply fall back to no-badge.
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final subjectGradient = AppTheme.getSubjectGradient(widget.course.subject);
    // Use the course's real first tag if defined (no more mock tags).
    final tagsList = widget.course.tagsList;
    final firstTag = tagsList.isNotEmpty ? tagsList.first : null;

    return Scaffold(
      body: Container(
        color: AppTheme.backgroundColor, // slate-50 background
        child: FutureBuilder(
          future: Future.wait([_episodesFuture, _progressFuture, _chaptersFuture]),
          builder: (context, AsyncSnapshot<List<dynamic>> snapshot) {
            if (snapshot.connectionState == ConnectionState.waiting) {
              return const Center(child: CircularProgressIndicator(color: AppTheme.primaryColor));
            }
            if (snapshot.hasError) {
              return _buildErrorBox(snapshot.error.toString());
            }

            final episodes = snapshot.data![0] as List<Episode>;
            final progressList = snapshot.data![1] as List<UserProgress>;
            final chapters = snapshot.data![2] as List<Chapter>;

            if (episodes.isEmpty) {
              return _buildEmptyBox();
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
            final groups = _groupEpisodesByChapter(episodes, chapters);

            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                // Sticky Top Bar with White 3D Back button
                Container(
                  color: Colors.white.withOpacity(0.7),
                  padding: const EdgeInsets.symmetric(horizontal: 40.0, vertical: 16.0),
                  child: Row(
                    children: [
                      Button3D.white(
                        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                        onPressed: () => Navigator.pop(context),
                        child: Row(
                          children: const [
                            Icon(Icons.arrow_back_rounded, size: 18, color: Color(0xFF64748B)),
                            SizedBox(width: 8),
                            Text('返回大厅', style: TextStyle(color: Color(0xFF64748B))),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),

                // Scrollable main content
                Expanded(
                  child: SingleChildScrollView(
                    physics: const BouncingScrollPhysics(),
                    padding: const EdgeInsets.symmetric(horizontal: 40.0, vertical: 24.0),
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
                                color: Colors.black.withOpacity(0.08),
                                blurRadius: 30,
                                offset: const Offset(0, 12),
                              )
                            ],
                          ),
                          padding: const EdgeInsets.all(48.0),
                          child: Row(
                            mainAxisAlignment: MainAxisAlignment.spaceBetween,
                            children: [
                              // Left content details
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Row(
                                      children: [
                                        _buildHeaderChip(widget.course.subject),
                                        const SizedBox(width: 10),
                                        _buildHeaderChip(widget.course.grade == 'universal' ? '通用' : '${widget.course.grade}年级'),
                                        if (firstTag != null) ...[
                                          const SizedBox(width: 10),
                                          _buildHeaderChip(firstTag),
                                        ],
                                      ],
                                    ),
                                    const SizedBox(height: 24),
                                    Text(
                                      widget.course.title,
                                      style: const TextStyle(
                                        fontSize: 36,
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
                                  ],
                                ),
                              ),

                              // Right rotated progress card
                              Transform.rotate(
                                angle: 3 * pi / 180, // Rotate slightly (3 degrees)
                                child: Container(
                                  padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
                                  decoration: BoxDecoration(
                                    color: Colors.white,
                                    borderRadius: BorderRadius.circular(28),
                                    boxShadow: const [
                                      BoxShadow(color: Colors.black12, blurRadius: 10)
                                    ],
                                    border: Border.all(color: Colors.white, width: 2),
                                  ),
                                  child: Column(
                                    children: [
                                      const Text(
                                        '学习进度',
                                        style: TextStyle(color: Color(0xFF94A3B8), fontSize: 11, fontWeight: FontWeight.w900),
                                      ),
                                      const SizedBox(height: 4),
                                      Row(
                                        crossAxisAlignment: CrossAxisAlignment.baseline,
                                        textBaseline: TextBaseline.alphabetic,
                                        children: [
                                          Text(
                                            '$progressPercent',
                                            style: const TextStyle(fontSize: 48, fontWeight: FontWeight.w900, color: Color(0xFF1E293B)),
                                          ),
                                          const Text(
                                            '%',
                                            style: TextStyle(fontSize: 18, color: Color(0xFF94A3B8), fontWeight: FontWeight.bold),
                                          ),
                                        ],
                                      ),
                                    ],
                                  ),
                                ),
                              ),
                            ],
                          ),
                        ),
                        const SizedBox(height: 40),

                        // Chapter Directory Panel
                        Container(
                          padding: const EdgeInsets.all(32),
                          decoration: BoxDecoration(
                            color: Colors.white,
                            borderRadius: BorderRadius.circular(36),
                            border: Border.all(color: const Color(0xFFE2E8F0), width: 2.0),
                            boxShadow: [
                              BoxShadow(
                                color: const Color(0xFF0F172A).withOpacity(0.02),
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
                                      color: const Color(0xFFEFF6FF),
                                      borderRadius: BorderRadius.circular(12),
                                    ),
                                    child: const Icon(Icons.list_alt_rounded, color: Color(0xFF2563EB), size: 24),
                                  ),
                                  const SizedBox(width: 14),
                                  const Text(
                                    '闯关目录',
                                    style: TextStyle(fontSize: 24, fontWeight: FontWeight.w900, color: AppTheme.textWhite),
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
                                                  gradient: const LinearGradient(
                                                    colors: [Color(0xFF60A5FA), Color(0xFF2563EB)],
                                                    begin: Alignment.topCenter,
                                                    end: Alignment.bottomCenter,
                                                  ),
                                                  borderRadius: BorderRadius.circular(3),
                                                ),
                                              ),
                                              const SizedBox(width: 12),
                                              Text(
                                                group.title,
                                                style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 18, color: AppTheme.textWhite),
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
                                              return _buildEpisodeRow(
                                                  ep, isCompleted,
                                                  resumeSeconds: resumeMap[ep.id] ?? 0,
                                                  totalSeconds: ep.durationSeconds);
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
                ),
              ],
            );
          },
        ),
      ),
    );
  }

  Widget _buildHeaderChip(String text) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 6),
      decoration: BoxDecoration(
        color: Colors.white.withOpacity(0.2),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: Colors.white.withOpacity(0.2)),
      ),
      child: Text(
        text,
        style: const TextStyle(color: Colors.white, fontSize: 12, fontWeight: FontWeight.w900),
      ),
    );
  }

  /// Groups episodes under their chapters in display order. Real chapters
  /// come first (in [sortOrder] then [id] order), each populated with the
  /// episodes whose [Episode.chapterId] matches. Any episodes left over
  /// (chapterId == 0 or pointing at a chapter not in the list) are collected
  /// into a trailing "全部课时" bucket.
  List<_GroupedChapter> _groupEpisodesByChapter(
      List<Episode> episodes, List<Chapter> chapters) {
    final sortedChapters = [...chapters]
      ..sort((a, b) {
        final c = a.sortOrder.compareTo(b.sortOrder);
        return c != 0 ? c : a.id.compareTo(b.id);
      });

    final byChapter = <int, List<Episode>>{};
    final ungrouped = <Episode>[];
    for (final ep in episodes) {
      if (ep.chapterId > 0 && sortedChapters.any((c) => c.id == ep.chapterId)) {
        byChapter.putIfAbsent(ep.chapterId, () => []).add(ep);
      } else {
        ungrouped.add(ep);
      }
    }

    final groups = <_GroupedChapter>[];
    for (final ch in sortedChapters) {
      final list = byChapter[ch.id];
      if (list != null && list.isNotEmpty) {
        groups.add(_GroupedChapter(title: ch.title, episodes: list, isUngrouped: false));
      }
    }
    if (ungrouped.isNotEmpty) {
      groups.add(_GroupedChapter(
        title: sortedChapters.isEmpty ? '全部课时' : '其他课时',
        episodes: ungrouped,
        isUngrouped: true,
      ));
    }
    // If there are no chapters and no episodes somehow, fall back to a single group.
    if (groups.isEmpty && episodes.isNotEmpty) {
      groups.add(_GroupedChapter(title: '全部课时', episodes: episodes, isUngrouped: true));
    }
    return groups;
  }

  Widget _buildEpisodeRow(Episode ep, bool isCompleted,
      {int resumeSeconds = 0, int totalSeconds = 0}) {
    String fmt(int s) {
      if (s <= 0) return '--:--';
      final h = s ~/ 3600;
      final m = (s % 3600) ~/ 60;
      final sec = s % 60;
      return h > 0
          ? '$h:${m.toString().padLeft(2, '0')}:${sec.toString().padLeft(2, '0')}'
          : '$m:${sec.toString().padLeft(2, '0')}';
    }

    final durationLabel = fmt(totalSeconds);
    final hasResume = resumeSeconds > 5 && !isCompleted;
    // Percentage watched (clamped, only meaningful when total is known).
    final resumePct = (hasResume && totalSeconds > 0)
        ? (resumeSeconds * 100 ~/ totalSeconds).clamp(0, 99)
        : 0;
    // Drive badges from real data: PDF attachment present + AI content present.
    final hasPdf = (_attachmentCache[ep.id] ?? const []).any((a) => a.isPdf);
    final hasSummary = (_aiCache[ep.id]?.preAdventureCards.isNotEmpty ?? false) ||
        (_aiCache[ep.id]?.postReviewQuiz.isNotEmpty ?? false);

    return Padding(
      padding: const EdgeInsets.only(bottom: 12.0),
      child: FocusButton(
        padding: const EdgeInsets.all(16.0),
        borderRadius: 20,
        borderColor: const Color(0xFFE2E8F0),
        onPressed: () {
          // Open Pre-Watch Modal ("探险任务卡") before playing!
          _showPreAdventureModal(context, ep);
        },
        child: Row(
          children: [
            // Status Circle Icon
            Container(
              width: 48,
              height: 48,
              decoration: BoxDecoration(
                color: isCompleted ? const Color(0xFFECFDF5) : const Color(0xFFF8FAFC),
                shape: BoxShape.circle,
                border: Border.all(
                  color: isCompleted ? const Color(0xFFA7F3D0) : const Color(0xFFCBD5E1),
                  width: 2.0,
                ),
              ),
              child: Icon(
                isCompleted ? Icons.check_circle_rounded : Icons.play_arrow_rounded,
                color: isCompleted ? AppTheme.accentGreen : const Color(0xFF94A3B8),
                size: 28,
              ),
            ),
            const SizedBox(width: 16),

            // Details info
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    ep.title,
                    style: TextStyle(
                      fontWeight: FontWeight.w800,
                      fontSize: 16,
                      color: isCompleted ? const Color(0xFF94A3B8) : AppTheme.textWhite,
                    ),
                  ),
                  const SizedBox(height: 8),

                  // Resource and Metadata row
                  Row(
                    children: [
                      // Duration tag
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                        decoration: BoxDecoration(
                          color: const Color(0xFFF1F5F9),
                          borderRadius: BorderRadius.circular(8),
                          border: Border.all(color: const Color(0xFFE2E8F0)),
                        ),
                        child: Row(
                          children: [
                            const Icon(Icons.watch_later_outlined, size: 12, color: AppTheme.textMuted),
                            const SizedBox(width: 4),
                            Text(
                              durationLabel,
                              style: const TextStyle(fontSize: 11, color: AppTheme.textMuted, fontWeight: FontWeight.bold),
                            ),
                          ],
                        ),
                      ),
                      const SizedBox(width: 10),

                      // Orange PDF Button
                      if (hasPdf)
                        GestureDetector(
                          onTap: () => _openResourceModal(context, 'pdf', ep),
                          child: Container(
                            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                            decoration: BoxDecoration(
                              color: const Color(0xFFFFF7ED),
                              borderRadius: BorderRadius.circular(8),
                              border: Border.all(color: const Color(0xFFFED7AA)),
                            ),
                            child: Row(
                              children: const [
                                Icon(Icons.picture_as_pdf_rounded, size: 12, color: Color(0xFFF97316)),
                                SizedBox(width: 4),
                                Text(
                                  '配套讲义',
                                  style: TextStyle(fontSize: 11, color: Color(0xFFC2410C), fontWeight: FontWeight.bold),
                                ),
                              ],
                            ),
                          ),
                        ),
                      const SizedBox(width: 10),

                      // Purple AI Summary Button
                      if (hasSummary)
                        GestureDetector(
                          onTap: () => _openResourceModal(context, 'summary', ep),
                          child: Container(
                            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                            decoration: BoxDecoration(
                              color: const Color(0xFFF5F3FF),
                              borderRadius: BorderRadius.circular(8),
                              border: Border.all(color: const Color(0xFFDDD6FE)),
                            ),
                            child: Row(
                              children: const [
                                Icon(Icons.auto_awesome_rounded, size: 12, color: Color(0xFF8B5CF6)),
                                SizedBox(width: 4),
                                Text(
                                  'AI 重点总结',
                                  style: TextStyle(fontSize: 11, color: Color(0xFF6D28D9), fontWeight: FontWeight.bold),
                                ),
                              ],
                            ),
                          ),
                        ),
                    ],
                  ),
                  // Resume progress indicator: shows how far the user got on
                  // this episode + a thin progress bar, so they can see at a
                  // glance where playback will resume.
                  if (hasResume) ...[
                    const SizedBox(height: 10),
                    Row(
                      children: [
                        const Icon(Icons.history_rounded,
                            size: 12, color: AppTheme.primaryColor),
                        const SizedBox(width: 4),
                        Text(
                          '已观看 $resumePct%  ·  续播 ${fmt(resumeSeconds)}',
                          style: const TextStyle(
                            fontSize: 11,
                            color: AppTheme.primaryColor,
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 6),
                    ClipRRect(
                      borderRadius: BorderRadius.circular(4),
                      child: LinearProgressIndicator(
                        value: resumePct / 100,
                        minHeight: 4,
                        backgroundColor: const Color(0xFFE2E8F0),
                        valueColor:
                            const AlwaysStoppedAnimation<Color>(AppTheme.primaryColor),
                      ),
                    ),
                  ],
                ],
              ),
            ),

            // Caret right
            const Icon(Icons.chevron_right_rounded, color: Color(0xFF94A3B8), size: 24),
          ],
        ),
      ),
    );
  }

  void _showPreAdventureModal(BuildContext context, Episode ep) {
    // Resolve the latest AI pre-adventure tasks for this episode. If the
    // background prefetch already filled the cache we use it directly;
    // otherwise we show a loading state inside the modal until it resolves.
    showDialog(
      context: context,
      barrierColor: const Color(0x900F172A), // dim background
      builder: (dialogContext) {
        return _PreAdventureDialog(
          episode: ep,
          activeUserId: widget.activeUserId,
          cachedTasks: _aiCache[ep.id]?.preAdventureCards
                  .map((c) => c.prompt)
                  .toList() ??
              const [],
          onStart: () {
            Navigator.pop(dialogContext); // close dialog
            // Play video — pass the real pre-adventure tasks along.
            final tasks = _aiCache[ep.id]?.preAdventureCards
                    .map((c) => c.prompt)
                    .toList() ??
                const <String>[];
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
          },
        );
      },
    );
  }

  void _openResourceModal(BuildContext context, String type, Episode ep) {
    final isPdf = type == 'pdf';
    showDialog(
      context: context,
      barrierColor: const Color(0x900F172A),
      builder: (context) {
        return Center(
          child: Container(
            constraints: const BoxConstraints(maxWidth: 700),
            height: MediaQuery.of(context).size.height * 0.75,
            child: GlassPanel(
              borderRadius: 32,
              baseColor: Colors.white,
              borderColor: Colors.white,
              borderWidth: 2,
              padding: const EdgeInsets.all(0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  // Title Bar
                  Container(
                    padding: const EdgeInsets.all(24),
                    decoration: BoxDecoration(
                      color: isPdf ? const Color(0xFFFFF7ED) : const Color(0xFFF5F3FF),
                      borderRadius: const BorderRadius.only(
                        topLeft: Radius.circular(30),
                        topRight: Radius.circular(30),
                      ),
                    ),
                    child: Row(
                      children: [
                        Container(
                          width: 44,
                          height: 44,
                          decoration: BoxDecoration(
                            color: isPdf ? const Color(0xFFFFF0E0) : const Color(0xFFEDE9FE),
                            borderRadius: BorderRadius.circular(12),
                          ),
                          child: Icon(
                            isPdf ? Icons.picture_as_pdf_rounded : Icons.auto_awesome_rounded,
                            color: isPdf ? const Color(0xFFF97316) : const Color(0xFF8B5CF6),
                          ),
                        ),
                        const SizedBox(width: 16),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                isPdf ? '课后讲义预览' : 'AI 核心知识总结',
                                style: TextStyle(
                                  fontWeight: FontWeight.w900,
                                  fontSize: 16,
                                  color: isPdf ? const Color(0xFF7C2D12) : const Color(0xFF4C1D95),
                                ),
                              ),
                              Text(
                                ep.title,
                                style: TextStyle(color: isPdf ? const Color(0xFFC2410C) : const Color(0xFF6D28D9), fontSize: 12),
                              ),
                            ],
                          ),
                        ),
                        IconButton(
                          onPressed: () => Navigator.pop(context),
                          icon: const Icon(Icons.close_rounded),
                        ),
                      ],
                    ),
                  ),

                  // Content Body
                  Expanded(
                    child: Container(
                      color: const Color(0xFFF8FAFC),
                      padding: const EdgeInsets.all(32),
                      child: isPdf
                          ? Container(
                              decoration: BoxDecoration(
                                color: Colors.white,
                                borderRadius: BorderRadius.circular(20),
                                border: Border.all(color: const Color(0xFFE2E8F0)),
                              ),
                              alignment: Alignment.center,
                              child: Column(
                                mainAxisAlignment: MainAxisAlignment.center,
                                children: [
                                  const Icon(Icons.picture_as_pdf_outlined, color: Color(0xFFF97316), size: 64),
                                  const SizedBox(height: 16),
                                  const Text('PDF 文件渲染器加载中...', style: TextStyle(fontWeight: FontWeight.w900)),
                                  const SizedBox(height: 8),
                                  const Text('这里将同步调用 syncfusion_flutter_pdfviewer 预览课件', style: TextStyle(color: AppTheme.textMuted, fontSize: 12)),
                                  const SizedBox(height: 24),
                                  Button3D.blue(
                                    onPressed: () => Navigator.pop(context),
                                    child: const Text('好的，关闭', style: TextStyle(color: Colors.white)),
                                  ),
                                ],
                              ),
                            )
                          : SingleChildScrollView(
                              physics: const BouncingScrollPhysics(),
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  _buildSummarySection('核心内容提要', '本讲详细讲述了雨来被抓住后机智巧妙跟鬼子军官周旋的过程。展示了小英雄极强的爱国主义精神和无畏拼搏的智慧。'),
                                  const SizedBox(height: 20),
                                  _buildSummarySection('重点知识梳理', '1. 生字词学习：晋察冀边区、扫荡、周旋\n2. 阅读技巧：如何通过对话描写分析人物性格特写\n3. 历史背景：了解抗日战争时期华北根据地少年儿童的斗争历史'),
                                  const SizedBox(height: 20),
                                  _buildSummarySection('随堂问题互动', '课后思考：雨来能够成功逃跑的核心原因是什么？请结合第三章课本文字分析描写。'),
                                ],
                              ),
                            ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }

  Widget _buildSummarySection(String title, String content) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: const Color(0xFFE2E8F0)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(width: 4, height: 16, decoration: BoxDecoration(color: const Color(0xFF8B5CF6), borderRadius: BorderRadius.circular(2))),
              const SizedBox(width: 8),
              Text(title, style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 15, color: Color(0xFF4C1D95))),
            ],
          ),
          const SizedBox(height: 12),
          Text(content, style: const TextStyle(color: Color(0xFF475569), fontSize: 13, height: 1.5, fontWeight: FontWeight.bold)),
        ],
      ),
    );
  }

  Widget _buildErrorBox(String error) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.error_outline, size: 48, color: Colors.redAccent),
          const SizedBox(height: 16),
          const Text('加载失败，请重试！', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16)),
          const SizedBox(height: 8),
          Text(error, style: const TextStyle(color: AppTheme.textMuted), textAlign: TextAlign.center),
          const SizedBox(height: 24),
          Button3D.blue(
            onPressed: _refreshData,
            child: const Text('重试加载'),
          ),
        ],
      ),
    );
  }

  Widget _buildEmptyBox() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.video_library_outlined, size: 48, color: AppTheme.textMuted),
          const SizedBox(height: 16),
          const Text('该课程库下暂无课时视频', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 18)),
          const SizedBox(height: 8),
          const Text('请登录管理后台导入相关的网盘视频资源。', style: TextStyle(color: AppTheme.textMuted)),
          const SizedBox(height: 24),
          Button3D.blue(
            onPressed: _refreshData,
            child: const Text('刷新页面'),
          ),
        ],
      ),
    );
  }
}

/// Pre-adventure task card dialog. Shows the AI-generated exploration prompts
/// for an episode; if no tasks are available yet it falls back to a graceful
/// "no tasks" state instead of fabricating mock content.
class _PreAdventureDialog extends StatefulWidget {
  final Episode episode;
  final int activeUserId;
  final List<String> cachedTasks;
  final VoidCallback onStart;

  const _PreAdventureDialog({
    required this.episode,
    required this.activeUserId,
    required this.cachedTasks,
    required this.onStart,
  });

  @override
  State<_PreAdventureDialog> createState() => _PreAdventureDialogState();
}

class _PreAdventureDialogState extends State<_PreAdventureDialog> {
  late List<String> _tasks;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _tasks = widget.cachedTasks;
    // If the background prefetch hadn't populated tasks yet, fetch on demand.
    if (_tasks.isEmpty) {
      _loadTasks();
    }
  }

  Future<void> _loadTasks() async {
    setState(() => _loading = true);
    try {
      final ai = await ApiService.fetchAILesson(widget.activeUserId, widget.episode.id);
      if (mounted) {
        setState(() {
          _tasks = ai?.preAdventureCards.map((c) => c.prompt).toList() ?? const [];
          _loading = false;
        });
      }
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Container(
        constraints: const BoxConstraints(maxWidth: 550),
        child: GlassPanel(
          borderRadius: 36,
          baseColor: Colors.white,
          borderColor: Colors.white,
          borderWidth: 2,
          padding: const EdgeInsets.all(0),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // Header gradient banner
              Container(
                decoration: const BoxDecoration(
                  gradient: LinearGradient(
                    colors: [Color(0xFF3B82F6), Color(0xFF6366F1)],
                  ),
                  borderRadius: BorderRadius.only(
                    topLeft: Radius.circular(34),
                    topRight: Radius.circular(34),
                  ),
                ),
                padding: const EdgeInsets.all(32),
                child: Column(
                  children: [
                    Container(
                      width: 64,
                      height: 64,
                      decoration: BoxDecoration(
                        color: Colors.white.withOpacity(0.2),
                        borderRadius: BorderRadius.circular(20),
                        border: Border.all(color: Colors.white.withOpacity(0.3), width: 1.5),
                      ),
                      child: const Icon(Icons.casino_rounded, color: Colors.white, size: 36),
                    ),
                    const SizedBox(height: 16),
                    const Text('探险任务卡',
                        style: TextStyle(fontSize: 24, fontWeight: FontWeight.w900, color: Colors.white)),
                    const SizedBox(height: 8),
                    Text('即将探索：${widget.episode.title}',
                        style: const TextStyle(color: Colors.white70, fontSize: 13, fontWeight: FontWeight.bold)),
                  ],
                ),
              ),

              // Tasks list
              Padding(
                padding: const EdgeInsets.all(32),
                child: Column(
                  children: [
                    Row(
                      mainAxisAlignment: MainAxisAlignment.center,
                      children: [
                        const Icon(Icons.info_outline_rounded, color: Color(0xFF3B82F6), size: 20),
                        const SizedBox(width: 8),
                        Text(
                          _tasks.isEmpty ? '本节暂未生成探险任务' : '带上这 ${_tasks.length} 个秘密任务出发吧：',
                          style: const TextStyle(fontWeight: FontWeight.w900, color: Color(0xFF64748B), fontSize: 15),
                        ),
                      ],
                    ),
                    const SizedBox(height: 24),
                    if (_loading)
                      const Padding(
                        padding: EdgeInsets.symmetric(vertical: 24),
                        child: CircularProgressIndicator(color: AppTheme.primaryColor),
                      )
                    else if (_tasks.isEmpty)
                      const Padding(
                        padding: EdgeInsets.symmetric(vertical: 16),
                        child: Text('AI 老师还没布置任务，可以直接开始观看～',
                            style: TextStyle(color: AppTheme.textMuted, fontWeight: FontWeight.bold),
                            textAlign: TextAlign.center),
                      )
                    else
                      Column(
                        children: List.generate(_tasks.length, (i) {
                          return Padding(
                            padding: EdgeInsets.only(bottom: i == _tasks.length - 1 ? 0 : 12),
                            child: _PreAdventureTaskRow(index: i + 1, text: _tasks[i]),
                          );
                        }),
                      ),
                    const SizedBox(height: 32),

                    // Action Button
                    Button3D.blue(
                      onPressed: widget.onStart,
                      padding: const EdgeInsets.symmetric(vertical: 16),
                      child: Row(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: const [
                          Text('接受任务，开始播放',
                              style: TextStyle(fontSize: 18, color: Colors.white, fontWeight: FontWeight.w900)),
                          SizedBox(width: 8),
                          Icon(Icons.rocket_launch_rounded, color: Colors.white, size: 20),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _PreAdventureTaskRow extends StatelessWidget {
  final int index;
  final String text;
  const _PreAdventureTaskRow({required this.index, required this.text});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: const Color(0xFFF1F5F9), width: 2),
        boxShadow: const [BoxShadow(color: Color(0x03000000), blurRadius: 4, offset: Offset(0, 2))],
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 28,
            height: 28,
            alignment: Alignment.center,
            decoration: BoxDecoration(
              color: const Color(0xFFEFF6FF),
              borderRadius: BorderRadius.circular(8),
              border: Border.all(color: const Color(0xFFDBEAFE)),
            ),
            child: Text('$index',
                style: const TextStyle(fontWeight: FontWeight.w900, color: Color(0xFF2563EB), fontSize: 13)),
          ),
          const SizedBox(width: 14),
          Expanded(
            child: Text(text,
                style: const TextStyle(color: Color(0xFF475569), fontWeight: FontWeight.bold, fontSize: 14, height: 1.4)),
          ),
        ],
      ),
    );
  }
}

/// Display-only grouping of episodes under a chapter title.
class _GroupedChapter {
  final String title;
  final List<Episode> episodes;
  final bool isUngrouped;

  const _GroupedChapter({
    required this.title,
    required this.episodes,
    required this.isUngrouped,
  });
}
