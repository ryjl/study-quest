import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../model/progress.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/glass_panel.dart';
import '../widget/button_3d.dart';
import 'course_detail_screen.dart';
import 'player_screen.dart';

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

          final courses = snapshot.data?[0] as List<Course>;
          final progressList = snapshot.data?[1] as List<UserProgress>;

          // Find the last incomplete episode to continue watching
          Episode? continueEpisode;
          Course? continueCourse;
          if (progressList.isNotEmpty && courses.isNotEmpty) {
            final incomplete = progressList.firstWhere(
              (p) => !p.isCompleted,
              orElse: () => progressList.first,
            );
            // Mock default if none or find match
            continueCourse = courses.first;
            // Let's create a mockup episode to direct user to play
            continueEpisode = Episode(
              id: incomplete.episodeId,
              courseId: continueCourse.id,
              sortOrder: 1,
              title: '第1集：雨来的家乡与夜校读书',
              videoRelativePath: 'course_1_episode_1.mp4',
              attachmentJson: '["讲义.pdf"]',
              fileHash: 'xyz',
              fileSize: 45000000,
              durationSeconds: 725,
            );
          }

          // Apply filters
          final filteredCourses = courses.filter((c) {
            final matchSearch = c.title.toLowerCase().contains(_searchQuery.toLowerCase());
            final matchSubject = _selectedSubject == '全部' || c.subject == _selectedSubject;
            final matchGrade = _selectedGrade == '全部' || c.grade == _selectedGrade;
            // Mock tags if not in model, or default true
            final matchTag = _selectedTag == '全部' || _mockGetTag(c.id) == _selectedTag;
            return matchSearch && matchSubject && matchGrade && matchTag;
          });

          return Padding(
            padding: const EdgeInsets.symmetric(horizontal: 40.0, vertical: 30.0),
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

                    // Continue Learning Button (3D)
                    if (continueEpisode != null && continueCourse != null)
                      Button3D.blue(
                        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 8),
                        onPressed: () {
                          Navigator.push(
                            context,
                            MaterialPageRoute(
                              builder: (context) => PlayerScreen(
                                activeUserId: widget.activeUserId,
                                episode: continueEpisode!,
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
                                  '${continueCourse.title}：第1集',
                                  style: const TextStyle(color: Colors.white, fontSize: 14, fontWeight: FontWeight.w900),
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

                // Grid view of course cards
                Expanded(
                  child: filteredCourses.isEmpty
                      ? _buildEmptyBox()
                      : GridView.builder(
                          physics: const BouncingScrollPhysics(),
                          itemCount: filteredCourses.length,
                          gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                            crossAxisCount: 4,
                            crossAxisSpacing: 24,
                            mainAxisSpacing: 24,
                            childAspectRatio: 0.82,
                          ),
                          itemBuilder: (context, index) {
                            return _buildCourseCard(filteredCourses[index], progressList);
                          },
                        ),
                ),
              ],
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

  Widget _buildCourseCard(Course course, List<UserProgress> progressList) {
    // Get completion percentage
    final isCompleted = course.id % 3 == 0; // Mock: some completed courses
    final progressVal = isCompleted ? 100 : (course.id * 35) % 90;
    final tag = _mockGetTag(course.id);

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
          Container(
            height: 140,
            decoration: BoxDecoration(
              gradient: AppTheme.getSubjectGradient(course.subject),
              borderRadius: const BorderRadius.only(
                topLeft: Radius.circular(26),
                topRight: Radius.circular(26),
              ),
            ),
            child: Stack(
              children: [
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
                      '$tag | ${course.grade == 'universal' ? '通用' : '${course.grade}年级'}',
                      style: const TextStyle(color: Colors.white, fontSize: 10, fontWeight: FontWeight.w900),
                    ),
                  ),
                ),
                if (isCompleted)
                  const Positioned(
                    top: 12,
                    right: 12,
                    child: Icon(Icons.check_circle_rounded, color: Colors.white, size: 24),
                  ),
              ],
            ),
          ),

          // Course info
          Expanded(
            child: Padding(
              padding: const EdgeInsets.all(16.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
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
                    ],
                  ),
                  const SizedBox(height: 10),
                  Text(
                    course.title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 16, color: AppTheme.textWhite),
                  ),
                  const Spacer(),

                  // Progress values
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Text(
                        isCompleted ? '已圆满通关' : '当前进度 $progressVal%',
                        style: TextStyle(
                          color: isCompleted ? AppTheme.accentGreen : AppTheme.primaryColor,
                          fontWeight: FontWeight.w900,
                          fontSize: 11,
                        ),
                      ),
                      const Text(
                        '1/2 讲',
                        style: TextStyle(color: AppTheme.textMuted, fontSize: 11, fontWeight: FontWeight.bold),
                      ),
                    ],
                  ),
                  const SizedBox(height: 8),

                  // Progress bar
                  ClipRRect(
                    borderRadius: BorderRadius.circular(4),
                    child: Container(
                      height: 8,
                      width: double.infinity,
                      color: const Color(0xFFF1F5F9),
                      child: FractionallySizedBox(
                        alignment: Alignment.centerLeft,
                        widthFactor: progressVal / 100.0,
                        child: Container(
                          decoration: BoxDecoration(
                            color: isCompleted ? AppTheme.accentGreen : AppTheme.primaryColor,
                          ),
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  String _mockGetTag(int courseId) {
    final mockTags = ['必修', '思维', '拓展', '探索', '课外', '逻辑', '视野'];
    return mockTags[courseId % mockTags.length];
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

// Helper extension to make filtering easier
extension ListFilter<T> on List<T> {
  List<T> filter(bool Function(T) test) {
    final List<T> result = [];
    for (var element in this) {
      if (test(element)) {
        result.add(element);
      }
    }
    return result;
  }
}
