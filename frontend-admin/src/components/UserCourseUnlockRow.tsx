import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { Modal } from './ui';
import { UnlockStrategyEditor, type StrategyValue } from './UnlockStrategyEditor';
import { useUnlockPreview, strategyLabel } from '../lib/useUnlock';
import { useToast } from '../lib/toast';
import type { Episode, UnlockStrategy } from '../lib/types';

// One row inside the user authorization drawer: shows the per-(user, course)
// unlock status and the actions appropriate to the effective strategy.
//   - manual/interval/weekly → 「手动解锁 +1」推进水位
//   - selected               → 「勾选课时」打开多选 Modal
//   - any                    → 「覆盖策略」单独改这个学生的节奏
export function UserCourseUnlockRow({ userId, courseId, courseTitle }: { userId: number; courseId: number; courseTitle: string }) {
  const qc = useQueryClient();
  const toast = useToast();
  const previewQ = useUnlockPreview(userId, courseId);
  const [selecting, setSelecting] = useState(false);
  const [overriding, setOverriding] = useState(false);

  // Load the effective override (or template-inherited) to decide which
  // actions to show. We read the override directly; absence = inherits
  // template, and the preview already reflects the resolved strategy.
  const overrideQ = useQuery({
    queryKey: ['unlock-override', userId, courseId],
    queryFn: () => api.getUnlockOverride(userId, courseId),
    staleTime: 5_000,
  });

  const effStrategy: UnlockStrategy = (previewQ.data?.strategy as UnlockStrategy) ?? 'all_open';

  // Centralized cache invalidation for this (user, course). Every unlock write
  // must refresh preview + override so the row reads fresh after the mutation,
  // per the React-Query "invalidate what you change" rule.
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['unlock-preview', userId, courseId] });
    qc.invalidateQueries({ queryKey: ['unlock-override', userId, courseId] });
    qc.invalidateQueries({ queryKey: ['unlock-preview'] });
  };

  const manualMut = useMutation({
    mutationFn: () => api.manualUnlock(userId, courseId),
    onSuccess: () => {
      toast.success('已手动解锁下一节');
      refresh();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const undoMut = useMutation({
    mutationFn: () => api.manualUnlockUndo(userId, courseId),
    onSuccess: () => {
      toast.success('已回退一次手动解锁');
      refresh();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const isAuto = effStrategy === 'manual' || effStrategy === 'interval' || effStrategy === 'weekly';

  // Whether a manual decrement is meaningful: only under an auto strategy when
  // the override row exists and has a positive manual count. Hides the −1
  // button when there's nothing to undo (Selected strategy, or count already 0)
  // so the UI never offers a no-op. Declared AFTER isAuto (it depends on it).
  const canUndo = isAuto && (overrideQ.data?.manual_unlock_count ?? 0) > 0;

  return (
    <div className="rounded-lg border border-border bg-card-2 px-3 py-2">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm text-txt">{courseTitle}</div>
          <div className="mt-0.5 text-[11px] text-muted">
            {previewQ.data ? (
              <>
                <span className="rounded bg-card px-1.5 py-0.5">{strategyLabel(effStrategy)}</span>{' '}
                可见 {previewQ.data.visible_count}/{previewQ.data.total} 节
                {previewQ.data.next_unlock_at && effStrategy !== 'all_open' && effStrategy !== 'selected' && (
                  <> · 下次解锁 {previewQ.data.next_unlock_at}</>
                )}
                {overrideQ.data?.exists && <span className="ml-1 text-warn">（已覆盖）</span>}
              </>
            ) : (
              '加载中…'
            )}
          </div>
        </div>
        <div className="flex shrink-0 gap-1">
          {isAuto && (
            <>
              <button
                className="btn-secondary btn-sm"
                onClick={() => manualMut.mutate()}
                disabled={manualMut.isPending}
                title="立即多解锁一节，不影响自动节奏"
              >
                +1
              </button>
              {canUndo && (
                <button
                  className="btn-ghost btn-sm"
                  onClick={() => undoMut.mutate()}
                  disabled={undoMut.isPending}
                  title="回退一次误点的手动解锁（不会减到负数）"
                >
                  −1
                </button>
              )}
            </>
          )}
          {effStrategy === 'selected' && (
            <button className="btn-secondary btn-sm" onClick={() => setSelecting(true)}>
              勾选课时
            </button>
          )}
          <button className="btn-ghost btn-sm" onClick={() => setOverriding(true)}>
            覆盖策略
          </button>
        </div>
      </div>

      {selecting && (
        <EpisodeSelectModal userId={userId} courseId={courseId} courseTitle={courseTitle} onClose={() => setSelecting(false)} />
      )}
      {overriding && (
        <OverrideModal userId={userId} courseId={courseId} courseTitle={courseTitle} onClose={() => setOverriding(false)} />
      )}
    </div>
  );
}

// OverrideModal — replace this student's strategy wholesale. Manual count is
// preserved server-side (SaveOverride keeps the existing counter), so tweaking
// the cadence never erases accumulated manual bumps.
function OverrideModal({ userId, courseId, courseTitle, onClose }: { userId: number; courseId: number; courseTitle: string; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const overrideQ = useQuery({
    queryKey: ['unlock-override', userId, courseId],
    queryFn: () => api.getUnlockOverride(userId, courseId),
  });
  const initial: StrategyValue = overrideQ.data?.exists
    ? {
        strategy: overrideQ.data.strategy as UnlockStrategy,
        interval_seconds: overrideQ.data.interval_seconds || 86400,
        weekly_times: overrideQ.data.weekly_times ?? [],
      }
    : { strategy: 'all_open', interval_seconds: 86400, weekly_times: [] };
  const [val, setVal] = useState<StrategyValue>(initial);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['unlock-override', userId, courseId] });
    qc.invalidateQueries({ queryKey: ['unlock-preview', userId, courseId] });
  };

  const saveMut = useMutation({
    mutationFn: () =>
      api.saveUnlockOverride(userId, courseId, {
        strategy: val.strategy,
        interval_seconds: val.interval_seconds,
        weekly_times: val.weekly_times,
        // Keep whatever allowlist exists when not selected; selected mode's
        // allowlist is managed via the picker modal.
        allowed_episode_ids: overrideQ.data?.allowed_episode_ids ?? [],
      }),
    onSuccess: () => {
      toast.success('已为该学生覆盖策略');
      refresh();
      onClose();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const delMut = useMutation({
    mutationFn: () => api.deleteUnlockOverride(userId, courseId),
    onSuccess: () => {
      toast.success('已恢复沿用模板');
      refresh();
      onClose();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <Modal open onClose={onClose} title={`覆盖策略 · ${courseTitle}`} size="md">
      {overrideQ.isLoading ? (
        <div className="py-6 text-center text-sm text-muted">加载中…</div>
      ) : (
        <div className="space-y-4">
          <p className="text-xs text-muted">单独覆盖此学生的解锁节奏。留空 / 删除覆盖 = 沿用课程模板。</p>
          <UnlockStrategyEditor value={val} onChange={setVal} />
          <div className="flex justify-between">
            {overrideQ.data?.exists ? (
              <button className="btn-danger btn-sm" onClick={() => delMut.mutate()} disabled={delMut.isPending}>
                删除覆盖（沿用模板）
              </button>
            ) : (
              <span />
            )}
            <button className="btn-primary" onClick={() => saveMut.mutate()} disabled={saveMut.isPending}>
              {saveMut.isPending ? '保存中…' : '保存覆盖'}
            </button>
          </div>
        </div>
      )}
    </Modal>
  );
}

// EpisodeSelectModal — the cherry-picker for selected mode. Lists all
// episodes with checkboxes; save replaces the allowlist wholesale.
function EpisodeSelectModal({ userId, courseId, courseTitle, onClose }: { userId: number; courseId: number; courseTitle: string; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  // Dedicated key ('unlock-episodes') so this picker never shares or disturbs
  // the CourseTree editor's ['episodes', courseId] cache — the data is the same
  // but invalidation semantics differ (reordering in CourseTree shouldn't
  // retrigger this modal's fetch, and vice versa).
  const epsQ = useQuery({
    queryKey: ['unlock-episodes', courseId],
    queryFn: () => api.listEpisodes(courseId),
  });
  const overrideQ = useQuery({
    queryKey: ['unlock-override', userId, courseId],
    queryFn: () => api.getUnlockOverride(userId, courseId),
  });
  const episodes: Episode[] = epsQ.data ?? [];
  const initial = new Set<number>(overrideQ.data?.allowed_episode_ids ?? []);
  const [picked, setPicked] = useState<Set<number>>(initial);

  const toggle = (id: number) => {
    const next = new Set(picked);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setPicked(next);
  };

  const saveMut = useMutation({
    mutationFn: () => api.setAllowedEpisodes(userId, courseId, Array.from(picked).sort((a, b) => a - b)),
    onSuccess: () => {
      toast.success(`已设置 ${picked.size} 节可见`);
      qc.invalidateQueries({ queryKey: ['unlock-override', userId, courseId] });
      qc.invalidateQueries({ queryKey: ['unlock-preview', userId, courseId] });
      onClose();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <Modal open onClose={onClose} title={`勾选可见课时 · ${courseTitle}`} size="md">
      {epsQ.isLoading || overrideQ.isLoading ? (
        <div className="py-6 text-center text-sm text-muted">加载中…</div>
      ) : (
        <div className="space-y-3">
          <div className="flex items-center justify-between text-xs text-muted">
            <span>共 {episodes.length} 节，已选 {picked.size} 节</span>
            <div className="flex gap-1">
              <button className="btn-ghost btn-sm" onClick={() => setPicked(new Set(episodes.map((e) => e.id)))}>
                全选
              </button>
              <button className="btn-ghost btn-sm" onClick={() => setPicked(new Set())}>
                清空
              </button>
            </div>
          </div>
          <div className="max-h-72 space-y-1 overflow-auto">
            {episodes.map((ep) => (
              <label key={ep.id} className="flex items-center gap-2 rounded-lg border border-border bg-card px-3 py-1.5 text-sm">
                <input type="checkbox" checked={picked.has(ep.id)} onChange={() => toggle(ep.id)} className="h-4 w-4 accent-primary" />
                <span className="text-muted">#{ep.sort_order}</span>
                <span className="flex-1 text-txt">{ep.title}</span>
              </label>
            ))}
          </div>
          <button className="btn-primary w-full" onClick={() => saveMut.mutate()} disabled={saveMut.isPending}>
            {saveMut.isPending ? '保存中…' : '保存勾选'}
          </button>
        </div>
      )}
    </Modal>
  );
}
