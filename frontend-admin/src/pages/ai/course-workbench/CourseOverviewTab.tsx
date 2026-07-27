// CourseOverviewTab — 课程工作台「概览」tab。这门课的 AI 状态全景:一屏看清
// 总结/作业/术语/任务的状态,并给出"下一步该做什么"的行动入口。
//
// 设计参考专业后台(Stripe/Vercel/Dify)的 Overview = "异常/待办入口",不是数据墙。
// 这里聚合四个维度的状态卡片,每张卡片本身就是跳转到对应 tab 的入口,让概览
// 成为"我先看哪里需要处理"的枢纽,而不是"看一堆数字"的报表。
import { useQuery } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { FileText, BookOpen, AlertTriangle, ClipboardList, ArrowRight } from 'lucide-react';
import { api } from '../../../lib/api';
import { pollWhileGenerating } from '../../../lib/query';
import { Spinner } from '../../../components/ui';

export function CourseOverviewTab({ courseId }: { courseId: number }) {
  const [, setParams] = useSearchParams();
  const goTab = (tab: string) => {
    setParams({ tab }, { replace: true });
    // 滚到顶部,让用户看到切到的 tab 内容(概览在顶部,切走后从顶开始看)。
    window.scrollTo({ top: 0, behavior: 'smooth' });
  };

  // 课程总结状态(三态:ready/generating/'')。generating 时轮询。
  const summaryQ = useQuery({
    queryKey: ['course-summary', courseId],
    queryFn: () => api.getCourseSummary(courseId),
    refetchInterval: pollWhileGenerating(),
    refetchIntervalInBackground: false,
  });
  // 哪些课时已有总结——给"内容"卡片显示进度。
  const summaryStatusQ = useQuery({
    queryKey: ['episode-summary-status', courseId],
    queryFn: () => api.listEpisodeSummaryStatus(courseId),
  });
  // 课时列表——统计总数 + 有字幕数。
  const episodesQ = useQuery({
    queryKey: ['course-episodes', courseId],
    queryFn: () => api.listEpisodes(courseId),
  });
  // 作业列表——统计已生成数。
  const homeworksQ = useQuery({
    queryKey: ['homeworks', courseId],
    queryFn: () => api.homeworkList(courseId),
  });
  // 待审术语数。
  const glossaryQ = useQuery({
    queryKey: ['glossary-candidates', courseId, 'pending'],
    queryFn: () => api.listGlossaryCandidates(courseId, 'pending'),
  });
  // 本课相关的失败任务数。queryKey 用全局 ['ai-jobs', null, 'failed'](和 AIWorkflow
  // 的 failed 视图一致),复用缓存——不在 key 里塞 courseId,因为 queryFn 拉的是全量
  // failed jobs(client 端按 course_id 过滤)。否则每门课概览都重复发同一个全量请求。
  const jobsQ = useQuery({
    queryKey: ['ai-jobs', null, 'failed'],
    queryFn: () => api.listAiJobs(undefined, 'failed'),
  });

  if (summaryQ.isLoading || episodesQ.isLoading) {
    return (
      <div className="flex items-center justify-center py-16">
        <Spinner size={24} />
      </div>
    );
  }

  const episodes = episodesQ.data ?? [];
  const episodesWithSubtitle = episodes.filter((ep) => (ep.subtitle_count ?? 0) > 0);
  const episodesWithSummary = summaryStatusQ.data ?? [];
  const summaryStatus = summaryQ.data?.status ?? '';
  const summaryStale =
    summaryStatus === 'ready' &&
    (summaryQ.data?.current_episode_count ?? 0) > (summaryQ.data?.episode_count_at_gen ?? 0);
  const activeHomeworks = (homeworksQ.data ?? []).filter((h) => h.status === 'active');
  const pendingGlossary = glossaryQ.data ?? [];
  // 失败任务:client 端按 course 过滤(jobs 接口按 status 返回全量,这里筛本课)。
  // 粗略匹配:job.course_title 不可靠,用 course_id 字段(后端 enrich 出来的)。
  const failedJobs = (jobsQ.data?.jobs ?? []).filter((j) => j.course_id === courseId);

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      {/* 内容卡片:总结 + 作业 的生成进度 */}
      <button
        onClick={() => goTab('content')}
        className="group flex items-start justify-between gap-3 rounded-lg border border-border bg-card p-4 text-left transition-colors hover:border-primary/40"
      >
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium text-txt">
            <FileText size={15} /> 内容产出
          </div>
          <div className="mt-1.5 space-y-0.5 text-xs text-muted">
            <div>
              课程总结:
              {summaryStatus === 'ready'
                ? `已生成${summaryStale ? '(已陈旧,建议刷新)' : ''}`
                : summaryStatus === 'generating'
                  ? '生成中…'
                  : '未生成'}
            </div>
            <div>课时总结:{episodesWithSummary.length}/{episodes.length} 节</div>
            <div>课后作业:{activeHomeworks.length} 份</div>
            <div className="text-muted/70">{episodesWithSubtitle.length}/{episodes.length} 节有字幕</div>
          </div>
        </div>
        <ArrowRight size={16} className="mt-1 shrink-0 text-muted opacity-0 transition-opacity group-hover:opacity-100" />
      </button>

      {/* Prompt 卡片:这门课是否覆盖了学科默认 */}
      <button
        onClick={() => goTab('prompt')}
        className="group flex items-start justify-between gap-3 rounded-lg border border-border bg-card p-4 text-left transition-colors hover:border-primary/40"
      >
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium text-txt">
            <BookOpen size={15} /> Prompt 配置
          </div>
          <p className="mt-1.5 text-xs text-muted">
            编辑这门课的 AI 提示词覆盖(总结/出题/建议/术语),并即时预览拼装结果。
          </p>
        </div>
        <ArrowRight size={16} className="mt-1 shrink-0 text-muted opacity-0 transition-opacity group-hover:opacity-100" />
      </button>

      {/* 术语卡片:待审数量是关键行动信号 */}
      <button
        onClick={() => goTab('glossary')}
        className="group flex items-start justify-between gap-3 rounded-lg border border-border bg-card p-4 text-left transition-colors hover:border-primary/40"
      >
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium text-txt">
            <ClipboardList size={15} /> 术语候选
          </div>
          <p className="mt-1.5 text-xs text-muted">
            {pendingGlossary.length > 0
              ? `${pendingGlossary.length} 条待审核 —— 字幕润色挖出的术语纠错建议,审核后可一键应用到本课字幕。`
              : '暂无待审术语。字幕润色产出的术语建议会出现在这里。'}
          </p>
        </div>
        <ArrowRight size={16} className="mt-1 shrink-0 text-muted opacity-0 transition-opacity group-hover:opacity-100" />
      </button>

      {/* 质量卡片:失败任务 + 错题观测 */}
      <button
        onClick={() => goTab('quality')}
        className="group flex items-start justify-between gap-3 rounded-lg border border-border bg-card p-4 text-left transition-colors hover:border-primary/40"
      >
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium text-txt">
            <AlertTriangle size={15} /> 质量与异常
          </div>
          <p className="mt-1.5 text-xs text-muted">
            {failedJobs.length > 0
              ? `${failedJobs.length} 个失败任务需要处理。`
              : '暂无失败任务。'}
            {' '}错题/考试观测帮你发现题面问题或难点。
          </p>
        </div>
        <ArrowRight size={16} className="mt-1 shrink-0 text-muted opacity-0 transition-opacity group-hover:opacity-100" />
      </button>
    </div>
  );
}
