import type { ComponentType } from 'react';
import {
  BookOpen,
  Calculator,
  Languages,
  PenLine,
  Atom,
  FlaskConical,
  Leaf,
  Scroll,
  Globe,
  Scale,
  Compass,
  Music,
  Palette,
  Trophy,
  Code,
  Film,
  Clapperboard,
  Tv,
  Video,
  type LucideProps,
} from 'lucide-react';

// Subject icon mapping: resolve a subject key to a Lucide line icon.
//
// Subjects are DB-driven (admin can edit/create them), so we map the known
// system-seeded keys (see backend service/subject_service.go seed list) to a
// semantically-close Lucide icon, and fall back to BookOpen for anything
// custom or unrecognized. The icon is meant to be tinted with the subject's
// `color` from the DB by the caller.
//
// Why Lucide and not emoji: emoji glyphs vary across platforms (Windows/Mac/
// Android render the same codepoint differently) and clash with the panel's
// line-icon visual language. Lucide icons are single-color SVGs that pick up
// the subject color cleanly.

const SUBJECT_ICONS: Record<string, ComponentType<LucideProps>> = {
  chinese: PenLine, // 语文 — pen
  math: Calculator, // 数学 — calculator
  english: Languages, // 英语 — languages
  physics: Atom, // 物理/科学 — atom
  chemistry: FlaskConical, // 化学 — flask
  biology: Leaf, // 生物 — leaf
  history: Scroll, // 历史 — scroll
  geography: Globe, // 地理 — globe
  politics: Scale, // 道德与法治 — scale
  extra: Compass, // 课外百科 — compass
  entertainment: Film, // 娱乐 — film
  // 2026-07-20 新增娱乐子类(配合 Subject.Category=entertainment)。
  animation: Clapperboard, // 动画片
  movie: Film, // 电影
  documentary: Video, // 纪录片
  variety: Tv, // 综艺
  // Common aliases a custom subject might use:
  music: Music,
  art: Palette,
  pe: Trophy,
  sport: Trophy,
  programming: Code,
  coding: Code,
  cs: Code,
  science: Atom,
};

// resolveSubjectIcon returns the Lucide icon component for a subject key, or
// BookOpen as a neutral fallback. Callers render it as `<Icon size={n} />`.
export function resolveSubjectIcon(key: string): ComponentType<LucideProps> {
  return SUBJECT_ICONS[key] ?? BookOpen;
}

export { BookOpen };
