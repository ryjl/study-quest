import { useMemo, useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Check, X, Pencil, CheckSquare } from 'lucide-react';
import { api } from '../../lib/api';
import { useToast } from '../../lib/toast';
import type { Course, GlossaryCandidate } from '../../lib/types';

// GlossaryTab is the PR2.5 admin review surface for term-correction candidates
// mined by the polish job. The polish LLM, while fixing homophones in a
// whisper transcript, surfaces the reusable rules it spotted (军→车 in a
// xiangqi course). Those land as pending rows here; the admin accepts the good
// ones (which promotes them into the course's TermDict so future polish runs
// apply them automatically) or rejects the bad ones.
//
// One tab per course (术语 are domain-specific). Top-level course picker, then
// the candidate list grouped by status: pending first (the actionable ones),
// then accepted/rejected collapsed for history.
export function GlossaryTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const [courseId, setCourseId] = useState<number | null>(null);
  const [statusFilter, setStatusFilter] = useState<'pending' | 'all'>('pending');

  const courses: Course[] = coursesQ.data ?? [];
  // Auto-pick the first course once on load so the admin sees candidates
  // without an extra click. If they've picked one, keep it.
  const effectiveCourseId = courseId ?? courses[0]?.id ?? null;

  const candsQ = useQuery({
    queryKey: ['glossary-candidates', effectiveCourseId, statusFilter],
    queryFn: () => api.listGlossaryCandidates(effectiveCourseId!, statusFilter),
    enabled: effectiveCourseId != null,
  });

  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [applySiblings, setApplySiblings] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editCorrected, setEditCorrected] = useState('');
  const [editContext, setEditContext] = useState('');

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['glossary-candidates', effectiveCourseId] });
    // Accepted candidates mutate Course.AIConfig.TermDict, so the Prompt 配置
    // tab (which reads ai_config) also needs refreshing.
    qc.invalidateQueries({ queryKey: ['courses'] });
  };

  const acceptMut = useMutation({
    mutationFn: (args: { id: number; corrected?: string; context?: string }) =>
      api.acceptGlossaryCandidate(args.id, {
        corrected: args.corrected,
        context: args.context,
        apply_to_subject_siblings: applySiblings,
      }),
    onSuccess: () => {
      toast.success('已接受,已追加到课程 TermDict');
      invalidate();
      setEditingId(null);
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const rejectMut = useMutation({
    mutationFn: (id: number) => api.rejectGlossaryCandidate(id),
    onSuccess: () => {
      toast.success('已拒绝');
      invalidate();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const batchAcceptMut = useMutation({
    mutationFn: () => api.acceptGlossaryCandidateBatch(Array.from(selected), applySiblings),
    onSuccess: (d) => {
      if (d.ok) {
        toast.success(`已批量接受 ${d.accepted.length} 条`);
      } else {
        toast.success(`接受 ${d.accepted.length} 条,${Object.keys(d.errors).length} 条失败`);
      }
      setSelected(new Set());
      invalidate();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const candidates: GlossaryCandidate[] = candsQ.data ?? [];
  const pendingCount = useMemo(
    () => candidates.filter((c) => c.status === 'pending').length,
    [candidates],
  );

  if (coursesQ.isLoading) {
    return <div className="card"><p className="text-sm text-muted">加载课程列表…</p></div>;
  }
  if (courses.length === 0) {
    return <div className="card"><p className="text-sm text-muted">还没有课程。</p></div>;
  }

  return (
    <div className="space-y-4">
      {/* 顶部：课程选择 + 状态过滤 + 批量操作 */}
      <div className="card flex flex-wrap items-end gap-3">
        <div>
          <label className="mb-1 block text-xs text-muted">课程</label>
          <select
            className="input"
            value={effectiveCourseId ?? ''}
            onChange={(e) => {
              setCourseId(Number(e.target.value));
              setSelected(new Set());
            }}
          >
            {courses.map((c) => (
              <option key={c.id} value={c.id}>{c.title}</option>
            ))}
          </select>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">状态</label>
          <select
            className="input"
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value as 'pending' | 'all')}
          >
            <option value="pending">待审 ({pendingCount})</option>
            <option value="all">全部</option>
          </select>
        </div>
        <label className="flex items-center gap-1.5 text-xs text-muted" title="接受时把同一条规则也追加到同学科的其他课程,省去每门课重复审核">
          <input
            type="checkbox"
            checked={applySiblings}
            onChange={(e) => setApplySiblings(e.target.checked)}
            className="h-3.5 w-3.5 accent-primary"
          />
          接受时应用到同学科所有课程
        </label>
        {selected.size > 0 && (
          <button
            className="btn-primary btn-sm inline-flex items-center gap-1.5"
            onClick={() => batchAcceptMut.mutate()}
            disabled={batchAcceptMut.isPending}
          >
            {batchAcceptMut.isPending ? '处理中…' : <><CheckSquare size={14} /> 批量接受选中 ({selected.size})</>}
          </button>
        )}
      </div>

      {/* 候选列表 */}
      <div className="card">
        {candsQ.isLoading && <p className="text-sm text-muted">加载候选…</p>}
        {candsQ.error && <p className="text-sm text-bad">加载失败:{(candsQ.error as Error).message}</p>}
        {!candsQ.isLoading && !candsQ.error && candidates.length === 0 && (
          <p className="py-6 text-center text-sm text-muted">
            {statusFilter === 'pending' ? '该课程暂无待审候选(字幕润色产出术语建议后会出现在这里)' : '该课程暂无候选记录'}
          </p>
        )}
        <div className="divide-y divide-border/60">
          {candidates.map((c) => (
            <CandidateRow
              key={c.id}
              c={c}
              selected={selected.has(c.id)}
              onToggleSelect={() => {
                setSelected((prev) => {
                  const next = new Set(prev);
                  if (next.has(c.id)) next.delete(c.id);
                  else next.add(c.id);
                  return next;
                });
              }}
              editing={editingId === c.id}
              editCorrected={editCorrected}
              editContext={editContext}
              onEditStart={() => {
                setEditingId(c.id);
                setEditCorrected(c.corrected);
                setEditContext(c.context ?? '');
              }}
              onEditCancel={() => setEditingId(null)}
              onEditCorrected={setEditCorrected}
              onEditContext={setEditContext}
              onAccept={() => acceptMut.mutate({ id: c.id })}
              onAcceptEdited={() => acceptMut.mutate({ id: c.id, corrected: editCorrected, context: editContext })}
              onReject={() => rejectMut.mutate(c.id)}
              acceptPending={acceptMut.isPending}
              rejectPending={rejectMut.isPending}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

// CandidateRow renders one candidate. Three modes:
//   - pending (default): shows original → corrected + confidence + evidence,
//     with [编辑][接受][拒绝] actions and a checkbox for batch accept.
//   - pending + editing: corrected/context become editable inputs, [取消][确认] replace the actions.
//   - accepted/rejected: read-only badge + the (possibly admin-edited) values.
function CandidateRow(props: {
  c: GlossaryCandidate;
  selected: boolean;
  onToggleSelect: () => void;
  editing: boolean;
  editCorrected: string;
  editContext: string;
  onEditStart: () => void;
  onEditCancel: () => void;
  onEditCorrected: (v: string) => void;
  onEditContext: (v: string) => void;
  onAccept: () => void;
  onAcceptEdited: () => void;
  onReject: () => void;
  acceptPending: boolean;
  rejectPending: boolean;
}) {
  const { c } = props;
  const isPending = c.status === 'pending';
  const statusBadge = {
    pending: <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[11px] text-amber-600 dark:text-amber-400">待审</span>,
    accepted: <span className="rounded-full bg-green-500/10 px-2 py-0.5 text-[11px] text-green-600 dark:text-green-400">已接受</span>,
    rejected: <span className="rounded-full bg-red-500/10 px-2 py-0.5 text-[11px] text-red-600 dark:text-red-400">已拒绝</span>,
  }[c.status];

  return (
    <div className="px-4 py-3">
      <div className="flex items-start gap-3">
        {/* 批量选择 checkbox（仅 pending 显示）*/}
        {isPending && (
          <input
            type="checkbox"
            checked={props.selected}
            onChange={props.onToggleSelect}
            className="mt-1 h-4 w-4 accent-primary"
            aria-label="选择该候选"
          />
        )}

        <div className="min-w-0 flex-1">
          {/* 原始 → 修正 + 状态 */}
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-sm text-txt">
              <span className="text-muted line-through decoration-muted/40">{c.original}</span>
              <span className="mx-1.5 text-muted">→</span>
              {props.editing ? (
                <input
                  className="input ml-1 inline-block w-32 font-mono text-sm"
                  value={props.editCorrected}
                  onChange={(e) => props.onEditCorrected(e.target.value)}
                  autoFocus
                />
              ) : (
                <span className="font-medium">{c.corrected}</span>
              )}
            </span>
            {statusBadge}
          </div>

          {/* context */}
          <div className="mt-1 text-xs text-muted">
            {props.editing ? (
              <input
                className="input w-full max-w-md text-xs"
                value={props.editContext}
                onChange={(e) => props.onEditContext(e.target.value)}
                placeholder="上下文说明(可选)"
              />
            ) : (
              c.context && <span>上下文:{c.context}</span>
            )}
          </div>

          {/* 元数据:confidence + evidence count */}
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-muted">
            <span>置信度 {(c.confidence * 100).toFixed(0)}%</span>
            <span>· 出现 {c.evidence_count} 次</span>
          </div>
        </div>

        {/* 操作按钮 */}
        {isPending && (
          <div className="flex shrink-0 items-center gap-1">
            {!props.editing ? (
              <>
                <button
                  className="btn-ghost btn-sm inline-flex items-center gap-1"
                  onClick={props.onEditStart}
                  title="编辑后接受(LLM 的建议不一定对)"
                >
                  <Pencil size={13} /> 编辑
                </button>
                <button
                  className="btn-primary btn-sm inline-flex items-center gap-1"
                  onClick={props.onAccept}
                  disabled={props.acceptPending}
                  title="接受并追加到课程 TermDict"
                >
                  <Check size={13} /> 接受
                </button>
                <button
                  className="btn-ghost btn-sm inline-flex items-center gap-1 text-bad"
                  onClick={props.onReject}
                  disabled={props.rejectPending}
                  title="拒绝(不再展示在待审列表)"
                >
                  <X size={13} /> 拒绝
                </button>
              </>
            ) : (
              <>
                <button className="btn-ghost btn-sm" onClick={props.onEditCancel}>取消</button>
                <button
                  className="btn-primary btn-sm inline-flex items-center gap-1"
                  onClick={props.onAcceptEdited}
                  disabled={props.acceptPending}
                >
                  <Check size={13} /> 确认编辑后接受
                </button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
