import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import 'course_detail_screen.dart';

class CourseListScreen extends StatefulWidget {
  final int activeUserId;

  const CourseListScreen({Key? key, required this.activeUserId}) : super(key: key);

  @override
  State<CourseListScreen> createState() => _CourseListScreenState();
}

class _CourseListScreenState extends State<CourseListScreen> {
  late Future<List<Course>> _coursesFuture;

  @override
  void initState() {
    super.initState();
    _loadCourses();
  }

  void _loadCourses() {
    setState(() {
      _coursesFuture = ApiService.fetchCourses(widget.activeUserId);
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.transparent,
      body: Padding(
        padding: const EdgeInsets.all(40.0),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Screen header
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                const Text(
                  '学习大厅',
                  style: TextStyle(fontSize: 32, fontWeight: FontWeight.bold),
                ),
                IconButton(
                  icon: const Icon(Icons.refresh, color: AppTheme.textMuted),
                  onPressed: _loadCourses,
                ),
              ],
            ),
            const SizedBox(height: 32),

            // Courses grid loader
            Expanded(
              child: FutureBuilder<List<Course>>(
                future: _coursesFuture,
                builder: (context, snapshot) {
                  if (snapshot.connectionState == ConnectionState.waiting) {
                    return const Center(child: CircularProgressIndicator(color: AppTheme.primaryColor));
                  }
                  if (snapshot.hasError) {
                    return _buildErrorBox(snapshot.error.toString());
                  }
                  final courses = snapshot.data ?? [];
                  if (courses.isEmpty) {
                    return _buildEmptyBox();
                  }
                  return _buildCoursesGrid(courses);
                },
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildCoursesGrid(List<Course> courses) {
    // Dynamic grid columns based on available horizontal viewport width (PAD vs TV Box)
    final width = MediaQuery.of(context).size.width;
    final crossAxisCount = width > 900 ? 3 : 2;

    return GridView.builder(
      itemCount: courses.length,
      gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: crossAxisCount,
        crossAxisSpacing: 24,
        mainAxisSpacing: 24,
        childAspectRatio: 1.1, // Square-ish Switch cards
      ),
      itemBuilder: (context, index) {
        final course = courses[index];
        return FocusButton(
          padding: EdgeInsets.zero, // Cover image goes edge to edge
          onPressed: () {
            Navigator.push(
              context,
              MaterialPageRoute(
                builder: (context) => CourseDetailScreen(
                  activeUserId: widget.activeUserId,
                  course: course,
                ),
              ),
            );
          },
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // Cover Image
              Expanded(
                child: Container(
                  decoration: BoxDecoration(
                    color: Colors.black12,
                    borderRadius: const BorderRadius.only(
                      topLeft: Radius.circular(AppTheme.borderRadiusValue - AppTheme.borderWidthValue),
                      topRight: Radius.circular(AppTheme.borderRadiusValue - AppTheme.borderWidthValue),
                    ),
                    image: course.coverUrl.isNotEmpty
                        ? DecorationImage(
                            image: NetworkImage(course.coverUrl),
                            fit: BoxFit.cover,
                          )
                        : null,
                  ),
                  child: course.coverUrl.isEmpty
                      ? const Center(child: Icon(Icons.school, size: 48, color: AppTheme.textMuted))
                      : null,
                ),
              ),

              // Course metadata
              Padding(
                padding: const EdgeInsets.all(16.0),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      course.title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 18),
                    ),
                    const SizedBox(height: 6),
                    Row(
                      mainAxisAlignment: MainAxisAlignment.spaceBetween,
                      children: [
                        // Grade Badge
                        Container(
                          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                          decoration: BoxDecoration(
                            color: AppTheme.primaryColor.withOpacity(0.2),
                            borderRadius: BorderRadius.circular(6),
                          ),
                          child: Text(
                            course.grade == 'universal' ? '通用' : '${course.grade}年级',
                            style: const TextStyle(color: AppTheme.primaryColor, fontWeight: FontWeight.bold, fontSize: 11),
                          ),
                        ),
                        // Subject
                        Text(
                          _getSubjectName(course.subject),
                          style: const TextStyle(color: AppTheme.textMuted, fontSize: 12),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }

  String _getSubjectName(String key) {
    switch (key.toLowerCase()) {
      case 'chinese':
        return '语文 📚';
      case 'math':
        return '数学 📐';
      case 'english':
        return '英语 🔠';
      case 'physics':
        return '科学 🧪';
      case 'extra':
        return '百科 🌎';
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
          FocusButton(
            onPressed: _loadCourses,
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
          FocusButton(
            onPressed: _loadCourses,
            child: const Text('刷新列表'),
          ),
        ],
      ),
    );
  }
}
