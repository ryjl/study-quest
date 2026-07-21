import 'package:flutter/material.dart';

import '../../service/api_service.dart';
import '../../theme.dart';
import 'subject_icon.dart';

/// Shared bookshelf primitives used by both [ReadingRoomScreen] and
/// [ReadingSeriesDetailScreen]. Extracted verbatim from the two screens to
/// kill a ~100-line copy-paste of `_buildCoverTile` plus its three peers.

/// Section heading used above each shelf row.
Widget sectionTitle(String text) {
  return Text(
    text,
    style: const TextStyle(
      fontSize: 18,
      fontWeight: FontWeight.w900,
      color: AppTheme.textWhite,
    ),
  );
}

/// The "bookshelf board" — a semi-transparent gradient container with a
/// dark bottom border simulating the shelf edge.
Widget buildShelfBoard({required List<Widget> children}) {
  return Container(
    width: double.infinity,
    padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
    decoration: BoxDecoration(
      gradient: const LinearGradient(
        begin: Alignment.topCenter,
        end: Alignment.bottomCenter,
        colors: [Color(0xFFFFF7ED), Color(0xFFFEF3C7)],
      ),
      borderRadius: BorderRadius.circular(20),
      border: Border.all(color: AppTheme.borderMuted, width: 2.0),
    ),
    child: Wrap(
      spacing: 16,
      runSpacing: 20,
      children: children,
    ),
  );
}

/// Gradient fallback cover when no cover image is set (or the network image
/// fails to load).
Widget gradientCover(Gradient gradient, String subjectKey, String subjectColor, double w, double h) {
  return Container(
    width: w,
    height: h,
    decoration: BoxDecoration(gradient: gradient),
    child: Center(
      child: Icon(
        subjectIconData(subjectKey),
        size: 48,
        color: AppTheme.colorFromHex(subjectColor),
      ),
    ),
  );
}

/// A single book cover on the shelf — image or gradient fallback, with a
/// slight 3D tilt and drop shadow for the "standing on the shelf" look.
class BookCoverTile extends StatelessWidget {
  final String coverUrl;
  final String subjectKey;
  final String subjectColor;
  final Gradient gradient;
  final IconData badgeIcon;
  final String title;
  final String subtitle;
  final VoidCallback? onTap;

  const BookCoverTile({
    super.key,
    required this.coverUrl,
    required this.subjectKey,
    required this.subjectColor,
    required this.gradient,
    required this.badgeIcon,
    required this.title,
    required this.subtitle,
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    const coverWidth = 130.0;
    const coverHeight = 180.0;
    return GestureDetector(
      onTap: onTap,
      child: Transform.rotate(
        angle: -0.03,
        child: Container(
          width: coverWidth,
          margin: const EdgeInsets.only(bottom: 6),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(8),
            boxShadow: const [
              BoxShadow(
                color: Color(0x33000000),
                blurRadius: 8,
                offset: Offset(3, 5),
              ),
            ],
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              ClipRRect(
                borderRadius: const BorderRadius.only(
                  topLeft: Radius.circular(8),
                  topRight: Radius.circular(8),
                ),
                child: SizedBox(
                  width: coverWidth,
                  height: coverHeight,
                  child: coverUrl.isNotEmpty
                      ? Image.network(
                          ApiService.absoluteUrl(coverUrl),
                          fit: BoxFit.cover,
                          errorBuilder: (_, __, ___) => gradientCover(
                              gradient, subjectKey, subjectColor, coverWidth, coverHeight),
                        )
                      : gradientCover(gradient, subjectKey, subjectColor, coverWidth, coverHeight),
                ),
              ),
              Container(
                width: coverWidth,
                height: 6,
                decoration: const BoxDecoration(
                  color: Color(0xFF78350F),
                  borderRadius: BorderRadius.only(
                    bottomLeft: Radius.circular(8),
                    bottomRight: Radius.circular(8),
                  ),
                ),
              ),
              const SizedBox(height: 8),
              SizedBox(
                width: coverWidth,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Icon(badgeIcon, size: 14, color: AppTheme.textMuted),
                        const SizedBox(width: 4),
                        Expanded(
                          child: Text(
                            title,
                            style: const TextStyle(
                              fontSize: 13,
                              fontWeight: FontWeight.bold,
                              color: AppTheme.textWhite,
                            ),
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                      ],
                    ),
                    Text(
                      subtitle,
                      style: const TextStyle(fontSize: 11, color: AppTheme.textMuted),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
