import { request } from './_request';
import type { ExamStats } from '../types';

// exam.ts — 课程考试观测 API for the /admin/exam page (TODO.md P0)。
// Thin wrapper over the backend /admin/api/exam/stats endpoint。AI 未配置时
// 服务端返回零值(total/submitted=0, source_quality=[]),前端据此显示空态。

export const exam = {
  /** 课程考试全局统计 + 题源质量(pool vs generated 正确率对比)。AI 未配置时返回零值。 */
  async examStats(): Promise<ExamStats> {
    return request('/admin/api/exam/stats');
  },
};
