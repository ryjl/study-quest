// CourseRegenColumn — the left column of RegenTab. Pick a course → trigger /
// delete the course summary (3-state: ready / generating / ''), and list every
// episode with per-episode summary regen / re-polish / delete actions.
//
// Subtitle gate: a course (or episode) with NO subtitles can't get a summary —
// the summary agent runs over the polished subtitle text, so the trigger is
// disabled and labelled 无字幕. The `episode-summary-status` bulk query (one
// call per course, not N+1) tells us which episodes already have summaries so
// the delete button only shows where it can act.

import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../../../lib/api';
import { useConfirm } from '../../../lib/toast';
import { pollWhileGenerating } from '../../../lib/query';
import { useTypedMutation } from '../../../lib/useTypedMutation';

export function CourseRegenColumn() {
  const confirm = useConfirm();
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const courses = coursesQ.data ?? [];
  const [courseId, setCourseId] = useState<number | null>(null);

  const episodesQ = useQuery({
    queryKey: ['course-episodes', courseId],
    queryFn: () => api.listEpisodes(courseId!),
    enabled: courseId != null,
  });
  const episodes = episodesQ.data ?? [];
  const selectedCourse = courses.find((c) => c.id === courseId);
  // 课程下是否有任何字幕——给课程总结按钮做 gate(无字幕则禁用)。
  const noSubtitleAtAll = episodes.length > 0 && episodes.every((ep) => (ep.subtitle_count ?? 0) === 0);

  // 课程总结状态(三态):ready / generating / ''(未生成)。
  // 生成中自动轮询;ready 时 gate 删除按钮 + 显示文本预览 + 陈旧提示。
  const summaryQ = useQuery({
    queryKey: ['course-summary', courseId],
    queryFn: () => api.getCourseSummary(courseId!),
    enabled: courseId != null,
    refetchInterval: pollWhileGenerating(),
    refetchIntervalInBackground: false,
  });
  const summaryData = summaryQ.data;
  const summaryStatus = summaryData?.status ?? '';
  const summaryStale =
    summaryStatus === 'ready' &&
    (summaryData?.current_episode_count ?? 0) > (summaryData?.episode_count_at_gen ?? 0);
  const newSinceGen = summaryStale
    ? summaryData!.current_episode_count! - summaryData!.episode_count_at_gen!
    : 0;

  // 课程下"哪些 episode 已有 summary"——给每集的删除按钮做 gate。
  // 一个课程一次批量查询,避免每集 N+1。
  const summaryStatusQ = useQuery({
    queryKey: ['episode-summary-status', courseId],
    queryFn: () => api.listEpisodeSummaryStatus(courseId!),
    enabled: courseId != null,
  });
  const episodesWithSummary = useMemo(
    () => new Set(summaryStatusQ.data ?? []),
    [summaryStatusQ.data],
  );

  // Course summary: trigger enqueues a generation job (async), delete removes
  // the existing summary so the next run regenerates it.
  const triggerSummaryMut = useTypedMutation({
    mutationFn: () => api.triggerCourseSummary(courseId!),
    successMsg: '已入队课程总结,生成进度见「任务队列」标签',
    invalidateKeys: [['course-summary', courseId]],
    errorMsg: '入队失败',
  });
  const deleteSummaryMut = useTypedMutation({
    mutationFn: () => api.deleteCourseSummary(courseId!),
    successMsg: '课程总结已删除',
    invalidateKeys: [['course-summary', courseId]],
    errorMsg: '删除失败',
  });
  const onDeleteCourseSummary = async () => {
    if (courseId == null) return;
    const ok = await confirm({
      message: `删除课程「${selectedCourse?.title ?? courseId}」的总结?`,
      detail: '删除后下次重新生成会从零开始。此操作不可撤销。',
      danger: true,
    });
    if (ok) deleteSummaryMut.mutate();
  };

  // Per-episode summary regen: enqueue a 'summary' job for that single
  // episode. Existing AiJob dedup on the backend handles re-enqueue races.
  const regenEpisodeMut = useTypedMutation({
    mutationFn: (episodeId: number) => api.enqueueAiJobs('summary', [episodeId]),
    successMsg: '已入队,生成进度见「任务队列」标签',
    // The jobs list rolls up status counts; invalidate so it refreshes
    // when the user opens that tab.
    invalidateKeys: [['ai-jobs']],
    errorMsg: '入队失败',
  });
  // Per-episode polish regen: re-run the subtitle polish pipeline. The typical
  // trigger is "I just accepted glossary candidates → TermDict grew → apply the
  // new terminology to this episode's already-polished subtitle". Drift-safe:
  // the backend reads RawVttContent (original whisper transcript), not the
  // current polished text, so re-polishing doesn't compound LLM changes.
  const regenPolishMut = useTypedMutation({
    mutationFn: (episodeId: number) => api.enqueueAiJobs('polish', [episodeId]),
    successMsg: '已入队润色,进度见「任务队列」标签',
    invalidateKeys: [['ai-jobs'], ['courses']],
    errorMsg: '入队失败',
  });
  const deleteEpisodeSummaryMut = useTypedMutation({
    mutationFn: (episodeId: number) => api.deleteSummary(episodeId),
    successMsg: '课时总结已删除',
    invalidateKeys: [['ai-summaries']],
    errorMsg: '删除失败',
  });
  const onDeleteEpisodeSummary = async (episodeId: number, title: string) => {
    const ok = await confirm({
      message: `删除课时「${title}」的总结?`,
      detail: '删除后下次重新生成会从零开始。',
      danger: true,
    });
    if (ok) deleteEpisodeSummaryMut.mutate(episodeId);
  };

  return (
    <section className="space-y-4 rounded-lg border border-border bg-card p-4">
      <header className="space-y-0.5">
        <h2 className="text-base font-semibold">按课程操作</h2>
        <p className="text-xs text-muted">重新生成 / 删除课程总结与课时总结。</p>
      </header>

      <div>
        <label className="mb-1 block text-xs text-muted">选择课程</label>
        <select
          className="input"
          value={courseId ?? ''}
          onChange={(e) => setCourseId(e.target.value ? Number(e.target.value) : null)}
          disabled={coursesQ.isLoading}
        >
          <option value="">{coursesQ.isLoading ? '加载中…' : '— 请选择 —'}</option>
          {courses.map((c) => (
            <option key={c.id} value={c.id}>
              {c.title}
            </option>
          ))}
        </select>
        {coursesQ.error && <p className="mt-1 text-xs text-bad">加载失败: {(coursesQ.error as Error).message}</p>}
      </div>

      {courseId != null && (
        <>
          {/* 课程总结:status 轮询(生成中)+ gate 删除(无总结不显示)+ 陈旧提示 */}
          <div className="rounded-md border border-border bg-card-2 p-3">
            <div className="mb-2 flex items-center justify-between gap-2">
              <h3 className="text-sm font-medium">课程总结</h3>
              <div className="flex items-center gap-1.5">
                <button
                  className="btn-ghost btn-sm"
                  onClick={() => triggerSummaryMut.mutate()}
                  disabled={noSubtitleAtAll || triggerSummaryMut.isPending || summaryStatus === 'generating'}
                  title={
                    noSubtitleAtAll
                      ? '该课程下没有任何字幕，无法生成总结'
                      : summaryStatus === 'ready'
                        ? '重新生成(覆盖当前总结)'
                        : '生成课程总结(异步入队)'
                  }
                >
                  {triggerSummaryMut.isPending
                    ? '提交中…'
                    : noSubtitleAtAll
                      ? '无字幕'
                      : summaryStatus === 'ready'
                        ? '重新生成'
                        : '生成总结'}
                </button>
                {/* 删除只在有总结时显示。无总结时点删除是无效的幂等 no-op,显示出来反而误导。 */}
                {summaryStatus === 'ready' && (
                  <button
                    className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                    onClick={onDeleteCourseSummary}
                    disabled={deleteSummaryMut.isPending}
                    title="删除现有课程总结(下次重新生成会从零开始)"
                  >
                    {deleteSummaryMut.isPending ? '删除中…' : '删除'}
                  </button>
                )}
              </div>
            </div>
            {summaryStatus === 'generating' ? (
              <div className="flex items-center gap-2 px-1 py-2 text-xs text-muted">
                <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-muted border-t-transparent" />
                正在生成课程总结…(agent 正在串起课时脉络,约需数十秒)
              </div>
            ) : summaryStatus === 'ready' ? (
              <div className="space-y-1">
                {summaryStale && (
                  <div className="rounded border border-warn/40 bg-warn/5 px-2 py-1 text-[11px] text-warn">
                    已新增 {newSinceGen} 节课时总结,当前总览内容已陈旧,建议重新生成。
                  </div>
                )}
                <p className="line-clamp-3 whitespace-pre-wrap text-xs leading-relaxed text-txt">
                  {summaryData?.summary_text}
                </p>
                <div className="flex flex-wrap gap-x-4 gap-y-0.5 text-[10px] text-muted">
                  {summaryData?.generated_at && (
                    <span>生成于 {new Date(summaryData.generated_at).toLocaleString('zh-CN')}</span>
                  )}
                  {summaryData?.model_used && <span>模型: {summaryData.model_used}</span>}
                  <span>基于 {summaryData?.episode_count_at_gen ?? 0} 节内容</span>
                </div>
              </div>
            ) : (
              <p className="text-[11px] text-muted">
                暂无课程总结。点击「生成总结」由 agent 串起所有课时的知识脉络。所有学生共享。
              </p>
            )}
          </div>

          {/* 课时总结列表:每集 trigger(总是可用)+ delete(只有 hasSummary 时显示) */}
          <div className="rounded-md border border-border bg-card-2 p-3">
            <div className="mb-2 flex items-center justify-between">
              <h3 className="text-sm font-medium">课时总结 ({episodes.length})</h3>
            </div>
            {episodesQ.isLoading ? (
              <div className="px-1 py-3 text-xs text-muted">加载中…</div>
            ) : episodes.length === 0 ? (
              <div className="px-1 py-3 text-xs text-muted">该课程下暂无课时。</div>
            ) : (
              <ul className="space-y-1">
                {episodes.map((ep) => {
                  const hasSummary = episodesWithSummary.has(ep.id);
                  const noSubtitle = (ep.subtitle_count ?? 0) === 0;
                  return (
                    <li
                      key={ep.id}
                      className="flex items-center justify-between gap-2 rounded-md border border-border/60 bg-card px-2.5 py-1.5"
                    >
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm text-txt">{ep.title}</div>
                        <div className="text-[10px] text-muted">
                          #{ep.id}
                          {noSubtitle ? (
                            <span className="ml-1.5 text-muted/70">· 无字幕</span>
                          ) : hasSummary ? null : (
                            <span className="ml-1.5 text-muted/70">· 未生成</span>
                          )}
                        </div>
                      </div>
                      <div className="flex flex-shrink-0 items-center gap-1">
                        <button
                          className="btn-ghost btn-sm"
                          onClick={() => regenEpisodeMut.mutate(ep.id)}
                          disabled={noSubtitle || regenEpisodeMut.isPending}
                          title={
                            noSubtitle
                              ? '该课时没有字幕，无法生成总结'
                              : hasSummary
                                ? '重新生成该课时总结(异步入队)'
                                : '生成该课时总结(异步入队)'
                          }
                        >
                          {noSubtitle ? '无字幕' : hasSummary ? '重新生成' : '生成'}
                        </button>
                        {!noSubtitle && (
                          <button
                            className="btn-ghost btn-sm"
                            onClick={() => regenPolishMut.mutate(ep.id)}
                            disabled={regenPolishMut.isPending}
                            title="重新润色该课时字幕(从原始 whisper 文本重跑,不会越润越偏)。典型场景:刚在「术语候选」接受了新术语,想应用到这节课。"
                          >
                            {regenPolishMut.isPending ? '入队中…' : '重新润色'}
                          </button>
                        )}
                        {hasSummary && (
                          <button
                            className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                            onClick={() => onDeleteEpisodeSummary(ep.id, ep.title)}
                            disabled={deleteEpisodeSummaryMut.isPending}
                            title="删除该课时总结"
                          >
                            删除
                          </button>
                        )}
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </>
      )}
    </section>
  );
}
