import 'dart:math';
import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../model/progress.dart';
import '../../model/subject.dart';
import '../../model/tag.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/button_3d.dart';
import 'course_detail_screen.dart';

// Backend stores grade as a stable key ("1".."9", "universal"); the UI shows
// Chinese labels. These maps bridge the two so filtering compares keys and
// display resolves to labels (fixes the old "'3'+'年级'='3年级' ≠ '三年级'" bug).
const Map<String, String> _gradeLabels = {
  '1': '一年级', '2': '二年级', '3': '三年级', '4': '四年级',
  '5': '五年级', '6': '六年级', '7': '七年级', '8': '八年级', '9': '九年级',
  'universal': '通用',
};
String gradeLabelOf(String key) => _gradeLabels[key] ?? '$key 年级';
// Filter chips: "全部" + each grade key, shown via gradeLabelOf.
final List<String> _gradeFilterKeys = ['全部', ..._gradeLabels.keys];

class CourseListScreen extends StatefulWidget {
  final int activeUserId;

  const CourseListScreen({Key? key, required this.activeUserId}) : super(key: key);

  @override
  State<CourseListScreen> createState() => _CourseListScreenState();
}

class _CourseListScreenState extends State<CourseListScreen> {
  late Future<List<Course>> _coursesFuture;
  late Future<List<UserProgress>> _progressFuture;
  // Cached combined future — FutureBuilder must see a STABLE future reference
  // across rebuilds, otherwise each setState (e.g. typing in the search box)
  // makes FutureBuilder re-subscribe, flip to ConnectionState.waiting, rebuild
  // the whole subtree, and blow away the TextField's focus/input mid-keystroke.
  late final Future<List<dynamic>> _combinedFuture;

  String _selectedSubject = '全部';
  String _selectedGrade = '全部';
  String _searchQuery = '';

  // Subject catalog fetched from /api/v1/subjects. Drives the filter chips and
  // the key→label/emoji/color lookups on each card. Empty until loaded; the
  // fallback in _subjectMeta keeps the UI working pre-load / on fetch failure.
  List<Subject> _subjectsCatalog = const [];
  static const Subject _fallbackSubject = Subject(
    key: '', label: '科目', emoji: '📦', color: '#9ca3af',
  );

  // Tag catalog fetched from /api/v1/tags. Drives the multi-select filter
  // chips. Selection stores tag IDs and matches against Course.tagIds, so it
  // stays correct even if a tag's label is later renamed.
  List<Tag> _tagsCatalog = const [];
  final Set<int> _selectedTagIDs = {};

  /// "全部" + one entry per subject key. Refreshed when the catalog loads.
  List<String> get _subjectFilters => [
    '全部',
    ..._subjectsCatalog.map((s) => s.key),
  ];

  Subject _subjectMeta(String key) {
    for (final s in _subjectsCatalog) {
      if (s.key == key) return s;
    }
    return _fallbackSubject.copyWith(label: key.isEmpty ? '科目' : key, key: key);
  }

  @override
  void initState() {
    super.initState();
    _loadData();
  }

  void _loadData() {
    _coursesFuture = ApiService.fetchCourses(widget.activeUserId);
    _progressFuture = ApiService.fetchProgressOverview(widget.activeUserId);
    // Build the combined future ONCE per load; the FutureBuilder reads this
    // stable reference on every rebuild instead of constructing a new
    // Future.wait (which would re-subscribe and reset the subtree).
    _combinedFuture = Future.wait([_coursesFuture, _progressFuture]);
    setState(() {});
    // Load the subject catalog in the background; non-fatal on failure.
    ApiService.fetchSubjects(widget.activeUserId).then((list) {
      if (mounted) setState(() => _subjectsCatalog = list);
    });
    // Load the tag catalog the same way (drives the multi-select chips).
    ApiService.fetchTags(widget.activeUserId).then((list) {
      if (mounted) setState(() => _tagsCatalog = list);
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.transparent,
      body: FutureBuilder(
        future: _combinedFuture,
        builder: (context, AsyncSnapshot<List<dynamic>> snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator(color: AppTheme.primaryColor));
          }
          if (snapshot.hasError) {
            return _buildErrorBox(snapshot.error.toString());
          }

          final courses = snapshot.data![0] as List<Course>;
          final progressList = snapshot.data![1] as List<UserProgress>;
          // Keep progressList referenced for future per-course resume logic;
          // it intentionally influences the "继续学习" target choice below.

          // Apply filters — tags now come from real course data.
          final filteredCourses = courses.where((c) {
            final matchSearch = c.title.toLowerCase().contains(_searchQuery.toLowerCase());
            final matchSubject = _selectedSubject == '全部' || c.subject == _selectedSubject;
            final matchGrade = _selectedGrade == '全部' || c.grade == _selectedGrade;
            // Tag multi-select: course matches if it carries ANY of the
            // selected tag IDs (intersection of selected set and course tagIds).
            final matchTag = _selectedTagIDs.isEmpty ||
                c.tagIds.any((id) => _selectedTagIDs.contains(id));
            return matchSearch && matchSubject && matchGrade && matchTag;
          }).toList();

          // Pick the most relevant course for the "继续学习" button. If the user
          // has any in-progress episodes, prefer a course other than the first
          // so it feels like a real "pick up where you left off" cue.
          Course? continueCourse = filteredCourses.isNotEmpty ? filteredCourses.first : null;
          if (progressList.any((p) => !p.isCompleted) && filteredCourses.length > 1) {
            continueCourse = filteredCourses[1];
          }

          return Padding(
            padding: const EdgeInsets.symmetric(horizontal: 20.0, vertical: 16.0),
            // Wrap the whole layout in a vertical scroller. Without this the
            // fixed-height Column overflows ("bottom overflowed by N pixels")
            // whenever the course grid + filters exceed the viewport (e.g.
            // many subjects/tags, large font, smaller window).
            child: SingleChildScrollView(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                // Header Area
                Row(
                  mainAxisAlignment: MainAxisAlignment.spaceBetween,
                  children: [
                    Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: const [
                        Text(
                          '学习大厅',
                          style: TextStyle(
                            fontFamily: 'Quicksand',
                            fontSize: 32,
                            fontWeight: FontWeight.w900,
                            color: AppTheme.textWhite,
                          ),
                        ),
                        SizedBox(height: 6),
                        Text(
                          '今天想探索哪个领域呢？🚀',
                          style: TextStyle(
                            fontSize: 15,
                            color: AppTheme.textMuted,
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                      ],
                    ),

                    // Continue Learning Button (3D) — opens the most relevant course
                    if (continueCourse != null)
                      Button3D.blue(
                        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 8),
                        onPressed: () {
                          Navigator.push(
                            context,
                            MaterialPageRoute(
                              builder: (context) => CourseDetailScreen(
                                activeUserId: widget.activeUserId,
                                course: continueCourse!,
                              ),
                            ),
                          ).then((_) => _loadData());
                        },
                        child: Row(
                          children: [
                            const Icon(Icons.play_circle_fill_rounded, color: Colors.white, size: 36),
                            const SizedBox(width: 12),
                            Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                const Text(
                                  '继续学习',
                                  style: TextStyle(color: Color(0xFFDBEAFE), fontSize: 10, fontWeight: FontWeight.bold),
                                ),
                                Text(
                                  continueCourse.title,
                                  style: const TextStyle(color: Colors.white, fontSize: 14, fontWeight: FontWeight.w900),
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                              ],
                            ),
                          ],
                        ),
                      ),
                  ],
                ),
                const SizedBox(height: 32),

                // Subject chips (full width, horizontally scrollable)
                SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  physics: const BouncingScrollPhysics(),
                  child: Row(
                    children: _subjectFilters.map((subj) {
                      final active = _selectedSubject == subj;
                      final label = subj == '全部'
                          ? '全部'
                          : _subjectMeta(subj).label;
                      return Padding(
                        padding: const EdgeInsets.only(right: 12.0),
                        child: active
                            ? Button3D.dark(
                                padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                                onPressed: () => setState(() => _selectedSubject = subj),
                                child: Text(label, style: const TextStyle(fontSize: 14)),
                              )
                            : Button3D.white(
                                padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                                onPressed: () => setState(() => _selectedSubject = subj),
                                child: Text(label, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w900)),
                              ),
                      );
                    }).toList(),
                  ),
                ),
                const SizedBox(height: 12),

                // Search input (own row, full width — no longer squeezes the
                // subject chips above it).
                TextField(
                  onChanged: (val) => setState(() => _searchQuery = val),
                  style: const TextStyle(fontSize: 14, fontWeight: FontWeight.bold, color: AppTheme.textWhite),
                  decoration: InputDecoration(
                    hintText: '搜索课程名称...',
                    hintStyle: const TextStyle(color: AppTheme.textMuted),
                    prefixIcon: const Icon(Icons.search_rounded, color: AppTheme.textMuted),
                    filled: true,
                    fillColor: Colors.white,
                    contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(16),
                      borderSide: const BorderSide(color: Color(0xFFE2E8F0), width: 2.0),
                    ),
                    focusedBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(16),
                      borderSide: const BorderSide(color: AppTheme.primaryColor, width: 2.0),
                    ),
                    enabledBorder: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(16),
                      borderSide: const BorderSide(color: Color(0xFFE2E8F0), width: 2.0),
                    ),
                  ),
                ),
                const SizedBox(height: 24),

                // Grade & Tag Dropdowns
                Container(
                  padding: const EdgeInsets.all(10),
                  decoration: BoxDecoration(
                    color: Colors.white,
                    borderRadius: BorderRadius.circular(20),
                    border: Border.all(color: const Color(0xFFE2E8F0), width: 2.0),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      // Grade Selector
                      _buildDropdown(
                        icon: Icons.school_rounded,
                        color: Colors.blue,
                        value: _selectedGrade,
                        items: _gradeFilterKeys,
                        labelOf: gradeLabelOf,
                        onChanged: (val) => setState(() => _selectedGrade = val!),
                      ),
                      Container(width: 2, height: 24, color: const Color(0xFFE2E8F0), margin: const EdgeInsets.symmetric(horizontal: 16)),

                      // Tag multi-select chips (tappable to toggle). Empty when
                      // the catalog hasn't loaded yet — falls back to nothing.
                      if (_tagsCatalog.isNotEmpty)
                        Expanded(
                          child: SingleChildScrollView(
                            scrollDirection: Axis.horizontal,
                            child: Row(
                              children: _tagsCatalog.map((t) {
                                final selected = _selectedTagIDs.contains(t.id);
                                return Padding(
                                  padding: const EdgeInsets.only(right: 8),
                                  child: Button3D.white(
                                    padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
                                    onPressed: () => setState(() {
                                      if (selected) {
                                        _selectedTagIDs.remove(t.id);
                                      } else {
                                        _selectedTagIDs.add(t.id);
                                      }
                                    }),
                                    child: Row(
                                      mainAxisSize: MainAxisSize.min,
                                      children: [
                                        Text(
                                          t.label,
                                          style: TextStyle(
                                            fontSize: 13,
                                            fontWeight: FontWeight.bold,
                                            color: selected ? const Color(0xFF10B981) : AppTheme.textMuted,
                                          ),
                                        ),
                                        if (selected) ...[
                                          const SizedBox(width: 4),
                                          const Icon(Icons.check, size: 14, color: Color(0xFF10B981)),
                                        ],
                                      ],
                                    ),
                                  ),
                                );
                              }).toList(),
                            ),
                          ),
                        ),

                      const SizedBox(width: 24),
                      Text(
                        '找到 ${filteredCourses.length} 门',
                        style: const TextStyle(color: AppTheme.textMuted, fontSize: 13, fontWeight: FontWeight.bold),
                      ),
                    ],
                  ),
                ),
                const SizedBox(height: 32),

                // Grid view of course cards. shrinkWrap so it sizes to its
                // content inside the outer SingleChildScrollView (no Expanded
                // allowed in an unbounded-height scroll view).
                filteredCourses.isEmpty
                    ? _buildEmptyBox()
                    : LayoutBuilder(
                        builder: (context, constraints) {
                          final width = constraints.maxWidth;
                          final crossAxisCount = width > 1200 ? 4 : (width > 800 ? 3 : 2);
                          return GridView.builder(
                            shrinkWrap: true,
                            physics: const NeverScrollableScrollPhysics(),
                            itemCount: filteredCourses.length,
                            gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                              crossAxisCount: crossAxisCount,
                              crossAxisSpacing: 16,
                              mainAxisSpacing: 16,
                              childAspectRatio: 0.86,
                            ),
                            itemBuilder: (context, index) {
                              return _buildCourseCard(filteredCourses[index]);
                            },
                          );
                        },
                      ),
              ],
            ),
          ),
        );
        },
      ),
    );
  }

  Widget _buildDropdown({
    required IconData icon,
    required MaterialColor color,
    required String value,
    required List<String> items,
    required ValueChanged<String?> onChanged,
    String Function(String)? labelOf,
  }) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, color: color, size: 20),
        const SizedBox(width: 8),
        DropdownButton<String>(
          value: value,
          onChanged: onChanged,
          underline: const SizedBox(),
          icon: const Icon(Icons.arrow_drop_down, color: AppTheme.textMuted),
          style: TextStyle(color: color.shade800, fontWeight: FontWeight.bold, fontSize: 14),
          dropdownColor: Colors.white,
          items: items.map((item) {
            final label = labelOf != null ? labelOf(item) : item;
            return DropdownMenuItem<String>(
              value: item,
              child: Text(item == '全部' ? '所有' : label),
            );
          }).toList(),
        ),
      ],
    );
  }

  Widget _buildSubjectWatermark(String subject) {
    IconData iconData;
    switch (subject.toLowerCase()) {
      case 'chinese':
      case '语文':
        iconData = Icons.menu_book_rounded;
        break;
      case 'math':
      case '数学':
        iconData = Icons.calculate_rounded;
        break;
      case 'english':
      case '英语':
        iconData = Icons.translate_rounded;
        break;
      case 'physics':
      case '科学':
        iconData = Icons.science_rounded;
        break;
      case 'extra':
      case '百科':
        iconData = Icons.public_rounded;
        break;
      case '兴趣':
        iconData = Icons.extension_rounded;
        break;
      case '综合':
        iconData = Icons.map_rounded;
        break;
      default:
        iconData = Icons.school_rounded;
    }

    return Positioned(
      right: -24,
      bottom: -24,
      child: Transform.rotate(
        angle: -15 * pi / 180,
        child: Icon(
          iconData,
          size: 130,
          color: Colors.white.withOpacity(0.15),
        ),
      ),
    );
  }

  Widget _buildCourseCard(Course course) {
    // Real first tag (replaces mock tag rotation).
    final tags = course.tagsList;
    final tag = tags.isNotEmpty ? tags.first : '';
    final gradeLabel = gradeLabelOf(course.grade);
    final cardLabel = tag.isEmpty ? gradeLabel : '$tag | $gradeLabel';

    return FocusButton(
      padding: EdgeInsets.zero,
      borderRadius: 28,
      borderColor: const Color(0xFFE2E8F0),
      onPressed: () {
        Navigator.push(
          context,
          MaterialPageRoute(
            builder: (context) => CourseDetailScreen(
              activeUserId: widget.activeUserId,
              course: course,
            ),
          ),
        ).then((_) => _loadData());
      },
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // Dynamic gradient banner header. Banner:info ratio is 1:1 (was 5:4)
          // so the card reads as balanced rather than top-heavy now that the
          // overall card is shorter (aspectRatio 0.86).
          Expanded(
            flex: 1,
            child: Container(
              decoration: BoxDecoration(
                gradient: AppTheme.getSubjectGradientFromColor(_subjectMeta(course.subject).color),
                borderRadius: const BorderRadius.only(
                  topLeft: Radius.circular(26),
                  topRight: Radius.circular(26),
                ),
              ),
              child: Stack(
                fit: StackFit.expand,
                children: [
                  // 1. Watermark (if no cover)
                  if (course.coverUrl.isEmpty)
                    _buildSubjectWatermark(course.subject),

                  // 2. Cover image (if exists)
                  if (course.coverUrl.isNotEmpty)
                    ClipRRect(
                      borderRadius: const BorderRadius.only(
                        topLeft: Radius.circular(26),
                        topRight: Radius.circular(26),
                      ),
                      child: Image.network(
                        ApiService.absoluteUrl(course.coverUrl),
                        fit: BoxFit.cover,
                        errorBuilder: (context, error, stackTrace) =>
                            _buildSubjectWatermark(course.subject),
                      ),
                    ),

                  // 3. Chip overlay (top-left)
                  Positioned(
                    top: 12,
                    left: 12,
                    child: Container(
                      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
                      decoration: BoxDecoration(
                        color: Colors.white.withOpacity(0.25),
                        borderRadius: BorderRadius.circular(12),
                        border: Border.all(color: Colors.white.withOpacity(0.2), width: 1.0),
                      ),
                      child: Text(
                        cardLabel,
                        style: const TextStyle(color: Colors.white, fontSize: 10, fontWeight: FontWeight.w900),
                      ),
                    ),
                  ),

                  // 4. Play icon overlay (bottom-right)
                  const Positioned(
                    bottom: 12,
                    right: 12,
                    child: Icon(Icons.play_circle_fill_rounded, color: Colors.white70, size: 28),
                  ),
                ],
              ),
            ),
          ),

          // Course info — flex 1:1 with the banner now (was 4, matching the 5
          // above). Tightened padding (12→10) and a couple font sizes so the
          // shorter card doesn't feel cramped.
          Expanded(
            flex: 1,
            child: Padding(
              padding: const EdgeInsets.all(10.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 3),
                    decoration: BoxDecoration(
                      color: const Color(0xFFF1F5F9),
                      borderRadius: BorderRadius.circular(8),
                      border: Border.all(color: const Color(0xFFCBD5E1), width: 1),
                    ),
                    child: Text(
                      _getSubjectName(course.subject),
                      style: const TextStyle(fontSize: 9, color: AppTheme.textMuted, fontWeight: FontWeight.bold),
                    ),
                  ),
                  const SizedBox(height: 6),
                  Text(
                    course.title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 13, color: AppTheme.textWhite),
                  ),
                  const Spacer(),

                  // Entry prompt — replaced by a drip-unlock badge when the
                  // course runs under a schedule, so the student sees the
                  // cadence + next unlock from the grid. Falls back to the
                  // plain "点击进入学习" cue for all-open courses.
                  if (course.hasUnlockSchedule)
                    _buildUnlockBadge(course)
                  else
                    Row(
                      mainAxisAlignment: MainAxisAlignment.spaceBetween,
                      children: [
                        Text(
                          '点击进入学习',
                          style: TextStyle(
                            color: AppTheme.primaryColor,
                            fontWeight: FontWeight.w900,
                            fontSize: 10,
                          ),
                        ),
                        const Icon(Icons.arrow_forward_rounded, color: AppTheme.textMuted, size: 14),
                      ],
                    ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  /// Compact drip-unlock badge for the course card: strategy label + visible
  /// progress, and the next auto-unlock instant when one is scheduled.
  /// Layout is tuned to fit the card's narrow bottom row (one line, ellipsized).
  Widget _buildUnlockBadge(Course course) {
    // nextUnlockAt is RFC3339 (business tz). Show a friendly "周日 03/12 19:00".
    String nextLabel = '';
    if (course.nextUnlockAt.isNotEmpty) {
      try {
        final dt = DateTime.parse(course.nextUnlockAt);
        const weekdays = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];
        // DateTime.weekday is 1=Mon..7=Sun; map to the 0-based array above.
        final wd = weekdays[(dt.weekday - 1) % 7];
        final mm = dt.month.toString().padLeft(2, '0');
        final dd = dt.day.toString().padLeft(2, '0');
        final hh = dt.hour.toString().padLeft(2, '0');
        final mi = dt.minute.toString().padLeft(2, '0');
        nextLabel = '$wd $mm/$dd $hh:$mi';
      } catch (_) {
        nextLabel = '';
      }
    }

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 3),
      decoration: BoxDecoration(
        color: const Color(0xFFFFFBEB),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: const Color(0xFFFDE68A), width: 1),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.lock_clock_outlined, size: 10, color: Color(0xFFD97706)),
              const SizedBox(width: 3),
              Flexible(
                child: Text(
                  '${course.unlockStrategyLabel} · ${course.unlockedCount}/${course.episodeTotal}',
                  style: const TextStyle(
                    fontSize: 9,
                    color: Color(0xFF92400E),
                    fontWeight: FontWeight.bold,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
          if (nextLabel.isNotEmpty) ...[
            const SizedBox(height: 1),
            Text(
              '下次解锁 $nextLabel',
              style: const TextStyle(fontSize: 8, color: Color(0xFFB45309)),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ],
      ),
    );
  }

  String _getSubjectName(String key) {
    final meta = _subjectMeta(key);
    return '${meta.label} ${meta.emoji}'.trim();
  }

  Widget _buildErrorBox(String error) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.error_outline, size: 48, color: Colors.redAccent),
          const SizedBox(height: 16),
          const Text('加载失败，请检查网络或后端配置！', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16)),
          const SizedBox(height: 8),
          Text(error, style: const TextStyle(color: AppTheme.textMuted), textAlign: TextAlign.center),
          const SizedBox(height: 24),
          Button3D.blue(
            onPressed: _loadData,
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
          const Icon(Icons.school_outlined, size: 48, color: AppTheme.textMuted),
          const SizedBox(height: 16),
          const Text('没有找到已授权的课程库', style: TextStyle(fontWeight: FontWeight.bold, fontSize: 18)),
          const SizedBox(height: 8),
          const Text('请让爸爸妈妈在后台给您分配可学习的课程吧！', style: TextStyle(color: AppTheme.textMuted)),
          const SizedBox(height: 24),
          Button3D.blue(
            onPressed: _loadData,
            child: const Text('刷新列表'),
          ),
        ],
      ),
    );
  }
}
