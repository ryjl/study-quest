// Course video import (scan / preview-tree / execute).

import { request, qs } from './_request';
import type { FileInfo, ImportPreviewNode } from '../types';

export const imports = {
  async scanPath(path: string, sourceId?: number): Promise<FileInfo[]> {
    return request(`/admin/api/import/scan${qs({ path, source_id: sourceId })}`);
  },
  async previewTree(path: string, sourceId?: number): Promise<ImportPreviewNode> {
    return request(`/admin/api/import/preview-tree${qs({ path, source_id: sourceId })}`);
  },
  async executeImport(body: unknown): Promise<{ status: string }> {
    return request('/admin/api/import/execute', { method: 'POST', body: JSON.stringify(body) });
  },
};
