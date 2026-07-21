// Dashboard stats (top-level overview numbers).

import { request } from './_request';
import type { DashboardStats } from '../types';

export const dashboard = {
  async dashboardStats(): Promise<DashboardStats> {
    return request('/admin/api/stats/dashboard');
  },
};
