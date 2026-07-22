// Grade tag management (open-tag-system CRUD: list with usage counts, rename,
// merge, delete). Distinct from the per-course GradePicker, which creates tags
// implicitly by saving a course with a new tag value. This module is for the
// LATER management surface the admin needs once tags accumulate.

import { request } from './_request';

export interface GradeUsage {
  grade: string;
  label: string;
  count: number;
  is_preset: boolean;
}

export const grades = {
  /** List every grade tag (presets first, then customs alphabetically) with
   * reference counts across all four grade tables. Presets with 0 uses still
   * appear (they're always-available options). */
  async listGrades(): Promise<GradeUsage[]> {
    return request('/admin/api/grades');
  },
  /** Rename a custom tag across all four tables. Refuses presets (409). */
  async renameGrade(from: string, newKey: string): Promise<{ status: string }> {
    return request(`/admin/api/grades/${encodeURIComponent(from)}`, {
      method: 'PUT',
      body: JSON.stringify({ new_key: newKey }),
    });
  },
  /** Merge `from` into `to` (move every row). Unlike rename, `from` MAY be a
   * preset — this is the migration path for deprecated presets (adult→college). */
  async mergeGrades(from: string, to: string): Promise<{ status: string }> {
    return request('/admin/api/grades/merge', {
      method: 'POST',
      body: JSON.stringify({ from, to }),
    });
  },
  /** Delete a tag with 0 uses. Refuses presets + in-use tags (409). */
  async deleteGrade(key: string): Promise<{ status: string }> {
    return request(`/admin/api/grades/${encodeURIComponent(key)}`, { method: 'DELETE' });
  },
};
