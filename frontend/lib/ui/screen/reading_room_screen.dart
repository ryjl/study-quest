import 'package:flutter/material.dart';
import '../../model/reading.dart';
import '../../model/subject.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../widget/bookshelf.dart';
import '../widget/focus_button.dart';
import '../widget/state_widgets.dart';
import 'pdf_reader_screen.dart';
import 'article_reader_screen.dart';
import 'reading_series_detail_screen.dart';

class ReadingRoomScreen extends StatefulWidget {
  final int activeUserId;

  const ReadingRoomScreen({Key? key, required this.activeUserId}) : super(key: key);

  @override
  State<ReadingRoomScreen> createState() => _ReadingRoomScreenState();
}

class _ReadingRoomScreenState extends State<ReadingRoomScreen> {
  late Future<ReadingRoomView> _readingRoomFuture;
  List<Subject> _subjectsCatalog = const [];
  String _searchQuery = '';

  @override
  void initState() {
    super.initState();
    _loadData();
  }

  void _loadData() {
    _readingRoomFuture = ApiService.fetchReadingRoom(widget.activeUserId);
    setState(() {});
    ApiService.fetchSubjects(widget.activeUserId).then((list) {
      if (mounted) setState(() => _subjectsCatalog = list);
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.transparent,
      body: FutureBuilder<ReadingRoomView>(
        future: _readingRoomFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return loadingSpinner();
          }
          if (snapshot.hasError) {
            return errorStateBox(snapshot.error.toString(), _loadData,
                message: '加载阅读室失败，请检查网络！');
          }

          final view = snapshot.data!;

          // Apply search filter.
          final filteredSeries = view.series.where((s) =>
              _searchQuery.isEmpty ||
              s.title.toLowerCase().contains(_searchQuery.toLowerCase())).toList();
          final filteredBooks = view.books.where((b) =>
              _searchQuery.isEmpty ||
              b.title.toLowerCase().contains(_searchQuery.toLowerCase())).toList();
          final filteredArticles = view.articles.where((a) =>
              _searchQuery.isEmpty ||
              a.title.toLowerCase().contains(_searchQuery.toLowerCase())).toList();

          final isEmpty = filteredSeries.isEmpty &&
              filteredBooks.isEmpty &&
              filteredArticles.isEmpty;

          return Padding(
            padding: const EdgeInsets.symmetric(horizontal: 20.0, vertical: 16.0),
            child: SingleChildScrollView(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  // Header
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: const [
                          Text(
                            '阅读室',
                            style: TextStyle(
                              fontFamily: 'Quicksand',
                              fontSize: 32,
                              fontWeight: FontWeight.w900,
                              color: AppTheme.textWhite,
                            ),
                          ),
                          SizedBox(height: 6),
                          Text(
                            '翻开一本书，开启一段旅程 📖',
                            style: TextStyle(
                              fontSize: 15,
                              color: AppTheme.textMuted,
                              fontWeight: FontWeight.bold,
                            ),
                          ),
                        ],
                      ),
                    ],
                  ),
                  const SizedBox(height: 24),

                  // Search bar
                  TextField(
                    onChanged: (val) => setState(() => _searchQuery = val),
                    style: const TextStyle(fontSize: 14, fontWeight: FontWeight.bold, color: AppTheme.textWhite),
                    decoration: InputDecoration(
                      hintText: '搜索书名或文章...',
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

                  if (isEmpty)
                    emptyStateBox(
                      icon: Icons.menu_book_outlined,
                      headline: '阅读室还没有内容',
                      hint: '请让爸爸妈妈在后台分配阅读资源吧！',
                      refreshLabel: '刷新',
                      onRefresh: _loadData,
                    )
                  else ...[
                    // Series section — bookshelf rows
                    if (filteredSeries.isNotEmpty) ...[
                      _sectionTitle('📚 系列'),
                      const SizedBox(height: 12),
                      ...filteredSeries.map((s) => _buildSeriesShelfRow(s)),
                      if (filteredBooks.isNotEmpty || filteredArticles.isNotEmpty)
                        const SizedBox(height: 28),
                    ],

                    // Standalone books section
                    if (filteredBooks.isNotEmpty) ...[
                      _sectionTitle('📕 单本（PDF）'),
                      const SizedBox(height: 12),
                      _buildBookShelfRow(filteredBooks),
                      if (filteredArticles.isNotEmpty)
                        const SizedBox(height: 28),
                    ],

                    // Standalone articles section
                    if (filteredArticles.isNotEmpty) ...[
                      _sectionTitle('🌐 单文（网页）'),
                      const SizedBox(height: 12),
                      _buildArticleShelfRow(filteredArticles),
                    ],
                  ],
                ],
              ),
            ),
          );
        },
      ),
    );
  }

  Widget _sectionTitle(String text) => sectionTitle(text);

  /// A series shelf row: a "bookshelf board" with the series cover + title,
  /// tappable to enter the series detail.
  Widget _buildSeriesShelfRow(ReadingSeriesCard series) {
    final subj = resolveSubject(series.subject, _subjectsCatalog);
    final gradient = AppTheme.getSubjectGradientFromColor(subj.color);
    return Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: FocusButton(
        padding: EdgeInsets.zero,
        borderRadius: 20,
        borderColor: const Color(0xFFE2E8F0),
        onPressed: () {
          Navigator.push(
            context,
            MaterialPageRoute(
              builder: (context) => ReadingSeriesDetailScreen(
                activeUserId: widget.activeUserId,
                series: series,
              ),
            ),
          ).then((_) => _loadData());
        },
        child: _buildShelfBoard(
          children: [
            // Cover with 3D tilt + shadow
            _buildCoverTile(
              coverUrl: series.coverUrl,
              subjectKey: series.subject,
              subjectColor: subj.color,
              gradient: gradient,
              badgeIcon: Icons.collections_bookmark_rounded,
              title: series.title,
              subtitle: '${series.bookCount} 本书 · ${series.articleCount} 篇文章',
            ),
          ],
        ),
      ),
    );
  }

  /// A horizontal shelf of standalone books.
  Widget _buildBookShelfRow(List<ReadingBook> books) {
    return _buildShelfBoard(
      children: books.map((b) => _buildBookCover(b)).toList(),
    );
  }

  /// A horizontal shelf of standalone articles.
  Widget _buildArticleShelfRow(List<ReadingArticle> articles) {
    return _buildShelfBoard(
      children: articles.map((a) => _buildArticleCover(a)).toList(),
    );
  }

  /// The "bookshelf board" — a semi-transparent gradient container with a
  /// dark bottom border simulating the shelf edge.
  Widget _buildShelfBoard({required List<Widget> children}) => buildShelfBoard(children: children);

  /// Cover tile for a standalone book — tilted with shadow, tappable.
  Widget _buildBookCover(ReadingBook book) {
    final subj = resolveSubject(book.subject, _subjectsCatalog);
    final gradient = AppTheme.getSubjectGradientFromColor(subj.color);
    return _buildCoverTile(
      coverUrl: book.coverUrl,
      subjectKey: book.subject,
      subjectColor: subj.color,
      gradient: gradient,
      badgeIcon: Icons.picture_as_pdf_rounded,
      title: book.title,
      subtitle: book.pageCount != null ? '${book.pageCount} 页' : 'PDF',
      onTap: () {
        Navigator.push(
          context,
          MaterialPageRoute(
            builder: (context) => PdfReaderScreen(
              activeUserId: widget.activeUserId,
              book: book,
            ),
          ),
        ).then((_) => _loadData());
      },
    );
  }

  /// Cover tile for a standalone article.
  Widget _buildArticleCover(ReadingArticle article) {
    final subj = resolveSubject(article.subject, _subjectsCatalog);
    final gradient = AppTheme.getSubjectGradientFromColor(subj.color);
    return _buildCoverTile(
      coverUrl: article.coverUrl,
      subjectKey: article.subject,
      subjectColor: subj.color,
      gradient: gradient,
      badgeIcon: Icons.language_rounded,
      title: article.title,
      subtitle: '网页',
      onTap: () {
        Navigator.push(
          context,
          MaterialPageRoute(
            builder: (context) => ArticleReaderScreen(
              activeUserId: widget.activeUserId,
              article: article,
            ),
          ),
        ).then((_) => _loadData());
      },
    );
  }

  /// A single book cover on the shelf — image or gradient fallback, with a
  /// slight 3D tilt and drop shadow for the "standing on the shelf" look.
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
