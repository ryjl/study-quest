import 'dart:convert';

// 课程考试(TODO.md P0)的客户端 model。镜像后端 service.ExamView /
// ExamSubmitReport / ExamStatus 的 JSON 形状(见 exam_service.go)。
//
// 和 quiz/wrong_book 同构:开考返回一张不带正确答案的卷子(防作弊),交卷后
// 逐题揭示正确答案 + 解析。题面/选项复用 quiz 渲染范式。

/// GET /courses/:id/exam/status 的响应:是否可考 + 原因。
/// available=false 时 reason 给出提示(题库不足 / 考试功能未启用)。
class ExamStatus {
  final bool available;
  final String reason;

  const ExamStatus({required this.available, this.reason = ''});

  factory ExamStatus.fromJson(Map<String, dynamic> j) => ExamStatus(
        available: (j['available'] ?? false) as bool,
        reason: (j['reason'] ?? '').toString(),
      );
}

/// 考试卷里的一道题(对应后端 QuizViewQuestion,复用 quiz 渲染)。
/// 不带正确答案(防作弊),只有 id/type/stem/options/hasJump。
class ExamQuestion {
  final int id;
  final String type; // choice | multi_choice | fill
  final String stem;
  final List<String> options; // choice / multi_choice
  final bool hasJump;

  const ExamQuestion({
    required this.id,
    required this.type,
    required this.stem,
    required this.options,
    required this.hasJump,
  });

  bool get isFill => type == 'fill';
  bool get isMultiChoice => type == 'multi_choice';

  factory ExamQuestion.fromJson(Map<String, dynamic> j) {
    final optsRaw = j['options'];
    List<String> options = const [];
    if (optsRaw is List) {
      options = optsRaw.map((e) => e.toString()).toList();
    } else if (optsRaw is String && optsRaw.isNotEmpty) {
      try {
        final decoded = jsonDecode(optsRaw);
        if (decoded is List) options = decoded.map((e) => e.toString()).toList();
      } catch (_) {}
    }
    return ExamQuestion(
      id: (j['id'] as num?)?.toInt() ?? 0,
      type: (j['type'] ?? 'choice').toString(),
      stem: (j['stem'] ?? '').toString(),
      options: options,
      hasJump: (j['has_jump'] ?? false) as bool,
    );
  }
}

/// 开考/取 active exam 返回的考试卷(对应后端 ExamView)。
class ExamView {
  final int examId;
  final int courseId;
  final List<ExamQuestion> questions;
  /// 该 exam 是否已交卷(取 active exam 时回填)。已交卷则前端锁定。
  final bool submitted;

  const ExamView({
    required this.examId,
    required this.courseId,
    required this.questions,
    this.submitted = false,
  });

  factory ExamView.fromJson(Map<String, dynamic> j) {
    final qs = (j['questions'] as List<dynamic>?)
            ?.map((e) => ExamQuestion.fromJson(e as Map<String, dynamic>))
            .toList() ??
        const [];
    return ExamView(
      examId: (j['exam_id'] as num?)?.toInt() ?? 0,
      courseId: (j['course_id'] as num?)?.toInt() ?? 0,
      questions: qs,
      submitted: (j['submitted'] ?? false) as bool,
    );
  }
}

/// 交卷的逐题结果(对应后端 ExamSubmitResult)。揭示正确答案 + 解析。
/// source 区分题库题(pool)vs 新生题(generated)。
class ExamSubmitResult {
  final int questionId;
  final bool correct;
  final bool partial;
  final int? correctIndex; // choice
  final String correctText; // fill
  final List<int> correctIndices; // multi_choice
  final String explanation;
  final String source; // pool | generated

  const ExamSubmitResult({
    required this.questionId,
    required this.correct,
    required this.partial,
    required this.correctIndex,
    required this.correctText,
    required this.correctIndices,
    required this.explanation,
    required this.source,
  });

  factory ExamSubmitResult.fromJson(Map<String, dynamic> j) {
    final ci = j['correct_indices'];
    return ExamSubmitResult(
      questionId: (j['question_id'] as num?)?.toInt() ?? 0,
      correct: (j['correct'] ?? false) as bool,
      partial: (j['partial'] ?? false) as bool,
      correctIndex: (j['correct_index'] as num?)?.toInt(),
      correctText: (j['correct_text'] ?? '').toString(),
      correctIndices: ci is List ? ci.map((e) => (e as num).toInt()).toList() : const [],
      explanation: (j['explanation'] ?? '').toString(),
      source: (j['source'] ?? 'pool').toString(),
    );
  }
}

/// 交卷的整体报告(对应后端 ExamSubmitReport)。
class ExamSubmitReport {
  final int examId;
  final double score; // 得分率 0-1
  final List<ExamSubmitResult> results;

  const ExamSubmitReport({
    required this.examId,
    required this.score,
    required this.results,
  });

  factory ExamSubmitReport.fromJson(Map<String, dynamic> j) {
    final rs = (j['results'] as List<dynamic>?)
            ?.map((e) => ExamSubmitResult.fromJson(e as Map<String, dynamic>))
            .toList() ??
        const [];
    return ExamSubmitReport(
      examId: (j['exam_id'] as num?)?.toInt() ?? 0,
      score: (j['score'] as num?)?.toDouble() ?? 0.0,
      results: rs,
    );
  }
}
