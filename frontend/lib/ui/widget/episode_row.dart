import 'package:flutter/material.dart';

import '../../model/course.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../ai/ai_availability.dart';
import '../screen/ai_study_screen.dart';
import 'focus_button.dart';

/// One episode row in the course-detail timeline.
///
/// Extracted verbatim from `CourseDetailScreenState._buildEpisodeRow` (+
/// `_buildThumbnailPlaceholder`, which only this row used) so the detail screen
/// stops hosting ~320 lines of presentational code with no direct dependency on
/// the screen's state beyond a play callback. Mirrors the `HelperPanel`
/// extraction pattern: pure consumer of props, side effects wired via
/// [onPlay]. The screen still owns episode selection / player navigation.
///
/// The row is self-contained: duration formatting, resume-percent math, and the
/// AI-study availability check are all computed locally from the props.
/// Tapping the row body calls [onPlay]; the inline AI-study button navigates
/// itself (it needs [activeUserId] + the episode, both passed as props) and
/// shows a SnackBar when AI is unavailable.
class EpisodeRow extends StatelessWidget {
  const EpisodeRow({
    super.key,
    required this.ep,
    required this.isCompleted,
    required this.activeUserId,
    this.resumeSeconds = 0,
    this.totalSeconds = 0,
    required this.onPlay,
  });

  final Episode ep;
  final bool isCompleted;
  final int activeUserId;
  final int resumeSeconds;
  final int totalSeconds;
  final VoidCallback onPlay;

  static String _fmt(int s) {
    if (s <= 0) return '--:--';
    final h = s ~/ 3600;
    final m = (s % 3600) ~/ 60;
    final sec = s % 60;
    return h > 0
        ? '$h:${m.toString().padLeft(2, '0')}:${sec.toString().padLeft(2, '0')}'
        : '$m:${sec.toString().padLeft(2, '0')}';
  }

  Widget _buildThumbnailPlaceholder(BuildContext context) {
    final colors = context.colors;
    return Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          colors: [colors.slate400, colors.textMuted],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ),
      ),
      child: const Icon(
        Icons.video_file_rounded,
        color: Colors.white54,
        size: 24,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final durationLabel = _fmt(totalSeconds);
    final hasResume = resumeSeconds > 5 && !isCompleted;
    // Percentage watched (clamped, only meaningful when total is known).
    final resumePct = (hasResume && totalSeconds > 0)
        ? (resumeSeconds * 100 ~/ totalSeconds).clamp(0, 99)
        : 0;
    // AI 学习按钮的三态由 episode 上的 AI 开关 + 字幕标志决定。按钮始终展示
    // (保持入口可见),但不可用时变灰且点击只弹提示。
    final aiAvailability = AiAvailabilityHelper.fromEpisode(ep);

    // Locked episodes render as a greyed-out row with a lock affordance and
    // refuse to open the player — the unlock schedule (drip) keeps them
    // invisible to play-info anyway, so this just stops the tap from producing
    // a confusing 403.
    if (ep.locked) {
      return Padding(
        padding: const EdgeInsets.only(bottom: 12.0),
        child: FocusButton(
          padding: const EdgeInsets.all(16.0),
          borderRadius: 20,
          borderColor: colors.borderMuted,
          onPressed: () {
            ScaffoldMessenger.of(context).showSnackBar(
              const SnackBar(
                content: Text('🔒 这一节还没解锁，耐心等待吧～'),
                duration: Duration(seconds: 2),
              ),
            );
          },
          child: Row(
            children: [
              Container(
                width: 120,
                height: 68,
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(12),
                  color: colors.cardColor,
                  border: Border.all(color: colors.borderMuted, width: 1.5),
                ),
                child: Center(
                  child: Icon(Icons.lock_outline_rounded, color: colors.slate400, size: 26),
                ),
              ),
              const SizedBox(width: 16),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      ep.title,
                      style: TextStyle(
                        fontWeight: FontWeight.w800,
                        fontSize: 16,
                        color: colors.slate400,
                      ),
                    ),
                    const SizedBox(height: 8),
                    Row(
                      children: [
                        Icon(Icons.lock_clock_outlined, size: 12, color: colors.slate400),
                        const SizedBox(width: 4),
                        Text(
                          '等待解锁',
                          style: TextStyle(fontSize: 11, color: colors.slate400, fontWeight: FontWeight.bold),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      );
    }

    return Padding(
      padding: const EdgeInsets.only(bottom: 12.0),
      child: FocusButton(
        padding: const EdgeInsets.all(16.0),
        borderRadius: 20,
        borderColor: colors.borderMuted,
        onPressed: onPlay,
        child: Row(
          children: [
            // Video Thumbnail
            Container(
              width: 120,
              height: 68,
              decoration: BoxDecoration(
                borderRadius: BorderRadius.circular(12),
                color: colors.cardColor,
                border: Border.all(
                  color: isCompleted ? const Color(0xFFA7F3D0) : colors.borderMuted,
                  width: 1.5,
                ),
              ),
              child: ClipRRect(
                borderRadius: BorderRadius.circular(10.5),
                child: Stack(
                  fit: StackFit.expand,
                  children: [
                    ep.coverUrl.isNotEmpty
                        ? Image.network(
                            ApiService.absoluteUrl(ep.coverUrl),
                            fit: BoxFit.cover,
                            errorBuilder: (context, error, stackTrace) =>
                                _buildThumbnailPlaceholder(context),
                          )
                        : _buildThumbnailPlaceholder(context),

                    // Semi-transparent dark overlay for play button visibility
                    Container(
                      color: Colors.black.withValues(alpha: 0.15),
                    ),

                    // Status Circle Overlay (Play / Complete check) in the center
                    Center(
                      child: Container(
                        width: 32,
                        height: 32,
                        decoration: BoxDecoration(
                          color: isCompleted
                              ? const Color(0xFFECFDF5).withValues(alpha: 0.9)
                              : Colors.white.withValues(alpha: 0.9),
                          shape: BoxShape.circle,
                          boxShadow: [
                            BoxShadow(
                              color: Colors.black.withValues(alpha: 0.1),
                              blurRadius: 4,
                              offset: const Offset(0, 2),
                            )
                          ],
                        ),
                        child: Icon(
                          isCompleted ? Icons.check_rounded : Icons.play_arrow_rounded,
                          color: isCompleted ? AppTheme.accentGreen : AppTheme.primaryColor,
                          size: 20,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ),
            const SizedBox(width: 16),

            // Details info
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    ep.title,
                    style: TextStyle(
                      fontWeight: FontWeight.w800,
                      fontSize: 16,
                      color: isCompleted ? colors.textMuted : colors.textWhite,
                    ),
                  ),
                  const SizedBox(height: 8),

                  // Resource and Metadata row — Wrap so tags reflow instead of
                  // overflowing on narrow portrait widths.
                  Wrap(
                    spacing: 10,
                    runSpacing: 6,
                    children: [
                      // Duration tag
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                        decoration: BoxDecoration(
                          color: colors.cardColor,
                          borderRadius: BorderRadius.circular(8),
                          border: Border.all(color: colors.borderMuted),
                        ),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Icon(Icons.watch_later_outlined, size: 12, color: colors.textMuted),
                            const SizedBox(width: 4),
                            Text(
                              durationLabel,
                              style: TextStyle(fontSize: 11, color: colors.textMuted, fontWeight: FontWeight.bold),
                            ),
                          ],
                        ),
                      ),

                      // Purple AI Study Button — opens the AI study page (summary +
                      // practice). 按钮始终展示,但可用性三态化:enabled 才真正跳转,
                      // 否则弹 SnackBar 提示原因。
                      Builder(builder: (btnContext) {
                        final enabled = aiAvailability == AiAvailability.enabled;
                        final bgColor = enabled ? const Color(0xFFF5F3FF) : colors.cardColor;
                        final borderColor = enabled ? const Color(0xFFDDD6FE) : colors.borderMuted;
                        final iconColor = enabled ? AppTheme.violet500 : colors.textMuted;
                        final textColor = enabled ? const Color(0xFF6D28D9) : colors.textMuted;
                        return GestureDetector(
                          onTap: () {
                            if (!enabled) {
                              ScaffoldMessenger.of(btnContext).showSnackBar(
                                SnackBar(
                                  content: Text(AiAvailabilityHelper.tooltipFor(aiAvailability)!),
                                  duration: const Duration(seconds: 2),
                                ),
                              );
                              return;
                            }
                            Navigator.of(context).push(
                              MaterialPageRoute(
                                builder: (context) => AiStudyScreen(
                                  activeUserId: activeUserId,
                                  episode: ep,
                                ),
                              ),
                            );
                          },
                          child: Container(
                            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                            decoration: BoxDecoration(
                              color: bgColor,
                              borderRadius: BorderRadius.circular(8),
                              border: Border.all(color: borderColor),
                            ),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Icon(Icons.auto_awesome_rounded, size: 12, color: iconColor),
                                const SizedBox(width: 4),
                                Text(
                                  'AI 学习',
                                  style: TextStyle(fontSize: 11, color: textColor, fontWeight: FontWeight.bold),
                                ),
                              ],
                            ),
                          ),
                        );
                      }),
                    ],
                  ),
                  // Resume progress indicator: shows how far the user got on
                  // this episode + a thin progress bar, so they can see at a
                  // glance where playback will resume.
                  if (hasResume) ...[
                    const SizedBox(height: 10),
                    Row(
                      children: [
                        const Icon(Icons.history_rounded,
                            size: 12, color: AppTheme.primaryColor),
                        const SizedBox(width: 4),
                        Text(
                          '已观看 $resumePct%  ·  续播 ${_fmt(resumeSeconds)}',
                          style: const TextStyle(
                            fontSize: 11,
                            color: AppTheme.primaryColor,
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 6),
                    ClipRRect(
                      borderRadius: BorderRadius.circular(4),
                      child: LinearProgressIndicator(
                        value: resumePct / 100,
                        minHeight: 4,
                        backgroundColor: colors.borderMuted,
                        valueColor:
                            const AlwaysStoppedAnimation<Color>(AppTheme.primaryColor),
                      ),
                    ),
                  ],
                ],
              ),
            ),

            // Caret right
            Icon(Icons.chevron_right_rounded, color: colors.slate400, size: 24),
          ],
        ),
      ),
    );
  }
}
