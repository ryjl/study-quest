import 'dart:convert';

class Course {
  final int id;
  final String title;
  final String grade;
  final String subject;
  final String coverUrl;
  // Display tags for the course card. Backend serves this as the `TagsList`
  // string array (see client_dto.go) alongside `tag_ids`; the old comma-joined
  // `Tags` string contract was dropped.
  final List<String> tagsList;
  final List<int> tagIds;
  // Drip-unlock summary shown on the course card (student roles only). Populated
  // by GetCourses; empty/zero values mean "no drip cadence" → card hides the badge.
  final String unlockStrategy;
  final String unlockStrategyLabel;
  final int unlockedCount;
  final int episodeTotal;
  final String nextUnlockAt;

  Course({
    required this.id,
    required this.title,
    required this.grade,
    required this.subject,
    required this.coverUrl,
    this.tagsList = const [],
    this.tagIds = const [],
    this.unlockStrategy = '',
    this.unlockStrategyLabel = '',
    this.unlockedCount = 0,
    this.episodeTotal = 0,
    this.nextUnlockAt = '',
  });

  factory Course.fromJson(Map<String, dynamic> json) {
    final rawTagIds = json['TagIDs'] ?? json['tag_ids'];
    final rawTagsList = json['TagsList'] ?? json['tags_list'];
    return Course(
      id: json['ID'] ?? json['id'] ?? 0,
      title: json['Title'] ?? json['title'] ?? '',
      grade: json['Grade'] ?? json['grade'] ?? 'universal',
      subject: json['Subject'] ?? json['subject'] ?? '',
      coverUrl: json['CoverURL'] ?? json['cover_url'] ?? '',
      tagsList: rawTagsList is List
          ? rawTagsList.map((e) => e.toString()).toList()
          : const [],
      tagIds: rawTagIds is List
          ? rawTagIds.map((e) => (e as num).toInt()).toList()
          : const [],
      unlockStrategy: json['UnlockStrategy'] ?? json['unlock_strategy'] ?? '',
      unlockStrategyLabel: json['UnlockStrategyLabel'] ?? json['unlock_strategy_label'] ?? '',
      unlockedCount: (json['UnlockedCount'] ?? json['unlocked_count'] ?? 0) as int,
      episodeTotal: (json['EpisodeTotal'] ?? json['episode_total'] ?? 0) as int,
      nextUnlockAt: json['NextUnlockAt'] ?? json['next_unlock_at'] ?? '',
    );
  }

  /// Whether this course runs under a drip-unlock schedule (vs all-open). Drives
  /// the badge visibility on the course card.
  bool get hasUnlockSchedule => unlockStrategy.isNotEmpty && unlockStrategy != 'all_open';
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
  final bool locked;
  // Phase 2:课程级 AI 开关回显 + 该 episode 是否有字幕。三个字段都由后端
  // 在 episode DTO 里下发(PascalCase,add-only),客户端据此决定 AI 学习入口
  // 的可见性与可用提示。默认 false 兼容老后端(字段缺失视为"未开启/无字幕")。
  final bool aiSummaryEnabled;
  final bool aiQuizEnabled;
  final bool hasSubtitle;

  Episode({
    required this.id,
    required this.courseId,
    this.chapterId = 0,
    required this.sortOrder,
    required this.title,
    required this.videoRelativePath,
    this.coverUrl = '',
    required this.attachmentJson,
    this.fileHash = '',
    required this.fileSize,
    required this.durationSeconds,
    this.locked = false,
    this.aiSummaryEnabled = false,
    this.aiQuizEnabled = false,
    this.hasSubtitle = false,
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
      // The student-facing episodes endpoint annotates each episode with a
      // `locked` flag derived from the unlock resolution. Default false so a
      // missing field (older backends) treats everything as visible.
      locked: json['locked'] == true,
      // Phase 2 新增三字段:dual-key 读取,主读 PascalCase(与既有字段一致),
      // 兼容老后端缺失时默认 false。
      aiSummaryEnabled: json['AISummaryEnabled'] ?? json['ai_summary_enabled'] ?? false,
      aiQuizEnabled: json['AIQuizEnabled'] ?? json['ai_quiz_enabled'] ?? false,
      hasSubtitle: json['HasSubtitle'] ?? json['has_subtitle'] ?? false,
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
