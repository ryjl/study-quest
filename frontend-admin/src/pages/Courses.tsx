import { useState } from 'react';
import { CoursesContent } from './courses/CoursesContent';
import { CreateEditCourseModal } from './courses/CourseModal';
import type { Course } from '../lib/types';

export function Courses() {
  const [editing, setEditing] = useState<{ course?: Course; open: boolean }>({ open: false });
  const [refreshKey, setRefreshKey] = useState(0);

  return (
    <div>
      <div className="mb-6 flex items-center justify-between border-b border-border pb-4">
        <h1 className="text-2xl font-bold text-txt">课程库管理</h1>
        <button className="btn-primary" onClick={() => setEditing({ open: true })}>
          + 新增课程库
        </button>
      </div>

      <CoursesContent key={refreshKey} onEdit={(c) => setEditing({ course: c, open: true })} onChanged={() => setRefreshKey((k) => k + 1)} />

      <CreateEditCourseModal
        open={editing.open}
        course={editing.course}
        onClose={() => setEditing({ open: false })}
        onSaved={() => {
          setEditing({ open: false });
          setRefreshKey((k) => k + 1);
        }}
      />
    </div>
  );
}
