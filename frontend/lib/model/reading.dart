import 'dart:convert';

/// ReadingRoomView is the aggregated shelf payload from GET /api/v1/readings.
/// Series holds the user-visible series cards (with child counts); Books and
/// Articles are the standalone (散本/散文) items only.
class ReadingRoomView {
  final List<ReadingSeriesCard> series;
  final List<ReadingBook> books;
  final List<ReadingArticle> articles;

  ReadingRoomView({
    this.series = const [],
    this.books = const [],
    this.articles = const [],
  });

  factory ReadingRoomView.fromJson(Map<String, dynamic> json) {
    final seriesRaw = json['Series'] ?? json['series'] ?? [];
    final booksRaw = json['Books'] ?? json['books'] ?? [];
    final articlesRaw = json['Articles'] ?? json['articles'] ?? [];
    return ReadingRoomView(
      series: (seriesRaw as List)
          .map((e) => ReadingSeriesCard.fromJson(e as Map<String, dynamic>))
          .toList(),
      books: (booksRaw as List)
          .map((e) => ReadingBook.fromJson(e as Map<String, dynamic>))
          .toList(),
      articles: (articlesRaw as List)
          .map((e) => ReadingArticle.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}

/// A series card on the shelf (container for books + articles).
class ReadingSeriesCard {
  final int id;
  final String title;
  final String description;
  final String grade;
  final String subject;
  final String coverUrl;
  final int sortOrder;
  final int bookCount;
  final int articleCount;

  ReadingSeriesCard({
    required this.id,
    required this.title,
    this.description = '',
    this.grade = 'universal',
    this.subject = '',
    this.coverUrl = '',
    this.sortOrder = 0,
    this.bookCount = 0,
    this.articleCount = 0,
  });

  factory ReadingSeriesCard.fromJson(Map<String, dynamic> json) {
    return ReadingSeriesCard(
      id: json['ID'] ?? json['id'] ?? 0,
      title: json['Title'] ?? json['title'] ?? '',
      description: json['Description'] ?? json['description'] ?? '',
      grade: json['Grade'] ?? json['grade'] ?? 'universal',
      subject: json['Subject'] ?? json['subject'] ?? '',
      coverUrl: json['CoverURL'] ?? json['cover_url'] ?? '',
      sortOrder: (json['SortOrder'] ?? json['sort_order'] ?? 0) as int,
      bookCount: (json['BookCount'] ?? json['book_count'] ?? 0) as int,
      articleCount: (json['ArticleCount'] ?? json['article_count'] ?? 0) as int,
    );
  }
}

/// A PDF book on the shelf.
class ReadingBook {
  final int id;
  final int seriesId;
  final int sortOrder;
  final String title;
  final String fileHash;
  final int? pageCount;
  final String coverUrl;
  final String grade;
  final String subject;

  ReadingBook({
    required this.id,
    required this.title,
    this.seriesId = 0,
    this.sortOrder = 0,
    this.fileHash = '',
    this.pageCount,
    this.coverUrl = '',
    this.grade = 'universal',
    this.subject = '',
  });

  factory ReadingBook.fromJson(Map<String, dynamic> json) {
    return ReadingBook(
      id: json['ID'] ?? json['id'] ?? 0,
      title: json['Title'] ?? json['title'] ?? '',
      seriesId: (json['SeriesID'] ?? json['series_id'] ?? 0) as int,
      sortOrder: (json['SortOrder'] ?? json['sort_order'] ?? 0) as int,
      fileHash: json['FileHash'] ?? json['file_hash'] ?? '',
      pageCount: json['PageCount'] != null
          ? (json['PageCount'] as num).toInt()
          : (json['page_count'] != null
              ? (json['page_count'] as num).toInt()
              : null),
      coverUrl: json['CoverURL'] ?? json['cover_url'] ?? '',
      grade: json['Grade'] ?? json['grade'] ?? 'universal',
      subject: json['Subject'] ?? json['subject'] ?? '',
    );
  }
}
/// A web article on the shelf.
class ReadingArticle {
  final int id;
  final int seriesId;
  final int sortOrder;
  final String title;
  final String sourceUrl;
  final List<String> whitelistDomains;
  final String coverUrl;
  final String grade;
  final String subject;
  final String mirrorStatus;

  ReadingArticle({
    required this.id,
    required this.title,
    required this.sourceUrl,
    this.seriesId = 0,
    this.sortOrder = 0,
    this.whitelistDomains = const [],
    this.coverUrl = '',
    this.grade = 'universal',
    this.subject = '',
    this.mirrorStatus = 'none',
  });

  factory ReadingArticle.fromJson(Map<String, dynamic> json) {
    var domains = <String>[];
    final rawDomains = json['WhitelistDomains'] ?? json['whitelist_domains'];
    if (rawDomains is List) {
      domains = rawDomains.map((e) => e.toString()).toList();
    } else if (rawDomains is String && rawDomains.isNotEmpty) {
      try {
        final decoded = jsonDecode(rawDomains);
        if (decoded is List) {
          domains = decoded.map((e) => e.toString()).toList();
        }
      } catch (_) {
        // Treat as comma-separated
        domains = rawDomains.split(',').map((e) => e.trim()).where((e) => e.isNotEmpty).toList();
      }
    }
    return ReadingArticle(
      id: json['ID'] ?? json['id'] ?? 0,
      title: json['Title'] ?? json['title'] ?? '',
      sourceUrl: json['SourceURL'] ?? json['source_url'] ?? '',
      seriesId: (json['SeriesID'] ?? json['series_id'] ?? 0) as int,
      sortOrder: (json['SortOrder'] ?? json['sort_order'] ?? 0) as int,
      whitelistDomains: domains,
      coverUrl: json['CoverURL'] ?? json['cover_url'] ?? '',
      grade: json['Grade'] ?? json['grade'] ?? 'universal',
      subject: json['Subject'] ?? json['subject'] ?? '',
      mirrorStatus: json['MirrorStatus'] ?? json['mirror_status'] ?? 'none',
    );
  }

  /// The URL the client should load. Phase 1: always sourceUrl.
  /// Phase 2 (future): mirrored URL when mirrorStatus == 'ready'.
  String get effectiveUrl => sourceUrl;
}

/// Series detail (from GET /api/v1/readings/series/:id) — the series card
/// plus its child books and articles.
class ReadingSeriesDetail {
  final ReadingSeriesCard series;
  final List<ReadingBook> books;
  final List<ReadingArticle> articles;

  ReadingSeriesDetail({
    required this.series,
    this.books = const [],
    this.articles = const [],
  });

  factory ReadingSeriesDetail.fromJson(Map<String, dynamic> json) {
    final seriesJson = json['Series'] ?? json['series'] ?? {};
    final booksRaw = json['Books'] ?? json['books'] ?? [];
    final articlesRaw = json['Articles'] ?? json['articles'] ?? [];
    return ReadingSeriesDetail(
      series: ReadingSeriesCard.fromJson(seriesJson as Map<String, dynamic>),
      books: (booksRaw as List)
          .map((e) => ReadingBook.fromJson(e as Map<String, dynamic>))
          .toList(),
      articles: (articlesRaw as List)
          .map((e) => ReadingArticle.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}
