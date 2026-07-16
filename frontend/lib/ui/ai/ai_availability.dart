import '../../model/course.dart';

/// AI 学习入口的三态可用性。Phase 2 后端在 episode DTO 上回显了课程级 AI
/// 开关(AISummaryEnabled / AIQuizEnabled)和该 episode 是否有字幕(HasSubtitle),
/// 客户端据此统一决定按钮 / 图标的展示状态,避免在 course_detail 和 player 两处
/// 各写一遍相同的判断逻辑(那样容易漂移,文案也不一致)。
///
/// 取值优先级(见 [fromEpisode]):
///   1. 任一 AI 开关为 true 即视为"AI 已开启";
///   2. AI 已开启但该 episode 无字幕 → noSubtitle(AI 功能依赖字幕作为素材);
///   3. AI 已开启且有字幕 → enabled;
///   4. AI 都没开 → disabled。
enum AiAvailability {
  /// AI 已开启且有字幕:按钮亮,正常进入 AiStudyScreen。
  enabled,

  /// AI 已开启但该 episode 没有字幕:按钮灰,点击提示"本节没有字幕"。
  noSubtitle,

  /// 本课程未开启 AI:按钮灰,点击提示"本课程未开启 AI 学习"。
  disabled,
}

/// AI 学习入口可用性的共享判断 + 文案。course_detail 的"AI 学习"按钮和
/// player 顶栏的 AI 图标都走这一份逻辑,保证两处行为与提示文案完全一致。
class AiAvailabilityHelper {
  AiAvailabilityHelper._();

  /// 由 episode 的三字段派生可用性。
  /// 任一 AI 开关为 true 即视为"AI 已开启";再结合是否有字幕决定最终态。
  static AiAvailability fromEpisode(Episode ep) {
    final aiOn = ep.aiSummaryEnabled || ep.aiQuizEnabled;
    if (!aiOn) return AiAvailability.disabled;
    if (!ep.hasSubtitle) return AiAvailability.noSubtitle;
    return AiAvailability.enabled;
  }

  /// 按钮不可用时点击应弹出的提示文案。可用时返回 null(调用方正常跳转)。
  static String? tooltipFor(AiAvailability a) {
    switch (a) {
      case AiAvailability.enabled:
        return null;
      case AiAvailability.noSubtitle:
        return '本节没有字幕,AI 功能暂不可用';
      case AiAvailability.disabled:
        return '本课程未开启 AI 学习';
    }
  }
}
