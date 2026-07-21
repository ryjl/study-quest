// Subjects CRUD.

import { request } from './_request';
import type { SubjectMeta } from '../types';

export const subjects = {
  async listSubjects(): Promise<SubjectMeta[]> {
    return request('/admin/api/subjects');
  },
  async createSubject(body: Partial<SubjectMeta>): Promise<SubjectMeta> {
    return request('/admin/api/subjects', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateSubject(id: number, body: Partial<SubjectMeta>): Promise<SubjectMeta> {
    return request(`/admin/api/subjects/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteSubject(id: number): Promise<{ status: string }> {
    return request(`/admin/api/subjects/${id}`, { method: 'DELETE' });
  },
};
