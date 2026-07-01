import 'dart:convert';

class Course {
  final int id;
  final String title;
  final String grade;
  final String subject;
  final String coverUrl;

  Course({
    required this.id,
    required this.title,
    required this.grade,
    required this.subject,
    required this.coverUrl,
  });

  factory Course.fromJson(Map<String, dynamic> json) {
    return Course(
      id: json['ID'] ?? json['id'] ?? 0,
      title: json['Title'] ?? json['title'] ?? '',
      grade: json['Grade'] ?? json['grade'] ?? 'universal',
      subject: json['Subject'] ?? json['subject'] ?? '',
      coverUrl: json['CoverURL'] ?? json['cover_url'] ?? '',
    );
  }
}

class Episode {
  final int id;
  final int courseId;
  final int sortOrder;
  final String title;
  final String videoRelativePath;
  final String attachmentJson;
  final String fileHash;
  final int fileSize;
  final int durationSeconds;

  Episode({
    required this.id,
    required this.courseId,
    required this.sortOrder,
    required this.title,
    required this.videoRelativePath,
    required this.attachmentJson,
    required this.fileHash,
    required this.fileSize,
    required this.durationSeconds,
  });

  factory Episode.fromJson(Map<String, dynamic> json) {
    return Episode(
      id: json['ID'] ?? json['id'] ?? 0,
      courseId: json['CourseID'] ?? json['course_id'] ?? 0,
      sortOrder: json['SortOrder'] ?? json['sort_order'] ?? 1,
      title: json['Title'] ?? json['title'] ?? '',
      videoRelativePath: json['VideoRelativePath'] ?? json['video_relative_path'] ?? '',
      attachmentJson: json['AttachmentJSON'] ?? json['attachment_json'] ?? '[]',
      fileHash: json['FileHash'] ?? json['file_hash'] ?? '',
      fileSize: json['FileSize'] ?? json['file_size'] ?? 0,
      durationSeconds: json['DurationSeconds'] ?? json['duration_seconds'] ?? 0,
    );
  }

  List<String> get attachments {
    try {
      return List<String>.from(jsonDecode(attachmentJson));
    } catch (_) {
      return [];
    }
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
