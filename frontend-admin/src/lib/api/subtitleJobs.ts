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
};
