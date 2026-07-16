// Phase C AI quiz models for the client. These mirror the backend's
// /ai-summary and /ai-quiz JSON shapes. The client never sees the correct
// answer until after submit (QuizQuestion has no answer field); the verdict
// comes back via QuizAnswerResult.

/// The AI-generated summary for an episode (GET /episodes/:id/ai-summary).
/// Rendered at the top of the AI study screen.
class EpisodeSummary {
  final String headline;
  final List<String> keyPoints;
  final List<String> concepts;
  final String takeaway;

  EpisodeSummary({
    required this.headline,
    required this.keyPoints,
    required this.concepts,
    required this.takeaway,
  });

  factory EpisodeSummary.fromJson(Map<String, dynamic> j) => EpisodeSummary(
        headline: (j['headline'] ?? '').toString(),
        keyPoints: (j['key_points'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
        concepts: (j['concepts'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
        takeaway: (j['takeaway'] ?? '').toString(),
      );

  bool get isEmpty => headline.isEmpty && keyPoints.isEmpty && concepts.isEmpty;
}

/// The status returned by GET /ai-quiz. Drives the screen's state machine.
enum QuizStatus { ready, generating, unavailable }

/// One question as served to the client. Deliberately has NO answer field —
/// the correct answer is only revealed after submit (QuizAnswerResult).
class QuizQuestion {
  final int id;
  final String type; // "choice" | "fill"
  final String stem;
  final List<String> options; // choice only
  final int? chunkStartTime; // seconds, for "[跳转 12:38]"; null if synthetic
  final bool answered;

  QuizQuestion({
    required this.id,
    required this.type,
    required this.stem,
    required this.options,
    this.chunkStartTime,
    required this.answered,
  });

  bool get isFill => type == 'fill';

  factory QuizQuestion.fromJson(Map<String, dynamic> j) => QuizQuestion(
        id: (j['id'] as num).toInt(),
        type: (j['type'] ?? 'choice').toString(),
        stem: (j['stem'] ?? '').toString(),
        options: (j['options'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
        chunkStartTime: j['chunk_start_time'] == null ? null : (j['chunk_start_time'] as num).toInt(),
        answered: (j['answered'] ?? false) as bool,
      );
}

/// The full quiz payload returned when status == ready.
class QuizView {
  final int quizId;
  final int episodeId;
  final String difficulty;
  final String agentFeedback; // LLM's study advice for this student
  final List<QuizQuestion> questions;
  final int answeredCount;

  QuizView({
    required this.quizId,
    required this.episodeId,
    required this.difficulty,
    required this.agentFeedback,
    required this.questions,
    required this.answeredCount,
  });

  factory QuizView.fromJson(Map<String, dynamic> j) {
    final qs = (j['questions'] as List<dynamic>?)?.map((e) => QuizQuestion.fromJson(e as Map<String, dynamic>)).toList() ?? const [];
    return QuizView(
      quizId: (j['quiz_id'] as num?)?.toInt() ?? 0,
      episodeId: (j['episode_id'] as num?)?.toInt() ?? 0,
      difficulty: (j['difficulty'] ?? '').toString(),
      agentFeedback: (j['agent_feedback'] ?? '').toString(),
      questions: qs,
      answeredCount: (j['answered_count'] as num?)?.toInt() ?? 0,
    );
  }
}

/// The parsed GET /ai-quiz response: status + (when ready) the view.
class QuizResponse {
  final QuizStatus status;
  final QuizView? quiz;

  QuizResponse({required this.status, this.quiz});

  factory QuizResponse.fromJson(Map<String, dynamic> j) {
    final s = (j['status'] ?? 'unavailable').toString();
    final status = {
      'ready': QuizStatus.ready,
      'generating': QuizStatus.generating,
    }[s] ??
        QuizStatus.unavailable;
    // The quiz payload is nested under 'quiz' when present.
    final quizJson = j['quiz'];
    return QuizResponse(
      status: status,
      quiz: quizJson is Map<String, dynamic> ? QuizView.fromJson(quizJson) : null,
    );
  }
}

/// The verdict returned by POST /ai-quiz/submit. Reveals correctness, the
/// correct answer, the explanation, and the video-jump time.
class QuizAnswerResult {
  final bool correct;
  final int? correctIndex; // choice: the right option index
  final String correctText; // fill: canonical answer(s)
  final String explanation;
  final int? chunkStartTime; // seconds, for "[跳转 12:38]"

  QuizAnswerResult({
    required this.correct,
    this.correctIndex,
    required this.correctText,
    required this.explanation,
    this.chunkStartTime,
  });

  factory QuizAnswerResult.fromJson(Map<String, dynamic> j) => QuizAnswerResult(
        correct: (j['correct'] ?? false) as bool,
        correctIndex: j['correct_index'] == null ? null : (j['correct_index'] as num).toInt(),
        correctText: (j['correct_text'] ?? '').toString(),
        explanation: (j['explanation'] ?? '').toString(),
        chunkStartTime: j['chunk_start_time'] == null ? null : (j['chunk_start_time'] as num).toInt(),
      );
}

/// A request to jump the player to a timestamp, popped back from the AI study
/// screen to the player. The player's _seekTo handles the actual seek.
class JumpRequest {
  final Duration target;
  JumpRequest(this.target);
}
