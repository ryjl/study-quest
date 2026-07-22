import { request, qs } from './_request';
import type { LogEntry } from '../types';

// logs.ts — structured-log read API for the /admin/logs page (TODO.md P1).
// Mirrors ai.ts's listAiRuns/listAiJobs shape: thin wrapper over the backend
// /admin/api/logs endpoint, returning enriched views (episode/course titles).

export const logs = {
  /** List recent log entries, optionally filtered. Newest first. */
  async listLogs(opts?: { level?: string; source?: string; jobId?: number; limit?: number }): Promise<LogEntry[]> {
    return request(`/admin/api/logs${qs({
      level: opts?.level,
      source: opts?.source,
      job_id: opts?.jobId,
      limit: opts?.limit,
    })}`);
  },
};
