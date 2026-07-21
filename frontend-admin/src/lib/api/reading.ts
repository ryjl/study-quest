// Reading Room — series / books (PDF) / articles (web) CRUD, per-user access
// grants (series / book / article), folder import, and whitelist suggestion.

import { request, qs } from './_request';
import type {
  ReadingArticle,
  ReadingBook,
  ReadingSeries,
  ReadingSeriesDetail,
  ReadingTargetType,
} from '../types';

export const reading = {
  // Series
  async listReadingSeries(): Promise<ReadingSeries[]> {
    return request('/admin/api/reading-series');
  },
  async getReadingSeriesDetail(id: number): Promise<ReadingSeriesDetail> {
    return request(`/admin/api/reading-series/${id}/detail`);
  },
  async createReadingSeries(body: {
    title: string;
    description: string;
    grade: string;
    subject: string;
    cover_url: string;
    sort_order: number;
    tag_ids: number[];
  }): Promise<ReadingSeries> {
    return request('/admin/api/reading-series', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateReadingSeries(id: number, body: {
    title: string;
    description: string;
    grade: string;
    subject: string;
    cover_url: string;
    sort_order: number;
    tag_ids: number[];
  }): Promise<ReadingSeries> {
    return request(`/admin/api/reading-series/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteReadingSeries(id: number): Promise<{ status: string }> {
    return request(`/admin/api/reading-series/${id}`, { method: 'DELETE' });
  },
  // Books (PDF)
  async listReadingBooks(): Promise<ReadingBook[]> {
    return request('/admin/api/reading-books');
  },
  async createReadingBook(body: {
    series_id: number;
    sort_order: number;
    title: string;
    file_relative_path: string;
    cover_url: string;
    grade: string;
    subject: string;
    tag_ids: number[];
  }): Promise<ReadingBook> {
    return request('/admin/api/reading-books', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateReadingBook(id: number, body: {
    series_id: number;
    sort_order: number;
    title: string;
    file_relative_path: string;
    cover_url: string;
    grade: string;
    subject: string;
    tag_ids: number[];
  }): Promise<ReadingBook> {
    return request(`/admin/api/reading-books/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteReadingBook(id: number): Promise<{ status: string }> {
    return request(`/admin/api/reading-books/${id}`, { method: 'DELETE' });
  },
  // Articles (web)
  async listReadingArticles(): Promise<ReadingArticle[]> {
    return request('/admin/api/reading-articles');
  },
  async createReadingArticle(body: {
    series_id: number;
    sort_order: number;
    title: string;
    source_url: string;
    whitelist_domains: string;
    cover_url: string;
    grade: string;
    subject: string;
    tag_ids: number[];
  }): Promise<ReadingArticle> {
    return request('/admin/api/reading-articles', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateReadingArticle(id: number, body: {
    series_id: number;
    sort_order: number;
    title: string;
    source_url: string;
    whitelist_domains: string;
    cover_url: string;
    grade: string;
    subject: string;
    tag_ids: number[];
  }): Promise<ReadingArticle> {
    return request(`/admin/api/reading-articles/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteReadingArticle(id: number): Promise<{ status: string }> {
    return request(`/admin/api/reading-articles/${id}`, { method: 'DELETE' });
  },
  // Access (series / book / article)
  async grantReadingAccess(
    userId: number,
    targetType: ReadingTargetType,
    targetId: number,
  ): Promise<{ status: string }> {
    return request('/admin/api/reading-access', {
      method: 'POST',
      body: JSON.stringify({ user_id: userId, target_type: targetType, target_id: targetId }),
    });
  },
  async revokeReadingAccess(
    userId: number,
    targetType: ReadingTargetType,
    targetId: number,
  ): Promise<{ status: string }> {
    return request('/admin/api/reading-access/revoke', {
      method: 'POST',
      body: JSON.stringify({ user_id: userId, target_type: targetType, target_id: targetId }),
    });
  },
  // Reading Room — folder import
  async previewReadingImport(path: string): Promise<unknown> {
    return request(`/admin/api/reading-import/preview-tree${qs({ path })}`);
  },
  async executeReadingImport(body: unknown): Promise<{ status: string }> {
    return request('/admin/api/reading-import/execute', { method: 'POST', body: JSON.stringify(body) });
  },
  async suggestWhitelist(sourceUrl: string): Promise<{ domains: string[] }> {
    return request('/admin/api/reading-articles/suggest-whitelist', {
      method: 'POST',
      body: JSON.stringify({ source_url: sourceUrl }),
    });
  },
};
