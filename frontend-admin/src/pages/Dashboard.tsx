import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { StatCard, LoadingState } from '../components/ui';
import { formatDurationShort } from '../lib/format';
import { subjectMeta, type SubjectMeta } from '../lib/types';
import { useSubjects } from '../lib/useSubjects';

export function Dashboard() {
  const statsQ = useQuery({ queryKey: ['dashboard-stats'], queryFn: api.dashboardStats });
  const stats = statsQ.data;
  // Load the subject catalog so the subject-distribution chart can resolve
  // keys → Chinese labels + colors. Layout already warms the shared module
  // cache, but that cache is NOT reactive: if the dashboard response arrives
  // before the subjects response, the chart renders from an empty cache
  // (showing raw keys like "english" + the grey fallback color) and never
  // re-renders when the cache fills. Reading the query here makes the chart a
  // subscriber, so it re-renders the moment subjects land.
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];

  return (
    <div>
      <h1 className="mb-6 text-2xl font-bold text-txt">控制台概览</h1>

      {statsQ.isLoading ? (
        <LoadingState />
      ) : statsQ.error ? (
        <div className="card text-bad">加载统计失败: {(statsQ.error as Error).message}</div>
      ) : stats ? (
        <>
          <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
            <StatCard label="总用户数" value={stats.user_count} icon="👥" color="#a78bfa" />
            <StatCard label="课程数量" value={stats.course_count} icon="📚" color="#60a5fa" />
            <StatCard label="课时总数" value={stats.episode_count} icon="🎬" color="#34d399" />
            <StatCard label="视频总时长" value={formatDurationShort(stats.total_duration_seconds)} icon="⏱" color="#fbbf24" />
            <StatCard
              label="待探测时长"
              value={stats.pending_probe_count}
              hint={stats.pending_probe_count > 0 ? '可在课程页一键探测' : '全部已探测'}
              icon="📡"
              color="#f43f5e"
            />
          </div>

          {/* Learning-activity row */}
          <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <StatCard label="今日活跃用户" value={stats.active_users_today ?? 0} icon="🟢" color="#34d399" />
            <StatCard label="累计学习时长" value={formatDurationShort(stats.total_watch_seconds ?? 0)} icon="⏱" color="#fbbf24" />
            <StatCard label="累计完课数" value={stats.completed_episodes ?? 0} icon="✅" color="#60a5fa" />
            <StatCard label="勋章解锁总人次" value={stats.unlocked_badge_count ?? 0} icon="🏅" color="#a78bfa" />
          </div>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            {/* Subject distribution */}
            <div className="card">
              <h2 className="mb-4 text-lg font-bold text-txt">各科目课时分布</h2>
              {stats.subject_distribution.length === 0 ? (
                <p className="text-sm text-muted">暂无数据</p>
              ) : (
                <div className="space-y-2">
                  {stats.subject_distribution.map((s) => {
                    // Resolve meta from the reactive subjects list first (so
                    // the chart re-renders when subjects land), falling back to
                    // the module cache / default only if not found. This is what
                    // fixes the "shows 'english' + grey bar" bug: before, an
                    // empty non-reactive cache meant the fallback always won.
                    const found = subjects.find((x) => x.key === s.subject) as SubjectMeta | undefined;
                    const meta = found ?? subjectMeta(s.subject);
                    const max = Math.max(...stats.subject_distribution.map((x) => x.count), 1);
                    return (
                      <div key={s.subject}>
                        <div className="mb-1 flex justify-between text-sm">
                          <span className="text-txt">
                            {meta.emoji} {meta.label}
                          </span>
                          <span className="text-muted">{s.count} 课时</span>
                        </div>
                        <div className="h-2 overflow-hidden rounded-full bg-card-2">
                          <div className="h-full rounded-full transition-all" style={{ width: `${(s.count / max) * 100}%`, backgroundColor: meta.color }} />
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Recent episodes */}
            <div className="card">
              <h2 className="mb-4 text-lg font-bold text-txt">近 7 天新增课时</h2>
              {stats.recent_daily_episodes.length === 0 ? (
                <p className="text-sm text-muted">暂无数据</p>
              ) : (
                <div className="flex h-40 items-end gap-2">
                  {stats.recent_daily_episodes.map((d) => {
                    const max = Math.max(...stats.recent_daily_episodes.map((x) => x.count), 1);
                    return (
                      <div key={d.date} className="flex flex-1 flex-col items-center gap-1">
                        <div className="text-xs text-muted">{d.count}</div>
                        <div className="w-full rounded-t-md bg-gradient-to-t from-primary to-primary-dark transition-all" style={{ height: `${(d.count / max) * 100}%`, minHeight: '4px' }} />
                        <div className="text-[10px] text-muted">{d.date.slice(5)}</div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          </div>

          {/* Learning-time trend + leaderboards */}
          <div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-3">
            {/* Learning-time trend (distinct from "new episodes per day") */}
            <div className="card">
              <h2 className="mb-4 text-lg font-bold text-txt">近 7 天学习时长</h2>
              {!stats.recent_daily_watch || stats.recent_daily_watch.length === 0 ? (
                <p className="text-sm text-muted">暂无数据</p>
              ) : (
                <div className="flex h-40 items-end gap-2">
                  {stats.recent_daily_watch.map((d) => {
                    const max = Math.max(...stats.recent_daily_watch!.map((x) => x.seconds), 1);
                    return (
                      <div key={d.date} className="flex flex-1 flex-col items-center gap-1">
                        <div className="text-[10px] text-muted" title={`${d.seconds} 秒`}>
                          {Math.round(d.seconds / 60)}m
                        </div>
                        <div className="w-full rounded-t-md bg-gradient-to-t from-warn to-amber-300 transition-all" style={{ height: `${(d.seconds / max) * 100}%`, minHeight: '4px' }} />
                        <div className="text-[10px] text-muted">{d.date.slice(5)}</div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Most active users */}
            <div className="card">
              <h2 className="mb-4 text-lg font-bold text-txt">最活跃用户 Top 5</h2>
              {!stats.top_users || stats.top_users.length === 0 ? (
                <p className="text-sm text-muted">暂无数据</p>
              ) : (
                <div className="space-y-2">
                  {stats.top_users.map((u, i) => (
                    <div key={u.id} className="flex items-center justify-between rounded-lg bg-card-2 px-3 py-1.5 text-sm">
                      <span className="flex items-center gap-2 text-txt">
                        <span className="text-xs text-muted">{i + 1}.</span>
                        {u.label || `用户 #${u.id}`}
                      </span>
                      <span className="text-warn">{formatDurationShort(u.value)}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Popular courses */}
            <div className="card">
              <h2 className="mb-4 text-lg font-bold text-txt">热门课程 Top 5</h2>
              {!stats.top_courses || stats.top_courses.length === 0 ? (
                <p className="text-sm text-muted">暂无数据</p>
              ) : (
                <div className="space-y-2">
                  {stats.top_courses.map((c, i) => (
                    <div key={c.id} className="flex items-center justify-between rounded-lg bg-card-2 px-3 py-1.5 text-sm">
                      <span className="flex min-w-0 items-center gap-2 text-txt">
                        <span className="text-xs text-muted">{i + 1}.</span>
                        <span className="truncate">{c.label || `课程 #${c.id}`}</span>
                      </span>
                      <span className="flex-shrink-0 text-primary">{c.value} 完课</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        </>
      ) : null}
    </div>
  );
}
