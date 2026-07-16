import type {
  AdminBadge,
  Badge,
  Chapter,
  Course,
  CourseDetail,
  DashboardStats,
  Episode,
  FileInfo,
  ImportPreviewNode,
  PointsLedgerEntry,
  ProbeStats,
  StorageSource,
  SubjectMeta,
  TagMeta,
  Subtitle,
  SubtitleDetail,
  SubtitleJob,
  SubtitleJobEnqueueResult,
  SubtitleJobStats,
  UnlockOverride,
  UnlockPreview,
  UnlockTemplate,
  User,
  UserSession,
  WatchHistoryDay,
  WatchEventDTO,
  AppRelease,
  ReadingSeries,
  ReadingBook,
  ReadingArticle,
  ReadingSeriesDetail,
  ReadingTargetType,
  AiProvider,
  AiProviderTestResult,
  AiJob,
  AiJobsResponse,
  AiJobEnqueueResult,
  AiRun,
  AiSummaryRow,
  AiQuizRow,
  AiQuizDetail,
} from './types';

// Centralized API client. Same-origin cookies carry the admin session. All
// endpoints return typed data; errors throw an ApiError with the server msg.

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = 'ApiError';
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
    ...init,
  });

  // Empty bodies (e.g. 204) — return as-is
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;

  if (!res.ok) {
    const msg = (data && (data.error || data.message)) || `HTTP ${res.status}`;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

function qs(params: Record<string, string | number | boolean | undefined>): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : '';
}

// ---- Auth ----
export const api = {
  async me(): Promise<{ authenticated: boolean }> {
    return request('/admin/api/me');
  },
  async login(password: string): Promise<{ status: string }> {
    return request('/admin/api/login', { method: 'POST', body: JSON.stringify({ password }) });
  },
  async logout(): Promise<{ status: string }> {
    return request('/admin/api/logout', { method: 'POST' });
  },

  // ---- Dashboard ----
  async dashboardStats(): Promise<DashboardStats> {
    return request('/admin/api/stats/dashboard');
  },

  // ---- Courses ----
  async listCourses(): Promise<Course[]> {
    return request('/admin/api/courses');
  },
  async getCourse(id: number): Promise<CourseDetail> {
    return request(`/admin/api/courses/${id}/detail`);
  },
  async createCourse(body: Partial<Course>): Promise<Course> {
    return request('/admin/api/courses', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateCourse(id: number, body: Partial<Course>): Promise<Course> {
    return request(`/admin/api/courses/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteCourse(id: number): Promise<{ status: string }> {
    return request(`/admin/api/courses/${id}`, { method: 'DELETE' });
  },

  // ---- Episodes ----
  async listEpisodes(courseId: number): Promise<Episode[]> {
    return request(`/admin/api/courses/${courseId}/episodes`);
  },
  async createEpisode(courseId: number, body: Partial<Episode>): Promise<Episode> {
    return request(`/admin/api/courses/${courseId}/episodes`, { method: 'POST', body: JSON.stringify(body) });
  },
  async updateEpisode(id: number, body: Partial<Episode>): Promise<Episode> {
    return request(`/admin/api/episodes/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteEpisode(id: number): Promise<{ status: string }> {
    return request(`/admin/api/episodes/${id}`, { method: 'DELETE' });
  },
  async reorderEpisodes(ids: number[]): Promise<{ status: string }> {
    return request('/admin/api/episodes/reorder', { method: 'POST', body: JSON.stringify({ ids }) });
  },
  async bulkDeleteEpisodes(ids: number[]): Promise<{ status: string }> {
    return request('/admin/api/episodes/bulk-delete', { method: 'POST', body: JSON.stringify({ ids }) });
  },
  async bulkMoveEpisodes(ids: number[], chapterId: number): Promise<{ status: string }> {
    return request('/admin/api/episodes/bulk-move', { method: 'POST', body: JSON.stringify({ ids, chapter_id: chapterId }) });
  },

  // ---- Chapters ----
  async listChapters(courseId: number): Promise<Chapter[]> {
    return request(`/admin/api/courses/${courseId}/chapters`);
  },
  async createChapter(courseId: number, body: Partial<Chapter>): Promise<Chapter> {
    return request(`/admin/api/courses/${courseId}/chapters`, { method: 'POST', body: JSON.stringify(body) });
  },
  async updateChapter(id: number, body: Partial<Chapter>): Promise<Chapter> {
    return request(`/admin/api/chapters/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteChapter(id: number): Promise<{ status: string }> {
    return request(`/admin/api/chapters/${id}`, { method: 'DELETE' });
  },
  async reorderChapters(ids: number[]): Promise<{ status: string }> {
    return request('/admin/api/chapters/reorder', { method: 'POST', body: JSON.stringify({ ids }) });
  },

  // ---- Users ----
  async listUsers(): Promise<User[]> {
    return request('/admin/api/users');
  },
  async createUser(body: { nickname: string; avatar_url?: string; pin: string; role: string }): Promise<User> {
    return request('/admin/api/users', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateUser(id: number, body: { nickname: string; avatar_url?: string; pin?: string; role: string }): Promise<User> {
    return request(`/admin/api/users/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteUser(id: number): Promise<{ status: string }> {
    return request(`/admin/api/users/${id}`, { method: 'DELETE' });
  },
  async grantAccess(userId: number, courseId: number): Promise<{ status: string }> {
    return request('/admin/api/access', { method: 'POST', body: JSON.stringify({ user_id: userId, course_id: courseId }) });
  },
  async revokeAccess(userId: number, courseId: number): Promise<{ status: string }> {
    return request('/admin/api/access/revoke', { method: 'POST', body: JSON.stringify({ user_id: userId, course_id: courseId }) });
  },
  async bulkAccess(userId: number, action: 'grant_all' | 'revoke_all'): Promise<{ status: string }> {
    return request(`/admin/api/users/${userId}/access/bulk`, { method: 'POST', body: JSON.stringify({ action }) });
  },
  async userLedger(userId: number, limit = 20): Promise<PointsLedgerEntry[]> {
    return request(`/admin/api/users/${userId}/ledger${qs({ limit })}`);
  },
  async userBadges(userId: number): Promise<Badge[]> {
    return request(`/admin/api/users/${userId}/badges`);
  },

  // ---- User device sessions (admin manages which devices are logged in) ----
  async listUserSessions(userId: number): Promise<UserSession[]> {
    return request(`/admin/api/users/${userId}/sessions`);
  },
  async revokeUserSession(userId: number, token: string): Promise<{ status: string }> {
    return request(`/admin/api/users/${userId}/sessions/${token}`, { method: 'DELETE' });
  },
  async revokeAllUserSessions(userId: number): Promise<{ status: string }> {
    return request(`/admin/api/users/${userId}/sessions`, { method: 'DELETE' });
  },
  async updateSessionNote(token: string, note: string): Promise<{ status: string }> {
    return request(`/admin/api/sessions/${token}/note`, { method: 'PATCH', body: JSON.stringify({ note }) });
  },

  // ---- Watch history (admin per-day timeline + heatmap) ----
  // userWatchHistory: per-day totals over [from, to). from/to are YYYY-MM-DD.
  // `to` is exclusive on the server side; pass the day AFTER the last wanted.
  async userWatchHistory(userId: number, from: string, to: string): Promise<WatchHistoryDay[]> {
    return request(`/admin/api/users/${userId}/watch-history${qs({ from, to })}`);
  },
  // userWatchEvents: the selected day's timeline rows (titles denormalized).
  async userWatchEvents(userId: number, day: string): Promise<WatchEventDTO[]> {
    return request(`/admin/api/users/${userId}/watch-events${qs({ day })}`);
  },

  // ---- Badges ----
  // Admin endpoints return the raw Go model (PascalCase fields), but create/
  // update accept snake_case keys matching the Go handler's json tags, so the
  // request body is left loosely typed.
  async listBadges(): Promise<AdminBadge[]> {
    return request('/admin/api/badges');
  },
  async createBadge(body: Record<string, unknown>): Promise<AdminBadge> {
    return request('/admin/api/badges', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateBadge(id: number, body: Record<string, unknown>): Promise<AdminBadge> {
    return request(`/admin/api/badges/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteBadge(id: number): Promise<{ status: string }> {
    return request(`/admin/api/badges/${id}`, { method: 'DELETE' });
  },

  // ---- Subjects ----
  async listSubjects(): Promise<SubjectMeta[]> {
    return request('/admin/api/subjects');
  },
  async createSubject(body: Partial<SubjectMeta>): Promise<SubjectMeta> {
    return request('/admin/api/subjects', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateSubject(id: number, body: Partial<SubjectMeta>): Promise<SubjectMeta> {
    return request(`/admin/api/subjects/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteSubject(id: number): Promise<{ status: string }> {
    return request(`/admin/api/subjects/${id}`, { method: 'DELETE' });
  },

  // ---- Tags ----
  async listTags(): Promise<TagMeta[]> {
    return request('/admin/api/tags');
  },
  async createTag(body: Partial<TagMeta>): Promise<TagMeta> {
    return request('/admin/api/tags', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateTag(id: number, body: Partial<TagMeta>): Promise<TagMeta> {
    return request(`/admin/api/tags/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteTag(id: number): Promise<{ status: string }> {
    return request(`/admin/api/tags/${id}`, { method: 'DELETE' });
  },

  // ---- Subtitles ----
  async listSubtitles(episodeId: number): Promise<Subtitle[]> {
    return request(`/admin/api/episodes/${episodeId}/subtitles`);
  },
  async getSubtitle(id: number): Promise<SubtitleDetail> {
    return request(`/admin/api/subtitles/${id}`);
  },
  async saveSubtitle(episodeId: number, body: { language: string; label: string; srt_content: string }): Promise<{ status: string }> {
    return request(`/admin/api/episodes/${episodeId}/subtitles`, { method: 'POST', body: JSON.stringify(body) });
  },
  async deleteSubtitle(id: number): Promise<{ status: string }> {
    return request(`/admin/api/subtitles/${id}`, { method: 'DELETE' });
  },
  async autoMatchSubtitle(form: FormData): Promise<{ status: string; episode_id: number; title: string }> {
    return request('/admin/api/subtitles/auto-match', { method: 'POST', headers: {} as Record<string, string>, body: form });
  },

  // ---- Import / Storage ----
  async scanPath(path: string, sourceId?: number): Promise<FileInfo[]> {
    return request(`/admin/api/import/scan${qs({ path, source_id: sourceId })}`);
  },
  async previewTree(path: string, sourceId?: number): Promise<ImportPreviewNode> {
    return request(`/admin/api/import/preview-tree${qs({ path, source_id: sourceId })}`);
  },
  async executeImport(body: unknown): Promise<{ status: string }> {
    return request('/admin/api/import/execute', { method: 'POST', body: JSON.stringify(body) });
  },

  // ---- Storage sources (multi-source CRUD + per-source ping) ----
  async listStorageSources(): Promise<StorageSource[]> {
    return request('/admin/api/storage-sources');
  },
  async createStorageSource(body: StorageSource): Promise<StorageSource> {
    return request('/admin/api/storage-sources', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateStorageSource(id: number, body: StorageSource): Promise<StorageSource> {
    return request(`/admin/api/storage-sources/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteStorageSource(id: number): Promise<{ status: string }> {
    return request(`/admin/api/storage-sources/${id}`, { method: 'DELETE' });
  },
  async pingStorageSource(id: number): Promise<{ status: string; message: string }> {
    return request(`/admin/api/storage-sources/${id}/ping`, { method: 'POST' });
  },

  // ---- Per-user storage-source whitelist ----
  async getStorageWhitelist(userId: number): Promise<number[]> {
    return request(`/admin/api/users/${userId}/storage-whitelist`);
  },
  async setStorageWhitelist(userId: number, sourceIds: number[]): Promise<{ status: string }> {
    return request(`/admin/api/users/${userId}/storage-whitelist`, { method: 'PUT', body: JSON.stringify({ source_ids: sourceIds }) });
  },

  // ---- Settings ----
  async getSettings(): Promise<Record<string, never>> {
    return request('/admin/api/settings');
  },
  async updateSettings(body: { admin_password?: string }): Promise<{ status: string; message: string }> {
    return request('/admin/api/settings', { method: 'PUT', body: JSON.stringify(body) });
  },

  // ---- Attachments ----
  async scanAttachments(type: 'course' | 'chapter' | 'episode', id: number): Promise<{ path: string; files: FileInfo[] }> {
    return request(`/admin/api/scan-attachments${qs({ type, id })}`);
  },

  // ---- Probe ----
  async scanMissingDurations(): Promise<{ status: string; queued: number; total: number; message: string }> {
    return request('/admin/api/probe/scan-missing', { method: 'POST' });
  },
  async probeProgress(): Promise<ProbeStats> {
    return request('/admin/api/probe/progress');
  },

  // ---- Subtitle generation queue ----
  async enqueueSubtitleJobs(episodeIds: number[], priority = 0): Promise<SubtitleJobEnqueueResult> {
    return request('/admin/api/subtitle-jobs', { method: 'POST', body: JSON.stringify({ episode_ids: episodeIds, priority }) });
  },
  async listSubtitleJobs(status?: string): Promise<SubtitleJob[]> {
    return request(`/admin/api/subtitle-jobs${qs({ status })}`);
  },
  async subtitleJobStats(): Promise<SubtitleJobStats> {
    return request('/admin/api/subtitle-jobs/stats');
  },
  async skipSubtitleJob(id: number): Promise<{ status: string }> {
    return request(`/admin/api/subtitle-jobs/${id}/skip`, { method: 'POST' });
  },
  async retrySubtitleJob(id: number): Promise<{ status: string }> {
    return request(`/admin/api/subtitle-jobs/${id}/retry`, { method: 'POST' });
  },

  // ---- Unlock (drip schedule) ----
  async getUnlockTemplate(courseId: number): Promise<UnlockTemplate> {
    return request(`/admin/api/courses/${courseId}/unlock-template`);
  },
  async saveUnlockTemplate(courseId: number, body: { strategy: string; interval_seconds: number; weekly_times: unknown[] }): Promise<UnlockTemplate> {
    return request(`/admin/api/courses/${courseId}/unlock-template`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteUnlockTemplate(courseId: number): Promise<{ status: string }> {
    return request(`/admin/api/courses/${courseId}/unlock-template`, { method: 'DELETE' });
  },
  async listUserOverrides(userId: number): Promise<UnlockOverride[]> {
    return request(`/admin/api/users/${userId}/unlock-overrides`);
  },
  async getUnlockOverride(userId: number, courseId: number): Promise<UnlockOverride> {
    return request(`/admin/api/users/${userId}/courses/${courseId}/unlock-override`);
  },
  async saveUnlockOverride(userId: number, courseId: number, body: { strategy: string; interval_seconds: number; weekly_times: unknown[]; allowed_episode_ids: number[] }): Promise<UnlockOverride> {
    return request(`/admin/api/users/${userId}/courses/${courseId}/unlock-override`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteUnlockOverride(userId: number, courseId: number): Promise<{ status: string }> {
    return request(`/admin/api/users/${userId}/courses/${courseId}/unlock-override`, { method: 'DELETE' });
  },
  async manualUnlock(userId: number, courseId: number): Promise<{ status: string }> {
    return request(`/admin/api/users/${userId}/courses/${courseId}/manual-unlock`, { method: 'POST' });
  },
  async manualUnlockUndo(userId: number, courseId: number): Promise<{ status: string }> {
    return request(`/admin/api/users/${userId}/courses/${courseId}/manual-unlock-undo`, { method: 'POST' });
  },
  async setAllowedEpisodes(userId: number, courseId: number, ids: number[]): Promise<{ status: string }> {
    return request(`/admin/api/users/${userId}/courses/${courseId}/allowed-episodes`, { method: 'PUT', body: JSON.stringify({ allowed_episode_ids: ids }) });
  },
  async unlockPreview(userId: number, courseId: number): Promise<UnlockPreview> {
    return request(`/admin/api/users/${userId}/courses/${courseId}/unlock-preview`);
  },

  // ---- Uploads ----
  async uploadImage(file: File): Promise<{ url: string }> {
    const fd = new FormData();
    fd.append('file', file);
    return request('/admin/api/upload/image', { method: 'POST', headers: {} as Record<string, string>, body: fd });
  },

  // ---- App releases (APK OTA distribution) ----
  async listReleases(): Promise<AppRelease[]> {
    return request('/admin/api/releases');
  },
  async uploadRelease(body: {
    file: File;
    version_code: number;
    version_name: string;
    abi: string;
    force_update: boolean;
    release_notes: string;
  }): Promise<AppRelease> {
    const fd = new FormData();
    fd.append('file', body.file);
    fd.append('version_code', String(body.version_code));
    fd.append('version_name', body.version_name);
    fd.append('abi', body.abi);
    fd.append('force_update', String(body.force_update));
    fd.append('release_notes', body.release_notes);
    return request('/admin/api/releases/upload', {
      method: 'POST',
      headers: {} as Record<string, string>,
      body: fd,
    });
  },
  async updateRelease(id: number, body: Partial<Pick<AppRelease, 'release_notes' | 'force_update' | 'is_active'>>): Promise<AppRelease> {
    return request(`/admin/api/releases/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteRelease(id: number): Promise<{ status: string }> {
    return request(`/admin/api/releases/${id}`, { method: 'DELETE' });
  },

  // ---- Reading Room ----
  // Series
  async listReadingSeries(): Promise<ReadingSeries[]> {
    return request('/admin/api/reading-series');
  },
  async getReadingSeriesDetail(id: number): Promise<ReadingSeriesDetail> {
    return request(`/admin/api/reading-series/${id}/detail`);
  },
  async createReadingSeries(body: {
    title: string; description: string; grade: string; subject: string;
    cover_url: string; sort_order: number; tag_ids: number[];
  }): Promise<ReadingSeries> {
    return request('/admin/api/reading-series', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateReadingSeries(id: number, body: {
    title: string; description: string; grade: string; subject: string;
    cover_url: string; sort_order: number; tag_ids: number[];
  }): Promise<ReadingSeries> {
    return request(`/admin/api/reading-series/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteReadingSeries(id: number): Promise<{ status: string }> {
    return request(`/admin/api/reading-series/${id}`, { method: 'DELETE' });
  },
  // Books (PDF)
  async listReadingBooks(): Promise<ReadingBook[]> {
    return request('/admin/api/reading-books');
  },
  async createReadingBook(body: {
    series_id: number; sort_order: number; title: string; file_relative_path: string;
    cover_url: string; grade: string; subject: string; tag_ids: number[];
  }): Promise<ReadingBook> {
    return request('/admin/api/reading-books', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateReadingBook(id: number, body: {
    series_id: number; sort_order: number; title: string; file_relative_path: string;
    cover_url: string; grade: string; subject: string; tag_ids: number[];
  }): Promise<ReadingBook> {
    return request(`/admin/api/reading-books/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteReadingBook(id: number): Promise<{ status: string }> {
    return request(`/admin/api/reading-books/${id}`, { method: 'DELETE' });
  },
  // Articles (web)
  async listReadingArticles(): Promise<ReadingArticle[]> {
    return request('/admin/api/reading-articles');
  },
  async createReadingArticle(body: {
    series_id: number; sort_order: number; title: string; source_url: string;
    whitelist_domains: string; cover_url: string; grade: string; subject: string; tag_ids: number[];
  }): Promise<ReadingArticle> {
    return request('/admin/api/reading-articles', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateReadingArticle(id: number, body: {
    series_id: number; sort_order: number; title: string; source_url: string;
    whitelist_domains: string; cover_url: string; grade: string; subject: string; tag_ids: number[];
  }): Promise<ReadingArticle> {
    return request(`/admin/api/reading-articles/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteReadingArticle(id: number): Promise<{ status: string }> {
    return request(`/admin/api/reading-articles/${id}`, { method: 'DELETE' });
  },
  // Access (series / book / article)
  async grantReadingAccess(userId: number, targetType: ReadingTargetType, targetId: number): Promise<{ status: string }> {
    return request('/admin/api/reading-access', { method: 'POST', body: JSON.stringify({ user_id: userId, target_type: targetType, target_id: targetId }) });
  },
  async revokeReadingAccess(userId: number, targetType: ReadingTargetType, targetId: number): Promise<{ status: string }> {
    return request('/admin/api/reading-access/revoke', { method: 'POST', body: JSON.stringify({ user_id: userId, target_type: targetType, target_id: targetId }) });
  },
  async bulkReadingAccess(userId: number, action: 'grant_all' | 'revoke_all'): Promise<{ status: string }> {
    return request(`/admin/api/users/${userId}/reading-access/bulk`, { method: 'POST', body: JSON.stringify({ action }) });
  },
  // Reading Room — folder import
  async previewReadingImport(path: string): Promise<unknown> {
    return request(`/admin/api/reading-import/preview-tree${qs({ path })}`);
  },
  async executeReadingImport(body: unknown): Promise<{ status: string }> {
    return request('/admin/api/reading-import/execute', { method: 'POST', body: JSON.stringify(body) });
  },
  async suggestWhitelist(sourceUrl: string): Promise<{ domains: string[] }> {
    return request('/admin/api/reading-articles/suggest-whitelist', { method: 'POST', body: JSON.stringify({ source_url: sourceUrl }) });
  },

  // ---- AI ----
  // AI provider CRUD + per-provider connectivity test. Mirrors the
  // storage-sources section: list/create/update/delete + a ping-style test.
  async listAiProviders(): Promise<AiProvider[]> {
    return request('/admin/api/ai/providers');
  },
  async createAiProvider(body: AiProvider): Promise<AiProvider> {
    return request('/admin/api/ai/providers', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateAiProvider(id: number, body: AiProvider): Promise<AiProvider> {
    return request(`/admin/api/ai/providers/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteAiProvider(id: number): Promise<{ status: string }> {
    return request(`/admin/api/ai/providers/${id}`, { method: 'DELETE' });
  },
  async testAiProvider(id: number): Promise<AiProviderTestResult> {
    return request(`/admin/api/ai/providers/${id}/test`, { method: 'POST' });
  },

  // AI workflow jobs (slice/summarize/etc). The list endpoint rolls up status
  // counts alongside the jobs, so the page gets both in one round-trip.
  async enqueueAiJobs(jobType: string, episodeIds: number[]): Promise<AiJobEnqueueResult> {
    return request('/admin/api/ai/jobs', { method: 'POST', body: JSON.stringify({ job_type: jobType, episode_ids: episodeIds }) });
  },
  async listAiJobs(jobType?: string, status?: string): Promise<AiJobsResponse> {
    return request(`/admin/api/ai/jobs${qs({ job_type: jobType, status })}`);
  },
  async getAiJob(id: number): Promise<{ job: AiJob; runs: AiRun[] }> {
    return request(`/admin/api/ai/jobs/${id}`);
  },
  // Manually reset one stuck 'processing' job back to 'queued' (the admin
  // counterpart of the automatic reaper). Throws on a 409 when the job isn't
  // currently processing (already finished or was reaped) — the caller surfaces
  // that as a benign "nothing to reset" toast.
  async resetAiJob(id: number): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/jobs/${id}/reset`, { method: 'POST' });
  },
  // Decision-trace runs: the recorded model invocations an agent made. limit
  // caps the window (the page shows the most recent N).
  async listAiRuns(limit = 20): Promise<AiRun[]> {
    return request(`/admin/api/ai/runs${qs({ limit })}`);
  },
  async getAiRun(id: number): Promise<AiRun> {
    return request(`/admin/api/ai/runs/${id}`);
  },

  // Phase C — quiz observability. The admin reads a generated summary, lists a
  // user's quizzes, and drills into one quiz's full detail (questions + answers
  // + memory + the agent's reasoning trace + its feedback).
  async getAiSummary(episodeId: number): Promise<AiSummaryRow> {
    return request(`/admin/api/ai/summaries/${episodeId}`);
  },
  async listUserQuizzes(userId: number): Promise<AiQuizRow[]> {
    return request(`/admin/api/ai/users/${userId}/quizzes`);
  },
  async getQuizDetail(quizId: number): Promise<AiQuizDetail> {
    return request(`/admin/api/ai/quizzes/${quizId}`);
  },
};
