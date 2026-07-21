// UserAdviceCard — the 学习建议 card. Three independent scopes (episode /
// course / subject) each with regen + delete. Regen enqueues an async job
// (status observable via the jobs tab); delete removes the existing advice.
//
// All three scopes share one regen mutation + one delete mutation (keyed by
// the {scope, scopeId} arg) — they hit the same backend endpoints with a
// scope discriminator, so there's no benefit to splitting them.

import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../../lib/api';
import { useConfirm, useToast } from '../../../lib/toast';
import { useTypedMutation } from '../../../lib/useTypedMutation';
import { ScopeRow, scopeLabel } from './ScopeRow';

export function UserAdviceCard({ userId }: { userId: number }) {
  const toast = useToast();
  const confirm = useConfirm();
  const qc = useQueryClient();
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const subjectsQ = useQuery({ queryKey: ['subjects'], queryFn: api.listSubjects });
  const courses = coursesQ.data ?? [];
  const subjects = subjectsQ.data ?? [];

  const [courseId, setCourseId] = useState<number | null>(null);
  const [episodeId, setEpisodeId] = useState<number | null>(null);
  const [adviceCourseId, setAdviceCourseId] = useState<number | null>(null);
  const [subjectId, setSubjectId] = useState<number | null>(null);

  const episodesQ = useQuery({
    queryKey: ['course-episodes', courseId],
    queryFn: () => api.listEpisodes(courseId!),
    enabled: courseId != null,
  });
  const episodes = episodesQ.data ?? [];

  // 3 independent mutations (one per scope). All async-enqueue on success —
  // advice generation status is observable via the jobs tab, not polled here.
  const regenMut = useMutation({
    mutationFn: (args: { scope: 'episode' | 'course' | 'subject'; scopeId: number }) =>
      api.regenerateUserAdvice(userId, args.scope, args.scopeId),
    onSuccess: (_d, vars) => {
      toast.success(`已入队 ${scopeLabel(vars.scope)}建议,生成进度见「任务队列」标签`);
      qc.invalidateQueries({ queryKey: ['ai-user-advice', userId] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '入队失败'),
  });
  const delMut = useTypedMutation({
    mutationFn: (args: { scope: 'episode' | 'course' | 'subject'; scopeId: number }) =>
      api.deleteUserAdvice(userId, args.scope, args.scopeId),
    successMsg: '建议已删除',
    invalidateKeys: [['ai-user-advice', userId]],
    errorMsg: '删除失败',
  });

  const onRegen = async (scope: 'episode' | 'course' | 'subject', scopeId: number | null) => {
    if (scopeId == null) {
      toast.error('请先选择目标');
      return;
    }
    regenMut.mutate({ scope, scopeId });
  };
  const onDel = async (scope: 'episode' | 'course' | 'subject', scopeId: number | null) => {
    if (scopeId == null) {
      toast.error('请先选择目标');
      return;
    }
    const ok = await confirm({
      message: `删除该学生的 ${scopeLabel(scope)}建议?`,
      detail: '删除后可重新生成。',
      danger: true,
    });
    if (ok) delMut.mutate({ scope, scopeId });
  };

  return (
    <div className="rounded-md border border-border bg-card-2 p-3">
      <div className="mb-2">
        <h3 className="text-sm font-medium">学习建议</h3>
        <p className="text-[11px] text-muted">按 scope 重跑 / 删除。生成进度在「任务队列」标签。</p>
      </div>
      <div className="space-y-2">
        {/* episode scope: 先选课程再选课时 */}
        <ScopeRow
          title="课时建议"
          select={
            <div className="flex flex-wrap items-center gap-1.5">
              <select
                className="input !py-1 !text-xs max-w-[140px]"
                value={courseId ?? ''}
                onChange={(e) => {
                  const id = e.target.value ? Number(e.target.value) : null;
                  setCourseId(id);
                  setEpisodeId(null);
                }}
              >
                <option value="">选课程…</option>
                {courses.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.title}
                  </option>
                ))}
              </select>
              <select
                className="input !py-1 !text-xs max-w-[160px]"
                value={episodeId ?? ''}
                onChange={(e) => setEpisodeId(e.target.value ? Number(e.target.value) : null)}
                disabled={courseId == null || episodesQ.isLoading}
              >
                <option value="">{episodesQ.isLoading ? '加载…' : '选课时…'}</option>
                {episodes.map((ep) => (
                  <option key={ep.id} value={ep.id}>
                    {ep.title}
                  </option>
                ))}
              </select>
            </div>
          }
          onRegen={() => onRegen('episode', episodeId)}
          onDel={() => onDel('episode', episodeId)}
          canAct={episodeId != null && !regenMut.isPending && !delMut.isPending}
          regenPending={regenMut.isPending}
          delPending={delMut.isPending}
        />

        {/* course scope */}
        <ScopeRow
          title="课程建议"
          select={
            <select
              className="input !py-1 !text-xs max-w-[220px]"
              value={adviceCourseId ?? ''}
              onChange={(e) => setAdviceCourseId(e.target.value ? Number(e.target.value) : null)}
            >
              <option value="">选课程…</option>
              {courses.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.title}
                </option>
              ))}
            </select>
          }
          onRegen={() => onRegen('course', adviceCourseId)}
          onDel={() => onDel('course', adviceCourseId)}
          canAct={adviceCourseId != null && !regenMut.isPending && !delMut.isPending}
          regenPending={regenMut.isPending}
          delPending={delMut.isPending}
        />

        {/* subject scope */}
        <ScopeRow
          title="学科建议"
          select={
            <select
              className="input !py-1 !text-xs max-w-[220px]"
              value={subjectId ?? ''}
              onChange={(e) => setSubjectId(e.target.value ? Number(e.target.value) : null)}
            >
              <option value="">选学科…</option>
              {subjects.map((s) => (
                <option key={s.id ?? s.key} value={s.id ?? ''}>
                  {s.label}（{s.key}）
                </option>
              ))}
            </select>
          }
          onRegen={() => onRegen('subject', subjectId)}
          onDel={() => onDel('subject', subjectId)}
          canAct={subjectId != null && !regenMut.isPending && !delMut.isPending}
          regenPending={regenMut.isPending}
          delPending={delMut.isPending}
        />
      </div>
    </div>
  );
}
