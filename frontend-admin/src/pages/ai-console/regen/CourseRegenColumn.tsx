// CourseRegenColumn — the left column of RegenTab. Pick a course → trigger /
// delete the course summary (3-state: ready / generating / ''), and list every
// episode with per-episode summary regen / re-polish / delete actions.
//
// v2 重构(2026-07-26):勾选式批量操作 + 作业并入。
//   - 每行前加 checkbox(无字幕禁用),Set<number> 跟踪选中(抄 GlossaryTab 范式)。
//   - 顶部批量操作区(selected.size>0 显示):「生成总结」「生成作业」「重新润色」三按钮,
//     调 enqueueAiJobs('summary'/'homework'/'polish', [...selected])。
//   - 全选/清空(抄 CourseTree.tsx 范式)。
//   - 每行右侧保留现有 3 按钮(生成/重新生成、重新润色、删除)+ hasHomework 时加
//     「查看作业」按钮 → 打开 HomeworkPreviewModal。勾选=批量 + 行内=单条两套并存。
//   - 每行显示字幕状态(沿用 ep.subtitle_count 「· 无字幕」)+ hover tooltip(原生 title=)。
//
// Subtitle gate: a course (or episode) with NO subtitles can't get a summary or
// homework — the agent runs over the polished subtitle text, so the trigger is
// disabled and labelled 无字幕. The `episode-summary-status` bulk query (one
// call per course, not N+1) tells us which episodes already have summaries so
// the delete button only shows where it can act. Homework status maps the same
// way from homeworkList → Set<episode_id>.

import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { CheckSquare, Eye, FileText } from 'lucide-react';
import { api } from '../../../lib/api';
import { useConfirm, useToast } from '../../../lib/toast';
import { pollWhileGenerating } from '../../../lib/query';
import { useTypedMutation } from '../../../lib/useTypedMutation';
import { useSubjects } from '../../../lib/useSubjects';
import { HomeworkPreviewModal } from '../../../components/homework/HomeworkPreviewModal';

export function CourseRegenColumn() {
  const confirm = useConfirm();
  const toast = useToast();
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
  // v2:subject key → label 映射(后端 Course.subject 返回的是 key 如 "math",
  // 卷头要显示中文 label 如「数学」)。useSubjects 一次拉全量 subject 列表,本地建 map。
  const subjectsQ = useSubjects();
  const subjectLabelByKey = useMemo(() => {
    const m = new Map<string, string>();
    for (const s of subjectsQ.data ?? []) m.set(s.key, s.label);
    return m;
  }, [subjectsQ.data]);
  const selectedSubjectLabel = selectedCourse?.subject
    ? subjectLabelByKey.get(selectedCourse.subject) ?? selectedCourse.subject
    : undefined;
  // 课程下是否有任何字幕——给课程总结按钮做 gate(无字幕则禁用)。
  const noSubtitleAtAll = episodes.length > 0 && episodes.every((ep) => (ep.subtitle_count ?? 0) === 0);

  // v2:勾选式 Set<number>(抄 GlossaryTab L36)。
  const [selected, setSelected] = useState<Set<number>>(new Set());
  // v2:作业预览弹窗状态。
  const [previewHomeworkId, setPreviewHomeworkId] = useState<number | null>(null);
  const toggleSelect = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const selectAll = () => {
    // 只选有字幕的(无字幕的没法操作)。
    const eligible = episodes.filter((ep) => (ep.subtitle_count ?? 0) > 0).map((ep) => ep.id);
    setSelected(new Set(eligible));
  };

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

  // v2:作业列表(整门课),映射成 Set<episode_id> 给每行的「查看作业」按钮做 gate。
  // 照抄 episodesWithSummary 的范式(本文件 L54-62)。
  const homeworksQ = useQuery({
    queryKey: ['homeworks', courseId],
    queryFn: () => api.homeworkList(courseId!),
    enabled: courseId != null,
  });
  // episode_id → homework.id 的映射(行内「查看作业」按钮要传 homeworkId 给 Modal)。
  const homeworkIdByEpisode = useMemo(() => {
    const map = new Map<number, number>();
    // 只取 active 状态的作业(archived 是历史版本,不暴露给预览)。
    for (const h of homeworksQ.data ?? []) {
      if (h.status === 'active') map.set(h.episode_id, h.id);
    }
    return map;
  }, [homeworksQ.data]);

  // v2:homeworkId → episode title 反查(卷头主标题用)。Modal 打开时给卷头显示课时名,
  // 不传的话 HomeworkPrintView 兜底「课后作业」。homeworkId → episode_id → episode.title
  // 两层映射,用 useMemo 避免每次 render 都 find。
  const previewEpisodeTitle = useMemo(() => {
    if (previewHomeworkId == null) return undefined;
    const hw = (homeworksQ.data ?? []).find((h) => h.id === previewHomeworkId);
    if (!hw) return undefined;
    return episodes.find((ep) => ep.id === hw.episode_id)?.title;
  }, [previewHomeworkId, homeworksQ.data, episodes]);

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

  // v2:批量入队 mutation(总结/作业/润色 三按钮共用)。成功后清空 selected + 弹带跳过数的 toast。
  // successMsg 只支持静态字符串,动态计数走 onSuccess 自己 toast(toast 在组件顶层 hooks 区取)。
  const batchEnqueueMut = useTypedMutation({
    mutationFn: (jobType: 'summary' | 'homework' | 'polish') =>
      api.enqueueAiJobs(jobType, Array.from(selected)),
    invalidateKeys: [['ai-jobs'], ['homeworks', courseId]],
    errorMsg: '入队失败',
    onSuccess: (data) => {
      const enq = data.enqueued?.length ?? 0;
      const skp = Object.keys(data.skipped ?? {}).length;
      toast.success(
        skp > 0 ? `已入队 ${enq} 项,跳过 ${skp} 项(进度见「任务队列」)` : `已入队 ${enq} 项(进度见「任务队列」)`,
      );
      setSelected(new Set());
    },
  });

  return (
    <section className="space-y-4 rounded-lg border border-border bg-card p-4">
      <header className="space-y-0.5">
        <h2 className="text-base font-semibold">按课程操作</h2>
        <p className="text-xs text-muted">勾选课时批量生成总结/作业,或单行操作。</p>
      </header>

      <div>
        <label className="mb-1 block text-xs text-muted">选择课程</label>
        <select
          className="input"
          value={courseId ?? ''}
          onChange={(e) => {
            setCourseId(e.target.value ? Number(e.target.value) : null);
            setSelected(new Set()); // v2:切课程时清空选中(抄 GlossaryTab)
          }}
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

          {/* v2:批量操作区(selected.size>0 时显示)。抄 GlossaryTab L103-148 范式。 */}
          <div className="rounded-md border border-primary/40 bg-primary/5 p-3">
            <div className="mb-2 flex items-center justify-between">
              <h3 className="text-sm font-medium">
                课时操作 ({episodes.length})
                {selected.size > 0 && <span className="ml-2 text-xs text-primary">已选 {selected.size}</span>}
              </h3>
              {episodes.length > 0 && (
                <div className="flex items-center gap-2 text-xs">
                  <button className="btn-ghost btn-sm" onClick={selectAll} disabled={noSubtitleAtAll}>
                    全选有字幕
                  </button>
                  {selected.size > 0 && (
                    <button className="btn-ghost btn-sm" onClick={() => setSelected(new Set())}>
                      清空
                    </button>
                  )}
                </div>
              )}
            </div>

            {selected.size > 0 && (
              <div className="mb-3 flex flex-wrap gap-2 border-b border-border pb-3">
                <button
                  className="btn-primary btn-sm inline-flex items-center gap-1.5"
                  onClick={() => batchEnqueueMut.mutate('summary')}
                  disabled={batchEnqueueMut.isPending}
                  title="为选中课时批量生成/重新生成总结"
                >
                  <CheckSquare size={14} />
                  {batchEnqueueMut.isPending ? '入队中…' : `生成总结 (${selected.size})`}
                </button>
                <button
                  className="btn-primary btn-sm inline-flex items-center gap-1.5"
                  onClick={() => batchEnqueueMut.mutate('homework')}
                  disabled={batchEnqueueMut.isPending}
                  title="为选中课时批量生成/重新生成课后作业卷"
                >
                  <FileText size={14} />
                  {batchEnqueueMut.isPending ? '入队中…' : `生成作业 (${selected.size})`}
                </button>
                <button
                  className="btn-ghost btn-sm inline-flex items-center gap-1.5"
                  onClick={() => batchEnqueueMut.mutate('polish')}
                  disabled={batchEnqueueMut.isPending}
                  title="为选中课时批量重新润色字幕(从原始 whisper 文本重跑)"
                >
                  {batchEnqueueMut.isPending ? '入队中…' : `重新润色 (${selected.size})`}
                </button>
              </div>
            )}

            {episodesQ.isLoading ? (
              <div className="px-1 py-3 text-xs text-muted">加载中…</div>
            ) : episodes.length === 0 ? (
              <div className="px-1 py-3 text-xs text-muted">该课程下暂无课时。</div>
            ) : (
              <ul className="space-y-1">
                {episodes.map((ep) => {
                  const hasSummary = episodesWithSummary.has(ep.id);
                  const hwId = homeworkIdByEpisode.get(ep.id);
                  const hasHomework = hwId != null;
                  const noSubtitle = (ep.subtitle_count ?? 0) === 0;
                  const isSel = selected.has(ep.id);
                  return (
                    <li
                      key={ep.id}
                      className={`flex items-center gap-2 rounded-md border px-2.5 py-1.5 ${
                        isSel ? 'border-primary bg-primary/5' : 'border-border/60 bg-card'
                      }`}
                    >
                      {/* v2:行前 checkbox(无字幕禁用)。抄 GlossaryTab L231-239 + CourseTree L94-101。 */}
                      <input
                        type="checkbox"
                        className="h-4 w-4 flex-shrink-0 accent-primary"
                        checked={isSel}
                        onChange={() => toggleSelect(ep.id)}
                        disabled={noSubtitle}
                        aria-label={`选择课时 ${ep.title}`}
                      />
                      {/* v2:课时缩略图(cover_url 有则显示,无则灰底占位)。
                          来自视频首帧,16:9 小缩略图让 admin 一眼认出是哪节课。 */}
                      {ep.cover_url ? (
                        <img
                          src={ep.cover_url}
                          alt=""
                          className="h-9 w-16 flex-shrink-0 rounded bg-card-2 object-cover"
                          loading="lazy"
                          onError={(e) => {
                            // cover_url 无效(404/签名过期)时,把 img 藏掉露出一行
                            // 占位的灰底(collapse 到无封面态)。避免破图图标。
                            e.currentTarget.style.visibility = 'hidden';
                          }}
                        />
                      ) : (
                        <div className="flex h-9 w-16 flex-shrink-0 items-center justify-center rounded bg-card-2 text-[9px] text-muted/60">
                          无封面
                        </div>
                      )}
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm text-txt">{ep.title}</div>
                        <div className="text-[10px] text-muted">
                          #{ep.id}
                          {noSubtitle ? (
                            <span className="ml-1.5 text-muted/70">· 无字幕</span>
                          ) : (
                            <>
                              {!hasSummary && <span className="ml-1.5 text-muted/70">· 未生成总结</span>}
                              {!hasHomework && <span className="ml-1.5 text-muted/70">· 未生成作业</span>}
                            </>
                          )}
                        </div>
                      </div>
                      <div className="flex flex-shrink-0 items-center gap-1">
                        {/* v2:hasHomework 时加「查看作业」按钮 → 打开 Modal */}
                        {hasHomework && (
                          <button
                            className="btn-ghost btn-sm"
                            onClick={() => setPreviewHomeworkId(hwId!)}
                            title="预览/打印该课时作业卷"
                          >
                            <Eye size={13} />
                            查看作业
                          </button>
                        )}
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
                          {noSubtitle ? '无字幕' : hasSummary ? '重生总结' : '生总结'}
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

      {/* v2:作业预览弹窗。卷头需要课程名/学科/课时标题,从 selectedCourse/episodes 找。
          episodeTitle 用 previewEpisodeTitle useMemo(homeworkId → episode.title 反查)。 */}
      <HomeworkPreviewModal
        open={previewHomeworkId != null}
        onClose={() => setPreviewHomeworkId(null)}
        homeworkId={previewHomeworkId}
        courseTitle={selectedCourse?.title}
        subjectLabel={selectedSubjectLabel}
        episodeTitle={previewEpisodeTitle}
      />
    </section>
  );
}
