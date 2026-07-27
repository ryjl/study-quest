import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Plus, FolderInput, Bot } from 'lucide-react';
import { CoursesContent } from './courses/CoursesContent';
import { CreateEditCourseModal } from './courses/CourseModal';
import { ImportDialog } from './courses/ImportDialog';
import { PageHeader } from '../components/PageHeader';
import type { Course } from '../lib/types';

export function Courses() {
  const navigate = useNavigate();
  const [editing, setEditing] = useState<{ course?: Course; open: boolean }>({ open: false });
  const [refreshKey, setRefreshKey] = useState(0);
  const [importOpen, setImportOpen] = useState(false);

  return (
    <div>
      <PageHeader
        title="课程库管理"
        description="管理所有课程、章节与课时。点击卡片展开课时树。"
        actions={
          <div className="flex gap-2">
            {/* AI 工作台入口:从这里跳到课程工作台列表,再选具体课程进入它的 AI 工作台。
                课程库管理(CRUD)和 AI 工作台(围绕课程的 AI 事务)职责分离,不混在一页。 */}
            <button className="btn-secondary" onClick={() => navigate('/admin/ai/courses')} title="进入课程 AI 工作台">
              <Bot size={14} /> AI 工作台
            </button>
            <button className="btn-secondary" onClick={() => setImportOpen(true)}>
              <FolderInput size={14} /> 从文件夹导入
            </button>
            <button className="btn-primary" onClick={() => setEditing({ open: true })}>
              <Plus size={14} /> 新增课程库
            </button>
          </div>
        }
      />

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

      <ImportDialog
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onImported={() => setRefreshKey((k) => k + 1)}
      />
    </div>
  );
}
