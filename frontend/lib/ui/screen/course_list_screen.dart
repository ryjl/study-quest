import 'dart:math';
import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../model/progress.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/button_3d.dart';
import 'course_detail_screen.dart';

class CourseListScreen extends StatefulWidget {
  final int activeUserId;

  const CourseListScreen({Key? key, required this.activeUserId}) : super(key: key);

  @override
  State<CourseListScreen> createState() => _CourseListScreenState();
}

class _CourseListScreenState extends State<CourseListScreen> {
  late Future<List<Course>> _coursesFuture;
  late Future<List<UserProgress>> _progressFuture;
  
  String _selectedSubject = '全部';
  String _selectedGrade = '全部';
  String _selectedTag = '全部';
  String _searchQuery = '';
  
  final List<String> _subjects = ['全部', '语文', '数学', '英语', '科学', '兴趣', '综合'];
  final List<String> _grades = ['全部', '三年级', '四年级', '五年级', '六年级', '通用'];
  final List<String> _tags = ['全部', '必修', '思维', '拓展', '探索', '课外', '逻辑', '视野'];

  @override
  void initState() {
    super.initState();
    _loadData();
  }

  void _loadData() {
    setState(() {
      _coursesFuture = ApiService.fetchCourses(widget.activeUserId);
      _progressFuture = ApiService.fetchProgressOverview(widget.activeUserId);
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.transparent,
      body: FutureBuilder(
        future: Future.wait([_coursesFuture, _progressFuture]),
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
            final matchGrade = _selectedGrade == '全部' ||
                (c.grade == 'universal' && _selectedGrade == '通用') ||
                '${c.grade}年级' == _selectedGrade;
            final matchTag = _selectedTag == '全部' || c.tagsList.contains(_selectedTag);
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
            padding: const EdgeInsets.symmetric(horizontal: 40.0, vertical: 30.0),
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

                // Search & Subject Row
                Row(
                  children: [
                    // Subject List Scroll View
                    Expanded(
                      child: SingleChildScrollView(
                        scrollDirection: Axis.horizontal,
                        physics: const BouncingScrollPhysics(),
                        child: Row(
                          children: _subjects.map((subj) {
                            final active = _selectedSubject == subj;
                            return Padding(
                              padding: const EdgeInsets.only(right: 12.0),
                              child: active
                                  ? Button3D.dark(
                                      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                                      onPressed: () => setState(() => _selectedSubject = subj),
                                      child: Text(subj, style: const TextStyle(fontSize: 14)),
                                    )
                                  : Button3D.white(
                                      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                                      onPressed: () => setState(() => _selectedSubject = subj),
                                      child: Text(subj, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w900)),
                                    ),
                            );
                          }).toList(),
                        ),
                      ),
                    ),
                    const SizedBox(width: 20),

                    // Search input
                    SizedBox(
                      width: 280,
                      child: TextField(
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
                    ),
                  ],
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
                        items: _grades,
                        onChanged: (val) => setState(() => _selectedGrade = val!),
                      ),
                      Container(width: 2, height: 24, color: const Color(0xFFE2E8F0), margin: const EdgeInsets.symmetric(horizontal: 16)),

                      // Tag Selector
                      _buildDropdown(
                        icon: Icons.tag_rounded,
                        color: Colors.green,
                        value: _selectedTag,
                        items: _tags,
                        onChanged: (val) => setState(() => _selectedTag = val!),
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
                              crossAxisSpacing: 24,
                              mainAxisSpacing: 24,
                              childAspectRatio: 0.72,
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
            return DropdownMenuItem<String>(
              value: item,
              child: Text(item == '全部' ? '所有' : item),
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
    final gradeLabel = course.grade == 'universal' ? '通用' : '${course.grade}年级';
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
          // Dynamic gradient banner header
          Expanded(
            flex: 5,
            child: Container(
              decoration: BoxDecoration(
                gradient: AppTheme.getSubjectGradient(course.subject),
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

          // Course info
          Expanded(
            flex: 4,
            child: Padding(
              padding: const EdgeInsets.all(12.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                    decoration: BoxDecoration(
                      color: const Color(0xFFF1F5F9),
                      borderRadius: BorderRadius.circular(8),
                      border: Border.all(color: const Color(0xFFCBD5E1), width: 1),
                    ),
                    child: Text(
                      _getSubjectName(course.subject),
                      style: const TextStyle(fontSize: 10, color: AppTheme.textMuted, fontWeight: FontWeight.bold),
                    ),
                  ),
                  const SizedBox(height: 8),
                  Text(
                    course.title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 14, color: AppTheme.textWhite),
                  ),
                  const Spacer(),

                  // Entry prompt — real per-course progress is shown on the detail page.
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Text(
                        '点击进入学习',
                        style: TextStyle(
                          color: AppTheme.primaryColor,
                          fontWeight: FontWeight.w900,
                          fontSize: 11,
                        ),
                      ),
                      const Icon(Icons.arrow_forward_rounded, color: AppTheme.textMuted, size: 16),
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

  String _getSubjectName(String key) {
    switch (key.toLowerCase()) {
      case 'chinese':
      case '语文':
        return '语文 📚';
      case 'math':
      case '数学':
        return '数学 📐';
      case 'english':
      case '英语':
        return '英语 🔠';
      case 'physics':
      case '科学':
        return '科学 🧪';
      case 'extra':
      case '百科':
        return '百科 🌎';
      case '兴趣':
        return '兴趣 ♟️';
      case '综合':
        return '综合 🗺️';
      default:
        return key.toUpperCase();
    }
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
