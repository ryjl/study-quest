import 'dart:math';
import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../model/progress.dart';
import '../../model/quiz.dart';
import '../../model/subject.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../ai/ai_availability.dart';
import '../widget/focus_button.dart';
import '../widget/glass_panel.dart';
import '../widget/button_3d.dart';
import '../widget/state_widgets.dart';
import '../widget/subject_icon.dart';
import '../responsive.dart';
import 'player_screen.dart';
import 'ai_study_screen.dart';

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
  // Cached combined future — FutureBuilder must see a STABLE future reference
  // across rebuilds, otherwise each setState (e.g. enrichment prefetch filling
  // the AI/attachment caches) makes FutureBuilder re-subscribe, flip to
  // ConnectionState.waiting, flash the loading spinner, then flip back. On real
  // devices this shows as continuous flicker making the list untappable; MuMu
  // (fast x86) hides it because the waiting→done flip lands within one frame.
  // Built once per load in _refreshData; the FutureBuilder reads this.
  late Future<List<dynamic>> _combinedFuture;

  /// Per-episode attachments cache, keyed by episode id. Fetched lazily once
  /// the episode list is available so the row badge ("配套讲义") reflects real
  /// data instead of a mock toggle.
  final Map<int, List<Attachment>> _attachmentCache = {};
  // summary 缓存。课前探险问题的数据源是 /ai-summary 的 pre_adventure。这里缓存
  // 避免列表行点击弹窗 + 播放器进入时重复请求同一个 summary。失败/老数据存 null,
  // 取不到就降级为"暂无探索任务"。
  final Map<int, EpisodeSummary?> _summaryCache = {};

  // Subject catalog for resolving the course's subject key → label/color.
  List<Subject> _subjectsCatalog = const [];

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
    // Compose the combined future ONCE here so FutureBuilder sees a stable
    // reference across rebuilds (see _combinedFuture comment above).
    _combinedFuture = Future.wait([_episodesFuture, _progressFuture, _chaptersFuture]);
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
      try {
        final atts = await ApiService.fetchAttachments(widget.activeUserId, ep.id);
        _attachmentCache[ep.id] = atts;
        changed = true;
      } catch (_) {
        // Enrichment is best-effort; rows simply fall back to no-badge.
      }
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
    final subjectGradient = AppTheme.getSubjectGradientFromColor(resolveSubject(widget.course.subject, _subjectsCatalog).color);
    // Use the course's real first tag if defined (no more mock tags).
    final tagsList = widget.course.tagsList;
    final firstTag = tagsList.isNotEmpty ? tagsList.first : null;

    return Scaffold(
      body: Container(
        color: AppTheme.backgroundColor, // slate-50 background
        child: FutureBuilder(
          future: _combinedFuture,
          builder: (context, AsyncSnapshot<List<dynamic>> snapshot) {
            if (snapshot.connectionState == ConnectionState.waiting) {
              return loadingSpinner();
            }
            if (snapshot.hasError) {
              return errorStateBox(snapshot.error.toString(), _refreshData);
            }

            final episodes = snapshot.data![0] as List<Episode>;
            final progressList = snapshot.data![1] as List<UserProgress>;
            final chapters = snapshot.data![2] as List<Chapter>;

            if (episodes.isEmpty) {
              return emptyStateBox(
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
            final groups = _groupEpisodesByChapter(episodes, chapters);

            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                // Sticky Top Bar with White 3D Back button
                Container(
                  color: Colors.white.withOpacity(0.7),
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
                                color: Colors.black.withOpacity(0.08),
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

                        // Chapter Directory Panel
                        Container(
                          padding: EdgeInsets.all(isPortrait(context) ? 16 : 32),
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

  /// Hero card content: course details + progress card. Wide: side-by-side
  /// row. Narrow (portrait): stacked vertically, details on top and the
  /// progress card below.
  Widget _buildHeroContent(
    BuildContext context, {
    required List<Episode> episodes,
    required String? firstTag,
    required int progressPercent,
  }) {
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
      ],
    );

    final progressCard = Transform.rotate(
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
        color: Colors.white.withOpacity(0.2),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: Colors.white.withOpacity(0.2)),
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

  Widget _buildThumbnailPlaceholder() {
    return Container(
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          colors: [Color(0xFF94A3B8), Color(0xFF64748B)],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
      ),
      child: const Icon(
        Icons.video_file_rounded,
        color: Colors.white54,
        size: 24,
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
    // Phase 2:AI 学习按钮的三态由 episode 上的 AI 开关 + 字幕标志决定。
    // 按钮始终展示(保持入口可见),但不可用时变灰且点击只弹提示:
    //   - enabled:亮色,进入 AiStudyScreen
    //   - noSubtitle:灰色,提示"本节没有字幕,AI 功能暂不可用"
    //   - disabled:灰色,提示"本课程未开启 AI 学习"
    final aiAvailability = AiAvailabilityHelper.fromEpisode(ep);

    // Locked episodes render as a greyed-out row with a lock affordance and
    // refuse to open the player — the unlock schedule (drip) keeps them
    // invisible to play-info anyway, so this just stops the tap from producing
    // a confusing 403.
    if (ep.locked) {
      return Padding(
        padding: const EdgeInsets.only(bottom: 12.0),
        child: FocusButton(
          padding: const EdgeInsets.all(16.0),
          borderRadius: 20,
          borderColor: const Color(0xFFE2E8F0),
          onPressed: () {
            ScaffoldMessenger.of(context).showSnackBar(
              const SnackBar(
                content: Text('🔒 这一节还没解锁，耐心等待吧～'),
                duration: Duration(seconds: 2),
              ),
            );
          },
          child: Row(
            children: [
              Container(
                width: 120,
                height: 68,
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(12),
                  color: const Color(0xFFF1F5F9),
                  border: Border.all(color: const Color(0xFFE2E8F0), width: 1.5),
                ),
                child: const Center(
                  child: Icon(Icons.lock_outline_rounded, color: Color(0xFF94A3B8), size: 26),
                ),
              ),
              const SizedBox(width: 16),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      ep.title,
                      style: const TextStyle(
                        fontWeight: FontWeight.w800,
                        fontSize: 16,
                        color: Color(0xFF94A3B8),
                      ),
                    ),
                    const SizedBox(height: 8),
                    Row(
                      children: const [
                        Icon(Icons.lock_clock_outlined, size: 12, color: Color(0xFF94A3B8)),
                        SizedBox(width: 4),
                        Text(
                          '等待解锁',
                          style: TextStyle(fontSize: 11, color: Color(0xFF94A3B8), fontWeight: FontWeight.bold),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      );
    }

    return Padding(
      padding: const EdgeInsets.only(bottom: 12.0),
      child: FocusButton(
        padding: const EdgeInsets.all(16.0),
        borderRadius: 20,
        borderColor: const Color(0xFFE2E8F0),
        onPressed: () {
          // 直接播放:课前探索任务已改为在播放器右侧 helper panel 常驻显示
          // (player_screen 的 _buildPreAdventureSection),不再前置弹窗打断。
          _playEpisode(ep);
        },
        child: Row(
          children: [
            // Video Thumbnail
            Container(
              width: 120,
              height: 68,
              decoration: BoxDecoration(
                borderRadius: BorderRadius.circular(12),
                color: const Color(0xFFF1F5F9),
                border: Border.all(
                  color: isCompleted ? const Color(0xFFA7F3D0) : const Color(0xFFCBD5E1),
                  width: 1.5,
                ),
              ),
              child: ClipRRect(
                borderRadius: BorderRadius.circular(10.5),
                child: Stack(
                  fit: StackFit.expand,
                  children: [
                    // Cover Image
                    ep.coverUrl.isNotEmpty
                        ? Image.network(
                            ApiService.absoluteUrl(ep.coverUrl),
                            fit: BoxFit.cover,
                            errorBuilder: (context, error, stackTrace) => _buildThumbnailPlaceholder(),
                          )
                        : _buildThumbnailPlaceholder(),

                    // Semi-transparent dark overlay for play button visibility
                    Container(
                      color: Colors.black.withOpacity(0.15),
                    ),

                    // Status Circle Overlay (Play / Complete check) in the center
                    Center(
                      child: Container(
                        width: 32,
                        height: 32,
                        decoration: BoxDecoration(
                          color: isCompleted
                              ? const Color(0xFFECFDF5).withOpacity(0.9)
                              : Colors.white.withOpacity(0.9),
                          shape: BoxShape.circle,
                          boxShadow: [
                            BoxShadow(
                              color: Colors.black.withOpacity(0.1),
                              blurRadius: 4,
                              offset: const Offset(0, 2),
                            )
                          ],
                        ),
                        child: Icon(
                          isCompleted ? Icons.check_rounded : Icons.play_arrow_rounded,
                          color: isCompleted ? AppTheme.accentGreen : AppTheme.primaryColor,
                          size: 20,
                        ),
                      ),
                    ),
                  ],
                ),
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

                  // Resource and Metadata row — Wrap so tags reflow instead of
                  // overflowing on narrow portrait widths.
                  Wrap(
                    spacing: 10,
                    runSpacing: 6,
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
                          mainAxisSize: MainAxisSize.min,
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
                              mainAxisSize: MainAxisSize.min,
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

                      // Purple AI Study Button — opens the AI study page (summary +
                      // practice). Phase 2:按钮始终展示,但可用性三态化。
                      // enabled 才真正跳转,否则弹 SnackBar 提示原因。
                      Builder(builder: (btnContext) {
                        // 不可用时整套配色降到中性灰,视觉上明确"现在用不了"。
                        final enabled = aiAvailability == AiAvailability.enabled;
                        final bgColor = enabled ? const Color(0xFFF5F3FF) : const Color(0xFFF1F5F9);
                        final borderColor = enabled ? const Color(0xFFDDD6FE) : const Color(0xFFE2E8F0);
                        final iconColor = enabled ? const Color(0xFF8B5CF6) : const Color(0xFF94A3B8);
                        final textColor = enabled ? const Color(0xFF6D28D9) : const Color(0xFF94A3B8);
                        return GestureDetector(
                          onTap: () {
                            if (!enabled) {
                              ScaffoldMessenger.of(btnContext).showSnackBar(
                                SnackBar(
                                  content: Text(AiAvailabilityHelper.tooltipFor(aiAvailability)!),
                                  duration: const Duration(seconds: 2),
                                ),
                              );
                              return;
                            }
                            Navigator.of(context).push(
                              MaterialPageRoute(
                                builder: (context) => AiStudyScreen(
                                  activeUserId: widget.activeUserId,
                                  episode: ep,
                                ),
                              ),
                            );
                          },
                          child: Container(
                            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                            decoration: BoxDecoration(
                              color: bgColor,
                              borderRadius: BorderRadius.circular(8),
                              border: Border.all(color: borderColor),
                            ),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Icon(Icons.auto_awesome_rounded, size: 12, color: iconColor),
                                const SizedBox(width: 4),
                                Text(
                                  'AI 学习',
                                  style: TextStyle(fontSize: 11, color: textColor, fontWeight: FontWeight.bold),
                                ),
                              ],
                            ),
                          ),
                        );
                      }),
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

  void _showPreAdventureModal(BuildContext context, Episode ep) {
    // Resolve the latest AI pre-adventure tasks for this episode. Phase 2 起从
    // summary.pre_adventure 取;若 prefetch 已填缓存直接用,否则弹窗内 lazy fetch。
    showDialog(
      context: context,
      barrierColor: const Color(0x900F172A), // dim background
      builder: (dialogContext) {
        return _PreAdventureDialog(
          episode: ep,
          activeUserId: widget.activeUserId,
          cachedTasks: _cachedPreAdventureTasks(ep.id),
          onStart: () {
            Navigator.pop(dialogContext); // close dialog
            _playEpisode(ep);
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
                      padding: EdgeInsets.all(isPortrait(context) ? 16 : 32),
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
      // Phase 2:课前任务数据源切到 /ai-summary 的 pre_adventure。
      // 失败 / 老数据 / 未生成时为空,弹窗自动降级为"AI 老师还没布置任务"。
      final summary = await ApiService.fetchEpisodeSummary(widget.activeUserId, widget.episode.id);
      if (mounted) {
        setState(() {
          _tasks = summary?.preAdventure.map((p) => p.prompt).toList() ?? const [];
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
