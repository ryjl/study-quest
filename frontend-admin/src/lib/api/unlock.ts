// Unlock (drip schedule) — per-course templates + per-user overrides +
// manual unlock / allowed-episode overrides + unlock preview.

import { request } from './_request';
import type { UnlockOverride, UnlockPreview, UnlockTemplate } from '../types';

export const unlock = {
  async getUnlockTemplate(courseId: number): Promise<UnlockTemplate> {
    return request(`/admin/api/courses/${courseId}/unlock-template`);
  },
  async saveUnlockTemplate(
    courseId: number,
    body: { strategy: string; interval_seconds: number; weekly_times: unknown[] },
  ): Promise<UnlockTemplate> {
    return request(`/admin/api/courses/${courseId}/unlock-template`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteUnlockTemplate(courseId: number): Promise<{ status: string }> {
    return request(`/admin/api/courses/${courseId}/unlock-template`, { method: 'DELETE' });
  },
  async getUnlockOverride(userId: number, courseId: number): Promise<UnlockOverride> {
    return request(`/admin/api/users/${userId}/courses/${courseId}/unlock-override`);
  },
  async saveUnlockOverride(
    userId: number,
    courseId: number,
    body: {
      strategy: string;
      interval_seconds: number;
      weekly_times: unknown[];
      allowed_episode_ids: number[];
    },
  ): Promise<UnlockOverride> {
    return request(`/admin/api/users/${userId}/courses/${courseId}/unlock-override`, {
      method: 'PUT',
      body: JSON.stringify(body),
    });
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
    return request(`/admin/api/users/${userId}/courses/${courseId}/allowed-episodes`, {
      method: 'PUT',
      body: JSON.stringify({ allowed_episode_ids: ids }),
    });
  },
  async unlockPreview(userId: number, courseId: number): Promise<UnlockPreview> {
    return request(`/admin/api/users/${userId}/courses/${courseId}/unlock-preview`);
  },
};
