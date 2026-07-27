// UserStudyReportCard — 3-state study-report card (re-implemented from
// AIUserView so we can add a 删除 button without refactoring AIUserView).
// ready / generating / '' (not-yet-generated). Generating polls automatically.
//
// The trigger is a bespoke (non-useTypedMutation) flow because it optimistically
// flips the cache to 'generating' before the network call resolves, so the
// spinner appears instantly and the next poll calibrates against the server.

import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../../lib/api';
import { MiniMarkdown } from '../../../components/ai/MiniMarkdown';
import { useConfirm, useToast } from '../../../lib/toast';
import { pollWhileGenerating } from '../../../lib/query';
import { useTypedMutation } from '../../../lib/useTypedMutation';

export function UserStudyReportCard({ userId }: { userId: number }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const reportQ = useQuery({
    queryKey: ['ai-user-study-report', userId],
    queryFn: () => api.getUserReport(userId),
    refetchInterval: pollWhileGenerating(),
    refetchIntervalInBackground: false,
  });
  const data = reportQ.data;
  const generating = data?.status === 'generating';
  const [triggering, setTriggering] = useState(false);

  const trigger = async () => {
    setTriggering(true);
    try {
      // Optimistically mark generating so the spinner appears immediately;
      // the next poll calibrates against the server's real status.
      qc.setQueryData(['ai-user-study-report', userId], { status: 'generating' });
      await api.triggerUserReport(userId);
      await reportQ.refetch();
    } catch {
      qc.invalidateQueries({ queryKey: ['ai-user-study-report', userId] });
      toast.error('触发失败,请重试');
    } finally {
      setTriggering(false);
    }
  };

  const delMut = useTypedMutation({
    mutationFn: () => api.deleteUserStudyReport(userId),
    successMsg: '学习报告已删除',
    invalidateKeys: [['ai-user-study-report', userId]],
    errorMsg: '删除失败',
  });
  const onDelete = async () => {
    const ok = await confirm({
      message: '删除该学生的学习报告?',
      detail: '删除后可重新生成。此操作不可撤销。',
      danger: true,
    });
    if (ok) delMut.mutate();
  };

  return (
    <div className="rounded-md border border-border bg-card-2 p-3">
      <div className="mb-2 flex items-start justify-between gap-2">
        <div>
          <h3 className="text-sm font-medium">学习报告</h3>
          {/* 跨课程说明:报告是该生所有授权课程的综合画像(agent 逐课程分析 mastery/总结/
              建议后综合),不需要也不应该选课程——选了反而以偏概全。说明这点消除"没选课给谁生成"
              的困惑。 */}
          <p className="mt-0.5 text-[11px] text-muted">跨该生所有课程的综合画像(agent 逐课程分析后综合),无需选课。</p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          <button
            className="btn-ghost btn-sm"
            onClick={trigger}
            disabled={generating || triggering}
            title={data?.status === 'ready' ? '重新生成(覆盖当前报告)' : '生成学习报告'}
          >
            {triggering ? '提交中…' : data?.status === 'ready' ? '重新生成' : '生成学习报告'}
          </button>
          {data?.status === 'ready' && (
            <button
              className="btn-ghost btn-sm text-bad hover:bg-bad/10"
              onClick={onDelete}
              disabled={delMut.isPending}
              title="删除现有学习报告"
            >
              {delMut.isPending ? '删除中…' : '删除'}
            </button>
          )}
        </div>
      </div>
      {generating ? (
        <div className="flex items-center gap-2 px-1 py-4 text-xs text-muted">
          <span className="h-4 w-4 animate-spin rounded-full border-2 border-muted border-t-transparent" />
          正在生成学习报告…(agent 正在跨课程分析,约需数十秒)
        </div>
      ) : data?.status === 'ready' && data.report ? (
        <div className="space-y-1.5">
          <MiniMarkdown text={data.report} />
          <div className="flex flex-wrap gap-x-4 gap-y-0.5 text-[11px] text-muted">
            {data.generated_at && <span>生成于 {new Date(data.generated_at).toLocaleString('zh-CN')}</span>}
            {data.model_used && <span>模型: {data.model_used}</span>}
          </div>
        </div>
      ) : (
        <div className="px-1 py-4 text-xs text-muted">
          {reportQ.isLoading ? '加载中…' : '暂无学习报告。点击「生成学习报告」由 agent 分析该学生跨课程的学习情况。'}
        </div>
      )}
    </div>
  );
}
