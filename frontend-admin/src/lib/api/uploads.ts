// Image upload (multipart). Empty headers object overrides the JSON
// Content-Type so the browser can set the correct multipart boundary.

import { request } from './_request';

export const uploads = {
  async uploadImage(file: File): Promise<{ url: string }> {
    const fd = new FormData();
    fd.append('file', file);
    return request('/admin/api/upload/image', {
      method: 'POST',
      headers: {} as Record<string, string>,
      body: fd,
    });
  },
};
