// API types — mirror the snake_case DTOs served by /admin/api/*. All fields
// are optional-tolerant where the backend may omit them.

export interface SubjectMeta {
  id?: number;
  key: string;
  label: string;
  emoji: string;
  color: string;
  sort_order?: number;
  is_system?: boolean; // true = seeded default, protected from deletion (still editable)
}

// Subject catalog cache. The admin SPA used to ship a hardcoded SUBJECTS
// constant here; subjects are now DB-driven and editable. useSubjects()
// (in lib/useSubjects.ts) fetches /admin/api/subjects and seeds this cache so
// that the plain subjectMeta() helper below — used outside React render paths
// or in legacy call sites — keeps resolving keys without a hook.
let subjectCache: SubjectMeta[] = [];

export function setSubjectCache(list: SubjectMeta[]) {
  subjectCache = list;
}

export function subjectMeta(key: string): SubjectMeta {
  return (
    subjectCache.find((s) => s.key === key) ?? {
      key,
      label: key,
      emoji: '📦',
      color: '#9ca3af',
    }
  );
}

export interface TagMeta {
  id?: number;
  key: string;
  label: string;
  color: string;
  sort_order?: number;
  is_system?: boolean; // true = seeded default, protected from deletion
  course_count?: number; // how many courses use this tag (delete-confirm blast radius)
}

// Tag catalog cache — same pattern as subjects. Warmed by useTags() in
// Layout so tagMeta(id) works outside React render paths.
let tagCache: TagMeta[] = [];

export function setTagCache(list: TagMeta[]) {
  tagCache = list;
}

export function tagMetaByID(id: number): TagMeta | undefined {
  return tagCache.find((t) => t.id === id);
}

export const GRADES: { key: string; name: string }[] = [
  { key: '1', name: '一年级' },
  { key: '2', name: '二年级' },
  { key: '3', name: '三年级' },
  { key: '4', name: '四年级' },
  { key: '5', name: '五年级' },
  { key: '6', name: '六年级' },
  { key: '7', name: '七年级/初一' },
  { key: '8', name: '八年级/初二' },
  { key: '9', name: '九年级/初三' },
  { key: 'universal', name: '全学段通用' },
];

export function gradeName(key: string): string {
  return GRADES.find((g) => g.key === key)?.name ?? key;
}

export function gradeDisplay(grade: string): string {
  if (!grade) return '';
  if (grade === 'universal') return '全学段通用';
  return grade
    .split(',')
    .map((g) => g.trim())
    .map((g) => (g === 'universal' ? '通用' : `${g}年级`))
    .join(', ');
}

export interface MediaMeta {
  duration_seconds: number;
  format_name?: string;
  bit_rate?: number;
  width?: number;
  height?: number;
  video_codec?: string;
  fps?: string;
  audio_codec?: string;
  audio_channels?: number;
}

export interface Episode {
  id: number;
  course_id: number;
  chapter_id: number;
  sort_order: number;
  title: string;
  video_relative_path: string;
  cover_url: string;
  attachment_json: string;
  file_hash: string;
  original_relative_path: string;
  file_size: number | null;
  duration_seconds: number | null;
  media_meta_json: string;
  media_meta?: MediaMeta | null;
  subtitle_count?: number;
  created_at?: string;
  updated_at?: string;
}

export interface Chapter {
  id: number;
  course_id: number;
  title: string;
  description: string;
  cover_url: string;
  attachment_json: string;
  sort_order: number;
  created_at?: string;
  updated_at?: string;
}

export interface Course {
  id: number;
  title: string;
  grade: string;
  subject: string;
  subject_id?: number;
  cover_url: string;
  cover_fallback_url?: string; // first-episode cover, shown only when cover_url is empty
  tags: string;
  attachment_json: string;
  tags_list?: string[];
  tag_ids?: number[];
  grade_display?: string;
  episode_count?: number;
  chapter_count?: number;
  total_duration_seconds?: number;
  created_at?: string;
  updated_at?: string;
}

export interface CourseDetail {
  course: Course;
  episodes: Episode[];
  chapters: Chapter[];
}

export interface User {
  id: number;
  nickname: string;
  avatar_url: string;
  role: string;
  created_at?: string;
  updated_at?: string;
  current_points?: number;
  total_earned_points?: number;
  course_access?: number[];
  // Per-user learning stats (populated by the batch-aggregated ListUsers).
  completed_episodes?: number;
  accessible_episodes?: number;
  watch_minutes?: number;
  watch_seconds?: number; // raw accumulated seconds (for sub-minute precision display)
  unlocked_badges?: number;
  total_badges?: number;
  last_active_at?: string;
}

export interface Badge {
  id: number;
  code: string;
  title: string;
  description: string;
  icon_name: string;
  rule_type: string;
  rule_target: string;
  threshold: number;
  unlocked?: boolean;
  unlocked_at?: string;
}

// The admin /admin/api/badges endpoints return the raw Go model, which
// serializes with PascalCase field names (ID, Code, IconName, RuleType,
// RuleTarget, Threshold). AdminBadge mirrors that shape so the Badges page
// can read what the API actually sends. (The snake_case Badge type above is
// retained for the client-facing /users/:id/badges DTO.)
export interface AdminBadge {
  ID: number;
  Code: string;
  Title: string;
  Description: string;
  IconName: string;
  RuleType: string;
  RuleTarget: string;
  Threshold: number;
  RuleJSON?: string; // composite rule tree; present when RuleType === 'composite'
  IsSystem?: boolean; // true = seeded default, protected from deletion
}

export interface Subtitle {
  id: number;
  episode_id: number;
  language: string;
  label: string;
  created_at?: string;
}

export interface ProbeStats {
  running: boolean;
  current_episode_id?: number;
  current_title?: string;
  total: number;
  done: number;
  failed: number;
  last_error?: string;
  last_finished_at?: string;
}

export interface DashboardStats {
  user_count: number;
  course_count: number;
  episode_count: number;
  total_duration_seconds: number;
  pending_probe_count: number;
  subject_distribution: { subject: string; count: number }[];
  recent_daily_episodes: { date: string; count: number }[];
  // Learning-activity aggregates (may be absent on older backends).
  total_watch_seconds?: number;
  completed_episodes?: number;
  active_users_today?: number;
  unlocked_badge_count?: number;
  recent_daily_watch?: { date: string; seconds: number }[];
  top_users?: { id: number; label: string; value: number }[];
  top_courses?: { id: number; label: string; value: number }[];
}

export interface FileInfo {
  name: string;
  path: string;
  size: number;
  is_dir: boolean;
  modified?: string;
  hash?: string;
}

export interface ImportPreviewNode {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  hash: string;
  type: string;
  children?: ImportPreviewNode[];
}

export interface Settings {
  storage_type: string;
  storage_url: string;
  storage_username: string;
  storage_password: string;
  storage_token: string;
}

export interface PointsLedgerEntry {
  id: number;
  user_id: number;
  change_amount: number;
  reason_type: string;
  description: string;
  created_at: string;
}
