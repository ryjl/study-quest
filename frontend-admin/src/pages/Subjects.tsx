import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { api } from '../lib/api';
import { useSubjects, useInvalidateSubjects } from '../lib/useSubjects';
import { useDeleteConfirm } from '../lib/useDeleteConfirm';
import type { SubjectMeta } from '../lib/types';
import { Modal, LoadingState, EmptyState } from '../components/ui';
import { useToast } from '../lib/toast';

// Color swatch palette offered for new/custom subjects. The admin can also
// paste any hex value in the dedicated field.
const COLOR_CHOICES = [
  '#60a5fa', '#f59e0b', '#34d399', '#a78bfa', '#f43f5e',
  '#06b6d4', '#ec4899', '#84cc16', '#eab308', '#64748b',
];

const EMOJI_CHOICES = ['📚', '📐', '🔠', '🧪', '🌎', '💻', '🎨', '🎵', '⚽', '🧩', '📖', '🌍'];

export function Subjects() {
  const subjectsQ = useSubjects();
  const invalidate = useInvalidateSubjects();

  const [editing, setEditing] = useState<SubjectMeta | null>(null);
  const [creating, setCreating] = useState(false);

  const del = useDeleteConfirm({
    mutationFn: api.deleteSubject,
    noun: '科目',
    onDeleted: invalidate,
  });

  const onDelete = (s: SubjectMeta) =>
    del.confirmAndDelete(s.id!, `确认删除科目「${s.label}」？`, '若该科目下仍有课程，删除将被拒绝。');

  if (subjectsQ.isLoading) return <LoadingState />;
  const subjects = subjectsQ.data ?? [];

  return (
    <div>
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-txt">科目管理</h1>
          <p className="mt-1 text-sm text-muted">
            管理课程分类科目。删除有课程绑定的科目会被拒绝；重命名 Key 会自动级联到相关徽章规则。
          </p>
        </div>
        <button className="btn-primary" onClick={() => setCreating(true)}>+ 新增科目</button>
      </div>

      {subjects.length === 0 ? (
        <EmptyState icon="🏷️" title="还没有科目" hint="新增第一个科目以开始分类课程。" />
      ) : (
        <div className="card overflow-hidden p-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted">
                <th className="px-4 py-3 font-medium">科目</th>
                <th className="px-4 py-3 font-medium">Key</th>
                <th className="px-4 py-3 font-medium">颜色</th>
                <th className="px-4 py-3 font-medium">排序</th>
                <th className="px-4 py-3 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {subjects.map((s) => (
                <tr key={s.id ?? s.key} className="border-b border-border last:border-0 hover:bg-card-2/50">
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center gap-2 font-medium text-txt">
                      <span className="text-base">{s.emoji}</span>
                      {s.label}
                      {s.is_system && (
                        <span title="系统默认科目，不可删除（可在编辑里改名/改色）" className="text-xs">🔒</span>
                      )}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <code className="rounded bg-card-2 px-1.5 py-0.5 text-xs text-muted">{s.key}</code>
                  </td>
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center gap-2">
                      <span className="h-4 w-4 rounded-full" style={{ backgroundColor: s.color }} />
                      <span className="text-xs text-muted">{s.color}</span>
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted">{s.sort_order ?? '-'}</td>
                  <td className="px-4 py-3 text-right">
                    <button className="btn-ghost btn-sm" onClick={() => setEditing(s)}>编辑</button>
                    {s.is_system ? (
                      <button className="btn-ghost btn-sm opacity-40" disabled title="系统默认科目，不可删除">
                        删除
                      </button>
                    ) : (
                      <button
                        className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                        onClick={() => onDelete(s)}
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
        <SubjectModal
          subject={editing}
          onClose={() => {
            setCreating(false);
            setEditing(null);
          }}
        />
      )}
    </div>
  );
}

function SubjectModal({ subject, onClose }: { subject: SubjectMeta | null; onClose: () => void }) {
  const isEdit = !!subject;
  const toast = useToast();
  const invalidate = useInvalidateSubjects();

  const [key, setKey] = useState('');
  const [label, setLabel] = useState('');
  const [emoji, setEmoji] = useState('📦');
  const [color, setColor] = useState(COLOR_CHOICES[0]);
  const [sortOrder, setSortOrder] = useState(0);

  useEffect(() => {
    if (subject) {
      setKey(subject.key);
      setLabel(subject.label);
      setEmoji(subject.emoji || '📦');
      setColor(subject.color || COLOR_CHOICES[0]);
      setSortOrder(subject.sort_order ?? 0);
    } else {
      setKey('');
      setLabel('');
      setEmoji('📦');
      setColor(COLOR_CHOICES[0]);
      setSortOrder(0);
    }
  }, [subject]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = {
        key: key.trim().toLowerCase(),
        label: label.trim(),
        emoji,
        color,
        sort_order: sortOrder,
      };
      if (!body.key) throw new Error('请填写科目 Key（小写英文）');
      if (!body.label) throw new Error('请填写科目名称');
      if (isEdit && subject?.id) return api.updateSubject(subject.id, body);
      return api.createSubject(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '科目已更新' : '科目已创建');
      invalidate();
      onClose();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑科目' : '新增科目'} size="md">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">名称（显示名）</label>
          <input className="input" placeholder="如：数学" value={label} onChange={(e) => setLabel(e.target.value)} required autoFocus />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">
            Key（稳定标识，{isEdit ? '修改会级联更新相关徽章规则' : '小写英文/数字'}）
          </label>
          <input
            className="input"
            placeholder="如：math"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            required
            spellCheck={false}
          />
          {isEdit && (
            <p className="mt-1 text-xs text-warn">
              ⚠️ 修改 Key 会同步更新使用它的徽章规则目标（subject_count）。
            </p>
          )}
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">图标</label>
          <div className="flex flex-wrap gap-1.5">
            {EMOJI_CHOICES.map((em) => (
              <button
                type="button"
                key={em}
                onClick={() => setEmoji(em)}
                className={`flex h-9 w-9 items-center justify-center rounded-lg border text-lg transition ${
                  emoji === em ? 'border-primary bg-primary/10' : 'border-border hover:bg-card-2'
                }`}
              >
                {em}
              </button>
            ))}
          </div>
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
            <input
              className="input ml-2 w-28 py-1 text-xs"
              placeholder="#custom"
              value={color}
              onChange={(e) => setColor(e.target.value)}
            />
          </div>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">排序权重（数字越小越靠前）</label>
          <input
            className="input"
            type="number"
            value={sortOrder}
            onChange={(e) => setSortOrder(Number(e.target.value))}
          />
        </div>
        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '创建科目'}
        </button>
      </form>
    </Modal>
  );
}
