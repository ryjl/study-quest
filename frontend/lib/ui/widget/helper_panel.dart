import 'package:flutter/material.dart';

import '../../model/course.dart';
import '../../model/quiz.dart';
import '../../theme.dart';
import '../ai/ai_availability.dart';
import 'focus_button.dart';

/// Right-hand "随堂助手" panel shown alongside the player.
///
/// Extracted verbatim from `PlayerScreenState`'s helper-panel subtree
/// (`_buildHelperPanel` + `_buildAttachmentsSection` + `_buildPreAdventureSection`
/// + `_buildAiStudyEntry` + `_placeholderTile` + `_taskCard`) so the player
/// screen stops hosting ~300 lines of presentational code that has no direct
/// dependency on the media_kit [Player].
///
/// The widget is a pure consumer of props; the player state still owns the
/// `_attachments` / `_loadingExtras` / `_summary` source data and the side
/// effects (pause-before-AI, panel close also restores fullscreen, opening
/// attachments). Side effects are wired via the [onClosePanel],
/// [onOpenAttachment], and [onEnterAiStudy] callbacks.
class HelperPanel extends StatelessWidget {
  final Episode episode;
  final List<Attachment> attachments;
  final bool loadingExtras;
  final EpisodeSummary? summary;
  final List<String> preAdventureTasks;
  final bool disableAiTab;
  final bool tvModeActive;
  final VoidCallback onClosePanel;
  final void Function(Attachment attachment) onOpenAttachment;
  final VoidCallback onEnterAiStudy;

  const HelperPanel({
    super.key,
    required this.episode,
    required this.attachments,
    required this.loadingExtras,
    required this.summary,
    required this.preAdventureTasks,
    required this.disableAiTab,
    required this.tvModeActive,
    required this.onClosePanel,
    required this.onOpenAttachment,
    required this.onEnterAiStudy,
  });

  double _tvScaled(double base) {
    final textScale = tvModeActive ? 1.3 : 1.0;
    return (base * textScale).roundToDouble();
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 360,
      decoration: const BoxDecoration(
        color: Colors.white,
        border: Border(left: BorderSide(color: AppTheme.borderMuted, width: 2)),
      ),
      child: SingleChildScrollView(
        physics: const BouncingScrollPhysics(),
        padding: const EdgeInsets.all(24),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Title bar
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.all(8),
                  decoration: const BoxDecoration(
                    color: AppTheme.blue100,
                    shape: BoxShape.circle,
                  ),
                  child: const Icon(Icons.psychology_rounded,
                      color: AppTheme.blue600, size: 24),
                ),
                const SizedBox(width: 12),
                Text('随堂助手',
                    style: TextStyle(
                        fontSize: _tvScaled(20),
                        fontWeight: FontWeight.w900,
                        color: AppTheme.textWhite)),
                const Spacer(),
                IconButton(
                  icon: const Icon(Icons.close_rounded, color: AppTheme.slate400),
                  onPressed: onClosePanel,
                ),
              ],
            ),
            const SizedBox(height: 24),

            // Episode title context
            Text(episode.title,
                style: TextStyle(
                    fontWeight: FontWeight.w900,
                    fontSize: _tvScaled(15),
                    color: AppTheme.textWhite)),
            const SizedBox(height: 28),

            // Attachments section
            Text('附属资料',
                style: TextStyle(
                    fontSize: _tvScaled(12),
                    fontWeight: FontWeight.w900,
                    letterSpacing: 1.5,
                    color: AppTheme.textMuted)),
            const SizedBox(height: 12),
            _buildAttachmentsSection(),
            const SizedBox(height: 28),

            // Pre-adventure tasks
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text('本节探索任务',
                    style: TextStyle(
                        fontSize: _tvScaled(12),
                        fontWeight: FontWeight.w900,
                        letterSpacing: 1.5,
                        color: AppTheme.textMuted)),
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  decoration: BoxDecoration(
                    color: AppTheme.blue100,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Text('带着问题看',
                      style: TextStyle(
                          fontSize: _tvScaled(10),
                          fontWeight: FontWeight.bold,
                          color: AppTheme.blue600)),
                ),
              ],
            ),
            const SizedBox(height: 12),
            _buildPreAdventureSection(),
            const SizedBox(height: 28),

            // AI 学习入口 —— 常驻 helper panel,不随顶栏自动隐藏。比顶栏那个
            // 会消失的图标更可发现。三态 gating 与顶栏一致(走同一 helper)。
            _buildAiStudyEntry(context),
          ],
        ),
      ),
    );
  }

  // AI 学习入口卡片:常驻显示(不受 _controlsVisible 影响)。AI 开 + 有字幕时
  // 可点击进入 AiStudyScreen;不可用时置灰 + 点击弹 SnackBar 提示原因。
  // disableAiTab=true 时整体不渲染(AI 跳转 push 出来的播放器不能再进 AI 页)。
  Widget _buildAiStudyEntry(BuildContext context) {
    if (disableAiTab) return const SizedBox.shrink();
    final availability = AiAvailabilityHelper.fromEpisode(episode);
    final enabled = availability == AiAvailability.enabled;
    return GestureDetector(
      onTap: () {
        if (!enabled) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
              content: Text(AiAvailabilityHelper.tooltipFor(availability)!),
              duration: const Duration(seconds: 2),
            ),
          );
          return;
        }
        onEnterAiStudy();
      },
      child: Opacity(
        opacity: enabled ? 1.0 : 0.5,
        child: Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 14),
          decoration: BoxDecoration(
            gradient: const LinearGradient(
              colors: [Color(0xFF6366F1), AppTheme.violet500],
              begin: Alignment.centerLeft,
              end: Alignment.centerRight,
            ),
            borderRadius: BorderRadius.circular(14),
          ),
          child: Row(
            children: [
              Icon(Icons.auto_awesome_rounded,
                  color: Colors.white, size: _tvScaled(20)),
              const SizedBox(width: 10),
              Expanded(
                child: Text('AI 学习',
                    style: TextStyle(
                        color: Colors.white,
                        fontWeight: FontWeight.w800,
                        fontSize: _tvScaled(14))),
              ),
              Icon(Icons.chevron_right_rounded,
                  color: Colors.white70, size: _tvScaled(20)),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildAttachmentsSection() {
    if (loadingExtras) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 8),
        child: SizedBox(
          width: 20,
          height: 20,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      );
    }
    if (attachments.isEmpty) {
      return _placeholderTile(
        icon: Icons.picture_as_pdf_outlined,
        title: '暂无配套讲义',
        accent: AppTheme.accentOrange,
      );
    }
    return Column(
      children: attachments.map((att) {
        final isPdf = att.isPdf;
        final accent =
            isPdf ? AppTheme.accentOrange : AppTheme.violet500;
        return Padding(
          padding: const EdgeInsets.only(bottom: 10),
          child: FocusButton(
            onPressed: () => onOpenAttachment(att),
            borderRadius: 14,
            baseColor: isPdf
                ? const Color(0xFFFFF7ED)
                : const Color(0xFFF5F3FF),
            borderColor:
                isPdf ? const Color(0xFFFED7AA) : const Color(0xFFDDD6FE),
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
            child: Row(
              children: [
                Icon(isPdf ? Icons.picture_as_pdf_rounded : Icons.attach_file,
                    color: accent, size: _tvScaled(18)),
                const SizedBox(width: 10),
                Expanded(
                  child: Text(att.fileName.isEmpty ? '配套资料' : att.fileName,
                      style: TextStyle(
                          color: accent,
                          fontWeight: FontWeight.w800,
                          fontSize: _tvScaled(13)),
                      overflow: TextOverflow.ellipsis),
                ),
                Icon(Icons.chevron_right_rounded,
                    color: AppTheme.slate400, size: _tvScaled(18)),
              ],
            ),
          ),
        );
      }).toList(),
    );
  }

  Widget _buildPreAdventureSection() {
    // Phase 2:数据源切到 /ai-summary 的 pre_adventure(课程详情页传进来的
    // preAdventureTasks 也来自 summary)。优先用显式入参(列表页已缓存),
    // 否则取本屏 lazy 加载的 _summary.preAdventure。
    final tasks = preAdventureTasks.isNotEmpty
        ? preAdventureTasks
        : (summary?.preAdventure.map((p) => p.prompt).toList() ?? const []);
    if (tasks.isEmpty) {
      return _placeholderTile(
        icon: Icons.casino_outlined,
        title: '本节暂无探索任务',
        accent: const Color(0xFF3B82F6),
      );
    }
    return Column(
      children: List.generate(tasks.length, (i) {
        return Padding(
          padding: const EdgeInsets.only(bottom: 10),
          child: _taskCard(i + 1, tasks[i]),
        );
      }),
    );
  }

  Widget _placeholderTile(
      {required IconData icon,
      required String title,
      required Color accent}) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 12),
      decoration: BoxDecoration(
        color: AppTheme.backgroundColor,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: AppTheme.borderMuted),
      ),
      child: Row(
        children: [
          Icon(icon, color: accent.withValues(alpha: 0.5), size: _tvScaled(18)),
          const SizedBox(width: 10),
          Text(title,
              style: TextStyle(
                  color: AppTheme.textMuted,
                  fontWeight: FontWeight.bold,
                  fontSize: _tvScaled(13))),
        ],
      ),
    );
  }

  Widget _taskCard(int index, String text) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.backgroundColor,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppTheme.borderMuted),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 24,
            height: 24,
            alignment: Alignment.center,
            decoration: const BoxDecoration(
              color: AppTheme.blue100,
              shape: BoxShape.circle,
            ),
            child: Text('$index',
                style: TextStyle(
                    fontWeight: FontWeight.w900,
                    color: AppTheme.blue600,
                    fontSize: _tvScaled(11))),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(text,
                style: TextStyle(
                    color: Color(0xFF475569),
                    fontWeight: FontWeight.bold,
                    fontSize: _tvScaled(13),
                    height: 1.4)),
          ),
        ],
      ),
    );
  }
}
