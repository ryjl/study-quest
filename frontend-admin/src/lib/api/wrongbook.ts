import { request } from './_request';
import type { WrongBookStats } from '../types';

// wrongbook.ts — 错题本观测 API for the /admin/wrong-book page (TODO.md P0).
// Thin wrapper over the backend /admin/api/wrong-book/stats endpoint. Each
// aggregate degrades independently server-side, so the response always carries
// all fields (zeros on failure).

export const wrongbook = {
  /** 错题本全局统计 + 高频错题榜 + 科目分布。AI 未配置时返回零值。 */
  async wrongBookStats(): Promise<WrongBookStats> {
    return request('/admin/api/wrong-book/stats');
  },
};
