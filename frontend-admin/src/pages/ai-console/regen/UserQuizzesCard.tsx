// UserQuizzesCard — the 题库列表 card. Lists every AI-generated quiz for the
// selected user with 重出题 (regenerate, replaces the old quiz) and 删除.
// Both are async-enqueue; status is observable via the jobs tab.

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../../lib/api';
import { useConfirm, useToast } from '../../../lib/toast';
import { useTypedMutation } from '../../../lib/useTypedMutation';

export function UserQuizzesCard({ userId }: { userId: number }) {
  const toast = useToast();
  const confirm = useConfirm();
  const qc = useQueryClient();
  const quizzesQ = useQuery({
    queryKey: ['ai-user-quizzes', userId],
    queryFn: () => api.listUserQuizzes(userId),
  });
  const quizzes = quizzesQ.data ?? [];

  // regenerateUserQuiz reruns the quiz agent for one episode (replaces the
  // old quiz). Async — toast "已入队", status via jobs tab.
  const regenMut = useMutation({
    mutationFn: (episodeId: number) => api.regenerateUserQuiz(userId, episodeId),
    onSuccess: () => {
      toast.success('已入队重出题,生成进度见「任务队列」标签');
      qc.invalidateQueries({ queryKey: ['ai-user-quizzes', userId] });
      qc.invalidateQueries({ queryKey: ['ai-jobs'] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '入队失败'),
  });
  const delMut = useTypedMutation({
    mutationFn: (quizId: number) => api.deleteAiQuiz(quizId),
    successMsg: '题库已删除',
    invalidateKeys: [['ai-user-quizzes', userId]],
    errorMsg: '删除失败',
  });
  const onDel = async (quizId: number, title: string) => {
    const ok = await confirm({
      message: `删除题库「${title}」?`,
      detail: '将连带删除其题目、答题记录与掌握度。此操作不可撤销。',
      danger: true,
    });
    if (ok) delMut.mutate(quizId);
  };

  return (
    <div className="rounded-md border border-border bg-card-2 p-3">
      <div className="mb-2">
        <h3 className="text-sm font-medium">题库列表 ({quizzes.length})</h3>
      </div>
      {quizzesQ.isLoading ? (
        <div className="px-1 py-3 text-xs text-muted">加载中…</div>
      ) : quizzes.length === 0 ? (
        <div className="px-1 py-3 text-xs text-muted">该学生暂无题库。</div>
      ) : (
        <ul className="space-y-1">
          {quizzes.map((q) => (
            <li
              key={q.id}
              className="flex items-center justify-between gap-2 rounded-md border border-border/60 bg-card px-2.5 py-1.5"
            >
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-txt">{q.episode_title || `课时 #${q.episode_id}`}</div>
                <div className="text-[10px] text-muted">
                  题库 #{q.id} · {q.course_title || `课程 ${q.course_id}`} · {q.difficulty}
                </div>
              </div>
              <div className="flex flex-shrink-0 items-center gap-1">
                <button
                  className="btn-ghost btn-sm"
                  onClick={() => regenMut.mutate(q.episode_id)}
                  disabled={regenMut.isPending}
                  title="对这一课时重出题(替换旧题库)"
                >
                  {regenMut.isPending ? '提交中…' : '重出题'}
                </button>
                <button
                  className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                  onClick={() => onDel(q.id, q.episode_title || `#${q.episode_id}`)}
                  disabled={delMut.isPending}
                  title="删除该题库"
                >
                  {delMut.isPending ? '删除中…' : '删除'}
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
