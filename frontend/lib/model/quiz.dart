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
/// done = 有历史归档(交卷/换题归档过)但无 active quiz:不自动出新题,前端渲染
/// 「已完成、点重新生成」入口。区别于 unavailable(AI 未开/无 chunks)。
/// cooling = 连续多次生成失败已熔断,后端拒绝自动重试(避免反复入队烧 token)。
/// 前端提示「AI 多次生成失败,已暂停,请联系老师或稍后重试」,并提供手动重试入口。
enum QuizStatus { ready, generating, unavailable, done, cooling }

/// One question as served to the client. Deliberately has NO answer field —
/// the correct answer is only revealed after submit (QuizAnswerResult).
class QuizQuestion {
  final int id;
  final String type; // "choice" | "fill" | "multi_choice"
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
  // 填空题学生当时填的原文(交卷后回填,用于"你填的 X"回放)。后端 Answer.UserAnswerText。
  final String userAnswerText; // fill: 学生当时填的
  final bool correct; // 这题对不对(交卷后才有意义)
  final int? correctIndex; // choice: 正确选项索引
  final String correctText; // fill: 标准答案
  final String explanation; // 解析
  // ── multi_choice 专用(交卷后回填)──
  // correctIndices:多选题的正确答案索引集合(交卷后后端揭示);单选/填空为 const []。
  // userAnswerIndices:学生当时选的索引集合(重进已交卷卷子时回填,用于错项红框高亮)。
  // partial:多选题"部分对"(漏选但没多选错项)。correct=true 表示全对;两者都 false 表示错。
  final List<int> correctIndices;
  final List<int> userAnswerIndices;
  final bool partial;
  // missedCount/extraCount:多选题部分对/错时回填,供横幅显示"漏选 X / 多选 Y"。
  final int missedCount;
  final int extraCount;

  QuizQuestion({
    required this.id,
    required this.type,
    required this.stem,
    required this.options,
    this.chunkStartTime,
    required this.answered,
    this.hasJump = false,
    this.userAnswerIndex,
    this.userAnswerText = '',
    this.correct = false,
    this.correctIndex,
    this.correctText = '',
    this.explanation = '',
    this.correctIndices = const [],
    this.userAnswerIndices = const [],
    this.partial = false,
    this.missedCount = 0,
    this.extraCount = 0,
  });

  bool get isFill => type == 'fill';

  // multi_choice:多选题(可选多个答案,后端按集合判分 + partial 部分对)。
  bool get isMultiChoice => type == 'multi_choice';

  factory QuizQuestion.fromJson(Map<String, dynamic> j) => QuizQuestion(
        id: (j['id'] as num).toInt(),
        type: (j['type'] ?? 'choice').toString(),
        stem: (j['stem'] ?? '').toString(),
        options: (j['options'] as List<dynamic>?)?.map((e) => e.toString()).toList() ?? const [],
        chunkStartTime: j['chunk_start_time'] == null ? null : (j['chunk_start_time'] as num).toInt(),
        answered: (j['answered'] ?? false) as bool,
        hasJump: (j['has_jump'] ?? false) as bool,
        userAnswerIndex: j['user_answer_index'] == null ? null : (j['user_answer_index'] as num).toInt(),
        userAnswerText: (j['user_answer_text'] ?? '').toString(),
        correct: (j['correct'] ?? false) as bool,
        correctIndex: j['correct_index'] == null ? null : (j['correct_index'] as num).toInt(),
        correctText: (j['correct_text'] ?? '').toString(),
        explanation: (j['explanation'] ?? '').toString(),
        // multi_choice 字段:用 ?.cast<int>() 防 null/脏类型。后端 omitempty 时为 const []。
        correctIndices: (j['correct_indices'] as List<dynamic>?)?.cast<int>() ?? const [],
        userAnswerIndices: (j['user_answer_indices'] as List<dynamic>?)?.cast<int>() ?? const [],
        partial: (j['partial'] ?? false) as bool,
        missedCount: (j['missed_count'] as num?)?.toInt() ?? 0,
        extraCount: (j['extra_count'] as num?)?.toInt() ?? 0,
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
      'done': QuizStatus.done,
      'cooling': QuizStatus.cooling,
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
  // multi_choice 专用:correctIndices 是正确答案索引集合(后端在交卷后揭示);
  // partial=true 表示部分对(漏选但没多选错项),correct=true 表示全对,两者都 false 表示错。
  // 单选/填空题这两字段保持默认值(const [] / false),不影响渲染。
  final List<int> correctIndices;
  final bool partial;
  // missedCount/extraCount:多选题部分对/错时,后端下发漏选数和错选数,供横幅显示
  // "漏选 2 项"等具体反馈。单选/填空恒 0。
  final int missedCount;
  final int extraCount;

  QuizAnswerResult({
    this.questionId,
    required this.correct,
    this.correctIndex,
    required this.correctText,
    required this.explanation,
    this.chunkStartTime,
    this.correctIndices = const [],
    this.partial = false,
    this.missedCount = 0,
    this.extraCount = 0,
  });

  factory QuizAnswerResult.fromJson(Map<String, dynamic> j) => QuizAnswerResult(
        questionId: j['question_id'] == null ? null : (j['question_id'] as num).toInt(),
        correct: (j['correct'] ?? false) as bool,
        correctIndex: j['correct_index'] == null ? null : (j['correct_index'] as num).toInt(),
        correctText: (j['correct_text'] ?? '').toString(),
        explanation: (j['explanation'] ?? '').toString(),
        chunkStartTime: j['chunk_start_time'] == null ? null : (j['chunk_start_time'] as num).toInt(),
        correctIndices: (j['correct_indices'] as List<dynamic>?)?.cast<int>() ?? const [],
        partial: (j['partial'] ?? false) as bool,
        missedCount: (j['missed_count'] as num?)?.toInt() ?? 0,
        extraCount: (j['extra_count'] as num?)?.toInt() ?? 0,
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
  final String type; // "choice" | "fill" | "multi_choice"
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
  // multi_choice 专用(历史 review):correctIndices 是正确答案索引集合;
  // userIndices 是学生当时选的索引集合(对应后端 user_indices)。单选/填空为 const []。
  final List<int> correctIndices;
  final List<int> userIndices;

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
    this.correctIndices = const [],
    this.userIndices = const [],
  });

  bool get isFill => type == 'fill';

  // multi_choice:历史多选题,按集合高亮正确项 + 学生错选项。
  bool get isMultiChoice => type == 'multi_choice';

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
        correctIndices: (j['correct_indices'] as List<dynamic>?)?.cast<int>() ?? const [],
        userIndices: (j['user_indices'] as List<dynamic>?)?.cast<int>() ?? const [],
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

// --- Phase C advice (agent 驱动的学习建议) ---
//
// 镜像后端 adviceResponse(GET /episodes|courses|subjects/:id/ai-advice)。
// 和 quiz 同一套 lazy 生成 + 轮询模式:首次访问触发后端入队 advice job,返回
// generating;ready 时带 advice 对象(advice agent 的自然语言 FinalText)。
// 区别于 quiz 的"单次出题"——advice 是跨知识点/跨课程读 mastery 后的综合分析。

/// advice 端点的状态,和 QuizStatus 同构。
/// cooling = 连续多次生成失败已熔断(语义同 QuizStatus.cooling)。
enum AdviceStatus { ready, generating, unavailable, cooling }

/// 一条学习建议(后端 model.StudyAdvice 的客户端镜像)。advice_text 是 agent 的
/// 自然语言输出(可能跨多个知识点),generated_at 是生成时间。
class StudyAdvice {
  final String scope; // episode | course | subject
  final int scopeId;
  final String adviceText;
  final String modelUsed;
  final String generatedAt; // ISO 时间串,展示时按需格式化

  const StudyAdvice({
    required this.scope,
    required this.scopeId,
    required this.adviceText,
    this.modelUsed = '',
    this.generatedAt = '',
  });

  factory StudyAdvice.fromJson(Map<String, dynamic> j) => StudyAdvice(
        scope: (j['scope'] ?? 'episode').toString(),
        scopeId: (j['scope_id'] as num?)?.toInt() ?? 0,
        adviceText: (j['advice_text'] ?? '').toString(),
        modelUsed: (j['model_used'] ?? '').toString(),
        generatedAt: (j['generated_at'] ?? '').toString(),
      );

  bool get isEmpty => adviceText.isEmpty;
}

/// GET /ai-advice 的响应:status + (ready 时)advice。和 QuizResponse 同构。
class AdviceResponse {
  final AdviceStatus status;
  final StudyAdvice? advice;

  const AdviceResponse({required this.status, this.advice});

  factory AdviceResponse.fromJson(Map<String, dynamic> j) {
    final s = (j['status'] ?? 'unavailable').toString();
    final status = {
          'ready': AdviceStatus.ready,
          'generating': AdviceStatus.generating,
          'cooling': AdviceStatus.cooling,
        }[s] ??
        AdviceStatus.unavailable;
    final a = j['advice'];
    return AdviceResponse(
      status: status,
      advice: a is Map<String, dynamic> ? StudyAdvice.fromJson(a) : null,
    );
  }
}
