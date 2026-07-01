import 'package:flutter/material.dart';
import '../../model/course.dart';
import '../../model/progress.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
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

  @override
  void initState() {
    super.initState();
    _refreshData();
  }

  void _refreshData() {
    setState(() {
      _episodesFuture = ApiService.fetchEpisodes(widget.activeUserId, widget.course.id);
      _progressFuture = ApiService.fetchProgressOverview(widget.activeUserId);
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppTheme.backgroundColor,
      appBar: AppBar(
        backgroundColor: Colors.transparent,
        elevation: 0,
        title: Text(widget.course.title, style: const TextStyle(fontWeight: FontWeight.bold)),
      ),
      body: FutureBuilder(
        future: Future.wait([_episodesFuture, _progressFuture]),
        builder: (context, AsyncSnapshot<List<dynamic>> snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator(color: AppTheme.primaryColor));
          }
          if (snapshot.hasError) {
            return _buildErrorBox(snapshot.error.toString());
          }

          final episodes = snapshot.data?[0] as List<Episode>;
          final progressList = snapshot.data?[1] as List<UserProgress>;

          if (episodes.isEmpty) {
            return _buildEmptyBox();
          }

          // Build quick mapping for completion states
          final Map<int, bool> completionMap = {};
          for (var p in progressList) {
            completionMap[p.episodeId] = p.isCompleted;
          }

          return Padding(
            padding: const EdgeInsets.symmetric(horizontal: 40.0, vertical: 20.0),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                // Left Column: Course cover detail
                SizedBox(
                  width: 300,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      // Large cover
                      AspectRatio(
                        aspectRatio: 1.5,
                        child: Container(
                          decoration: BoxDecoration(
                            borderRadius: BorderRadius.circular(AppTheme.borderRadiusValue),
                            border: Border.all(color: AppTheme.borderMuted, width: AppTheme.borderWidthValue),
                            image: widget.course.coverUrl.isNotEmpty
                                ? DecorationImage(
                                    image: NetworkImage(widget.course.coverUrl),
                                    fit: BoxFit.cover,
                                  )
                                : null,
                          ),
                          child: widget.course.coverUrl.isEmpty
                              ? const Icon(Icons.school, size: 64, color: AppTheme.textMuted)
                              : null,
                        ),
                      ),
                      const SizedBox(height: 24),
                      Text(
                        widget.course.title,
                        style: const TextStyle(fontSize: 24, fontWeight: FontWeight.bold),
                      ),
                      const SizedBox(height: 12),
                      Row(
                        children: [
                          Container(
                            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                            decoration: BoxDecoration(
                              color: AppTheme.primaryColor.withOpacity(0.2),
                              borderRadius: BorderRadius.circular(6),
                            ),
                            child: Text(
                              widget.course.grade == 'universal' ? '通用' : '${widget.course.grade}年级',
                              style: const TextStyle(color: AppTheme.primaryColor, fontWeight: FontWeight.bold, fontSize: 11),
                            ),
                          ),
                          const SizedBox(width: 12),
                          Text(widget.course.subject.toUpperCase(), style: const TextStyle(color: AppTheme.textMuted)),
                        ],
                      ),
                    ],
                  ),
                ),
                const SizedBox(width: 48),

                // Right Column: Vertical linear timeline of lessons
                Expanded(
                  child: ListView.builder(
                    itemCount: episodes.length,
                    itemBuilder: (context, index) {
                      final ep = episodes[index];
                      final isCompleted = completionMap[ep.id] ?? false;

                      return Container(
                        margin: const EdgeInsets.only(bottom: 16),
                        child: FocusButton(
                          onPressed: () {
                            Navigator.push(
                              context,
                              MaterialPageRoute(
                                builder: (context) => PlayerScreen(
                                  activeUserId: widget.activeUserId,
                                  episode: ep,
                                ),
                              ),
                            ).then((_) => _refreshData()); // refresh progress status when user returns
                          },
                          child: Row(
                            children: [
                              // Episode number badge
                              Container(
                                width: 48,
                                height: 48,
                                alignment: Alignment.center,
                                decoration: BoxDecoration(
                                  color: isCompleted ? AppTheme.accentGreen : AppTheme.cardColor,
                                  shape: BoxShape.circle,
                                  border: Border.all(
                                    color: isCompleted ? AppTheme.accentGreen : AppTheme.borderMuted,
                                    width: 2,
                                  ),
                                ),
                                child: isCompleted
                                    ? const Icon(Icons.check, color: Colors.white)
                                    : Text(
                                        'P${ep.sortOrder}',
                                        style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
                                      ),
                              ),
                              const SizedBox(width: 24),

                              // Episode Title & file details
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(
                                      ep.title,
                                      style: const TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
                                    ),
                                    const SizedBox(height: 6),
                                    Text(
                                      '课时路径: ${ep.videoRelativePath}',
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      style: const TextStyle(color: AppTheme.textMuted, fontSize: 12),
                                    ),
                                  ],
                                ),
                              ),

                              // Play Button Icon indication
                              Icon(
                                isCompleted ? Icons.check_circle : Icons.play_arrow_rounded,
                                color: isCompleted ? AppTheme.accentGreen : AppTheme.primaryColor,
                                size: 32,
                              ),
                            ],
                          ),
                        ),
                      );
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
          FocusButton(
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
          FocusButton(
            onPressed: _refreshData,
            child: const Text('刷新页面'),
          ),
        ],
      ),
    );
  }
}
