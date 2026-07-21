// Episodes CRUD + bulk ops (reorder / bulk-delete / bulk-move across chapters).

import { request } from './_request';
import type { Episode } from '../types';

export const episodes = {
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
    return request('/admin/api/episodes/bulk-move', {
      method: 'POST',
      body: JSON.stringify({ ids, chapter_id: chapterId }),
    });
  },
};
