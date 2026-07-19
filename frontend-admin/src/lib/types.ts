// API types — mirror the snake_case DTOs served by /admin/api/*. All fields
// are optional-tolerant where the backend may omit them.

/**
 * AIConfig — 5 个独立配置维度,镜像后端 model.AIConfig(存 Course/Subject 的
 * AIConfigJSON blob)。每个字段喂给不同的 AI 能力:
 *   - whisper_hint:Whisper 字幕转录(术语/口音,≤240 字)
 *   - summary_hint:summary agent(总结风格/侧重点)
 *   - quiz_hint:quiz agent(题型偏好/难度/出题指引)
 *   - advice_hint:advice agent(建议侧重点/口吻)
 *   - term_dict:横切给 summary/quiz/advice 的术语纠错字典(车→居)
 * Course 和 Subject 都用这个结构。解析优先级:Course > Subject(课程覆盖学科);
 * term_dict 特殊:课程级 + 学科级合并。
 */
export interface AiConfig {
  whisper_hint?: string;
  summary_hint?: string;
  quiz_hint?: string;
  advice_hint?: string;
  term_dict?: string;
}

export interface SubjectMeta {
  id?: number;
  key: string;
  label: string;
  color: string;
  sort_order?: number;
  is_system?: boolean; // true = seeded default, protected from deletion (still editable)
  /** 学科级默认 AI 配置。课程级对应字段为空时回退到这里。见 AiConfig。 */
  ai_config?: AiConfig;
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
  grade: string; // comma-joined grade keys, e.g. "3,4,5" (alias for grades)
  grades?: string; // comma-joined grade keys (new field; backend sends this)
  subject: string;
  subject_id?: number;
  content_type?: string; // "learning" | "entertainment"
  cover_url: string;
  cover_fallback_url?: string; // first-episode cover, shown only when cover_url is empty
  attachment_json: string;
  /** DEPRECATED. Single-field predecessor of the split whisper_hint/quiz_hint
   * (now stored together in one backend JSON column, AIConfigJSON). Kept for
   * edit-form repopulation of legacy rows; new writes go to whisper_hint /
   * quiz_hint. See backend model.Course.AIConfig. */
  ai_hint?: string;
  /** Admin-authored hint fed to the subtitle worker's Whisper initial_prompt
   * (terminology list, accent notes, ≤240 chars). Sourced from the backend's
   * AIConfigJSON blob. Empty when unset. */
  whisper_hint?: string;
  /** Admin-authored hint fed to the summary/quiz/advice LLM agents: question-
   * style preferences, difficulty bias, AND a terminology correction dictionary
   * (terms Whisper mishears that the LLM must output correctly). Longer text.
   * Sourced from the backend's AIConfigJSON blob. Empty when unset. */
  quiz_hint?: string;
  /** 课程级 AI 配置(5 字段,见 AiConfig)。提交时整体发送;whisper_hint/quiz_hint
   * 字段保留仅为回显老数据兼容。新增/编辑课程时应优先用 ai_config 整体提交。 */
  ai_config?: AiConfig;
  /** Course-level switches for AI post-processing of its episodes. When false,
   * the worker skips summary / quiz generation for this course even if the AI
   * pipeline is globally configured. Optional (absent = disabled, the model
   * default is false — AI is an opt-in add-on per course). */
  ai_summary_enabled?: boolean;
  ai_quiz_enabled?: boolean;
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
  reading_series_access?: number[];
  reading_book_access?: number[];
  reading_article_access?: number[];
  // Storage-source whitelist (防呆). Empty/undefined = unrestricted.
  storage_source_access?: number[];
  // Per-user learning stats (populated by the batch-aggregated ListUsers).
  completed_episodes?: number;
  accessible_episodes?: number;
  watch_seconds?: number; // raw accumulated seconds (for sub-minute precision display)
  unlocked_badges?: number;
  total_badges?: number;
  last_active_at?: string;
}

// A user's active login session (one per device). Returned by the admin
// device-session endpoints. `token` is the full opaque value used to target a
// row for revoke/note; `token_prefix` is just a short display stub.
export interface UserSession {
  token: string;
  token_prefix: string;
  user_id: number;
  device_name: string;
  user_agent: string;
  note: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
}

// One cell of the month heatmap: a business-calendar day + total watch
// duration that day (seconds).
export interface WatchHistoryDay {
  date: string;   // YYYY-MM-DD (business zone)
  seconds: number;
}

// One row of the selected-day detail timeline. Returned with episode + course
// titles denormalized for display.
export interface WatchEventDTO {
  id: number;
  episode_id: number;
  episode_title: string;
  course_id: number;
  course_title: string;
  content_type: 'learning' | 'entertainment';
  started_at: string;       // ISO timestamp
  ended_at: string;         // ISO timestamp
  duration_seconds: number; // real watch seconds (pauses excluded)
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
// serializes with PascalCase field names (ID, Code, RuleType, RuleTarget,
// Threshold). AdminBadge mirrors that shape so the Badges page can read what
// the API actually sends. (The snake_case Badge type below is retained for the
// client-facing /users/:id/badges DTO.)
export interface AdminBadge {
  ID: number;
  Code: string;
  Title: string;
  Description: string;
  RuleType: string;
  RuleTarget: string;
  Threshold: number;
  Tiers?: string; // multi-tier JSON '[{"t":3,"r":10}]'; empty = single-tier
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

/** Full subtitle including SRT text — fetched on demand when viewing content. */
export interface SubtitleDetail extends Subtitle {
  srt_content: string;
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

// StorageSource is one netdisk backend (alist or webdav). Admin configures N;
// content points at one via source_id. Mirrors the Go StorageSource model.
// Field names are snake_case to match the backend JSON tags.
export interface StorageSource {
  id?: number;
  name: string;
  type: string; // "alist" | "webdav"
  url: string;
  username: string;
  password: string;
  token: string; // alist only
  is_default?: boolean;
}

// AiProvider is one configured AI capability backend (chat / embedding /
// rerank). Mirrors the Go AiProvider model; field names are snake_case to match
// the backend JSON tags. api_key is sensitive — it is NOT echoed back by the
// server on GET; the edit form leaves it blank and "blank = don't change".
export interface AiProvider {
  id?: number;
  capability: 'chat' | 'embedding' | 'rerank';
  name: string; // display name, e.g. "主聊天模型"
  provider_type: string; // 'openai_compat' | 'onnx_local'
  base_url: string; // chat (openai_compat); empty for onnx_local
  api_key: string; // chat (openai_compat); empty for onnx_local. Sensitive.
  model_name: string; // model name (chat) or model path (onnx)
  is_enabled: boolean;
}

// Result of POST /admin/api/ai/providers/:id/test.
export interface AiProviderTestResult {
  ok: boolean;
  message: string;
  latency_ms?: number;
}

// Result of POST /admin/api/ai/providers/models (probe a relay's model catalog
// before saving). ok=false carries a message; ok=true carries the model id list.
export interface AiModelsResult {
  ok: boolean;
  models?: string[];
  message?: string;
}

// AppRelease is one published APK build. (version_code, abi) is the identity —
// the same pair the OTA client contract keys on. is_active=false means the
// build is withdrawn (hidden from clients, not downloadable) but kept for history.
export interface AppRelease {
  id: number;
  version_code: number;
  version_name: string;
  abi: string; // arm64-v8a | armeabi-v7a | x86_64
  file_size: number;
  sha256: string;
  release_notes: string;
  force_update: boolean;
  is_active: boolean;
  created_at: string;
}

export interface PointsLedgerEntry {
  id: number;
  user_id: number;
  change_amount: number;
  reason_type: string;
  description: string;
  created_at: string;
}

// ---- Unlock (drip schedule) ----

// One configured weekly unlock time point. Weekday follows Go's time.Weekday:
// 0 = Sunday ... 6 = Saturday. Hour/Minute are in the business timezone.
export interface WeeklyTime {
  weekday: number;
  hour: number;
  minute: number;
}

// Strategy values mirror backend model.Strategy* constants.
export type UnlockStrategy =
  | 'all_open'
  | 'manual'
  | 'interval'
  | 'weekly'
  | 'selected';

// Course-level default unlock strategy template. Absence (exists=false) means
// all_open (backward compatible — every episode visible).
export interface UnlockTemplate {
  course_id: number;
  strategy: UnlockStrategy;
  interval_seconds: number;
  weekly_times: WeeklyTime[];
  exists: boolean;
}

// Per-(user, course) override. Absence means "inherit the template".
export interface UnlockOverride {
  user_id: number;
  course_id: number;
  strategy: UnlockStrategy;
  interval_seconds: number;
  weekly_times: WeeklyTime[];
  manual_unlock_count: number;
  allowed_episode_ids: number[];
  exists: boolean;
}

// Resolved visibility preview for a (user, course), from unlock-preview.
export interface UnlockPreview {
  visible_ids: number[];
  visible_count: number;
  total: number;
  unlocked_n: number;
  strategy: UnlockStrategy;
  strategy_label: string;
  next_unlock_at: string;
}

// ---- Reading Room ----

// ReadingSeries is the container/series for reading material (mirrors Course).
export interface ReadingSeries {
  id: number;
  title: string;
  description: string;
  grade: string;
  subject: string; // subject key
  subject_id: number;
  cover_url: string;
  tags_list: string[];
  tag_ids: number[];
  grade_display: string;
  sort_order: number;
  book_count: number;
  article_count: number;
  created_at: string;
  updated_at: string;
}

// ReadingBook is a PDF document (mirrors Episode).
export interface ReadingBook {
  id: number;
  series_id: number; // 0 = standalone
  sort_order: number;
  title: string;
  file_relative_path: string;
  file_size: number | null;
  page_count: number | null;
  cover_url: string;
  grade: string;
  subject: string;
  subject_id: number;
  tags_list: string[];
  tag_ids: number[];
  grade_display: string;
  created_at: string;
  updated_at: string;
}

// ReadingArticle is a web/rich-text article. mirror_status/mirrored_url are
// Phase 2 offline-mirror reservations (not used by the UI today).
export interface ReadingArticle {
  id: number;
  series_id: number;
  sort_order: number;
  title: string;
  source_url: string;
  whitelist_domains: string; // JSON []string
  mirror_status: string; // none | pending | ready | failed
  mirrored_url: string;
  cover_url: string;
  grade: string;
  subject: string;
  subject_id: number;
  tags_list: string[];
  tag_ids: number[];
  grade_display: string;
  created_at: string;
  updated_at: string;
}

export interface ReadingSeriesDetail {
  series: ReadingSeries;
  books: ReadingBook[];
  articles: ReadingArticle[];
}

export type ReadingTargetType = 'series' | 'book' | 'article';

// ---- Subtitle generation queue ----

export type SubtitleJobStatus = 'queued' | 'processing' | 'done' | 'failed' | 'skipped';

export interface SubtitleJob {
  id: number;
  episode_id: number;
  episode_title?: string;
  course_id?: number;
  status: SubtitleJobStatus;
  priority: number;
  attempt: number;
  language: string;
  claimed_by?: string;
  claimed_at?: string | null;
  completed_at?: string | null;
  error?: string;
  /** Worker-reported transcription ratio 0..1, or null when none reported. */
  progress?: number | null;
  duration_seconds?: number | null;
  created_at: string;
  updated_at: string;
}

export interface SubtitleJobStats {
  running: boolean;
  current_job_id?: number;
  current_episode_id?: number;
  current_title?: string;
  queued: number;
  processing: number;
  done: number;
  failed: number;
  skipped: number;
  last_finished_at?: string;
}

// Result of a batch enqueue: which episodes were added vs skipped, with a
// per-id machine reason code for the skipped ones.
export interface SubtitleJobEnqueueResult {
  status: string;
  enqueued: number[];
  skipped: number[];
  reasons: Record<number, string>;
}

// ---- AI workflow observability ----

export type AiJobStatus = 'queued' | 'processing' | 'done' | 'failed' | 'skipped';

// One AI job enqueued against an episode (e.g. "summarize" / "quiz"). Mirrors
// the Go AiJob model; field names are snake_case to match backend JSON tags.
export interface AiJob {
  id: number;
  job_type: string; // "summarize" | "quiz" | ...
  episode_id: number;
  // course_id is always populated by the backend (model column is non-null),
  // so it's required here. Episode/course/user_*_title are best-effort
  // display names resolved server-side (empty when the referenced row was
  // deleted); user_nickname only exists on per-user quiz jobs.
  course_id: number;
  status: AiJobStatus;
  attempt: number;
  error?: string;
  /** Worker-reported ratio 0..1, or null when none reported. */
  progress?: number | null;
  created_at: string;
  completed_at?: string | null;
  episode_title?: string;
  course_title?: string;
  user_nickname?: string;
}

// Aggregate counts for the jobs stats bar (returned alongside the job list).
export interface AiJobStats {
  queued: number;
  processing: number;
  done: number;
  failed: number;
  skipped: number;
}

// Response of GET /admin/api/ai/jobs: a job list + rolled-up status counts.
export interface AiJobsResponse {
  jobs: AiJob[];
  stats: AiJobStats;
}

// Result of POST /admin/api/ai/jobs: which episode ids were enqueued vs
// skipped, with a per-id reason map.
export interface AiJobEnqueueResult {
  enqueued: number[];
  skipped: Record<number, string>;
}

// One recorded model invocation (an "agent decision trace"). A job produces
// one or more runs; each run is a single capability call (chat / embedding /
// rerank) with its prompt/response captured for replay.
export interface AiRun {
  id: number;
  job_id: number;
  capability: string; // "summary" | "quiz" | "chat"
  input_json: string; // raw JSON string of the call's structured input
  prompt_tokens: number;
  completion_tokens: number;
  model_used: string;
  // Resolved display names for the episode/course the run's job targeted
  // (populated by the admin runs-list endpoint so the dashboard/workflow can
  // show what each run was about without a client-side join).
  episode_title?: string;
  course_title?: string;
  response_text: string;
  // trace_json carries the ReAct step-by-step reasoning for quiz runs: an array
  // of {step, thought, action:{tool,args}, observation, is_final}. Empty for
  // single-shot capabilities (summary). The Workflow page renders this as the
  // "思考时间线" — the centerpiece for learning how the agent decided.
  trace_json?: string;
  self_check_result?: string; // "" / pass / fail / machine-readable result
  self_check_note?: string;
  duration_ms: number;
  created_at: string;
  /** 最终发给 LLM 的完整 system prompt(= 代码常量,冗余存一份供 admin 查看)。
   * 新增字段(可观测性);老 run 没有,展示时兜底空串。 */
  system_prompt_text?: string;
  /** 最终拼好的 user prompt(含注入的 hint/TermDict)。新增字段。 */
  user_prompt_text?: string;
}

// One step in an agent's ReAct trace (parsed from AiRun.trace_json).
export interface AiTraceStep {
  step: number;
  thought: string;
  action?: { tool: string; args: string };
  observation?: string;
  is_final?: boolean;
}

// ---------------------------------------------------------------------------
// Phase C — quiz observability types
// ---------------------------------------------------------------------------

// A generated summary row (GET /admin/api/ai/summaries/:episodeID).
export interface AiSummaryRow {
  episode_id: number;
  course_id: number;
  summary_json: string; // {headline, key_points[], concepts[], takeaway}
  model_used: string;
  created_at: string;
}

// One quiz for a user (GET /admin/api/ai/users/:userID/quizzes).
export interface AiQuizRow {
  id: number;
  episode_id: number;
  user_id: number;
  course_id: number;
  difficulty: string;
  agent_feedback: string;
  created_at: string;
  /** Resolved display names (best-effort, empty when the row was deleted). */
  episode_title?: string;
  course_title?: string;
}

// A question in a quiz (admin detail view — includes the answer).
export interface AiQuizQuestion {
  id: number;
  quiz_id: number;
  chunk_id?: number;
  type: string; // "choice" | "multi_choice" | "fill"
  stem: string;
  options?: string; // JSON []string (choice / multi_choice)
  answer: number; // DEPRECATED choice: 0-based index (multi_choice 恒 0,看 correct_indices)
  answer_text?: string; // DEPRECATED fill: JSON []string of acceptable answers (multi_choice 空)
  /** multi_choice: 正确选项索引数组(choice/fill 不下发)。admin 核对多选题答案用。 */
  correct_indices?: number[];
  /** multi_choice: 是否允许部分对 scoring。 */
  partial_credit?: boolean;
  /** 原始判分元数据 JSON(按 type 解析),供 admin 排查判分问题。 */
  scoring?: string;
  explanation: string;
  chunk_start_time?: number | null; // joined from content_chunks, for video-jump
}

// One student answer record (append-only history).
export interface AiAnswerRow {
  id: number;
  question_id: number;
  user_id: number;
  user_answer: number;
  correct: boolean;
  answered_at: string;
}

// Per-chunk mastery (the feedback-loop state the agent reads).
export interface AiMasteryRow {
  id: number;
  user_id: number;
  episode_id: number;
  chunk_id: number;
  mastery: number;
  correct_count: number;
  wrong_count: number;
  last_reviewed?: string | null;
}

// Full per-quiz observability bundle (GET /admin/api/ai/quizzes/:quizID).
export interface AiQuizDetail {
  quiz: AiQuizRow;
  questions: AiQuizQuestion[];
  answers: AiAnswerRow[];
  masteries: AiMasteryRow[];
  runs: AiRun[]; // the ai_runs that generated this quiz (trace lives here)
}

// ---------------------------------------------------------------------------
// Phase E — admin 用户学习报告(跨课程画像,agent 驱动)
// ---------------------------------------------------------------------------

// 一个用户的学习报告响应(GET /admin/api/ai/users/:id/study-report)。
// status 三态驱动前端渲染:
//   - 'ready': 有报告(report 非空),渲染文本
//   - 'generating': 无报告 + 有在途 job,显示 spinner + 轮询
//   - '': 无报告 + 未生成,显示"生成学习报告"按钮
// report/model_used/generated_at 仅 ready 时有值。
export interface UserStudyReport {
  status: 'ready' | 'generating' | '';
  report?: string;
  model_used?: string;
  generated_at?: string;
}

// raw StudyAdvice row from GET /admin/api/ai/users/:userID/advice. Mirrors the
// backend model.StudyAdvice Go struct (models.go ~L1230). The struct marshals
// with explicit snake_case json tags on the public fields; the rest are
// `json:"-"` and therefore NOT present in the response — they're omitted here
// too so the type matches what the server actually sends:
//   - ID / UserID / MasterySnapshotJSON / CreatedAt / UpdatedAt → json:"-" (excluded)
//   - scope / scope_id / advice_text / generated_at → required
//   - model_used → json:"model_used,omitempty" (optional, absent when empty)
export interface StudyAdviceRow {
  scope: string;     // 'episode' | 'course' | 'subject'
  scope_id: number;  // episode_id / course_id / subject_id (per scope)
  advice_text: string;
  model_used?: string;
  generated_at: string;
}
