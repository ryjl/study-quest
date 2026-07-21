// Admin settings (password rotation).

import { request } from './_request';

export const settings = {
  async updateSettings(body: {
    admin_password?: string;
  }): Promise<{ status: string; message: string }> {
    return request('/admin/api/settings', { method: 'PUT', body: JSON.stringify(body) });
  },
};
