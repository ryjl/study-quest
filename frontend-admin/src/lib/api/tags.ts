// Tags CRUD.

import { request } from './_request';
import type { TagMeta } from '../types';

export const tags = {
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
};
