import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { StatCard, LoadingState } from '../components/ui';
import { formatDurationShort } from '../lib/format';
import { subjectMeta } from '../lib/types';

export function Dashboard() {
  const statsQ = useQuery({ queryKey: ['dashboard-stats'], queryFn: api.dashboardStats });
  const stats = statsQ.data;

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

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            {/* Subject distribution */}
            <div className="card">
              <h2 className="mb-4 text-lg font-bold text-txt">各科目课时分布</h2>
              {stats.subject_distribution.length === 0 ? (
                <p className="text-sm text-muted">暂无数据</p>
              ) : (
                <div className="space-y-2">
                  {stats.subject_distribution.map((s) => {
                    const meta = subjectMeta(s.subject);
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
        </>
      ) : null}
    </div>
  );
}
