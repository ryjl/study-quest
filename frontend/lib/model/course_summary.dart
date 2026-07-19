/// CourseSummary —— 课程总览(跨课时汇总的纯内容导览)。
///
/// 与 EpisodeSummary 区分:
///   - EpisodeSummary: 单节课的总结(headline/sections/methods/...),JSON。
///   - CourseSummary: 整门课的"讲什么 + 学习路径",自然语言文本(+ markdown/SVG)。
///
/// 字段对应后端 courseSummaryResponse(ai_handler.go):
///   - status: 'ready' | 'unavailable'(404 时);客户端不触发,无 'generating'。
///   - summary_text: agent 生成的自然语言(可能含 markdown 表格/SVG 围栏块)。
///   - generated_at: RFC3339(与 admin 统一)。
///   - episode_count_at_gen / current_episode_count: 陈旧检测——生成时快照的
///     "已总结课时数" vs 当前;差值 > 0 → 课程总览内容已变旧,前端诚实提示。
class CourseSummary {
  final String status;
  final String? summaryText;
  final String? modelUsed;
  final DateTime? generatedAt;
  final int episodeCountAtGen;
  final int currentEpisodeCount;

  const CourseSummary({
    required this.status,
    this.summaryText,
    this.modelUsed,
    this.generatedAt,
    this.episodeCountAtGen = 0,
    this.currentEpisodeCount = 0,
  });

  bool get isReady => status == 'ready';

  /// 内容已陈旧:生成后又有新课时总结补进来。学生端只读不触发,所以仅作诚实提示;
  /// admin 端会看到"建议重新生成"。
  bool get isStale =>
      isReady && currentEpisodeCount > episodeCountAtGen;

  /// 自生成后新增了多少节有 summary 的课时。isStale 为 false 时为 0。
  int get newEpisodesSinceGen =>
      isStale ? (currentEpisodeCount - episodeCountAtGen) : 0;

  factory CourseSummary.fromJson(Map<String, dynamic> j) {
    final rawGeneratedAt = j['generated_at'] as String?;
    DateTime? generatedAt;
    if (rawGeneratedAt != null && rawGeneratedAt.isNotEmpty) {
      generatedAt = DateTime.tryParse(rawGeneratedAt);
    }
    return CourseSummary(
      status: (j['status'] as String?) ?? 'unavailable',
      summaryText: j['summary_text'] as String?,
      modelUsed: j['model_used'] as String?,
      generatedAt: generatedAt,
      episodeCountAtGen: (j['episode_count_at_gen'] as num?)?.toInt() ?? 0,
      currentEpisodeCount: (j['current_episode_count'] as num?)?.toInt() ?? 0,
    );
  }
}
