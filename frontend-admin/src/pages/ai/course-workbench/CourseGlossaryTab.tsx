// CourseGlossaryTab — 课程工作台「术语」tab:术语审核 + "应用到本课字幕"
// (批量重新润色)聚合到同一页——接受完术语想更新字幕,就地一键润色,不跨 tab。
//
// 这里把 GlossaryTab(术语审核)和"应用到本课字幕"(批量重新润色)聚合到同一页:
// admin 接受完术语后,顶部出现"一键应用到本课字幕"按钮,直接批量入队 polish job,
// 不用再跨 tab。这是"对象即导航"重构的核心价值——一个对象的关联动作在同一上下文完成。
import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Sparkles } from 'lucide-react';
import { api } from '../../../lib/api';
import { useToast } from '../../../lib/toast';
import { useTypedMutation } from '../../../lib/useTypedMutation';
import { GlossaryTab } from '../../ai-console/GlossaryTab';

export function CourseGlossaryTab({ courseId }: { courseId: number }) {
  const toast = useToast();
  // 本课所有有字幕的课时——一键应用时给 polish job 用。无字幕的课时跳过(润色无意义)。
  const episodesQ = useQuery({
    queryKey: ['course-episodes', courseId],
    queryFn: () => api.listEpisodes(courseId),
  });
  const subtitleEpisodeIds = useMemo(
    () => (episodesQ.data ?? []).filter((ep) => (ep.subtitle_count ?? 0) > 0).map((ep) => ep.id),
    [episodesQ.data],
  );

  // 一键应用:对本课所有有字幕的课时批量入队 polish job。后端 polish 从原始 whisper
  // 文本重跑(不基于已润色文本),所以重复润色不会"越润越偏",接受的新术语会被应用。
  const [justApplied, setJustApplied] = useState(false);
  const applyMut = useTypedMutation({
    mutationFn: () => api.enqueueAiJobs('polish', subtitleEpisodeIds),
    onSuccess: (data) => {
      const enq = data.enqueued?.length ?? 0;
      const skp = Object.keys(data.skipped ?? {}).length;
      toast.success(
        skp > 0
          ? `已入队 ${enq} 节重新润色,跳过 ${skp} 节(进度见「任务队列」)`
          : `已入队 ${enq} 节重新润色(进度见「任务队列」)`,
      );
      setJustApplied(true);
    },
    errorMsg: '入队失败',
  });

  return (
    <div className="space-y-4">
      {/* 一键应用行动条:接受术语后就地应用,不跨 tab。
          解释清楚为什么需要这一步(术语进了字典,但已润色的字幕不会自动更新)+
          一键触发的后果(从原始 whisper 重跑,不会越润越偏)。 */}
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-primary/30 bg-primary/5 p-3">
        <div className="min-w-0">
          <div className="flex items-center gap-1.5 text-sm font-medium text-txt">
            <Sparkles size={14} className="text-primary" />
            接受术语后,应用到本课字幕
          </div>
          <p className="mt-0.5 text-[11px] text-muted">
            接受的术语进了课程字典,但已润色的字幕不会自动更新。点这里对本课 {subtitleEpisodeIds.length} 节有字幕的课时批量重新润色(从原始 whisper 文本重跑,不会越润越偏)。
          </p>
        </div>
        <button
          className="btn-primary btn-sm inline-flex shrink-0 items-center gap-1.5"
          onClick={() => applyMut.mutate()}
          disabled={applyMut.isPending || subtitleEpisodeIds.length === 0}
          title={subtitleEpisodeIds.length === 0 ? '本课没有带字幕的课时' : '批量入队重新润色'}
        >
          {applyMut.isPending
            ? '入队中…'
            : justApplied
              ? '再次应用'
              : <><Sparkles size={14} /> 一键应用到本课字幕</>}
        </button>
      </div>

      {/* 术语审核主体:复用 GlossaryTab,传入 courseId 进入"课程已固定"模式。
          key=courseId 强制 remount:可选 prop 锁定模式下,同路由切课程(Router 复用
          组件实例)会让 useState 残留旧值,加 key 保证 state 跟着 courseId 重置。 */}
      <GlossaryTab key={courseId} courseId={courseId} />
    </div>
  );
}
