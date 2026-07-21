// Storage sources (multi-source CRUD + per-source ping).

import { request } from './_request';
import type { StorageSource } from '../types';

export const storage = {
  async listStorageSources(): Promise<StorageSource[]> {
    return request('/admin/api/storage-sources');
  },
  async createStorageSource(body: StorageSource): Promise<StorageSource> {
    return request('/admin/api/storage-sources', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateStorageSource(id: number, body: StorageSource): Promise<StorageSource> {
    return request(`/admin/api/storage-sources/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteStorageSource(id: number): Promise<{ status: string }> {
    return request(`/admin/api/storage-sources/${id}`, { method: 'DELETE' });
  },
  async pingStorageSource(id: number): Promise<{ status: string; message: string }> {
    return request(`/admin/api/storage-sources/${id}/ping`, { method: 'POST' });
  },
};
