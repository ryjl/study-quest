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
      // FocusScope(独立焦点域)+ ClampingScrollPhysics 解决两个 D-pad 焦点问题:
      //  1. 之前用 BouncingScrollPhysics(iOS 弹性),D-pad 垂直键被 Scrollable
      //     接管去滚动,而不是在 FocusButton 间跳。ClampingScrollPhysics 让方向键
      //     优先给焦点遍历。
      //  2. 之前用 FocusTraversalGroup(普通 group,非 scope),panel 和视频区的
      //     FocusButton 在同一 traversal scope,按 ▲▼ 到边界时几何算法把视频区控制行
      //     的 FocusButton 当候选 → 焦点"跳走丢失"。FocusScope 创建独立 FocusScopeNode,
      //     directionalTraversalEdgeBehavior 默认 stop,panel 内 ▲▼ 到边界停住不溢出。
      // 跨区(视频区 → panel)由播放器顶层 FocusScope(parentScope)处理:video 按 →
      // 到边界,parentScope 在顶层 scope 找到 panel 边界 FocusButton,跨进 panel。
      // 触屏拖动滚动不受影响(ClampingScrollPhysics 本身就是触屏滚动用)。
      child: FocusScope(
        child: SingleChildScrollView(
          physics: const ClampingScrollPhysics(),
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
                // 右上角关闭按钮已移除:panel 显隐统一由播放器的全屏按钮控制,
                // 不再给用户多个关闭入口(减少心智负担)。
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
      ),
    );
  }

  // AI 学习入口卡片:常驻显示(不受 _controlsVisible 影响)。AI 开 + 有字幕时
  // 可点击进入 AiStudyScreen;不可用时置灰 + 点击弹 SnackBar 提示原因。
  // disableAiTab=true 时整体不渲染(AI 跳转 push 出来的播放器不能再进 AI 页)。
  //
  // 用 FocusButton 替代原裸 GestureDetector —— 让 D-pad 可聚焦、Enter 进入,
  // 顺带继承 GlassPanel 的焦点发光环。紫色渐变内层保留,靠 FocusButton 的
  // 透明 baseColor + borderColor 让发光环正确显现。
  Widget _buildAiStudyEntry(BuildContext context) {
    if (disableAiTab) return const SizedBox.shrink();
    final availability = AiAvailabilityHelper.fromEpisode(episode);
    final enabled = availability == AiAvailability.enabled;
    return FocusButton(
      onPressed: () {
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
      borderRadius: 14,
      baseColor: Colors.transparent,
      borderColor: Colors.transparent,
      padding: EdgeInsets.zero,
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
