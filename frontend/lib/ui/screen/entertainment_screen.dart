import 'package:flutter/material.dart';
import 'package:study_quest/model/course.dart';
import 'package:study_quest/service/api_service.dart';
import 'package:study_quest/ui/screen/course_detail_screen.dart';

/// EntertainmentScreen shows fun videos (content_type=entertainment). It's a
/// simplified CourseListScreen — no subject/grade filters, no "continue
/// learning" button, no points/badges. Tapping a card opens the same
/// CourseDetailScreen + player as learning courses; the backend tracks progress
/// in a separate table so learning stats stay uncontaminated.
class EntertainmentScreen extends StatefulWidget {
  final int activeUserId;

  const EntertainmentScreen({super.key, required this.activeUserId});

  @override
  State<EntertainmentScreen> createState() => _EntertainmentScreenState();
}

class _EntertainmentScreenState extends State<EntertainmentScreen> {
  late Future<List<Course>> _coursesFuture;
  String _searchQuery = '';

  @override
  void initState() {
    super.initState();
    _loadData();
  }

  void _loadData() {
    _coursesFuture = ApiService.fetchCourses(widget.activeUserId,
        contentType: 'entertainment');
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: const Color(0xFFF8F4FF),
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Header
            Padding(
              padding:
                  const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
              child: Row(
                children: [
                  const Icon(Icons.movie_rounded,
                      color: Color(0xFF8B5CF6), size: 32),
                  const SizedBox(width: 12),
                  const Text(
                    '娱乐',
                    style: TextStyle(
                        fontSize: 24, fontWeight: FontWeight.bold),
                  ),
                  const Spacer(),
                  SizedBox(
                    width: 200,
                    child: TextField(
                      decoration: InputDecoration(
                        hintText: '搜索...',
                        prefixIcon: const Icon(Icons.search, size: 20),
                        isDense: true,
                        contentPadding: const EdgeInsets.symmetric(
                            horizontal: 12, vertical: 8),
                        border: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(12),
                          borderSide: BorderSide.none,
                        ),
                        filled: true,
                        fillColor: Colors.white,
                      ),
                      onChanged: (v) =>
                          setState(() => _searchQuery = v),
                    ),
                  ),
                ],
              ),
            ),

            // Course grid
            Expanded(
              child: RefreshIndicator(
                onRefresh: () async => _loadData(),
                child: FutureBuilder<List<Course>>(
                  future: _coursesFuture,
                  builder: (context, snapshot) {
                    if (snapshot.connectionState ==
                        ConnectionState.waiting) {
                      return const Center(
                          child: CircularProgressIndicator());
                    }
                    if (snapshot.hasError) {
                      return Center(
                          child: Text('加载失败: ${snapshot.error}'));
                    }

                    final courses = snapshot.data ?? [];
                    final filtered = courses.where((c) {
                      return c.title
                          .toLowerCase()
                          .contains(_searchQuery.toLowerCase());
                    }).toList();

                    if (filtered.isEmpty) {
                      return ListView(children: [
                        const SizedBox(height: 120),
                        Center(
                          child: Column(
                            children: [
                              const Icon(Icons.movie_filter_outlined,
                                  size: 64, color: Colors.grey),
                              const SizedBox(height: 16),
                              Text(
                                courses.isEmpty
                                    ? '还没有娱乐视频'
                                    : '没有匹配的视频',
                                style: const TextStyle(
                                    color: Colors.grey, fontSize: 16),
                              ),
                            ],
                          ),
                        ),
                      ]);
                    }

                    return GridView.builder(
                      padding: const EdgeInsets.all(16),
                      gridDelegate:
                          const SliverGridDelegateWithMaxCrossAxisExtent(
                        maxCrossAxisExtent: 280,
                        childAspectRatio: 0.75,
                        crossAxisSpacing: 16,
                        mainAxisSpacing: 16,
                      ),
                      itemCount: filtered.length,
                      itemBuilder: (context, index) {
                        final course = filtered[index];
                        return GestureDetector(
                          onTap: () {
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
                          child: _EntertainmentCard(course: course),
                        );
                      },
                    );
                  },
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// A simple card for entertainment courses — purple gradient banner, title,
/// no subject/grade badges (entertainment has no subject concept).
class _EntertainmentCard extends StatelessWidget {
  final Course course;

  const _EntertainmentCard({required this.course});

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(20),
        color: Colors.white,
        boxShadow: [
          BoxShadow(
            color: Colors.black.withOpacity(0.06),
            blurRadius: 8,
            offset: const Offset(0, 2),
          ),
        ],
      ),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(20),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Expanded(
              flex: 3,
              child: Stack(
                fit: StackFit.expand,
                children: [
                  // Cover image or gradient fallback
                  if (course.coverUrl.isNotEmpty)
                    Image.network(
                      course.coverUrl,
                      fit: BoxFit.cover,
                      errorBuilder: (_, __, ___) => _gradientBanner(),
                    )
                  else
                    _gradientBanner(),
                  // Play icon overlay
                  const Positioned(
                    bottom: 8,
                    right: 8,
                    child: CircleAvatar(
                      radius: 18,
                      backgroundColor: Colors.black54,
                      child: Icon(Icons.play_arrow_rounded,
                          color: Colors.white, size: 20),
                    ),
                  ),
                ],
              ),
            ),
            // Title
            Expanded(
              flex: 2,
              child: Padding(
                padding: const EdgeInsets.all(10),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      course.title,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        fontSize: 14,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const Spacer(),
                    Row(
                      children: [
                        Icon(Icons.play_circle_outline,
                            size: 14, color: Colors.grey[400]),
                        const SizedBox(width: 4),
                        Text(
                          '${course.tagsList.isEmpty ? "" : course.tagsList.first}',
                          style: TextStyle(
                            fontSize: 11,
                            color: Colors.grey[500],
                          ),
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
    );
  }

  Widget _gradientBanner() {
    return Container(
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [Color(0xFF8B5CF6), Color(0xFF6366F1)],
        ),
      ),
      child: const Center(
        child: Icon(Icons.movie_rounded, color: Colors.white38, size: 48),
      ),
    );
  }
}
