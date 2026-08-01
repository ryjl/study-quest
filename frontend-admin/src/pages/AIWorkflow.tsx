import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { AiJob, AiJobStatus, AiJobsResponse, AiRun, AiTraceStep } from '../lib/types';
import { fmtTime } from '../lib/format';
import { STATUS_META, STATUS_FILTERS } from '../lib/jobStatus';
import { jobTypeLabel } from '../lib/jobType';
import { pollWhen } from '../lib/query';
import { useTypedMutation } from '../lib/useTypedMutation';
import { Modal } from '../components/ui';
import { PageHeader } from '../components/PageHeader';

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

// embedded=true 时不渲染自己的 PageHeader —— 由父页面(任务队列页 /admin/ai/jobs)提供
// 统一标题。实际上 embedded 总是 true(任务队列是它的唯一入口);但保留 embedded 默认值
// false 是为了将来万一有别的嵌入场景或独立路由复活时不破坏 API。
export function AIWorkflow({ embedded = false }: { embedded?: boolean } = {}) {
  const [filter, setFilter] = useState<AiJobStatus | 'all'>('all');

  // Jobs + stats come back together from one endpoint. We poll only while
  // there's queued/processing work, mirroring SubtitleQueue's pattern.
  const jobsQ = useQuery({
    queryKey: ['ai-jobs', null, filter],
    queryFn: () => api.listAiJobs(undefined, filter === 'all' ? undefined : filter),
    refetchInterval: pollWhen(
      (data: AiJobsResponse | undefined) =>
        !!data?.jobs.some((j) => j.status === 'queued' || j.status === 'processing'),
    ),
    refetchIntervalInBackground: false,
  });

  // Recent decision-trace runs. The list endpoint serves the last N runs; we
  // poll lightly while jobs are in flight so a fresh run appears here as soon
  // as the agent finishes it, then stop.
  const runsQ = useQuery({
    queryKey: ['ai-runs', 20],
    queryFn: () => api.listAiRuns(20),
    refetchInterval: pollWhen(
      (data: AiRun[] | undefined) =>
        !!data?.some((r) => Date.now() - new Date(r.created_at).getTime() < 60_000),
      5000,
    ),
    refetchIntervalInBackground: false,
  });

  const stats = jobsQ.data?.stats;
  const jobs = jobsQ.data?.jobs ?? [];
  const runs = runsQ.data ?? [];

  return (
    <div className="space-y-6">
      {!embedded && (
        <PageHeader
          title="AI Workflow"
          breadcrumb={[{ label: 'AI 运营' }]}
          description="观测 AI 任务队列与 agent 决策痕迹。失败任务可重试。"
        />
      )}

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

// DetailCell renders the job's detail/error string with status-aware coloring
// and click-to-expand. job.error carries success telemetry (polish's
// "polished: N/M cues changed…", summary's empty) AND failure messages, so
// coloring purely red was wrong — a green-looking polish job used to render as
// an error. Only 'failed' is red now; everything else is neutral muted. Long
// partial-failure detail ("(partial: 1/5 chunks failed; chunk#3: parse polish
// json…)") collapses to 2 lines by default and expands on click so the actual
// failure cause is reachable instead of truncated away.
function DetailCell({ status, text }: { status: AiJobStatus; text: string }) {
  const [expanded, setExpanded] = useState(false);
  const isFailed = status === 'failed';
  const cls = isFailed ? 'text-bad' : 'text-muted';
  const long = text.length > 80;
  return (
    <span
      className={`text-xs ${cls} ${expanded || !long ? 'whitespace-pre-wrap break-words' : 'line-clamp-2'} ${long ? 'cursor-pointer' : ''}`}
      title={text}
      onClick={long ? () => setExpanded((v) => !v) : undefined}
    >
      {text}
      {long && !expanded && <span className="text-muted"> (点击展开)</span>}
    </span>
  );
}

function JobRow({ job }: { job: AiJob }) {
  const meta = STATUS_META[job.status];
  const showProgress = job.status === 'processing' && job.progress != null;

  // Manual reset of a stuck 'processing' job (admin counterpart of the auto
  // reaper). Inherited from SubtitleQueue's retryMut pattern: invalidate the
  // jobs list on success so the row re-renders as 'queued'. The 409 (not
  // processing) path surfaces as a benign toast, not a scary error.
  const resetMut = useTypedMutation({
    mutationFn: () => api.resetAiJob(job.id),
    successMsg: '已重置回排队',
    invalidateKeys: [['ai-jobs']],
  });

  // Retry a 'failed' job: revive it to 'queued' so the worker re-runs it. Use
  // case: the job failed (e.g. embedding was misconfigured), the admin fixed the
  // problem, now they want to re-run. Distinct from resetMut (which targets
  // stuck-but-alive 'processing' jobs). 409 (not failed) → benign toast.
  const retryMut = useTypedMutation({
    mutationFn: () => api.retryAiJob(job.id),
    successMsg: '已重新排队,worker 将重试',
    invalidateKeys: [['ai-jobs']],
  });

  // Skip polish: polish-specific escape hatch for a FAILED polish job. Polish
  // failures HALT the chain (segment never enqueues), so the admin must decide
  // — retry (retryMut above) or give up on polish and let downstream proceed
  // off the raw subtitle. Marks the job done + chains segment. 409 (not a
  // failed polish job) → benign toast.
  const skipPolishMut = useTypedMutation({
    mutationFn: () => api.skipPolish(job.id),
    successMsg: '已跳过润色,切片任务已入队(用原始字幕)',
    invalidateKeys: [['ai-jobs']],
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
        {job.error ? (
          // job.error carries BOTH success detail strings (e.g. polish's
          // "polished: N/M cues changed…") and failure messages — UpdateJobStatus
          // writes the detail into the same column for done/failed/skipped. The
          // old code rendered all of them in text-bad (red), making a successful
          // polish look like an error. Color by status instead: only 'failed' is
          // red; done/skipped/processing are neutral. Expandable so the (partial:
          // chunk#N: …) detail that explains WHY a chunk failed isn't truncated
          // out of view.
          <DetailCell status={job.status} text={job.error} />
        ) : (
          '—'
        )}
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
          <div className="inline-flex gap-1">
            <button
              className="btn-ghost btn-sm"
              disabled={retryMut.isPending}
              onClick={() => retryMut.mutate()}
              title="重新排队重试(修复了失败原因后用这个)"
            >
              {retryMut.isPending ? '重试中…' : '重试'}
            </button>
            {job.job_type === 'polish' && (
              <button
                className="btn-ghost btn-sm"
                disabled={skipPolishMut.isPending}
                onClick={() => skipPolishMut.mutate()}
                title="放弃润色,用原始字幕继续切片/总结(润色失败且不想重试时用)"
              >
                {skipPolishMut.isPending ? '跳过中…' : '跳过润色'}
              </button>
            )}
          </div>
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
              <th className="px-4 py-2.5 text-left font-medium">课程 / 课时</th>
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
                <td className="px-4 py-3">
                  {/* 课程 / 课时:后端 AIRunView enrich 出来的标题(经 run.job →
                      episode/course/user 解析)。subject-scope 的 advice job 没
                      episode/course,这里两行都为空 → 显示 —。 */}
                  <div className="text-xs text-txt">{r.course_title || <span className="text-muted">—</span>}</div>
                  <div className="text-[11px] text-muted">{r.episode_title || (r.user_nickname ? `学生: ${r.user_nickname}` : '')}</div>
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
  // 完整 Prompt 折叠区:展示本次 Run 发给 LLM 的 system+user prompt。老 run 没有这俩
  // 字段(主会话引入前的历史数据),兜底显示"(本次 run 未记录 prompt)"。
  const hasPrompt = !!(run.system_prompt_text || run.user_prompt_text);
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

      {/* 完整 Prompt 折叠区:默认展开(老 run 没数据时隐藏整段)。
          system_prompt 是代码常量(冗余存一份);user_prompt 是拼装结果(含 hint/TermDict 注入)。
          让 admin 看到"这次到底发了什么给 LLM",告别盲调 prompt。 */}
      {hasPrompt && (
        <details open className="rounded-xl border border-border bg-card-2">
          <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium text-txt">
            完整 Prompt (system + user)
          </summary>
          <div className="space-y-3 border-t border-border p-3">
            <div>
              <div className="mb-1 text-[11px] font-medium text-muted">System Prompt</div>
              <pre className="max-h-72 overflow-auto whitespace-pre-wrap rounded-lg border border-border bg-card p-3 text-[11px] text-txt">{run.system_prompt_text || '(空)'}</pre>
            </div>
            <div>
              <div className="mb-1 text-[11px] font-medium text-muted">User Prompt</div>
              <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-lg border border-border bg-card p-3 text-[11px] text-txt">{run.user_prompt_text || '(空)'}</pre>
            </div>
          </div>
        </details>
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
