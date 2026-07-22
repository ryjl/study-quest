// Subtitle generation queue (Whisper jobs).

import { request, qs } from './_request';
import type { SubtitleJob, SubtitleJobEnqueueResult, SubtitleJobStats } from '../types';

export const subtitleJobs = {
  async enqueueSubtitleJobs(episodeIds: number[], priority = 0): Promise<SubtitleJobEnqueueResult> {
    return request('/admin/api/subtitle-jobs', {
      method: 'POST',
      body: JSON.stringify({ episode_ids: episodeIds, priority }),
    });
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
  /** Reset a stuck processing job back to queued — manual reaper. Mirrors
   * ai.resetAiJob. Use when the worker is alive but a relay/network call hung
   * and the admin has judged the job stuck before the auto-reaper kicks in. */
  async resetSubtitleJob(id: number): Promise<{ status: string }> {
    return request(`/admin/api/subtitle-jobs/${id}/reset`, { method: 'POST' });
  },
};
