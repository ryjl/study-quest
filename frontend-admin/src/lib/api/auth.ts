// Auth + session endpoints.

import { request } from './_request';

export const auth = {
  async me(): Promise<{ authenticated: boolean }> {
    return request('/admin/api/me');
  },
  async login(password: string): Promise<{ status: string }> {
    return request('/admin/api/login', { method: 'POST', body: JSON.stringify({ password }) });
  },
  async logout(): Promise<{ status: string }> {
    return request('/admin/api/logout', { method: 'POST' });
  },
};
