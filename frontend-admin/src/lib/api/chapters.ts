// Chapters (course sub-grouping) CRUD.

import { request } from './_request';
import type { Chapter } from '../types';

export const chapters = {
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
};
