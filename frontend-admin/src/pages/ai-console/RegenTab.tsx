import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { useToast, useConfirm } from '../../lib/toast';
import { useAiProviders } from '../../lib/useAiProviders';

// RegenTab — the "重新生成" tab on the AI Console. Two columns:
//   - 按课程操作 (left): pick a course → trigger/delete course summary,
//     list episodes with per-episode summary regen/delete.
//   - 按学生操作 (right): pick a user → 3-state study report (re-implemented
//     from AIUserView's UserStudyReportSection so we can add a delete button
//     without refactoring AIUserView), 3-scope advice (episode/course/subject),
//     and the user's quiz list with regen/delete per row.
//
// AI附加层原则: if no chat provider is configured (the global "AI is on"
// signal), the WHOLE tab shows a placeholder pointing to the Provider tab.
// AI is an opt-in add-on — without a chat backend, none of these actions
// would produce anything.

export function RegenTab() {
  const providersQ = useAiProviders();
  // "Configured" = at least one enabled chat provider. This is the same
  // signal AiProvidersSection acts on (it only manages chat). Embedding
  // models are auto-seeded and never user-toggleable, so chat is the gate.
  const configured = useMemo(() => {
    const list = providersQ.data ?? [];
    return list.some((p) => p.capability === 'chat' && p.is_enabled);
  }, [providersQ.data]);

  if (providersQ.isLoading) {
    return (
      <div className="rounded-lg border border-border bg-card px-4 py-10 text-center text-sm text-muted">加载中…</div>
    );
  }
  if (!configured) {
    return (
      <div className="rounded-lg border border-dashed border-warn/40 bg-warn/5 px-4 py-10 text-center">
        <p className="text-sm font-medium text-warn">AI 未配置</p>
        <p className="mt-1 text-xs text-muted">请到「Provider」标签配置聊天模型后重试。AI 是附加层,未配置时无法重新生成内容。</p>
      </div>
    );
  }
  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <CourseRegenColumn />
      <UserRegenColumn />
    </div>
  );
}

// ============================================================
// Left column: course-scoped regen
// ============================================================

function CourseRegenColumn() {
  const toast = useToast();
  const confirm = useConfirm();
  const qc = useQueryClient();
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

  // Course summary: trigger enqueues a generation job (async), delete removes
  // the existing summary so the next run regenerates it. NO status polling —
  // the api.ts file has no GET course-summary endpoint, and the spec's
  // escape hatch is to skip polling entirely. The jobs tab is where the
  // admin watches progress.
  const triggerSummaryMut = useMutation({
    mutationFn: () => api.triggerCourseSummary(courseId!),
    onSuccess: () => {
      toast.success('已入队课程总结,生成进度见「任务队列」标签');
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '入队失败'),
  });
  const deleteSummaryMut = useMutation({
    mutationFn: () => api.deleteCourseSummary(courseId!),
    onSuccess: () => {
      toast.success('课程总结已删除');
      qc.invalidateQueries({ queryKey: ['course-summary', courseId] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '删除失败'),
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
  const regenEpisodeMut = useMutation({
    mutationFn: (episodeId: number) => api.enqueueAiJobs('summary', [episodeId]),
    onSuccess: (_d, _episodeId) => {
      toast.success('已入队,生成进度见「任务队列」标签');
      // The jobs list rolls up status counts; invalidate so it refreshes
      // when the user opens that tab.
      qc.invalidateQueries({ queryKey: ['ai-jobs'] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '入队失败'),
  });
  const deleteEpisodeSummaryMut = useMutation({
    mutationFn: (episodeId: number) => api.deleteSummary(episodeId),
    onSuccess: () => {
      toast.success('课时总结已删除');
      qc.invalidateQueries({ queryKey: ['ai-summaries'] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '删除失败'),
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
          {/* 课程总结:仅 trigger + delete,无 status 轮询(api.ts 无 GET course-summary) */}
          <div className="rounded-md border border-border bg-card-2 p-3">
            <div className="mb-2 flex items-center justify-between gap-2">
              <h3 className="text-sm font-medium">课程总结</h3>
              <div className="flex items-center gap-1.5">
                <button
                  className="btn-ghost btn-sm"
                  onClick={() => triggerSummaryMut.mutate()}
                  disabled={triggerSummaryMut.isPending}
                  title="重新生成该课程的跨课时总结(异步入队)"
                >
                  {triggerSummaryMut.isPending ? '提交中…' : '重新生成'}
                </button>
                <button
                  className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                  onClick={onDeleteCourseSummary}
                  disabled={deleteSummaryMut.isPending}
                  title="删除现有课程总结(下次重新生成会从零开始)"
                >
                  {deleteSummaryMut.isPending ? '删除中…' : '删除'}
                </button>
              </div>
            </div>
            <p className="text-[11px] text-muted">
              课程总结是该课程所有课时汇总后的整体总结,所有学生共享。生成进度在「任务队列」标签查看。
            </p>
          </div>

          {/* 课时总结列表:每行 trigger + delete,不做 per-episode status 轮询(开销过大) */}
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
                {episodes.map((ep) => (
                  <li
                    key={ep.id}
                    className="flex items-center justify-between gap-2 rounded-md border border-border/60 bg-card px-2.5 py-1.5"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm text-txt">{ep.title}</div>
                      <div className="text-[10px] text-muted">#{ep.id}</div>
                    </div>
                    <div className="flex flex-shrink-0 items-center gap-1">
                      <button
                        className="btn-ghost btn-sm"
                        onClick={() => regenEpisodeMut.mutate(ep.id)}
                        disabled={regenEpisodeMut.isPending}
                        title="重新生成该课时总结(异步入队)"
                      >
                        重新生成
                      </button>
                      <button
                        className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                        onClick={() => onDeleteEpisodeSummary(ep.id, ep.title)}
                        disabled={deleteEpisodeSummaryMut.isPending}
                        title="删除该课时总结"
                      >
                        删除
                      </button>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </>
      )}
    </section>
  );
}

// ============================================================
// Right column: user-scoped regen
// ============================================================

function UserRegenColumn() {
  const usersQ = useQuery({ queryKey: ['users'], queryFn: api.listUsers });
  const users = usersQ.data ?? [];
  const [userId, setUserId] = useState<number | null>(null);
  const [query, setQuery] = useState('');
  const selectedUser = useMemo(() => users.find((u) => u.id === userId) ?? null, [users, userId]);

  return (
    <section className="space-y-4 rounded-lg border border-border bg-card p-4">
      <header className="space-y-0.5">
        <h2 className="text-base font-semibold">按学生操作</h2>
        <p className="text-xs text-muted">重新生成 / 删除学习报告、学习建议、题库。</p>
      </header>

      {/* 用户选择 —— 复用 AIUserView 的 datalist + "(#id)" 解析模式
          (nickname 可能重复,不能用 find 反查)。 */}
      <div>
        <label className="mb-1 block text-xs text-muted">选择学生</label>
        <input
          className="input max-w-[260px]"
          list="regen-user-options"
          placeholder={usersQ.isLoading ? '加载用户…' : '搜索昵称选择用户'}
          value={selectedUser ? `${selectedUser.nickname} (#${selectedUser.id})` : query}
          onChange={(e) => {
            const entered = e.target.value;
            const idMatch = entered.match(/\(#(\d+)\)\s*$/);
            if (idMatch) {
              const id = Number(idMatch[1]);
              if (users.some((u) => u.id === id)) {
                setUserId(id);
                setQuery('');
                return;
              }
            }
            setUserId(null);
            setQuery(entered);
          }}
        />
        <datalist id="regen-user-options">
          {users
            .filter((u) => (query ? u.nickname.toLowerCase().includes(query.toLowerCase()) : true))
            .map((u) => (
              <option key={u.id} value={`${u.nickname} (#${u.id})`}>
                {u.role}
              </option>
            ))}
        </datalist>
      </div>

      {userId != null ? (
        <>
          <UserStudyReportCard userId={userId} />
          <UserAdviceCard userId={userId} />
          <UserQuizzesCard userId={userId} />
        </>
      ) : (
        <div className="rounded-md border border-dashed border-border bg-card-2 px-4 py-8 text-center text-sm text-muted">
          选择一个学生以操作其 AI 数据。
        </div>
      )}
    </section>
  );
}

// ---- 学习报告 (3-state, re-implemented from AIUserView so we can add 删除) ----

function UserStudyReportCard({ userId }: { userId: number }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const reportQ = useQuery({
    queryKey: ['ai-user-study-report', userId],
    queryFn: () => api.getUserReport(userId),
    refetchInterval: (q) => (q.state.data?.status === 'generating' ? 3000 : false),
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

  const delMut = useMutation({
    mutationFn: () => api.deleteUserStudyReport(userId),
    onSuccess: () => {
      toast.success('学习报告已删除');
      qc.invalidateQueries({ queryKey: ['ai-user-study-report', userId] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '删除失败'),
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
      <div className="mb-2 flex items-center justify-between gap-2">
        <h3 className="text-sm font-medium">学习报告</h3>
        <div className="flex items-center gap-1.5">
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
          <p className="whitespace-pre-wrap text-sm leading-relaxed text-txt">{data.report}</p>
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

// ---- 学习建议 (3 scopes: episode / course / subject) ----

function UserAdviceCard({ userId }: { userId: number }) {
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
  const delMut = useMutation({
    mutationFn: (args: { scope: 'episode' | 'course' | 'subject'; scopeId: number }) =>
      api.deleteUserAdvice(userId, args.scope, args.scopeId),
    onSuccess: () => {
      toast.success('建议已删除');
      qc.invalidateQueries({ queryKey: ['ai-user-advice', userId] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '删除失败'),
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

function ScopeRow({
  title,
  select,
  onRegen,
  onDel,
  canAct,
  regenPending,
  delPending,
}: {
  title: string;
  select: React.ReactNode;
  onRegen: () => void;
  onDel: () => void;
  canAct: boolean;
  regenPending: boolean;
  delPending: boolean;
}) {
  return (
    <div className="rounded-md border border-border/60 bg-card px-2.5 py-1.5">
      <div className="mb-1 text-[11px] font-medium text-muted">{title}</div>
      <div className="flex flex-wrap items-center justify-between gap-1.5">
        <div className="min-w-0 flex-1">{select}</div>
        <div className="flex flex-shrink-0 items-center gap-1">
          <button
            className="btn-ghost btn-sm"
            onClick={onRegen}
            disabled={!canAct || regenPending}
            title="重新生成(异步入队)"
          >
            {regenPending ? '提交中…' : '重新生成'}
          </button>
          <button
            className="btn-ghost btn-sm text-bad hover:bg-bad/10"
            onClick={onDel}
            disabled={!canAct || delPending}
            title="删除现有建议"
          >
            {delPending ? '删除中…' : '删除'}
          </button>
        </div>
      </div>
    </div>
  );
}

function scopeLabel(scope: 'episode' | 'course' | 'subject'): string {
  return scope === 'episode' ? '课时' : scope === 'course' ? '课程' : '学科';
}

// ---- 题库列表 ----

function UserQuizzesCard({ userId }: { userId: number }) {
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
  const delMut = useMutation({
    mutationFn: (quizId: number) => api.deleteAiQuiz(quizId),
    onSuccess: () => {
      toast.success('题库已删除');
      qc.invalidateQueries({ queryKey: ['ai-user-quizzes', userId] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '删除失败'),
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
