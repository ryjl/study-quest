// SeriesModal — create / edit a ReadingSeries (container that groups related
// PDF books and web articles). On save it fires createReadingSeries or
// updateReadingSeries, invalidates the series + series-detail caches, and
// closes.

import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { ReadingSeries } from '../../lib/types';
import { Modal } from '../../components/ui';
import { ImageUpload } from '../../components/inputs';
import { useToast } from '../../lib/toast';
import { SubjectSelect, GradeField, TagField } from './shared';

export function SeriesModal({
  series,
  onClose,
}: {
  series: ReadingSeries | null;
  onClose: () => void;
}) {
  const isEdit = !!series;
  const toast = useToast();
  const qc = useQueryClient();
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['reading-series'] });
    qc.invalidateQueries({ queryKey: ['reading-series-detail'] });
  };

  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [grade, setGrade] = useState('universal');
  const [subject, setSubject] = useState('');
  const [coverUrl, setCoverUrl] = useState('');
  const [sortOrder, setSortOrder] = useState(0);
  const [tagIds, setTagIds] = useState<number[]>([]);

  useEffect(() => {
    if (series) {
      setTitle(series.title);
      setDescription(series.description);
      setGrade(series.grade || 'universal');
      setSubject(series.subject);
      setCoverUrl(series.cover_url);
      setSortOrder(series.sort_order);
      setTagIds(series.tag_ids ?? []);
    } else {
      setTitle('');
      setDescription('');
      setGrade('universal');
      setSubject('');
      setCoverUrl('');
      setSortOrder(0);
      setTagIds([]);
    }
  }, [series]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = {
        title: title.trim(),
        description,
        grade,
        subject,
        cover_url: coverUrl,
        sort_order: sortOrder,
        tag_ids: tagIds,
      };
      if (!body.title) throw new Error('请填写标题');
      if (!body.subject) throw new Error('请选择科目');
      if (isEdit && series) return api.updateReadingSeries(series.id, body);
      return api.createReadingSeries(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '系列已更新' : '系列已创建');
      invalidate();
      onClose();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑系列' : '新建系列'} size="md">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">标题</label>
          <input
            className="input"
            placeholder="如：上博展厅系列"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            required
            autoFocus
          />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">描述</label>
          <textarea
            className="input"
            rows={2}
            placeholder="系列简介（可选）"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>
        <SubjectSelect value={subject} onChange={setSubject} />
        <GradeField value={grade} onChange={setGrade} />
        <ImageUpload value={coverUrl} onChange={setCoverUrl} />
        <TagField value={tagIds} onChange={setTagIds} />
        <div>
          <label className="mb-1 block text-xs text-muted">排序权重（越小越靠前）</label>
          <input
            className="input"
            type="number"
            value={sortOrder}
            onChange={(e) => setSortOrder(Number(e.target.value))}
          />
        </div>
        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '创建系列'}
        </button>
      </form>
    </Modal>
  );
}
