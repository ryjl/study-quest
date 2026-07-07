import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { Chapter } from '../../lib/types';
import { Modal } from '../../components/ui';
import { ImageUpload } from '../../components/inputs';
import { useToast } from '../../lib/toast';

export function ChapterEditor({
  courseId,
  chapter,
  onClose,
  onSaved,
}: {
  courseId: number;
  chapter: Chapter | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEdit = !!chapter;
  const toast = useToast();
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [coverUrl, setCoverUrl] = useState('');
  const [sortOrder, setSortOrder] = useState(1);

  useEffect(() => {
    if (chapter) {
      setTitle(chapter.title);
      setDescription(chapter.description);
      setCoverUrl(chapter.cover_url);
      setSortOrder(chapter.sort_order);
    } else {
      setTitle('');
      setDescription('');
      setCoverUrl('');
      setSortOrder(1);
    }
  }, [chapter]);

  const saveMut = useMutation({
    mutationFn: async () => {
      if (!title.trim()) throw new Error('请输入章节名称');
      const body = { title: title.trim(), description, cover_url: coverUrl, sort_order: sortOrder };
      if (isEdit && chapter) return api.updateChapter(chapter.id, body);
      return api.createChapter(courseId, body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '章节已更新' : '章节已创建');
      onSaved();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑章节' : '新增章节'} size="md">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div className="grid grid-cols-1 gap-4 md:grid-cols-[1fr_120px]">
          <div>
            <label className="mb-1 block text-xs text-muted">章节名称</label>
            <input className="input" value={title} onChange={(e) => setTitle(e.target.value)} required autoFocus />
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">排序值</label>
            <input type="number" className="input" value={sortOrder} onChange={(e) => setSortOrder(Number(e.target.value))} required min={0} />
          </div>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">章节简介</label>
          <textarea className="input" rows={2} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="输入本章节的学习目标或说明" />
        </div>
        <ImageUpload label="章节封面（可选）" value={coverUrl} onChange={setCoverUrl} />
        <div className="flex justify-end gap-2 pt-2">
          <button type="button" className="btn-secondary" onClick={onClose}>
            取消
          </button>
          <button type="submit" className="btn-primary" disabled={saveMut.isPending}>
            {saveMut.isPending ? '保存中...' : '保存'}
          </button>
        </div>
      </form>
    </Modal>
  );
}
