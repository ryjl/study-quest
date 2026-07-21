// App releases (APK OTA distribution).

import { request } from './_request';
import type { AppRelease } from '../types';

export const releases = {
  async listReleases(): Promise<AppRelease[]> {
    return request('/admin/api/releases');
  },
  async uploadRelease(body: {
    file: File;
    version_code: number;
    version_name: string;
    abi: string;
    force_update: boolean;
    release_notes: string;
  }): Promise<AppRelease> {
    const fd = new FormData();
    fd.append('file', body.file);
    fd.append('version_code', String(body.version_code));
    fd.append('version_name', body.version_name);
    fd.append('abi', body.abi);
    fd.append('force_update', String(body.force_update));
    fd.append('release_notes', body.release_notes);
    return request('/admin/api/releases/upload', {
      method: 'POST',
      headers: {} as Record<string, string>,
      body: fd,
    });
  },
  async updateRelease(
    id: number,
    body: Partial<Pick<AppRelease, 'release_notes' | 'force_update' | 'is_active'>>,
  ): Promise<AppRelease> {
    return request(`/admin/api/releases/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteRelease(id: number): Promise<{ status: string }> {
    return request(`/admin/api/releases/${id}`, { method: 'DELETE' });
  },
};
