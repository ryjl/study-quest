import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '../lib/api';
import { useToast } from '../lib/toast';
import type { SubtitleJob, SubtitleJobStatus } from '../lib/types';

// Human labels + Tailwind color classes per status. Kept inline since only
// this page renders status badges.
const STATUS_META: Record<SubtitleJobStatus, { label: string; cls: string }> = {
  queued: { label: '排队中', cls: 'bg-blue-500/15 text-blue-600' },
  processing: { label: '转录中', cls: 'bg-amber-500/15 text-amber-600' },
  done: { label: '已完成', cls: 'bg-emerald-500/15 text-emerald-600' },
  failed: { label: '失败', cls: 'bg-bad/15 text-bad' },
  skipped: { label: '已跳过', cls: 'bg-gray-500/15 text-muted' },
};

const STATUS_FILTERS: (SubtitleJobStatus | 'all')[] = ['all', 'queued', 'processing', 'failed', 'skipped', 'done'];

function fmtTime(s?: string | null): string {
  if (!s) return '—';
  try {
    return new Date(s).toLocaleString('zh-CN', { hour12: false });
  } catch {
    return s;
  }
}

export function SubtitleQueue() {
  const qc = useQueryClient();
  const toast = useToast();
  const [filter, setFilter] = useState<SubtitleJobStatus | 'all'>('all');

  // Poll the queue while there's queued or processing work, then stop. Mirrors
  // the probe-progress polling pattern in Layout.tsx.
  const jobsQ = useQuery({
    queryKey: ['subtitle-jobs', filter],
    queryFn: () => api.listSubtitleJobs(filter === 'all' ? undefined : filter),
    refetchInterval: (q) => {
      const hasActive = q.state.data?.some((j) => j.status === 'queued' || j.status === 'processing');
      return hasActive ? 3000 : false;
    },
    refetchIntervalInBackground: false,
  });

  const statsQ = useQuery({
    queryKey: ['subtitle-jobs-stats'],
    queryFn: api.subtitleJobStats,
    refetchInterval: (q) => (q.state.data?.running ? 3000 : false),
    refetchIntervalInBackground: false,
  });

  const skipMut = useMutation({
    mutationFn: api.skipSubtitleJob,
    onSuccess: () => {
      toast.success('已跳过');
      qc.invalidateQueries({ queryKey: ['subtitle-jobs'] });
      qc.invalidateQueries({ queryKey: ['subtitle-jobs-stats'] });
    },
    onError: (e) => toast.error((e as Error).message),
  });
  const retryMut = useMutation({
    mutationFn: api.retrySubtitleJob,
    onSuccess: () => {
      toast.success('已重新排队');
      qc.invalidateQueries({ queryKey: ['subtitle-jobs'] });
      qc.invalidateQueries({ queryKey: ['subtitle-jobs-stats'] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const stats = statsQ.data;
  const jobs = jobsQ.data ?? [];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">字幕队列</h1>
        <p className="mt-1 text-sm text-muted">
          勾选课时加入队列后，台式机上的 whisper worker 会认领并转录。转录完成的字幕会自动进库，播放器立即可用。
        </p>
      </div>

      {/* Stats bar */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Stat label="排队中" value={stats?.queued} tone="blue" />
        <Stat label="转录中" value={stats?.processing} tone="amber" />
        <Stat label="已完成" value={stats?.done} tone="emerald" />
        <Stat label="失败" value={stats?.failed} tone="bad" />
        <Stat label="已跳过" value={stats?.skipped} tone="muted" />
        <div className="rounded-xl border border-border bg-card p-3">
          <div className="text-[11px] text-muted">当前任务</div>
          <div className="mt-0.5 truncate text-sm font-medium">{stats?.current_title || '—'}</div>
        </div>
      </div>

      {/* Filter */}
      <div className="flex flex-wrap gap-1.5">
        {STATUS_FILTERS.map((f) => (
          <button
            key={f}
            onClick={() => setFilter(f)}
            className={`rounded-lg px-3 py-1.5 text-xs font-medium transition ${
              filter === f ? 'bg-primary text-white' : 'bg-card-2 text-muted hover:text-txt'
            }`}
          >
            {f === 'all' ? '全部' : STATUS_META[f].label}
          </button>
        ))}
      </div>

      {/* Jobs table */}
      <div className="overflow-hidden rounded-2xl border border-border bg-card">
        <table className="w-full text-sm">
          <thead className="border-b border-border bg-card-2 text-xs text-muted">
            <tr>
              <th className="px-4 py-3 text-left font-medium">课时</th>
              <th className="px-4 py-3 text-left font-medium">状态</th>
              <th className="px-4 py-3 text-left font-medium">Worker</th>
              <th className="px-4 py-3 text-left font-medium">优先级</th>
              <th className="px-4 py-3 text-left font-medium">尝试</th>
              <th className="px-4 py-3 text-left font-medium">更新时间</th>
              <th className="px-4 py-3 text-left font-medium">错误</th>
              <th className="px-4 py-3 text-right font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {jobs.length === 0 && (
              <tr>
                <td colSpan={8} className="px-4 py-10 text-center text-muted">
                  {jobsQ.isLoading ? '加载中…' : '暂无任务'}
                </td>
              </tr>
            )}
            {jobs.map((j) => (
              <JobRow key={j.id} job={j} onSkip={skipMut.mutate} onRetry={retryMut.mutate} busy={skipMut.isPending || retryMut.isPending} />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function JobRow({
  job,
  onSkip,
  onRetry,
  busy,
}: {
  job: SubtitleJob;
  onSkip: (id: number) => void;
  onRetry: (id: number) => void;
  busy: boolean;
}) {
  const meta = STATUS_META[job.status];
  const actionable = job.status === 'failed' || job.status === 'queued' || job.status === 'processing';
  return (
    <tr className="border-b border-border/60 last:border-0 hover:bg-card-2/50">
      <td className="px-4 py-3">
        <div className="font-medium">{job.episode_title || `#${job.episode_id}`}</div>
      </td>
      <td className="px-4 py-3">
        <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${meta.cls}`}>{meta.label}</span>
      </td>
      <td className="px-4 py-3 text-xs text-muted">
        {job.claimed_by ? <span title="正在/曾经处理此任务的 worker">{job.claimed_by}</span> : '—'}
      </td>
      <td className="px-4 py-3 text-muted">{job.priority}</td>
      <td className="px-4 py-3 text-muted">{job.attempt}</td>
      <td className="px-4 py-3 text-xs text-muted">{fmtTime(job.updated_at)}</td>
      <td className="max-w-[280px] px-4 py-3">
        {job.error ? <span className="line-clamp-2 text-xs text-bad" title={job.error}>{job.error}</span> : '—'}
      </td>
      <td className="px-4 py-3 text-right">
        {actionable && (
          <div className="flex justify-end gap-1.5">
            {job.status === 'failed' && (
              <button className="btn-ghost btn-sm" disabled={busy} onClick={() => onRetry(job.id)}>
                重试
              </button>
            )}
            <button className="btn-ghost btn-sm" disabled={busy} onClick={() => onSkip(job.id)}>
              跳过
            </button>
          </div>
        )}
      </td>
    </tr>
  );
}

function Stat({ label, value, tone }: { label: string; value?: number; tone: 'blue' | 'amber' | 'emerald' | 'bad' | 'muted' }) {
  const toneCls = {
    blue: 'text-blue-600',
    amber: 'text-amber-600',
    emerald: 'text-emerald-600',
    bad: 'text-bad',
    muted: 'text-muted',
  }[tone];
  return (
    <div className="rounded-xl border border-border bg-card p-3">
      <div className="text-[11px] text-muted">{label}</div>
      <div className={`mt-0.5 text-2xl font-bold ${toneCls}`}>{value ?? 0}</div>
    </div>
  );
}
