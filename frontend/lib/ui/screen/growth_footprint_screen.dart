import 'package:flutter/material.dart';

import '../../model/badge.dart';
import '../../model/progress.dart';
import '../../service/api_service.dart';
import '../../theme.dart';
import '../responsive.dart';
import '../widget/badge_style.dart';
import '../widget/button_3d.dart';
import '../widget/glass_panel.dart';

/// 成长足迹 dashboard — gradient metric cards + timeline + honor wall.
///
/// Extracted verbatim from `_MainNavigationState._buildProgressScreen` (and its
/// sub-builders `_buildMetricCardsRow` / `_metricCard` / `_buildTimelinePanel` /
/// `_buildBadgeWallPanel` / `_buildBadgeItem` / `_buildTierProgress`). All of
/// these were pure presentational consumers of FutureBuilder data with no
/// dependency on `_MainNavigationState` private fields, so the move is a
/// straight cut/paste.
class GrowthFootprintScreen extends StatelessWidget {
  final int activeUserId;

  const GrowthFootprintScreen({super.key, required this.activeUserId});

  @override
  Widget build(BuildContext context) {
    return FutureBuilder(
      future: Future.wait([
        ApiService.fetchUserPoints(activeUserId),
        ApiService.fetchProgressOverview(activeUserId),
        ApiService.fetchPointsLedger(activeUserId, limit: 8),
        ApiService.fetchUserBadges(activeUserId),
      ]),
      builder: (context, AsyncSnapshot<List<dynamic>> snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const Center(child: CircularProgressIndicator(color: AppTheme.primaryColor));
        }
        if (snapshot.hasError) {
          return Center(
            child: Text(
              '获取足迹数据失败: ${snapshot.error}',
              style: const TextStyle(color: Colors.redAccent, fontWeight: FontWeight.bold),
            ),
          );
        }

        final userPoint = snapshot.data![0] as UserPoint;
        final progressList = snapshot.data![1] as List<UserProgress>;
        final rawLedger = snapshot.data![2] as List<PointsLedger>;
        final badges = snapshot.data![3] as List<BadgeStatus>;

        // Curate the timeline: show badge unlocks/ups FIRST (they're the
        // highlights), then at most 2 recent video-completion rows (so the
        // list isn't dominated by repetitive "完成视频" entries). Keep the
        // original recency order within each group.
        final badgeEntries = rawLedger.where((e) => e.reasonType == 'badge_unlocked').take(4).toList();
        final watchEntries = rawLedger.where((e) => e.reasonType == 'system_watch').take(2).toList();
        final ledger = [...badgeEntries, ...watchEntries];

        final completedCount = progressList.where((p) => p.isCompleted).length;

        // Real accumulated study minutes from watch-seconds across all episodes.
        final totalWatchSeconds =
            progressList.fold<int>(0, (sum, p) => sum + p.watchSeconds);
        final studyMinutes = (totalWatchSeconds / 60).round();
        // Star counting: each unlocked tier = 1 star. Multi-tier badges
        // contribute (tier+1) stars (e.g. reached tier 2 = 3 stars); single-
        // tier badges contribute 1. This is more granular and rewarding than
        // "unlocked X/Y badges" — a child sees progress on every tier clear.
        final unlockedStars = badges.fold<int>(0, (sum, b) => sum + (b.unlocked ? (b.tier + 1) : 0));
        final totalStars = badges.fold<int>(0, (sum, b) => sum + b.tierCount);

        return SingleChildScrollView(
          padding: portraitAwarePadding(context),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Header title
              Text(
                '成长足迹',
                style: TextStyle(
                  fontFamily: 'Quicksand',
                  fontSize: isPortrait(context) ? 22 : 28,
                  fontWeight: FontWeight.w900,
                  color: AppTheme.textWhite,
                ),
              ),
              const SizedBox(height: 6),
              Text(
                '看看你取得了多少成就！',
                style: TextStyle(
                  fontSize: 14,
                  color: AppTheme.textMuted,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: 32),

              // 3 Gradient Metric Cards — row on wide screens, stacked column on
              // narrow (portrait) screens so each card gets full width.
              _buildMetricCardsRow(
                context,
                userPoint: userPoint,
                studyMinutes: studyMinutes,
                completedCount: completedCount,
              ),
              SizedBox(height: isPortrait(context) ? 24 : 40),

              // Bottom Grid: Left Timeline + Right Badges.
              // Wide: side-by-side (timeline flex 2, badges flex 1).
              // Narrow (portrait): stacked vertically, each full width.
              Builder(builder: (context) {
                final timeline = _buildTimelinePanel(context, ledger);
                final badgeWall = _buildBadgeWallPanel(context, badges, unlockedStars, totalStars);
                if (isPortrait(context)) {
                  return Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      timeline,
                      const SizedBox(height: 24),
                      badgeWall,
                    ],
                  );
                }
                return Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(flex: 2, child: timeline),
                    const SizedBox(width: 28),
                    Expanded(flex: 1, child: badgeWall),
                  ],
                );
              }),
            ],
          ),
        );
      },
    );
  }

  /// The 3 gradient metric cards (积分 / 时长 / 通关). Wide: a single Row of 3
  /// Expanded cards. Narrow: stacked vertically, each full-width.
  Widget _buildMetricCardsRow(
    BuildContext context, {
    required UserPoint userPoint,
    required int studyMinutes,
    required int completedCount,
  }) {
    final compact = isPortrait(context);
    final gap = compact ? const SizedBox(height: 16) : const SizedBox(width: 24);

    Widget card1 = _metricCard(
      gradient: const [AppTheme.accentOrange, Color(0xFFFB923C)],
      shadowColor: AppTheme.accentOrange,
      label: '累计获得积分',
      labelColor: const Color(0xFFFFEDD5),
      icon: Icons.star_rounded,
      value: '${userPoint.totalEarnedPoints}',
      compact: compact,
    );
    Widget card2 = _metricCard(
      gradient: const [Color(0xFF3B82F6), Color(0xFF60A5FA)],
      shadowColor: const Color(0xFF3B82F6),
      label: '专注学习时长',
      labelColor: AppTheme.blue100,
      icon: Icons.watch_later_rounded,
      value: '$studyMinutes',
      unit: '分钟',
      compact: compact,
    );
    Widget card3 = _metricCard(
      gradient: const [AppTheme.accentGreen, Color(0xFF34D399)],
      shadowColor: AppTheme.accentGreen,
      label: '已圆满通关',
      labelColor: const Color(0xFFECFDF5),
      icon: Icons.check_circle_rounded,
      value: '$completedCount',
      unit: '门课',
      compact: compact,
    );

    if (compact) {
      return Column(children: [card1, gap, card2, gap, card3]);
    }
    return Row(
      children: [
        Expanded(child: card1),
        gap,
        Expanded(child: card2),
        gap,
        Expanded(child: card3),
      ],
    );
  }

  /// One gradient metric card. [unit] is optional (积分 card has none).
  Widget _metricCard({
    required List<Color> gradient,
    required Color shadowColor,
    required String label,
    required Color labelColor,
    required IconData icon,
    required String value,
    String? unit,
    required bool compact,
  }) {
    return Container(
      padding: EdgeInsets.all(compact ? 20 : 28),
      decoration: BoxDecoration(
        gradient: LinearGradient(colors: gradient),
        borderRadius: BorderRadius.circular(compact ? 24 : 32),
        border: Border.all(color: Colors.white, width: 2),
        boxShadow: [
          BoxShadow(
            color: shadowColor.withValues(alpha: 0.2),
            blurRadius: 20,
            offset: const Offset(0, 8),
          )
        ],
      ),
      child: Stack(
        children: [
          Positioned(
            right: -10,
            bottom: -10,
            child: Icon(
              icon,
              color: Colors.white.withValues(alpha: 0.2),
              size: compact ? 56 : 80,
            ),
          ),
          Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                label,
                style: TextStyle(color: labelColor, fontWeight: FontWeight.bold, fontSize: 14),
              ),
              const SizedBox(height: 12),
              Row(
                crossAxisAlignment: CrossAxisAlignment.baseline,
                textBaseline: TextBaseline.alphabetic,
                children: [
                  Text(
                    value,
                    style: TextStyle(
                      fontSize: compact ? 32 : 40,
                      fontWeight: FontWeight.w900,
                      color: Colors.white,
                      fontFamily: 'Quicksand',
                    ),
                  ),
                  if (unit != null) ...[
                    const SizedBox(width: 6),
                    Text(
                      unit,
                      style: const TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.bold),
                    ),
                  ],
                ],
              ),
            ],
          ),
        ],
      ),
    );
  }

  /// Left bento panel: recent points-ledger activity (timeline).
  Widget _buildTimelinePanel(BuildContext context, List<PointsLedger> ledger) {
    return GlassPanel(
      padding: EdgeInsets.all(isPortrait(context) ? 20 : 32),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                padding: const EdgeInsets.all(8),
                decoration: BoxDecoration(
                  color: AppTheme.blue100,
                  borderRadius: BorderRadius.circular(12),
                ),
                child: const Icon(Icons.history_rounded, color: AppTheme.blue600, size: 24),
              ),
              const SizedBox(width: 14),
              const Text(
                '最近动态',
                style: TextStyle(fontSize: 22, fontWeight: FontWeight.w900, color: AppTheme.textWhite),
              ),
            ],
          ),
          const SizedBox(height: 32),

          // Dynamic timeline items
          if (ledger.isEmpty)
            const SizedBox(
              height: 200,
              child: Center(
                child: Text('暂无最近活动', style: TextStyle(color: AppTheme.textMuted)),
              ),
            )
          else
            ListView.builder(
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              itemCount: ledger.take(6).length,
              itemBuilder: (context, index) {
                final item = ledger[index];
                final isGain = item.changeAmount > 0;
                final isNeutral = item.changeAmount == 0;

                return Container(
                  margin: const EdgeInsets.only(bottom: 24),
                  child: Row(
                    children: [
                      Container(
                        width: 44,
                        height: 44,
                        decoration: BoxDecoration(
                          color: AppTheme.blue100,
                          shape: BoxShape.circle,
                          border: Border.all(color: Colors.white, width: 3),
                          boxShadow: const [
                            BoxShadow(color: Colors.black12, blurRadius: 4)
                          ],
                        ),
                        child: Icon(
                          ledgerIcon(item.reasonType),
                          color: isGain
                              ? AppTheme.accentGreen
                              : (isNeutral
                                  ? AppTheme.primaryColor
                                  : Colors.redAccent),
                        ),
                      ),
                      const SizedBox(width: 16),
                      Expanded(
                        child: Container(
                          padding: const EdgeInsets.all(16),
                          decoration: BoxDecoration(
                            color: const Color(0xFFF8FAFC),
                            borderRadius: BorderRadius.circular(20),
                            border: Border.all(color: AppTheme.borderMuted),
                          ),
                          child: Row(
                            mainAxisAlignment: MainAxisAlignment.spaceBetween,
                            children: [
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(
                                      item.description.isEmpty
                                          ? ledgerFallbackTitle(item.reasonType)
                                          : item.description,
                                      style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 13),
                                      maxLines: 2,
                                      overflow: TextOverflow.ellipsis,
                                    ),
                                    const SizedBox(height: 4),
                                    Text(
                                      formatLedgerTime(item.createdAt),
                                      style: const TextStyle(color: AppTheme.textMuted, fontSize: 11, fontWeight: FontWeight.bold),
                                    ),
                                  ],
                                ),
                              ),
                              Container(
                                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                                decoration: BoxDecoration(
                                  color: isGain
                                      ? const Color(0xFFFFEDD5)
                                      : AppTheme.slate100,
                                  borderRadius: BorderRadius.circular(10),
                                  border: Border.all(
                                    color: isGain
                                        ? const Color(0xFFFFDBB5)
                                        : AppTheme.borderMuted,
                                  ),
                                ),
                                child: Text(
                                  isNeutral ? '—' : '${item.changeAmount > 0 ? '+' : ''}${item.changeAmount}',
                                  style: TextStyle(
                                    color: isGain
                                        ? AppTheme.accentOrange
                                        : AppTheme.textMuted,
                                    fontWeight: FontWeight.w900,
                                    fontSize: 13,
                                  ),
                                ),
                              ),
                            ],
                          ),
                        ),
                      ),
                    ],
                  ),
                );
              },
            ),
        ],
      ),
    );
  }

  /// Right bento panel: honor wall (badge list).
  Widget _buildBadgeWallPanel(BuildContext context, List<BadgeStatus> badges, int unlockedStars, int totalStars) {
    return GlassPanel(
      padding: EdgeInsets.all(isPortrait(context) ? 20 : 32),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                padding: const EdgeInsets.all(8),
                decoration: BoxDecoration(
                  color: const Color(0xFFFEF3C7),
                  borderRadius: BorderRadius.circular(12),
                ),
                child: const Icon(Icons.military_tech_rounded, color: Color(0xFFD97706), size: 24),
              ),
              const SizedBox(width: 14),
              const Text(
                '荣誉墙',
                style: TextStyle(fontSize: 22, fontWeight: FontWeight.w900, color: AppTheme.textWhite),
              ),
            ],
          ),
          const SizedBox(height: 32),

          // Badges widgets (real data)
          if (badges.isEmpty)
            const SizedBox(
              height: 120,
              child: Center(
                child: Text('暂无成就', style: TextStyle(color: AppTheme.textMuted)),
              ),
            )
          else
            Column(
              children: [
                for (int i = 0; i < badges.length; i++) ...[
                  _buildBadgeItem(badges[i]),
                  if (i < badges.length - 1) const SizedBox(height: 16),
                ],
              ],
            ),

          const SizedBox(height: 24),
          Button3D.white(
            onPressed: () {},
            padding: const EdgeInsets.symmetric(vertical: 14),
            child: Center(
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(Icons.star_rounded, size: 18, color: Color(0xFFF59E0B)),
                  const SizedBox(width: 6),
                  Text('$unlockedStars / $totalStars',
                      style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 16)),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  /// One badge row in the honor wall. Multi-tier badges show tier dots + a
  /// progress bar toward the next tier; single-tier badges show lock/unlock.
  Widget _buildBadgeItem(BadgeStatus st) {
    final unlocked = st.unlocked;
    final token = BadgeStyle.tokenFor(st.badge.code, st.badge.ruleType);
    final icon = token.icon;
    final color = token.color;
    final bgColor = token.bgColor;
    final multiTier = st.badge.isMultiTier;

    return Opacity(
      opacity: unlocked ? 1.0 : 0.55,
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: Colors.white,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: unlocked ? AppTheme.borderMuted : AppTheme.slate100,
            width: 2,
          ),
        ),
        child: Row(
          children: [
            Container(
              width: 48,
              height: 48,
              decoration: BoxDecoration(
                color: bgColor,
                borderRadius: BorderRadius.circular(16),
              ),
              child: Icon(icon, color: color, size: 26),
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Flexible(
                        child: Text(
                          st.badge.title,
                          style: const TextStyle(fontWeight: FontWeight.w900, fontSize: 15, color: AppTheme.textWhite),
                        ),
                      ),
                      if (multiTier && unlocked && st.tier >= st.tierCount - 1) ...[
                        const SizedBox(width: 6),
                        const Icon(Icons.workspace_premium_rounded, size: 16, color: Color(0xFFF59E0B)),
                      ],
                    ],
                  ),
                  const SizedBox(height: 4),
                  Text(
                    st.badge.description,
                    style: const TextStyle(color: AppTheme.textMuted, fontSize: 11, fontWeight: FontWeight.bold),
                  ),
                  if (multiTier) ...[
                    const SizedBox(height: 8),
                    _buildTierProgress(st),
                  ],
                ],
              ),
            ),
            if (!multiTier && !unlocked)
              const Icon(Icons.lock_rounded, color: AppTheme.slate400, size: 16),
          ],
        ),
      ),
    );
  }

  /// Tier dots (●●●○○) + a progress bar showing progress → next tier.
  Widget _buildTierProgress(BadgeStatus st) {
    final cleared = st.tier + 1; // 0 if none cleared
    final total = st.tierCount;
    final maxed = st.tier >= 0 && st.tier >= total - 1;

    // Dots: filled for cleared tiers, empty for remaining.
    final dots = Row(
      children: [
        for (int i = 0; i < total; i++) ...[
          if (i > 0) const SizedBox(width: 4),
          Icon(
            i <= st.tier ? Icons.circle : Icons.circle_outlined,
            size: 8,
            color: i <= st.tier ? BadgeStyle.colorFor(st.badge.ruleType) : const Color(0xFFCBD5E1),
          ),
        ],
        const SizedBox(width: 8),
        Text(
          maxed ? '满级' : '$cleared/$total',
          style: TextStyle(
            fontSize: 10,
            fontWeight: FontWeight.w900,
            color: maxed ? const Color(0xFFF59E0B) : AppTheme.textMuted,
          ),
        ),
      ],
    );

    // Progress bar: only when not maxed and there's a next tier threshold.
    Widget? bar;
    if (!maxed && st.nextTier > 0) {
      final tiers = st.badge.parsedTiers;
      // Guard against tier index out of range (e.g. admin reduced the tier
      // count after the user already cleared a higher tier).
      final safeTier = (st.tier >= 0 && st.tier < tiers.length) ? st.tier : -1;
      final prevThreshold = safeTier >= 0 ? tiers[safeTier].t : 0;
      final span = st.nextTier - prevThreshold;
      final frac = span > 0 ? ((st.progress - prevThreshold) / span).clamp(0.0, 1.0) : 0.0;
      bar = Padding(
        padding: const EdgeInsets.only(top: 6),
        child: Row(
          children: [
            Expanded(
              child: ClipRRect(
                borderRadius: BorderRadius.circular(4),
                child: LinearProgressIndicator(
                  value: frac,
                  minHeight: 5,
                  backgroundColor: AppTheme.slate100,
                  valueColor: AlwaysStoppedAnimation<Color>(BadgeStyle.colorFor(st.badge.ruleType)),
                ),
              ),
            ),
            const SizedBox(width: 8),
            Text(
              '${st.progress}/${st.nextTier}',
              style: const TextStyle(fontSize: 9, color: AppTheme.textMuted, fontWeight: FontWeight.bold),
            ),
          ],
        ),
      );
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        dots,
        if (bar != null) bar,
      ],
    );
  }
}
