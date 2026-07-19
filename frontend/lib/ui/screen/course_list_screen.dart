import 'dart:math';
import 'package:flutter/material.dart';
// services.dart 暴露 LogicalKeyboardKey / KeyDownEvent / KeyEventResult /
// FocusManager,用于修复 Android TV 上搜索框的 D-pad 焦点陷阱(详见 build 中
// 搜索区 Focus widget 的 onKeyEvent)。
import 'package:flutter/services.dart';
import '../../model/course.dart';
import '../../model/progress.dart';
import '../../model/subject.dart';
import '../../model/tag.dart';
import '../../service/api_service.dart';
import '../../service/tv_mode.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/button_3d.dart';
import '../widget/state_widgets.dart';
import '../widget/subject_icon.dart';
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

  // 搜索框专属 FocusNode。关键:这个节点是 TextField 自己的焦点节点,它的
  // onKeyEvent 会在 EditableText 的默认按键处理(光标移动/多行导航)**之前**
  // 运行。把 D-pad 方向键在 onKeyEvent 里截掉并返回 handled,EditableText 就
  // 收不到方向键,无法把光标移动消费掉 —— 焦点就能 nextFocus/previousFocus
  // 跳出搜索框。用外层 Focus widget 包裹是拦不住的(TextField 自己消费在前)。
  late final FocusNode _searchFocusNode = FocusNode(
    onKeyEvent: (node, event) {
      if (event is! KeyDownEvent) {
        return KeyEventResult.ignored;
      }
      final k = event.logicalKey;
      final next = k == LogicalKeyboardKey.arrowDown ||
          k == LogicalKeyboardKey.arrowRight;
      final prev = k == LogicalKeyboardKey.arrowUp ||
          k == LogicalKeyboardKey.arrowLeft;
      if (!next && !prev) {
        // 字母/数字/回车/退格等一律放行给 TextField 输入。
        return KeyEventResult.ignored;
      }
      next ? node.nextFocus() : node.previousFocus();
      return KeyEventResult.handled;
    },
  );

  @override
  void dispose() {
    _searchFocusNode.dispose();
    super.dispose();
  }

  // Subject catalog fetched from /api/v1/subjects. Drives the filter chips and
  // the key→label/color lookups on each card. Empty until loaded; the
  // fallback in resolveSubject() keeps the UI working pre-load / on fetch failure.
  List<Subject> _subjectsCatalog = const [];

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
            return loadingSpinner();
          }
          if (snapshot.hasError) {
            return errorStateBox(snapshot.error.toString(), _loadData,
                message: '加载失败，请检查网络或后端配置！');
          }

          final courses = snapshot.data![0] as List<Course>;
          final progressList = snapshot.data![1] as List<UserProgress>;
          // TV 大字模式开关:只在字号上生效,不改布局结构。各处 Text 用三元放大。
          final tv = TvMode.instance.isActive;
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
            // 整页内容包一层 FocusTraversalGroup:让搜索框、筛选按钮、课程卡片
            // 共享同一个遍历顺序。搜索框的 onKeyEvent 调 nextFocus() 时,能沿
            // 视觉顺序自然跳到下方第一个课程卡片(D-pad 下)或旁边筛选按钮(右),
            // 而不会被限制在某个子分组里。
            child: FocusTraversalGroup(
              child: SingleChildScrollView(
                // Wrap the whole layout in a vertical scroller. Without this the
                // fixed-height Column overflows ("bottom overflowed by N pixels")
                // whenever the course grid + filters exceed the viewport (e.g.
                // many subjects/tags, large font, smaller window).
                child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                // Header Area
                // Header Area
                LayoutBuilder(
                  builder: (context, constraints) {
                    final isNarrow = constraints.maxWidth < 600;
                    final headerContent = Column(
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
                    );

                    final continueButton = continueCourse == null
                        ? const SizedBox.shrink()
                        : Button3D.blue(
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
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                const Icon(Icons.play_circle_fill_rounded, color: Colors.white, size: 36),
                                const SizedBox(width: 12),
                                ConstrainedBox(
                                  constraints: BoxConstraints(
                                    maxWidth: isNarrow ? constraints.maxWidth - 100 : 160,
                                  ),
                                  child: Column(
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
                                ),
                              ],
                            ),
                          );

                    if (isNarrow) {
                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          headerContent,
                          const SizedBox(height: 16),
                          SizedBox(
                            width: double.infinity,
                            child: continueButton,
                          ),
                        ],
                      );
                    }

                    return Row(
                      mainAxisAlignment: MainAxisAlignment.spaceBetween,
                      children: [
                        Expanded(child: headerContent),
                        const SizedBox(width: 16),
                        continueButton,
                      ],
                    );
                  },
                ),
                const SizedBox(height: 24),

                // Mainstream App Layout: Top Search + Filter Button, and Text Tab Subjects.
                //
                // 【Android TV 焦点陷阱修复】
                // 原来的搜索 TextField 是裸的 —— 它一旦获得焦点,EditableText 就会
                // 消费 D-pad 上下(用于光标移动/多行导航),导致方向键永远传不到焦点
                // 遍历系统,用户被困在搜索框里出不来。旁边的筛选按钮还是
                // GestureDetector(不可聚焦),默认遍历也没有可跳转的目标。
                //
                // 方案(双层):
                //   1. 整页内容用 FocusTraversalGroup 包住(见外层 Padding),让
                //      nextFocus/previousFocus 沿视觉顺序遍历到筛选按钮或下方网格;
                //   2. 给 TextField 一个专属 FocusNode(_searchFocusNode),在它的
                //      onKeyEvent 里拦截四个方向键,返回 handled 并手动 nextFocus/
                //      previousFocus 弹出。关键点:这个 onKeyEvent 跑在 EditableText
                //      的默认按键处理之前 —— 必须用「TextField 自己的 FocusNode」
                //      才拦得住,用外层 Focus widget 包裹是没用的(TextField 自己
                //      消费在前,事件不会冒泡到祖先)。
                //   字母/数字/回车/退格等一律放行,留给 TextField 正常输入。
                Row(
                  children: [
                    Expanded(
                      child: Container(
                        height: tv ? 56 : 48,
                          decoration: BoxDecoration(
                            color: Colors.white,
                            borderRadius: BorderRadius.circular(24),
                            boxShadow: [
                              BoxShadow(
                                color: Colors.black.withOpacity(0.03),
                                blurRadius: 10,
                                offset: const Offset(0, 2),
                              )
                            ],
                          ),
                          // 焦点陷阱修复见 _searchFocusNode 的 onKeyEvent(它在
                          // EditableText 默认处理之前截断方向键)。这里只把节点接上。
                          child: TextField(
                            focusNode: _searchFocusNode,
                            onChanged: (val) => setState(() => _searchQuery = val),
                            style: TextStyle(
                              fontSize: tv ? 18 : 14,
                              fontWeight: FontWeight.bold,
                              color: AppTheme.textWhite,
                            ),
                            decoration: InputDecoration(
                              hintText: '搜索课程名称...',
                              hintStyle: TextStyle(
                                color: AppTheme.textMuted,
                                fontSize: tv ? 18 : 14,
                              ),
                              prefixIcon: Icon(Icons.search_rounded,
                                  color: AppTheme.textMuted, size: tv ? 24 : 20),
                              border: InputBorder.none,
                              contentPadding: const EdgeInsets.symmetric(
                                  horizontal: 16, vertical: 14),
                            ),
                          ),
                        ),
                      ),
                      const SizedBox(width: 12),
                      // 筛选按钮改成可聚焦的 FocusButton:原本是 GestureDetector(D-pad
                      // 永远跳不过来),现在成为合法焦点目标,能从搜索框 D-pad 右跳到这。
                      FocusButton(
                        onPressed: _showFilterBottomSheet,
                        borderRadius: 24,
                        baseColor: Colors.white,
                        borderColor: const Color(0xFFE2E8F0),
                        padding: EdgeInsets.zero,
                        child: SizedBox(
                          height: tv ? 56 : 48,
                          width: tv ? 56 : 48,
                          child: Stack(
                            alignment: Alignment.center,
                            children: [
                              Icon(Icons.tune_rounded,
                                  color: AppTheme.textMuted, size: tv ? 26 : 24),
                              if (_selectedGrade != '全部' ||
                                  _selectedTagIDs.isNotEmpty)
                                Positioned(
                                  top: 12,
                                  right: 12,
                                  child: Container(
                                    width: 8,
                                    height: 8,
                                    decoration: const BoxDecoration(
                                      color: Colors.redAccent,
                                      shape: BoxShape.circle,
                                    ),
                                  ),
                                ),
                            ],
                          ),
                        ),
                      ),
                    ],
                  ),
                const SizedBox(height: 24),

                // Subject Tabs (Sleek text-based)
                SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  physics: const BouncingScrollPhysics(),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: _subjectFilters.map((subj) {
                      final active = _selectedSubject == subj;
                      final label = subj == '全部'
                          ? '推荐'
                          : resolveSubject(subj, _subjectsCatalog).label;
                      return GestureDetector(
                        onTap: () => setState(() => _selectedSubject = subj),
                        child: Container(
                          padding: const EdgeInsets.only(right: 24.0, bottom: 8.0),
                          child: Column(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              Text(
                                label,
                                style: TextStyle(
                                  fontSize: active ? 18 : 16,
                                  fontWeight: active ? FontWeight.w900 : FontWeight.bold,
                                  color: active ? AppTheme.textWhite : AppTheme.textMuted.withOpacity(0.6),
                                ),
                              ),
                              const SizedBox(height: 6),
                              if (active)
                                Container(
                                  width: 20,
                                  height: 4,
                                  decoration: BoxDecoration(
                                    color: AppTheme.primaryColor,
                                    borderRadius: BorderRadius.circular(2),
                                  ),
                                )
                              else
                                const SizedBox(height: 4),
                            ],
                          ),
                        ),
                      );
                    }).toList(),
                  ),
                ),
                
                if (_selectedGrade != '全部' || _selectedTagIDs.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 16.0),
                    child: Row(
                      children: [
                        const Icon(Icons.filter_alt_outlined, size: 14, color: AppTheme.primaryColor),
                        const SizedBox(width: 4),
                        Text(
                          '已应用高级筛选 · 找到 ${filteredCourses.length} 门',
                          style: const TextStyle(fontSize: 12, color: AppTheme.primaryColor, fontWeight: FontWeight.bold),
                        ),
                      ],
                    ),
                  ),
                const SizedBox(height: 24),

                // Grid view of course cards. shrinkWrap so it sizes to its
                // content inside the outer SingleChildScrollView (no Expanded
                // allowed in an unbounded-height scroll view).
                filteredCourses.isEmpty
                    ? emptyStateBox(
                        icon: Icons.school_outlined,
                        headline: '没有找到已授权的课程库',
                        hint: '请让爸爸妈妈在后台给您分配可学习的课程吧！',
                        refreshLabel: '刷新列表',
                        onRefresh: _loadData,
                      )
                    : LayoutBuilder(
                        builder: (context, constraints) {
                          final width = constraints.maxWidth;
                          // Include a single-column fallback for very narrow
                          // portrait widths (handset) so cards aren't crushed.
                          final crossAxisCount = width > 1200
                              ? 4
                              : (width > 800
                                  ? 3
                                  : (width > 400 ? 2 : 1));
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
    return Positioned(
      right: -24,
      bottom: -24,
      child: Transform.rotate(
        angle: -15 * pi / 180,
        child: Icon(
          subjectIconData(subject),
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
    // TV 大字模式:卡片各处字号放大,提升远距离可读性。
    final tv = TvMode.instance.isActive;

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
                gradient: AppTheme.getSubjectGradientFromColor(resolveSubject(course.subject, _subjectsCatalog).color),
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
                        style: TextStyle(
                          color: Colors.white,
                          fontSize: tv ? 13 : 10,
                          fontWeight: FontWeight.w900,
                        ),
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
                      style: TextStyle(
                        fontSize: tv ? 12 : 9,
                        color: AppTheme.textMuted,
                        fontWeight: FontWeight.bold,
                      ),
                    ),
                  ),
                  const SizedBox(height: 6),
                  Text(
                    course.title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontWeight: FontWeight.w900,
                      fontSize: tv ? 17 : 13,
                      color: AppTheme.textWhite,
                    ),
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
                            fontSize: tv ? 13 : 10,
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
    // Subject emoji was removed from the model; the chip is label-only now.
    // (The watermark icon elsewhere renders the subject's Material icon.)
    return resolveSubject(key, _subjectsCatalog).label;
  }

  void _showFilterBottomSheet() {
    showModalBottomSheet(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (context) {
        return StatefulBuilder(
          builder: (context, setModalState) {
            return Container(
              decoration: const BoxDecoration(
                color: Colors.white,
                borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
              ),
              padding: const EdgeInsets.all(24.0),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      const Text('高级筛选', style: TextStyle(fontSize: 18, fontWeight: FontWeight.w900, color: AppTheme.textWhite)),
                      IconButton(
                        icon: const Icon(Icons.close_rounded, color: AppTheme.textMuted),
                        onPressed: () => Navigator.pop(context),
                      ),
                    ],
                  ),
                  const SizedBox(height: 24),
                  
                  const Text('适合年级', style: TextStyle(fontSize: 14, fontWeight: FontWeight.bold, color: AppTheme.textMuted)),
                  const SizedBox(height: 12),
                  Wrap(
                    spacing: 10,
                    runSpacing: 10,
                    children: _gradeFilterKeys.map((grade) {
                      final active = _selectedGrade == grade;
                      return ChoiceChip(
                        label: Text(
                          grade == '全部' ? '所有年级' : gradeLabelOf(grade),
                          style: TextStyle(
                            fontSize: 13,
                            fontWeight: FontWeight.bold,
                            color: active ? Colors.white : AppTheme.textMuted,
                          )
                        ),
                        selected: active,
                        selectedColor: AppTheme.primaryColor,
                        backgroundColor: const Color(0xFFF1F5F9),
                        showCheckmark: false,
                        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12), side: BorderSide.none),
                        onSelected: (val) {
                          setModalState(() => _selectedGrade = grade);
                          setState(() => _selectedGrade = grade);
                        },
                      );
                    }).toList(),
                  ),
                  
                  if (_tagsCatalog.isNotEmpty) ...[
                    const SizedBox(height: 32),
                    const Text('课程标签', style: TextStyle(fontSize: 14, fontWeight: FontWeight.bold, color: AppTheme.textMuted)),
                    const SizedBox(height: 12),
                    Wrap(
                      spacing: 10,
                      runSpacing: 10,
                      children: _tagsCatalog.map((t) {
                        final active = _selectedTagIDs.contains(t.id);
                        return FilterChip(
                          label: Text(
                            t.label,
                            style: TextStyle(
                              fontSize: 13,
                              fontWeight: FontWeight.bold,
                              color: active ? const Color(0xFF047857) : AppTheme.textMuted,
                            )
                          ),
                          selected: active,
                          selectedColor: const Color(0xFFD1FAE5),
                          backgroundColor: const Color(0xFFF1F5F9),
                          showCheckmark: false,
                          shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(12), 
                            side: active ? const BorderSide(color: Color(0xFF34D399), width: 1.5) : BorderSide.none
                          ),
                          onSelected: (val) {
                            setModalState(() {
                              if (val) _selectedTagIDs.add(t.id);
                              else _selectedTagIDs.remove(t.id);
                            });
                            setState(() {}); // update main screen
                          },
                        );
                      }).toList(),
                    ),
                  ],
                  
                  const SizedBox(height: 40),
                  SizedBox(
                    width: double.infinity,
                    height: 52,
                    child: ElevatedButton(
                      style: ElevatedButton.styleFrom(
                        backgroundColor: AppTheme.primaryColor,
                        elevation: 0,
                        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
                      ),
                      onPressed: () => Navigator.pop(context),
                      child: const Text('查看结果', style: TextStyle(fontSize: 16, fontWeight: FontWeight.bold, color: Colors.white)),
                    ),
                  ),
                  const SizedBox(height: 16),
                ],
              ),
            );
          }
        );
      }
    );
  }

}
