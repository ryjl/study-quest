import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { LogEntry } from '../lib/types';
import { fmtTime } from '../lib/format';
import { pollWhen } from '../lib/query';
import { PageHeader } from '../components/PageHeader';

// Logs.tsx — /admin/logs page (TODO.md P1). Operational event stream from the
// AI/subtitle worker: job failures, reaper runs, polish telemetry, provider
// errors, worker panics. The point is admin observability WITHOUT SSH — these
// land in log_entries via service.appendLog and surface here.
//
// Mirrors AIWorkflow's shape (PageHeader + filter bar + table + pollWhile tail).
// Level/source filter buttons + a free-text filter on message/fields. Polls
// while the newest entry is < 60s old (a tail view), matching runsQ's behavior.

const LEVELS = ['all', 'error', 'warn', 'info'] as const;
const SOURCES = ['all', 'ai_worker', 'reaper', 'polish', 'provider', 'segment'] as const;

// levelMeta maps a level to its badge color. error=red (failJob/panic/provider),
// warn=amber (reaper), info=neutral (polish telemetry). Matches the text-bad /
// amber conventions elsewhere so the severity reads at a glance.
const levelMeta: Record<string, string> = {
  error: 'bg-red-500/15 text-red-600',
  warn: 'bg-amber-500/15 text-amber-600',
  info: 'bg-card-2 text-muted',
};

export function Logs() {
  const [level, setLevel] = useState<string>('all');
  const [source, setSource] = useState<string>('all');
  const [q, setQ] = useState('');

  const logsQ = useQuery({
    queryKey: ['logs', level, source],
    queryFn: () =>
      api.listLogs({
        level: level === 'all' ? undefined : level,
        source: source === 'all' ? undefined : source,
        limit: 200,
      }),
    // Tail: poll while the newest entry is fresh (< 60s), so a live failure /
    // reaper event shows up without a manual refresh. Stops when the tab is
    // hidden (refetchIntervalInBackground: false) to avoid background chatter.
    refetchInterval: pollWhen(
      (data: LogEntry[] | undefined) =>
        !!data && data.length > 0 && Date.now() - new Date(data[0].created_at).getTime() < 60_000,
    ),
    refetchIntervalInBackground: false,
  });

  const rows = logsQ.data ?? [];
  const filtered = q.trim()
    ? rows.filter((r) => (r.message + ' ' + (r.fields_json ?? '')).toLowerCase().includes(q.trim().toLowerCase()))
    : rows;

  return (
    <>
      <PageHeader
        title="系统日志"
        description="AI / 字幕 worker 的运行事件（任务失败、reaper、润色遥测、provider 错误、worker panic）。替代 SSH 看 stderr。"
      />

      {/* Filter bar */}
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <div className="flex flex-wrap gap-1.5">
          {LEVELS.map((l) => (
            <button
              key={l}
              onClick={() => setLevel(l)}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                level === l ? 'bg-txt text-bg' : 'text-muted hover:bg-card-2 hover:text-txt'
              }`}
            >
              {l === 'all' ? '全部级别' : l}
            </button>
          ))}
        </div>
        <div className="flex flex-wrap gap-1.5">
          {SOURCES.map((s) => (
            <button
              key={s}
              onClick={() => setSource(s)}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                source === s ? 'bg-txt text-bg' : 'text-muted hover:bg-card-2 hover:text-txt'
              }`}
            >
              {s === 'all' ? '全部来源' : s}
            </button>
          ))}
        </div>
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="过滤消息 / 字段…"
          className="ml-auto w-56 rounded-md border border-border bg-card px-3 py-1.5 text-xs text-txt placeholder:text-muted focus:border-primary focus:outline-none"
        />
      </div>

      {/* Table */}
      <div className="overflow-hidden rounded-lg border border-border/60 bg-card">
        <table className="w-full text-sm">
          <thead className="border-b border-border bg-card-2 text-xs uppercase tracking-wide text-muted">
            <tr>
              <th className="px-4 py-2.5 text-left font-medium">时间</th>
              <th className="px-4 py-2.5 text-left font-medium">级别</th>
              <th className="px-4 py-2.5 text-left font-medium">来源</th>
              <th className="px-4 py-2.5 text-left font-medium">课程 / 课时</th>
              <th className="px-4 py-2.5 text-left font-medium">消息</th>
            </tr>
          </thead>
          <tbody>
            {logsQ.isLoading && (
              <tr>
                <td colSpan={5} className="px-4 py-10 text-center text-muted">
                  加载中…
                </td>
              </tr>
            )}
            {!logsQ.isLoading && filtered.length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-10 text-center text-muted">
                  暂无日志
                </td>
              </tr>
            )}
            {filtered.map((r) => (
              <LogRow key={r.id} entry={r} />
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function LogRow({ entry }: { entry: LogEntry }) {
  const [expanded, setExpanded] = useState(false);
  const hasFields = !!entry.fields_json && entry.fields_json !== '{}';
  return (
    <>
      <tr
        className={`border-b border-border/60 last:border-0 hover:bg-card-2/50 ${
          hasFields ? 'cursor-pointer' : ''
        }`}
        onClick={hasFields ? () => setExpanded((v) => !v) : undefined}
      >
        <td className="whitespace-nowrap px-4 py-3 text-xs text-muted">{fmtTime(entry.created_at)}</td>
        <td className="px-4 py-3">
          <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${levelMeta[entry.level] ?? levelMeta.info}`}>
            {entry.level}
          </span>
        </td>
        <td className="px-4 py-3 text-xs text-muted">{entry.source}</td>
        <td className="px-4 py-3 text-xs">
          <div className="text-txt">{entry.course_title || <span className="text-muted">—</span>}</div>
          <div className="text-[11px] text-muted">{entry.episode_title || (entry.job_id ? `job #${entry.job_id}` : '')}</div>
        </td>
        <td className="px-4 py-3 text-xs text-txt">
          <span className="whitespace-pre-wrap break-words">{entry.message}</span>
          {hasFields && (
            <span className="ml-1 text-muted">{expanded ? '（收起）' : '（展开字段）'}</span>
          )}
        </td>
      </tr>
      {expanded && hasFields && (
        <tr className="border-b border-border/60 bg-card-2/30">
          <td colSpan={5} className="px-4 py-2">
            <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded-lg border border-border bg-card p-3 text-[11px] text-muted">
              {prettyJSON(entry.fields_json)}
            </pre>
          </td>
        </tr>
      )}
    </>
  );
}

// prettyJSON pretty-prints a JSON string; falls back to the raw string if it
// isn't valid JSON (defensive — appendLog callers hand-build JSON, but a future
// caller might pass something else).
function prettyJSON(s?: string): string {
  if (!s) return '';
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}
