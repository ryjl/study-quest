// Courses CRUD.

import { request } from './_request';
import type { Course } from '../types';

export const courses = {
  async listCourses(): Promise<Course[]> {
    return request('/admin/api/courses');
  },
  async listGradeTags(): Promise<{ key: string; label: string; preset: boolean }[]> {
    return request('/admin/api/courses/grade-tags');
  },
  async createCourse(body: Partial<Course>): Promise<Course> {
    return request('/admin/api/courses', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateCourse(id: number, body: Partial<Course>): Promise<Course> {
    return request(`/admin/api/courses/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async deleteCourse(id: number): Promise<{ status: string }> {
    return request(`/admin/api/courses/${id}`, { method: 'DELETE' });
  },
};
