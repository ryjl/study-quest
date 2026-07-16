// Phase C AI quiz models for the client. These mirror the backend's
// /ai-summary and /ai-quiz JSON shapes. The client never sees the correct
// answer until after submit (QuizQuestion has no answer field); the verdict
// comes back via QuizAnswerResult.

/// The AI-generated summary for an episode (GET /episodes/:id/ai-summary).
/// Rendered at the top of the AI study screen.
class EpisodeSummary {
  final String headline;
  // Phase F:按知识点分的小节,让总结有结构(知识点卡片)而非平铺要点。
  final List<SummarySection> sections;
  final List<String> keyPoints;
  // 具体方法/技巧/公式,便于速查。
  final List<String> methods;
  // 易错点/易混淆处,帮学生避坑。
  final List<String> commonMistakes;
  final List<String> concepts;
  final String takeaway;
  // Phase 2:课前探险问题,和 summary 同一次 LLM 生成产出(零额外调用)。
  // 引导孩子带着问题进入视频。老数据 / 生成失败时为空,前端优雅降级不展示。
  final List<PreAdventurePrompt> preAdventure;

  EpisodeSummary({
    required this.headline,
    this.sections = const [],
    required this.keyPoints,
    this.methods = const [],
    this.commonMistakes = const [],
    required this.concepts,
    required this.takeaway,
    this.preAdventure = const [],
  });

  factory EpisodeSummary.fromJson(Map<String, dynamic> j) {
    final rawPre = j['pre_adventure'];
    final List<PreAdventurePrompt> pre = (rawPre is List<dynamic>)
        ? rawPre
            .whereType<Map<String, dynamic>>()
            .map(PreAdventurePrompt.fromJson)
            // 过滤掉 prompt 为空的脏数据,避免渲染出空白任务卡
            .where((p) => p.prompt.isNotEmpty)
            .toList()
        : const [];
    final rawSections = j['sections'];
    final List<SummarySection> secs = (rawSections is List<dynamic>)
        ? rawSections
            .whereType<Map<String, dynamic>>()
            .map(SummarySection.fromJson)
            .where((s) => s.title.isNotEmpty)
            .toList()
        : const [];
    return EpisodeSummary(
      headline: (j['headline'] ?? '').toString(),
      sections: secs,
      keyPoints: (j['key_points'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
      methods: (j['methods'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
      commonMistakes: (j['common_mistakes'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
      concepts: (j['concepts'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
      takeaway: (j['takeaway'] ?? '').toString(),
      preAdventure: pre,
    );
  }

  bool get isEmpty =>
      headline.isEmpty &&
      keyPoints.isEmpty &&
      concepts.isEmpty &&
      sections.isEmpty &&
      methods.isEmpty &&
      commonMistakes.isEmpty &&
      takeaway.isEmpty;
}

/// 一个知识点小节:title 是知识点名称,points 是该知识点的要点。
class SummarySection {
  final String title;
  final List<String> points;

  const SummarySection({required this.title, this.points = const []});

  factory SummarySection.fromJson(Map<String, dynamic> j) => SummarySection(
        title: (j['title'] ?? '').toString(),
        points: (j['points'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
      );
}

/// 一道课前探险问题。
///   - prompt:问题本身(开放式,激发好奇心),展示在任务卡上。
///   - hint:一句不剧透答案的思考方向提示(Phase 2 暂未单独展示,留给后续 UI)。
class PreAdventurePrompt {
  final String prompt;
  final String hint;

  const PreAdventurePrompt({required this.prompt, this.hint = ''});

  factory PreAdventurePrompt.fromJson(Map<String, dynamic> j) => PreAdventurePrompt(
        prompt: (j['prompt'] ?? '').toString(),
        hint: (j['hint'] ?? '').toString(),
      );
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
  // has_jump:这题是否对应明确的视频片段。仅 has_jump=true 的题渲染"跳转视频"按钮,
  // 综合题(贯穿全文无单一锚点)为 false 不给跳转。老数据缺省 false。
  final bool hasJump;

  // ── 仅在 quiz 已交卷(submitted)时由后端回填 ──
  // 未交卷时为零值(后端 omitempty 不下发),不泄露答案。交卷后重进页面能 review。
  final int? userAnswerIndex; // choice: 学生当时选的索引
  final bool correct; // 这题对不对(交卷后才有意义)
  final int? correctIndex; // choice: 正确选项索引
  final String correctText; // fill: 标准答案
  final String explanation; // 解析

  QuizQuestion({
    required this.id,
    required this.type,
    required this.stem,
    required this.options,
    this.chunkStartTime,
    required this.answered,
    this.hasJump = false,
    this.userAnswerIndex,
    this.correct = false,
    this.correctIndex,
    this.correctText = '',
    this.explanation = '',
  });

  bool get isFill => type == 'fill';

  factory QuizQuestion.fromJson(Map<String, dynamic> j) => QuizQuestion(
        id: (j['id'] as num).toInt(),
        type: (j['type'] ?? 'choice').toString(),
        stem: (j['stem'] ?? '').toString(),
        options: (j['options'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
        chunkStartTime: j['chunk_start_time'] == null ? null : (j['chunk_start_time'] as num).toInt(),
        answered: (j['answered'] ?? false) as bool,
        hasJump: (j['has_jump'] ?? false) as bool,
        userAnswerIndex: j['user_answer_index'] == null ? null : (j['user_answer_index'] as num).toInt(),
        correct: (j['correct'] ?? false) as bool,
        correctIndex: j['correct_index'] == null ? null : (j['correct_index'] as num).toInt(),
        correctText: (j['correct_text'] ?? '').toString(),
        explanation: (j['explanation'] ?? '').toString(),
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
  // submitted:该 quiz 是否已交卷(quiz.SubmittedAt 非空)。Phase B 统一提交后,
  // 前端据此切到"只读看结果"状态:已交卷就锁定所有题、逐题展示对错。
  final bool submitted;

  QuizView({
    required this.quizId,
    required this.episodeId,
    required this.difficulty,
    required this.agentFeedback,
    required this.questions,
    required this.answeredCount,
    this.submitted = false,
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
      submitted: (j['submitted'] ?? false) as bool,
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
  // questionId:后端在 submit-all 返回里带上,前端按 id 映射结果到题目,
  // 不依赖返回顺序与题序一致(位置映射在并发删题/DB 排序漂移时会错位)。
  final int? questionId;
  final bool correct;
  final int? correctIndex; // choice: the right option index
  final String correctText; // fill: canonical answer(s)
  final String explanation;
  final int? chunkStartTime; // seconds, for "[跳转 12:38]"

  QuizAnswerResult({
    this.questionId,
    required this.correct,
    this.correctIndex,
    required this.correctText,
    required this.explanation,
    this.chunkStartTime,
  });

  factory QuizAnswerResult.fromJson(Map<String, dynamic> j) => QuizAnswerResult(
        questionId: j['question_id'] == null ? null : (j['question_id'] as num).toInt(),
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

// --- Phase 3: archived quiz history (read-only) ---
//
// Mirrors the backend QuizHistoryView. A history quiz is FULLY revealed — the
// correct answer is shown for every question because the student can only
// review, not answer (no submit path). This is the one place the client model
// carries the correct answer.

/// One question in an archived (history) quiz, WITH the correct answer revealed.
class ArchivedQuizQuestion {
  final int id;
  final String type; // "choice" | "fill"
  final String stem;
  final List<String> options; // choice only
  final int? correctIndex; // choice: the right option index
  final String correctText; // fill: canonical answer(s)
  final String explanation;
  final int? chunkStartTime; // seconds, for "[跳转 12:38]"; null if synthetic
  final bool wrong; // true if the student answered this wrong at least once
  // 历史 review 回放学生当时的作答:选择题给索引(0-based),填空题后端目前不回放原文。
  // userIndex=null 表示当时没作答(漏答)。
  final int? userIndex;
  final String userText;
  // hasJump:历史题是否可跳转视频(透传 question.HasJump)。仅 has_jump=true 才给按钮。
  final bool hasJump;

  ArchivedQuizQuestion({
    required this.id,
    required this.type,
    required this.stem,
    required this.options,
    this.correctIndex,
    required this.correctText,
    required this.explanation,
    this.chunkStartTime,
    required this.wrong,
    this.userIndex,
    this.userText = '',
    this.hasJump = false,
  });

  bool get isFill => type == 'fill';

  factory ArchivedQuizQuestion.fromJson(Map<String, dynamic> j) => ArchivedQuizQuestion(
        id: (j['id'] as num).toInt(),
        type: (j['type'] ?? 'choice').toString(),
        stem: (j['stem'] ?? '').toString(),
        options: (j['options'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
        correctIndex: j['correct_index'] == null ? null : (j['correct_index'] as num).toInt(),
        correctText: (j['correct_text'] ?? '').toString(),
        explanation: (j['explanation'] ?? '').toString(),
        chunkStartTime: j['chunk_start_time'] == null ? null : (j['chunk_start_time'] as num).toInt(),
        wrong: (j['wrong'] ?? false) as bool,
        userIndex: j['user_index'] == null ? null : (j['user_index'] as num).toInt(),
        userText: (j['user_text'] ?? '').toString(),
        hasJump: (j['has_jump'] ?? false) as bool,
      );
}

/// One archived (superseded) quiz, fully read-only, for the history panel.
class ArchivedQuizView {
  final int quizId;
  final int episodeId;
  final String generatedAt; // when the set was generated (formatted)
  final String archivedAt; // when it was superseded (formatted)
  final int questionCount;
  final int wrongCount; // answers with Correct=false against this quiz
  final String agentFeedback;
  final List<ArchivedQuizQuestion> questions;

  ArchivedQuizView({
    required this.quizId,
    required this.episodeId,
    required this.generatedAt,
    required this.archivedAt,
    required this.questionCount,
    required this.wrongCount,
    required this.agentFeedback,
    required this.questions,
  });

  factory ArchivedQuizView.fromJson(Map<String, dynamic> j) {
    final qs = (j['questions'] as List<dynamic>?)
            ?.map((e) => ArchivedQuizQuestion.fromJson(e as Map<String, dynamic>))
            .toList() ??
        const [];
    return ArchivedQuizView(
      quizId: (j['quiz_id'] as num?)?.toInt() ?? 0,
      episodeId: (j['episode_id'] as num?)?.toInt() ?? 0,
      generatedAt: (j['generated_at'] ?? '').toString(),
      archivedAt: (j['archived_at'] ?? '').toString(),
      questionCount: (j['question_count'] as num?)?.toInt() ?? 0,
      wrongCount: (j['wrong_count'] as num?)?.toInt() ?? 0,
      agentFeedback: (j['agent_feedback'] ?? '').toString(),
      questions: qs,
    );
  }
}
