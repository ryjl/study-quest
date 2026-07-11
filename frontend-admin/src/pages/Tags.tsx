import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { api } from '../lib/api';
import { useTags, useInvalidateTags } from '../lib/useTags';
import { useDeleteConfirm } from '../lib/useDeleteConfirm';
import type { TagMeta } from '../lib/types';
import { Modal, LoadingState, EmptyState } from '../components/ui';
import { useToast } from '../lib/toast';

const COLOR_CHOICES = [
  '#ef4444', '#f59e0b', '#8b5cf6', '#06b6d4', '#10b981',
  '#ec4899', '#3b82f6', '#84cc16', '#eab308', '#64748b',
];

export function Tags() {
  const tagsQ = useTags();
  const invalidate = useInvalidateTags();
  const [editing, setEditing] = useState<TagMeta | null>(null);
  const [creating, setCreating] = useState(false);

  const del = useDeleteConfirm({
    mutationFn: api.deleteTag,
    noun: '标签',
    onDeleted: invalidate,
  });

  const onDelete = (t: TagMeta) => {
    const usedBy = t.course_count ?? 0;
    del.confirmAndDelete(
      t.id!,
      `删除标签「${t.label}」？`,
      usedBy > 0
        ? `该标签正被 ${usedBy} 门课程使用，删除后将自动从这些课程上移除（课程本身不受影响）。`
        : '该标签当前未被任何课程使用。',
    );
  };

  if (tagsQ.isLoading) return <LoadingState />;
  const tags = tagsQ.data ?? [];

  return (
    <div>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-txt">标签管理</h1>
          <p className="mt-1 text-sm text-muted">
            管理课程标签。删除标签会自动从所有课程上解除关联（不会删除课程）。
          </p>
        </div>
        <button className="btn-primary" onClick={() => setCreating(true)}>+ 新增标签</button>
      </div>

      {tags.length === 0 ? (
        <EmptyState icon="🏷️" title="还没有标签" hint="新增第一个标签以开始给课程打标。" />
      ) : (
        <div className="card overflow-hidden p-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted">
                <th className="px-4 py-3 font-medium">标签</th>
                <th className="px-4 py-3 font-medium">Key</th>
                <th className="px-4 py-3 font-medium">颜色</th>
                <th className="px-4 py-3 font-medium">使用课程</th>
                <th className="px-4 py-3 font-medium">排序</th>
                <th className="px-4 py-3 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {tags.map((t) => (
                <tr key={t.id ?? t.key} className="border-b border-border last:border-0 hover:bg-card-2/50">
                  <td className="px-4 py-3">
                    <span
                      className="inline-flex items-center gap-2 rounded-md px-2 py-0.5 font-medium"
                      style={{ backgroundColor: `${t.color}20`, color: t.color }}
                    >
                      {t.label}
                      {t.is_system && (
                        <span title="系统默认标签，不可删除（可在编辑里改名/改色）" className="text-xs">🔒</span>
                      )}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <code className="rounded bg-card-2 px-1.5 py-0.5 text-xs text-muted">{t.key}</code>
                  </td>
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center gap-2">
                      <span className="h-4 w-4 rounded-full" style={{ backgroundColor: t.color }} />
                      <span className="text-xs text-muted">{t.color}</span>
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted">{t.course_count ?? 0} 门</td>
                  <td className="px-4 py-3 text-muted">{t.sort_order ?? '-'}</td>
                  <td className="px-4 py-3 text-right">
                    <button className="btn-ghost btn-sm" onClick={() => setEditing(t)}>编辑</button>
                    {t.is_system ? (
                      <button className="btn-ghost btn-sm opacity-40" disabled title="系统默认标签，不可删除">
                        删除
                      </button>
                    ) : (
                      <button
                        className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                        onClick={() => onDelete(t)}
                        disabled={del.isPending}
                      >
                        删除
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {(creating || editing) && (
        <TagModal tag={editing} onClose={() => { setCreating(false); setEditing(null); }} />
      )}
    </div>
  );
}

function TagModal({ tag, onClose }: { tag: TagMeta | null; onClose: () => void }) {
  const isEdit = !!tag;
  const toast = useToast();
  const invalidate = useInvalidateTags();

  const [key, setKey] = useState('');
  const [label, setLabel] = useState('');
  const [color, setColor] = useState(COLOR_CHOICES[0]);
  const [sortOrder, setSortOrder] = useState(0);

  useEffect(() => {
    if (tag) {
      setKey(tag.key);
      setLabel(tag.label);
      setColor(tag.color || COLOR_CHOICES[0]);
      setSortOrder(tag.sort_order ?? 0);
    } else {
      setKey('');
      setLabel('');
      setColor(COLOR_CHOICES[0]);
      setSortOrder(0);
    }
  }, [tag]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = {
        key: key.trim().toLowerCase(),
        label: label.trim(),
        color,
        sort_order: sortOrder,
      };
      if (!body.key) throw new Error('请填写 Key');
      if (!body.label) throw new Error('请填写标签名称');
      if (isEdit && tag?.id) return api.updateTag(tag.id, body);
      return api.createTag(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '标签已更新' : '标签已创建');
      invalidate();
      onClose();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑标签' : '新增标签'} size="md">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">名称</label>
          <input className="input" placeholder="如：必修" value={label} onChange={(e) => setLabel(e.target.value)} required autoFocus />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">Key（小写英文/数字）</label>
          <input className="input" placeholder="如：required" value={key} onChange={(e) => setKey(e.target.value)} required spellCheck={false} />
          <p className="mt-1 text-xs text-muted">课程存的是标签 ID，改 Key/名称不影响已关联的课程。</p>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">颜色</label>
          <div className="flex flex-wrap items-center gap-1.5">
            {COLOR_CHOICES.map((c) => (
              <button
                type="button"
                key={c}
                onClick={() => setColor(c)}
                className={`h-7 w-7 rounded-full border-2 transition ${color === c ? 'border-txt' : 'border-transparent'}`}
                style={{ backgroundColor: c }}
                aria-label={c}
              />
            ))}
            <input className="input ml-2 w-28 py-1 text-xs" placeholder="#custom" value={color} onChange={(e) => setColor(e.target.value)} />
          </div>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">排序权重（越小越靠前）</label>
          <input className="input" type="number" value={sortOrder} onChange={(e) => setSortOrder(Number(e.target.value))} />
        </div>
        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '创建标签'}
        </button>
      </form>
    </Modal>
  );
}
