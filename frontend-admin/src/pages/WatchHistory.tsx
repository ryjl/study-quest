import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { User } from '../lib/types';
import { LoadingState, EmptyState } from '../components/ui';
import { formatDurationShort, formatDuration } from '../lib/format';
import { PageHeader } from '../components/PageHeader';

// localStorage key for the last-selected user, so re-entry doesn't reset.
const LS_USER_KEY = 'watch-history-user';

// Heatmap color thresholds (seconds). 0 → grey; tier picks the first threshold
// the value exceeds. Tuned for a family scale: 15min / 45min / 90min tiers.
const HEAT_TIERS = [15 * 60, 45 * 60, 90 * 60];
const HEAT_COLORS = ['bg-card-2', 'bg-emerald-900/60', 'bg-emerald-700/80', 'bg-emerald-500'];
// index 0 = no learning that day; tier N = at least HEAT_TIERS[N-1].

export function WatchHistory() {
  const usersQ = useQuery({ queryKey: ['users'], queryFn: api.listUsers });
  const users: User[] = usersQ.data ?? [];

  // Selected user — persisted across visits. Default to first user once loaded.
  const [userId, setUserId] = useState<number | null>(() => {
    const saved = localStorage.getItem(LS_USER_KEY);
    return saved ? Number(saved) : null;
  });
  if (userId === null && users.length > 0) {
    setUserId(users[0].id);
  }
  const pickUser = (id: number) => {
    setUserId(id);
    localStorage.setItem(LS_USER_KEY, String(id));
  };

  // Month navigation — defaults to the current month. `monthCursor` is any
  // instant within the target month; we derive from/to from it.
  const [monthCursor, setMonthCursor] = useState(() => new Date());
  const { from, to, label } = monthBounds(monthCursor);

  // Selected day within the month (YYYY-MM-DD). Defaults to today if the
  // cursor is on the current month, else null (no day preselected).
  const todayStr = toDayStr(new Date());
  const [selectedDay, setSelectedDay] = useState<string | null>(
    fromMonthString(monthCursor) === fromMonthString(new Date()) ? todayStr : null,
  );

  // Heatmap data: per-day totals for the visible month.
  const historyQ = useQuery({
    queryKey: ['watch-history', userId, from, to],
    queryFn: () => api.userWatchHistory(userId!, from, to),
    enabled: userId !== null,
  });
  // Map date → seconds for quick cell lookup.
  const byDate = new Map<string, number>();
  for (const d of historyQ.data ?? []) byDate.set(d.date, d.seconds);

  // Detail timeline for the selected day.
  const eventsQ = useQuery({
    queryKey: ['watch-events', userId, selectedDay],
    queryFn: () => api.userWatchEvents(userId!, selectedDay!),
    enabled: userId !== null && selectedDay !== null,
  });

  const shiftMonth = (delta: number) => {
    const c = new Date(monthCursor);
    c.setDate(1);
    c.setMonth(c.getMonth() + delta);
    setMonthCursor(c);
    // Reset selection: today if landing on current month, else none.
    setSelectedDay(fromMonthString(c) === fromMonthString(new Date()) ? todayStr : null);
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="观看历史"
        breadcrumb={[{ label: '用户与授权' }]}
        description="查看用户观看记录与进度。"
        actions={
          <select
            className="input"
            value={userId ?? ''}
            onChange={(e) => pickUser(Number(e.target.value))}
            disabled={users.length === 0}
          >
            {users.length === 0 && <option value="">（暂无用户）</option>}
            {users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.nickname}（{roleLabel(u.role)}）
              </option>
            ))}
          </select>
        }
      />

      {userId !== null && (
        <>
          <section className="rounded-xl border border-border bg-card p-4">
            <div className="mb-3 flex items-center justify-between">
              <h2 className="text-lg font-medium text-txt">{label}</h2>
              <div className="flex gap-2">
                <button className="btn-ghost btn-sm" onClick={() => shiftMonth(-1)}>‹ 上月</button>
                <button className="btn-ghost btn-sm" onClick={() => shiftMonth(1)}>下月 ›</button>
              </div>
            </div>

            {historyQ.isLoading ? (
              <LoadingState label="加载中..." />
            ) : historyQ.error ? (
              <div className="text-sm text-danger">加载失败：{(historyQ.error as Error).message}</div>
            ) : (
              <Heatmap
                year={monthCursor.getFullYear()}
                month={monthCursor.getMonth()}
                byDate={byDate}
                selectedDay={selectedDay}
                onSelect={setSelectedDay}
              />
            )}

            {/* Legend */}
            <div className="mt-3 flex items-center gap-2 text-[11px] text-muted">
              <span>少</span>
              {HEAT_COLORS.map((c, i) => (
                <span key={i} className={`inline-block h-3 w-3 rounded-sm ${c}`} />
              ))}
              <span>多</span>
            </div>
          </section>

          <section className="rounded-xl border border-border bg-card p-4">
            <h2 className="mb-3 text-lg font-medium text-txt">
              {selectedDay ? `${selectedDay} 当日明细` : '点击上方某天查看当日明细'}
            </h2>
            {selectedDay === null ? (
              <EmptyState title="未选择日期" hint="在月历上点击某一天查看那天的观看记录。" />
            ) : eventsQ.isLoading ? (
              <LoadingState label="加载明细..." />
            ) : eventsQ.error ? (
              <div className="text-sm text-danger">加载明细失败：{(eventsQ.error as Error).message}</div>
            ) : (eventsQ.data ?? []).length === 0 ? (
              <EmptyState title="这天没有观看记录" />
            ) : (
              <DayDetail events={eventsQ.data!} />
            )}
          </section>
        </>
      )}
    </div>
  );
}

// Heatmap renders a month grid (Sunday-first) with per-day cells colored by
// watch duration. Leading/trailing blanks fill out the first week.
function Heatmap({
  year,
  month,
  byDate,
  selectedDay,
  onSelect,
}: {
  year: number;
  month: number; // 0-indexed (Date.getMonth convention)
  byDate: Map<string, number>;
  selectedDay: string | null;
  onSelect: (day: string) => void;
}) {
  const firstOfMonth = new Date(year, month, 1);
  // Sunday=0 → leading blanks = firstOfDay. (Matches the weekday header order.)
  const leadingBlanks = firstOfMonth.getDay();
  const daysInMonth = new Date(year, month + 1, 0).getDate();
  const today = toDayStr(new Date());

  const cells: { day: string | null; dom: number | null }[] = [];
  for (let i = 0; i < leadingBlanks; i++) cells.push({ day: null, dom: null });
  for (let d = 1; d <= daysInMonth; d++) {
    const ds = `${year}-${String(month + 1).padStart(2, '0')}-${String(d).padStart(2, '0')}`;
    cells.push({ day: ds, dom: d });
  }

  return (
    <div>
      <div className="mb-1 grid grid-cols-7 gap-1 text-center text-[11px] text-muted">
        {['日', '一', '二', '三', '四', '五', '六'].map((w) => (
          <div key={w}>{w}</div>
        ))}
      </div>
      <div className="grid grid-cols-7 gap-1">
        {cells.map((c, i) => {
          if (c.day === null) return <div key={`b-${i}`} />;
          const secs = byDate.get(c.day) ?? 0;
          const tier = heatTier(secs);
          const isSelected = c.day === selectedDay;
          const isToday = c.day === today;
          return (
            <button
              key={c.day}
              onClick={() => onSelect(c.day!)}
              title={`${c.day} · ${formatDurationShort(secs)}`}
              className={`relative aspect-square rounded-md p-1 text-left transition ${
                HEAT_COLORS[tier]
              } ${isSelected ? 'ring-2 ring-emerald-300' : isToday ? 'ring-1 ring-emerald-400/50' : ''}`}
            >
              <span className="text-[10px] font-medium text-txt/70">{c.dom}</span>
              {secs > 0 && (
                <span className="absolute bottom-1 right-1 text-[9px] text-txt/60">
                  {formatDurationShort(secs)}
                </span>
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
}

// DayDetail renders the selected day's event timeline + a totals footer that
// shows both the wall-clock span and the effective watch time.
function DayDetail({ events }: { events: import('../lib/types').WatchEventDTO[] }) {
  // Totals: sum of durations, and the wall-clock span from earliest start to
  // latest end (which includes any inter-event pauses inside the day).
  let totalDuration = 0;
  let earliest: number | null = null;
  let latest: number | null = null;
  for (const e of events) {
    totalDuration += e.duration_seconds;
    const s = new Date(e.started_at).getTime();
    const en = new Date(e.ended_at).getTime();
    if (earliest === null || s < earliest) earliest = s;
    if (latest === null || en > latest) latest = en;
  }
  const spanSec = earliest !== null && latest !== null ? Math.round((latest - earliest) / 1000) : 0;

  return (
    <div className="space-y-2">
      {events.map((e) => {
        const isEnt = e.content_type === 'entertainment';
        return (
          <div
            key={e.id}
            className="flex items-center justify-between gap-3 rounded-lg border border-border bg-card-2 px-3 py-2"
          >
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className={`text-[10px] px-1.5 py-0.5 rounded ${isEnt ? 'bg-amber-600/30 text-amber-300' : 'bg-emerald-600/30 text-emerald-300'}`}>
                  {isEnt ? '娱乐' : '学习'}
                </span>
                <span className="truncate text-sm text-txt">
                  {e.course_title || `课程#${e.course_id}`} · {e.episode_title || `课时#${e.episode_id}`}
                </span>
              </div>
              <div className="mt-0.5 text-[11px] text-muted">
                {hm(e.started_at)} — {hm(e.ended_at)}
                {spanForEvent(e) > e.duration_seconds && (
                  <span className="ml-2 text-amber-400/80">
                    （含 {formatDuration(spanForEvent(e) - e.duration_seconds)} 未计入）
                  </span>
                )}
              </div>
            </div>
            <div className="shrink-0 text-right">
              <div className="text-sm font-medium text-emerald-300">
                {formatDurationShort(e.duration_seconds)}
              </div>
            </div>
          </div>
        );
      })}

      <div className="mt-2 flex items-center justify-between border-t border-border pt-2 text-sm">
        <span className="text-muted">当日合计</span>
        <span className="text-txt">
          跨 <span className="text-muted">{formatDuration(spanSec)}</span> · 学习{' '}
          <span className="font-medium text-emerald-300">{formatDuration(totalDuration)}</span>
        </span>
      </div>
    </div>
  );
}

// spanForEvent returns the wall-clock seconds of one event (started→ended),
// which includes any pauses folded in by the merge window.
function spanForEvent(e: import('../lib/types').WatchEventDTO): number {
  return Math.round((new Date(e.ended_at).getTime() - new Date(e.started_at).getTime()) / 1000);
}

// ---- small date helpers ----

function monthBounds(cursor: Date): { from: string; to: string; label: string } {
  const y = cursor.getFullYear();
  const m = cursor.getMonth();
  const from = new Date(y, m, 1);
  const to = new Date(y, m + 1, 1); // exclusive
  return {
    from: toDayStr(from),
    to: toDayStr(to),
    label: `${y}年${m + 1}月`,
  };
}

function fromMonthString(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
}

// toDayStr formats a Date as local YYYY-MM-DD (the admin browser's zone). The
// backend re-buckets into the business zone (Asia/Shanghai) via appclock, so a
// browser in any zone sends a calendar date that the server interprets the same
// way it interprets its own day boundaries.
function toDayStr(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

// hm formats an ISO timestamp as HH:MM (local).
function hm(iso: string): string {
  const d = new Date(iso);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

function roleLabel(role: string): string {
  switch (role) {
    case 'student': return '学生';
    case 'teen': return '青少年';
    case 'parent': return '家长';
    case 'admin': return '管理员';
    default: return role;
  }
}

// heatTier maps a seconds value to a 0..3 index into HEAT_COLORS.
function heatTier(secs: number): number {
  if (secs <= 0) return 0;
  let tier = 1;
  for (let i = 0; i < HEAT_TIERS.length; i++) {
    if (secs >= HEAT_TIERS[i]) tier = i + 1;
  }
  // Cap at the highest color index.
  return Math.min(tier, HEAT_COLORS.length - 1);
}
