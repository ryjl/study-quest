// AIOverview — AI 运营的概览枢纽页。参考 Stripe/Vercel/Dify 的 Overview =
// "异常/待办入口",不是 BI 数据墙。核心职责:让 admin 一眼看到"今天该处理什么"。
//
// 三个区:
//   1. 待办(行动信号):失败任务、处理中任务、待审术语 —— 每个是可点击的跳转入口
//   2. 任务监控精华:队列状态 stat + 最近运行(精简版,完整页在任务队列)
//   3. 快捷入口:课程/学生工作台
//
// 不堆图表。admin 打开这里要能立刻判断"有没有需要我介入的",而不是看一堆数字。
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { AlertTriangle, Loader2, ClipboardList, Bot, Users, ArrowRight, ListChecks } from 'lucide-react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { useToast } from '../../lib/toast';
import { PageHeader } from '../../components/PageHeader';
import { HomeworkPromptSection } from '../ai-console/HomeworkPromptSection';
import type { AiJob } from '../../lib/types';
import { jobTypeLabel } from '../../lib/jobType';

export function AIOverview() {
  const navigate = useNavigate();

  // 失败任务 + 队列状态。listAiJobs 返回 stats + jobs。
  // 有处理中/排队中任务时轻轮询(5s),让"处理中"数字实时更新;空闲时停止省请求。
  // 复用 AIWorkflow 的 pollWhen 模式(查 stats 判断是否在跑)。
  const jobsQ = useQuery({
    queryKey: ['ai-jobs', null, 'all'],
    queryFn: () => api.listAiJobs(undefined, undefined),
    refetchInterval: (query) => {
      const s = query.state.data?.stats;
      // 有排队或处理中时轮询,否则停。
      return s && (s.queued > 0 || s.processing > 0) ? 5000 : false;
    },
    refetchIntervalInBackground: false,
  });
  // 失败任务按课程聚合——让 admin 知道"哪门课出问题最多",直接跳那门课的工作台。
  // 失败任务按课程分组,每门课带上具体 jobs(含 error/episode)— 让 admin 展开能直接
  // 看到"什么类型的任务、哪节课时、什么错误",并就地 acknowledge/重试,不用跳走。
  const failedByCourse = useMemo(() => {
    const map = new Map<number, { courseId: number; courseTitle: string; jobs: AiJob[] }>();
    for (const j of jobsQ.data?.jobs ?? []) {
      if (j.status !== 'failed') continue;
      const key = j.course_id;
      const entry = map.get(key);
      if (entry) entry.jobs.push(j);
      else map.set(key, { courseId: key, courseTitle: j.course_title ?? `课程 ${key}`, jobs: [j] });
    }
    return Array.from(map.values()).sort((a, b) => b.jobs.length - a.jobs.length);
  }, [jobsQ.data]);

  const qc = useQueryClient();
  const toast = useToast();
  // Acknowledge:failed→skipped。用于"无字幕"这类 admin 无法修复的失败。成功后刷新 jobs
  // 列表,失败任务从概览消失(落到历史 skipped)。
  const ackMut = useMutation({
    mutationFn: (jobId: number) => api.acknowledgeAiJob(jobId),
    onSuccess: () => {
      toast.success('已忽略该失败任务');
      qc.invalidateQueries({ queryKey: ['ai-jobs'] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '操作失败'),
  });
  // 重试:failed→queued。admin 修复了根因(如补了字幕、修了 provider)后重跑。
  const retryMut = useMutation({
    mutationFn: (jobId: number) => api.retryAiJob(jobId),
    onSuccess: () => {
      toast.success('已重新排队,worker 将重试');
      qc.invalidateQueries({ queryKey: ['ai-jobs'] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '操作失败'),
  });

  const stats = jobsQ.data?.stats;
  const failedTotal = stats?.failed ?? 0;
  const queuedTotal = stats?.queued ?? 0;
  const processingTotal = stats?.processing ?? 0;
  // jobs 接口返回最近 100 条(后端硬限),client 端聚合的失败数。当它 < stats.failed
  // (准确总数)时,说明有更早的失败任务被挤出窗口,明细不完整——诚实提示。
  const failedShown = (jobsQ.data?.jobs ?? []).filter((j) => j.status === 'failed').length;
  const failedTruncated = failedShown < failedTotal;

  return (
    <div>
      <PageHeader
        title="AI 概览"
        breadcrumb={[{ label: 'AI 运营' }]}
        description="AI 运营的起点 —— 先看有没有需要处理的,再进入具体工作台。"
      />

      <div className="space-y-6">
        {/* 待办区:只有"需要 admin 介入"的信号才显示,避免噪音。
            绿色平静态:无失败、无处理中时显示"一切正常",让 admin 放心离开。 */}
        <section>
          <h2 className="mb-3 text-sm font-medium text-txt">待办</h2>
          {failedTotal === 0 && processingTotal === 0 ? (
            <div className="flex items-center gap-2 rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4 text-sm text-emerald-700 dark:text-emerald-400">
              <AlertTriangle size={16} />
              一切正常 —— 没有失败任务,也没有正在处理的任务。
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {/* 失败任务卡片:最高优先级待办。按课程聚合,点课程展开看具体失败任务 +
                  就地 acknowledge/重试。展开是因为"无字幕"这类失败 admin 看一眼就知道
                  能不能修——不需要跳到别的页。 */}
              {failedTotal > 0 && (
                <FailedCoursesCard
                  failedTotal={failedTotal}
                  failedByCourse={failedByCourse}
                  failedTruncated={failedTruncated}
                  onAcknowledge={(id) => ackMut.mutate(id)}
                  onRetry={(id) => retryMut.mutate(id)}
                  onGotoCourse={(cid) => navigate(`/admin/ai/course/${cid}?tab=content`)}
                  onGotoJobs={() => navigate('/admin/ai/jobs')}
                  pendingAckId={ackMut.isPending}
                  pendingRetryId={retryMut.isPending}
                />
              )}

              {/* 处理中卡片:不是错误,但 admin 可能想盯着。点进去看任务队列。 */}
              {processingTotal > 0 && (
                <button
                  onClick={() => navigate('/admin/ai/jobs')}
                  className="flex items-center gap-3 rounded-lg border border-amber-500/40 bg-amber-500/5 p-4 text-left transition-colors hover:border-amber-500/60"
                >
                  <Loader2 size={16} className="animate-spin text-amber-600" />
                  <div>
                    <div className="text-sm font-medium text-amber-700 dark:text-amber-400">{processingTotal} 个处理中</div>
                    <div className="text-[11px] text-muted">{queuedTotal} 个排队中</div>
                  </div>
                </button>
              )}
            </div>
          )}
        </section>

        {/* 快捷入口:三个工作台 + 任务队列。卡片式入口(参考 Dify Studio 首页)。 */}
        <section>
          <h2 className="mb-3 text-sm font-medium text-txt">工作台</h2>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {[
              { label: '课程工作台', desc: '按课程管理 AI 内容、Prompt、术语与质量', icon: <Bot size={18} />, to: '/admin/ai/courses' },
              { label: '学生工作台', desc: '按学生查看题库、错题,重新生成报告与建议', icon: <Users size={18} />, to: '/admin/ai/students' },
              { label: '任务队列', desc: '观测 AI 任务队列与 agent 决策痕迹', icon: <ListChecks size={18} />, to: '/admin/ai/jobs' },
              { label: '术语审核', desc: '字幕润色产出的术语候选,审核后可一键应用', icon: <ClipboardList size={18} />, to: '/admin/ai/courses' },
            ].map((c) => (
              <button
                key={c.label}
                onClick={() => navigate(c.to)}
                className="group flex items-start justify-between gap-2 rounded-lg border border-border bg-card p-4 text-left transition-colors hover:border-primary/40"
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-2 text-sm font-medium text-txt">
                    <span className="text-primary">{c.icon}</span>
                    {c.label}
                  </div>
                  <p className="mt-1 text-xs text-muted">{c.desc}</p>
                </div>
                <ArrowRight size={15} className="mt-1 shrink-0 text-muted opacity-0 transition-opacity group-hover:opacity-100" />
              </button>
            ))}
          </div>
        </section>

        {/* 学科级作业生成 Prompt。学习 Prompt 在「分类与标签」的学科编辑里配(学科级),
            作业 Prompt 是独立的完整 system prompt(同学科所有课共享),放这里集中管理。
            带自带选学科下拉。原 AI 控制台 Prompt tab 删除后的归处。 */}
        <section>
          <h2 className="mb-1 text-sm font-medium text-txt">学科作业 Prompt</h2>
          <p className="mb-3 text-[11px] text-muted">
            每个学科的完整作业生成 prompt(同学科所有课共享)。学习类的 5 字段 hint 在「分类与标签」的学科编辑里配。
          </p>
          <HomeworkPromptSection />
        </section>
      </div>
    </div>
  );
}

// FailedCoursesCard — 失败任务卡片。每门课一行,点开展开本课的失败任务明细:
// 每个任务显示 类型·课时·错误原因 + [重试][忽略] 按钮。"忽略"(acknowledge)用于
// "无字幕"这类 admin 无法修复的失败——retry 无意义,但留在 failed 列表会淹没新失败。
// 展开用 useState(哪门课展开了),不跳转,就地处理。
function FailedCoursesCard({
  failedTotal,
  failedByCourse,
  failedTruncated,
  onAcknowledge,
  onRetry,
  onGotoCourse,
  onGotoJobs,
  pendingAckId,
  pendingRetryId,
}: {
  failedTotal: number;
  failedByCourse: { courseId: number; courseTitle: string; jobs: AiJob[] }[];
  failedTruncated: boolean;
  onAcknowledge: (jobId: number) => void;
  onRetry: (jobId: number) => void;
  onGotoCourse: (courseId: number) => void;
  onGotoJobs: () => void;
  pendingAckId: boolean;
  pendingRetryId: boolean;
}) {
  const [expanded, setExpanded] = useState<number | null>(null);
  return (
    <div className="rounded-lg border border-red-500/40 bg-red-500/5 p-4">
      <div className="flex items-center gap-2 text-sm font-medium text-bad">
        <AlertTriangle size={15} /> {failedTotal} 个失败任务
      </div>
      <div className="mt-2 space-y-1">
        {failedByCourse.map((f) => {
          const isOpen = expanded === f.courseId;
          return (
            <div key={f.courseId} className="rounded border border-border/40 bg-card/50">
              <div className="flex items-center justify-between">
                <button
                  onClick={() => setExpanded(isOpen ? null : f.courseId)}
                  className="flex min-w-0 flex-1 items-center gap-1.5 rounded px-2 py-1.5 text-left text-xs text-txt transition-colors hover:bg-card-2"
                >
                  <span className={`text-muted transition-transform ${isOpen ? 'rotate-90' : ''}`}>▸</span>
                  <span className="truncate">{f.courseTitle}</span>
                  <span className="ml-1 shrink-0 rounded-full bg-bad/15 px-1.5 py-0.5 text-[10px] text-bad">{f.jobs.length}</span>
                </button>
                <button
                  onClick={() => onGotoCourse(f.courseId)}
                  className="shrink-0 px-2 py-1.5 text-[10px] text-muted hover:text-primary"
                  title="去该课程工作台"
                >
                  工作台 →
                </button>
              </div>
              {isOpen && (
                <ul className="space-y-1.5 border-t border-border/40 p-2">
                  {f.jobs.map((j) => (
                    <li key={j.id} className="rounded bg-card-2/60 px-2 py-1.5">
                      <div className="flex items-center justify-between gap-2">
                        <div className="min-w-0 flex-1">
                          <div className="text-xs text-txt">
                            <span className="font-medium">{jobTypeLabel(j.job_type)}</span>
                            <span className="ml-1.5 text-muted">· {j.episode_title || `课时 #${j.episode_id}`}</span>
                          </div>
                          {/* 错误原因:让 admin 一眼判断"能不能修"。"no subtitle" 这种一眼
                              就知道是无字幕(admin 改不了),其它错误可能要查。 */}
                          <div className="mt-0.5 truncate font-mono text-[10px] text-bad" title={j.error}>
                            {j.error || '(无错误详情)'}
                          </div>
                        </div>
                        <div className="flex shrink-0 items-center gap-1">
                          <button
                            onClick={() => onRetry(j.id)}
                            disabled={pendingRetryId}
                            className="rounded border border-border px-1.5 py-0.5 text-[10px] text-muted transition-colors hover:border-primary hover:text-primary disabled:opacity-50"
                            title="重新排队重试(修复根因后用,如补了字幕)"
                          >
                            重试
                          </button>
                          <button
                            onClick={() => onAcknowledge(j.id)}
                            disabled={pendingAckId}
                            className="rounded border border-border px-1.5 py-0.5 text-[10px] text-muted transition-colors hover:border-primary hover:text-primary disabled:opacity-50"
                            title="忽略此失败(failed→skipped)。用于无字幕这类无法修复的失败,清出失败列表。"
                          >
                            忽略
                          </button>
                        </div>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          );
        })}
        {/* 截断提示:后端 jobs 接口只返回最近 100 条,失败任务超过这个数时分课程明细不全。 */}
        {failedTruncated && (
          <p className="px-1.5 text-[10px] text-muted">(仅显示最近 100 条任务中的失败,完整列表见任务队列)</p>
        )}
        <button onClick={onGotoJobs} className="mt-1 px-1.5 text-[11px] text-primary hover:underline">
          去任务队列查看全部 →
        </button>
      </div>
    </div>
  );
}
