// Badges CRUD. Admin endpoints return the raw Go model (PascalCase fields),
// but create/update accept snake_case keys matching the Go handler's json tags,
// so the request body is left loosely typed.

import { request } from './_request';
import type { AdminBadge } from '../types';

export const badges = {
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
};
