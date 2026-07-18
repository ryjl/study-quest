import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { useToast } from '../lib/toast';
import type { AiJob, AiJobStatus, AiRun, AiTraceStep } from '../lib/types';
import { Modal } from '../components/ui';
import { PageHeader } from '../components/PageHeader';

// Status badge palette. Kept inline since only this page renders these badges.
// Follows the SubtitleQueue pattern: tailwind palette swatches per status,
// `bad` token for failures so it tracks the theme.
const STATUS_META: Record<AiJobStatus, { label: string; cls: string }> = {
  queued: { label: '排队中', cls: 'bg-blue-500/15 text-blue-600' },
  processing: { label: '处理中', cls: 'bg-amber-500/15 text-amber-600' },
  done: { label: '已完成', cls: 'bg-emerald-500/15 text-emerald-600' },
  failed: { label: '失败', cls: 'bg-bad/15 text-bad' },
  skipped: { label: '已跳过', cls: 'bg-gray-500/15 text-muted' },
};

const STATUS_FILTERS: (AiJobStatus | 'all')[] = ['all', 'queued', 'processing', 'done', 'failed', 'skipped'];

// job_type → Chinese label. The backend job_type is a free string today; map
// the two known ones and pass anything else through verbatim.
const JOB_TYPE_LABEL: Record<string, string> = {
  slice: '切片',
  summarize: '总结',
  quiz: '出题',
};

function jobTypeLabel(t: string): string {
  return JOB_TYPE_LABEL[t] ?? t;
}

function fmtTime(s?: string | null): string {
  if (!s) return '—';
  try {
    return new Date(s).toLocaleString('zh-CN', { hour12: false });
  } catch {
    return s;
  }
}

// Wall-clock duration of a finished job: completed_at - created_at, in seconds.
// Returns '—' if either bound is missing (e.g. still processing).
function jobDuration(j: AiJob): string {
  if (!j.completed_at || !j.created_at) return '—';
  const ms = new Date(j.completed_at).getTime() - new Date(j.created_at).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '—';
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  return `${Math.floor(s / 60)}m${Math.round(s % 60)}s`;
}

export function AIWorkflow() {
  const [filter, setFilter] = useState<AiJobStatus | 'all'>('all');

  // Jobs + stats come back together from one endpoint. We poll only while
  // there's queued/processing work, mirroring SubtitleQueue's pattern.
  const jobsQ = useQuery({
    queryKey: ['ai-jobs', null, filter],
    queryFn: () => api.listAiJobs(undefined, filter === 'all' ? undefined : filter),
    refetchInterval: (q) => {
      const hasActive = q.state.data?.jobs.some((j) => j.status === 'queued' || j.status === 'processing');
      return hasActive ? 3000 : false;
    },
    refetchIntervalInBackground: false,
  });

  // Recent decision-trace runs. The list endpoint serves the last N runs; we
  // poll lightly while jobs are in flight so a fresh run appears here as soon
  // as the agent finishes it, then stop.
  const runsQ = useQuery({
    queryKey: ['ai-runs', 20],
    queryFn: () => api.listAiRuns(20),
    refetchInterval: (q) => {
      const hasActive = q.state.data?.some((r) => Date.now() - new Date(r.created_at).getTime() < 60_000);
      return hasActive ? 5000 : false;
    },
    refetchIntervalInBackground: false,
  });

  const stats = jobsQ.data?.stats;
  const jobs = jobsQ.data?.jobs ?? [];
  const runs = runsQ.data ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="AI Workflow"
        breadcrumb={[{ label: 'AI 运营' }]}
        description="观测 AI 任务队列与 agent 决策痕迹。失败任务可重试。"
      />

      {/* Stats bar */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <Stat label="排队中" value={stats?.queued} tone="blue" />
        <Stat label="处理中" value={stats?.processing} tone="amber" />
        <Stat label="已完成" value={stats?.done} tone="emerald" />
        <Stat label="失败" value={stats?.failed} tone="bad" />
        <Stat label="已跳过" value={stats?.skipped} tone="muted" />
      </div>

      {/* Jobs section */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-base font-semibold">任务队列</h2>
          <div className="flex flex-wrap gap-1.5">
            {STATUS_FILTERS.map((f) => (
              <button
                key={f}
                onClick={() => setFilter(f)}
                className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                  filter === f ? 'bg-txt text-bg' : 'text-muted hover:bg-card-2 hover:text-txt'
                }`}
              >
                {f === 'all' ? '全部' : STATUS_META[f].label}
              </button>
            ))}
          </div>
        </div>

        <div className="overflow-hidden rounded-lg border border-border/60 bg-card">
          <table className="w-full text-sm">
            <thead className="border-b border-border bg-card-2 text-xs uppercase tracking-wide text-muted">
              <tr>
                <th className="px-4 py-2.5 text-left font-medium">类型</th>
                <th className="px-4 py-2.5 text-left font-medium">状态</th>
                <th className="px-4 py-2.5 text-left font-medium">Episode</th>
                <th className="px-4 py-2.5 text-left font-medium">进度</th>
                <th className="px-4 py-2.5 text-left font-medium">耗时</th>
                <th className="px-4 py-2.5 text-left font-medium">创建时间</th>
                <th className="px-4 py-2.5 text-left font-medium">错误</th>
                <th className="px-4 py-2.5 text-right font-medium">操作</th>
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
                <JobRow key={j.id} job={j} />
              ))}
            </tbody>
          </table>
        </div>
      </div>

        {/* Decision-trace section */}
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-base font-semibold">决策痕迹（最近运行）</h2>
            <span className="text-xs text-muted">点击展开查看输入 / 完整响应</span>
          </div>
          <RunList runs={runs} loading={runsQ.isLoading} />
        </div>
    </div>
  );
}

function JobRow({ job }: { job: AiJob }) {
  const qc = useQueryClient();
  const toast = useToast();
  const meta = STATUS_META[job.status];
  const showProgress = job.status === 'processing' && job.progress != null;

  // Manual reset of a stuck 'processing' job (admin counterpart of the auto
  // reaper). Inherited from SubtitleQueue's retryMut pattern: invalidate the
  // jobs list on success so the row re-renders as 'queued'. The 409 (not
  // processing) path surfaces as a benign toast, not a scary error.
  const resetMut = useMutation({
    mutationFn: () => api.resetAiJob(job.id),
    onSuccess: () => {
      toast.success('已重置回排队');
      qc.invalidateQueries({ queryKey: ['ai-jobs'] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  // Retry a 'failed' job: revive it to 'queued' so the worker re-runs it. Use
  // case: the job failed (e.g. embedding was misconfigured), the admin fixed the
  // problem, now they want to re-run. Distinct from resetMut (which targets
  // stuck-but-alive 'processing' jobs). 409 (not failed) → benign toast.
  const retryMut = useMutation({
    mutationFn: () => api.retryAiJob(job.id),
    onSuccess: () => {
      toast.success('已重新排队,worker 将重试');
      qc.invalidateQueries({ queryKey: ['ai-jobs'] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <tr className="border-b border-border/60 last:border-0 hover:bg-card-2/50">
      <td className="px-4 py-3">
        <span className="font-medium">{jobTypeLabel(job.job_type)}</span>
        <span className="ml-1.5 text-[11px] text-muted">{job.job_type}</span>
        {job.user_nickname && (
          <span className="ml-1.5 rounded-full bg-blue-500/10 px-2 py-0.5 text-[11px] text-blue-600" title="此任务的定向用户">
            {job.user_nickname}
          </span>
        )}
      </td>
      <td className="px-4 py-3">
        <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${meta.cls}`}>{meta.label}</span>
      </td>
      <td className="px-4 py-3">
        <div className="font-medium text-txt">{job.episode_title || `#${job.episode_id}`}</div>
        <div className="text-[11px] text-muted">{job.course_title ? `${job.course_title}` : `课程 ${job.course_id}`}</div>
      </td>
      <td className="px-4 py-3">
        {showProgress ? (
          <div className="flex items-center gap-2">
            <div className="h-1.5 w-24 overflow-hidden rounded-full bg-card-2">
              <div className="h-full rounded-full bg-primary" style={{ width: `${Math.round((job.progress ?? 0) * 100)}%` }} />
            </div>
            <span className="text-xs text-muted">{Math.round((job.progress ?? 0) * 100)}%</span>
          </div>
        ) : (
          <span className="text-muted">—</span>
        )}
      </td>
      <td className="px-4 py-3 text-xs text-muted">{jobDuration(job)}</td>
      <td className="px-4 py-3 text-xs text-muted">{fmtTime(job.created_at)}</td>
      <td className="max-w-[280px] px-4 py-3">
        {job.error ? <span className="line-clamp-2 text-xs text-bad" title={job.error}>{job.error}</span> : '—'}
      </td>
      <td className="px-4 py-3 text-right">
        {job.status === 'processing' && (
          <button
            className="btn-ghost btn-sm"
            disabled={resetMut.isPending}
            onClick={() => resetMut.mutate()}
            title="重置回排队(worker 可能卡住时手动触发)"
          >
            重置
          </button>
        )}
        {job.status === 'failed' && (
          <button
            className="btn-ghost btn-sm"
            disabled={retryMut.isPending}
            onClick={() => retryMut.mutate()}
            title="重新排队重试(修复了失败原因后用这个)"
          >
            {retryMut.isPending ? '重试中…' : '重试'}
          </button>
        )}
      </td>
    </tr>
  );
}

function RunList({ runs, loading }: { runs: AiRun[]; loading: boolean }) {
  const [openId, setOpenId] = useState<number | null>(null);
  const openRun = runs.find((r) => r.id === openId) ?? null;

  if (loading) {
    return <div className="rounded-lg border border-border/60 bg-card px-4 py-10 text-center text-sm text-muted">加载中…</div>;
  }
  if (runs.length === 0) {
    return <div className="rounded-lg border border-border/60 bg-card px-4 py-10 text-center text-sm text-muted">暂无运行记录</div>;
  }

  return (
    <>
      <div className="overflow-hidden rounded-lg border border-border/60 bg-card">
        <table className="w-full text-sm">
          <thead className="border-b border-border bg-card-2 text-xs uppercase tracking-wide text-muted">
            <tr>
              <th className="px-4 py-2.5 text-left font-medium">能力</th>
              <th className="px-4 py-2.5 text-left font-medium">模型</th>
              <th className="px-4 py-2.5 text-left font-medium">Tokens (输入/输出)</th>
              <th className="px-4 py-2.5 text-left font-medium">耗时</th>
              <th className="px-4 py-2.5 text-left font-medium">自检</th>
              <th className="px-4 py-2.5 text-left font-medium">时间</th>
              <th className="px-4 py-2.5 text-right font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {runs.map((r) => (
              <tr key={r.id} className="border-b border-border/60 last:border-0 hover:bg-card-2/50">
                <td className="px-4 py-3">
                  <span className="font-medium">{r.capability}</span>
                  <span className="ml-1.5 text-[11px] text-muted">#{r.job_id}</span>
                </td>
                <td className="px-4 py-3 text-xs text-muted">{r.model_used || '—'}</td>
                <td className="px-4 py-3 text-xs text-muted">
                  {r.prompt_tokens} / {r.completion_tokens}
                </td>
                <td className="px-4 py-3 text-xs text-muted">{r.duration_ms}ms</td>
                <td className="px-4 py-3">
                  <SelfCheckBadge result={r.self_check_result} />
                </td>
                <td className="px-4 py-3 text-xs text-muted">{fmtTime(r.created_at)}</td>
                <td className="px-4 py-3 text-right">
                  <button className="btn-ghost btn-sm" onClick={() => setOpenId(r.id)}>
                    查看回放
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <Modal open={openRun != null} onClose={() => setOpenId(null)} title={`运行回放 #${openRun?.id ?? ''}`} size="xl">
        {openRun && <RunDetail run={openRun} />}
      </Modal>
    </>
  );
}

function SelfCheckBadge({ result }: { result?: string }) {
  if (!result) return <span className="text-muted">—</span>;
  const r = result.toLowerCase();
  // pass/ok = good; fail = bad; regenerated = amber (first attempt failed,
  // retried but not re-checked — honest middle state); skipped/other = muted.
  const cls =
    r === '' || r === 'pass' || r === 'ok'
      ? 'bg-emerald-500/15 text-emerald-600'
      : r.includes('fail')
        ? 'bg-bad/15 text-bad'
        : r === 'regenerated'
          ? 'bg-amber-500/15 text-amber-600'
          : 'bg-gray-500/15 text-muted';
  return <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${cls}`}>{result}</span>;
}

function RunDetail({ run }: { run: AiRun }) {
  // Pretty-print JSON input if possible; fall back to the raw string.
  let prettyInput = run.input_json;
  try {
    if (run.input_json) prettyInput = JSON.stringify(JSON.parse(run.input_json), null, 2);
  } catch {
    /* not JSON — keep raw */
  }
  // Parse the ReAct trace (quiz runs carry it; summary runs don't).
  let trace: AiTraceStep[] | null = null;
  try {
    if (run.trace_json) trace = JSON.parse(run.trace_json);
  } catch {
    /* malformed — leave null */
  }
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
        <Meta label="能力" value={run.capability} />
        <Meta label="模型" value={run.model_used || '—'} />
        <Meta label="Tokens" value={`${run.prompt_tokens} / ${run.completion_tokens}`} />
        <Meta label="耗时" value={`${run.duration_ms}ms`} />
      </div>

      {/* The ReAct "思考时间线": the agent's step-by-step reasoning. This is the
          learning centerpiece — each step shows the tool it called (with args)
          and the observation it got back. Empty for single-shot runs (summary). */}
      {trace && trace.length > 0 && (
        <div>
          <div className="mb-1 text-xs font-medium text-muted">思考时间线 (ReAct 循环)</div>
          <ol className="space-y-2">
            {trace.map((s) => (
              <li key={s.step} className="rounded-xl border border-border bg-card-2 p-3 text-xs">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-[11px] text-muted">#{s.step}</span>
                  <span className="font-medium text-txt">{s.thought}</span>
                  {s.is_final && <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] text-emerald-600">最终答案</span>}
                </div>
                {s.action && (
                  <div className="mt-1 font-mono text-[11px] text-blue-600">
                    → {s.action.tool}({s.action.args || ''})
                  </div>
                )}
                {s.observation && (
                  <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap text-[11px] text-muted">{s.observation}</pre>
                )}
              </li>
            ))}
          </ol>
        </div>
      )}

      <div>
        <div className="mb-1 text-xs font-medium text-muted">输入 (input_json)</div>
        <pre className="max-h-64 overflow-auto rounded-xl border border-border bg-card-2 p-3 text-xs text-txt">{prettyInput || '(空)'}</pre>
      </div>

      <div>
        <div className="mb-1 text-xs font-medium text-muted">响应 (response_text)</div>
        <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-xl border border-border bg-card-2 p-3 text-xs text-txt">{run.response_text || '(空)'}</pre>
      </div>
    </div>
  );
}

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-border bg-card-2 p-2.5">
      <div className="text-[11px] text-muted">{label}</div>
      <div className="mt-0.5 truncate text-sm font-medium">{value}</div>
    </div>
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
    <div className="rounded-lg border border-border/60 bg-card p-3">
      <div className="text-[11px] text-muted">{label}</div>
      <div className={`mt-0.5 text-xl font-semibold tabular-nums ${toneCls}`}>{value ?? 0}</div>
    </div>
  );
}
