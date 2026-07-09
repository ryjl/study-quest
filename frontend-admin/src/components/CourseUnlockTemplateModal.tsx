import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { LoadingState, Modal } from './ui';
import { UnlockStrategyEditor, type StrategyValue } from './UnlockStrategyEditor';
import { useToast } from '../lib/toast';
import type { UnlockTemplate, UnlockStrategy } from '../lib/types';

const EMPTY: StrategyValue = { strategy: 'all_open', interval_seconds: 86400, weekly_times: [] };

// Modal editor for a course's default unlock strategy template. Shared by the
// Courses page (per-card "解锁" button) — no longer a standalone nav tab, since
// the cadence is a property of the course and reads better edited in place.
//
// `onSaved` lets the parent refresh whatever it showed about this course's
// strategy (e.g. a header badge), beyond the internal cache invalidation.
export function CourseUnlockTemplateModal({
  courseId,
  courseTitle,
  onClose,
  onSaved,
}: {
  courseId: number;
  courseTitle: string;
  onClose: () => void;
  onSaved?: () => void;
}) {
  const tplQ = useQuery({
    queryKey: ['unlock-template', courseId],
    queryFn: () => api.getUnlockTemplate(courseId),
  });

  return (
    <Modal open onClose={onClose} title={`解锁策略 · ${courseTitle}`} size="md">
      {tplQ.isLoading ? (
        <LoadingState />
      ) : (
        <TemplateForm
          courseId={courseId}
          template={tplQ.data}
          onClose={onClose}
          onSaved={onSaved}
        />
      )}
    </Modal>
  );
}

// TemplateForm is mounted ONLY after the template query resolves, so its
// useState lazy-init picks up the real loaded value (not EMPTY). This avoids
// the classic "useState pins the first (loading) value and ignores later data"
// bug without needing a remount key or useEffect sync.
function TemplateForm({
  courseId,
  template,
  onClose,
  onSaved,
}: {
  courseId: number;
  template: UnlockTemplate | undefined;
  onClose: () => void;
  onSaved?: () => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const initial: StrategyValue = template
    ? {
        strategy: template.strategy as UnlockStrategy,
        interval_seconds: template.interval_seconds || 86400,
        weekly_times: template.weekly_times ?? [],
      }
    : EMPTY;
  const [val, setVal] = useState<StrategyValue>(initial);

  const saveMut = useMutation({
    mutationFn: () =>
      api.saveUnlockTemplate(courseId, {
        strategy: val.strategy,
        interval_seconds: val.interval_seconds,
        weekly_times: val.weekly_times,
      }),
    onSuccess: () => {
      toast.success('解锁策略已保存');
      qc.invalidateQueries({ queryKey: ['unlock-template', courseId] });
      qc.invalidateQueries({ queryKey: ['unlock-template'] });
      qc.invalidateQueries({ queryKey: ['unlock-preview'] });
      onSaved?.();
      onClose();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const delMut = useMutation({
    mutationFn: () => api.deleteUnlockTemplate(courseId),
    onSuccess: () => {
      toast.success('已重置为「全部开放」默认');
      qc.invalidateQueries({ queryKey: ['unlock-template', courseId] });
      qc.invalidateQueries({ queryKey: ['unlock-template'] });
      onSaved?.();
      onClose();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <div className="space-y-4">
      <p className="text-xs text-muted">
        这是该课程的默认解锁节奏。分配课程时若未单独覆盖，所有学生沿用此模板。无模板 = 全部开放（兼容旧行为）。
      </p>
      <UnlockStrategyEditor value={val} onChange={setVal} />
      <div className="flex justify-between">
        {template?.exists ? (
          <button className="btn-danger btn-sm" onClick={() => delMut.mutate()} disabled={delMut.isPending}>
            重置为全部开放
          </button>
        ) : (
          <span />
        )}
        <button className="btn-primary" onClick={() => saveMut.mutate()} disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中…' : '保存'}
        </button>
      </div>
    </div>
  );
}
