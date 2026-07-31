import 'package:flutter/material.dart';
import 'package:study_quest/model/course.dart';
import 'package:study_quest/service/api_service.dart';
import 'package:study_quest/ui/screen/course_detail_screen.dart';
import '../../theme.dart';
import '../widget/focus_button.dart';
import '../widget/state_widgets.dart';
import '../widget/tv_focus.dart';

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
  // 焦点陷阱修复(对齐 reading_room):搜索框 TextField 默认吞方向键,D-pad 进了出不来。
  // dpadEscapeFocusNode 截断方向键转 nextFocus/previousFocus,字母数字放行。
  late final FocusNode _searchFocusNode = dpadEscapeFocusNode();

  @override
  void initState() {
    super.initState();
    _loadData();
  }

  @override
  void dispose() {
    _searchFocusNode.dispose();
    super.dispose();
  }

  void _loadData() {
    _coursesFuture = ApiService.fetchCourses(widget.activeUserId,
        contentType: 'entertainment');
    // 必须触发重建:FutureBuilder 靠拿到新的 future 引用才重新订阅。缺这一行会导致
    // 下拉刷新和从详情页返回 .then((_)=>_loadData()) 时数据不更新(对照 reading_room)。
    setState(() {});
  }

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Scaffold(
      // 透明底让 MainNavigation 的页面底色(backgroundColor)透出,深色模式下不再
      // 是硬编码浅紫一片。原 Color(0xFFF8F4FF) 是亮色专属,深色下刺眼且与其余屏脱节。
      backgroundColor: Colors.transparent,
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
                  Icon(Icons.movie_rounded,
                      color: colors.violet500, size: 32),
                  const SizedBox(width: 12),
                  Text(
                    '娱乐',
                    style: TextStyle(
                        fontSize: 24,
                        fontWeight: FontWeight.bold,
                        color: colors.textWhite),
                  ),
                  const Spacer(),
                  SizedBox(
                    width: 200,
                    child: TextField(
                      focusNode: _searchFocusNode,
                      onChanged: (v) => setState(() => _searchQuery = v),
                      style: TextStyle(
                          fontSize: 14,
                          fontWeight: FontWeight.bold,
                          color: colors.textWhite),
                      decoration: InputDecoration(
                        hintText: '搜索...',
                        hintStyle: TextStyle(color: colors.textMuted),
                        prefixIcon:
                            Icon(Icons.search, size: 20, color: colors.textMuted),
                        isDense: true,
                        contentPadding: const EdgeInsets.symmetric(
                            horizontal: 12, vertical: 8),
                        border: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(12),
                          borderSide: BorderSide(color: colors.borderMuted, width: 1.5),
                        ),
                        focusedBorder: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(12),
                          borderSide: BorderSide(color: colors.primaryColor, width: 2),
                        ),
                        enabledBorder: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(12),
                          borderSide: BorderSide(color: colors.borderMuted, width: 1.5),
                        ),
                        filled: true,
                        // 用主题卡片色(亮=白 / 暗=slate800)而非硬编码白:深色模式下白底
                        // 与默认 TextField 浅色文字撞色看不见,取 context.colors.cardColor
                        // 跟随主题。
                        fillColor: colors.cardColor,
                      ),
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
                      return loadingSpinner(context);
                    }
                    if (snapshot.hasError) {
                      return errorStateBox(
                          context, snapshot.error.toString(), _loadData,
                          message: '加载失败，请检查网络！');
                    }

                    final all = snapshot.data ?? const <Course>[];
                    final filtered = all
                        .where((c) =>
                            _searchQuery.isEmpty ||
                            c.title
                                .toLowerCase()
                                .contains(_searchQuery.toLowerCase()))
                        .toList();

                    if (filtered.isEmpty) {
                      return emptyStateBox(
                        context: context,
                        icon: Icons.movie_filter_outlined,
                        headline: _searchQuery.isEmpty ? '还没有娱乐视频' : '没有匹配的视频',
                        hint: _searchQuery.isEmpty
                            ? '请让爸爸妈妈在后台添加娱乐内容吧！'
                            : '换个关键词试试',
                        refreshLabel: '刷新',
                        onRefresh: _loadData,
                      );
                    }

                    return GridView.builder(
                      padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
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
                        // 用 FocusButton 包卡片(对齐 course_list / reading_room):
                        // TV D-pad 才能聚焦选中;裸 GestureDetector 在 TV 上无法点入。
                        return FocusButton(
                          padding: EdgeInsets.zero,
                          borderRadius: 20,
                          borderColor: colors.borderMuted,
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
    final colors = context.colors;
    return Container(
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(20),
        // 卡片底接入主题(原硬编码 Colors.white,深色下白卡与浅字撞色)。
        color: colors.cardColor,
        border: Border.all(color: colors.borderMuted),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withValues(alpha: 0.06),
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
                      errorBuilder: (_, __, ___) => _gradientBanner(colors),
                    )
                  else
                    _gradientBanner(colors),
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
                      style: TextStyle(
                        fontSize: 14,
                        fontWeight: FontWeight.w600,
                        color: colors.textWhite,
                      ),
                    ),
                    const Spacer(),
                    Row(
                      children: [
                        Icon(Icons.play_circle_outline,
                            size: 14, color: colors.textMuted),
                        const SizedBox(width: 4),
                        Text(
                          '${course.tagsList.isEmpty ? "" : course.tagsList.first}',
                          style: TextStyle(
                            fontSize: 11,
                            color: colors.textMuted,
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

  Widget _gradientBanner(AppColors colors) {
    return Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          // 品牌渐变(两端一致,作娱乐 tab 的视觉标识),保留;改用 token 取色保持同步。
          colors: [colors.violet500, colors.indigo500],
        ),
      ),
      child: Center(
        child: Icon(Icons.movie_rounded,
            color: Colors.white.withValues(alpha: 0.4), size: 48),
      ),
    );
  }
}
