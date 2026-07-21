// Users CRUD + course access grants + ledger/badges + device-session management
// + per-day watch history + per-user storage-source whitelist.

import { request, qs } from './_request';
import type { Badge, PointsLedgerEntry, User, UserSession, WatchEventDTO, WatchHistoryDay } from '../types';

export const users = {
  async listUsers(): Promise<User[]> {
    return request('/admin/api/users');
  },
  async createUser(body: {
    nickname: string;
    avatar_url?: string;
    pin: string;
    role: string;
  }): Promise<User> {
    return request('/admin/api/users', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateUser(
    id: number,
    body: { nickname: string; avatar_url?: string; pin?: string; role: string },
  ): Promise<User> {
    return request(`/admin/api/users/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteUser(id: number): Promise<{ status: string }> {
    return request(`/admin/api/users/${id}`, { method: 'DELETE' });
  },
  async grantAccess(userId: number, courseId: number): Promise<{ status: string }> {
    return request('/admin/api/access', {
      method: 'POST',
      body: JSON.stringify({ user_id: userId, course_id: courseId }),
    });
  },
  async revokeAccess(userId: number, courseId: number): Promise<{ status: string }> {
    return request('/admin/api/access/revoke', {
      method: 'POST',
      body: JSON.stringify({ user_id: userId, course_id: courseId }),
    });
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

  // ---- Per-user storage-source whitelist ----
  async setStorageWhitelist(userId: number, sourceIds: number[]): Promise<{ status: string }> {
    return request(`/admin/api/users/${userId}/storage-whitelist`, {
      method: 'PUT',
      body: JSON.stringify({ source_ids: sourceIds }),
    });
  },
};
