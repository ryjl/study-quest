// Probe (ffprobe duration/codec scan for episodes missing media metadata).

import { request } from './_request';
import type { ProbeStats } from '../types';

export const probe = {
  async scanMissingDurations(): Promise<{
    status: string;
    queued: number;
    total: number;
    message: string;
  }> {
    return request('/admin/api/probe/scan-missing', { method: 'POST' });
  },
  async probeProgress(): Promise<ProbeStats> {
    return request('/admin/api/probe/progress');
  },
};
