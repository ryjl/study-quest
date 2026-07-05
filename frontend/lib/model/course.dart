import 'dart:convert';

class Course {
  final int id;
  final String title;
  final String grade;
  final String subject;
  final String coverUrl;
  final String tags;

  Course({
    required this.id,
    required this.title,
    required this.grade,
    required this.subject,
    required this.coverUrl,
    this.tags = '',
  });

  factory Course.fromJson(Map<String, dynamic> json) {
    return Course(
      id: json['ID'] ?? json['id'] ?? 0,
      title: json['Title'] ?? json['title'] ?? '',
      grade: json['Grade'] ?? json['grade'] ?? 'universal',
      subject: json['Subject'] ?? json['subject'] ?? '',
      coverUrl: json['CoverURL'] ?? json['cover_url'] ?? '',
      tags: json['Tags'] ?? json['tags'] ?? '',
    );
  }

  List<String> get tagsList {
    if (tags.isEmpty) return const [];
    return tags
        .split(',')
        .map((t) => t.trim())
        .where((t) => t.isNotEmpty)
        .toList();
  }
}

/// A chapter/module within a course (mirrors backend model.Chapter).
class Chapter {
  final int id;
  final int courseId;
  final String title;
  final String description;
  final String coverUrl;
  final int sortOrder;

  const Chapter({
    required this.id,
    required this.courseId,
    required this.title,
    this.description = '',
    this.coverUrl = '',
    this.sortOrder = 0,
  });

  factory Chapter.fromJson(Map<String, dynamic> json) {
    return Chapter(
      id: json['ID'] ?? json['id'] ?? 0,
      courseId: json['CourseID'] ?? json['course_id'] ?? 0,
      title: json['Title'] ?? json['title'] ?? '',
      description: json['Description'] ?? json['description'] ?? '',
      coverUrl: json['CoverURL'] ?? json['cover_url'] ?? '',
      sortOrder: json['SortOrder'] ?? json['sort_order'] ?? 0,
    );
  }
}

class Episode {
  final int id;
  final int courseId;
  final int chapterId;
  final int sortOrder;
  final String title;
  final String videoRelativePath;
  final String coverUrl;
  final String attachmentJson;
  final String fileHash;
  final int fileSize;
  final int durationSeconds;

  Episode({
    required this.id,
    required this.courseId,
    this.chapterId = 0,
    required this.sortOrder,
    required this.title,
    required this.videoRelativePath,
    this.coverUrl = '',
    required this.attachmentJson,
    required this.fileHash,
    required this.fileSize,
    required this.durationSeconds,
  });

  factory Episode.fromJson(Map<String, dynamic> json) {
    return Episode(
      id: json['ID'] ?? json['id'] ?? 0,
      courseId: json['CourseID'] ?? json['course_id'] ?? 0,
      chapterId: json['ChapterID'] ?? json['chapter_id'] ?? 0,
      sortOrder: json['SortOrder'] ?? json['sort_order'] ?? 1,
      title: json['Title'] ?? json['title'] ?? '',
      videoRelativePath:
          json['VideoRelativePath'] ?? json['video_relative_path'] ?? '',
      coverUrl: json['CoverURL'] ?? json['cover_url'] ?? '',
      attachmentJson: json['AttachmentJSON'] ?? json['attachment_json'] ?? '[]',
      fileHash: json['FileHash'] ?? json['file_hash'] ?? '',
      fileSize: json['FileSize'] ?? json['file_size'] ?? 0,
      durationSeconds: json['DurationSeconds'] ?? json['duration_seconds'] ?? 0,
    );
  }

  List<String> get attachmentPaths {
    try {
      final decoded = jsonDecode(attachmentJson);
      if (decoded is List) {
        return decoded.map((e) => e.toString()).toList();
      }
    } catch (_) {}
    return const [];
  }
}

/// One supplementary file attached to an episode (PDF, doc, etc.).
///
/// The backend stores attachments as a JSON array of relative storage paths
/// (e.g. `/courses/math/lecture1.pdf`). [path] is the raw path while [index]
/// is its position in that array, used to build the streaming URL
/// `/episodes/:id/attachments/:index/stream`.
class Attachment {
  final int index;
  final String path;

  const Attachment({required this.index, required this.path});

  factory Attachment.fromJson(Map<String, dynamic> json) {
    // Backend attachment list is a plain JSON array of strings, but the helper
    // in api_service maps each entry into an object so the UI can stay typed.
    final raw = json['path'] ?? json['Path'] ?? json['name'] ?? '';
    return Attachment(
      index: (json['index'] ?? json['Index'] ?? 0) as int,
      path: raw.toString(),
    );
  }

  String get fileName {
    if (path.isEmpty) return '';
    final parts = path.split(RegExp(r'[/\\]'));
    return parts.isEmpty ? path : parts.last;
  }

  String get extension {
    final dot = fileName.lastIndexOf('.');
    return dot >= 0 ? fileName.substring(dot + 1).toLowerCase() : '';
  }

  bool get isPdf => extension == 'pdf';
}

/// A subtitle track exposed by play-info, mapped to a WebVTT URL.
///
/// Named [EpisodeSubtitle] to avoid colliding with media_kit's own
/// [SubtitleTrack] type which we also use inside the player.
class EpisodeSubtitle {
  final int id;
  final String language;
  final String label;
  final String url;

  const EpisodeSubtitle({
    required this.id,
    required this.language,
    required this.label,
    required this.url,
  });

  factory EpisodeSubtitle.fromJson(Map<String, dynamic> json) {
    return EpisodeSubtitle(
      id: json['id'] ?? json['ID'] ?? 0,
      language: json['language'] ?? json['Language'] ?? 'zh-CN',
      label: json['label'] ?? json['Label'] ?? '字幕',
      url: json['url'] ?? '',
    );
  }
}

/// Strongly typed view of the `/episodes/:id/play-info` response.
class PlayInfo {
  final String url;
  final Map<String, String> headers;
  final int? resumePositionSeconds;
  final bool isCompleted;
  final List<EpisodeSubtitle> subtitles;

  const PlayInfo({
    required this.url,
    required this.headers,
    this.resumePositionSeconds,
    this.isCompleted = false,
    this.subtitles = const [],
  });

  factory PlayInfo.fromJson(Map<String, dynamic> json) {
    final Map<String, String> headers = {};
    final rawHeaders = json['headers'];
    if (rawHeaders is Map) {
      rawHeaders.forEach((k, v) => headers[k.toString()] = v.toString());
    }

    int? resume;
    bool completed = false;
    final progress = json['progress'];
    if (progress is Map) {
      final last = progress['last_position_seconds'];
      if (last is num) resume = last.toInt();
      completed = progress['is_completed'] == true ||
          progress['is_completed'] == 1;
    }

    final List<EpisodeSubtitle> subs = [];
    final rawSubs = json['subtitles'];
    if (rawSubs is List) {
      for (final s in rawSubs) {
        if (s is Map) {
          subs.add(EpisodeSubtitle.fromJson(s.cast<String, dynamic>()));
        }
      }
    }

    return PlayInfo(
      url: json['url'] ?? '',
      headers: headers,
      resumePositionSeconds: resume,
      isCompleted: completed,
      subtitles: subs,
    );
  }
}

class ExplorerCard {
  final String prompt;
  ExplorerCard({required this.prompt});
}

class ReviewQuiz {
  final String question;
  final List<String> options;
  final int answerIndex;

  ReviewQuiz({
    required this.question,
    required this.options,
    required this.answerIndex,
  });
}

class AILessonContent {
  final int episodeId;
  final List<ExplorerCard> preAdventureCards;
  final List<ReviewQuiz> postReviewQuiz;

  AILessonContent({
    required this.episodeId,
    required this.preAdventureCards,
    required this.postReviewQuiz,
  });

  factory AILessonContent.fromJson(Map<String, dynamic> json) {
    final epId = json['EpisodeID'] ?? json['episode_id'] ?? 0;
    
    // Parse Pre-adventure cards
    final List<ExplorerCard> preCards = [];
    final preStr = json['PreAdventureJSON'] ?? json['pre_adventure_json'] ?? '';
    if (preStr.isNotEmpty) {
      try {
        final List<dynamic> list = jsonDecode(preStr);
        for (var item in list) {
          preCards.add(ExplorerCard(prompt: item.toString()));
        }
      } catch (_) {}
    }

    // Parse Post-review quiz
    final List<ReviewQuiz> postQuiz = [];
    final postStr = json['PostReviewJSON'] ?? json['post_review_json'] ?? '';
    if (postStr.isNotEmpty) {
      try {
        final List<dynamic> list = jsonDecode(postStr);
        for (var item in list) {
          final q = item['question'] ?? '';
          final List<String> opts = List<String>.from(item['options'] ?? []);
          final ansIdx = item['answer_index'] ?? 0;
          postQuiz.add(ReviewQuiz(question: q, options: opts, answerIndex: ansIdx));
        }
      } catch (_) {}
    }

    return AILessonContent(
      episodeId: epId,
      preAdventureCards: preCards,
      postReviewQuiz: postQuiz,
    );
  }
}
