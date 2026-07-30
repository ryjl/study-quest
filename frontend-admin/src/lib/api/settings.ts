// Admin settings (password rotation + AI 性能 knobs).

import { request } from './_request';

export interface SettingsState {
  has_admin_password: boolean;
  polish_concurrency: string; // string from DB; backend stores "1".."10"
}

export const settings = {
  async getSettings(): Promise<SettingsState> {
    return request('/admin/api/settings', { method: 'GET' });
  },

  async updateSettings(body: {
    admin_password?: string;
    polish_concurrency?: number;
  }): Promise<{ status: string; message: string }> {
    return request('/admin/api/settings', { method: 'PUT', body: JSON.stringify(body) });
  },
};
