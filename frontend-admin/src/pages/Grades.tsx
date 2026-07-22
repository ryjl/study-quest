import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Lock, Plus } from 'lucide-react';
import { api } from '../lib/api';
import type { GradeUsage } from '../lib/api/grades';
import { Modal, LoadingState, EmptyState } from '../components/ui';
import { useToast } from '../lib/toast';

// Grade management — the admin-side CRUD surface for the open-tag-system
// grade values. Grades get created implicitly when a course/reading item is
// saved with a new tag value; this component is for the LATER management the
// admin needs once tags accumulate:
//   - see what's in use + how many entities reference each tag
//   - rename a typo'd custom tag (cascades across all four grade tables)
//   - merge two tags that mean the same thing (考研 / 研究生)
//   - delete a now-unused custom tag
//
// Preset tags (primary/junior/senior/college/other/universal) are locked from
// rename/delete — they're the system defaults every GradePicker checkbox maps
// to. The admin CAN merge a preset's rows into another tag (the historical
// adult→college migration path), which doesn't rename the preset itself.
//
// Exported as GradesTable (no page header) so it slots into the Classification
// page as a tab alongside SubjectsTable and TagsTable. Mirrors that pair's
// "reusable table component" shape.
export function GradesTable() {
  const qc = useQueryClient();
  const toast = useToast();
  const [editing, setEditing] = useState<GradeUsage | null>(null);
  const [merging, setMerging] = useState<GradeUsage | null>(null);

  const gradesQ = useQuery({
    queryKey: ['grades'],
    queryFn: api.listGrades,
  });

  // Centralized invalidate so every mutation refreshes the same list. The table
  // is small and reads-only between mutations, so no polling needed.
  const invalidate = () => qc.invalidateQueries({ queryKey: ['grades'] });

  const renameMut = useMutation({
    mutationFn: (vars: { from: string; newKey: string }) => api.renameGrade(vars.from, vars.newKey),
    onSuccess: () => {
      toast.success('已重命名');
      invalidate();
      setEditing(null);
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const mergeMut = useMutation({
    mutationFn: (vars: { from: string; to: string }) => api.mergeGrades(vars.from, vars.to),
    onSuccess: () => {
      toast.success('已合并');
      invalidate();
      setMerging(null);
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const deleteMut = useMutation({
    mutationFn: api.deleteGrade,
    onSuccess: () => {
      toast.success('已删除');
      invalidate();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  if (gradesQ.isLoading) return <LoadingState />;
  const grades = gradesQ.data ?? [];

  return (
    <div>
      <div className="mb-3 rounded-lg border border-border/60 bg-card-2 p-3 text-xs text-muted">
        预设标签不可重命名/删除；自定义标签可重命名、合并、删除。历史 「成人」(adult) 标签可用「合并」迁到 「大学」(college) 或 「其它」(other)。
      </div>

      {grades.length === 0 ? (
        <EmptyState
          icon={<Plus size={28} />}
          title="暂无年级标签"
          hint="添加课程时选择的年级会自动出现在这里。"
        />
      ) : (
        <div className="card overflow-hidden p-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted">
                <th className="px-4 py-3 font-medium">标签</th>
                <th className="px-4 py-3 font-medium">Key</th>
                <th className="px-4 py-3 font-medium">类型</th>
                <th className="px-4 py-3 font-medium">使用数</th>
                <th className="px-4 py-3 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {grades.map((g) => (
                <tr key={g.grade} className="border-b border-border last:border-0 hover:bg-card-2/50">
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center gap-2 font-medium text-txt">
                      {g.label}
                      {g.is_preset && (
                        <span title="系统预设，不可重命名/删除（可合并其引用到其它标签）" className="text-muted">
                          <Lock size={12} />
                        </span>
                      )}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <code className="rounded bg-card-2 px-1.5 py-0.5 text-xs text-muted">{g.grade}</code>
                  </td>
                  <td className="px-4 py-3">
                    <span
                      className={`rounded-full px-2 py-0.5 text-xs ${
                        g.is_preset ? 'bg-blue-100 text-blue-700' : 'bg-purple-100 text-purple-700'
                      }`}
                    >
                      {g.is_preset ? '预设' : '自定义'}
                    </span>
                  </td>
                  <td className="px-4 py-3 tabular-nums text-muted">{g.count}</td>
                  <td className="px-4 py-3 text-right">
                    <div className="flex justify-end gap-1.5">
                      {/* Rename: custom tags only. Presets are locked. */}
                      {!g.is_preset && (
                        <button className="btn-ghost btn-sm" onClick={() => setEditing(g)}>
                          重命名
                        </button>
                      )}
                      {/* Merge: available on every tag (presets included — that's the
                          adult→college migration path). */}
                      <button
                        className="btn-ghost btn-sm"
                        onClick={() => setMerging(g)}
                        title="把这个标签的所有引用合并到另一个标签"
                      >
                        合并
                      </button>
                      {/* Delete: custom tags with 0 uses only. */}
                      {!g.is_preset && g.count === 0 && (
                        <button
                          className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                          onClick={() => {
                            if (confirm(`确认删除自定义标签「${g.label}」？`)) deleteMut.mutate(g.grade);
                          }}
                          disabled={deleteMut.isPending}
                        >
                          删除
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {editing && (
        <RenameModal
          grade={editing}
          onClose={() => setEditing(null)}
          onConfirm={(newKey) => renameMut.mutate({ from: editing.grade, newKey })}
          busy={renameMut.isPending}
        />
      )}
      {merging && gradesQ.data && (
        <MergeModal
          grade={merging}
          all={gradesQ.data}
          onClose={() => setMerging(null)}
          onConfirm={(to) => mergeMut.mutate({ from: merging.grade, to })}
          busy={mergeMut.isPending}
        />
      )}
    </div>
  );
}

// RenameModal edits a custom tag's key. The new key must differ from the old;
// the backend rejects same-key renames. We don't validate uniqueness client-side
// because the backend's per-table dedup (delete-then-update) handles collisions
// with an existing tag of the same name gracefully (rows fold into the target).
function RenameModal({
  grade,
  onClose,
  onConfirm,
  busy,
}: {
  grade: GradeUsage;
  onClose: () => void;
  onConfirm: (newKey: string) => void;
  busy: boolean;
}) {
  const [newKey, setNewKey] = useState(grade.grade);
  return (
    <Modal open onClose={onClose} title={`重命名「${grade.label}」`} size="sm">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!newKey.trim() || newKey.trim() === grade.grade) return;
          onConfirm(newKey.trim());
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">新 Key（将替换所有引用此标签的课程/阅读资源）</label>
          <input
            className="input"
            value={newKey}
            onChange={(e) => setNewKey(e.target.value)}
            autoFocus
            required
            spellCheck={false}
          />
          <p className="mt-1 text-[11px] text-muted">
            重命名会级联更新 4 张表（course_grades / reading_*_grades）。如果目标 Key 已存在，两者的引用会合并。
          </p>
        </div>
        <button type="submit" className="btn-primary w-full" disabled={busy || !newKey.trim() || newKey.trim() === grade.grade}>
          {busy ? '保存中...' : '确认重命名'}
        </button>
      </form>
    </Modal>
  );
}

// MergeModal picks a target tag to merge INTO. The dropdown excludes the source
// tag itself. Presets appear first so the admin can merge a custom tag into a
// preset (考研→other) without hunting for it.
function MergeModal({
  grade,
  all,
  onClose,
  onConfirm,
  busy,
}: {
  grade: GradeUsage;
  all: GradeUsage[];
  onClose: () => void;
  onConfirm: (to: string) => void;
  busy: boolean;
}) {
  // Targets = every other tag. Presets first, then customs.
  const targets = all
    .filter((g) => g.grade !== grade.grade)
    .sort((a, b) => (b.is_preset ? 1 : 0) - (a.is_preset ? 1 : 0));
  const [to, setTo] = useState(targets[0]?.grade ?? '');
  return (
    <Modal open onClose={onClose} title={`合并「${grade.label}」到...`} size="sm">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!to) return;
          onConfirm(to);
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">目标标签（所有引用「{grade.label}」的实体将改用此标签）</label>
          <select className="input" value={to} onChange={(e) => setTo(e.target.value)} required>
            {targets.map((g) => (
              <option key={g.grade} value={g.grade}>
                {g.label} ({g.grade}) — 当前 {g.count} 引用
              </option>
            ))}
          </select>
          <p className="mt-1 text-[11px] text-muted">
            合并后，「{grade.label}」将不再出现在列表里（除非它是预设）。此操作不可撤销。
          </p>
        </div>
        <button type="submit" className="btn-primary w-full" disabled={busy || !to}>
          {busy ? '合并中...' : '确认合并'}
        </button>
      </form>
    </Modal>
  );
}
