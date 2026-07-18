import { Award, type LucideProps } from 'lucide-react';
import type { ComponentType } from 'react';

// Badge icon display: all badges share ONE Lucide line icon (Award) and are
// distinguished by a colored ring. The color is derived from the badge's
// `code` (e.g. "first_blood", "subject_math") via substring match; custom
// badges fall back to a neutral indigo. The DB no longer stores an icon field
// — both clients (admin lucide, Flutter Material) derive the visual from the
// code/ruleType at render time.

// Maps a badge code to a ring/icon color. Matched by substring so it works for
// the seeded codes (first_blood, streak, episode_master, subject_<key>, ...).
const BADGE_COLORS: Array<{ match: string; color: string }> = [
  { match: 'first_blood', color: '#f59e0b' }, // amber — 首战
  { match: 'streak', color: '#ef4444' }, // red — 连胜/坚持
  { match: 'time_master', color: '#06b6d4' }, // cyan — 时长
  { match: 'explorer', color: '#84cc16' }, // lime — 探索
  { match: 'points_hero', color: '#eab308' }, // yellow — 积分
  { match: 'episode_master', color: '#10b981' }, // emerald — 完课
  { match: 'course_master', color: '#a16207' }, // gold — 通关
  { match: 'weekly', color: '#8b5cf6' }, // violet — 周打卡
  { match: 'subject_math', color: '#f59e0b' },
  { match: 'subject_english', color: '#3b82f6' },
  { match: 'subject_chinese', color: '#ef4444' },
  { match: 'subject_physics', color: '#10b981' },
];

const DEFAULT_BADGE_COLOR = '#6366f1'; // indigo — 自定义勋章 fallback

// badgeColor resolves the ring color for a badge code.
export function badgeColor(code: string | undefined): string {
  if (!code) return DEFAULT_BADGE_COLOR;
  const hit = BADGE_COLORS.find((c) => code.includes(c.match));
  return hit?.color ?? DEFAULT_BADGE_COLOR;
}

// The single shared badge icon component. Callers wrap it in a colored ring.
export const BadgeIcon: ComponentType<LucideProps> = Award;
