import 'dart:convert';

/// 错题本的一行(对应后端 WrongBookItemView)。题面现查 Question 表(后端 join),
/// 这里只是客户端展示用——题面 + curation 状态合并。
class WrongBookItem {
  final int questionId;
  final String stem;
  final String type; // choice | multi_choice | fill
  final List<String> options;
  final String explanation;
  // 正确答案(列表卡片展开时直接显示,后端 GetWrongBook 派生)。复用 redo result 字段名。
  final int? correctIndex; // choice
  final String correctText; // fill
  final List<int> correctIndices; // multi_choice
  final bool hasJump;
  final int chunkId;
  final int courseId;
  final int episodeId;
  final int subjectId;
  final String firstWrongAt;
  final String lastAttemptedAt;
  final int attemptCount;
  // 连续答对次数(重做流累加,达 3 掌握)。前端展示"再对 N 次掌握"。
  final int correctStreak;
  final bool mastered;

  WrongBookItem({
    required this.questionId,
    required this.stem,
    required this.type,
    required this.options,
    required this.explanation,
    required this.correctIndex,
    required this.correctText,
    required this.correctIndices,
    required this.hasJump,
    required this.chunkId,
    required this.courseId,
    required this.episodeId,
    required this.subjectId,
    required this.firstWrongAt,
    required this.lastAttemptedAt,
    required this.attemptCount,
    required this.correctStreak,
    required this.mastered,
  });

  factory WrongBookItem.fromJson(Map<String, dynamic> json) {
    final optsRaw = json['options'];
    List<String> options = const [];
    if (optsRaw is List) {
      options = optsRaw.map((e) => e.toString()).toList();
    } else if (optsRaw is String && optsRaw.isNotEmpty) {
      try {
        final decoded = jsonDecode(optsRaw);
        if (decoded is List) options = decoded.map((e) => e.toString()).toList();
      } catch (_) {}
    }
    final ci = json['correct_indices'];
    return WrongBookItem(
      questionId: (json['question_id'] as num?)?.toInt() ?? 0,
      stem: json['stem'] as String? ?? '',
      type: json['type'] as String? ?? 'choice',
      options: options,
      explanation: json['explanation'] as String? ?? '',
      correctIndex: (json['correct_index'] as num?)?.toInt(),
      correctText: json['correct_text'] as String? ?? '',
      correctIndices: ci is List ? ci.map((e) => (e as num).toInt()).toList() : const [],
      hasJump: json['has_jump'] as bool? ?? false,
      chunkId: (json['chunk_id'] as num?)?.toInt() ?? 0,
      courseId: (json['course_id'] as num?)?.toInt() ?? 0,
      episodeId: (json['episode_id'] as num?)?.toInt() ?? 0,
      subjectId: (json['subject_id'] as num?)?.toInt() ?? 0,
      firstWrongAt: json['first_wrong_at'] as String? ?? '',
      lastAttemptedAt: json['last_attempted_at'] as String? ?? '',
      attemptCount: (json['attempt_count'] as num?)?.toInt() ?? 0,
      correctStreak: (json['correct_streak'] as num?)?.toInt() ?? 0,
      mastered: json['mastered'] as bool? ?? false,
    );
  }
}

/// 错题本列表响应:items + 未掌握总数(给 tab 角标用,独立于 items 的过滤)。
/// 对应后端 wrongBookListResponse {items, unmastered_count}。
class WrongBookList {
  final List<WrongBookItem> items;
  final int unmasteredCount;

  const WrongBookList({required this.items, required this.unmasteredCount});

  factory WrongBookList.fromJson(Map<String, dynamic> json) {
    final raw = json['items'];
    final items = raw is List
        ? raw.map((e) => WrongBookItem.fromJson(e as Map<String, dynamic>)).toList()
        : const <WrongBookItem>[];
    return WrongBookList(
      items: items,
      unmasteredCount: (json['unmastered_count'] as num?)?.toInt() ?? 0,
    );
  }
}

/// 错题本重做卷里的一道题(对应后端 QuizViewQuestion,复用 quiz 渲染)。
/// 重做卷不带正确答案(防作弊),只有 id/type/stem/options。
class WrongBookRedoQuestion {
  final int id;
  final String type;
  final String stem;
  final List<String> options;
  final bool hasJump;

  WrongBookRedoQuestion({
    required this.id,
    required this.type,
    required this.stem,
    required this.options,
    required this.hasJump,
  });

  factory WrongBookRedoQuestion.fromJson(Map<String, dynamic> json) {
    final optsRaw = json['options'];
    List<String> options = const [];
    if (optsRaw is List) {
      options = optsRaw.map((e) => e.toString()).toList();
    }
    return WrongBookRedoQuestion(
      id: (json['id'] as num?)?.toInt() ?? 0,
      type: json['type'] as String? ?? 'choice',
      stem: json['stem'] as String? ?? '',
      options: options,
      hasJump: json['has_jump'] as bool? ?? false,
    );
  }
}

/// 错题本重做交卷的逐题结果(对应后端 WrongBookRedoResult)。
class WrongBookRedoResult {
  final int questionId;
  final bool correct;
  final bool partial;
  final int? correctIndex;
  final String correctText;
  final List<int> correctIndices;
  final String explanation;

  WrongBookRedoResult({
    required this.questionId,
    required this.correct,
    required this.partial,
    required this.correctIndex,
    required this.correctText,
    required this.correctIndices,
    required this.explanation,
  });

  factory WrongBookRedoResult.fromJson(Map<String, dynamic> json) {
    final ci = json['correct_indices'];
    return WrongBookRedoResult(
      questionId: (json['question_id'] as num?)?.toInt() ?? 0,
      correct: json['correct'] as bool? ?? false,
      partial: json['partial'] as bool? ?? false,
      correctIndex: (json['correct_index'] as num?)?.toInt(),
      correctText: json['correct_text'] as String? ?? '',
      correctIndices: ci is List ? ci.map((e) => (e as num).toInt()).toList() : const [],
      explanation: json['explanation'] as String? ?? '',
    );
  }
}
