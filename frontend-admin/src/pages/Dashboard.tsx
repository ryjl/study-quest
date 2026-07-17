import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import {
  ActivityFeed,
  LoadingState,
  Section,
  StatCard,
  StatusCard,
  TodoItem,
  type ActivityItem,
} from '../components/ui';
import { PageHeader } from '../components/PageHeader';
import { formatDurationShort } from '../lib/format';
import { subjectMeta, type SubjectMeta } from '../lib/types';
import { useSubjects } from '../lib/useSubjects';

// Role key → Chinese label for the activity feed detail line.
function roleLabel(role: string): string {
  switch (role) {
    case 'student':
      return '学生';
    case 'teen':
      return '青少年';
    case 'parent':
      return '家长';
    case 'admin':
      return '管理员';
    default:
      return role;
  }
}

// AI job_type → Chinese label for the activity feed title.
const AI_JOB_TYPE_LABEL: Record<string, string> = {
  summarize: '总结',
  quiz: '出题',
  advice: '建议',
  course_summary: '课程总结',
  user_report: '用户报告',
  segment: '切片',
  slice: '切片',
};
function aiJobTypeLabel(jobType: string): string {
  return AI_JOB_TYPE_LABEL[jobType] ?? jobType;
}

export function Dashboard() {
  // Primary stats — its loading state gates the whole page (as before).
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

  // Supplementary queries for the todo + activity sections. Kept 1-min stale
  // so they don't poll aggressively — they're context, not the focus.
  const subtitleStatsQ = useQuery({
    queryKey: ['subtitle-job-stats'],
    queryFn: api.subtitleJobStats,
    staleTime: 60_000,
  });
  const aiJobsQ = useQuery({
    queryKey: ['ai-jobs', 'dashboard'],
    queryFn: () => api.listAiJobs(),
    staleTime: 60_000,
  });
  const usersQ = useQuery({
    queryKey: ['users', 'dashboard'],
    queryFn: api.listUsers,
    staleTime: 60_000,
  });
  const aiRunsQ = useQuery({
    queryKey: ['ai-runs', 'dashboard'],
    queryFn: () => api.listAiRuns(10),
    staleTime: 60_000,
  });

  // ---- Section 1: TODO / alerts ----
  const pendingProbe = stats?.pending_probe_count ?? 0;
  const subtitleStats = subtitleStatsQ.data;
  const subtitleFailed = subtitleStats?.failed ?? 0;
  const subtitlePending = (subtitleStats?.processing ?? 0) + (subtitleStats?.queued ?? 0);
  const aiStats = aiJobsQ.data?.stats;
  const aiFailed = aiStats?.failed ?? 0;
  const aiPending = (aiStats?.queued ?? 0) + (aiStats?.processing ?? 0);

  type Todo = { icon: string; label: string; count: number; hint?: string; to: string };
  const todos: Todo[] = [];
  if (pendingProbe > 0) {
    todos.push({
      icon: '📡',
      label: '待探测时长',
      count: pendingProbe,
      hint: '课时缺少视频时长',
      to: '/admin/courses',
    });
  }
  if (subtitleFailed > 0) {
    todos.push({
      icon: '字幕',
      label: '字幕任务失败',
      count: subtitleFailed,
      hint: '需要重试或排查',
      to: '/admin/subtitle-queue',
    });
  }
  if (subtitlePending > 0) {
    todos.push({
      icon: '🎬',
      label: '字幕队列待处理',
      count: subtitlePending,
      hint: '排队或处理中',
      to: '/admin/subtitle-queue',
    });
  }
  if (aiFailed > 0) {
    todos.push({
      icon: '🤖',
      label: 'AI 任务失败',
      count: aiFailed,
      hint: '需要重试或排查',
      to: '/admin/ai-workflow',
    });
  }
  if (aiPending > 0) {
    todos.push({
      icon: '⏳',
      label: 'AI 任务待处理',
      count: aiPending,
      hint: '排队或处理中',
      to: '/admin/ai-workflow',
    });
  }
  const hasTodos = todos.length > 0;
  const todosReady = stats != null && subtitleStatsQ.isSuccess && aiJobsQ.isSuccess;

  // ---- Section 3: activity feed (merged timeline) ----
  const activityItems: ActivityItem[] = [];

  // New users — top 5 by created_at desc.
  const users = usersQ.data ?? [];
  for (const u of [...users]
    .filter((u) => u.created_at)
    .sort((a, b) => new Date(b.created_at!).getTime() - new Date(a.created_at!).getTime())
    .slice(0, 5)) {
    activityItems.push({
      id: `user-${u.id}`,
      icon: '👥',
      title: `新用户「${u.nickname || `用户 #${u.id}`}」加入`,
      detail: roleLabel(u.role),
      time: u.created_at!,
    });
  }

  // AI runs — recent completions.
  const aiRuns = aiRunsQ.data ?? [];
  for (const r of aiRuns) {
    activityItems.push({
      id: `airun-${r.id}`,
      icon: '🤖',
      title: `AI ${aiJobTypeLabel(r.capability)} 任务完成`,
      detail: r.model_used || undefined,
      time: r.created_at,
    });
  }

  // Daily new episodes — top 3 recent days with count > 0.
  const dailyEpisodes = stats?.recent_daily_episodes ?? [];
  for (const d of [...dailyEpisodes]
    .filter((d) => d.count > 0)
    .sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : 0))
    .slice(0, 3)) {
    activityItems.push({
      id: `daily-ep-${d.date}`,
      icon: '🎬',
      title: `当日新增 ${d.count} 个课时`,
      detail: d.date,
      time: d.date,
    });
  }

  // Sort by time desc; items with invalid/missing time sink to the end. Cap 15.
  activityItems.sort((a, b) => {
    const ta = new Date(a.time).getTime();
    const tb = new Date(b.time).getTime();
    const aOk = !isNaN(ta);
    const bOk = !isNaN(tb);
    if (!aOk && !bOk) return 0;
    if (!aOk) return 1;
    if (!bOk) return -1;
    return tb - ta;
  });
  const feedItems = activityItems.slice(0, 15);

  return (
    <div>
      <PageHeader title="控制台" description="运营概览、待办事项与最近活动" />

      {statsQ.isLoading ? (
        <LoadingState />
      ) : statsQ.error ? (
        <div className="card text-bad">加载统计失败: {(statsQ.error as Error).message}</div>
      ) : stats ? (
        <div className="space-y-6">
          {/* ---- Section 1: 待办与异常 (always open, top priority) ---- */}
          <Section
            title="待办与异常"
            icon="🔔"
            collapsible={false}
            right={
              hasTodos ? <span className="text-xs text-muted">共 {todos.length} 项</span> : undefined
            }
          >
            {hasTodos ? (
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
                {todos.map((t, i) => (
                  <TodoItem
                    key={i}
                    icon={t.icon}
                    label={t.label}
                    count={t.count}
                    hint={t.hint}
                    to={t.to}
                  />
                ))}
              </div>
            ) : (
              // All-clear: only render the rewarding green banner once the
              // supplementary queries have resolved, so we don't flash "一切正常"
              // and then surface todos a moment later.
              <StatusCard
                tone="ok"
                icon="✅"
                title="一切正常"
                children={todosReady ? '没有待处理的任务' : '加载中...'}
              />
            )}
          </Section>

          {/* ---- Section 2: 数据概览 (existing stats + charts, demoted) ---- */}
          <Section title="数据概览" icon="📊" defaultOpen collapsible>
            <div className="space-y-6">
              {/* Core counts */}
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
                <StatCard label="总用户数" value={stats.user_count} icon="👥" color="#a78bfa" />
                <StatCard label="课程数量" value={stats.course_count} icon="📚" color="#60a5fa" />
                <StatCard label="课时总数" value={stats.episode_count} icon="🎬" color="#34d399" />
                <StatCard
                  label="视频总时长"
                  value={formatDurationShort(stats.total_duration_seconds)}
                  icon="⏱"
                  color="#fbbf24"
                />
                <StatCard
                  label="待探测时长"
                  value={stats.pending_probe_count}
                  hint={stats.pending_probe_count > 0 ? '可在课程页一键探测' : '全部已探测'}
                  icon="📡"
                  color="#f43f5e"
                />
              </div>

              {/* Learning-activity row */}
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
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
              <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
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
            </div>
          </Section>

          {/* ---- Section 3: 最近活动 (merged timeline feed) ---- */}
          <Section title="最近活动" icon="🕒" defaultOpen collapsible>
            <ActivityFeed items={feedItems} emptyHint="暂无最近活动" />
          </Section>
        </div>
      ) : null}
    </div>
  );
}
