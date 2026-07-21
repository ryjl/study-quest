import 'package:flutter/material.dart';
import '../../model/reading.dart';
import '../../model/subject.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/bookshelf.dart';
import '../widget/state_widgets.dart';
import '../widget/subject_icon.dart';
import 'pdf_reader_screen.dart';
import 'article_reader_screen.dart';

/// Series detail — shows the series header + its child books and articles
/// on a bookshelf layout. Mirrors CourseDetailScreen's hero + content structure.
class ReadingSeriesDetailScreen extends StatefulWidget {
  final int activeUserId;
  final ReadingSeriesCard series;

  const ReadingSeriesDetailScreen({
    Key? key,
    required this.activeUserId,
    required this.series,
  }) : super(key: key);

  @override
  State<ReadingSeriesDetailScreen> createState() =>
      _ReadingSeriesDetailScreenState();
}

class _ReadingSeriesDetailScreenState extends State<ReadingSeriesDetailScreen> {
  late Future<ReadingSeriesDetail> _detailFuture;
  List<Subject> _subjectsCatalog = const [];

  @override
  void initState() {
    super.initState();
    _loadData();
  }

  void _loadData() {
    _detailFuture =
        ApiService.fetchReadingSeries(widget.activeUserId, widget.series.id);
    setState(() {});
    ApiService.fetchSubjects(widget.activeUserId).then((list) {
      if (mounted) setState(() => _subjectsCatalog = list);
    });
  }

  @override
  Widget build(BuildContext context) {
    final series = widget.series;
    final subj = resolveSubject(series.subject, _subjectsCatalog);
    final gradient = AppTheme.getSubjectGradientFromColor(subj.color);

    return Scaffold(
      backgroundColor: AppTheme.backgroundColor,
      body: FutureBuilder<ReadingSeriesDetail>(
        future: _detailFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return loadingSpinner();
          }
          if (snapshot.hasError) {
            return errorStateBox(snapshot.error.toString(), _loadData,
                message: '加载系列失败！');
          }

          final detail = snapshot.data!;
          final books = detail.books;
          final articles = detail.articles;

          return CustomScrollView(
            slivers: [
              // Hero header
              SliverAppBar(
                expandedHeight: 220,
                pinned: true,
                backgroundColor: AppTheme.primaryColor,
                flexibleSpace: FlexibleSpaceBar(
                  title: Text(
                    series.title,
                    style: const TextStyle(
                      fontWeight: FontWeight.w900,
                      color: Colors.white,
                    ),
                  ),
                  background: series.coverUrl.isNotEmpty
                      ? Image.network(
                          ApiService.absoluteUrl(series.coverUrl),
                          fit: BoxFit.cover,
                          errorBuilder: (_, __, ___) =>
                              _gradientHeader(gradient, series.subject, subj.color),
                        )
                      : _gradientHeader(gradient, series.subject, subj.color),
                ),
                leading: IconButton(
                  icon: const Icon(Icons.arrow_back_rounded, color: Colors.white),
                  onPressed: () => Navigator.pop(context),
                ),
              ),

              SliverToBoxAdapter(
                child: Padding(
                  padding: const EdgeInsets.all(20),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      if (series.description.isNotEmpty) ...[
                        Text(
                          series.description,
                          style: const TextStyle(
                            fontSize: 15,
                            color: AppTheme.textMuted,
                            height: 1.6,
                          ),
                        ),
                        const SizedBox(height: 24),
                      ],

                      // Books section
                      if (books.isNotEmpty) ...[
                        _sectionTitle('📕 书籍 (${books.length})'),
                        const SizedBox(height: 12),
                        _buildShelfBoard(
                          children: books.map((b) {
                            final bSubj = resolveSubject(b.subject, _subjectsCatalog);
                            return _buildCoverTile(
                              coverUrl: b.coverUrl,
                              subjectKey: b.subject,
                              subjectColor: bSubj.color,
                              gradient: AppTheme.getSubjectGradientFromColor(bSubj.color),
                              badgeIcon: Icons.picture_as_pdf_rounded,
                              title: b.title,
                              subtitle: b.pageCount != null ? '${b.pageCount} 页' : 'PDF',
                              onTap: () => Navigator.push(
                                context,
                                MaterialPageRoute(
                                  builder: (context) => PdfReaderScreen(
                                    activeUserId: widget.activeUserId,
                                    book: b,
                                  ),
                                ),
                              ).then((_) => _loadData()),
                            );
                          }).toList(),
                        ),
                        const SizedBox(height: 28),
                      ],

                      // Articles section
                      if (articles.isNotEmpty) ...[
                        _sectionTitle('🌐 文章 (${articles.length})'),
                        const SizedBox(height: 12),
                        _buildShelfBoard(
                          children: articles.map((a) {
                            final aSubj = resolveSubject(a.subject, _subjectsCatalog);
                            return _buildCoverTile(
                              coverUrl: a.coverUrl,
                              subjectKey: a.subject,
                              subjectColor: aSubj.color,
                              gradient: AppTheme.getSubjectGradientFromColor(aSubj.color),
                              badgeIcon: Icons.language_rounded,
                              title: a.title,
                              subtitle: '网页',
                              onTap: () => Navigator.push(
                                context,
                                MaterialPageRoute(
                                  builder: (context) => ArticleReaderScreen(
                                    activeUserId: widget.activeUserId,
                                    article: a,
                                  ),
                                ),
                              ).then((_) => _loadData()),
                            );
                          }).toList(),
                        ),
                      ],

                      if (books.isEmpty && articles.isEmpty)
                        const Padding(
                          padding: EdgeInsets.only(top: 40),
                          child: Center(
                            child: Text(
                              '该系列还没有内容',
                              style: TextStyle(color: AppTheme.textMuted, fontSize: 16),
                            ),
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
    );
  }

  Widget _gradientHeader(Gradient gradient, String subjectKey, String subjectColor) {
    return Container(
      decoration: BoxDecoration(gradient: gradient),
      child: Center(
        child: Icon(
          subjectIconData(subjectKey),
          size: 64,
          color: AppTheme.colorFromHex(subjectColor),
        ),
      ),
    );
  }

  Widget _sectionTitle(String text) => sectionTitle(text);

  Widget _buildShelfBoard({required List<Widget> children}) => buildShelfBoard(children: children);

  Widget _buildCoverTile({
    required String coverUrl,
    required String subjectKey,
    required String subjectColor,
    required Gradient gradient,
    required IconData badgeIcon,
    required String title,
    required String subtitle,
    VoidCallback? onTap,
  }) {
    return BookCoverTile(
      coverUrl: coverUrl,
      subjectKey: subjectKey,
      subjectColor: subjectColor,
      gradient: gradient,
      badgeIcon: badgeIcon,
      title: title,
      subtitle: subtitle,
      onTap: onTap,
    );
  }
}
