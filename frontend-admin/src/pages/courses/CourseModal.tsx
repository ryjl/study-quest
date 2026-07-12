import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { type Course } from '../../lib/types';
import { useSubjects } from '../../lib/useSubjects';
import { Modal } from '../../components/ui';
import { GradePicker } from '../../components/inputs';
import { TagInput } from '../../components/TagInput';
import { ImageUpload } from '../../components/inputs';
import { useToast } from '../../lib/toast';

export function CreateEditCourseModal({
  open,
  course,
  onClose,
  onSaved,
}: {
  open: boolean;
  course?: Course;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEdit = !!course;
  const qc = useQueryClient();
  const toast = useToast();
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];

  const [title, setTitle] = useState('');
  const [grade, setGrade] = useState('');
  const [subject, setSubject] = useState('');
  const [contentType, setContentType] = useState<'learning' | 'entertainment'>('learning');
  const [coverUrl, setCoverUrl] = useState('');
  const [tagIDs, setTagIDs] = useState<number[]>([]);

  useEffect(() => {
    if (open) {
      setTitle(course?.title ?? '');
      setGrade(course?.grade ?? '');
      const ct = (course?.content_type === 'entertainment' ? 'entertainment' : 'learning') as 'learning' | 'entertainment';
      setContentType(ct);
      // Entertainment courses are pinned to the "entertainment" subject.
      setSubject(ct === 'entertainment' ? 'entertainment' : (course?.subject ?? subjects[0]?.key ?? ''));
      setCoverUrl(course?.cover_url ?? '');
      setTagIDs(course?.tag_ids ?? []);
    }
  }, [open, course, subjects]);

  const isEntertainment = contentType === 'entertainment';

  const saveMut = useMutation({
    mutationFn: async () => {
      if (!title.trim()) throw new Error('请输入课程名称');
      const grades = grade.split(',').map((g) => g.trim()).filter(Boolean);
      if (grades.length === 0) throw new Error('请至少选择一个适用年级');
      const body = {
        title: title.trim(),
        grade,
        subject: isEntertainment ? 'entertainment' : subject,
        content_type: contentType,
        cover_url: coverUrl,
        tag_ids: tagIDs,
      };
      if (isEdit && course) return api.updateCourse(course.id, body);
      return api.createCourse(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '课程已更新' : '课程已创建');
      qc.invalidateQueries({ queryKey: ['courses'] });
      onSaved();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <Modal open={open} onClose={onClose} title={isEdit ? '编辑课程' : '新增课程库'} size="md">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">课程名称</label>
          <input className="input" placeholder="如：神奇的物理世界" value={title} onChange={(e) => setTitle(e.target.value)} required autoFocus />
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted">适用年级（可多选）</label>
          <GradePicker value={grade} onChange={setGrade} />
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted">内容类型</label>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => { setContentType('learning'); setSubject(course?.subject ?? subjects[0]?.key ?? ''); }}
              className={`flex-1 rounded-lg border px-3 py-2 text-sm ${!isEntertainment ? 'border-blue-500 bg-blue-50 text-blue-700' : 'border-gray-200 text-gray-600'}`}
            >
              📚 学习
            </button>
            <button
              type="button"
              onClick={() => { setContentType('entertainment'); setSubject('entertainment'); }}
              className={`flex-1 rounded-lg border px-3 py-2 text-sm ${isEntertainment ? 'border-purple-500 bg-purple-50 text-purple-700' : 'border-gray-200 text-gray-600'}`}
            >
              🎬 娱乐
            </button>
          </div>
        </div>

        {!isEntertainment && (
          <div>
            <label className="mb-1 block text-xs text-muted">类别 / 科目</label>
            <select className="input" value={subject} onChange={(e) => setSubject(e.target.value)}>
              {subjects.filter((s) => s.key !== 'entertainment').map((s) => (
                <option key={s.key} value={s.key}>
                  {s.emoji} {s.label} ({s.key})
                </option>
              ))}
            </select>
          </div>
        )}

        <ImageUpload label="封面图" value={coverUrl} onChange={setCoverUrl} />

        <div>
          <label className="mb-1 block text-xs text-muted">标签</label>
          <TagInput value={tagIDs} onChange={setTagIDs} />
        </div>

        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '保存并创建'}
        </button>
      </form>
    </Modal>
  );
}
